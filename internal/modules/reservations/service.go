package reservations

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"ticket-reservation-go-lab/internal/db"
	"ticket-reservation-go-lab/internal/sqlc"
)

const (
	StatusReserved          = "reserved"
	StatusConfirmed         = "confirmed"
	StatusCancelled         = "cancelled"
	StatusExpired           = "expired"
	ReservationTypeQuantity = "quantity"
	ReservationTypeSeats    = "seats"
)

var (
	ErrInvalidQuantityReservation = errors.New("invalid quantity reservation")
	ErrInvalidSeatReservation     = errors.New("invalid seat reservation")
	ErrInsufficientStock          = errors.New("insufficient stock")
	ErrSeatsNotFound              = errors.New("seats not found")
	ErrSeatUnavailable            = errors.New("seat unavailable")
	ErrIdempotencyKeyConflict     = errors.New("idempotency key conflict")
	ErrIdempotencyReplayRequired  = errors.New("idempotency replay required")
	ErrReservationNotFound        = errors.New("reservation not found")
	ErrReservationCannotCancel    = errors.New("reservation cannot be cancelled")
	ErrReservationCannotConfirm   = errors.New("reservation cannot be confirmed")
	ErrReservationInvariantBroken = errors.New("reservation invariant broken")
)

type Service struct {
	repository     *Repository
	reservationTTL time.Duration
}

func NewService(repository *Repository, reservationTTL time.Duration) *Service {
	return &Service{
		repository:     repository,
		reservationTTL: reservationTTL,
	}
}

// ReserveQuantity reserves tickets using a short database transaction.
//
// The stock validation is done inside PostgreSQL with an atomic UPDATE. This prevents the
// classic race where two requests read the same available stock and both decide they can reserve
// it before either write is committed. The context is propagated from the HTTP request so a client
// disconnect or timeout can cancel database work that is no longer useful.
func (s *Service) ReserveQuantity(
	ctx context.Context,
	visitorSession sqlc.VisitorSession,
	input QuantityReservationRequest,
) (QuantityReservationResponse, error) {
	eventID, ticketTypeID, err := parseQuantityInput(input)
	if err != nil {
		return QuantityReservationResponse{}, err
	}

	var response QuantityReservationResponse
	expiresAt := time.Now().UTC().Add(s.reservationTTL)

	err = s.repository.WithinTx(ctx, func(q *sqlc.Queries) error {
		// The idempotency key represents one logical client operation. If the client retries
		// because of a timeout or network error, we return the previous reservation instead of
		// incrementing reserved_quantity a second time.
		existing, err := q.GetQuantityReservationByIdempotencyKey(
			ctx,
			sqlc.GetQuantityReservationByIdempotencyKeyParams{
				VisitorSessionID: visitorSession.ID,
				IdempotencyKey:   strings.TrimSpace(input.IdempotencyKey),
			},
		)
		if err == nil {
			response = quantityRowToResponse(existing)
			return nil
		}
		if !isNoRows(err) {
			return fmt.Errorf("get idempotent quantity reservation: %w", err)
		}

		/*
			This UPDATE is intentionally atomic.
			PostgreSQL checks the available stock and increments reserved_quantity in the same
			statement, preventing two concurrent requests from reserving the same last ticket.
			No Redis counter or application-side read-then-write calculation is trusted here.

			The update runs before inserting the reservation header so expected stock conflicts do
			not create rows that will immediately be rolled back under heavy contention.
		*/
		if _, err := q.ReserveTicketQuantity(ctx, sqlc.ReserveTicketQuantityParams{
			ID:               ticketTypeID,
			EventID:          eventID,
			ReservedQuantity: input.Quantity,
		}); isNoRows(err) {
			return ErrInsufficientStock
		} else if err != nil {
			return fmt.Errorf("reserve ticket quantity: %w", err)
		}

		reservation, err := q.CreateReservation(ctx, sqlc.CreateReservationParams{
			EventID:          eventID,
			VisitorSessionID: visitorSession.ID,
			UserID:           visitorSession.UserID,
			Status:           StatusReserved,
			ReservationType:  ReservationTypeQuantity,
			IdempotencyKey:   strings.TrimSpace(input.IdempotencyKey),
			ExpiresAt:        pgtype.Timestamptz{Time: expiresAt, Valid: true},
		})
		if isUniqueViolation(err) {
			return ErrIdempotencyReplayRequired
		}
		if err != nil {
			return fmt.Errorf("create reservation: %w", err)
		}

		item, err := q.CreateReservationItem(ctx, sqlc.CreateReservationItemParams{
			ReservationID: reservation.ID,
			TicketTypeID:  ticketTypeID,
			Quantity:      input.Quantity,
			Status:        StatusReserved,
		})
		if err != nil {
			return fmt.Errorf("create reservation item: %w", err)
		}

		response = quantityReservationToResponse(reservation, item)
		return nil
	})
	if errors.Is(err, ErrIdempotencyReplayRequired) {
		return s.replayQuantity(ctx, visitorSession.ID, strings.TrimSpace(input.IdempotencyKey))
	}
	if err != nil {
		return QuantityReservationResponse{}, err
	}
	return response, nil
}

// ReserveSeats reserves one or more physical seats inside a short transaction.
//
// Seat inventory is protected with row locks and a partial unique index. The lock makes concurrent
// transactions wait on the same seat rows, while the unique index remains the final database guard
// against two active reservations for the same seat.
func (s *Service) ReserveSeats(
	ctx context.Context,
	visitorSession sqlc.VisitorSession,
	input SeatReservationRequest,
) (SeatReservationResponse, error) {
	eventID, seatIDs, err := parseSeatInput(input)
	if err != nil {
		return SeatReservationResponse{}, err
	}

	var response SeatReservationResponse
	idempotencyKey := strings.TrimSpace(input.IdempotencyKey)
	expiresAt := time.Now().UTC().Add(s.reservationTTL)

	err = s.repository.WithinTx(ctx, func(q *sqlc.Queries) error {
		// A repeated idempotency key is a retry of the same logical operation. Returning the
		// existing seat reservation avoids inserting duplicate reservation_seats rows.
		existing, err := q.GetSeatReservationByIdempotencyKey(
			ctx,
			sqlc.GetSeatReservationByIdempotencyKeyParams{
				VisitorSessionID: visitorSession.ID,
				IdempotencyKey:   idempotencyKey,
			},
		)
		if err != nil {
			return fmt.Errorf("get idempotent seat reservation: %w", err)
		}
		if len(existing) > 0 {
			response = seatRowsToResponse(existing)
			return nil
		}

		/*
			Every API process must coordinate through PostgreSQL, not through in-memory locks.
			In production, there can be many containers and workers; a mutex in one process would
			not protect another process. SELECT FOR UPDATE locks the actual seat rows in the
			database transaction, so concurrent requests queue on the same shared source of truth.

			The query also orders by seat id. When two requests ask for [A, B] and [B, A], both
			transactions acquire locks in the same deterministic order, reducing deadlock risk.

			The seats are locked before the reservation header is inserted, matching the Python
			implementation and avoiding wasted reservation rows for requests that fail validation.
		*/
		lockedSeatIDs, err := q.LockEventSeats(ctx, sqlc.LockEventSeatsParams{
			EventID: eventID,
			Column2: seatIDs,
		})
		if err != nil {
			return fmt.Errorf("lock event seats: %w", err)
		}
		if !sameUUIDSet(lockedSeatIDs, seatIDs) {
			return ErrSeatsNotFound
		}

		/*
			Expired holds are released inside the same transaction before inserting the new rows.
			This keeps the partial unique index strict for active holds while allowing old, expired
			history to remain in reservation_seats.
		*/
		expiredReservationIDs, err := q.ListExpiredActiveReservationIDsForSeats(
			ctx,
			sqlc.ListExpiredActiveReservationIDsForSeatsParams{
				Column1:   seatIDs,
				ExpiresAt: pgtype.Timestamptz{Time: time.Now().UTC(), Valid: true},
			},
		)
		if err != nil {
			return fmt.Errorf("list expired active seat reservations: %w", err)
		}
		if len(expiredReservationIDs) > 0 {
			/*
				Lock the reservation headers in a deterministic order before changing statuses.
				This prepares the same pattern used by a future expiration worker: multiple workers
				can cooperate through row locks instead of racing in application memory.
			*/
			if _, err := q.LockReservationsByID(ctx, expiredReservationIDs); err != nil {
				return fmt.Errorf("lock expired seat reservations: %w", err)
			}
			if err := q.ExpireReservationSeatsByReservationIDs(ctx, expiredReservationIDs); err != nil {
				return fmt.Errorf("expire reservation seats: %w", err)
			}
			if err := q.ExpireReservationsByIDs(ctx, expiredReservationIDs); err != nil {
				return fmt.Errorf("expire reservations: %w", err)
			}
		}

		reservation, err := q.CreateReservation(ctx, sqlc.CreateReservationParams{
			EventID:          eventID,
			VisitorSessionID: visitorSession.ID,
			UserID:           visitorSession.UserID,
			Status:           StatusReserved,
			ReservationType:  ReservationTypeSeats,
			IdempotencyKey:   idempotencyKey,
			ExpiresAt:        pgtype.Timestamptz{Time: expiresAt, Valid: true},
		})
		if isUniqueViolation(err) {
			return ErrIdempotencyReplayRequired
		}
		if err != nil {
			return fmt.Errorf("create seat reservation: %w", err)
		}

		reservedSeats := make([]sqlc.ReservationSeat, 0, len(seatIDs))
		for _, seatID := range lockedSeatIDs {
			/*
				The partial unique index uq_active_reservation_seat is the final database guard:
				only one row per seat can have status reserved or confirmed. If a concurrent
				transaction still reaches this insert first, PostgreSQL raises a unique violation
				and the handler converts it to 409 Conflict.
			*/
			reservationSeat, err := q.CreateReservationSeat(ctx, sqlc.CreateReservationSeatParams{
				ReservationID: reservation.ID,
				SeatID:        seatID,
				Status:        StatusReserved,
				ExpiresAt:     pgtype.Timestamptz{Time: expiresAt, Valid: true},
			})
			if isUniqueViolation(err) {
				return ErrSeatUnavailable
			}
			if err != nil {
				return fmt.Errorf("create reservation seat: %w", err)
			}
			reservedSeats = append(reservedSeats, reservationSeat)
		}

		response = seatReservationToResponse(reservation, reservedSeats)
		return nil
	})
	if errors.Is(err, ErrIdempotencyReplayRequired) {
		return s.replaySeats(ctx, visitorSession.ID, idempotencyKey)
	}
	if err != nil {
		return SeatReservationResponse{}, err
	}
	return response, nil
}

// GetReservation returns a reservation only when it belongs to the current visitor session or to
// the user linked to that session.
//
// Returning "not found" for unauthorized access avoids leaking whether another user's reservation
// ID exists.
func (s *Service) GetReservation(
	ctx context.Context,
	visitorSession sqlc.VisitorSession,
	reservationID pgtype.UUID,
) (ReservationResponse, error) {
	reservation, err := s.repository.GetReservationByID(ctx, reservationID)
	if isNoRows(err) {
		return ReservationResponse{}, ErrReservationNotFound
	}
	if err != nil {
		return ReservationResponse{}, fmt.Errorf("get reservation: %w", err)
	}
	if !canAccessReservation(reservation, visitorSession) {
		return ReservationResponse{}, ErrReservationNotFound
	}

	response, err := s.buildReservationResponse(ctx, s.repository.queries, reservation)
	if err != nil {
		return ReservationResponse{}, fmt.Errorf("build reservation response: %w", err)
	}
	return response, nil
}

// CancelReservation cancels a reserved hold and releases the inventory it was blocking.
//
// Cancellation is idempotent. Once a reservation is already cancelled, repeated cancel calls return
// the current state without changing counters again.
func (s *Service) CancelReservation(
	ctx context.Context,
	visitorSession sqlc.VisitorSession,
	reservationID pgtype.UUID,
) (ReservationResponse, error) {
	var response ReservationResponse
	var transitionErr error

	err := s.repository.WithinTx(ctx, func(q *sqlc.Queries) error {
		/*
			All lifecycle transitions start by locking the reservation header.
			That one row becomes the serialization point for cancel, confirm and expiration,
			so two concurrent requests cannot both move stock for the same reservation.
		*/
		reservation, err := q.LockReservationForUpdate(ctx, reservationID)
		if isNoRows(err) {
			return ErrReservationNotFound
		}
		if err != nil {
			return fmt.Errorf("lock reservation: %w", err)
		}
		if !canAccessReservation(reservation, visitorSession) {
			return ErrReservationNotFound
		}

		switch reservation.Status {
		case StatusCancelled:
			// Cancellation is idempotent: repeating the same operation returns the current state.
			response, err = s.buildReservationResponse(ctx, q, reservation)
			return err
		case StatusConfirmed, StatusExpired:
			transitionErr = ErrReservationCannotCancel
			response, err = s.buildReservationResponse(ctx, q, reservation)
			return err
		case StatusReserved:
			if reservationExpired(reservation, time.Now().UTC()) {
				/*
					If the reservation already passed its deadline, we process expiration here
					instead of letting cancellation overwrite history after the hold expired.
				*/
				updated, err := s.expireLockedReservation(ctx, q, reservation)
				if err != nil {
					return err
				}
				transitionErr = ErrReservationCannotCancel
				response, err = s.buildReservationResponse(ctx, q, updated)
				return err
			}
			updated, err := s.cancelLockedReservation(ctx, q, reservation)
			if err != nil {
				return err
			}
			response, err = s.buildReservationResponse(ctx, q, updated)
			return err
		default:
			return ErrReservationInvariantBroken
		}
	})
	if err != nil {
		return ReservationResponse{}, err
	}
	if transitionErr != nil {
		return response, transitionErr
	}
	return response, nil
}

// ConfirmReservation simulates a successful purchase.
//
// For quantity reservations, stock moves from reserved_quantity to sold_quantity. For seat
// reservations, active holds become confirmed and remain unavailable. The reservation row lock is
// what makes concurrent confirmations increment sold_quantity only once.
func (s *Service) ConfirmReservation(
	ctx context.Context,
	visitorSession sqlc.VisitorSession,
	reservationID pgtype.UUID,
) (ReservationResponse, error) {
	var response ReservationResponse
	var transitionErr error

	err := s.repository.WithinTx(ctx, func(q *sqlc.Queries) error {
		/*
			Confirmation simulates a successful purchase. The reservation row lock prevents
			duplicate sold_quantity increments when many clients confirm the same reservation.
		*/
		reservation, err := q.LockReservationForUpdate(ctx, reservationID)
		if isNoRows(err) {
			return ErrReservationNotFound
		}
		if err != nil {
			return fmt.Errorf("lock reservation: %w", err)
		}
		if !canAccessReservation(reservation, visitorSession) {
			return ErrReservationNotFound
		}

		switch reservation.Status {
		case StatusConfirmed:
			// Confirmation is idempotent: after the first success, later calls return confirmed.
			response, err = s.buildReservationResponse(ctx, q, reservation)
			return err
		case StatusCancelled, StatusExpired:
			transitionErr = ErrReservationCannotConfirm
			response, err = s.buildReservationResponse(ctx, q, reservation)
			return err
		case StatusReserved:
			if reservationExpired(reservation, time.Now().UTC()) {
				updated, err := s.expireLockedReservation(ctx, q, reservation)
				if err != nil {
					return err
				}
				transitionErr = ErrReservationCannotConfirm
				response, err = s.buildReservationResponse(ctx, q, updated)
				return err
			}
			updated, err := s.confirmLockedReservation(ctx, q, reservation)
			if err != nil {
				return err
			}
			response, err = s.buildReservationResponse(ctx, q, updated)
			return err
		default:
			return ErrReservationInvariantBroken
		}
	})
	if err != nil {
		return ReservationResponse{}, err
	}
	if transitionErr != nil {
		return response, transitionErr
	}
	return response, nil
}

// ExpireExpiredReservations processes overdue reserved reservations in batches.
//
// The SQL query uses FOR UPDATE SKIP LOCKED, so multiple worker processes can safely run this
// function at the same time without double-processing the same reservation rows.
func (s *Service) ExpireExpiredReservations(ctx context.Context, batchSize int32) (int, error) {
	if batchSize <= 0 {
		return 0, ErrReservationInvariantBroken
	}

	processed := 0
	err := s.repository.WithinTx(ctx, func(q *sqlc.Queries) error {
		/*
			FOR UPDATE SKIP LOCKED lets multiple workers share the same queue safely.
			A worker locks a small batch of expired reservations; other workers skip those
			locked rows and process different reservations instead of waiting or double-processing.
		*/
		expiredReservations, err := q.ListExpiredReservationsForUpdate(
			ctx,
			sqlc.ListExpiredReservationsForUpdateParams{
				ExpiresAt: pgtype.Timestamptz{Time: time.Now().UTC(), Valid: true},
				Limit:     batchSize,
			},
		)
		if err != nil {
			return fmt.Errorf("list expired reservations: %w", err)
		}

		for _, reservation := range expiredReservations {
			if _, err := s.expireLockedReservation(ctx, q, reservation); err != nil {
				return err
			}
			processed++
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return processed, nil
}

// replayQuantity returns the reservation created by the first request that used this idempotency
// key. A missing quantity row means the same key was reused for another reservation type.
func (s *Service) replayQuantity(
	ctx context.Context,
	visitorSessionID pgtype.UUID,
	idempotencyKey string,
) (QuantityReservationResponse, error) {
	existing, err := s.repository.GetQuantityByIdempotencyKey(ctx, visitorSessionID, idempotencyKey)
	if isNoRows(err) {
		return QuantityReservationResponse{}, ErrIdempotencyKeyConflict
	}
	if err != nil {
		return QuantityReservationResponse{}, fmt.Errorf("replay idempotent reservation: %w", err)
	}
	return quantityRowToResponse(existing), nil
}

// replaySeats mirrors replayQuantity for seat reservations and prevents retry storms from creating
// duplicate active seat rows.
func (s *Service) replaySeats(
	ctx context.Context,
	visitorSessionID pgtype.UUID,
	idempotencyKey string,
) (SeatReservationResponse, error) {
	existing, err := s.repository.GetSeatsByIdempotencyKey(ctx, visitorSessionID, idempotencyKey)
	if err != nil {
		return SeatReservationResponse{}, fmt.Errorf("replay idempotent seat reservation: %w", err)
	}
	if len(existing) == 0 {
		return SeatReservationResponse{}, ErrIdempotencyKeyConflict
	}
	return seatRowsToResponse(existing), nil
}

func parseQuantityInput(
	input QuantityReservationRequest,
) (pgtype.UUID, pgtype.UUID, error) {
	if input.Quantity <= 0 || strings.TrimSpace(input.IdempotencyKey) == "" {
		return pgtype.UUID{}, pgtype.UUID{}, ErrInvalidQuantityReservation
	}

	eventID, err := db.ParseUUID(input.EventID)
	if err != nil {
		return pgtype.UUID{}, pgtype.UUID{}, ErrInvalidQuantityReservation
	}
	ticketTypeID, err := db.ParseUUID(input.TicketTypeID)
	if err != nil {
		return pgtype.UUID{}, pgtype.UUID{}, ErrInvalidQuantityReservation
	}
	return eventID, ticketTypeID, nil
}

// parseSeatInput rejects duplicate seat IDs before opening the transaction.
//
// Duplicates in the same request would make the response confusing and could produce misleading
// lock-count checks. Sorting here also mirrors the SQL ORDER BY id used for deterministic locking.
func parseSeatInput(input SeatReservationRequest) (pgtype.UUID, []pgtype.UUID, error) {
	if strings.TrimSpace(input.IdempotencyKey) == "" || len(input.SeatIDs) == 0 {
		return pgtype.UUID{}, nil, ErrInvalidSeatReservation
	}

	eventID, err := db.ParseUUID(input.EventID)
	if err != nil {
		return pgtype.UUID{}, nil, ErrInvalidSeatReservation
	}

	seen := make(map[string]struct{}, len(input.SeatIDs))
	seatIDs := make([]pgtype.UUID, 0, len(input.SeatIDs))
	for _, rawSeatID := range input.SeatIDs {
		seatID, err := db.ParseUUID(strings.TrimSpace(rawSeatID))
		if err != nil {
			return pgtype.UUID{}, nil, ErrInvalidSeatReservation
		}
		key := db.UUIDToString(seatID)
		if _, exists := seen[key]; exists {
			return pgtype.UUID{}, nil, ErrInvalidSeatReservation
		}
		seen[key] = struct{}{}
		seatIDs = append(seatIDs, seatID)
	}

	// Sorting the input mirrors the SQL ORDER BY id and makes downstream responses deterministic.
	sort.Slice(seatIDs, func(i int, j int) bool {
		return db.UUIDToString(seatIDs[i]) < db.UUIDToString(seatIDs[j])
	})
	return eventID, seatIDs, nil
}

func quantityReservationToResponse(
	reservation sqlc.Reservation,
	item sqlc.ReservationItem,
) QuantityReservationResponse {
	return QuantityReservationResponse{
		ReservationID:   db.UUIDToString(reservation.ID),
		Status:          reservation.Status,
		ReservationType: reservation.ReservationType,
		ExpiresAt:       db.TimestamptzToTime(reservation.ExpiresAt),
		Items: []QuantityReservationItemResponse{
			{
				TicketTypeID: db.UUIDToString(item.TicketTypeID),
				Quantity:     item.Quantity,
			},
		},
	}
}

func quantityRowToResponse(row sqlc.GetQuantityReservationByIdempotencyKeyRow) QuantityReservationResponse {
	return QuantityReservationResponse{
		ReservationID:   db.UUIDToString(row.ReservationID),
		Status:          row.Status,
		ReservationType: row.ReservationType,
		ExpiresAt:       db.TimestamptzToTime(row.ExpiresAt),
		Items: []QuantityReservationItemResponse{
			{
				TicketTypeID: db.UUIDToString(row.TicketTypeID),
				Quantity:     row.Quantity,
			},
		},
	}
}

func seatReservationToResponse(
	reservation sqlc.Reservation,
	seats []sqlc.ReservationSeat,
) SeatReservationResponse {
	response := SeatReservationResponse{
		ReservationID:   db.UUIDToString(reservation.ID),
		Status:          reservation.Status,
		ReservationType: reservation.ReservationType,
		ExpiresAt:       db.TimestamptzToTime(reservation.ExpiresAt),
		Seats:           make([]SeatReservationItemResponse, 0, len(seats)),
	}
	for _, seat := range seats {
		response.Seats = append(response.Seats, SeatReservationItemResponse{
			SeatID: db.UUIDToString(seat.SeatID),
		})
	}
	return response
}

func seatRowsToResponse(rows []sqlc.GetSeatReservationByIdempotencyKeyRow) SeatReservationResponse {
	response := SeatReservationResponse{
		ReservationID:   db.UUIDToString(rows[0].ReservationID),
		Status:          rows[0].Status,
		ReservationType: rows[0].ReservationType,
		ExpiresAt:       db.TimestamptzToTime(rows[0].ExpiresAt),
		Seats:           make([]SeatReservationItemResponse, 0, len(rows)),
	}
	for _, row := range rows {
		response.Seats = append(response.Seats, SeatReservationItemResponse{
			SeatID: db.UUIDToString(row.SeatID),
		})
	}
	return response
}

func sameUUIDSet(left []pgtype.UUID, right []pgtype.UUID) bool {
	if len(left) != len(right) {
		return false
	}

	leftKeys := make(map[string]struct{}, len(left))
	for _, id := range left {
		leftKeys[db.UUIDToString(id)] = struct{}{}
	}
	for _, id := range right {
		if _, exists := leftKeys[db.UUIDToString(id)]; !exists {
			return false
		}
	}
	return true
}

// cancelLockedReservation assumes the reservation row is already locked by the caller.
//
// Keeping this precondition explicit matters: without the reservation lock, two cancel/confirm
// requests could both try to move the same inventory counters.
func (s *Service) cancelLockedReservation(
	ctx context.Context,
	q *sqlc.Queries,
	reservation sqlc.Reservation,
) (sqlc.Reservation, error) {
	switch reservation.ReservationType {
	case ReservationTypeQuantity:
		if err := releaseReservedQuantityItems(ctx, q, reservation.ID, StatusCancelled); err != nil {
			return sqlc.Reservation{}, err
		}
	case ReservationTypeSeats:
		if err := q.UpdateReservationSeatsStatus(ctx, sqlc.UpdateReservationSeatsStatusParams{
			ReservationID: reservation.ID,
			Status:        StatusCancelled,
			Status_2:      StatusReserved,
		}); err != nil {
			return sqlc.Reservation{}, fmt.Errorf("cancel reservation seats: %w", err)
		}
	default:
		return sqlc.Reservation{}, ErrReservationInvariantBroken
	}

	updated, err := q.UpdateReservationStatus(ctx, sqlc.UpdateReservationStatusParams{
		ID:     reservation.ID,
		Status: StatusCancelled,
	})
	if err != nil {
		return sqlc.Reservation{}, fmt.Errorf("mark reservation cancelled: %w", err)
	}
	return updated, nil
}

// confirmLockedReservation assumes the reservation row is already locked and performs the state
// transition atomically with the inventory movement.
func (s *Service) confirmLockedReservation(
	ctx context.Context,
	q *sqlc.Queries,
	reservation sqlc.Reservation,
) (sqlc.Reservation, error) {
	switch reservation.ReservationType {
	case ReservationTypeQuantity:
		if err := confirmReservedQuantityItems(ctx, q, reservation.ID); err != nil {
			return sqlc.Reservation{}, err
		}
	case ReservationTypeSeats:
		if err := q.UpdateReservationSeatsStatus(ctx, sqlc.UpdateReservationSeatsStatusParams{
			ReservationID: reservation.ID,
			Status:        StatusConfirmed,
			Status_2:      StatusReserved,
		}); err != nil {
			return sqlc.Reservation{}, fmt.Errorf("confirm reservation seats: %w", err)
		}
	default:
		return sqlc.Reservation{}, ErrReservationInvariantBroken
	}

	updated, err := q.UpdateReservationStatus(ctx, sqlc.UpdateReservationStatusParams{
		ID:     reservation.ID,
		Status: StatusConfirmed,
	})
	if err != nil {
		return sqlc.Reservation{}, fmt.Errorf("mark reservation confirmed: %w", err)
	}
	return updated, nil
}

// expireLockedReservation releases inventory for an expired reservation while preserving its
// historical rows with status=expired.
func (s *Service) expireLockedReservation(
	ctx context.Context,
	q *sqlc.Queries,
	reservation sqlc.Reservation,
) (sqlc.Reservation, error) {
	switch reservation.ReservationType {
	case ReservationTypeQuantity:
		if err := releaseReservedQuantityItems(ctx, q, reservation.ID, StatusExpired); err != nil {
			return sqlc.Reservation{}, err
		}
	case ReservationTypeSeats:
		if err := q.UpdateReservationSeatsStatus(ctx, sqlc.UpdateReservationSeatsStatusParams{
			ReservationID: reservation.ID,
			Status:        StatusExpired,
			Status_2:      StatusReserved,
		}); err != nil {
			return sqlc.Reservation{}, fmt.Errorf("expire reservation seats: %w", err)
		}
	default:
		return sqlc.Reservation{}, ErrReservationInvariantBroken
	}

	updated, err := q.UpdateReservationStatus(ctx, sqlc.UpdateReservationStatusParams{
		ID:     reservation.ID,
		Status: StatusExpired,
	})
	if err != nil {
		return sqlc.Reservation{}, fmt.Errorf("mark reservation expired: %w", err)
	}
	return updated, nil
}

// releaseReservedQuantityItems decrements reserved_quantity for every still-reserved item.
//
// The database UPDATE contains a reserved_quantity >= quantity guard, so a programming mistake or
// unexpected concurrent state cannot push the counter below zero.
func releaseReservedQuantityItems(
	ctx context.Context,
	q *sqlc.Queries,
	reservationID pgtype.UUID,
	targetStatus string,
) error {
	items, err := q.ListReservationItems(ctx, reservationID)
	if err != nil {
		return fmt.Errorf("list reservation items: %w", err)
	}

	releasedAny := false
	for _, item := range items {
		if item.Status != StatusReserved {
			continue
		}
		/*
			Releasing quantity is also guarded by PostgreSQL. The WHERE clause requires
			reserved_quantity >= quantity, so a bug or concurrent transition cannot make
			reserved_quantity negative.
		*/
		if _, err := q.ReleaseTicketQuantity(ctx, sqlc.ReleaseTicketQuantityParams{
			ID:               item.TicketTypeID,
			ReservedQuantity: item.Quantity,
		}); isNoRows(err) {
			return ErrReservationInvariantBroken
		} else if err != nil {
			return fmt.Errorf("release ticket quantity: %w", err)
		}
		releasedAny = true
	}
	if len(items) == 0 || !releasedAny {
		return ErrReservationInvariantBroken
	}

	if err := q.UpdateReservationItemsStatus(ctx, sqlc.UpdateReservationItemsStatusParams{
		ReservationID: reservationID,
		Status:        targetStatus,
		Status_2:      StatusReserved,
	}); err != nil {
		return fmt.Errorf("mark reservation items %s: %w", targetStatus, err)
	}
	return nil
}

// confirmReservedQuantityItems moves stock from reserved_quantity to sold_quantity.
//
// This is intentionally one SQL UPDATE per ticket type. PostgreSQL enforces the counter invariants
// and the surrounding reservation lock makes the operation logically idempotent.
func confirmReservedQuantityItems(ctx context.Context, q *sqlc.Queries, reservationID pgtype.UUID) error {
	items, err := q.ListReservationItems(ctx, reservationID)
	if err != nil {
		return fmt.Errorf("list reservation items: %w", err)
	}

	confirmedAny := false
	for _, item := range items {
		if item.Status != StatusReserved {
			continue
		}
		/*
			Confirmation moves stock from reserved to sold in one SQL statement.
			The total allocated quantity stays bounded by the database constraint, and
			reserved_quantity cannot go negative because the UPDATE checks it first.
		*/
		if _, err := q.ConfirmTicketQuantity(ctx, sqlc.ConfirmTicketQuantityParams{
			ID:               item.TicketTypeID,
			ReservedQuantity: item.Quantity,
		}); isNoRows(err) {
			return ErrReservationInvariantBroken
		} else if err != nil {
			return fmt.Errorf("confirm ticket quantity: %w", err)
		}
		confirmedAny = true
	}
	if len(items) == 0 || !confirmedAny {
		return ErrReservationInvariantBroken
	}

	if err := q.UpdateReservationItemsStatus(ctx, sqlc.UpdateReservationItemsStatusParams{
		ReservationID: reservationID,
		Status:        StatusConfirmed,
		Status_2:      StatusReserved,
	}); err != nil {
		return fmt.Errorf("mark reservation items confirmed: %w", err)
	}
	return nil
}

// buildReservationResponse reads child rows after the transition so clients see the final item or
// seat status, not a stale status from before the transaction.
func (s *Service) buildReservationResponse(
	ctx context.Context,
	q *sqlc.Queries,
	reservation sqlc.Reservation,
) (ReservationResponse, error) {
	response := ReservationResponse{
		ReservationID:   db.UUIDToString(reservation.ID),
		Status:          reservation.Status,
		ReservationType: reservation.ReservationType,
		ExpiresAt:       db.TimestamptzToTime(reservation.ExpiresAt),
	}

	switch reservation.ReservationType {
	case ReservationTypeQuantity:
		items, err := q.ListReservationItems(ctx, reservation.ID)
		if err != nil {
			return ReservationResponse{}, fmt.Errorf("list reservation items: %w", err)
		}
		response.Items = make([]ReservationItemDetailResponse, 0, len(items))
		for _, item := range items {
			response.Items = append(response.Items, ReservationItemDetailResponse{
				TicketTypeID: db.UUIDToString(item.TicketTypeID),
				Quantity:     item.Quantity,
				Status:       item.Status,
			})
		}
	case ReservationTypeSeats:
		seats, err := q.ListReservationSeats(ctx, reservation.ID)
		if err != nil {
			return ReservationResponse{}, fmt.Errorf("list reservation seats: %w", err)
		}
		response.Seats = make([]ReservationSeatDetailResponse, 0, len(seats))
		for _, seat := range seats {
			response.Seats = append(response.Seats, ReservationSeatDetailResponse{
				SeatID: db.UUIDToString(seat.SeatID),
				Status: seat.Status,
			})
		}
	default:
		return ReservationResponse{}, ErrReservationInvariantBroken
	}

	return response, nil
}

// canAccessReservation keeps authorization close to reservation reads. A logged-in user may see
// reservations linked to their user_id, while anonymous users are limited to their visitor session.
func canAccessReservation(reservation sqlc.Reservation, visitorSession sqlc.VisitorSession) bool {
	if sameUUID(reservation.VisitorSessionID, visitorSession.ID) {
		return true
	}
	return reservation.UserID.Valid &&
		visitorSession.UserID.Valid &&
		sameUUID(reservation.UserID, visitorSession.UserID)
}

func sameUUID(left pgtype.UUID, right pgtype.UUID) bool {
	return left.Valid && right.Valid && left.Bytes == right.Bytes
}

func reservationExpired(reservation sqlc.Reservation, now time.Time) bool {
	return reservation.ExpiresAt.Valid && !reservation.ExpiresAt.Time.After(now)
}

func isNoRows(err error) bool {
	return errors.Is(err, pgx.ErrNoRows) || errors.Is(err, sql.ErrNoRows)
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

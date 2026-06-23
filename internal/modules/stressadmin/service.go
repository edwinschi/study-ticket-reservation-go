package stressadmin

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"ticket-reservation-go-lab/internal/db"
)

const StressEventPrefix = "__stress_seed__:"
const staleActiveReservationTolerance = 60 * time.Second

var ErrStressAdminDisabled = errors.New("stress admin endpoints are disabled")

type Service struct {
	repository *Repository
	appEnv     string
}

func NewService(repository *Repository, appEnv string) *Service {
	return &Service{
		repository: repository,
		appEnv:     appEnv,
	}
}

// Seed creates a coherent local fixture for stress tests.
//
// k6 needs stable IDs for one event, one quantity inventory, and a set of seats. Creating them
// through this endpoint keeps scripts simple and avoids hard-coding IDs from a previous database.
func (s *Service) Seed(ctx context.Context) (SeedResponse, error) {
	if !s.isLocal() {
		return SeedResponse{}, ErrStressAdminDisabled
	}

	suffix, err := randomHex(8)
	if err != nil {
		return SeedResponse{}, err
	}

	now := time.Now().UTC()
	event, ticketType, seats, err := s.repository.Seed(
		ctx,
		StressEventPrefix+suffix,
		pgtype.Timestamptz{Time: now.Add(24 * time.Hour), Valid: true},
		pgtype.Timestamptz{Time: now.Add(27 * time.Hour), Valid: true},
	)
	if err != nil {
		return SeedResponse{}, fmt.Errorf("seed stress fixture: %w", err)
	}

	seatIDs := make([]string, 0, len(seats))
	for _, seat := range seats {
		seatIDs = append(seatIDs, db.UUIDToString(seat.ID))
	}

	return SeedResponse{
		EventID:      db.UUIDToString(event.ID),
		TicketTypeID: db.UUIDToString(ticketType.ID),
		SeatIDs:      seatIDs,
	}, nil
}

// Reset removes only stress fixtures created with the reserved prefix.
//
// This keeps the endpoint safe for local study use: manual events created while exploring the API
// are not deleted accidentally.
func (s *Service) Reset(ctx context.Context) (ResetResponse, error) {
	if !s.isLocal() {
		return ResetResponse{}, ErrStressAdminDisabled
	}

	deleted, err := s.repository.Reset(ctx, StressEventPrefix+"%")
	if err != nil {
		return ResetResponse{}, fmt.Errorf("reset stress fixture: %w", err)
	}
	return ResetResponse{EventsDeleted: deleted}, nil
}

// AssertConsistency validates database invariants after concurrency or k6 runs.
//
// It does not repair data. It reports violations with enough IDs to investigate which invariant
// failed, making it useful as a final safety check after heavy load.
func (s *Service) AssertConsistency(ctx context.Context) (ConsistencyResponse, error) {
	if !s.isLocal() {
		return ConsistencyResponse{}, ErrStressAdminDisabled
	}

	response := ConsistencyResponse{
		Checks: ConsistencyChecks{
			TicketQuantityNotOversold:        true,
			TicketQuantityNotNegative:        true,
			NoDuplicateActiveSeats:           true,
			NoOrphanReservationItems:         true,
			NoOrphanReservationSeats:         true,
			NoStaleExpiredActiveReservations: true,
		},
		Details: []ConsistencyDetail{},
	}

	/*
		These checks are intentionally read-only and database-driven. They are meant to be called
		after concurrency tests or k6 runs to verify the invariants that PostgreSQL constraints and
		transactions should protect under load.
	*/
	negativeQuantities, err := s.repository.ListNegativeTicketQuantityViolations(ctx)
	if err != nil {
		return ConsistencyResponse{}, fmt.Errorf("check negative ticket quantities: %w", err)
	}
	if len(negativeQuantities) > 0 {
		response.Checks.TicketQuantityNotNegative = false
		for _, row := range negativeQuantities {
			response.Details = append(response.Details, ConsistencyDetail{
				Check:   "ticket_quantity_not_negative",
				Message: "Ticket type has a negative quantity",
				Data: map[string]any{
					"ticket_type_id":    db.UUIDToString(row.ID),
					"total_quantity":    row.TotalQuantity,
					"sold_quantity":     row.SoldQuantity,
					"reserved_quantity": row.ReservedQuantity,
				},
			})
		}
	}

	oversoldQuantities, err := s.repository.ListOversoldTicketQuantityViolations(ctx)
	if err != nil {
		return ConsistencyResponse{}, fmt.Errorf("check oversold ticket quantities: %w", err)
	}
	if len(oversoldQuantities) > 0 {
		response.Checks.TicketQuantityNotOversold = false
		for _, row := range oversoldQuantities {
			response.Details = append(response.Details, ConsistencyDetail{
				Check:   "ticket_quantity_not_oversold",
				Message: "Ticket type has sold + reserved greater than total",
				Data: map[string]any{
					"ticket_type_id":    db.UUIDToString(row.ID),
					"total_quantity":    row.TotalQuantity,
					"sold_quantity":     row.SoldQuantity,
					"reserved_quantity": row.ReservedQuantity,
				},
			})
		}
	}

	duplicateSeats, err := s.repository.ListDuplicateActiveSeatViolations(ctx)
	if err != nil {
		return ConsistencyResponse{}, fmt.Errorf("check duplicate active seats: %w", err)
	}
	if len(duplicateSeats) > 0 {
		response.Checks.NoDuplicateActiveSeats = false
		for _, row := range duplicateSeats {
			response.Details = append(response.Details, ConsistencyDetail{
				Check:   "no_duplicate_active_seats",
				Message: "Seat has more than one active reservation",
				Data: map[string]any{
					"seat_id":      db.UUIDToString(row.SeatID),
					"active_count": row.ActiveCount,
				},
			})
		}
	}

	orphanItems, err := s.repository.ListOrphanReservationItemViolations(ctx)
	if err != nil {
		return ConsistencyResponse{}, fmt.Errorf("check orphan reservation items: %w", err)
	}
	if len(orphanItems) > 0 {
		response.Checks.NoOrphanReservationItems = false
		for _, row := range orphanItems {
			response.Details = append(response.Details, ConsistencyDetail{
				Check:   "no_orphan_reservation_items",
				Message: "Reservation item points to a missing reservation",
				Data: map[string]any{
					"reservation_item_id": db.UUIDToString(row.ReservationItemID),
					"reservation_id":      db.UUIDToString(row.ReservationID),
				},
			})
		}
	}

	orphanSeats, err := s.repository.ListOrphanReservationSeatViolations(ctx)
	if err != nil {
		return ConsistencyResponse{}, fmt.Errorf("check orphan reservation seats: %w", err)
	}
	if len(orphanSeats) > 0 {
		response.Checks.NoOrphanReservationSeats = false
		for _, row := range orphanSeats {
			response.Details = append(response.Details, ConsistencyDetail{
				Check:   "no_orphan_reservation_seats",
				Message: "Reservation seat points to a missing reservation",
				Data: map[string]any{
					"reservation_seat_id": db.UUIDToString(row.ReservationSeatID),
					"reservation_id":      db.UUIDToString(row.ReservationID),
				},
			})
		}
	}

	staleThreshold := pgtype.Timestamptz{
		Time:  time.Now().UTC().Add(-staleActiveReservationTolerance),
		Valid: true,
	}
	staleReservations, err := s.repository.ListStaleExpiredActiveReservationViolations(ctx, staleThreshold)
	if err != nil {
		return ConsistencyResponse{}, fmt.Errorf("check stale expired active reservations: %w", err)
	}
	if len(staleReservations) > 0 {
		response.Checks.NoStaleExpiredActiveReservations = false
		for _, row := range staleReservations {
			response.Details = append(response.Details, ConsistencyDetail{
				Check:   "no_stale_expired_active_reservations",
				Message: "Reserved reservation is expired beyond the local processing tolerance",
				Data: map[string]any{
					"reservation_id": db.UUIDToString(row.ID),
					"status":         row.Status,
					"expires_at":     db.TimestamptzToTime(row.ExpiresAt),
				},
			})
		}
	}

	response.OK = response.Checks.TicketQuantityNotOversold &&
		response.Checks.TicketQuantityNotNegative &&
		response.Checks.NoDuplicateActiveSeats &&
		response.Checks.NoOrphanReservationItems &&
		response.Checks.NoOrphanReservationSeats &&
		response.Checks.NoStaleExpiredActiveReservations

	return response, nil
}

func (s *Service) isLocal() bool {
	return s.appEnv == "development" || s.appEnv == "local" || s.appEnv == "test"
}

func randomHex(size int) (string, error) {
	bytes := make([]byte, size)
	if _, err := rand.Read(bytes); err != nil {
		return "", fmt.Errorf("generate random suffix: %w", err)
	}
	return hex.EncodeToString(bytes), nil
}

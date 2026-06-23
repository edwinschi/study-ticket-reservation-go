package stressadmin

import (
	"context"
	"strconv"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"ticket-reservation-go-lab/internal/sqlc"
)

type Repository struct {
	pool    *pgxpool.Pool
	queries *sqlc.Queries
}

func NewRepository(pool *pgxpool.Pool, queries *sqlc.Queries) *Repository {
	return &Repository{
		pool:    pool,
		queries: queries,
	}
}

// Seed writes the whole stress fixture in one transaction.
//
// If any insert fails, PostgreSQL rolls the fixture back and k6 never receives a partially-created
// event with missing seats or ticket type.
func (r *Repository) Seed(
	ctx context.Context,
	eventName string,
	startsAt pgtype.Timestamptz,
	endsAt pgtype.Timestamptz,
) (sqlc.Event, sqlc.TicketType, []sqlc.Seat, error) {
	// Seed is wrapped in one transaction so stress fixtures are never half-created. That matters
	// later because k6 will consume these IDs as a single coherent inventory fixture.
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return sqlc.Event{}, sqlc.TicketType{}, nil, err
	}
	defer tx.Rollback(ctx) // safe no-op after commit

	q := r.queries.WithTx(tx)
	event, err := q.CreateEvent(ctx, sqlc.CreateEventParams{
		Name:     eventName,
		StartsAt: startsAt,
		EndsAt:   endsAt,
	})
	if err != nil {
		return sqlc.Event{}, sqlc.TicketType{}, nil, err
	}

	ticketType, err := q.CreateTicketType(ctx, sqlc.CreateTicketTypeParams{
		EventID:       event.ID,
		Name:          "General Admission",
		TotalQuantity: 1000,
	})
	if err != nil {
		return sqlc.Event{}, sqlc.TicketType{}, nil, err
	}

	seats := make([]sqlc.Seat, 0, 100)
	for row := 1; row <= 10; row++ {
		for seat := 1; seat <= 10; seat++ {
			created, createErr := q.CreateSeat(ctx, sqlc.CreateSeatParams{
				EventID:    event.ID,
				Section:    "A",
				RowName:    strconv.Itoa(row),
				SeatNumber: strconv.Itoa(seat),
			})
			if createErr != nil {
				return sqlc.Event{}, sqlc.TicketType{}, nil, createErr
			}
			seats = append(seats, created)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return sqlc.Event{}, sqlc.TicketType{}, nil, err
	}
	return event, ticketType, seats, nil
}

// Reset deletes stress-owned rows in dependency order.
//
// The production schema uses foreign keys and RESTRICT/CASCADE choices intentionally. Deleting
// children first makes the cleanup explicit and keeps the example easy to reason about.
func (r *Repository) Reset(ctx context.Context, eventNamePattern string) (int64, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)

	q := r.queries.WithTx(tx)
	eventIDs, err := q.ListStressEventIDs(ctx, eventNamePattern)
	if err != nil {
		return 0, err
	}
	if len(eventIDs) == 0 {
		return 0, tx.Commit(ctx)
	}

	// Delete children explicitly before events. This remains safe when later stages add
	// reservations, because event deletion is intentionally RESTRICTed by reservation ownership.
	if err := q.DeleteStressReservationSeats(ctx, eventIDs); err != nil {
		return 0, err
	}
	if err := q.DeleteStressReservationItems(ctx, eventIDs); err != nil {
		return 0, err
	}
	if err := q.DeleteStressReservations(ctx, eventIDs); err != nil {
		return 0, err
	}
	deleted, err := q.DeleteStressEvents(ctx, eventIDs)
	if err != nil {
		return 0, err
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, err
	}
	return deleted, nil
}

func (r *Repository) ListNegativeTicketQuantityViolations(
	ctx context.Context,
) ([]sqlc.ListNegativeTicketQuantityViolationsRow, error) {
	return r.queries.ListNegativeTicketQuantityViolations(ctx)
}

func (r *Repository) ListOversoldTicketQuantityViolations(
	ctx context.Context,
) ([]sqlc.ListOversoldTicketQuantityViolationsRow, error) {
	return r.queries.ListOversoldTicketQuantityViolations(ctx)
}

func (r *Repository) ListDuplicateActiveSeatViolations(
	ctx context.Context,
) ([]sqlc.ListDuplicateActiveSeatViolationsRow, error) {
	return r.queries.ListDuplicateActiveSeatViolations(ctx)
}

func (r *Repository) ListOrphanReservationItemViolations(
	ctx context.Context,
) ([]sqlc.ListOrphanReservationItemViolationsRow, error) {
	return r.queries.ListOrphanReservationItemViolations(ctx)
}

func (r *Repository) ListOrphanReservationSeatViolations(
	ctx context.Context,
) ([]sqlc.ListOrphanReservationSeatViolationsRow, error) {
	return r.queries.ListOrphanReservationSeatViolations(ctx)
}

func (r *Repository) ListStaleExpiredActiveReservationViolations(
	ctx context.Context,
	threshold pgtype.Timestamptz,
) ([]sqlc.ListStaleExpiredActiveReservationViolationsRow, error) {
	return r.queries.ListStaleExpiredActiveReservationViolations(ctx, threshold)
}

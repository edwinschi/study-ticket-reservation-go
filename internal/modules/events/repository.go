package events

import (
	"context"

	"github.com/jackc/pgx/v5/pgtype"

	"ticket-reservation-go-lab/internal/sqlc"
)

type Repository struct {
	queries *sqlc.Queries
}

func NewRepository(queries *sqlc.Queries) *Repository {
	return &Repository{queries: queries}
}

func (r *Repository) CreateEvent(
	ctx context.Context,
	name string,
	startsAt pgtype.Timestamptz,
	endsAt pgtype.Timestamptz,
) (sqlc.Event, error) {
	return r.queries.CreateEvent(ctx, sqlc.CreateEventParams{
		Name:     name,
		StartsAt: startsAt,
		EndsAt:   endsAt,
	})
}

func (r *Repository) GetEvent(ctx context.Context, eventID pgtype.UUID) (sqlc.Event, error) {
	return r.queries.GetEventByID(ctx, eventID)
}

func (r *Repository) CreateTicketType(
	ctx context.Context,
	eventID pgtype.UUID,
	name string,
	totalQuantity int32,
) (sqlc.TicketType, error) {
	return r.queries.CreateTicketType(ctx, sqlc.CreateTicketTypeParams{
		EventID:       eventID,
		Name:          name,
		TotalQuantity: totalQuantity,
	})
}

func (r *Repository) CreateSeat(
	ctx context.Context,
	eventID pgtype.UUID,
	seat CreateSeatRequest,
) (sqlc.Seat, error) {
	return r.queries.CreateSeat(ctx, sqlc.CreateSeatParams{
		EventID:    eventID,
		Section:    seat.Section,
		RowName:    seat.RowName,
		SeatNumber: seat.SeatNumber,
	})
}

func (r *Repository) ListTicketTypeInventory(
	ctx context.Context,
	eventID pgtype.UUID,
) ([]sqlc.ListTicketTypeInventoryByEventRow, error) {
	return r.queries.ListTicketTypeInventoryByEvent(ctx, eventID)
}

func (r *Repository) ListSeatInventory(
	ctx context.Context,
	eventID pgtype.UUID,
) ([]sqlc.ListSeatInventoryByEventRow, error) {
	return r.queries.ListSeatInventoryByEvent(ctx, eventID)
}

package reservations

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"ticket-reservation-go-lab/internal/db"
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

// WithinTx gives services a short-lived sqlc query object bound to one pgx transaction.
//
// The pgxpool itself is safe to share across requests, but a transaction is not. This helper keeps
// the transaction lifetime local to the service operation and prevents accidentally reusing a tx
// across goroutines or requests.
func (r *Repository) WithinTx(ctx context.Context, fn func(q *sqlc.Queries) error) error {
	return db.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		return fn(r.queries.WithTx(tx))
	})
}

// GetQuantityByIdempotencyKey is used after a unique-key race.
//
// If two requests with the same key arrive together, one inserts the reservation and the other
// receives a unique violation. The loser then reads the winner's row through this method.
func (r *Repository) GetQuantityByIdempotencyKey(
	ctx context.Context,
	visitorSessionID pgtype.UUID,
	idempotencyKey string,
) (sqlc.GetQuantityReservationByIdempotencyKeyRow, error) {
	return r.queries.GetQuantityReservationByIdempotencyKey(
		ctx,
		sqlc.GetQuantityReservationByIdempotencyKeyParams{
			VisitorSessionID: visitorSessionID,
			IdempotencyKey:   idempotencyKey,
		},
	)
}

func (r *Repository) GetReservationByID(ctx context.Context, reservationID pgtype.UUID) (sqlc.Reservation, error) {
	return r.queries.GetReservationByID(ctx, reservationID)
}

// GetSeatsByIdempotencyKey replays a seat reservation retry without touching seat availability.
func (r *Repository) GetSeatsByIdempotencyKey(
	ctx context.Context,
	visitorSessionID pgtype.UUID,
	idempotencyKey string,
) ([]sqlc.GetSeatReservationByIdempotencyKeyRow, error) {
	return r.queries.GetSeatReservationByIdempotencyKey(
		ctx,
		sqlc.GetSeatReservationByIdempotencyKeyParams{
			VisitorSessionID: visitorSessionID,
			IdempotencyKey:   idempotencyKey,
		},
	)
}

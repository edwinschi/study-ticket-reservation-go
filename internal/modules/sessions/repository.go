package sessions

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

func (r *Repository) Create(
	ctx context.Context,
	tokenHash string,
	now pgtype.Timestamptz,
	expiresAt pgtype.Timestamptz,
) (sqlc.VisitorSession, error) {
	return r.queries.CreateVisitorSession(ctx, sqlc.CreateVisitorSessionParams{
		AnonymousTokenHash: tokenHash,
		LastSeenAt:         now,
		ExpiresAt:          expiresAt,
	})
}

func (r *Repository) GetByTokenHash(
	ctx context.Context,
	tokenHash string,
) (sqlc.VisitorSession, error) {
	return r.queries.GetVisitorSessionByTokenHash(ctx, tokenHash)
}

func (r *Repository) AttachUser(
	ctx context.Context,
	sessionID pgtype.UUID,
	userID pgtype.UUID,
) (sqlc.VisitorSession, error) {
	return r.queries.AttachUserToVisitorSession(ctx, sqlc.AttachUserToVisitorSessionParams{
		ID:     sessionID,
		UserID: userID,
	})
}

func (r *Repository) Touch(ctx context.Context, sessionID pgtype.UUID) (sqlc.VisitorSession, error) {
	return r.queries.TouchVisitorSession(ctx, sessionID)
}

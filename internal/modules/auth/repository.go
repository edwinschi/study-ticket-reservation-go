package auth

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

func (r *Repository) CreateUser(
	ctx context.Context,
	email string,
	passwordHash string,
) (sqlc.User, error) {
	return r.queries.CreateUser(ctx, sqlc.CreateUserParams{
		Email:        email,
		PasswordHash: passwordHash,
	})
}

func (r *Repository) GetUserByEmail(ctx context.Context, email string) (sqlc.User, error) {
	return r.queries.GetUserByEmail(ctx, email)
}

func (r *Repository) GetUserByID(ctx context.Context, userID pgtype.UUID) (sqlc.User, error) {
	return r.queries.GetUserByID(ctx, userID)
}

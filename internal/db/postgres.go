package db

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"

	"ticket-reservation-go-lab/internal/sqlc"
)

func OpenPostgres(ctx context.Context, databaseURL string) (*pgxpool.Pool, error) {
	// pgxpool manages a pool of PostgreSQL connections. The pool is intentionally shared by all
	// handlers and workers, while each request still opens its own transaction when it needs one.
	// The startup context bounds connection attempts so a broken database does not hang forever.
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("parse postgres config: %w", err)
	}

	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return nil, fmt.Errorf("create postgres pool: %w", err)
	}

	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping postgres: %w", err)
	}

	return pool, nil
}

func NewQueries(pool *pgxpool.Pool) *sqlc.Queries {
	// sqlc generates code against a small DBTX interface. pgxpool.Pool satisfies that
	// interface, so services can use type-safe query methods without introducing an ORM or hiding
	// the SQL that is important for this concurrency study.
	return sqlc.New(pool)
}

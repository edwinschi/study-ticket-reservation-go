package integration_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func openTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()

	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		t.Skip("DATABASE_URL is not set; integration database is unavailable")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatalf("open postgres pool: %v", err)
	}

	t.Cleanup(pool.Close)
	return pool
}

func TestInitialMigrationIsApplied(t *testing.T) {
	pool := openTestPool(t)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var version int
	var dirty bool
	err := pool.QueryRow(ctx, "SELECT version, dirty FROM schema_migrations").Scan(&version, &dirty)
	if err != nil {
		t.Fatalf("query schema_migrations: %v", err)
	}

	if version != 1 {
		t.Fatalf("expected migration version 1, got %d", version)
	}
	if dirty {
		t.Fatal("expected clean migration state, got dirty=true")
	}
}

func TestMainTablesExist(t *testing.T) {
	pool := openTestPool(t)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	expectedTables := []string{
		"users",
		"visitor_sessions",
		"events",
		"ticket_types",
		"seats",
		"reservations",
		"reservation_items",
		"reservation_seats",
	}

	for _, tableName := range expectedTables {
		t.Run(tableName, func(t *testing.T) {
			var exists bool
			err := pool.QueryRow(
				ctx,
				`
				SELECT EXISTS (
					SELECT 1
					FROM information_schema.tables
					WHERE table_schema = 'public'
					  AND table_name = $1
				)
				`,
				tableName,
			).Scan(&exists)
			if err != nil {
				t.Fatalf("check table existence: %v", err)
			}
			if !exists {
				t.Fatalf("expected table %q to exist", tableName)
			}
		})
	}
}

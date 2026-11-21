// Package pgtest starts a throwaway PostgreSQL for integration tests of the
// packages above storage (httpapi, service). It needs Docker; callers keep
// their tests behind the "integration" build tag.
//
// Tests inside package postgres cannot use it (import cycle) and keep their
// own helper.
package pgtest

import (
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/sergezossimov0/meters/internal/storage/postgres"
)

// Start runs PostgreSQL 16, applies the migrations and returns a pool. The
// container and the pool are cleaned up with the test.
func Start(t *testing.T) *pgxpool.Pool {
	t.Helper()
	ctx := t.Context()

	ctr, err := tcpostgres.Run(ctx, "postgres:16-alpine",
		tcpostgres.WithDatabase("meters"),
		tcpostgres.BasicWaitStrategies(),
	)
	testcontainers.CleanupContainer(t, ctr)
	if err != nil {
		t.Fatalf("start postgres: %v", err)
	}
	dsn, err := ctr.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("connection string: %v", err)
	}
	pool, err := postgres.Open(ctx, dsn)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(pool.Close)
	if _, err := postgres.Migrate(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return pool
}

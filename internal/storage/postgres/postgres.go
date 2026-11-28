// Package postgres implements the storage on PostgreSQL through pgx/v5.
package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/pressly/goose/v3/lock"

	"github.com/sergezossimov0/meters/migrations"
)

// pingTimeout bounds the connectivity check in Open.
const pingTimeout = 5 * time.Second

// Open creates a connection pool and checks that the database is reachable.
// The DSN may omit the password: pgx reads PGPASSWORD on its own.
func Open(ctx context.Context, dsn string) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		// pgx error text may quote the DSN, so it is not wrapped.
		return nil, fmt.Errorf("parse database DSN: invalid format")
	}
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("create pool: %w", err)
	}

	pingCtx, cancel := context.WithTimeout(ctx, pingTimeout)
	defer cancel()
	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping database: %w", err)
	}
	return pool, nil
}

// newMigrator builds a goose provider over the embedded migrations. The
// returned close function releases the database/sql wrapper, not the pool.
func newMigrator(pool *pgxpool.Pool) (*goose.Provider, func() error, error) {
	locker, err := lock.NewPostgresSessionLocker()
	if err != nil {
		return nil, nil, fmt.Errorf("create migration lock: %w", err)
	}
	db := stdlib.OpenDBFromPool(pool)
	provider, err := goose.NewProvider(goose.DialectPostgres, db, migrations.FS,
		goose.WithSessionLocker(locker), // advisory lock: concurrent server starts are safe
	)
	if err != nil {
		_ = db.Close()
		return nil, nil, fmt.Errorf("create migrator: %w", err)
	}
	return provider, db.Close, nil
}

// Migrate applies all pending migrations and returns how many were applied.
func Migrate(ctx context.Context, pool *pgxpool.Pool) (int, error) {
	provider, closeDB, err := newMigrator(pool)
	if err != nil {
		return 0, err
	}
	defer func() { _ = closeDB() }()

	results, err := provider.Up(ctx)
	if err != nil {
		return len(results), fmt.Errorf("apply migrations: %w", err)
	}
	return len(results), nil
}

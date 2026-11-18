//go:build integration

package postgres

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
)

// startPostgres runs a throwaway PostgreSQL 16 and returns a pool to it.
func startPostgres(t *testing.T) *pgxpool.Pool {
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

	pool, err := Open(ctx, dsn)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func countRows(t *testing.T, pool *pgxpool.Pool, table string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(t.Context(), "SELECT count(*) FROM "+table).Scan(&n); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return n
}

func TestMigrate_UpIdempotentDownUp(t *testing.T) {
	pool := startPostgres(t)
	ctx := t.Context()

	applied, err := Migrate(ctx, pool)
	if err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if applied != 2 {
		t.Errorf("applied = %d, want 2", applied)
	}
	if got := countRows(t, pool, "meter_types"); got != 4 {
		t.Errorf("meter_types = %d, want 4", got)
	}
	if got := countRows(t, pool, "meter_subtypes"); got != 8 {
		t.Errorf("meter_subtypes = %d, want 8", got)
	}

	// A restart applies nothing.
	if applied, err = Migrate(ctx, pool); err != nil || applied != 0 {
		t.Fatalf("second Migrate: applied=%d err=%v", applied, err)
	}

	// Down to zero and up again: Down sections are correct.
	provider, closeDB, err := newMigrator(pool)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = closeDB() }()
	if _, err := provider.DownTo(ctx, 0); err != nil {
		t.Fatalf("DownTo(0): %v", err)
	}
	var tables int
	err = pool.QueryRow(ctx,
		`SELECT count(*) FROM information_schema.tables
		 WHERE table_schema = 'public' AND table_name <> 'goose_db_version'`).Scan(&tables)
	if err != nil || tables != 0 {
		t.Fatalf("tables after DownTo(0) = %d, err = %v", tables, err)
	}
	if applied, err = Migrate(ctx, pool); err != nil || applied != 2 {
		t.Fatalf("Migrate after down: applied=%d err=%v", applied, err)
	}
}

func TestMigrate_Concurrent(t *testing.T) {
	pool := startPostgres(t)
	ctx := t.Context()

	// Two servers starting at once: the advisory lock serializes them.
	errs := make(chan error, 2)
	total := make(chan int, 2)
	for range 2 {
		go func() {
			n, err := Migrate(ctx, pool)
			total <- n
			errs <- err
		}()
	}
	sum := 0
	for range 2 {
		if err := <-errs; err != nil {
			t.Fatalf("Migrate: %v", err)
		}
		sum += <-total
	}
	if sum != 2 {
		t.Errorf("migrations applied in total = %d, want 2", sum)
	}
}

func TestSchemaConstraints(t *testing.T) {
	pool := startPostgres(t)
	ctx := t.Context()
	if _, err := Migrate(ctx, pool); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	mustExec := func(sql string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, sql, args...); err != nil {
			t.Fatalf("%s: %v", sql, err)
		}
	}
	mustFail := func(name, sql string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, sql, args...); err == nil {
			t.Errorf("%s: want constraint violation", name)
		}
	}

	mustExec(`INSERT INTO gateways (name, key_hash) VALUES ('gw', '\x01')`)
	mustExec(`INSERT INTO devices (serial, model, status, gateway_id) VALUES ('WM-1', 'X1', 'pending', 1)`)

	mustFail("active device without owner",
		`INSERT INTO devices (serial, model, status, gateway_id) VALUES ('WM-2', 'X1', 'active', 1)`)
	mustFail("unknown device status",
		`INSERT INTO devices (serial, model, status, gateway_id) VALUES ('WM-3', 'X1', 'broken', 1)`)
	mustFail("battery over 100",
		`INSERT INTO device_status (device_id, battery_pct, gateway_id, reported_at) VALUES (1, 101, 1, now())`)
	mustFail("unknown anomaly code",
		`INSERT INTO anomalies (device_id, code, started_at) VALUES (1, 'weird', now())`)

	mustExec(`INSERT INTO anomalies (device_id, code, started_at) VALUES (1, 'no_data', now())`)
	mustFail("second open anomaly with the same code",
		`INSERT INTO anomalies (device_id, code, started_at) VALUES (1, 'no_data', now())`)
	mustFail("resolved without resolved_at",
		`UPDATE anomalies SET status = 'resolved' WHERE device_id = 1`)
	mustExec(`UPDATE anomalies SET status = 'resolved', resolved_at = now() WHERE device_id = 1`)
	mustExec(`INSERT INTO anomalies (device_id, code, started_at) VALUES (1, 'no_data', now())`)

	mustFail("free memory over total", `INSERT INTO gateway_status
		(gateway_id, boot_id, seq, sent_at, report_interval_s, uptime_s, buffered_readings, devices_heard,
		 readings_collected, batches_sent, send_errors, send_retries, mem_total_bytes, mem_free_bytes,
		 cpu_pct, disk_free_bytes, heap_alloc_bytes, goroutines)
		VALUES (1, gen_random_uuid(), 1, now(), 10, 0, 0, 0, 0, 0, 0, 0, 100, 200, '{1.5}', 0, 0, 1)`)
}

func TestOpen_Unreachable(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	// Port 1 on localhost: connection refused immediately.
	if _, err := Open(ctx, "postgres://127.0.0.1:1/meters?sslmode=disable&connect_timeout=1"); err == nil {
		t.Fatal("want error for unreachable database")
	}
}

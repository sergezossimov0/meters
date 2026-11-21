package postgres

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"testing/synctest"
	"time"

	"github.com/jackc/pgerrcode"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

func TestIsRetryable(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"connection failure 08006", &pgconn.PgError{Code: pgerrcode.ConnectionFailure}, true},
		{"cannot connect now 57P03 is not class 08", &pgconn.PgError{Code: pgerrcode.CannotConnectNow}, false},
		{"admin shutdown 57P01 is not class 08", &pgconn.PgError{Code: pgerrcode.AdminShutdown}, false},
		{"connection exception 08000 wrapped", fmt.Errorf("insert user: %w", &pgconn.PgError{Code: pgerrcode.ConnectionException}), true},
		{"unique violation", &pgconn.PgError{Code: pgerrcode.UniqueViolation}, false},
		{"syntax error", &pgconn.PgError{Code: pgerrcode.SyntaxError}, false},
		{"context canceled", context.Canceled, false},
		{"deadline exceeded", fmt.Errorf("query: %w", context.DeadlineExceeded), false},
		{"plain error", errors.New("boom"), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsRetryable(tt.err); got != tt.want {
				t.Errorf("IsRetryable(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

// A real "connection refused" from pgxpool must be retryable: that is what
// the server sees while PostgreSQL restarts.
func TestIsRetryable_RealConnectionRefused(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, "postgres://127.0.0.1:1/meters?sslmode=disable&connect_timeout=1")
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	err = pool.Ping(ctx)
	if err == nil {
		t.Fatal("want an error from a closed port")
	}
	if !IsRetryable(err) {
		t.Errorf("IsRetryable(%v) = false, want true", err)
	}
}

func TestNewRetrier_LogsEachRetry(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		core, logs := observer.New(zap.WarnLevel)
		r := newRetrier(zap.New(core))
		calls := 0
		err := r.Do(t.Context(), func(context.Context) error {
			calls++
			return &pgconn.PgError{Code: pgerrcode.ConnectionFailure}
		})
		if !IsRetryable(err) || calls != 4 {
			t.Fatalf("err = %v, calls = %d; want the connection error after 4 calls", err, calls)
		}
		if logs.Len() != 3 {
			t.Fatalf("logged %d retries, want 3", logs.Len())
		}
		fields := logs.All()[2].ContextMap()
		if fields["attempt"] != int64(3) || fields["wait"] != 5*time.Second {
			t.Errorf("last retry fields = %v, want attempt 3, wait 5s", fields)
		}
	})
}

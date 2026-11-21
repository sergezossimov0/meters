package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgerrcode"
	"github.com/jackc/pgx/v5/pgconn"
	"go.uber.org/zap"

	"github.com/sergezossimov0/meters/internal/retry"
)

// IsRetryable reports whether err means the connection to PostgreSQL failed,
// so the whole operation may be repeated (spec п. 10.3):
//   - a server error of class 08 Connection Exception;
//   - a failure to connect at all (server down, refused, DNS);
//   - an error pgconn knows happened before anything reached the server.
//
// Cancellation and deadlines are never retried: the caller has given up.
func IsRetryable(err error) bool {
	if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	if pgErr, ok := errors.AsType[*pgconn.PgError](err); ok {
		return pgerrcode.IsConnectionException(pgErr.Code)
	}
	if _, ok := errors.AsType[*pgconn.ConnectError](err); ok {
		return true
	}
	return pgconn.SafeToRetry(err)
}

// newRetrier returns the retrier used around every repository operation.
func newRetrier(log *zap.Logger) retry.Retrier {
	return retry.Retrier{
		Retryable: IsRetryable,
		OnRetry: func(attempt int, wait time.Duration, err error) {
			log.Warn("database unavailable, retrying",
				zap.Int("attempt", attempt),
				zap.Int("max_attempts", retry.Attempts),
				zap.Duration("wait", wait),
				zap.Error(err))
		},
	}
}

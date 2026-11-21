package postgres

import (
	"context"
	"errors"
	"io"
	"net"
	"time"

	"github.com/jackc/pgerrcode"
	"github.com/jackc/pgx/v5/pgconn"
	"go.uber.org/zap"

	"github.com/sergezossimov0/meters/internal/retry"
)

// IsRetryable reports whether err means the connection to PostgreSQL was
// lost or could not be made, so the whole operation may be repeated (spec
// п. 10.3):
//   - a server error of class 08 Connection Exception;
//   - 57P01 admin_shutdown, 57P02 crash_shutdown, 57P03 cannot_connect_now:
//     the server is restarting or terminated the session (a failover, a
//     restart, pg_terminate_backend);
//   - a failure to connect at all (server down, refused, DNS);
//   - the connection broke during the exchange (EOF, a network error): pgx
//     reports these without an SQLSTATE;
//   - an error pgconn knows happened before anything reached the server.
//
// A broken connection rolls back an open transaction, so repeating it is
// safe; a lost acknowledgement of COMMIT is covered by each repository (the
// batch replays, a unique key reports a conflict). Cancellation and
// deadlines are never retried: the caller has given up.
func IsRetryable(err error) bool {
	if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	if pgErr, ok := errors.AsType[*pgconn.PgError](err); ok {
		switch pgErr.Code {
		case pgerrcode.AdminShutdown, pgerrcode.CrashShutdown, pgerrcode.CannotConnectNow:
			return true
		}
		return pgerrcode.IsConnectionException(pgErr.Code)
	}
	if _, ok := errors.AsType[*pgconn.ConnectError](err); ok {
		return true
	}
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return true
	}
	if _, ok := errors.AsType[*net.OpError](err); ok {
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

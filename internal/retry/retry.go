// Package retry repeats an operation after transient failures with the fixed
// pauses from the spec: 1 s, 3 s, 5 s (three retries after the first attempt).
// Shared by the server (database) and the gateway (sending to the server).
package retry

import (
	"context"
	"errors"
	"time"
)

// delays are the pauses before the 2nd, 3rd and 4th attempts.
var delays = [...]time.Duration{1 * time.Second, 3 * time.Second, 5 * time.Second}

// Attempts is the maximum number of calls Do makes.
const Attempts = len(delays) + 1

// Retrier decides which errors are worth retrying.
type Retrier struct {
	// Retryable reports whether err is transient. Nil means nothing is.
	Retryable func(err error) bool
	// OnRetry, if set, is called before each pause, e.g. to log the failure.
	// attempt is the number of the attempt that has just failed (1-based).
	OnRetry func(attempt int, wait time.Duration, err error)
}

// Do calls fn until it succeeds, fails with a non-retryable error or the
// attempts run out; then it returns fn's last error. If ctx is cancelled
// during a pause, Do stops and returns ctx.Err() joined with that error.
func (r Retrier) Do(ctx context.Context, fn func(ctx context.Context) error) error {
	err := fn(ctx)
	for i, wait := range delays {
		if err == nil || r.Retryable == nil || !r.Retryable(err) {
			return err
		}
		if r.OnRetry != nil {
			r.OnRetry(i+1, wait, err)
		}
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return errors.Join(ctx.Err(), err)
		case <-timer.C:
		}
		err = fn(ctx)
	}
	return err
}

package retry

import (
	"context"
	"errors"
	"slices"
	"testing"
	"testing/synctest"
	"time"
)

var (
	errTransient = errors.New("transient")
	errFatal     = errors.New("fatal")
)

func isTransient(err error) bool { return errors.Is(err, errTransient) }

// failing returns an fn that fails with errs in order, then succeeds, and
// records the virtual time of every call.
func failing(calls *[]time.Duration, start time.Time, errs ...error) func(context.Context) error {
	return func(context.Context) error {
		*calls = append(*calls, time.Since(start))
		if n := len(*calls); n <= len(errs) {
			return errs[n-1]
		}
		return nil
	}
}

func durations(ds ...int) []time.Duration {
	out := make([]time.Duration, len(ds))
	for i, d := range ds {
		out[i] = time.Duration(d) * time.Second
	}
	return out
}

func TestDo(t *testing.T) {
	tests := []struct {
		name      string
		errs      []error
		wantErr   error
		wantCalls []time.Duration // virtual time of each attempt
		wantRetry []int           // attempts reported to OnRetry
	}{
		{name: "first attempt succeeds", wantCalls: durations(0)},
		{
			name:      "succeeds on the third attempt",
			errs:      []error{errTransient, errTransient},
			wantCalls: durations(0, 1, 4),
			wantRetry: []int{1, 2},
		},
		{
			name:      "all four attempts fail",
			errs:      []error{errTransient, errTransient, errTransient, errTransient},
			wantErr:   errTransient,
			wantCalls: durations(0, 1, 4, 9),
			wantRetry: []int{1, 2, 3},
		},
		{
			name:      "non-retryable error stops at once",
			errs:      []error{errFatal},
			wantErr:   errFatal,
			wantCalls: durations(0),
		},
		{
			name:      "non-retryable error after a retry",
			errs:      []error{errTransient, errFatal},
			wantErr:   errFatal,
			wantCalls: durations(0, 1),
			wantRetry: []int{1},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				var calls []time.Duration
				var retries []int
				r := Retrier{
					Retryable: isTransient,
					OnRetry:   func(attempt int, _ time.Duration, _ error) { retries = append(retries, attempt) },
				}
				err := r.Do(t.Context(), failing(&calls, time.Now(), tt.errs...))
				if !errors.Is(err, tt.wantErr) || (tt.wantErr == nil && err != nil) {
					t.Errorf("err = %v, want %v", err, tt.wantErr)
				}
				if !slices.Equal(calls, tt.wantCalls) {
					t.Errorf("attempts at %v, want %v", calls, tt.wantCalls)
				}
				if !slices.Equal(retries, tt.wantRetry) {
					t.Errorf("OnRetry attempts = %v, want %v", retries, tt.wantRetry)
				}
			})
		})
	}
}

func TestDo_CancelDuringPause(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		start := time.Now()
		calls := 0
		done := make(chan error, 1)
		go func() {
			done <- Retrier{Retryable: isTransient}.Do(ctx, func(context.Context) error {
				calls++
				return errTransient
			})
		}()

		time.Sleep(2 * time.Second) // inside the 3 s pause before the 3rd attempt
		cancel()
		err := <-done

		if !errors.Is(err, context.Canceled) || !errors.Is(err, errTransient) {
			t.Errorf("err = %v, want context.Canceled joined with the last error", err)
		}
		if calls != 2 {
			t.Errorf("calls = %d, want 2", calls)
		}
		if elapsed := time.Since(start); elapsed != 2*time.Second {
			t.Errorf("returned after %v, want right at cancel (2s)", elapsed)
		}
	})
}

func TestDo_NilRetryableNeverRetries(t *testing.T) {
	calls := 0
	err := Retrier{}.Do(t.Context(), func(context.Context) error {
		calls++
		return errTransient
	})
	if !errors.Is(err, errTransient) || calls != 1 {
		t.Errorf("err = %v, calls = %d; want the error after one call", err, calls)
	}
}

func TestAttempts(t *testing.T) {
	if Attempts != 4 {
		t.Errorf("Attempts = %d, want 4 (first try + 1/3/5 s retries)", Attempts)
	}
}

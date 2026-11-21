package gateway

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"go.uber.org/zap"

	"github.com/sergezossimov0/meters/internal/domain"
)

// fakeSender plays the server for Sender: it takes delay per request,
// answers with the result of answer and records what it got.
type fakeSender struct {
	delay  time.Duration
	answer func(call int, req domain.BatchRequest) error

	mu       sync.Mutex
	calls    int
	inFlight int
	maxIn    int
	ids      []string
	seen     map[string]int // reading -> times delivered successfully
	active   map[string]bool
	overlap  bool
}

func (f *fakeSender) SendBatch(ctx context.Context, req domain.BatchRequest) (domain.BatchResult, error) {
	f.mu.Lock()
	f.calls++
	call := f.calls
	f.inFlight++
	f.maxIn = max(f.maxIn, f.inFlight)
	f.ids = append(f.ids, req.BatchID)
	if f.active == nil {
		f.active, f.seen = map[string]bool{}, map[string]int{}
	}
	keys := flat(req)
	for _, k := range keys {
		if f.active[k] {
			f.overlap = true // the same reading in two batches at once
		}
		f.active[k] = true
	}
	f.mu.Unlock()

	var err error
	select {
	case <-time.After(f.delay):
		if f.answer != nil {
			err = f.answer(call, req)
		}
	case <-ctx.Done():
		err = ctx.Err()
	}

	f.mu.Lock()
	defer f.mu.Unlock()
	f.inFlight--
	for _, k := range keys {
		delete(f.active, k)
		if err == nil {
			f.seen[k]++
		}
	}
	return domain.BatchResult{Accepted: len(keys), Rejected: []domain.Rejected{}}, err
}

// stats reads the counters under the lock, after everything has settled.
func (f *fakeSender) stats() (calls, maxIn int, ids []string, overlap bool, seen map[string]int) {
	synctest.Wait()
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls, f.maxIn, append([]string(nil), f.ids...), f.overlap, f.seen
}

// fill adds n distinct readings numbered from 'from'.
func fill(buf *Buffer, from, n int) {
	for i := from; i < from+n; i++ {
		buf.Add(rdg(fmt.Sprintf("D%d", i%7), i, int64(i)))
	}
}

// runSender starts s and returns a stop function that ends the dispatching
// and waits for the workers.
func runSender(t *testing.T, s *Sender) (stop func()) {
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() { s.Run(ctx, t.Context()); close(done) }()
	return func() { cancel(); <-done }
}

// No more than -l requests at once, every reading delivered exactly once,
// never in two batches at the same time.
func TestSender_PoolLimitAndExactlyOnce(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		buf := NewBuffer(nil)
		fill(buf, 0, 1000)
		srv := &fakeSender{delay: 4 * time.Second} // slower than the interval
		s := NewSender(buf, srv, 50, 2*time.Second, 3, nil, zap.NewNop())
		stop := runSender(t, s)

		for i := range 5 { // producers keep adding while sends are slow
			time.Sleep(3 * time.Second)
			fill(buf, 1000+100*i, 100)
		}
		time.Sleep(2 * time.Minute)
		stop()

		_, maxIn, _, overlap, seen := srv.stats()
		if maxIn != 3 {
			t.Errorf("max %d requests at once, want 3", maxIn)
		}
		if overlap {
			t.Error("a reading was in two batches at the same time")
		}
		if buf.Len() != 0 || len(seen) != 1500 {
			t.Errorf("%d readings left, %d delivered; want 0 and 1500", buf.Len(), len(seen))
		}
		for k, n := range seen {
			if n != 1 {
				t.Errorf("%s delivered %d times", k, n)
			}
		}
	})
}

func TestSender_Outcomes(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		wantLeft int
	}{
		{"poison 400 drops", fmt.Errorf("%w: 400", ErrPoison), 0},
		{"auth 401 keeps", fmt.Errorf("%w: 401", ErrAuth), 10},
		{"down after retries keeps", fmt.Errorf("%w: 503", errTransient), 10},
		{"unexpected 404 keeps", fmt.Errorf("unexpected answer 404"), 10},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				buf := NewBuffer(nil)
				fill(buf, 0, 10)
				srv := &fakeSender{answer: func(int, domain.BatchRequest) error { return tt.err }}
				var changed atomic.Int32
				s := NewSender(buf, srv, 100, 10*time.Second, 2, func() { changed.Add(1) }, zap.NewNop())
				stop := runSender(t, s)
				time.Sleep(15 * time.Second) // one round
				stop()
				if buf.Len() != tt.wantLeft {
					t.Errorf("%d readings left, want %d", buf.Len(), tt.wantLeft)
				}
				if (tt.wantLeft == 0) != (changed.Load() == 1) {
					t.Errorf("changed called %d times", changed.Load())
				}
				if calls, _, _, _, _ := srv.stats(); calls != 1 {
					t.Errorf("%d sends in one round, want 1: a failure ends the round", calls)
				}
			})
		})
	}
}

// Kept readings go out in the next round under a new batch_id; a round
// that fails stops instead of hammering the server.
func TestSender_FailedRoundStopsAndRetriesNextRound(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		buf := NewBuffer(nil)
		fill(buf, 0, 100)
		srv := &fakeSender{answer: func(call int, _ domain.BatchRequest) error {
			if call <= 2 {
				return fmt.Errorf("%w: 503", errTransient)
			}
			return nil
		}}
		s := NewSender(buf, srv, 10, 10*time.Second, 1, nil, zap.NewNop())
		stop := runSender(t, s)

		time.Sleep(15 * time.Second) // round 1: one failure, stop
		if calls, _, _, _, _ := srv.stats(); calls != 1 || buf.Len() != 100 {
			t.Fatalf("round 1: %d calls, %d left; want 1 and 100", calls, buf.Len())
		}
		time.Sleep(10 * time.Second) // round 2: fails again
		if calls, _, _, _, _ := srv.stats(); calls != 2 {
			t.Fatalf("round 2: %d calls", calls)
		}
		time.Sleep(10 * time.Second) // round 3: server is back, all 10 batches
		stop()
		calls, _, ids, _, _ := srv.stats()
		if buf.Len() != 0 || calls != 12 {
			t.Errorf("after recovery: %d calls, %d left; want 12 and 0", calls, buf.Len())
		}
		if ids[0] == ids[1] || ids[1] == ids[2] {
			t.Errorf("a resend must use a new batch_id: %v", ids[:3])
		}
	})
}

// On shutdown the running sends finish under sendCtx; cancelling it keeps
// their readings for the file.
func TestSender_Shutdown(t *testing.T) {
	for _, abort := range []bool{false, true} {
		t.Run(fmt.Sprintf("abort=%v", abort), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				buf := NewBuffer(nil)
				fill(buf, 0, 30)
				srv := &fakeSender{delay: 20 * time.Second}
				s := NewSender(buf, srv, 10, time.Second, 2, nil, zap.NewNop())
				ctx, cancel := context.WithCancel(t.Context())
				sendCtx, cancelSends := context.WithCancel(t.Context())
				defer cancelSends()
				done := make(chan struct{})
				go func() { s.Run(ctx, sendCtx); close(done) }()

				time.Sleep(2 * time.Second) // two sends running, a third batch waiting
				cancel()
				if abort {
					cancelSends()
				}
				<-done
				want := 10 // the third batch was released, not sent
				if abort {
					want = 30
				}
				if calls, _, _, _, _ := srv.stats(); buf.Len() != want || calls != 2 {
					t.Errorf("%d readings left after %d sends, want %d after 2", buf.Len(), calls, want)
				}
				if _, ok := buf.Take(100); !ok {
					t.Error("kept readings must be free, not in flight")
				}
			})
		})
	}
}

func TestNewSender_Defaults(t *testing.T) {
	s := NewSender(NewBuffer(nil), &fakeSender{}, 1, time.Second, 0, nil, zap.NewNop())
	if s.workers != 1 {
		t.Errorf("workers = %d, want at least 1", s.workers)
	}
	s.changed() // nil is replaced by a no-op
}

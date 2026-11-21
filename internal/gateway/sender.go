package gateway

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"go.uber.org/zap"

	"github.com/sergezossimov0/meters/internal/domain"
)

// BatchSender delivers a batch to the server; *Client implements it.
type BatchSender interface {
	SendBatch(ctx context.Context, req domain.BatchRequest) (domain.BatchResult, error)
}

// Sender takes batches from the buffer every interval and hands them to a
// pool of workers, at most workers requests at a time (spec п. 7.2, -l).
type Sender struct {
	buf       *Buffer
	client    BatchSender
	batchSize int
	interval  time.Duration
	workers   int
	changed   func() // called after readings left the buffer (-i 0 saves)
	log       *zap.Logger

	failed atomic.Bool // a send of the current round failed
}

// NewSender wires a sender. changed may be nil.
func NewSender(buf *Buffer, client BatchSender, batchSize int, interval time.Duration, workers int,
	changed func(), log *zap.Logger) *Sender {
	if changed == nil {
		changed = func() {}
	}
	return &Sender{buf: buf, client: client, batchSize: batchSize, interval: interval,
		workers: max(workers, 1), changed: changed, log: log}
}

// Run dispatches batches until ctx is done, then waits for the workers to
// finish the sends they have. Those sends run under sendCtx: the caller
// cancels it when it no longer wants to wait (graceful shutdown).
func (s *Sender) Run(ctx, sendCtx context.Context) {
	jobs := make(chan Batch) // unbuffered: a batch is taken only for a free worker
	var wg sync.WaitGroup
	for range s.workers {
		wg.Go(func() {
			for b := range jobs {
				if s.failed.Load() {
					// The dispatcher took it before a send of this round
					// failed: leave it for the next round.
					s.buf.Release(b.ID)
					continue
				}
				s.send(sendCtx, b)
			}
		})
	}
	defer func() {
		close(jobs)
		wg.Wait()
	}()

	t := time.NewTicker(s.interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if !s.dispatch(ctx, jobs) {
				return
			}
		}
	}
}

// dispatch is one round: batches go to the workers while the buffer has
// free readings. After a failed send the round stops: the released readings
// wait for the next round (spec п. 7.2) instead of hammering a server that
// is down. It returns false when ctx is done.
func (s *Sender) dispatch(ctx context.Context, jobs chan<- Batch) bool {
	s.failed.Store(false)
	for !s.failed.Load() {
		b, ok := s.buf.Take(s.batchSize)
		if !ok {
			return true
		}
		select {
		case jobs <- b:
		case <-ctx.Done():
			s.buf.Release(b.ID)
			return false
		}
	}
	return true
}

// send delivers one batch and settles its readings by the outcome
// (spec п. 7.4).
func (s *Sender) send(ctx context.Context, b Batch) {
	log := s.log.With(zap.String("batch_id", b.ID), zap.Int("readings", b.Size))
	res, err := s.client.SendBatch(ctx, b.Request)
	switch {
	case err == nil:
		s.buf.Ack(b.ID)
		s.changed()
		log.Info("batch sent", zap.Int("accepted", res.Accepted),
			zap.Int("duplicates", res.Duplicates), zap.Int("rejected", len(res.Rejected)))
	case errors.Is(err, ErrPoison):
		// The server will never take this batch: drop it, or it would be
		// sent forever.
		s.buf.Ack(b.ID)
		s.changed()
		log.Error("batch refused by the server, readings dropped", zap.Error(err))
	default:
		// ErrAuth, a failure after all repeats, an unexpected answer or a
		// shutdown: keep the readings for a later round.
		s.buf.Release(b.ID)
		s.failed.Store(true)
		if errors.Is(err, ErrAuth) {
			log.Error("gateway refused by the server, readings kept", zap.Error(err))
		} else {
			log.Warn("batch not sent, readings kept", zap.Error(err))
		}
	}
}

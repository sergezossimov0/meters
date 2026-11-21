package gateway

import (
	"context"
	"fmt"
	"sync"
	"time"

	"go.uber.org/zap"

	"github.com/sergezossimov0/meters/internal/config"
)

// ShutdownGrace is how long running sends may finish after a stop signal.
// Docker gives the gateway 15 s (stop_grace_period) before SIGKILL; the rest
// is for saving the buffer.
const ShutdownGrace = 10 * time.Second

// Run starts the simulator and blocks until ctx is done (spec п. 7.2):
// device polling every -p, the buffer file every -i, sending every -r
// through -l workers. On shutdown it stops polling and dispatching, lets
// running sends finish for ShutdownGrace, then cancels them and saves the
// buffer. The error is a start failure or a failed final save.
func Run(ctx context.Context, cfg config.Gateway, log *zap.Logger) error {
	var (
		state    State
		restored bool
	)
	if cfg.Restore {
		var err error
		if state, restored, err = Load(cfg.FileStoragePath); err != nil {
			return err
		}
	}
	clock := NewClock(cfg.Backfill, cfg.Speed)
	if restored {
		clock = ResumeClock(state.ModelTime, cfg.Speed)
	}
	fleet := NewFleet(cfg.GatewayKey.Value(), cfg.Devices, clock.Now(), state.Devices)
	buf := NewBuffer(state.Readings)
	src := NewSource(clock, fleet, buf)
	saver := NewSaver(cfg.FileStoragePath, cfg.StoreInterval, src, log)
	client := NewClient(cfg.ServerURL, cfg.GatewayKey.Value(), []byte(cfg.Key.Value()), nil, log)
	sender := NewSender(buf, client, cfg.BatchSize, cfg.ReportInterval, cfg.RateLimit, saver.Changed, log)

	log.Info("simulator started",
		zap.Bool("restored", restored),
		zap.Int("buffered_readings", buf.Len()),
		zap.Time("model_time", clock.Now()),
		zap.Int("devices", cfg.Devices))

	// Sends outlive ctx: they get ShutdownGrace after it is done.
	sendCtx, cancelSends := context.WithCancel(context.WithoutCancel(ctx))
	defer cancelSends()

	var wg sync.WaitGroup
	wg.Go(func() { poll(ctx, cfg.PollInterval, src, clock, saver, log) })
	wg.Go(func() { saver.Run(ctx) })
	sent := make(chan struct{})
	go func() { sender.Run(ctx, sendCtx); close(sent) }()

	<-ctx.Done()
	log.Info("stopping simulator", zap.Duration("grace", ShutdownGrace))
	grace := time.NewTimer(ShutdownGrace)
	select {
	case <-sent:
		grace.Stop()
	case <-grace.C:
		log.Warn("sends did not finish in time, cancelling them")
		cancelSends()
		<-sent
	}
	wg.Wait()

	if err := saver.Save(); err != nil {
		return fmt.Errorf("final save: %w", err)
	}
	log.Info("simulator stopped", zap.Int("buffered_readings", buf.Len()), zap.Time("model_time", clock.Now()))
	return nil
}

// poll collects readings every interval until ctx is done.
func poll(ctx context.Context, interval time.Duration, src *Source, clock *Clock, saver *Saver, log *zap.Logger) {
	t := time.NewTicker(interval)
	defer t.Stop()
	caughtUp := clock.CaughtUp()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		if n := src.Poll(); n > 0 {
			saver.Changed()
			log.Debug("devices polled", zap.Int("readings", n))
		}
		if !caughtUp && clock.CaughtUp() {
			caughtUp = true
			log.Info("model time caught up with real time")
		}
	}
}

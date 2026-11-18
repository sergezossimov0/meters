// Command gateway simulates a LPWAN gateway: it models meters, buffers their
// readings on disk and sends them, together with heartbeats, to the server.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"go.uber.org/zap"

	"github.com/sergezossimov0/meters/internal/app"
	"github.com/sergezossimov0/meters/internal/config"
	"github.com/sergezossimov0/meters/internal/logger"
)

// version is set at build time via -ldflags "-X main.version=...".
var version = "dev"

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	err := run(ctx, os.Args[1:], os.LookupEnv)
	stop()
	os.Exit(app.ExitCode("gateway", err, os.Stdout, os.Stderr))
}

// run is a skeleton until L3: it loads the config and waits for a signal.
// The simulator, buffer and sender will run in an errgroup here.
func run(ctx context.Context, args []string, lookupEnv config.LookupEnv) error {
	cfg, err := config.LoadGateway(args, lookupEnv)
	if err != nil {
		return err
	}
	log, err := logger.New(cfg.LogLevel)
	if err != nil {
		return fmt.Errorf("create logger: %w", err)
	}
	defer func() { _ = log.Sync() }()
	log.Info("starting gateway", zap.String("version", version), zap.Stringer("config", cfg))

	<-ctx.Done()
	log.Info("gateway stopped")
	return nil
}

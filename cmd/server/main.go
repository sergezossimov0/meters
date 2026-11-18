// Command server accepts meter readings from gateways, stores them in
// PostgreSQL and serves the customer/admin API and the status page.
package main

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/signal"
	"syscall"

	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"

	"github.com/sergezossimov0/meters/internal/app"
	"github.com/sergezossimov0/meters/internal/config"
	"github.com/sergezossimov0/meters/internal/httpapi"
	"github.com/sergezossimov0/meters/internal/logger"
	"github.com/sergezossimov0/meters/internal/storage/postgres"
)

// version is set at build time via -ldflags "-X main.version=...".
var version = "dev"

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	err := run(ctx, os.Args[1:], os.LookupEnv)
	stop()
	os.Exit(app.ExitCode("server", err, os.Stdout, os.Stderr))
}

// run starts the server and blocks until ctx is cancelled or a component
// fails. Background workers (L6 anomalies, L7 retention) join the errgroup.
func run(ctx context.Context, args []string, lookupEnv config.LookupEnv) error {
	cfg, err := config.LoadServer(args, lookupEnv)
	if err != nil {
		return err
	}
	log, err := logger.New(cfg.LogLevel)
	if err != nil {
		return fmt.Errorf("create logger: %w", err)
	}
	defer func() { _ = log.Sync() }()
	log.Info("starting server", zap.String("version", version), zap.Stringer("config", cfg))

	pool, err := postgres.Open(ctx, cfg.DatabaseDSN.Value())
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer pool.Close()

	applied, err := postgres.Migrate(ctx, pool)
	if err != nil {
		return err
	}
	log.Info("migrations applied", zap.Int("count", applied))

	var lc net.ListenConfig
	ln, err := lc.Listen(ctx, "tcp", cfg.Address)
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	log.Info("listening", zap.Stringer("addr", ln.Addr()))

	srv := app.NewHTTPServer(httpapi.NewRouter(httpapi.Deps{
		Log:          log,
		DB:           pool,
		MaxBodyBytes: cfg.MaxBodyBytes,
		Version:      version,
	}))

	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() error { return app.ServeHTTP(gctx, srv, ln, app.ShutdownTimeout, log) })
	err = g.Wait()
	log.Info("shutdown complete", zap.Error(err))
	return err
}

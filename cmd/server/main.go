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
	"golang.org/x/crypto/bcrypt"
	"golang.org/x/sync/errgroup"

	"github.com/sergezossimov0/meters/internal/app"
	"github.com/sergezossimov0/meters/internal/auth"
	"github.com/sergezossimov0/meters/internal/config"
	"github.com/sergezossimov0/meters/internal/httpapi"
	"github.com/sergezossimov0/meters/internal/logger"
	"github.com/sergezossimov0/meters/internal/service"
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

	passwords, err := auth.NewPasswords(bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	tokens, err := auth.NewTokens([]byte(cfg.JWTSecret.Value()), cfg.JWTTTL)
	if err != nil {
		return err
	}
	users := service.NewUsers(postgres.NewUsers(pool, log), passwords, tokens)
	if err := ensureAdmin(ctx, log, users, cfg); err != nil {
		return err
	}

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
		Users:        users,
		Tokens:       tokens,
		MeterTypes:   service.NewMeterTypes(postgres.NewMeterTypes(pool, log)),
	}))

	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() error { return app.ServeHTTP(gctx, srv, ln, app.ShutdownTimeout, log) })
	err = g.Wait()
	log.Info("shutdown complete", zap.Error(err))
	return err
}

// ensureAdmin creates the first administrator when ADMIN_LOGIN is set.
func ensureAdmin(ctx context.Context, log *zap.Logger, users *service.Users, cfg config.Server) error {
	if cfg.AdminLogin == "" {
		return nil
	}
	res, err := users.EnsureAdmin(ctx, cfg.AdminLogin, cfg.AdminPassword.Value())
	if err != nil {
		return fmt.Errorf("first admin: %w", err)
	}
	switch res {
	case service.AdminCreated:
		log.Info("first admin created", zap.String("login", cfg.AdminLogin))
	case service.AdminExists:
		log.Info("first admin already exists, password unchanged", zap.String("login", cfg.AdminLogin))
	}
	return nil
}

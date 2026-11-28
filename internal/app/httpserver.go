// Package app holds the process plumbing shared by the commands: the HTTP
// server lifecycle and the exit-code policy.
package app

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"time"

	"go.uber.org/zap"
)

// HTTP server limits (spec п. 10.1).
const (
	ReadHeaderTimeout = 5 * time.Second
	ReadTimeout       = 30 * time.Second
	WriteTimeout      = 30 * time.Second
	ShutdownTimeout   = 10 * time.Second
)

// NewHTTPServer wraps h in an http.Server with the project's timeouts.
func NewHTTPServer(h http.Handler) *http.Server {
	return &http.Server{
		Handler:           h,
		ReadHeaderTimeout: ReadHeaderTimeout,
		ReadTimeout:       ReadTimeout,
		WriteTimeout:      WriteTimeout,
	}
}

// ServeHTTP serves on ln until ctx is cancelled, then stops accepting
// connections and waits up to shutdownTimeout for in-flight requests. If they
// do not finish in time, the remaining connections are closed forcibly and an
// error is returned. ServeHTTP owns ln and closes it.
func ServeHTTP(ctx context.Context, srv *http.Server, ln net.Listener, shutdownTimeout time.Duration, log *zap.Logger) error {
	served := make(chan error, 1)
	go func() { served <- srv.Serve(ln) }()

	select {
	case err := <-served:
		// Serve returns only on a listener failure here: nobody called Shutdown.
		return fmt.Errorf("http server: %w", err)
	case <-ctx.Done():
	}

	log.Info("shutting down http server", zap.Duration("timeout", shutdownTimeout))
	// ctx is already cancelled; the shutdown needs a fresh deadline of its own.
	shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), shutdownTimeout)
	defer cancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		_ = srv.Close()
		if serveErr := <-served; !errors.Is(serveErr, http.ErrServerClosed) {
			log.Warn("http server stopped with error", zap.Error(serveErr))
		}
		return fmt.Errorf("graceful shutdown: %w", err)
	}
	if err := <-served; !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("http server: %w", err)
	}
	log.Info("http server stopped")
	return nil
}

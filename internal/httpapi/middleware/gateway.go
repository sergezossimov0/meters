package middleware

import (
	"context"
	"errors"
	"net/http"

	"go.uber.org/zap"

	"github.com/sergezossimov0/meters/internal/domain"
)

// GatewayKeyHeader carries the gateway's API key (spec п. 6.1).
const GatewayKeyHeader = "X-Gateway-Key"

// GatewayAuthenticator finds a gateway by its API key; *service.Gateways
// implements it. Errors: domain.ErrNotFound, domain.ErrGatewayBlocked.
type GatewayAuthenticator interface {
	Authenticate(ctx context.Context, key string) (domain.Gateway, error)
}

type gatewayKey struct{}

// GatewayFrom returns the gateway put into the context by GatewayAuth.
func GatewayFrom(ctx context.Context) (domain.Gateway, bool) {
	g, ok := ctx.Value(gatewayKey{}).(domain.Gateway)
	return g, ok
}

// WithGateway returns a context carrying g, as GatewayAuth does. For tests of
// handlers that sit behind GatewayAuth.
func WithGateway(ctx context.Context, g domain.Gateway) context.Context {
	return context.WithValue(ctx, gatewayKey{}, g)
}

// GatewayAuth requires a valid X-Gateway-Key and puts the gateway into the
// request context: missing or unknown key — 401, blocked gateway — 403. It
// runs before VerifyRequest, so an unknown client cannot make the server read
// and hash a large body. The key is never logged.
func GatewayAuth(gateways GatewayAuthenticator, log *zap.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			key := r.Header.Get(GatewayKeyHeader)
			if key == "" {
				WriteError(w, http.StatusUnauthorized, "missing "+GatewayKeyHeader)
				return
			}
			g, err := gateways.Authenticate(r.Context(), key)
			switch {
			case err == nil:
				next.ServeHTTP(w, r.WithContext(WithGateway(r.Context(), g)))
			case errors.Is(err, domain.ErrNotFound):
				log.Debug("gateway key rejected", zap.String("uri", r.URL.Path), zap.String("remote", r.RemoteAddr))
				WriteError(w, http.StatusUnauthorized, "invalid "+GatewayKeyHeader)
			case errors.Is(err, domain.ErrGatewayBlocked):
				WriteError(w, http.StatusForbidden, "gateway is blocked")
			default:
				log.Error("gateway authentication failed", zap.String("uri", r.URL.Path), zap.Error(err))
				WriteError(w, http.StatusInternalServerError, "internal error")
			}
		})
	}
}

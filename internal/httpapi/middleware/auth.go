package middleware

import (
	"context"
	"net/http"
	"strings"

	"go.uber.org/zap"

	"github.com/sergezossimov0/meters/internal/auth"
	"github.com/sergezossimov0/meters/internal/domain"
)

// TokenParser checks a bearer token; *auth.Tokens implements it.
type TokenParser interface {
	Parse(token string) (auth.Claims, error)
}

type claimsKey struct{}

// ClaimsFrom returns the caller's claims put into the context by Authenticate.
func ClaimsFrom(ctx context.Context) (auth.Claims, bool) {
	c, ok := ctx.Value(claimsKey{}).(auth.Claims)
	return c, ok
}

// WithClaims returns a context carrying c, as Authenticate does. For tests of
// handlers that sit behind Authenticate.
func WithClaims(ctx context.Context, c auth.Claims) context.Context {
	return context.WithValue(ctx, claimsKey{}, c)
}

// Authenticate requires "Authorization: Bearer <token>" with a valid token and
// puts its claims into the request context. Otherwise it answers 401 with
// WWW-Authenticate (RFC 6750 §3): no error code when credentials are missing,
// error="invalid_token" when the token is rejected. The token itself is never
// logged.
func Authenticate(tokens TokenParser, log *zap.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			token, ok := bearerToken(r.Header.Get("Authorization"))
			if !ok {
				unauthorized(w, `Bearer`, "authorization required")
				return
			}
			claims, err := tokens.Parse(token)
			if err != nil {
				log.Debug("token rejected", zap.String("uri", r.URL.Path), zap.Error(err))
				unauthorized(w, `Bearer error="invalid_token"`, "invalid or expired token")
				return
			}
			next.ServeHTTP(w, r.WithContext(WithClaims(r.Context(), claims)))
		})
	}
}

// bearerToken extracts the token from an Authorization header value. The
// scheme is case-insensitive (RFC 7235 §2.1).
func bearerToken(header string) (string, bool) {
	scheme, token, ok := strings.Cut(header, " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") {
		return "", false
	}
	token = strings.TrimSpace(token)
	return token, token != ""
}

func unauthorized(w http.ResponseWriter, challenge, msg string) {
	w.Header().Set("WWW-Authenticate", challenge)
	WriteError(w, http.StatusUnauthorized, msg)
}

// RequireRole lets through only callers with the given role; others get 403.
// It must run after Authenticate; without claims in the context it answers
// 401, so a wiring mistake denies access rather than grants it.
func RequireRole(role domain.Role) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			claims, ok := ClaimsFrom(r.Context())
			switch {
			case !ok:
				unauthorized(w, `Bearer`, "authorization required")
			case claims.Role != role:
				WriteError(w, http.StatusForbidden, "access denied")
			default:
				next.ServeHTTP(w, r)
			}
		})
	}
}

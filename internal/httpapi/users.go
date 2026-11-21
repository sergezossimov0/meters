package httpapi

import (
	"context"
	"errors"
	"net/http"

	"go.uber.org/zap"

	"github.com/sergezossimov0/meters/internal/domain"
	"github.com/sergezossimov0/meters/internal/httpapi/middleware"
	"github.com/sergezossimov0/meters/internal/service"
)

// UserService is what the user handlers need; *service.Users implements it.
type UserService interface {
	Register(ctx context.Context, login, password string) (string, error)
	Login(ctx context.Context, login, password string) (string, error)
}

type credentials struct {
	Login    string `json:"login"`
	Password string `json:"password"`
}

type tokenResponse struct {
	Token string `json:"token"`
}

// register: POST /api/user/register → 200 {"token"}; 400 bad input,
// 409 login taken.
func register(log *zap.Logger, users UserService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var c credentials
		if !decodeJSON(w, r, &c) {
			return
		}
		token, err := users.Register(r.Context(), c.Login, c.Password)
		switch {
		case err == nil:
			writeToken(w, log, token)
		case writeInvalid(w, err):
		case errors.Is(err, domain.ErrConflict):
			middleware.WriteError(w, http.StatusConflict, "login is already taken")
		default:
			writeInternal(w, r, log, err)
		}
	}
}

// login: POST /api/user/login → 200 {"token"}; 401 wrong pair.
func login(log *zap.Logger, users UserService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var c credentials
		if !decodeJSON(w, r, &c) {
			return
		}
		token, err := users.Login(r.Context(), c.Login, c.Password)
		switch {
		case err == nil:
			writeToken(w, log, token)
		case errors.Is(err, service.ErrBadCredentials):
			middleware.WriteError(w, http.StatusUnauthorized, "invalid login or password")
		default:
			writeInternal(w, r, log, err)
		}
	}
}

// writeToken answers 200 with the token; no-store keeps it out of caches
// (RFC 6749 §5.1).
func writeToken(w http.ResponseWriter, log *zap.Logger, token string) {
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, log, http.StatusOK, tokenResponse{Token: token})
}

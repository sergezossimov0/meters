// Package httpapi wires the server's HTTP routes.
package httpapi

import (
	"bytes"
	"context"
	"embed"
	"html/template"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"go.uber.org/zap"

	"github.com/sergezossimov0/meters/internal/domain"
	"github.com/sergezossimov0/meters/internal/httpapi/middleware"
	"github.com/sergezossimov0/meters/internal/logger"
)

// pingTimeout bounds the database check behind GET /ping.
const pingTimeout = time.Second

//go:embed templates/*.html
var templatesFS embed.FS

var pages = template.Must(template.ParseFS(templatesFS, "templates/*.html"))

// Pinger reports whether the storage is reachable; *pgxpool.Pool satisfies it.
type Pinger interface {
	Ping(ctx context.Context) error
}

// Deps holds everything the handlers need.
type Deps struct {
	Log          *zap.Logger
	DB           Pinger
	MaxBodyBytes int64
	Version      string
	Users        UserService
	Tokens       middleware.TokenParser
	MeterTypes   MeterTypeService
	Gateways     GatewayService
	SignKey      []byte // HashSHA256 key for /gateway/v1; empty disables signing
	Ingest       IngestService
}

// NewRouter builds the server's handler. Middleware order: logging sees the
// final status and the compressed size; compression wraps everything below.
func NewRouter(d Deps) http.Handler {
	r := chi.NewRouter()
	r.Use(logger.HTTP(d.Log), middleware.Compress, middleware.Decompress(d.MaxBodyBytes))

	r.NotFound(func(w http.ResponseWriter, _ *http.Request) {
		middleware.WriteError(w, http.StatusNotFound, "not found")
	})
	r.MethodNotAllowed(methodNotAllowed)

	r.Get("/ping", ping(d.Log, d.DB))
	r.Get("/", index(d.Log, d.Version))

	r.Route("/api", func(r chi.Router) {
		r.Post("/user/register", register(d.Log, d.Users))
		r.Post("/user/login", login(d.Log, d.Users))

		// Everything below needs a valid token. /api/admin is mounted even
		// while empty, so an unknown admin path answers 401/403, not 404.
		// RequireRole is attached at the mount point (With), not inside the
		// subrouter (Use): chi skips a subrouter's middlewares when no route
		// in it matches, which would turn 403 into 404.
		r.Group(func(r chi.Router) {
			r.Use(middleware.Authenticate(d.Tokens, d.Log))
			r.Get("/meter-types", listMeterTypes(d.Log, d.MeterTypes))

			r.With(middleware.RequireRole(domain.RoleAdmin)).Route("/admin", func(r chi.Router) {
				r.Post("/meter-types", createMeterType(d.Log, d.MeterTypes))

				r.Get("/gateways", listGateways(d.Log, d.Gateways))
				r.Post("/gateways", createGateway(d.Log, d.Gateways))
				r.Post("/gateways/{id}/block", setGatewayBlocked(d.Log, d.Gateways, true))
				r.Post("/gateways/{id}/unblock", setGatewayBlocked(d.Log, d.Gateways, false))
			})
		})
	})

	// Gateways: X-Gateway-Key and HashSHA256 instead of JWT. Order (spec
	// п. 6.1): the response signature is the outer layer, so 401/403/400 are
	// signed too; the key is checked before the body is read and hashed.
	// Attached with With at the mount point for the same reason as RequireRole:
	// an unknown path below still needs the key and gets a signed answer.
	r.With(
		middleware.SignResponse(d.SignKey),
		middleware.GatewayAuth(d.Gateways, d.Log),
		middleware.VerifyRequest(d.SignKey),
	).Route("/gateway/v1", func(r chi.Router) {
		r.Post("/readings", ingestReadings(d.Log, d.Ingest))
		// POST /heartbeat — L4.
	})
	return r
}

// ping answers 200 when the database responds within pingTimeout, else 500.
func ping(log *zap.Logger, db Pinger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), pingTimeout)
		defer cancel()
		if err := db.Ping(ctx); err != nil {
			log.Warn("ping: database unavailable", zap.Error(err))
			middleware.WriteError(w, http.StatusInternalServerError, "database unavailable")
			return
		}
		w.WriteHeader(http.StatusOK)
	}
}

type indexPage struct {
	Title   string
	Version string
}

// index renders the status page. It renders into a buffer first so that a
// template error still yields a clean 500 instead of a half-written page.
func index(log *zap.Logger, version string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		var buf bytes.Buffer
		page := indexPage{Title: "meters — состояние системы", Version: version}
		if err := pages.ExecuteTemplate(&buf, "index.html", page); err != nil {
			log.Error("render index", zap.Error(err))
			middleware.WriteError(w, http.StatusInternalServerError, "internal error")
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = buf.WriteTo(w)
	}
}

// allMethods are probed to build the Allow header of a 405 response.
var allMethods = []string{
	http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut,
	http.MethodPatch, http.MethodDelete, http.MethodOptions,
}

// methodNotAllowed answers 405 in JSON. chi only sets Allow in its default
// handler, so the allowed methods are found by matching the path again.
func methodNotAllowed(w http.ResponseWriter, r *http.Request) {
	if rctx := chi.RouteContext(r.Context()); rctx != nil && rctx.Routes != nil {
		path := r.URL.RawPath
		if path == "" {
			path = r.URL.Path
		}
		var allowed []string
		for _, m := range allMethods {
			if rctx.Routes.Match(chi.NewRouteContext(), m, path) {
				allowed = append(allowed, m)
			}
		}
		if len(allowed) > 0 {
			w.Header().Set("Allow", strings.Join(allowed, ", "))
		}
	}
	middleware.WriteError(w, http.StatusMethodNotAllowed, "method not allowed")
}

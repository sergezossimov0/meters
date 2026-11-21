package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"go.uber.org/zap"

	"github.com/sergezossimov0/meters/internal/domain"
	"github.com/sergezossimov0/meters/internal/httpapi/middleware"
)

// errNoGateway: a /gateway/v1 handler runs without GatewayAuth before it.
var errNoGateway = errors.New("no gateway in the request context")

// GatewayService is what the admin gateway handlers and the /gateway/v1
// authentication need; *service.Gateways implements it.
type GatewayService interface {
	middleware.GatewayAuthenticator
	Create(ctx context.Context, name string) (domain.Gateway, string, error)
	List(ctx context.Context) ([]domain.Gateway, error)
	Block(ctx context.Context, id int64) (domain.Gateway, error)
	Unblock(ctx context.Context, id int64) (domain.Gateway, error)
}

type gatewayJSON struct {
	ID              int64      `json:"id"`
	Name            string     `json:"name"`
	Blocked         bool       `json:"blocked"`
	LastHeartbeatAt *time.Time `json:"last_heartbeat_at"` // null until the first heartbeat (L4)
	CreatedAt       time.Time  `json:"created_at"`
}

func toGatewayJSON(g domain.Gateway) gatewayJSON {
	return gatewayJSON{ID: g.ID, Name: g.Name, Blocked: g.Blocked, LastHeartbeatAt: g.LastHeartbeatAt, CreatedAt: g.CreatedAt}
}

// listGateways: GET /api/admin/gateways → 200 [gateway, ...] ordered by id.
func listGateways(log *zap.Logger, gateways GatewayService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		list, err := gateways.List(r.Context())
		if err != nil {
			writeInternal(w, r, log, err)
			return
		}
		out := make([]gatewayJSON, 0, len(list))
		for _, g := range list {
			out = append(out, toGatewayJSON(g))
		}
		writeJSON(w, log, http.StatusOK, out)
	}
}

type createGatewayRequest struct {
	Name string `json:"name"`
}

type createdGatewayJSON struct {
	gatewayJSON
	Key string `json:"key"`
}

// createGateway: POST /api/admin/gateways {"name"} → 201 with the gateway and
// its API key. The key is shown only in this answer, which must not be cached.
func createGateway(log *zap.Logger, gateways GatewayService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req createGatewayRequest
		if !decodeJSON(w, r, &req) {
			return
		}
		g, key, err := gateways.Create(r.Context(), req.Name)
		switch {
		case err == nil:
		case writeInvalid(w, err):
			return
		default:
			writeInternal(w, r, log, err)
			return
		}
		log.Info("gateway created", zap.Int64("gateway_id", g.ID), zap.String("name", g.Name))
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, log, http.StatusCreated, createdGatewayJSON{gatewayJSON: toGatewayJSON(g), Key: key})
	}
}

// setGatewayBlocked: POST /api/admin/gateways/{id}/block and /unblock →
// 200 with the gateway; repeating is not an error. 400 bad id, 404 unknown.
func setGatewayBlocked(log *zap.Logger, gateways GatewayService, blocked bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := pathID(w, r)
		if !ok {
			return
		}
		// The method is picked per request, not when the router is built:
		// a router without gateways (tests of other handlers) must not panic.
		set := gateways.Unblock
		if blocked {
			set = gateways.Block
		}
		g, err := set(r.Context(), id)
		switch {
		case err == nil:
			log.Info("gateway blocked flag set", zap.Int64("gateway_id", id), zap.Bool("blocked", blocked))
			writeJSON(w, log, http.StatusOK, toGatewayJSON(g))
		case errors.Is(err, domain.ErrNotFound):
			middleware.WriteError(w, http.StatusNotFound, "gateway not found")
		default:
			writeInternal(w, r, log, err)
		}
	}
}

// pathID parses the {id} URL parameter: a positive decimal int64. On failure
// it answers 400 and returns false.
func pathID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		middleware.WriteError(w, http.StatusBadRequest, "id must be a positive integer")
		return 0, false
	}
	return id, true
}

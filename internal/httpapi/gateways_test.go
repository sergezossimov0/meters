package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/sergezossimov0/meters/internal/domain"
)

type gatewayResp struct {
	ID              int64      `json:"id"`
	Name            string     `json:"name"`
	Blocked         bool       `json:"blocked"`
	LastHeartbeatAt *time.Time `json:"last_heartbeat_at"`
	CreatedAt       time.Time  `json:"created_at"`
	Key             string     `json:"key"`
}

func decodeGateway(t *testing.T, rec *httptest.ResponseRecorder, want int) gatewayResp {
	t.Helper()
	if rec.Code != want {
		t.Fatalf("status = %d (body %s), want %d", rec.Code, rec.Body, want)
	}
	var g gatewayResp
	if err := json.Unmarshal(rec.Body.Bytes(), &g); err != nil {
		t.Fatalf("body %s: %v", rec.Body, err)
	}
	return g
}

func TestGateways_CreateListBlock(t *testing.T) {
	e := newAPI(t)
	admin := e.issue(t, 1, domain.RoleAdmin)

	if rec := e.do(t, http.MethodGet, "/api/admin/gateways", "", admin); rec.Code != http.StatusOK || strings.TrimSpace(rec.Body.String()) != "[]" {
		t.Fatalf("empty list: %d %s, want 200 []", rec.Code, rec.Body)
	}

	rec := e.do(t, http.MethodPost, "/api/admin/gateways", `{"name":" Шлюз 17 "}`, admin)
	created := decodeGateway(t, rec, http.StatusCreated)
	if cc := rec.Header().Get("Cache-Control"); cc != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", cc)
	}
	if created.ID <= 0 || created.Name != "Шлюз 17" || created.Blocked || created.CreatedAt.IsZero() ||
		!strings.HasPrefix(created.Key, "gwk_") {
		t.Fatalf("created = %+v", created)
	}
	if !strings.Contains(rec.Body.String(), `"last_heartbeat_at":null`) {
		t.Errorf("body %s: last_heartbeat_at must be null before any heartbeat", rec.Body)
	}

	// The key is shown once: neither the list nor block/unblock carry it.
	rec = e.do(t, http.MethodGet, "/api/admin/gateways", "", admin)
	if rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), created.Key) || strings.Contains(rec.Body.String(), `"key"`) {
		t.Errorf("list: %d %s; want 200 without the key", rec.Code, rec.Body)
	}
	var list []gatewayResp
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil || len(list) != 1 || list[0].ID != created.ID {
		t.Errorf("list = %+v, %v", list, err)
	}

	path := fmt.Sprintf("/api/admin/gateways/%d/", created.ID)
	for _, step := range []struct {
		action  string
		blocked bool
	}{{"block", true}, {"block", true}, {"unblock", false}, {"unblock", false}} {
		rec := e.do(t, http.MethodPost, path+step.action, "", admin)
		g := decodeGateway(t, rec, http.StatusOK)
		if g.ID != created.ID || g.Blocked != step.blocked || g.Key != "" {
			t.Errorf("%s: %+v, want blocked=%v without a key", step.action, g, step.blocked)
		}
	}

	// The key never reaches the logs.
	for _, entry := range e.logs.All() {
		if strings.Contains(fmt.Sprint(entry.Message, entry.ContextMap()), created.Key) {
			t.Errorf("the key is logged: %s %v", entry.Message, entry.ContextMap())
		}
	}
}

func TestGateways_Errors(t *testing.T) {
	e := newAPI(t)
	admin := e.issue(t, 1, domain.RoleAdmin)
	customer := e.issue(t, 2, domain.RoleCustomer)

	tests := []struct {
		name, method, path, body, token string
		want                            int
		wantErr                         string
	}{
		{"empty name", http.MethodPost, "/api/admin/gateways", `{"name":"  "}`, admin, http.StatusBadRequest, "name must be"},
		{"unknown field", http.MethodPost, "/api/admin/gateways", `{"name":"gw","key":"mine"}`, admin, http.StatusBadRequest, "unknown field"},
		{"unknown id", http.MethodPost, "/api/admin/gateways/999/block", "", admin, http.StatusNotFound, "gateway not found"},
		{"unknown id unblock", http.MethodPost, "/api/admin/gateways/999/unblock", "", admin, http.StatusNotFound, "gateway not found"},
		{"bad id", http.MethodPost, "/api/admin/gateways/abc/block", "", admin, http.StatusBadRequest, "positive integer"},
		{"zero id", http.MethodPost, "/api/admin/gateways/0/block", "", admin, http.StatusBadRequest, "positive integer"},
		{"negative id", http.MethodPost, "/api/admin/gateways/-1/unblock", "", admin, http.StatusBadRequest, "positive integer"},
		{"id overflow", http.MethodPost, "/api/admin/gateways/99999999999999999999/block", "", admin, http.StatusBadRequest, "positive integer"},
		{"customer list", http.MethodGet, "/api/admin/gateways", "", customer, http.StatusForbidden, ""},
		{"customer create", http.MethodPost, "/api/admin/gateways", `{"name":"gw"}`, customer, http.StatusForbidden, ""},
		{"customer block", http.MethodPost, "/api/admin/gateways/1/block", "", customer, http.StatusForbidden, ""},
		{"no token", http.MethodGet, "/api/admin/gateways", "", "", http.StatusUnauthorized, ""},
		{"GET block", http.MethodGet, "/api/admin/gateways/1/block", "", admin, http.StatusMethodNotAllowed, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := e.do(t, tt.method, tt.path, tt.body, tt.token)
			if rec.Code != tt.want {
				t.Fatalf("status = %d (body %s), want %d", rec.Code, rec.Body, tt.want)
			}
			if tt.wantErr != "" && !strings.Contains(errorBody(t, rec), tt.wantErr) {
				t.Errorf("error = %q, want it to mention %q", errorBody(t, rec), tt.wantErr)
			}
		})
	}
}

type brokenGateways struct{}

func (brokenGateways) Create(context.Context, string) (domain.Gateway, string, error) {
	return domain.Gateway{}, "", errDBDown
}
func (brokenGateways) List(context.Context) ([]domain.Gateway, error) { return nil, errDBDown }
func (brokenGateways) Block(context.Context, int64) (domain.Gateway, error) {
	return domain.Gateway{}, errDBDown
}
func (brokenGateways) Authenticate(context.Context, string) (domain.Gateway, error) {
	return domain.Gateway{}, errDBDown
}
func (brokenGateways) Unblock(context.Context, int64) (domain.Gateway, error) {
	return domain.Gateway{}, errDBDown
}

func TestGateways_InternalError(t *testing.T) {
	e := newAPI(t)
	e.h = NewRouter(Deps{Log: zap.NewNop(), MaxBodyBytes: 1 << 10, Tokens: e.tokens, Gateways: brokenGateways{}})
	admin := e.issue(t, 1, domain.RoleAdmin)
	for _, rec := range []*httptest.ResponseRecorder{
		e.do(t, http.MethodGet, "/api/admin/gateways", "", admin),
		e.do(t, http.MethodPost, "/api/admin/gateways", `{"name":"gw"}`, admin),
		e.do(t, http.MethodPost, "/api/admin/gateways/1/block", "", admin),
		e.do(t, http.MethodPost, "/api/admin/gateways/1/unblock", "", admin),
	} {
		if rec.Code != http.StatusInternalServerError || errorBody(t, rec) != "internal error" {
			t.Errorf("status = %d, body %s; want 500 internal error", rec.Code, rec.Body)
		}
	}
}

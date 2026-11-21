package middleware

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	"github.com/sergezossimov0/meters/internal/domain"
)

type fakeGateways map[string]error

func (f fakeGateways) Authenticate(_ context.Context, key string) (domain.Gateway, error) {
	err, ok := f[key]
	if !ok {
		return domain.Gateway{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.Gateway{}, err
	}
	return domain.Gateway{ID: 7, Name: "gw-7"}, nil
}

func TestGatewayAuth(t *testing.T) {
	const secretKey = "gwk_valid-key-for-test"
	gateways := fakeGateways{
		secretKey:       nil,
		"gwk_blocked":   domain.ErrGatewayBlocked,
		"gwk_db_broken": errors.New("db down"),
	}
	core, logs := observer.New(zapcore.DebugLevel)
	var seen domain.Gateway
	h := GatewayAuth(gateways, zap.New(core))(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen, _ = GatewayFrom(r.Context())
		w.WriteHeader(http.StatusNoContent)
	}))

	tests := []struct {
		name, key string
		want      int
		wantErr   string
	}{
		{"valid", secretKey, http.StatusNoContent, ""},
		{"missing", "", http.StatusUnauthorized, "missing X-Gateway-Key"},
		{"unknown", "gwk_other", http.StatusUnauthorized, "invalid X-Gateway-Key"},
		{"blocked", "gwk_blocked", http.StatusForbidden, "gateway is blocked"},
		{"storage error", "gwk_db_broken", http.StatusInternalServerError, "internal error"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			seen = domain.Gateway{}
			req := httptest.NewRequest(http.MethodPost, "/gateway/v1/readings", nil)
			if tt.key != "" {
				req.Header.Set(GatewayKeyHeader, tt.key)
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != tt.want || !strings.Contains(rec.Body.String(), tt.wantErr) {
				t.Fatalf("got %d %s, want %d %q", rec.Code, rec.Body, tt.want, tt.wantErr)
			}
			if tt.want == http.StatusNoContent && seen.ID != 7 {
				t.Errorf("gateway in context = %+v, want id 7", seen)
			}
		})
	}
	for _, e := range logs.All() {
		for _, v := range e.ContextMap() {
			if s, ok := v.(string); ok && strings.Contains(s, "gwk_") {
				t.Errorf("a key is logged: %s %v", e.Message, e.ContextMap())
			}
		}
	}
	if _, ok := GatewayFrom(context.Background()); ok {
		t.Error("GatewayFrom on an empty context must report false")
	}
}

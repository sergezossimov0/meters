package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"go.uber.org/zap"

	"github.com/sergezossimov0/meters/internal/httpapi/middleware"
	"github.com/sergezossimov0/meters/internal/service"
	"github.com/sergezossimov0/meters/internal/sign"
	"github.com/sergezossimov0/meters/internal/storage/memory"
)

var testSignKey = []byte("test-sign-key")

type gatewayEnv struct {
	h        http.Handler
	svc      *service.Gateways
	readings *memory.Readings
	key      string // API key of an active gateway
	blocked  string // API key of a blocked gateway
}

func newGatewayAPI(t *testing.T, signKey []byte) gatewayEnv {
	t.Helper()
	svc := service.NewGateways(memory.NewGateways())
	ctx := t.Context()
	_, key, err := svc.Create(ctx, "active")
	if err != nil {
		t.Fatal(err)
	}
	b, blocked, err := svc.Create(ctx, "blocked")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Block(ctx, b.ID); err != nil {
		t.Fatal(err)
	}
	readings := memory.NewReadings()
	h := NewRouter(Deps{
		Log:          zap.NewNop(),
		DB:           pingerFunc(func(context.Context) error { return nil }),
		MaxBodyBytes: 1 << 20,
		Gateways:     svc,
		SignKey:      signKey,
		Ingest:       service.NewIngest(readings),
	})
	return gatewayEnv{h: h, svc: svc, readings: readings, key: key, blocked: blocked}
}

// send posts body to path with the given gateway key and signature
// ("" leaves the header out).
func (e gatewayEnv) send(path, body, key, sig string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if key != "" {
		req.Header.Set(middleware.GatewayKeyHeader, key)
	}
	if sig != "" {
		req.Header.Set(sign.Header, sig)
	}
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	return rec
}

// statusBatch is a minimal valid batch: one device with its state only.
const statusBatch = `{"batch_id":"b1","items":[{"device_serial":"A","model":"M","device_status":{}}]}`

// The order of checks: key (401/403) before the signature (400), and every
// answer, errors included, is signed.
func TestGatewayGroup_Order(t *testing.T) {
	e := newGatewayAPI(t, testSignKey)
	const body = statusBatch
	good := sign.Sum([]byte(body), testSignKey)
	bad := sign.Sum([]byte(body+" "), testSignKey)
	const path = "/gateway/v1/readings"

	tests := []struct {
		name, key, sig string
		want           int
		wantErr        string
	}{
		{"no key, bad signature", "", bad, http.StatusUnauthorized, "missing X-Gateway-Key"},
		{"no key, no signature", "", "", http.StatusUnauthorized, "missing X-Gateway-Key"},
		{"unknown key, bad signature", "gwk_unknown", bad, http.StatusUnauthorized, "invalid X-Gateway-Key"},
		{"blocked, bad signature", e.blocked, bad, http.StatusForbidden, "gateway is blocked"},
		{"valid key, bad signature", e.key, bad, http.StatusBadRequest, "invalid HashSHA256 signature"},
		{"valid key, no signature", e.key, "", http.StatusBadRequest, "missing HashSHA256 signature"},
		{"all valid", e.key, good, http.StatusOK, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := e.send(path, body, tt.key, tt.sig)
			if rec.Code != tt.want {
				t.Fatalf("got %d %s, want %d %q", rec.Code, rec.Body, tt.want, tt.wantErr)
			}
			if tt.wantErr != "" && !strings.Contains(errorBody(t, rec), tt.wantErr) {
				t.Fatalf("got %s, want error %q", rec.Body, tt.wantErr)
			}
			if !sign.Verify(rec.Body.Bytes(), testSignKey, rec.Header().Get(sign.Header)) {
				t.Errorf("response is not signed (header %q)", rec.Header().Get(sign.Header))
			}
		})
	}
}

func TestGatewayGroup_NoSignKey(t *testing.T) {
	e := newGatewayAPI(t, nil)
	// The key is still required; the signature is neither checked nor added.
	rec := e.send("/gateway/v1/readings", `{}`, "", "")
	if rec.Code != http.StatusUnauthorized || rec.Header().Get(sign.Header) != "" {
		t.Errorf("no key: %d, signature %q; want 401 unsigned", rec.Code, rec.Header().Get(sign.Header))
	}
	rec = e.send("/gateway/v1/readings", statusBatch, e.key, "bogus")
	if rec.Code != http.StatusOK || rec.Header().Get(sign.Header) != "" {
		t.Errorf("valid key: %d, signature %q; want to pass the checks unsigned", rec.Code, rec.Header().Get(sign.Header))
	}
}

// An unknown path below /gateway/v1 still needs the key and is signed.
func TestGatewayGroup_UnknownPath(t *testing.T) {
	e := newGatewayAPI(t, testSignKey)
	if rec := e.send("/gateway/v1/nope", `{}`, "", ""); rec.Code != http.StatusUnauthorized {
		t.Errorf("without a key: %d, want 401", rec.Code)
	}
	rec := e.send("/gateway/v1/nope", `{}`, e.key, sign.Sum([]byte(`{}`), testSignKey))
	if rec.Code != http.StatusNotFound || !sign.Verify(rec.Body.Bytes(), testSignKey, rec.Header().Get(sign.Header)) {
		t.Errorf("got %d %s, want a signed 404", rec.Code, rec.Body)
	}
}

// The public API is not signed and does not take the gateway key.
func TestGatewayGroup_PublicAPIUnsigned(t *testing.T) {
	e := newGatewayAPI(t, testSignKey)
	req := httptest.NewRequest(http.MethodGet, "/api/admin/gateways", nil)
	req.Header.Set(middleware.GatewayKeyHeader, e.key)
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized || rec.Header().Get(sign.Header) != "" {
		t.Errorf("got %d, signature %q; want 401 (JWT required) unsigned", rec.Code, rec.Header().Get(sign.Header))
	}
}

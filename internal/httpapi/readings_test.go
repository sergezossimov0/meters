package httpapi

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/sergezossimov0/meters/internal/domain"
	"github.com/sergezossimov0/meters/internal/httpapi/middleware"
	"github.com/sergezossimov0/meters/internal/sign"
)

const readingsPath = "/gateway/v1/readings"

// post sends a correctly signed batch from the active gateway.
func (e gatewayEnv) post(body string) *httptest.ResponseRecorder {
	return e.send(readingsPath, body, e.key, sign.Sum([]byte(body), testSignKey))
}

// ts formats a time h hours ago.
func ts(h int) string {
	return time.Now().UTC().Add(-time.Duration(h) * time.Hour).Truncate(time.Second).Format(time.RFC3339)
}

func TestReadings_MixedBatch(t *testing.T) {
	e := newGatewayAPI(t, testSignKey)
	if rec := e.post(statusBatch); rec.Code != http.StatusOK {
		t.Fatalf("create device: %d %s", rec.Code, rec.Body)
	}
	if err := e.readings.SetDevice("A", domain.DeviceActive, 100_000); err != nil {
		t.Fatal(err)
	}

	body := fmt.Sprintf(`{
	  "batch_id": "gw-1-000002",
	  "items": [
	    {"device_serial": "A", "model": "M",
	     "device_status": {"battery_pct": 101, "rssi": -90},
	     "readings": [
	       {"measured_at": %q, "value": "99.5"},
	       {"measured_at": %q, "value": "101"},
	       {"measured_at": %q, "value": "101"},
	       {"measured_at": %q, "value": "1e3"},
	       {"measured_at": "2099-01-01T00:00:00Z", "value": "1"}
	     ]},
	    {"device_serial": "NEW", "model": "M", "readings": [{"measured_at": %q, "value": "5"}]}
	  ]
	}`, ts(3), ts(2), ts(2), ts(1), ts(1))

	rec := e.post(body)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", rec.Code, rec.Body)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q", ct)
	}
	want := `{"accepted":2,"duplicates":1,"rejected":[` +
		`{"device_serial":"A","index":-1,"reason":"invalid_status"},` +
		`{"device_serial":"A","index":3,"reason":"invalid_value"},` +
		`{"device_serial":"A","index":4,"reason":"future_ts"},` +
		`{"device_serial":"NEW","index":0,"reason":"device_not_active"}]}`
	if got := strings.TrimSpace(rec.Body.String()); got != want {
		t.Errorf("answer\n got %s\nwant %s", got, want)
	}
	stored := e.readings.StoredReadings("A")
	if len(stored) != 2 || !stored[0].IsDecrease || stored[1].IsDecrease {
		t.Errorf("stored = %+v, want 99.5 with decrease and 101 without", stored)
	}

	// The same batch again: the same answer.
	again := e.post(body)
	if again.Code != http.StatusOK || strings.TrimSpace(again.Body.String()) != want {
		t.Errorf("replay: %d %s", again.Code, again.Body)
	}
}

func TestReadings_EmptyRejectedIsArray(t *testing.T) {
	e := newGatewayAPI(t, testSignKey)
	rec := e.post(statusBatch)
	if got := strings.TrimSpace(rec.Body.String()); rec.Code != http.StatusOK || got != `{"accepted":0,"duplicates":0,"rejected":[]}` {
		t.Errorf("got %d %s", rec.Code, got)
	}
}

func TestReadings_BadRequests(t *testing.T) {
	e := newGatewayAPI(t, testSignKey)
	var many strings.Builder
	for i := range domain.MaxBatchReadings + 1 {
		if i > 0 {
			many.WriteByte(',')
		}
		fmt.Fprintf(&many, `{"measured_at":%q,"value":"1"}`, time.Now().UTC().Add(-time.Duration(i)*time.Second).Format(time.RFC3339))
	}
	tests := []struct {
		name, body, wantErr string
	}{
		{"5001 readings", `{"batch_id":"b","items":[{"device_serial":"A","model":"M","readings":[` + many.String() + `]}]}`, "at most 5000"},
		{"empty items", `{"batch_id":"b","items":[]}`, "items must not be empty"},
		{"no batch_id", `{"items":[{"device_serial":"A","model":"M","device_status":{}}]}`, "batch_id"},
		{"trailing data", statusBatch + `{}`, "unexpected data"},
		{"malformed JSON", `{"batch_id":`, "malformed JSON"},
		{"bad measured_at", `{"batch_id":"b","items":[{"device_serial":"A","model":"M","readings":[{"measured_at":"yesterday","value":"1"}]}]}`, "malformed JSON"},
		{"value as number", `{"batch_id":"b","items":[{"device_serial":"A","model":"M","readings":[{"measured_at":"2026-11-08T00:00:00Z","value":1}]}]}`, "must be string"},
		{"battery as string", `{"batch_id":"b","items":[{"device_serial":"A","model":"M","device_status":{"battery_pct":"90"}}]}`, "must be int"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := e.post(tt.body)
			if rec.Code != http.StatusBadRequest || !strings.Contains(errorBody(t, rec), tt.wantErr) {
				t.Errorf("got %d %s, want 400 %q", rec.Code, rec.Body, tt.wantErr)
			}
		})
	}
}

// A newer gateway may send fields this server does not know.
func TestReadings_UnknownFieldsIgnored(t *testing.T) {
	e := newGatewayAPI(t, testSignKey)
	body := `{"batch_id":"b","firmware":"2.0","items":[{"device_serial":"A","model":"M","temperature":21,` +
		`"device_status":{"battery_pct":50,"humidity":40},"readings":[{"measured_at":"` + ts(1) + `","value":"1","quality":"good"}]}]}`
	rec := e.post(body)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"device_not_active"`) {
		t.Errorf("got %d %s, want 200", rec.Code, rec.Body)
	}
}

// The gateway sends gzip and signs the uncompressed body.
func TestReadings_GzipWithSignature(t *testing.T) {
	e := newGatewayAPI(t, testSignKey)
	var zbuf bytes.Buffer
	zw := gzip.NewWriter(&zbuf)
	_, _ = zw.Write([]byte(statusBatch))
	_ = zw.Close()

	req := httptest.NewRequest(http.MethodPost, readingsPath, &zbuf)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Content-Encoding", "gzip")
	req.Header.Set("Accept-Encoding", "gzip")
	req.Header.Set(middleware.GatewayKeyHeader, e.key)
	req.Header.Set(sign.Header, sign.Sum([]byte(statusBatch), testSignKey))
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	zr, err := gzip.NewReader(rec.Body)
	if err != nil {
		t.Fatalf("response is not gzip: %v", err)
	}
	plain, _ := io.ReadAll(zr)
	var res domain.BatchResult
	if err := json.Unmarshal(plain, &res); err != nil || res.Rejected == nil {
		t.Errorf("answer %s: %v", plain, err)
	}
	if !sign.Verify(plain, testSignKey, rec.Header().Get(sign.Header)) {
		t.Error("the signature must cover the uncompressed answer")
	}
}

type brokenIngest struct{}

func (brokenIngest) Batch(context.Context, int64, domain.BatchRequest) (domain.BatchResult, bool, error) {
	return domain.BatchResult{}, false, errDBDown
}

func TestReadings_InternalErrors(t *testing.T) {
	e := newGatewayAPI(t, nil)
	e.h = NewRouter(Deps{Log: zap.NewNop(), MaxBodyBytes: 1 << 10, Gateways: e.svc, Ingest: brokenIngest{}})
	if rec := e.send(readingsPath, statusBatch, e.key, ""); rec.Code != http.StatusInternalServerError {
		t.Errorf("storage failure: %d %s, want 500", rec.Code, rec.Body)
	}

	// Without GatewayAuth in front the handler refuses to store anything.
	h := ingestReadings(zap.NewNop(), brokenIngest{})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, readingsPath, strings.NewReader(statusBatch)))
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("no gateway in context: %d, want 500", rec.Code)
	}
}

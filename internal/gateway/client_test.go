package gateway

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	"github.com/sergezossimov0/meters/internal/domain"
	"github.com/sergezossimov0/meters/internal/sign"
)

var signKey = []byte("test-sign-key")

// reply is one scripted answer of fakeServer: a status and a body, or a
// transport error. badSig sends a wrong signature, unsigned sends none.
type reply struct {
	code     int
	body     string
	err      error
	badSig   bool
	unsigned bool
}

// fakeServer is an http.RoundTripper that answers from a script, records the
// requests and their times, and checks gzip and the signature like the
// server does. No network: it works inside synctest.
type fakeServer struct {
	t       *testing.T
	key     []byte
	mu      sync.Mutex
	script  []reply
	calls   []time.Time
	bodies  [][]byte
	headers []http.Header
}

func (f *fakeServer) RoundTrip(req *http.Request) (*http.Response, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	zr, err := gzip.NewReader(req.Body)
	if err != nil {
		f.t.Errorf("request body is not gzip: %v", err)
		return nil, err
	}
	body, _ := io.ReadAll(zr)
	f.calls = append(f.calls, time.Now())
	f.bodies = append(f.bodies, body)
	f.headers = append(f.headers, req.Header.Clone())

	r := reply{code: http.StatusOK, body: `{"accepted":0,"duplicates":0,"rejected":[]}`}
	if len(f.script) > 0 {
		r, f.script = f.script[0], f.script[1:]
	}
	if r.err != nil {
		return nil, r.err
	}
	h := http.Header{"Content-Type": {"application/json"}}
	switch {
	case len(f.key) == 0 || r.unsigned:
	case r.badSig:
		h.Set(sign.Header, sign.Sum([]byte("other"), f.key))
	default:
		h.Set(sign.Header, sign.Sum([]byte(r.body), f.key))
	}
	// Answer gzip-compressed, as the server does for Accept-Encoding: gzip.
	var zbuf bytes.Buffer
	zw := gzip.NewWriter(&zbuf)
	_, _ = zw.Write([]byte(r.body))
	_ = zw.Close()
	h.Set("Content-Encoding", "gzip")
	return &http.Response{StatusCode: r.code, Header: h, Body: io.NopCloser(&zbuf), Request: req}, nil
}

func newFake(t *testing.T, key []byte, script ...reply) *fakeServer {
	return &fakeServer{t: t, key: key, script: script}
}

func testBatch() domain.BatchRequest {
	return domain.BatchRequest{BatchID: "b-1", Items: []domain.ItemRequest{{
		DeviceSerial: "SIM-1", Model: "SimWater-1",
		Readings: []domain.ReadingRequest{{MeasuredAt: t0, Value: "1.000"}},
	}}}
}

func TestClient_RequestShape(t *testing.T) {
	srv := newFake(t, signKey, reply{code: 200, body: `{"accepted":1,"duplicates":0,"rejected":[]}`})
	c := NewClient("http://server:8080", "gwk_key", signKey, srv, zap.NewNop())
	res, err := c.SendBatch(t.Context(), testBatch())
	if err != nil || res.Accepted != 1 || res.Rejected == nil {
		t.Fatalf("SendBatch = %+v, %v", res, err)
	}
	h := srv.headers[0]
	if h.Get("X-Gateway-Key") != "gwk_key" || h.Get("Content-Encoding") != "gzip" ||
		h.Get("Accept-Encoding") != "gzip" || h.Get("Content-Type") != "application/json" {
		t.Errorf("headers %v", h)
	}
	if !sign.Verify(srv.bodies[0], signKey, h.Get(sign.Header)) {
		t.Error("the signature must cover the uncompressed body")
	}
	var got domain.BatchRequest
	if err := json.Unmarshal(srv.bodies[0], &got); err != nil || got.BatchID != "b-1" || got.Items[0].Readings[0].Value != "1.000" {
		t.Errorf("body %s: %v", srv.bodies[0], err)
	}
}

func TestClient_NoSignKey(t *testing.T) {
	srv := newFake(t, nil)
	c := NewClient("http://s", "k", nil, srv, zap.NewNop())
	if _, err := c.SendBatch(t.Context(), testBatch()); err != nil {
		t.Fatal(err)
	}
	if got := srv.headers[0].Get(sign.Header); got != "" {
		t.Errorf("without a key the request must not be signed, got %q", got)
	}
}

// 5xx is repeated 3 times after 1, 3 and 5 s; then the error is returned.
func TestClient_RetriesServerErrors(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := reply{code: 503, body: `{"error":"database unavailable"}`}
		srv := newFake(t, signKey, e, e, e, e)
		core, logs := observer.New(zapcore.WarnLevel)
		c := NewClient("http://s", "k", signKey, srv, zap.New(core))
		start := time.Now()
		_, err := c.SendBatch(t.Context(), testBatch())
		if !errors.Is(err, errTransient) || !strings.Contains(err.Error(), "database unavailable") {
			t.Fatalf("err = %v", err)
		}
		var gaps []time.Duration
		prev := start
		for _, at := range srv.calls {
			gaps = append(gaps, at.Sub(prev))
			prev = at
		}
		want := []time.Duration{0, time.Second, 3 * time.Second, 5 * time.Second}
		if len(gaps) != 4 || gaps[1] != want[1] || gaps[2] != want[2] || gaps[3] != want[3] {
			t.Errorf("attempts at %v, want pauses %v", gaps, want)
		}
		if logs.FilterMessage("send failed, retrying").Len() != 3 {
			t.Errorf("logged %d retries, want 3", logs.Len())
		}
	})
}

func TestClient_RecoversAfterTransientFailures(t *testing.T) {
	tests := map[string]reply{
		"5xx":             {code: 500, body: `{"error":"internal error"}`},
		"no connection":   {err: errors.New("dial tcp: connection refused")},
		"wrong signature": {code: 200, body: `{"accepted":1,"duplicates":0,"rejected":[]}`, badSig: true},
		"unsigned answer": {code: 200, body: `{"accepted":1,"duplicates":0,"rejected":[]}`, unsigned: true},
	}
	for name, first := range tests {
		t.Run(name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				srv := newFake(t, signKey, first)
				c := NewClient("http://s", "k", signKey, srv, zap.NewNop())
				if _, err := c.SendBatch(t.Context(), testBatch()); err != nil {
					t.Fatalf("second attempt must succeed: %v", err)
				}
				if len(srv.calls) != 2 {
					t.Errorf("%d calls, want 2", len(srv.calls))
				}
			})
		})
	}
}

func TestClient_NoRetryOn4xx(t *testing.T) {
	tests := []struct {
		code int
		want error
	}{
		{400, ErrPoison},
		{413, ErrPoison},
		{401, ErrAuth},
		{403, ErrAuth},
		{404, nil},
		{415, nil},
	}
	for _, tt := range tests {
		srv := newFake(t, signKey, reply{code: tt.code, body: `{"error":"refused"}`})
		c := NewClient("http://s", "k", signKey, srv, zap.NewNop())
		_, err := c.SendBatch(t.Context(), testBatch())
		if err == nil || len(srv.calls) != 1 || errors.Is(err, errTransient) {
			t.Errorf("%d: err = %v after %d calls; want one call, not transient", tt.code, err, len(srv.calls))
		}
		if tt.want != nil && !errors.Is(err, tt.want) {
			t.Errorf("%d: err = %v, want %v", tt.code, err, tt.want)
		}
		if tt.want == nil && (errors.Is(err, ErrPoison) || errors.Is(err, ErrAuth)) {
			t.Errorf("%d: err = %v must keep the readings", tt.code, err)
		}
		if !strings.Contains(err.Error(), "refused") {
			t.Errorf("%d: err = %v, want the server's message", tt.code, err)
		}
	}
}

func TestClient_BadAnswers(t *testing.T) {
	// A 200 that is not JSON: not repeated (the server would answer the
	// same), readings kept.
	srv := newFake(t, signKey, reply{code: 200, body: `not json`})
	c := NewClient("http://s", "k", signKey, srv, zap.NewNop())
	if _, err := c.SendBatch(t.Context(), testBatch()); err == nil || len(srv.calls) != 1 {
		t.Errorf("err = %v after %d calls", err, len(srv.calls))
	}
	// A too long answer is a transport failure.
	long := `{"error":"` + strings.Repeat("x", maxAnswerBytes) + `"}`
	synctest.Test(t, func(t *testing.T) {
		srv := newFake(t, nil, reply{code: 200, body: long}, reply{code: 200, body: `{}`})
		c := NewClient("http://s", "k", nil, srv, zap.NewNop())
		if _, err := c.SendBatch(t.Context(), testBatch()); err != nil || len(srv.calls) != 2 {
			t.Errorf("err = %v after %d calls; want a retry", err, len(srv.calls))
		}
	})
}

// A cancelled context stops the repeats at once.
func TestClient_CancelStopsRetries(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := reply{code: 503, body: `{}`}
		srv := newFake(t, signKey, e, e, e, e)
		c := NewClient("http://s", "k", signKey, srv, zap.NewNop())
		ctx, cancel := context.WithCancel(t.Context())
		go func() {
			time.Sleep(2 * time.Second) // during the 3 s pause
			cancel()
		}()
		_, err := c.SendBatch(ctx, testBatch())
		if !errors.Is(err, context.Canceled) || len(srv.calls) != 2 {
			t.Errorf("err = %v after %d calls; want cancel after 2", err, len(srv.calls))
		}
	})
}

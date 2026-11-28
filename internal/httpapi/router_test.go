package httpapi

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"go.uber.org/zap"
)

type pingerFunc func(ctx context.Context) error

func (f pingerFunc) Ping(ctx context.Context) error { return f(ctx) }

func newTestRouter(db Pinger) http.Handler {
	return NewRouter(Deps{Log: zap.NewNop(), DB: db, MaxBodyBytes: 1 << 20, Version: "test"})
}

func do(h http.Handler, method, target string, header map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, nil)
	for k, v := range header {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func errorBody(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
	var body struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("body %q is not JSON: %v", rec.Body.String(), err)
	}
	return body.Error
}

func TestPing(t *testing.T) {
	t.Run("ok", func(t *testing.T) {
		rec := do(newTestRouter(pingerFunc(func(context.Context) error { return nil })), http.MethodGet, "/ping", nil)
		if rec.Code != http.StatusOK {
			t.Errorf("status = %d, want 200", rec.Code)
		}
	})

	t.Run("database down", func(t *testing.T) {
		db := pingerFunc(func(context.Context) error { return errors.New("connection refused") })
		rec := do(newTestRouter(db), http.MethodGet, "/ping", nil)
		if rec.Code != http.StatusInternalServerError {
			t.Errorf("status = %d, want 500", rec.Code)
		}
		if got := errorBody(t, rec); got != "database unavailable" {
			t.Errorf("error = %q", got)
		}
	})

	t.Run("bounded by timeout", func(t *testing.T) {
		var deadline time.Time
		db := pingerFunc(func(ctx context.Context) error {
			deadline, _ = ctx.Deadline()
			return nil
		})
		start := time.Now()
		do(newTestRouter(db), http.MethodGet, "/ping", nil)
		if deadline.IsZero() || deadline.Sub(start) > pingTimeout+100*time.Millisecond {
			t.Errorf("ping context deadline = %v, want about %v from now", deadline.Sub(start), pingTimeout)
		}
	})
}

func TestIndex(t *testing.T) {
	h := newTestRouter(pingerFunc(func(context.Context) error { return nil }))

	t.Run("plain", func(t *testing.T) {
		rec := do(h, http.MethodGet, "/", nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}
		if ct := rec.Header().Get("Content-Type"); ct != "text/html; charset=utf-8" {
			t.Errorf("Content-Type = %q", ct)
		}
		if body := rec.Body.String(); !strings.Contains(body, "meters — состояние системы") || !strings.Contains(body, "test") {
			t.Errorf("unexpected body: %s", body)
		}
	})

	t.Run("gzip", func(t *testing.T) {
		rec := do(h, http.MethodGet, "/", map[string]string{"Accept-Encoding": "gzip"})
		if ce := rec.Header().Get("Content-Encoding"); ce != "gzip" {
			t.Fatalf("Content-Encoding = %q, want gzip", ce)
		}
		zr, err := gzip.NewReader(rec.Body)
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(zr)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(body), "<h1>meters — состояние системы</h1>") {
			t.Errorf("unexpected body: %s", body)
		}
	})
}

func TestNotFoundAndMethodNotAllowed(t *testing.T) {
	h := newTestRouter(pingerFunc(func(context.Context) error { return nil }))

	rec := do(h, http.MethodGet, "/nope", nil)
	if rec.Code != http.StatusNotFound {
		t.Errorf("GET /nope status = %d, want 404", rec.Code)
	}
	if got := errorBody(t, rec); got != "not found" {
		t.Errorf("error = %q", got)
	}

	rec = do(h, http.MethodPost, "/ping", nil)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST /ping status = %d, want 405", rec.Code)
	}
	if got := errorBody(t, rec); got != "method not allowed" {
		t.Errorf("error = %q", got)
	}
	if allow := rec.Header().Get("Allow"); allow != "GET" {
		t.Errorf("Allow = %q, want GET", allow)
	}
}

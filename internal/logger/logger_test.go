package logger

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

func TestNew(t *testing.T) {
	tests := []struct {
		level   string
		want    zapcore.Level
		wantErr bool
	}{
		{"debug", zapcore.DebugLevel, false},
		{"info", zapcore.InfoLevel, false},
		{"warn", zapcore.WarnLevel, false},
		{"error", zapcore.ErrorLevel, false},
		{"trace", 0, true},
	}
	for _, tt := range tests {
		t.Run(tt.level, func(t *testing.T) {
			log, err := New(tt.level)
			if tt.wantErr {
				if err == nil {
					t.Fatal("want error")
				}
				return
			}
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			if got := log.Level(); got != tt.want {
				t.Errorf("level = %s, want %s", got, tt.want)
			}
		})
	}
}

// serve runs one request through the middleware and returns the single log entry.
func serve(t *testing.T, h http.HandlerFunc, req *http.Request) (observer.LoggedEntry, *httptest.ResponseRecorder) {
	t.Helper()
	core, logs := observer.New(zapcore.InfoLevel)
	rec := httptest.NewRecorder()

	HTTP(zap.New(core))(h).ServeHTTP(rec, req)

	entries := logs.All()
	if len(entries) != 1 {
		t.Fatalf("want 1 log entry, got %d", len(entries))
	}
	return entries[0], rec
}

func TestHTTP_LogsRequestAndResponse(t *testing.T) {
	h := func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, "hello")
		_, _ = io.WriteString(w, ", world") // size is summed over writes
	}
	req := httptest.NewRequest(http.MethodPost, "/api/user/register?x=1", strings.NewReader(`{}`))

	entry, rec := serve(t, h, req)

	if rec.Code != http.StatusCreated || rec.Body.String() != "hello, world" {
		t.Fatalf("response changed by middleware: %d %q", rec.Code, rec.Body)
	}
	if entry.Level != zapcore.InfoLevel || entry.Message != "http request" {
		t.Errorf("entry = %s %q", entry.Level, entry.Message)
	}
	fields := entry.ContextMap()
	want := map[string]any{
		"method": "POST",
		"uri":    "/api/user/register?x=1",
		"status": int64(http.StatusCreated),
		"size":   int64(len("hello, world")),
	}
	for k, v := range want {
		if fields[k] != v {
			t.Errorf("%s = %v (%T), want %v", k, fields[k], fields[k], v)
		}
	}
	if d, ok := fields["duration"].(time.Duration); !ok || d < 0 {
		t.Errorf("duration = %v", fields["duration"])
	}
}

func TestHTTP_StatusDefaults(t *testing.T) {
	tests := []struct {
		name       string
		h          http.HandlerFunc
		wantStatus int64
		wantSize   int64
	}{
		{"nothing written", func(http.ResponseWriter, *http.Request) {}, 200, 0},
		{"implicit 200 on write", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok")) }, 200, 2},
		{"first WriteHeader wins", func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNotFound)
			w.WriteHeader(http.StatusInternalServerError) // ignored by net/http too
		}, 404, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			entry, _ := serve(t, tt.h, httptest.NewRequest(http.MethodGet, "/ping", nil))
			fields := entry.ContextMap()
			if fields["status"] != tt.wantStatus || fields["size"] != tt.wantSize {
				t.Errorf("status=%v size=%v, want %d %d", fields["status"], fields["size"], tt.wantStatus, tt.wantSize)
			}
		})
	}
}

func TestHTTP_DoesNotLogHeadersOrBody(t *testing.T) {
	h := func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "response-body-marker") }
	req := httptest.NewRequest(http.MethodPost, "/gateway/v1/readings", strings.NewReader("request-body-marker"))
	req.Header.Set("X-Gateway-Key", "header-marker")
	req.Header.Set("Authorization", "Bearer header-marker")

	entry, _ := serve(t, h, req)

	for k, v := range entry.ContextMap() {
		s, _ := v.(string)
		if strings.Contains(s, "marker") {
			t.Errorf("field %s leaks %q", k, s)
		}
	}
}

func TestResponseWriter_Unwrap(t *testing.T) {
	rec := httptest.NewRecorder()
	rw := &responseWriter{ResponseWriter: rec}
	if err := http.NewResponseController(rw).Flush(); err != nil {
		t.Fatalf("Flush through ResponseController: %v", err)
	}
	if !rec.Flushed {
		t.Error("underlying writer was not flushed")
	}
}

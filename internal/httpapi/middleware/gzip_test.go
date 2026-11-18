package middleware

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func gzipBytes(t *testing.T, data []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if _, err := zw.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// echoBody replies with the request body, or with the status ReadBodyError picks.
func echoBody(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		WriteError(w, ReadBodyError(err), "cannot read body")
		return
	}
	w.Header().Set("Content-Type", "text/plain")
	_, _ = w.Write(body)
}

func TestDecompress(t *testing.T) {
	const limit = 4 << 10
	payload := []byte(`{"batch_id":"b-1"}`)
	bomb := gzipBytes(t, bytes.Repeat([]byte("0"), 1<<20)) // ~2 KB compressed, 1 MB decoded

	tests := []struct {
		name       string
		body       []byte
		encoding   string
		wantStatus int
		wantBody   string
	}{
		{"plain", payload, "", http.StatusOK, string(payload)},
		{"identity", payload, "identity", http.StatusOK, string(payload)},
		{"gzip", gzipBytes(t, payload), "gzip", http.StatusOK, string(payload)},
		{"gzip upper case", gzipBytes(t, payload), "GZIP", http.StatusOK, string(payload)},
		{"plain over limit", bytes.Repeat([]byte("x"), limit+1), "", http.StatusRequestEntityTooLarge, ""},
		{"gzip bomb", bomb, "gzip", http.StatusRequestEntityTooLarge, ""},
		{"corrupt gzip header", []byte("not gzip"), "gzip", http.StatusBadRequest, "invalid gzip body"},
		{"corrupt gzip stream", gzipBytes(t, payload)[:15], "gzip", http.StatusBadRequest, "cannot read body"},
		{"unsupported encoding", payload, "br", http.StatusUnsupportedMediaType, "unsupported Content-Encoding: br"},
	}
	if len(bomb) > limit {
		t.Fatalf("bomb must be under the limit when compressed, got %d bytes", len(bomb))
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(tt.body))
			if tt.encoding != "" {
				req.Header.Set("Content-Encoding", tt.encoding)
			}
			rec := httptest.NewRecorder()

			Decompress(limit)(http.HandlerFunc(echoBody)).ServeHTTP(rec, req)

			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d (body %q)", rec.Code, tt.wantStatus, rec.Body)
			}
			if tt.wantBody != "" && !strings.Contains(rec.Body.String(), tt.wantBody) {
				t.Errorf("body = %q, want it to contain %q", rec.Body, tt.wantBody)
			}
		})
	}
}

func TestDecompress_HidesEncodingFromHandler(t *testing.T) {
	var gotEncoding string
	var gotLength int64
	h := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		gotEncoding = r.Header.Get("Content-Encoding")
		gotLength = r.ContentLength
	})
	body := gzipBytes(t, []byte("{}"))
	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(body))
	req.Header.Set("Content-Encoding", "gzip")

	Decompress(1<<10)(h).ServeHTTP(httptest.NewRecorder(), req)

	if gotEncoding != "" || gotLength != -1 {
		t.Errorf("handler sees Content-Encoding=%q ContentLength=%d", gotEncoding, gotLength)
	}
}

func TestCompress(t *testing.T) {
	bigJSON := `{"data":"` + strings.Repeat("a", 2048) + `"}`
	tests := []struct {
		name         string
		accept       string
		contentType  string // "" = let the handler leave it unset
		status       int
		body         string
		wantGzip     bool
		wantSameBody bool
	}{
		{"json", "gzip", "application/json", 200, bigJSON, true, true},
		{"json with charset", "gzip, deflate", "application/json; charset=utf-8", 200, bigJSON, true, true},
		{"html", "gzip", "text/html; charset=utf-8", 200, "<html>ok</html>", true, true},
		{"error json", "gzip", "application/json", 404, `{"error":"not found"}`, true, true},
		{"sniffed html", "gzip", "", 200, "<!DOCTYPE html><html></html>", true, true},
		{"plain text not compressed", "gzip", "text/plain", 200, "ok", false, true},
		{"no accept-encoding", "", "application/json", 200, bigJSON, false, true},
		{"gzip refused", "gzip;q=0, identity", "application/json", 200, bigJSON, false, true},
		{"gzip with weight", "br;q=1.0, gzip;q=0.8", "application/json", 200, bigJSON, true, true},
		{"no content", "gzip", "application/json", 204, "", false, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				if tt.contentType != "" {
					w.Header().Set("Content-Type", tt.contentType)
				}
				if tt.status != http.StatusOK {
					w.WriteHeader(tt.status) // 200 goes implicitly through Write, so sniffing works
				}
				_, _ = io.WriteString(w, tt.body)
			})
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			if tt.accept != "" {
				req.Header.Set("Accept-Encoding", tt.accept)
			}
			rec := httptest.NewRecorder()

			Compress(h).ServeHTTP(rec, req)

			if rec.Code != tt.status {
				t.Fatalf("status = %d, want %d", rec.Code, tt.status)
			}
			if rec.Header().Get("Vary") != "Accept-Encoding" {
				t.Errorf("Vary = %q", rec.Header().Get("Vary"))
			}
			gotGzip := rec.Header().Get("Content-Encoding") == "gzip"
			if gotGzip != tt.wantGzip {
				t.Fatalf("gzip = %t, want %t", gotGzip, tt.wantGzip)
			}
			body := rec.Body.Bytes()
			if gotGzip {
				zr, err := gzip.NewReader(bytes.NewReader(body))
				if err != nil {
					t.Fatalf("gzip.NewReader: %v", err)
				}
				if body, err = io.ReadAll(zr); err != nil {
					t.Fatalf("read gzip: %v", err)
				}
			}
			if tt.wantSameBody && string(body) != tt.body {
				t.Errorf("body = %q, want %q", body, tt.body)
			}
		})
	}
}

func TestCompress_DropsContentLength(t *testing.T) {
	h := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Length", "2")
		_, _ = io.WriteString(w, "{}")
	})
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	rec := httptest.NewRecorder()

	Compress(h).ServeHTTP(rec, req)

	if rec.Header().Get("Content-Length") != "" {
		t.Error("Content-Length of the uncompressed body must be removed")
	}
}

func TestCompress_Flush(t *testing.T) {
	h := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, "{")
		if err := http.NewResponseController(w).Flush(); err != nil {
			t.Errorf("Flush: %v", err)
		}
		_, _ = io.WriteString(w, "}")
	})
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	rec := httptest.NewRecorder()

	Compress(h).ServeHTTP(rec, req)

	if !rec.Flushed {
		t.Error("underlying writer was not flushed")
	}
}

func TestWriteError(t *testing.T) {
	rec := httptest.NewRecorder()
	WriteError(rec, http.StatusTeapot, `bad "input"`)

	if rec.Code != http.StatusTeapot || rec.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("status=%d content-type=%q", rec.Code, rec.Header().Get("Content-Type"))
	}
	var got struct{ Error string }
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || got.Error != `bad "input"` {
		t.Errorf("body = %q, err = %v", rec.Body, err)
	}
}

// TestStack checks the real order: Compress (outer) → Decompress → handler.
func TestStack(t *testing.T) {
	h := Compress(Decompress(1 << 10)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			WriteError(w, ReadBodyError(err), "cannot read body")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	})))
	srv := httptest.NewServer(h)
	defer srv.Close()

	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, srv.URL, bytes.NewReader(gzipBytes(t, []byte(`{"a":1}`))))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Encoding", "gzip")
	// The default transport adds Accept-Encoding: gzip and decodes the reply itself.
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	got, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusOK || string(got) != `{"a":1}` || !resp.Uncompressed {
		t.Errorf("status=%d body=%q uncompressed=%t", resp.StatusCode, got, resp.Uncompressed)
	}
}

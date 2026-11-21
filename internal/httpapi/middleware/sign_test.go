package middleware

import (
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sergezossimov0/meters/internal/sign"
)

var testKey = []byte("test-signing-key")

// echoJSON answers 200 with the request body as JSON.
func echoJSON(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		WriteError(w, ReadBodyError(err), err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(body)
}

func signedRequest(body string, sig string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/x", strings.NewReader(body))
	if sig != "" {
		req.Header.Set(sign.Header, sig)
	}
	return req
}

func TestVerifyRequest(t *testing.T) {
	const body = `{"batch_id":"b1"}`
	h := Decompress(1 << 10)(VerifyRequest(testKey)(http.HandlerFunc(echoJSON)))

	tests := []struct {
		name     string
		req      *http.Request
		wantCode int
		wantErr  string
	}{
		{"valid", signedRequest(body, sign.Sum([]byte(body), testKey)), http.StatusOK, ""},
		{"missing", signedRequest(body, ""), http.StatusBadRequest, "missing HashSHA256 signature"},
		{"wrong", signedRequest(body, sign.Sum([]byte(body+" "), testKey)), http.StatusBadRequest, "invalid HashSHA256 signature"},
		{"not hex", signedRequest(body, "xyz"), http.StatusBadRequest, "invalid HashSHA256 signature"},
		{"too large", signedRequest(strings.Repeat("a", 2<<10), "00"), http.StatusRequestEntityTooLarge, "request body too large"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, tt.req)
			if rec.Code != tt.wantCode {
				t.Fatalf("status = %d, want %d (body %s)", rec.Code, tt.wantCode, rec.Body)
			}
			if tt.wantErr == "" {
				if rec.Body.String() != body {
					t.Errorf("handler got body %q, want the original", rec.Body)
				}
				return
			}
			if !strings.Contains(rec.Body.String(), tt.wantErr) {
				t.Errorf("body = %s, want error %q", rec.Body, tt.wantErr)
			}
		})
	}
}

func TestSignResponse(t *testing.T) {
	h := SignResponse(testKey)(VerifyRequest(testKey)(http.HandlerFunc(echoJSON)))

	// A normal answer is signed, status and headers are kept.
	const body = `{"accepted":1}`
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, signedRequest(body, sign.Sum([]byte(body), testKey)))
	if rec.Code != http.StatusOK || rec.Body.String() != body {
		t.Fatalf("got %d %s", rec.Code, rec.Body)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q", ct)
	}
	if !sign.Verify(rec.Body.Bytes(), testKey, rec.Header().Get(sign.Header)) {
		t.Error("200 response is not signed correctly")
	}

	// An error written by inner middleware is signed too.
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, signedRequest(body, ""))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	if !sign.Verify(rec.Body.Bytes(), testKey, rec.Header().Get(sign.Header)) {
		t.Error("400 response is not signed correctly")
	}

	// An empty body is signed as an empty body.
	empty := SignResponse(testKey)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	rec = httptest.NewRecorder()
	empty.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/x", nil))
	if rec.Code != http.StatusNoContent || rec.Header().Get(sign.Header) != sign.Sum(nil, testKey) {
		t.Errorf("204: status %d, signature %q", rec.Code, rec.Header().Get(sign.Header))
	}
}

func TestSign_NoKey(t *testing.T) {
	h := SignResponse(nil)(VerifyRequest(nil)(http.HandlerFunc(echoJSON)))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, signedRequest(`{}`, ""))
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d: without a key the signature is not required", rec.Code)
	}
	if got := rec.Header().Get(sign.Header); got != "" {
		t.Errorf("without a key the response must not be signed, got %q", got)
	}
	// A wrong signature is ignored, not checked.
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, signedRequest(`{}`, "bogus"))
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d with an ignored signature", rec.Code)
	}
}

// The full gateway stack: both signatures are over the uncompressed bodies,
// while the request and the response travel gzip-compressed.
func TestSign_WithGzip(t *testing.T) {
	h := Compress(Decompress(1 << 20)(SignResponse(testKey)(VerifyRequest(testKey)(http.HandlerFunc(echoJSON)))))
	body := `{"items":"` + strings.Repeat("x", 4096) + `"}`

	req := httptest.NewRequest(http.MethodPost, "/x", bytes.NewReader(gzipBytes(t, []byte(body))))
	req.Header.Set("Content-Encoding", "gzip")
	req.Header.Set("Accept-Encoding", "gzip")
	req.Header.Set(sign.Header, sign.Sum([]byte(body), testKey))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d (body %s)", rec.Code, rec.Body)
	}
	if rec.Header().Get("Content-Encoding") != "gzip" {
		t.Fatal("response is not compressed")
	}
	zr, err := gzip.NewReader(rec.Body)
	if err != nil {
		t.Fatal(err)
	}
	plain, err := io.ReadAll(zr)
	if err != nil {
		t.Fatal(err)
	}
	if string(plain) != body {
		t.Errorf("decompressed response differs from the request body")
	}
	if !sign.Verify(plain, testKey, rec.Header().Get(sign.Header)) {
		t.Error("response signature must cover the uncompressed body")
	}
}

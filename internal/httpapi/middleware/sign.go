package middleware

import (
	"bytes"
	"io"
	"net/http"

	"github.com/sergezossimov0/meters/internal/sign"
)

// VerifyRequest rejects a request whose body does not carry a valid
// HashSHA256 signature: missing or wrong — 400 (spec п. 6.1). It must run
// after Decompress, so the signature is checked over the uncompressed body
// and the size limit is already in place. With an empty key it does nothing
// (development mode).
func VerifyRequest(key []byte) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		if len(key) == 0 {
			return next
		}
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			sig := r.Header.Get(sign.Header)
			if sig == "" {
				WriteError(w, http.StatusBadRequest, "missing "+sign.Header+" signature")
				return
			}
			body, err := io.ReadAll(r.Body)
			if err != nil {
				if code := ReadBodyError(err); code == http.StatusRequestEntityTooLarge {
					WriteError(w, code, "request body too large")
				} else {
					WriteError(w, code, "cannot read request body")
				}
				return
			}
			if !sign.Verify(body, key, sig) {
				WriteError(w, http.StatusBadRequest, "invalid "+sign.Header+" signature")
				return
			}
			r.Body = io.NopCloser(bytes.NewReader(body))
			next.ServeHTTP(w, r)
		})
	}
}

// SignResponse adds a HashSHA256 header with the signature of the response
// body. The header must precede the body, so the whole response is buffered
// and written once the handler returns. It must sit inside Compress, so the
// signature covers the uncompressed body. With an empty key it does nothing.
//
// The buffering writer deliberately implements neither Flush nor Unwrap: a
// handler must not be able to stream an unsigned body past it.
func SignResponse(key []byte) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		if len(key) == 0 {
			return next
		}
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			bw := &bufferedWriter{header: w.Header(), code: http.StatusOK}
			next.ServeHTTP(bw, r)
			w.Header().Set(sign.Header, sign.Sum(bw.body.Bytes(), key))
			w.WriteHeader(bw.code)
			_, _ = w.Write(bw.body.Bytes())
		})
	}
}

// bufferedWriter collects the status and body; headers go straight to the
// real writer's map, which is not sent until WriteHeader is called on it.
type bufferedWriter struct {
	header      http.Header
	code        int
	wroteHeader bool
	body        bytes.Buffer
}

func (w *bufferedWriter) Header() http.Header { return w.header }

func (w *bufferedWriter) WriteHeader(code int) {
	if w.wroteHeader {
		return
	}
	w.wroteHeader = true
	w.code = code
}

func (w *bufferedWriter) Write(b []byte) (int, error) {
	w.wroteHeader = true
	return w.body.Write(b)
}

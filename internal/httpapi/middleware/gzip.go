// Package middleware holds the HTTP middleware shared by the server routes.
package middleware

import (
	"compress/gzip"
	"errors"
	"io"
	"mime"
	"net/http"
	"strings"
	"sync"
)

// Decompress unpacks request bodies sent with Content-Encoding: gzip and
// limits the body size AFTER decoding to maxBytes, so a small gzip bomb cannot
// exhaust memory. Plain bodies get the same limit.
//
// A corrupt gzip header is rejected with 400 here; corruption later in the
// stream and an exceeded limit surface as read errors in the handler —
// use ReadBodyError to map them to 400/413.
func Decompress(maxBytes int64) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch enc := strings.ToLower(strings.TrimSpace(r.Header.Get("Content-Encoding"))); enc {
			case "", "identity":
				r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
			case "gzip":
				zr, err := gzip.NewReader(r.Body)
				if err != nil {
					WriteError(w, http.StatusBadRequest, "invalid gzip body")
					return
				}
				defer zr.Close()
				r.Body = http.MaxBytesReader(w, readCloser{Reader: zr, Closer: r.Body}, maxBytes)
				r.Header.Del("Content-Encoding")
				r.Header.Del("Content-Length")
				r.ContentLength = -1
			default:
				WriteError(w, http.StatusUnsupportedMediaType, "unsupported Content-Encoding: "+enc)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

type readCloser struct {
	io.Reader
	io.Closer
}

// ReadBodyError maps an error from reading a request body to a status code:
// 413 when the size limit was exceeded, 400 otherwise (including corrupt gzip).
func ReadBodyError(err error) int {
	if _, ok := errors.AsType[*http.MaxBytesError](err); ok {
		return http.StatusRequestEntityTooLarge
	}
	return http.StatusBadRequest
}

// compressibleTypes are the response types compressed by Compress (spec п. 6).
var compressibleTypes = map[string]bool{
	"application/json": true,
	"text/html":        true,
}

var gzipWriters = sync.Pool{
	New: func() any { return gzip.NewWriter(io.Discard) },
}

// Compress gzips application/json and text/html responses when the client
// sent Accept-Encoding: gzip. The decision is made when the handler writes
// the header, because only then is Content-Type known.
func Compress(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Add("Vary", "Accept-Encoding")
		if !acceptsGzip(r.Header.Get("Accept-Encoding")) {
			next.ServeHTTP(w, r)
			return
		}
		gw := &gzipResponseWriter{ResponseWriter: w}
		defer gw.close()
		next.ServeHTTP(gw, r)
	})
}

// acceptsGzip reports whether gzip is listed and not refused with q=0.
func acceptsGzip(header string) bool {
	for part := range strings.SplitSeq(header, ",") {
		coding, params, _ := strings.Cut(strings.TrimSpace(part), ";")
		if !strings.EqualFold(strings.TrimSpace(coding), "gzip") {
			continue
		}
		q := strings.ReplaceAll(strings.TrimSpace(params), " ", "")
		return q != "q=0" && q != "q=0.0" && q != "q=0.00" && q != "q=0.000"
	}
	return false
}

type gzipResponseWriter struct {
	http.ResponseWriter
	zw          *gzip.Writer // nil: pass through uncompressed
	wroteHeader bool
}

func (w *gzipResponseWriter) WriteHeader(code int) {
	if w.wroteHeader {
		w.ResponseWriter.WriteHeader(code) // let net/http report the superfluous call
		return
	}
	w.wroteHeader = true

	h := w.Header()
	if shouldCompress(code, h) {
		h.Set("Content-Encoding", "gzip")
		h.Del("Content-Length") // the compressed length differs
		zw, _ := gzipWriters.Get().(*gzip.Writer)
		zw.Reset(w.ResponseWriter)
		w.zw = zw
	}
	w.ResponseWriter.WriteHeader(code)
}

func shouldCompress(code int, h http.Header) bool {
	if code < http.StatusOK || code == http.StatusNoContent || code == http.StatusNotModified {
		return false // no body allowed
	}
	if h.Get("Content-Encoding") != "" {
		return false // already encoded by the handler
	}
	mediaType, _, err := mime.ParseMediaType(h.Get("Content-Type"))
	return err == nil && compressibleTypes[mediaType]
}

func (w *gzipResponseWriter) Write(b []byte) (int, error) {
	if !w.wroteHeader {
		if w.Header().Get("Content-Type") == "" {
			// Same sniffing net/http would do, so the decision matches the real type.
			// Handlers that call WriteHeader explicitly must set Content-Type first.
			w.Header().Set("Content-Type", http.DetectContentType(b))
		}
		w.WriteHeader(http.StatusOK)
	}
	if w.zw == nil {
		return w.ResponseWriter.Write(b)
	}
	return w.zw.Write(b)
}

// Flush pushes compressed data written so far to the client.
func (w *gzipResponseWriter) Flush() {
	if w.zw != nil {
		_ = w.zw.Flush()
	}
	_ = http.NewResponseController(w.ResponseWriter).Flush()
}

func (w *gzipResponseWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (w *gzipResponseWriter) close() {
	if w.zw == nil {
		return
	}
	_ = w.zw.Close() // writes the gzip footer
	w.zw.Reset(io.Discard)
	gzipWriters.Put(w.zw)
	w.zw = nil
}

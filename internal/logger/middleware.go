package logger

import (
	"net/http"
	"time"

	"go.uber.org/zap"
)

// HTTP logs every request at Info level: method, URI and duration for the
// request; status and body size for the response (spec п. 10.3).
//
// Headers and bodies are never logged: they carry API keys, JWTs and passwords.
// Place it outermost so the size is what actually went to the client.
func HTTP(log *zap.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			rw := &responseWriter{ResponseWriter: w}

			next.ServeHTTP(rw, r)

			log.Info("http request",
				zap.String("method", r.Method),
				zap.String("uri", r.URL.RequestURI()),
				zap.Duration("duration", time.Since(start)),
				zap.Int("status", rw.statusCode()),
				zap.Int("size", rw.size),
			)
		})
	}
}

// responseWriter records the status code and the number of body bytes written.
type responseWriter struct {
	http.ResponseWriter
	status      int
	size        int
	wroteHeader bool
}

func (w *responseWriter) WriteHeader(code int) {
	if !w.wroteHeader {
		w.status = code
		w.wroteHeader = true
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *responseWriter) Write(b []byte) (int, error) {
	if !w.wroteHeader {
		w.status = http.StatusOK // implicit WriteHeader(200), as in net/http
		w.wroteHeader = true
	}
	n, err := w.ResponseWriter.Write(b)
	w.size += n
	return n, err
}

// Unwrap lets http.ResponseController reach Flush, deadlines etc. of the
// underlying writer.
func (w *responseWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// statusCode is 200 if the handler wrote nothing at all, as net/http does.
func (w *responseWriter) statusCode() int {
	if !w.wroteHeader {
		return http.StatusOK
	}
	return w.status
}

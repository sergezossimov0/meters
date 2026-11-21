package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strings"

	"go.uber.org/zap"

	"github.com/sergezossimov0/meters/internal/domain"
	"github.com/sergezossimov0/meters/internal/httpapi/middleware"
)

// decodeJSON reads exactly one JSON object from the body into dst, strictly:
// unknown fields and trailing data are errors, so a typo in a field name is
// not silently ignored. On failure it writes the error response (400, 413
// or 415) and returns false.
func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	return decodeJSONMode(w, r, dst, true)
}

// decodeGatewayJSON is decodeJSON that ignores unknown fields: a newer
// gateway may send fields an older server does not know (spec п. 6.1).
// Trailing data is still an error.
func decodeGatewayJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	return decodeJSONMode(w, r, dst, false)
}

func decodeJSONMode(w http.ResponseWriter, r *http.Request, dst any, strict bool) bool {
	if ct := r.Header.Get("Content-Type"); ct != "" {
		if mt, _, err := mime.ParseMediaType(ct); err != nil || mt != "application/json" {
			middleware.WriteError(w, http.StatusUnsupportedMediaType, "Content-Type must be application/json")
			return false
		}
	}
	dec := json.NewDecoder(r.Body)
	if strict {
		dec.DisallowUnknownFields()
	}
	if err := dec.Decode(dst); err != nil {
		writeDecodeError(w, err)
		return false
	}
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err == nil || middleware.ReadBodyError(err) == http.StatusBadRequest {
			middleware.WriteError(w, http.StatusBadRequest, "unexpected data after the JSON object")
		} else {
			writeDecodeError(w, err)
		}
		return false
	}
	return true
}

func writeDecodeError(w http.ResponseWriter, err error) {
	code := middleware.ReadBodyError(err)
	switch {
	case code == http.StatusRequestEntityTooLarge:
		middleware.WriteError(w, code, "request body too large")
	case errors.Is(err, io.EOF):
		middleware.WriteError(w, http.StatusBadRequest, "empty request body")
	default:
		middleware.WriteError(w, http.StatusBadRequest, decodeErrorMessage(err))
	}
}

// decodeErrorMessage describes a JSON error without echoing the input: a
// syntax error quotes the offending character, which may be part of a
// password, so only the position is reported.
func decodeErrorMessage(err error) string {
	if syntax, ok := errors.AsType[*json.SyntaxError](err); ok {
		return fmt.Sprintf("malformed JSON at byte %d", syntax.Offset)
	}
	if typ, ok := errors.AsType[*json.UnmarshalTypeError](err); ok {
		return fmt.Sprintf("field %q must be %s", typ.Field, typ.Type)
	}
	if errors.Is(err, io.ErrUnexpectedEOF) {
		return "malformed JSON: unexpected end of input"
	}
	if msg := err.Error(); strings.HasPrefix(msg, "json: unknown field ") {
		return "unknown field " + strings.TrimPrefix(msg, "json: unknown field ")
	}
	return "malformed JSON"
}

// writeJSON writes v as JSON with the given status.
func writeJSON(w http.ResponseWriter, log *zap.Logger, code int, v any) {
	body, err := json.Marshal(v)
	if err != nil {
		log.Error("encode response", zap.Error(err))
		middleware.WriteError(w, http.StatusInternalServerError, "internal error")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_, _ = w.Write(body)
}

// writeInvalid answers 400 with the reason from a domain.InvalidError, or
// returns false if err is not one.
func writeInvalid(w http.ResponseWriter, err error) bool {
	invalid, ok := errors.AsType[*domain.InvalidError](err)
	if !ok {
		return false
	}
	middleware.WriteError(w, http.StatusBadRequest, invalid.Error())
	return true
}

// writeInternal logs an unexpected error and answers 500 without details.
func writeInternal(w http.ResponseWriter, r *http.Request, log *zap.Logger, err error) {
	log.Error("request failed", zap.String("uri", r.URL.Path), zap.Error(err))
	middleware.WriteError(w, http.StatusInternalServerError, "internal error")
}

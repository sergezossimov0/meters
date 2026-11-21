package httpapi

import (
	"context"
	"errors"
	"net/http"

	"go.uber.org/zap"

	"github.com/sergezossimov0/meters/internal/domain"
	"github.com/sergezossimov0/meters/internal/httpapi/middleware"
)

// MeterTypeService is what the catalogue handlers need; *service.MeterTypes
// implements it.
type MeterTypeService interface {
	List(ctx context.Context) ([]domain.MeterType, error)
	CreateType(ctx context.Context, code, name string) (domain.MeterType, error)
	CreateSubtype(ctx context.Context, typeCode, code, name, unit string) (domain.MeterSubtype, error)
}

// The API identifies types and subtypes by code; database IDs stay inside.

type meterTypeJSON struct {
	Code     string             `json:"code"`
	Name     string             `json:"name"`
	Subtypes []meterSubtypeJSON `json:"subtypes"`
}

type meterSubtypeJSON struct {
	TypeCode string `json:"type_code,omitempty"` // only in the POST answer
	Code     string `json:"code"`
	Name     string `json:"name"`
	Unit     string `json:"unit"`
}

func toMeterTypeJSON(t domain.MeterType) meterTypeJSON {
	out := meterTypeJSON{Code: t.Code, Name: t.Name, Subtypes: make([]meterSubtypeJSON, 0, len(t.Subtypes))}
	for _, s := range t.Subtypes {
		out.Subtypes = append(out.Subtypes, meterSubtypeJSON{Code: s.Code, Name: s.Name, Unit: s.Unit})
	}
	return out
}

// listMeterTypes: GET /api/meter-types → 200 [{code, name, subtypes: [...]}].
func listMeterTypes(log *zap.Logger, types MeterTypeService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		list, err := types.List(r.Context())
		if err != nil {
			writeInternal(w, r, log, err)
			return
		}
		out := make([]meterTypeJSON, 0, len(list))
		for _, t := range list {
			out = append(out, toMeterTypeJSON(t))
		}
		writeJSON(w, log, http.StatusOK, out)
	}
}

type createMeterTypeRequest struct {
	TypeCode string `json:"type_code"`
	Code     string `json:"code"`
	Name     string `json:"name"`
	Unit     string `json:"unit"`
}

// createMeterType: POST /api/admin/meter-types. Without type_code it adds a
// type, with type_code a subtype of it (unit required). 201 with the created
// record; 400 bad input, 409 code taken, 422 unknown type_code.
func createMeterType(log *zap.Logger, types MeterTypeService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req createMeterTypeRequest
		if !decodeJSON(w, r, &req) {
			return
		}
		var (
			created any
			err     error
		)
		if req.TypeCode == "" {
			if req.Unit != "" {
				middleware.WriteError(w, http.StatusBadRequest, "unit is set only for a subtype (with type_code)")
				return
			}
			var t domain.MeterType
			t, err = types.CreateType(r.Context(), req.Code, req.Name)
			created = toMeterTypeJSON(t)
		} else {
			var s domain.MeterSubtype
			s, err = types.CreateSubtype(r.Context(), req.TypeCode, req.Code, req.Name, req.Unit)
			created = meterSubtypeJSON{TypeCode: req.TypeCode, Code: s.Code, Name: s.Name, Unit: s.Unit}
		}
		switch {
		case err == nil:
			writeJSON(w, log, http.StatusCreated, created)
		case writeInvalid(w, err):
		case errors.Is(err, domain.ErrConflict):
			middleware.WriteError(w, http.StatusConflict, "code is already taken")
		case errors.Is(err, domain.ErrNotFound):
			middleware.WriteError(w, http.StatusUnprocessableEntity, "unknown type_code")
		default:
			writeInternal(w, r, log, err)
		}
	}
}

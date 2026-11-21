package httpapi

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"go.uber.org/zap"

	"github.com/sergezossimov0/meters/internal/domain"
)

// do sends a request with an optional bearer token.
func (e apiEnv) do(t *testing.T, method, path, body, token string) *httptest.ResponseRecorder {
	t.Helper()
	var r io.Reader
	if body != "" {
		r = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, r)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	return rec
}

func (e apiEnv) issue(t *testing.T, id int64, role domain.Role) string {
	t.Helper()
	token, err := e.tokens.Issue(id, role)
	if err != nil {
		t.Fatal(err)
	}
	return token
}

func TestMeterTypes_CreateAndList(t *testing.T) {
	e := newAPI(t)
	admin := e.issue(t, 1, domain.RoleAdmin)
	customer := e.issue(t, 2, domain.RoleCustomer)

	if rec := e.do(t, http.MethodGet, "/api/meter-types", "", customer); rec.Code != http.StatusOK || strings.TrimSpace(rec.Body.String()) != "[]" {
		t.Fatalf("empty catalogue: %d %s, want 200 []", rec.Code, rec.Body)
	}

	steps := []struct {
		body string
		want string
	}{
		{`{"code":"water","name":"Вода"}`, `{"code":"water","name":"Вода","subtypes":[]}`},
		{`{"code":"gas","name":"Газ"}`, `{"code":"gas","name":"Газ","subtypes":[]}`},
		{`{"type_code":"water","code":"hot_water","name":"Горячая вода","unit":"m3"}`,
			`{"type_code":"water","code":"hot_water","name":"Горячая вода","unit":"m3"}`},
		{`{"type_code":"water","code":"cold_water","name":" Холодная вода ","unit":"m3"}`,
			`{"type_code":"water","code":"cold_water","name":"Холодная вода","unit":"m3"}`},
	}
	for _, s := range steps {
		rec := e.do(t, http.MethodPost, "/api/admin/meter-types", s.body, admin)
		if rec.Code != http.StatusCreated {
			t.Fatalf("POST %s: status = %d (body %s), want 201", s.body, rec.Code, rec.Body)
		}
		if got := strings.TrimSpace(rec.Body.String()); got != s.want {
			t.Errorf("POST %s:\n got %s\nwant %s", s.body, got, s.want)
		}
	}

	// Both roles see the catalogue, types and subtypes ordered by code.
	want := `[{"code":"gas","name":"Газ","subtypes":[]},` +
		`{"code":"water","name":"Вода","subtypes":[` +
		`{"code":"cold_water","name":"Холодная вода","unit":"m3"},` +
		`{"code":"hot_water","name":"Горячая вода","unit":"m3"}]}]`
	for _, token := range []string{admin, customer} {
		rec := e.do(t, http.MethodGet, "/api/meter-types", "", token)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET: status = %d", rec.Code)
		}
		if got := strings.TrimSpace(rec.Body.String()); got != want {
			t.Errorf("GET:\n got %s\nwant %s", got, want)
		}
	}
}

func TestMeterTypes_Errors(t *testing.T) {
	e := newAPI(t)
	admin := e.issue(t, 1, domain.RoleAdmin)
	for _, body := range []string{
		`{"code":"water","name":"Вода"}`,
		`{"type_code":"water","code":"cold_water","name":"Холодная вода","unit":"m3"}`,
	} {
		if rec := e.do(t, http.MethodPost, "/api/admin/meter-types", body, admin); rec.Code != http.StatusCreated {
			t.Fatalf("setup %s: status %d", body, rec.Code)
		}
	}

	tests := []struct {
		name     string
		body     string
		token    string
		wantCode int
		wantMsg  string
	}{
		{"anonymous", `{"code":"gas","name":"Газ"}`, "", http.StatusUnauthorized, "authorization required"},
		{"customer", `{"code":"gas","name":"Газ"}`, e.issue(t, 2, domain.RoleCustomer), http.StatusForbidden, "access denied"},
		{"type taken", `{"code":"water","name":"Ещё вода"}`, admin, http.StatusConflict, "code is already taken"},
		{"subtype taken", `{"type_code":"water","code":"cold_water","name":"Х","unit":"m3"}`, admin, http.StatusConflict, "code is already taken"},
		{"unknown type", `{"type_code":"steam","code":"hot_steam","name":"Пар","unit":"t"}`, admin, http.StatusUnprocessableEntity, "unknown type_code"},
		{"unit on a type", `{"code":"gas","name":"Газ","unit":"m3"}`, admin, http.StatusBadRequest, "unit is set only for a subtype"},
		{"subtype without unit", `{"type_code":"water","code":"sewage","name":"Канализация"}`, admin, http.StatusBadRequest, "unit must be"},
		{"bad code", `{"code":"Gas","name":"Газ"}`, admin, http.StatusBadRequest, "code must be"},
		{"no name", `{"code":"gas"}`, admin, http.StatusBadRequest, "name must be"},
		{"unknown field", `{"code":"gas","name":"Газ","id":5}`, admin, http.StatusBadRequest, `unknown field "id"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := e.do(t, http.MethodPost, "/api/admin/meter-types", tt.body, tt.token)
			if rec.Code != tt.wantCode {
				t.Errorf("status = %d, want %d (body %s)", rec.Code, tt.wantCode, rec.Body)
			}
			if got := errorBody(t, rec); !strings.HasPrefix(got, tt.wantMsg) {
				t.Errorf("error = %q, want prefix %q", got, tt.wantMsg)
			}
		})
	}

	if rec := e.do(t, http.MethodGet, "/api/meter-types", "", ""); rec.Code != http.StatusUnauthorized {
		t.Errorf("GET without token: status = %d, want 401", rec.Code)
	}
}

// brokenMeterTypes fails like a storage outage would.
type brokenMeterTypes struct{}

var errDBDown = errors.New("db down")

func (brokenMeterTypes) List(context.Context) ([]domain.MeterType, error) { return nil, errDBDown }
func (brokenMeterTypes) CreateType(context.Context, string, string) (domain.MeterType, error) {
	return domain.MeterType{}, errDBDown
}
func (brokenMeterTypes) CreateSubtype(context.Context, string, string, string, string) (domain.MeterSubtype, error) {
	return domain.MeterSubtype{}, errDBDown
}

func TestMeterTypes_InternalError(t *testing.T) {
	e := newAPI(t)
	e.h = NewRouter(Deps{Log: zap.NewNop(), MaxBodyBytes: 1 << 10, Tokens: e.tokens, MeterTypes: brokenMeterTypes{}})
	admin := e.issue(t, 1, domain.RoleAdmin)
	for _, rec := range []*httptest.ResponseRecorder{
		e.do(t, http.MethodGet, "/api/meter-types", "", admin),
		e.do(t, http.MethodPost, "/api/admin/meter-types", `{"code":"gas","name":"Газ"}`, admin),
		e.do(t, http.MethodPost, "/api/admin/meter-types", `{"type_code":"gas","code":"lpg","name":"СУГ","unit":"m3"}`, admin),
	} {
		if rec.Code != http.StatusInternalServerError || errorBody(t, rec) != "internal error" {
			t.Errorf("status = %d, body %s; want 500 internal error", rec.Code, rec.Body)
		}
	}
}

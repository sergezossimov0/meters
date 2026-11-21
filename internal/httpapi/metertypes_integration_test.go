//go:build integration

package httpapi

import (
	"encoding/json"
	"net/http"
	"testing"

	"go.uber.org/zap"

	"github.com/sergezossimov0/meters/internal/domain"
	"github.com/sergezossimov0/meters/internal/service"
	"github.com/sergezossimov0/meters/internal/storage/postgres"
	"github.com/sergezossimov0/meters/internal/storage/postgres/pgtest"
)

// The catalogue from migration 00002 is served by the API, and an admin can
// extend it in the real database.
func TestMeterTypes_Postgres(t *testing.T) {
	pool := pgtest.Start(t)
	e := newAPI(t)
	e.h = NewRouter(Deps{
		Log:          zap.NewNop(),
		DB:           pool,
		MaxBodyBytes: 1 << 10,
		Tokens:       e.tokens,
		MeterTypes:   service.NewMeterTypes(postgres.NewMeterTypes(pool, zap.NewNop())),
	})
	admin := e.issue(t, 1, domain.RoleAdmin)

	list := func() map[string][]string {
		t.Helper()
		rec := e.do(t, http.MethodGet, "/api/meter-types", "", e.issue(t, 2, domain.RoleCustomer))
		if rec.Code != http.StatusOK {
			t.Fatalf("GET: status = %d (body %s)", rec.Code, rec.Body)
		}
		var types []meterTypeJSON
		if err := json.Unmarshal(rec.Body.Bytes(), &types); err != nil {
			t.Fatal(err)
		}
		out := map[string][]string{}
		for _, mt := range types {
			out[mt.Code] = []string{}
			for _, s := range mt.Subtypes {
				out[mt.Code] = append(out[mt.Code], s.Code)
			}
		}
		return out
	}

	got := list()
	want := map[string]int{"electricity": 2, "gas": 2, "heat": 1, "water": 3}
	if len(got) != len(want) {
		t.Fatalf("types = %v, want %d of them", got, len(want))
	}
	for code, n := range want {
		if len(got[code]) != n {
			t.Errorf("%s: subtypes %v, want %d", code, got[code], n)
		}
	}

	if rec := e.do(t, http.MethodPost, "/api/admin/meter-types",
		`{"type_code":"gas","code":"biogas","name":"Биогаз","unit":"m3"}`, admin); rec.Code != http.StatusCreated {
		t.Fatalf("add subtype: status = %d (body %s)", rec.Code, rec.Body)
	}
	if rec := e.do(t, http.MethodPost, "/api/admin/meter-types",
		`{"type_code":"water","code":"heat","name":"Дубль","unit":"m3"}`, admin); rec.Code != http.StatusConflict {
		t.Errorf("subtype code taken in another type: status = %d, want 409", rec.Code)
	}
	if rec := e.do(t, http.MethodPost, "/api/admin/meter-types",
		`{"type_code":"steam","code":"hot_steam","name":"Пар","unit":"t"}`, admin); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("unknown type: status = %d, want 422", rec.Code)
	}
	if gas := list()["gas"]; len(gas) != 3 || gas[0] != "biogas" {
		t.Errorf("gas subtypes = %v, want biogas first of 3", gas)
	}
}

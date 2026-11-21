//go:build integration

package postgres

import (
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"

	"github.com/sergezossimov0/meters/internal/storage"
	"github.com/sergezossimov0/meters/internal/storage/storagetest"
)

// migratedPool starts PostgreSQL and applies the migrations.
func migratedPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	pool := startPostgres(t)
	if _, err := Migrate(t.Context(), pool); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	return pool
}

// truncate empties tables between contract subtests; identities restart so
// every subtest sees the same IDs. CASCADE also empties dependent tables.
func truncate(t *testing.T, pool *pgxpool.Pool, tables string) {
	t.Helper()
	if _, err := pool.Exec(t.Context(), "TRUNCATE "+tables+" RESTART IDENTITY CASCADE"); err != nil {
		t.Fatalf("truncate %s: %v", tables, err)
	}
}

func TestUsers_Contract(t *testing.T) {
	pool := migratedPool(t)
	storagetest.UserRepository(t, func(t *testing.T) storage.UserRepository {
		truncate(t, pool, "users")
		return NewUsers(pool, zap.NewNop())
	})
}

func TestMeterTypes_Contract(t *testing.T) {
	pool := migratedPool(t)
	storagetest.MeterTypeRepository(t, func(t *testing.T) storage.MeterTypeRepository {
		truncate(t, pool, "meter_types, meter_subtypes")
		return NewMeterTypes(pool, zap.NewNop())
	})
}

// The catalogue from migration 00002 is visible through the repository.
func TestMeterTypes_SeededCatalogue(t *testing.T) {
	pool := migratedPool(t)
	list, err := NewMeterTypes(pool, zap.NewNop()).List(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]int{"electricity": 2, "gas": 2, "heat": 1, "water": 3}
	if len(list) != len(want) {
		t.Fatalf("types = %d, want %d", len(list), len(want))
	}
	for _, mt := range list {
		if len(mt.Subtypes) != want[mt.Code] {
			t.Errorf("%s has %d subtypes, want %d", mt.Code, len(mt.Subtypes), want[mt.Code])
		}
	}
}

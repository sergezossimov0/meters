//go:build integration

package postgres

import (
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"

	"github.com/sergezossimov0/meters/internal/domain"
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

func TestGateways_Contract(t *testing.T) {
	pool := migratedPool(t)
	storagetest.GatewayRepository(t, func(t *testing.T) storage.GatewayRepository {
		truncate(t, pool, "gateways")
		return NewGateways(pool, zap.NewNop())
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

// readingsFixture prepares gateways and approves devices with plain SQL:
// the repositories for that arrive in L4–L5.
type readingsFixture struct {
	pool *pgxpool.Pool
}

func (f readingsFixture) NewGateway(t *testing.T) int64 {
	t.Helper()
	var id int64
	err := f.pool.QueryRow(t.Context(),
		`INSERT INTO gateways (name, key_hash) VALUES ('test', sha256(gen_random_uuid()::text::bytea)) RETURNING id`).Scan(&id)
	if err != nil {
		t.Fatalf("create gateway: %v", err)
	}
	return id
}

// SetDevice gives an active device the subtype and owner its CHECK demands.
func (f readingsFixture) SetDevice(t *testing.T, serial string, status domain.DeviceStatus, initial domain.Value) {
	t.Helper()
	tag, err := f.pool.Exec(t.Context(), `
		WITH owner AS (
		    INSERT INTO users (login, password_hash, role)
		    VALUES ('owner-' || $1, 'x', 'customer')
		    ON CONFLICT (login) DO UPDATE SET login = EXCLUDED.login
		    RETURNING id
		)
		UPDATE devices SET status = $2, initial_value = $3::numeric,
		    subtype_id = (SELECT min(id) FROM meter_subtypes),
		    owner_id = (SELECT id FROM owner),
		    activated_at = now()
		WHERE serial = $1`,
		serial, status, initial.String())
	if err != nil || tag.RowsAffected() != 1 {
		t.Fatalf("SetDevice(%s): %v, %d rows", serial, err, tag.RowsAffected())
	}
}

func (f readingsFixture) Device(t *testing.T, serial string) (domain.Device, bool) {
	t.Helper()
	var d domain.Device
	err := f.pool.QueryRow(t.Context(),
		`SELECT id, serial, model, status, gateway_id, created_at FROM devices WHERE serial = $1`, serial,
	).Scan(&d.ID, &d.Serial, &d.Model, &d.Status, &d.GatewayID, &d.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Device{}, false
	}
	if err != nil {
		t.Fatalf("Device(%s): %v", serial, err)
	}
	return d, true
}

func (f readingsFixture) StoredReadings(t *testing.T, serial string) []domain.Reading {
	t.Helper()
	rows, err := f.pool.Query(t.Context(), `
		SELECT r.measured_at, r.value::text, r.is_decrease
		FROM readings r JOIN devices d ON d.id = r.device_id
		WHERE d.serial = $1 ORDER BY r.measured_at`, serial)
	if err != nil {
		t.Fatalf("StoredReadings(%s): %v", serial, err)
	}
	var out []domain.Reading
	for rows.Next() {
		var (
			r domain.Reading
			v string
		)
		if err := rows.Scan(&r.MeasuredAt, &v, &r.IsDecrease); err != nil {
			t.Fatal(err)
		}
		if r.Value, err = domain.ParseValue(v); err != nil {
			t.Fatal(err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestReadings_Contract(t *testing.T) {
	pool := migratedPool(t)
	storagetest.ReadingRepository(t, func(t *testing.T) (storage.ReadingRepository, storagetest.IngestFixture) {
		truncate(t, pool, "gateway_batches, readings, devices, gateways, users")
		return NewReadings(pool, zap.NewNop()), readingsFixture{pool: pool}
	})
}

//go:build integration

package postgres

import (
	"fmt"
	"math/rand/v2"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	"github.com/sergezossimov0/meters/internal/domain"
)

// Races and failures of IngestBatch on a real PostgreSQL (plan step 2.8).
// The plain contract (replays, duplicates, parallel repeats of one batch,
// parallel creation of one device) is in storagetest and runs for both
// implementations; here are the cases only row locks and the retry can pass.

// ingestEnv is a migrated, empty database with one gateway.
type ingestEnv struct {
	pool *pgxpool.Pool
	repo *Readings
	fx   readingsFixture
	gw   int64
	base time.Time
}

func newIngestEnv(t *testing.T, pool *pgxpool.Pool, log *zap.Logger) ingestEnv {
	t.Helper()
	truncate(t, pool, "gateway_batches, readings, devices, gateways, users")
	fx := readingsFixture{pool: pool}
	return ingestEnv{
		pool: pool, repo: NewReadings(pool, log), fx: fx, gw: fx.NewGateway(t),
		base: time.Now().UTC().Truncate(time.Hour).Add(-48 * time.Hour),
	}
}

func (e ingestEnv) at(h int) time.Time { return e.base.Add(time.Duration(h) * time.Hour) }

// batch builds a checked batch: serial -> {hour: value}.
func (e ingestEnv) batch(t *testing.T, id string, items ...domain.ItemRequest) (domain.Batch, []domain.Rejected) {
	t.Helper()
	b, rej, err := domain.ValidateBatch(domain.BatchRequest{BatchID: id, Items: items}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	return b, rej
}

func (e ingestEnv) item(serial string, hv ...any) domain.ItemRequest {
	it := domain.ItemRequest{DeviceSerial: serial, Model: "M", DeviceStatus: &domain.DeviceState{}}
	for i := 0; i+1 < len(hv); i += 2 {
		it.Readings = append(it.Readings, domain.ReadingRequest{MeasuredAt: e.at(hv[i].(int)), Value: hv[i+1].(string)})
	}
	return it
}

func (e ingestEnv) ingest(t *testing.T, id string, items ...domain.ItemRequest) domain.BatchResult {
	t.Helper()
	b, rej := e.batch(t, id, items...)
	res, _, err := e.repo.IngestBatch(t.Context(), e.gw, b, rej)
	if err != nil {
		t.Fatalf("IngestBatch(%s): %v", id, err)
	}
	return res
}

// activeDevice creates the device and approves it with the initial value.
func (e ingestEnv) activeDevice(t *testing.T, serial, initial string) {
	t.Helper()
	e.ingest(t, "create-"+serial, e.item(serial))
	v, err := domain.ParseValue(initial)
	if err != nil {
		t.Fatal(err)
	}
	e.fx.SetDevice(t, serial, domain.DeviceActive, v)
}

// Two batches for one device at the same time. X brings 120 at t2; Y brings
// 130 at t2 (X's duplicate) and 125 at t3. Whatever order the locks give,
// the flag of 125 must match the value that won t2: with 120 stored 125 is
// not a decrease, with 130 it is. Without the row lock both would read an
// empty t2 and Y would flag 125 against its own, never stored, 130.
func TestIngest_ParallelBatchesOneDevice(t *testing.T) {
	pool := migratedPool(t)
	for round := range 10 {
		e := newIngestEnv(t, pool, zap.NewNop())
		e.activeDevice(t, "A", "100")
		e.ingest(t, "b0", e.item("A", 1, "100"))

		bx, rx := e.batch(t, "x", e.item("A", 2, "120"))
		by, ry := e.batch(t, "y", e.item("A", 2, "130", 3, "125"))
		var (
			wg         sync.WaitGroup
			resX, resY domain.BatchResult
			errX, errY error
			start      = make(chan struct{})
		)
		wg.Go(func() { <-start; resX, _, errX = e.repo.IngestBatch(t.Context(), e.gw, bx, rx) })
		wg.Go(func() { <-start; resY, _, errY = e.repo.IngestBatch(t.Context(), e.gw, by, ry) })
		close(start)
		wg.Wait()
		if errX != nil || errY != nil {
			t.Fatalf("round %d: %v, %v", round, errX, errY)
		}
		if acc, dup := resX.Accepted+resY.Accepted, resX.Duplicates+resY.Duplicates; acc != 2 || dup != 1 {
			t.Fatalf("round %d: accepted %d, duplicates %d; want 2 and 1", round, acc, dup)
		}
		stored := e.fx.StoredReadings(t, "A")
		if len(stored) != 3 {
			t.Fatalf("round %d: stored %v", round, stored)
		}
		t2, t3 := stored[1], stored[2]
		wantDec := t2.Value.String() == "130.000"
		if t3.Value.String() != "125.000" || t3.IsDecrease != wantDec {
			t.Errorf("round %d: t2 = %s, t3 = %s decrease=%v; want decrease=%v", round, t2.Value, t3.Value, t3.IsDecrease, wantDec)
		}
	}
}

// Many batches lock overlapping devices — new and active, listed in random
// order — at the same time. Sorting by serial and locking by id must leave
// no deadlock (40P01 would fail a batch).
func TestIngest_NoDeadlocks(t *testing.T) {
	pool := migratedPool(t)
	e := newIngestEnv(t, pool, zap.NewNop())
	const active = 5
	for i := range active {
		e.activeDevice(t, fmt.Sprintf("ACT-%d", i), "0")
	}
	const workers, rounds, newSerials = 8, 5, 10
	for round := range rounds {
		var wg sync.WaitGroup
		errs := make([]error, workers)
		for w := range workers {
			var items []domain.ItemRequest
			for i := range active {
				h := round*workers + w + 1 // a distinct hour per batch: no duplicates
				items = append(items, e.item(fmt.Sprintf("ACT-%d", i), h, fmt.Sprint(h)))
			}
			for i := range newSerials {
				items = append(items, e.item(fmt.Sprintf("NEW-%d-%d", round, i), 1, "1"))
			}
			rand.Shuffle(len(items), func(i, j int) { items[i], items[j] = items[j], items[i] })
			b, rej := e.batch(t, fmt.Sprintf("r%d-w%d", round, w), items...)
			wg.Go(func() { _, _, errs[w] = e.repo.IngestBatch(t.Context(), e.gw, b, rej) })
		}
		wg.Wait()
		for w, err := range errs {
			if err != nil {
				t.Fatalf("round %d, worker %d: %v", round, w, err)
			}
		}
	}
	var devices, readings int
	if err := pool.QueryRow(t.Context(), `SELECT (SELECT count(*) FROM devices), (SELECT count(*) FROM readings)`).Scan(&devices, &readings); err != nil {
		t.Fatal(err)
	}
	if devices != active+rounds*newSerials || readings != active*rounds*workers {
		t.Errorf("devices = %d, readings = %d; want %d and %d", devices, readings, active+rounds*newSerials, active*rounds*workers)
	}
}

// The session dies in the middle of the transaction (pg_terminate_backend
// while it waits for a device lock): the whole transaction is repeated and
// the outcome is that of a single run.
func TestIngest_ConnectionKilledMidTransaction(t *testing.T) {
	pool := migratedPool(t)
	core, logs := observer.New(zapcore.WarnLevel)
	e := newIngestEnv(t, pool, zap.New(core))
	e.activeDevice(t, "A", "100")
	ctx := t.Context()

	// Hold the device so the batch stops after claiming batch_id and
	// creating NEW, right before the lock.
	blocker, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = blocker.Rollback(ctx) }()
	if _, err := blocker.Exec(ctx, `SELECT 1 FROM devices WHERE serial = 'A' FOR UPDATE`); err != nil {
		t.Fatal(err)
	}

	b, rej := e.batch(t, "b1", e.item("A", 1, "90", 2, "110"), e.item("NEW", 1, "1"))
	type outcome struct {
		res      domain.BatchResult
		replayed bool
		err      error
	}
	done := make(chan outcome, 1)
	go func() {
		res, replayed, err := e.repo.IngestBatch(ctx, e.gw, b, rej)
		done <- outcome{res, replayed, err}
	}()

	// Find the waiting backend and kill it.
	var pid int32
	deadline := time.Now().Add(10 * time.Second)
	for {
		err := pool.QueryRow(ctx, `
			SELECT pid FROM pg_stat_activity
			WHERE datname = current_database() AND wait_event_type = 'Lock' AND query LIKE '%FOR UPDATE%'`).Scan(&pid)
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the batch never waited for the lock: %v", err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	var killed bool
	if err := pool.QueryRow(ctx, `SELECT pg_terminate_backend($1)`, pid).Scan(&killed); err != nil || !killed {
		t.Fatalf("pg_terminate_backend(%d) = %v, %v", pid, killed, err)
	}
	if err := blocker.Rollback(ctx); err != nil {
		t.Fatal(err)
	}

	var got outcome
	select {
	case got = <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("IngestBatch did not finish after the retry")
	}
	if got.err != nil || got.replayed {
		t.Fatalf("IngestBatch = %+v; want a normal result", got)
	}
	want := domain.BatchResult{Accepted: 2, Rejected: []domain.Rejected{
		{DeviceSerial: "NEW", Index: 0, Reason: domain.ReasonDeviceNotActive},
	}}
	if fmt.Sprint(got.res) != fmt.Sprint(want) {
		t.Errorf("result = %+v, want %+v", got.res, want)
	}
	if n := logs.FilterMessage("database unavailable, retrying").Len(); n != 1 {
		t.Errorf("logged %d retries, want 1", n)
	}

	// Exactly one run's worth of rows.
	var batches, devices int
	if err := pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM gateway_batches WHERE batch_id = 'b1'),
		(SELECT count(*) FROM devices WHERE serial = 'NEW')`).Scan(&batches, &devices); err != nil {
		t.Fatal(err)
	}
	if batches != 1 || devices != 1 {
		t.Errorf("gateway_batches = %d, NEW devices = %d; want 1 and 1", batches, devices)
	}
	stored := e.fx.StoredReadings(t, "A")
	if len(stored) != 2 || !stored[0].IsDecrease || stored[1].IsDecrease {
		t.Errorf("stored = %+v; want 90 with decrease, 110 without", stored)
	}
	// A repeat of the batch replays the stored answer.
	again, replayed, err := e.repo.IngestBatch(ctx, e.gw, b, rej)
	if err != nil || !replayed || fmt.Sprint(again) != fmt.Sprint(want) {
		t.Errorf("replay = %+v, %v, %v", again, replayed, err)
	}
}

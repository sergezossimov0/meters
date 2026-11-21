package postgres

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"

	"github.com/sergezossimov0/meters/internal/domain"
	"github.com/sergezossimov0/meters/internal/retry"
	"github.com/sergezossimov0/meters/internal/storage"
)

var _ storage.ReadingRepository = (*Readings)(nil)

// Readings is the PostgreSQL storage.ReadingRepository.
type Readings struct {
	db    *pgxpool.Pool
	retry retry.Retrier
}

// NewReadings returns a repository over pool; log receives retry warnings.
func NewReadings(pool *pgxpool.Pool, log *zap.Logger) *Readings {
	return &Readings{db: pool, retry: newRetrier(log)}
}

// IngestBatch implements storage.ReadingRepository in one transaction,
// repeated as a whole on connection errors (spec п. 10.3). A repeat after a
// commit whose acknowledgement was lost finds the batch and replays it.
func (r *Readings) IngestBatch(ctx context.Context, gatewayID int64, b domain.Batch, rejected []domain.Rejected) (domain.BatchResult, bool, error) {
	var (
		res      domain.BatchResult
		replayed bool
	)
	err := r.retry.Do(ctx, func(ctx context.Context) error {
		var err error
		res, replayed, err = r.ingestTx(ctx, gatewayID, b, rejected)
		return err
	})
	if err != nil {
		return domain.BatchResult{}, false, fmt.Errorf("ingest batch %q: %w", b.ID, mapError(err))
	}
	return res, replayed, nil
}

func (r *Readings) ingestTx(ctx context.Context, gatewayID int64, b domain.Batch, rejected []domain.Rejected) (domain.BatchResult, bool, error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return domain.BatchResult{}, false, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	// 1. Claim the batch first. A parallel request with the same batch_id
	// waits here on the unique key and, after the first commits, inserts
	// nothing and reads the stored result.
	tag, err := tx.Exec(ctx,
		`INSERT INTO gateway_batches (gateway_id, batch_id, result) VALUES ($1, $2, 'null')
		 ON CONFLICT DO NOTHING`, gatewayID, b.ID)
	if err != nil {
		return domain.BatchResult{}, false, fmt.Errorf("claim batch: %w", err)
	}
	if tag.RowsAffected() == 0 {
		res, err := storedResult(ctx, tx, gatewayID, b.ID)
		return res, true, err
	}

	// 2. Create unknown devices, in serial order: two batches creating the
	// same new devices take the unique-key locks in the same order.
	serials := make([]string, 0, len(b.Items))
	models := make([]string, 0, len(b.Items))
	for _, it := range slices.SortedFunc(slices.Values(b.Items), func(x, y domain.BatchItem) int {
		return cmp.Compare(x.Serial, y.Serial) // byte order, the same in every batch
	}) {
		serials = append(serials, it.Serial)
		models = append(models, it.Model)
	}
	// NOT EXISTS skips known serials before a sequence value is taken;
	// ON CONFLICT covers a device created by a parallel batch meanwhile.
	if _, err := tx.Exec(ctx,
		`INSERT INTO devices (serial, model, status, gateway_id)
		 SELECT u.serial, u.model, 'pending', $3
		 FROM unnest($1::text[], $2::text[]) WITH ORDINALITY AS u(serial, model, n)
		 WHERE NOT EXISTS (SELECT 1 FROM devices d WHERE d.serial = u.serial)
		 ORDER BY u.n
		 ON CONFLICT (serial) DO NOTHING`,
		serials, models, gatewayID); err != nil {
		return domain.BatchResult{}, false, fmt.Errorf("create devices: %w", err)
	}

	// 3. Lock the devices in id order: batches sharing a device are serial,
	// and the order rules out deadlocks between them.
	devices, err := lockDevices(ctx, tx, serials)
	if err != nil {
		return domain.BatchResult{}, false, err
	}

	// 4. Split items into rejected and active, read what Classify needs.
	rejected = slices.Clone(rejected)
	var active []activeItem
	for _, it := range b.Items {
		if len(it.Readings) == 0 {
			continue
		}
		d, ok := devices[it.Serial]
		if !ok {
			return domain.BatchResult{}, false, fmt.Errorf("device %q is neither found nor created", it.Serial)
		}
		if reason := domain.DeviceRejection(d.Status); reason != "" {
			rejected = append(rejected, domain.RejectAll(it, reason)...)
			continue
		}
		if d.InitialValue == nil {
			return domain.BatchResult{}, false, fmt.Errorf("active device %d without initial value", d.ID)
		}
		active = append(active, activeItem{device: d, incoming: domain.Readings(it.Readings)})
	}
	if err := loadHistory(ctx, tx, active); err != nil {
		return domain.BatchResult{}, false, err
	}

	// 5. Classify and insert.
	res := domain.BatchResult{}
	var ins insertRows
	for _, a := range active {
		accepted, dups := domain.Classify(*a.device.InitialValue, a.before, a.stored, a.incoming)
		res.Duplicates += dups
		ins.add(a.device.ID, accepted)
	}
	if n := len(ins.deviceIDs); n > 0 {
		tag, err := tx.Exec(ctx,
			`INSERT INTO readings (device_id, measured_at, value, is_decrease, gateway_id)
			 SELECT u.device_id, u.measured_at, u.value::numeric, u.is_decrease, $5
			 FROM unnest($1::bigint[], $2::timestamptz[], $3::text[], $4::boolean[])
			   AS u(device_id, measured_at, value, is_decrease)
			 ON CONFLICT DO NOTHING`,
			ins.deviceIDs, ins.times, ins.values, ins.flags, gatewayID)
		if err != nil {
			return domain.BatchResult{}, false, fmt.Errorf("insert readings: %w", err)
		}
		// The devices are locked, so nothing can slip in between; a
		// conflict would still be counted as a duplicate.
		res.Accepted = int(tag.RowsAffected())
		res.Duplicates += n - res.Accepted
	}
	res.Rejected = domain.SortRejected(b, rejected)

	// 6. Store the answer for replays and commit.
	body, err := json.Marshal(res)
	if err != nil {
		return domain.BatchResult{}, false, fmt.Errorf("encode result: %w", err)
	}
	if _, err := tx.Exec(ctx,
		`UPDATE gateway_batches SET result = $3 WHERE gateway_id = $1 AND batch_id = $2`,
		gatewayID, b.ID, body); err != nil {
		return domain.BatchResult{}, false, fmt.Errorf("store result: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.BatchResult{}, false, fmt.Errorf("commit: %w", err)
	}
	return res, false, nil
}

func storedResult(ctx context.Context, tx pgx.Tx, gatewayID int64, batchID string) (domain.BatchResult, error) {
	var body []byte
	if err := tx.QueryRow(ctx,
		`SELECT result FROM gateway_batches WHERE gateway_id = $1 AND batch_id = $2`,
		gatewayID, batchID).Scan(&body); err != nil {
		return domain.BatchResult{}, fmt.Errorf("read stored result: %w", err)
	}
	var res domain.BatchResult
	if err := json.Unmarshal(body, &res); err != nil {
		return domain.BatchResult{}, fmt.Errorf("decode stored result: %w", err)
	}
	if res.Rejected == nil {
		return domain.BatchResult{}, errors.New("stored result is incomplete")
	}
	return res, nil
}

type lockedDevice struct {
	ID           int64
	Status       domain.DeviceStatus
	InitialValue *domain.Value
}

func lockDevices(ctx context.Context, tx pgx.Tx, serials []string) (map[string]lockedDevice, error) {
	rows, err := tx.Query(ctx,
		`SELECT id, serial, status, initial_value::text FROM devices
		 WHERE serial = ANY($1) ORDER BY id FOR UPDATE`, serials)
	if err != nil {
		return nil, fmt.Errorf("lock devices: %w", err)
	}
	defer rows.Close()
	out := make(map[string]lockedDevice, len(serials))
	for rows.Next() {
		var (
			d       lockedDevice
			serial  string
			initial *string
		)
		if err := rows.Scan(&d.ID, &serial, &d.Status, &initial); err != nil {
			return nil, fmt.Errorf("lock devices: %w", err)
		}
		if initial != nil {
			v, err := domain.ParseValue(*initial)
			if err != nil {
				return nil, fmt.Errorf("device %d initial value: %w", d.ID, err)
			}
			d.InitialValue = &v
		}
		out[serial] = d
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("lock devices: %w", err)
	}
	return out, nil
}

// activeItem is an item of an active device with what Classify needs.
type activeItem struct {
	device   lockedDevice
	incoming []domain.Reading // sorted
	before   *domain.Reading
	stored   []domain.Reading
}

// loadHistory reads, for every active item, the reference reading before the
// incoming ones and the stored readings within their time span — one query
// for all devices.
func loadHistory(ctx context.Context, tx pgx.Tx, items []activeItem) error {
	if len(items) == 0 {
		return nil
	}
	ids := make([]int64, len(items))
	from := make([]time.Time, len(items))
	to := make([]time.Time, len(items))
	pos := make(map[int64]int, len(items))
	for i, a := range items {
		ids[i] = a.device.ID
		from[i] = a.incoming[0].MeasuredAt
		to[i] = a.incoming[len(a.incoming)-1].MeasuredAt
		pos[a.device.ID] = i
	}
	// The first part is the reference: the latest reading without the flag
	// before the span (LATERAL … LIMIT 1 walks the primary key backwards).
	// The second part is everything stored within the span.
	rows, err := tx.Query(ctx,
		`SELECT u.id, true, r.measured_at, r.value::text, r.is_decrease
		 FROM unnest($1::bigint[], $2::timestamptz[]) AS u(id, t_from)
		 CROSS JOIN LATERAL (
		     SELECT measured_at, value, is_decrease FROM readings
		     WHERE device_id = u.id AND measured_at < u.t_from AND NOT is_decrease
		     ORDER BY measured_at DESC LIMIT 1
		 ) r
		 UNION ALL
		 SELECT r.device_id, false, r.measured_at, r.value::text, r.is_decrease
		 FROM unnest($1::bigint[], $2::timestamptz[], $3::timestamptz[]) AS u(id, t_from, t_to)
		 JOIN readings r ON r.device_id = u.id AND r.measured_at BETWEEN u.t_from AND u.t_to`,
		ids, from, to)
	if err != nil {
		return fmt.Errorf("load history: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var (
			id       int64
			isBefore bool
			rd       domain.Reading
			value    string
		)
		if err := rows.Scan(&id, &isBefore, &rd.MeasuredAt, &value, &rd.IsDecrease); err != nil {
			return fmt.Errorf("load history: %w", err)
		}
		if rd.Value, err = domain.ParseValue(value); err != nil {
			return fmt.Errorf("load history: device %d: %w", id, err)
		}
		a := &items[pos[id]]
		if isBefore {
			a.before = &rd
		} else {
			a.stored = append(a.stored, rd)
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("load history: %w", err)
	}
	return nil
}

// insertRows are the columns of the readings to insert, for unnest.
type insertRows struct {
	deviceIDs []int64
	times     []time.Time
	values    []string
	flags     []bool
}

func (r *insertRows) add(deviceID int64, readings []domain.Reading) {
	for _, rd := range readings {
		r.deviceIDs = append(r.deviceIDs, deviceID)
		r.times = append(r.times, rd.MeasuredAt)
		r.values = append(r.values, rd.Value.String())
		r.flags = append(r.flags, rd.IsDecrease)
	}
}

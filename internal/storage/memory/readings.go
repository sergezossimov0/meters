package memory

import (
	"context"
	"errors"
	"slices"
	"sync"
	"time"

	"github.com/sergezossimov0/meters/internal/domain"
	"github.com/sergezossimov0/meters/internal/storage"
)

var _ storage.ReadingRepository = (*Readings)(nil)

// Readings is an in-memory storage.ReadingRepository holding devices,
// readings and processed batches. One mutex makes every batch atomic and
// serial, which is what row locks give in PostgreSQL.
type Readings struct {
	mu       sync.Mutex
	devices  map[string]*domain.Device  // by serial
	readings map[int64][]domain.Reading // by device ID, sorted by MeasuredAt
	batches  map[batchKey]domain.BatchResult
	nextID   int64
}

type batchKey struct {
	gatewayID int64
	batchID   string
}

// NewReadings returns an empty repository.
func NewReadings() *Readings {
	return &Readings{
		devices:  map[string]*domain.Device{},
		readings: map[int64][]domain.Reading{},
		batches:  map[batchKey]domain.BatchResult{},
	}
}

// IngestBatch implements storage.ReadingRepository.
func (r *Readings) IngestBatch(_ context.Context, gatewayID int64, b domain.Batch, rejected []domain.Rejected) (domain.BatchResult, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	key := batchKey{gatewayID, b.ID}
	if res, ok := r.batches[key]; ok {
		return cloneResult(res), true, nil
	}

	res := domain.BatchResult{}
	rejected = slices.Clone(rejected)
	// Work on copies: the batch must leave no trace if it fails half-way.
	type pending struct {
		id   int64
		rows []domain.Reading
	}
	var writes []pending
	var created []*domain.Device
	nextID := r.nextID
	for _, it := range b.Items {
		d, ok := r.devices[it.Serial]
		if !ok {
			nextID++
			d = &domain.Device{ID: nextID, Serial: it.Serial, Model: it.Model, Status: domain.DevicePending,
				GatewayID: gatewayID, CreatedAt: time.Now().UTC()}
			created = append(created, d)
		}
		if len(it.Readings) == 0 {
			continue
		}
		if reason := domain.DeviceRejection(d.Status); reason != "" {
			rejected = append(rejected, domain.RejectAll(it, reason)...)
			continue
		}
		if d.InitialValue == nil {
			return domain.BatchResult{}, false, errors.New("active device without initial value")
		}
		incoming := domain.Readings(it.Readings)
		first, last := incoming[0].MeasuredAt, incoming[len(incoming)-1].MeasuredAt
		stored := r.readings[d.ID]
		var before *domain.Reading
		var inRange []domain.Reading
		for i := range stored {
			s := stored[i]
			switch {
			case s.MeasuredAt.Before(first):
				if !s.IsDecrease {
					before = &stored[i]
				}
			case !s.MeasuredAt.After(last):
				inRange = append(inRange, s)
			}
		}
		accepted, dups := domain.Classify(*d.InitialValue, before, inRange, incoming)
		res.Accepted += len(accepted)
		res.Duplicates += dups
		writes = append(writes, pending{d.ID, accepted})
	}

	// Commit.
	r.nextID = nextID
	for _, d := range created {
		r.devices[d.Serial] = d
	}
	for _, w := range writes {
		rows := append(r.readings[w.id], w.rows...)
		slices.SortFunc(rows, func(x, y domain.Reading) int { return x.MeasuredAt.Compare(y.MeasuredAt) })
		r.readings[w.id] = rows
	}
	res.Rejected = domain.SortRejected(b, rejected)
	r.batches[key] = cloneResult(res)
	return res, false, nil
}

func cloneResult(res domain.BatchResult) domain.BatchResult {
	res.Rejected = slices.Clone(res.Rejected)
	return res
}

// SetDevice changes the status and the initial value of a known device, as
// approval does (L5). For tests. An unknown serial gives domain.ErrNotFound.
func (r *Readings) SetDevice(serial string, status domain.DeviceStatus, initial domain.Value) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	d, ok := r.devices[serial]
	if !ok {
		return domain.ErrNotFound
	}
	d.Status = status
	d.InitialValue = &initial
	return nil
}

// Device returns a copy of the device with this serial. For tests.
func (r *Readings) Device(serial string) (domain.Device, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	d, ok := r.devices[serial]
	if !ok {
		return domain.Device{}, false
	}
	return *d, true
}

// StoredReadings returns the readings of the device with this serial in time
// order. For tests.
func (r *Readings) StoredReadings(serial string) []domain.Reading {
	r.mu.Lock()
	defer r.mu.Unlock()
	d, ok := r.devices[serial]
	if !ok {
		return nil
	}
	return slices.Clone(r.readings[d.ID])
}

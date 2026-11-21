package storagetest

import (
	"fmt"
	"reflect"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/sergezossimov0/meters/internal/domain"
	"github.com/sergezossimov0/meters/internal/storage"
)

// IngestFixture prepares and inspects what ReadingRepository does not expose:
// gateways, device approval (L5) and stored rows.
type IngestFixture interface {
	// NewGateway returns the id of a new gateway.
	NewGateway(t *testing.T) int64
	// SetDevice sets status and initial value of a known device, as approval does.
	SetDevice(t *testing.T, serial string, status domain.DeviceStatus, initial domain.Value)
	// Device returns the device with this serial.
	Device(t *testing.T, serial string) (domain.Device, bool)
	// StoredReadings returns the device's readings in time order.
	StoredReadings(t *testing.T, serial string) []domain.Reading
}

// base is a whole hour two days ago: every test reading is valid "now".
var base = time.Now().UTC().Truncate(time.Hour).Add(-48 * time.Hour)

func hr(h int) time.Time { return base.Add(time.Duration(h) * time.Hour) }

// in builds a reading as the gateway sends it, at base + h hours.
func in(h int, v string) domain.ReadingRequest {
	return domain.ReadingRequest{MeasuredAt: hr(h), Value: v}
}

func dev(serial string, readings ...domain.ReadingRequest) domain.ItemRequest {
	return domain.ItemRequest{DeviceSerial: serial, Model: "WM-X1", Readings: readings}
}

func statusOnly(serial string) domain.ItemRequest {
	return domain.ItemRequest{DeviceSerial: serial, Model: "WM-X1", DeviceStatus: &domain.DeviceState{}}
}

// checked runs the batch through domain.ValidateBatch, as the service does.
func checked(t *testing.T, id string, items ...domain.ItemRequest) (domain.Batch, []domain.Rejected) {
	t.Helper()
	b, rej, err := domain.ValidateBatch(domain.BatchRequest{BatchID: id, Items: items}, time.Now())
	if err != nil {
		t.Fatalf("ValidateBatch: %v", err)
	}
	return b, rej
}

func ingest(t *testing.T, repo storage.ReadingRepository, gw int64, id string, items ...domain.ItemRequest) domain.BatchResult {
	t.Helper()
	b, rej := checked(t, id, items...)
	res, replayed, err := repo.IngestBatch(t.Context(), gw, b, rej)
	if err != nil {
		t.Fatalf("IngestBatch(%s): %v", id, err)
	}
	if replayed {
		t.Fatalf("IngestBatch(%s): unexpected replay", id)
	}
	return res
}

func wantResult(t *testing.T, got domain.BatchResult, accepted, dups int, rejected ...domain.Rejected) {
	t.Helper()
	if rejected == nil {
		rejected = []domain.Rejected{}
	}
	want := domain.BatchResult{Accepted: accepted, Duplicates: dups, Rejected: rejected}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("result = %+v\n          want %+v", got, want)
	}
}

// rd is a stored reading at base + h hours.
func rd(h int, v string, dec bool) domain.Reading {
	val, err := domain.ParseValue(v)
	if err != nil {
		panic(err)
	}
	return domain.Reading{MeasuredAt: hr(h), Value: val, IsDecrease: dec}
}

func wantStored(t *testing.T, f IngestFixture, serial string, want ...domain.Reading) {
	t.Helper()
	got := f.StoredReadings(t, serial)
	if !slices.EqualFunc(got, want, func(a, b domain.Reading) bool {
		return a.MeasuredAt.Equal(b.MeasuredAt) && a.Value == b.Value && a.IsDecrease == b.IsDecrease
	}) {
		t.Errorf("stored readings of %s = %v\n want %v", serial, got, want)
	}
}

// activate creates the device through a status-only batch and approves it.
func activate(t *testing.T, repo storage.ReadingRepository, f IngestFixture, gw int64, serial string, initial string) {
	t.Helper()
	ingest(t, repo, gw, "create-"+serial, statusOnly(serial))
	v, err := domain.ParseValue(initial)
	if err != nil {
		t.Fatal(err)
	}
	f.SetDevice(t, serial, domain.DeviceActive, v)
}

// ReadingRepository checks a storage.ReadingRepository. newRepo must return
// an empty repository and its fixture.
func ReadingRepository(t *testing.T, newRepo func(t *testing.T) (storage.ReadingRepository, IngestFixture)) {
	t.Run("unknown serial becomes pending, its readings are rejected", func(t *testing.T) {
		repo, f := newRepo(t)
		gw := f.NewGateway(t)
		res := ingest(t, repo, gw, "b1",
			dev("NEW-1", in(1, "10"), in(0, "9"), in(2, "bad")),
			dev("NEW-2", in(1, "1")))
		wantResult(t, res, 0, 0,
			domain.Rejected{DeviceSerial: "NEW-1", Index: 0, Reason: domain.ReasonDeviceNotActive},
			domain.Rejected{DeviceSerial: "NEW-1", Index: 1, Reason: domain.ReasonDeviceNotActive},
			domain.Rejected{DeviceSerial: "NEW-1", Index: 2, Reason: domain.ReasonInvalidValue},
			domain.Rejected{DeviceSerial: "NEW-2", Index: 0, Reason: domain.ReasonDeviceNotActive})
		d, ok := f.Device(t, "NEW-1")
		if !ok || d.Status != domain.DevicePending || d.Model != "WM-X1" || d.GatewayID != gw || d.ID <= 0 {
			t.Errorf("device = %+v, %v; want pending WM-X1 from gateway %d", d, ok, gw)
		}
		if _, ok := f.Device(t, "NEW-2"); !ok {
			t.Error("NEW-2 is not created")
		}
		wantStored(t, f, "NEW-1")
	})

	t.Run("status-only item creates a pending device without rejections", func(t *testing.T) {
		repo, f := newRepo(t)
		gw := f.NewGateway(t)
		res := ingest(t, repo, gw, "b1", statusOnly("S-1"))
		wantResult(t, res, 0, 0)
		if d, ok := f.Device(t, "S-1"); !ok || d.Status != domain.DevicePending {
			t.Errorf("device = %+v, %v", d, ok)
		}
	})

	t.Run("blocked and rejected devices", func(t *testing.T) {
		repo, f := newRepo(t)
		gw := f.NewGateway(t)
		activate(t, repo, f, gw, "BL", "0")
		activate(t, repo, f, gw, "RJ", "0")
		f.SetDevice(t, "BL", domain.DeviceBlocked, 0)
		f.SetDevice(t, "RJ", domain.DeviceRejected, 0)
		res := ingest(t, repo, gw, "b1", dev("RJ", in(1, "1")), dev("BL", in(1, "1"), in(2, "2")))
		wantResult(t, res, 0, 0,
			domain.Rejected{DeviceSerial: "RJ", Index: 0, Reason: domain.ReasonDeviceBlocked},
			domain.Rejected{DeviceSerial: "BL", Index: 0, Reason: domain.ReasonDeviceBlocked},
			domain.Rejected{DeviceSerial: "BL", Index: 1, Reason: domain.ReasonDeviceBlocked})
		wantStored(t, f, "BL")
	})

	t.Run("active device: accepted with decrease against initial value", func(t *testing.T) {
		repo, f := newRepo(t)
		gw := f.NewGateway(t)
		activate(t, repo, f, gw, "A", "100")
		res := ingest(t, repo, gw, "b1", dev("A", in(2, "101.5"), in(1, "99.999"), in(3, "1.2345")))
		wantResult(t, res, 2, 0, domain.Rejected{DeviceSerial: "A", Index: 2, Reason: domain.ReasonInvalidValue})
		wantStored(t, f, "A", rd(1, "99.999", true), rd(2, "101.5", false))
	})

	t.Run("model of a known device is kept", func(t *testing.T) {
		repo, f := newRepo(t)
		gw := f.NewGateway(t)
		activate(t, repo, f, gw, "A", "0")
		ingest(t, repo, gw, "b1", domain.ItemRequest{DeviceSerial: "A", Model: "Other", Readings: []domain.ReadingRequest{in(1, "1")}})
		if d, _ := f.Device(t, "A"); d.Model != "WM-X1" {
			t.Errorf("model = %q, want the first one", d.Model)
		}
	})

	t.Run("duplicates are counted and not overwritten", func(t *testing.T) {
		repo, f := newRepo(t)
		gw := f.NewGateway(t)
		activate(t, repo, f, gw, "A", "0")
		ingest(t, repo, gw, "b1", dev("A", in(1, "10"), in(2, "20")))
		res := ingest(t, repo, gw, "b2", dev("A", in(2, "999"), in(3, "30"), in(3, "31")))
		wantResult(t, res, 1, 2)
		wantStored(t, f, "A", rd(1, "10", false), rd(2, "20", false), rd(3, "30", false))
	})

	t.Run("decrease uses stored readings: roll-back 100 -> 90 -> 91 across batches", func(t *testing.T) {
		repo, f := newRepo(t)
		gw := f.NewGateway(t)
		activate(t, repo, f, gw, "A", "50")
		ingest(t, repo, gw, "b1", dev("A", in(1, "100")))
		ingest(t, repo, gw, "b2", dev("A", in(2, "90")))
		ingest(t, repo, gw, "b3", dev("A", in(3, "91")))
		// A late reading between stored ones compares with the earlier one.
		ingest(t, repo, gw, "b4", dev("A", in(0, "60"), in(4, "100.001")))
		wantStored(t, f, "A", rd(0, "60", false), rd(1, "100", false), rd(2, "90", true), rd(3, "91", true), rd(4, "100.001", false))
	})

	t.Run("repeated batch_id returns the stored result and writes nothing", func(t *testing.T) {
		repo, f := newRepo(t)
		gw := f.NewGateway(t)
		activate(t, repo, f, gw, "A", "0")
		first := ingest(t, repo, gw, "b1", dev("A", in(1, "10"), in(2, "x")), dev("NEW", in(1, "1")))

		// Same id, other content: still the first answer.
		b, rej := checked(t, "b1", dev("A", in(3, "30")), dev("NEW-2", in(1, "1")))
		got, replayed, err := repo.IngestBatch(t.Context(), gw, b, rej)
		if err != nil || !replayed || !reflect.DeepEqual(got, first) {
			t.Fatalf("replay = %+v, %v, %v; want %+v, true", got, replayed, err, first)
		}
		wantStored(t, f, "A", rd(1, "10", false))
		if _, ok := f.Device(t, "NEW-2"); ok {
			t.Error("a replay must not create devices")
		}
	})

	t.Run("same batch_id from another gateway is another batch", func(t *testing.T) {
		repo, f := newRepo(t)
		gw1, gw2 := f.NewGateway(t), f.NewGateway(t)
		activate(t, repo, f, gw1, "A", "0")
		ingest(t, repo, gw1, "b1", dev("A", in(1, "10")))
		res := ingest(t, repo, gw2, "b1", dev("A", in(1, "10"), in(2, "20")))
		wantResult(t, res, 1, 1)
	})

	t.Run("parallel repeats of one batch: one is processed, all answer the same", func(t *testing.T) {
		repo, f := newRepo(t)
		gw := f.NewGateway(t)
		activate(t, repo, f, gw, "A", "0")
		b, rej := checked(t, "b1", dev("A", in(1, "10"), in(2, "20")), dev("NEW", in(1, "1")))
		const n = 8
		results := make([]domain.BatchResult, n)
		replays := make([]bool, n)
		errs := make([]error, n)
		var wg sync.WaitGroup
		for i := range n {
			wg.Go(func() { results[i], replays[i], errs[i] = repo.IngestBatch(t.Context(), gw, b, rej) })
		}
		wg.Wait()
		processed := 0
		for i := range n {
			if errs[i] != nil {
				t.Fatalf("call %d: %v", i, errs[i])
			}
			if !replays[i] {
				processed++
			}
			if !reflect.DeepEqual(results[i], results[0]) {
				t.Errorf("call %d answered %+v, call 0 %+v", i, results[i], results[0])
			}
		}
		if processed != 1 {
			t.Errorf("%d calls processed the batch, want 1", processed)
		}
		wantStored(t, f, "A", rd(1, "10", false), rd(2, "20", false))
	})

	t.Run("parallel batches for one new device create it once", func(t *testing.T) {
		repo, f := newRepo(t)
		gws := []int64{f.NewGateway(t), f.NewGateway(t), f.NewGateway(t)}
		var wg sync.WaitGroup
		errs := make([]error, len(gws))
		for i, gw := range gws {
			wg.Go(func() {
				// Overlapping new serials in different orders.
				b, rej := checked(t, fmt.Sprintf("b%d", i), statusOnly(fmt.Sprintf("N%d", i%2)), dev("SHARED", in(1, "1")), dev(fmt.Sprintf("N%d", (i+1)%2), in(1, "1")))
				_, _, errs[i] = repo.IngestBatch(t.Context(), gw, b, rej)
			})
		}
		wg.Wait()
		for i, err := range errs {
			if err != nil {
				t.Errorf("batch %d: %v", i, err)
			}
		}
		for _, s := range []string{"SHARED", "N0", "N1"} {
			if d, ok := f.Device(t, s); !ok || d.Status != domain.DevicePending {
				t.Errorf("%s = %+v, %v", s, d, ok)
			}
		}
	})
}

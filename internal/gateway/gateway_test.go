package gateway

import (
	"context"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/sergezossimov0/meters/internal/config"
	"github.com/sergezossimov0/meters/internal/domain"
	"github.com/sergezossimov0/meters/internal/httpapi"
	"github.com/sergezossimov0/meters/internal/service"
	"github.com/sergezossimov0/meters/internal/storage/memory"
)

// stand is the real server router on memory storage behind a real HTTP
// server, with one registered gateway.
type stand struct {
	url      string
	key      string
	readings *memory.Readings
}

func newStand(t *testing.T) stand {
	t.Helper()
	gateways := service.NewGateways(memory.NewGateways())
	_, key, err := gateways.Create(t.Context(), "e2e")
	if err != nil {
		t.Fatal(err)
	}
	readings := memory.NewReadings()
	srv := httptest.NewServer(httpapi.NewRouter(httpapi.Deps{
		Log: zap.NewNop(), MaxBodyBytes: 10 << 20,
		Gateways: gateways, SignKey: signKey, Ingest: service.NewIngest(readings),
	}))
	t.Cleanup(srv.Close)
	return stand{url: srv.URL, key: key, readings: readings}
}

func (s stand) config(file string) config.Gateway {
	return config.Gateway{
		ServerURL: s.url, GatewayKey: config.Secret(s.key), Key: config.Secret(signKey),
		PollInterval: 20 * time.Millisecond, ReportInterval: 50 * time.Millisecond,
		RateLimit: 2, StoreInterval: 100 * time.Millisecond, FileStoragePath: file,
		Restore: true, Devices: 4, Speed: 3600 * 24, Backfill: 2 * day, BatchSize: 100,
	}
}

// runFor runs the gateway for d, stops it and returns Run's error.
func runFor(t *testing.T, cfg config.Gateway, d time.Duration) error {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), d)
	defer cancel()
	return Run(ctx, cfg, zap.NewNop())
}

// End to end: the simulator sends signed gzip batches to the real server,
// which creates its devices as pending; the readings are answered with
// device_not_active and leave the buffer. A restart continues from the file.
func TestRun_EndToEnd(t *testing.T) {
	st := newStand(t)
	file := filepath.Join(t.TempDir(), "buffer.json")
	cfg := st.config(file)

	// 2 model days at 1 model day per real second: caught up after ~2 s.
	if err := runFor(t, cfg, 3*time.Second); err != nil {
		t.Fatalf("Run: %v", err)
	}

	serials := NewFleet(st.key, cfg.Devices, t0, nil).States()
	for _, d := range serials {
		dev, ok := st.readings.Device(d.Serial)
		if !ok || dev.Status != domain.DevicePending || dev.Model == "" {
			t.Errorf("%s on the server: %+v, %v; want a pending device", d.Serial, dev, ok)
		}
	}

	first, ok, err := Load(file)
	if err != nil || !ok {
		t.Fatalf("buffer file after shutdown: %v, %v", ok, err)
	}
	if len(first.Readings) > 10 {
		t.Errorf("%d readings left in the buffer; the server answered them all", len(first.Readings))
	}
	if since := time.Since(first.ModelTime); since < 0 || since > time.Minute {
		t.Errorf("model time %v: want caught up with real time", first.ModelTime)
	}
	last := map[string]DeviceState{}
	for _, d := range first.Devices {
		if d.Last.IsZero() {
			t.Errorf("%s never reported", d.Serial)
		}
		last[d.Serial] = d
	}

	// Restart with -restore: the counters continue, nothing goes backwards.
	if err := runFor(t, cfg, 300*time.Millisecond); err != nil {
		t.Fatalf("second Run: %v", err)
	}
	second, _, err := Load(file)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range second.Devices {
		prev := last[d.Serial]
		if d.Last.Before(prev.Last) || d.Value < prev.Value || !d.Since.Equal(prev.Since) {
			t.Errorf("%s went back after restart: %+v -> %+v", d.Serial, prev, d)
		}
	}
	if second.ModelTime.Before(first.ModelTime) {
		t.Errorf("model time went back: %v -> %v", first.ModelTime, second.ModelTime)
	}
}

// A server that refuses the gateway (wrong key): readings stay and are saved.
func TestRun_WrongKeyKeepsReadings(t *testing.T) {
	st := newStand(t)
	file := filepath.Join(t.TempDir(), "buffer.json")
	cfg := st.config(file)
	cfg.GatewayKey = "gwk_not-registered-key-0000000000000000000000"

	if err := runFor(t, cfg, time.Second); err != nil {
		t.Fatalf("Run: %v", err)
	}
	state, ok, err := Load(file)
	if err != nil || !ok || len(state.Readings) == 0 {
		t.Fatalf("buffer file: ok=%v err=%v readings=%d; want readings kept", ok, err, len(state.Readings))
	}
	if _, found := st.readings.Device(state.Readings[0].Serial); found {
		t.Error("the server must not have accepted anything")
	}
}

// A buffer file that cannot be written is reported by Run on shutdown.
func TestRun_FinalSaveFails(t *testing.T) {
	st := newStand(t)
	cfg := st.config(filepath.Join(t.TempDir(), "missing-dir", "buffer.json"))
	cfg.Restore = false
	if err := runFor(t, cfg, 100*time.Millisecond); err == nil {
		t.Error("an unwritable buffer file must fail the final save")
	}
}

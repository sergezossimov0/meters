package gateway

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"go.uber.org/zap"
)

func testState() State {
	return State{
		ModelTime: t0.Add(5 * time.Hour),
		Devices:   NewFleet(testGatewayKey, 3, t0, nil).States(),
		Readings:  []Reading{rdg("A", 1, 1000), rdg("B", 2, 2500)},
	}
}

func TestSaveLoad_RoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "buffer.json")
	want := testState()
	if err := Save(path, want); err != nil {
		t.Fatal(err)
	}
	got, ok, err := Load(path)
	if err != nil || !ok {
		t.Fatalf("Load = %v, %v", ok, err)
	}
	if !got.ModelTime.Equal(want.ModelTime) || !slices.Equal(got.Devices, want.Devices) ||
		!slices.EqualFunc(got.Readings, want.Readings, func(a, b Reading) bool {
			return a.Serial == b.Serial && a.Model == b.Model && a.MeasuredAt.Equal(b.MeasuredAt) && a.Value == b.Value
		}) {
		t.Errorf("Load = %+v\nwant   %+v", got, want)
	}
	entries, _ := os.ReadDir(filepath.Dir(path))
	if len(entries) != 1 {
		t.Errorf("directory holds %v: no temporary files may stay", entries)
	}
	if info, _ := os.Stat(path); info.Mode().Perm() != 0o600 {
		t.Errorf("file mode %v, want 0600", info.Mode().Perm())
	}
	body, _ := os.ReadFile(path)
	if !strings.Contains(string(body), `"value":"1.000"`) || !strings.Contains(string(body), `"format":1`) {
		t.Errorf("file %s: values as strings and a format version expected", body)
	}
}

func TestLoad_MissingDamagedUnknown(t *testing.T) {
	dir := t.TempDir()
	if _, ok, err := Load(filepath.Join(dir, "none.json")); ok || err != nil {
		t.Errorf("missing file: ok=%v err=%v; want nothing, no error", ok, err)
	}
	for name, body := range map[string]string{
		"torn":    `{"format":1,"readings":[{"serial":"A"`,
		"format":  `{"format":2,"readings":[]}`,
		"no fmt":  `{"readings":[]}`,
		"bad val": `{"format":1,"readings":[{"serial":"A","value":"-1"}]}`,
	} {
		path := filepath.Join(dir, name+".json")
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, ok, err := Load(path); ok || err == nil {
			t.Errorf("%s: ok=%v err=%v; want an error", name, ok, err)
		}
	}
	if _, _, err := Load(dir); err == nil {
		t.Error("a directory instead of a file must be an error")
	}
}

// A failed write leaves the previous file as it was.
func TestSave_FailureKeepsOldFile(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "buffer.json")
	if err := Save(path, testState()); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(path)
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })

	if err := Save(path, State{ModelTime: t0}); err == nil {
		t.Fatal("Save into a read-only directory must fail")
	}
	after, _ := os.ReadFile(path)
	if string(before) != string(after) {
		t.Error("the old file was changed by a failed save")
	}
	if err := Save(filepath.Join(dir, "missing", "b.json"), State{}); err == nil {
		t.Error("Save into a missing directory must fail")
	}
}

func newTestSource(t *testing.T) (*Source, *Buffer) {
	t.Helper()
	clock := NewClock(30*day, 3600)
	buf := NewBuffer(nil)
	return NewSource(clock, NewFleet(testGatewayKey, 4, clock.Now(), nil), buf), buf
}

// A snapshot never catches the devices ahead of or behind their readings:
// each device's last reading time equals its newest buffered reading.
func TestSource_SnapshotIsConsistent(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		src, _ := newTestSource(t)
		var wg sync.WaitGroup
		wg.Go(func() {
			for range 200 {
				time.Sleep(time.Second)
				src.Poll()
			}
		})
		wg.Go(func() {
			for range 400 {
				time.Sleep(500 * time.Millisecond)
				state, _ := src.State()
				newest := map[string]time.Time{}
				for _, r := range state.Readings {
					if r.MeasuredAt.After(newest[r.Serial]) {
						newest[r.Serial] = r.MeasuredAt
					}
				}
				for _, d := range state.Devices {
					if !d.Last.Equal(newest[d.Serial]) {
						t.Fatalf("%s: device at %v, newest reading %v", d.Serial, d.Last, newest[d.Serial])
					}
				}
			}
		})
		wg.Wait()
	})
}

func TestSaver_Interval(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "buffer.json")
		src, _ := newTestSource(t)
		s := NewSaver(path, 300*time.Second, src, zap.NewNop())
		done := make(chan struct{})
		go func() { s.Run(t.Context()); close(done) }()

		time.Sleep(10 * time.Second)
		src.Poll()
		if _, ok, _ := Load(path); ok {
			t.Fatal("saved before the interval")
		}
		time.Sleep(291 * time.Second)
		synctest.Wait()
		st, ok, err := Load(path)
		if !ok || err != nil || len(st.Readings) == 0 {
			t.Fatalf("after 300 s: ok=%v err=%v readings=%d", ok, err, len(st.Readings))
		}
	})
}

func TestSaver_Synchronous(t *testing.T) {
	path := filepath.Join(t.TempDir(), "buffer.json")
	src, buf := newTestSource(t)
	s := NewSaver(path, 0, src, zap.NewNop())

	buf.Add(rdg("X", 1, 1))
	s.Changed()
	st, ok, _ := Load(path)
	if !ok || len(st.Readings) != 1 {
		t.Fatalf("with -i 0 the change must be on disk at once: %+v", st)
	}
	batch, _ := buf.Take(10)
	buf.Ack(batch.ID)
	s.Changed()
	if st, _, _ := Load(path); len(st.Readings) != 0 {
		t.Errorf("after Ack the file still holds %v", st.Readings)
	}
}

// An unchanged buffer is not written again; a failed write is retried at
// the next call.
func TestSaver_SkipsUnchanged(t *testing.T) {
	path := filepath.Join(t.TempDir(), "buffer.json")
	src, buf := newTestSource(t)
	s := NewSaver(path, time.Minute, src, zap.NewNop())
	if err := s.Save(); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := s.Save(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err == nil {
		t.Error("unchanged buffer was written again")
	}
	buf.Add(rdg("X", 1, 1))
	if err := s.Save(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("changed buffer was not written: %v", err)
	}

	broken := NewSaver(filepath.Join(t.TempDir(), "missing", "b.json"), 0, src, zap.NewNop())
	broken.Changed() // logs the error, does not panic
	if err := broken.Save(); err == nil {
		t.Error("a failed save must be retried, not skipped as unchanged")
	}
}

func TestSaver_RunWithoutInterval(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		src, _ := newTestSource(t)
		s := NewSaver(filepath.Join(t.TempDir(), "b.json"), 0, src, zap.NewNop())
		ctx, cancel := context.WithCancel(t.Context())
		done := make(chan struct{})
		go func() { s.Run(ctx); close(done) }()
		time.Sleep(time.Hour)
		cancel()
		synctest.Wait()
		select {
		case <-done:
		default:
			t.Error("Run did not stop")
		}
	})
}

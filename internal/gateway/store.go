package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"time"

	"go.uber.org/zap"
)

// stateFormat is the version of the buffer file format.
const stateFormat = 1

// State is what the buffer file holds (spec п. 7.3): the unsent readings,
// in flight included, and what the simulation needs to continue after a
// restart without starting the meters over — the model time and the
// devices' counters.
type State struct {
	Format    int           `json:"format"`
	ModelTime time.Time     `json:"model_time"`
	Devices   []DeviceState `json:"devices"`
	Readings  []Reading     `json:"readings"`
}

// Save writes s to path atomically: into a temporary file in the same
// directory, flushed to disk, then renamed over path. A crash at any moment
// leaves either the old file or the new one, never a torn one.
func Save(path string, s State) (err error) {
	s.Format = stateFormat
	body, err := json.Marshal(s)
	if err != nil {
		return fmt.Errorf("encode buffer: %w", err)
	}
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("save buffer: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tmp.Close()
			_ = os.Remove(tmp.Name())
		}
	}()
	if _, err = tmp.Write(body); err != nil {
		return fmt.Errorf("save buffer: %w", err)
	}
	if err = tmp.Sync(); err != nil {
		return fmt.Errorf("save buffer: %w", err)
	}
	if err = tmp.Close(); err != nil {
		return fmt.Errorf("save buffer: %w", err)
	}
	if err = os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("save buffer: %w", err)
	}
	// Make the rename itself durable; not every system can sync a directory.
	if d, derr := os.Open(filepath.Clean(dir)); derr == nil {
		_ = d.Sync()
		_ = d.Close()
	}
	return nil
}

// Load reads the buffer file. A missing file is not an error: ok is false.
// A damaged file or an unknown format is an error, so the gateway does not
// start and overwrite readings someone may still recover.
func Load(path string) (s State, ok bool, err error) {
	body, err := os.ReadFile(filepath.Clean(path)) // the path comes from the operator (-f)
	if errors.Is(err, fs.ErrNotExist) {
		return State{}, false, nil
	}
	if err != nil {
		return State{}, false, fmt.Errorf("load buffer: %w", err)
	}
	if err := json.Unmarshal(body, &s); err != nil {
		return State{}, false, fmt.Errorf("load buffer %s: %w", path, err)
	}
	if s.Format != stateFormat {
		return State{}, false, fmt.Errorf("load buffer %s: unknown format %d", path, s.Format)
	}
	return s, true, nil
}

// Source joins the model clock, the devices and the buffer. A poll moves the
// devices and adds their readings as one step, and a snapshot is taken
// between steps: a file never holds counters ahead of the readings (readings
// lost on restart) or behind them (hours replayed with other values, which
// the server would flag as decrease).
type Source struct {
	mu    sync.Mutex
	clock *Clock
	fleet *Fleet
	buf   *Buffer
}

// NewSource wires the parts.
func NewSource(clock *Clock, fleet *Fleet, buf *Buffer) *Source {
	return &Source{clock: clock, fleet: fleet, buf: buf}
}

// Poll takes the readings due by the model time into the buffer and returns
// how many there were.
func (s *Source) Poll() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	rs := s.fleet.Poll(s.clock.Now())
	s.buf.Add(rs...)
	return len(rs)
}

// State returns a consistent snapshot for the file and the buffer version.
func (s *Source) State() (State, uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	readings, version := s.buf.Snapshot()
	return State{ModelTime: s.clock.Now(), Devices: s.fleet.States(), Readings: readings}, version
}

// Saver writes the buffer file: every interval, or after every change when
// the interval is 0 (spec п. 7.3, -i).
type Saver struct {
	path     string
	interval time.Duration
	src      *Source
	log      *zap.Logger

	mu      sync.Mutex // one write at a time, snapshots in order
	saved   bool
	version uint64
}

// NewSaver returns a saver of src to path.
func NewSaver(path string, interval time.Duration, src *Source, log *zap.Logger) *Saver {
	return &Saver{path: path, interval: interval, src: src, log: log}
}

// Save writes the current state unless the buffer has not changed since the
// last write. The snapshot is taken under the saver's lock, so a later call
// never writes an older state.
func (s *Saver) Save() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	state, version := s.src.State()
	if s.saved && version == s.version {
		return nil
	}
	if err := Save(s.path, state); err != nil {
		return err
	}
	s.saved, s.version = true, version
	s.log.Debug("buffer saved", zap.Int("readings", len(state.Readings)))
	return nil
}

// Changed is called after the buffer changed; with interval 0 it saves at
// once, in the caller's goroutine.
func (s *Saver) Changed() {
	if s.interval == 0 {
		s.saveAndLog()
	}
}

// Run saves every interval until ctx is done. With interval 0 it only waits:
// Changed does the work. The final save on shutdown is the caller's.
func (s *Saver) Run(ctx context.Context) {
	if s.interval == 0 {
		<-ctx.Done()
		return
	}
	t := time.NewTicker(s.interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.saveAndLog()
		}
	}
}

func (s *Saver) saveAndLog() {
	if err := s.Save(); err != nil {
		s.log.Error("save buffer failed", zap.String("file", s.path), zap.Error(err))
	}
}

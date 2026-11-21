package gateway

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"math/rand/v2"
	"sync"
	"time"

	"github.com/sergezossimov0/meters/internal/domain"
)

// Profile is how a simulated meter behaves (spec п. 7.1).
type Profile int

// Profiles.
const (
	// ProfileNormal grows by a random amount every hour.
	ProfileNormal Profile = iota
	// ProfileRollback is a tampered meter: once, SpecialAfter into the
	// model, its value drops by 5 %; then it grows again from there.
	ProfileRollback
	// ProfileSilent stops answering SpecialAfter into the model.
	ProfileSilent
)

func (p Profile) String() string {
	switch p {
	case ProfileRollback:
		return "rollback"
	case ProfileSilent:
		return "silent"
	default:
		return "normal"
	}
}

// Simulation constants.
const (
	// ReadingStep: every device reports on each whole model hour.
	ReadingStep = time.Hour
	// SpecialAfter is when the rollback and silent profiles act, counted
	// from the device's first model time. With a 30-day backfill the silent
	// meter's last reading is far older than 48 h when the model catches up.
	SpecialAfter = 3 * 24 * time.Hour
	// rollbackPercent is how much the rollback profile loses at once.
	rollbackPercent = 5
	// maxInitial bounds the random initial value: 1000 units.
	maxInitial = 1000 * 1000
)

// resource is a kind of meter: the model name and the mean hourly
// consumption in thousandths of the unit.
type resource struct {
	model   string
	perHour int64
}

var resources = [...]resource{
	{"SimWater-1", 10},  // 10 l/h of water, m3
	{"SimGas-1", 50},    // 0.05 m3/h of gas
	{"SimPower-1", 300}, // 0.3 kWh/h
	{"SimHeat-1", 2},    // 0.002 Gcal/h
}

// Reading is one reading taken from a device by the gateway.
type Reading struct {
	Serial     string       `json:"serial"`
	Model      string       `json:"model"`
	MeasuredAt time.Time    `json:"measured_at"`
	Value      domain.Value `json:"value"`
}

// DeviceState is what a device needs to continue after a restart; it goes
// into the buffer file (spec п. 7.3).
type DeviceState struct {
	Serial     string       `json:"serial"`
	Value      domain.Value `json:"value"`
	Since      time.Time    `json:"since"`         // first model time of the device
	Last       time.Time    `json:"last,omitzero"` // time of the last reading; zero: none yet
	RolledBack bool         `json:"rolled_back,omitempty"`
}

type device struct {
	DeviceState
	model   string
	profile Profile
	perHour int64
}

// Fleet is the set of simulated devices of one gateway. Serial numbers,
// models, profiles and initial values follow from the gateway key, so a
// restarted gateway has the same devices. Safe for concurrent use.
type Fleet struct {
	mu      sync.Mutex
	devices []*device
	rng     *rand.Rand
}

// NewFleet creates n devices whose model time starts at start. States saved
// before a restart are matched by serial number; unknown ones are ignored.
// With n ≥ 3 the last two devices are rollback and silent.
func NewFleet(gatewayKey string, n int, start time.Time, saved []DeviceState) *Fleet {
	sum := sha256.Sum256([]byte(gatewayKey))
	prefix := hex.EncodeToString(sum[:4])
	// A seeded generator on purpose: the simulation must repeat for the same
	// key. Nothing secret is derived from it.
	rng := rand.New(rand.NewPCG( //nolint:gosec // deterministic simulation, not security
		binary.BigEndian.Uint64(sum[8:16]), binary.BigEndian.Uint64(sum[16:24])))

	bySerial := make(map[string]DeviceState, len(saved))
	for _, s := range saved {
		bySerial[s.Serial] = s
	}
	f := &Fleet{devices: make([]*device, n), rng: rng}
	for i := range n {
		res := resources[i%len(resources)]
		d := &device{model: res.model, perHour: res.perHour}
		d.Serial = fmt.Sprintf("SIM-%s-%04d", prefix, i+1)
		switch {
		case n >= 3 && i == n-2:
			d.profile = ProfileRollback
		case n >= 3 && i == n-1:
			d.profile = ProfileSilent
		}
		// Drawn for every device, restored or not, so the sequence and
		// thus the other devices do not depend on what was saved.
		initial := domain.Value(rng.Int64N(maxInitial))
		if s, ok := bySerial[d.Serial]; ok {
			d.DeviceState = s
		} else {
			d.Value = initial
			d.Since = start.UTC()
		}
		f.devices[i] = d
	}
	return f
}

// Poll returns the readings of all devices for every whole model hour since
// their previous reading, up to and including modelNow, in time order per
// device.
func (f *Fleet) Poll(modelNow time.Time) []Reading {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []Reading
	for _, d := range f.devices {
		for next := d.next(); !next.After(modelNow); next = d.next() {
			if d.profile == ProfileSilent && !next.Before(d.Since.Add(SpecialAfter)) {
				break
			}
			d.step(f.rng, next)
			out = append(out, Reading{Serial: d.Serial, Model: d.model, MeasuredAt: next, Value: d.Value})
		}
	}
	return out
}

// next is the time of the device's next reading: the hour after the last
// one, or the first whole hour not before Since.
func (d *device) next() time.Time {
	if !d.Last.IsZero() {
		return d.Last.Add(ReadingStep)
	}
	first := d.Since.Truncate(ReadingStep)
	if first.Before(d.Since) {
		first = first.Add(ReadingStep)
	}
	return first
}

// step advances the device to the reading at t.
func (d *device) step(rng *rand.Rand, t time.Time) {
	// 0.5…1.5 of the mean hourly consumption.
	d.Value += domain.Value(d.perHour/2 + rng.Int64N(d.perHour+1))
	if d.profile == ProfileRollback && !d.RolledBack && !t.Before(d.Since.Add(SpecialAfter)) {
		d.Value -= d.Value * rollbackPercent / 100
		d.RolledBack = true
	}
	d.Value = min(d.Value, domain.MaxValue)
	d.Last = t
}

// States returns the devices' states for the buffer file.
func (f *Fleet) States() []DeviceState {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]DeviceState, len(f.devices))
	for i, d := range f.devices {
		out[i] = d.DeviceState
	}
	return out
}

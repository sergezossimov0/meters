package gateway

import (
	"encoding/json"
	"regexp"
	"slices"
	"testing"
	"time"

	"github.com/sergezossimov0/meters/internal/domain"
)

const testGatewayKey = "gwk_test-key"

var t0 = time.Date(2026, 10, 1, 10, 30, 0, 0, time.UTC)

// byDevice groups readings by serial, keeping the order.
func byDevice(rs []Reading) map[string][]Reading {
	out := map[string][]Reading{}
	for _, r := range rs {
		out[r.Serial] = append(out[r.Serial], r)
	}
	return out
}

func TestFleet_Serials(t *testing.T) {
	a := NewFleet(testGatewayKey, 5, t0, nil)
	again := NewFleet(testGatewayKey, 5, t0, nil)
	other := NewFleet("gwk_other-key", 5, t0, nil)

	pattern := regexp.MustCompile(`^SIM-[0-9a-f]{8}-\d{4}$`)
	var serials []string
	for i, s := range a.States() {
		if !pattern.MatchString(s.Serial) {
			t.Errorf("serial %q does not match %s", s.Serial, pattern)
		}
		if again.States()[i] != s {
			t.Errorf("device %d differs for the same key: %+v vs %+v", i, s, again.States()[i])
		}
		if other.States()[i].Serial == s.Serial {
			t.Errorf("device %d has the same serial under another key", i)
		}
		serials = append(serials, s.Serial)
	}
	if len(slices.Compact(slices.Clone(serials))) != 5 || serials[0][len(serials[0])-4:] != "0001" {
		t.Errorf("serials = %v: want 5 distinct, numbered from 0001", serials)
	}
	// Serial numbers are valid for the server.
	for _, s := range serials {
		if _, _, err := domain.ValidateBatch(domain.BatchRequest{BatchID: "b", Items: []domain.ItemRequest{
			{DeviceSerial: s, Model: "SimWater-1", DeviceStatus: &domain.DeviceState{}},
		}}, t0); err != nil {
			t.Errorf("serial %q is refused by the server: %v", s, err)
		}
	}
}

func TestFleet_Profiles(t *testing.T) {
	profiles := func(n int) []Profile {
		f := NewFleet(testGatewayKey, n, t0, nil)
		var out []Profile
		for _, d := range f.devices {
			out = append(out, d.profile)
		}
		return out
	}
	if got := profiles(1); !slices.Equal(got, []Profile{ProfileNormal}) {
		t.Errorf("n=1: %v", got)
	}
	if got := profiles(2); !slices.Equal(got, []Profile{ProfileNormal, ProfileNormal}) {
		t.Errorf("n=2: %v", got)
	}
	if got := profiles(4); !slices.Equal(got, []Profile{ProfileNormal, ProfileNormal, ProfileRollback, ProfileSilent}) {
		t.Errorf("n=4: %v", got)
	}
	if ProfileRollback.String() != "rollback" || ProfileSilent.String() != "silent" || ProfileNormal.String() != "normal" {
		t.Error("profile names")
	}
}

// Readings come on every whole hour, without gaps, from the first whole hour
// after the start; a poll catches up all hours since the previous one.
func TestFleet_HourlyReadings(t *testing.T) {
	f := NewFleet(testGatewayKey, 2, t0, nil) // t0 = 10:30
	if got := f.Poll(t0.Add(20 * time.Minute)); len(got) != 0 {
		t.Fatalf("before 11:00: %v", got)
	}
	first := f.Poll(t0.Add(30 * time.Minute)) // exactly 11:00
	if len(first) != 2 || !first[0].MeasuredAt.Equal(time.Date(2026, 10, 1, 11, 0, 0, 0, time.UTC)) {
		t.Fatalf("at 11:00: %v", first)
	}
	more := f.Poll(t0.Add(5*time.Hour + 10*time.Minute)) // 15:40: 12:00…15:00
	got := byDevice(more)
	for serial, rs := range got {
		if len(rs) != 4 {
			t.Errorf("%s: %d readings, want 4", serial, len(rs))
		}
		for i, r := range rs {
			want := time.Date(2026, 10, 1, 12+i, 0, 0, 0, time.UTC)
			if !r.MeasuredAt.Equal(want) {
				t.Errorf("%s reading %d at %v, want %v", serial, i, r.MeasuredAt, want)
			}
		}
	}
	if again := f.Poll(t0.Add(5*time.Hour + 10*time.Minute)); len(again) != 0 {
		t.Errorf("repeated poll at the same time: %v", again)
	}
}

func TestFleet_NormalNeverDecreases(t *testing.T) {
	f := NewFleet(testGatewayKey, 4, t0, nil)
	got := byDevice(f.Poll(t0.Add(30 * day)))
	for _, d := range f.devices[:2] {
		rs := got[d.Serial]
		if len(rs) != 30*24 {
			t.Errorf("%s: %d readings over 30 days, want %d", d.Serial, len(rs), 30*24)
		}
		for i := 1; i < len(rs); i++ {
			if rs[i].Value < rs[i-1].Value {
				t.Fatalf("%s decreased at %v: %s -> %s", d.Serial, rs[i].MeasuredAt, rs[i-1].Value, rs[i].Value)
			}
		}
		if rs[0].Model != d.model {
			t.Errorf("model %q, want %q", rs[0].Model, d.model)
		}
	}
}

func TestFleet_RollbackOnce(t *testing.T) {
	f := NewFleet(testGatewayKey, 3, t0, nil)
	rb := f.devices[1]
	rs := byDevice(f.Poll(t0.Add(10 * day)))[rb.Serial]
	drops := 0
	for i := 1; i < len(rs); i++ {
		if rs[i].Value < rs[i-1].Value {
			drops++
			if rs[i].MeasuredAt.Before(t0.Add(SpecialAfter)) {
				t.Errorf("dropped at %v, before %v", rs[i].MeasuredAt, t0.Add(SpecialAfter))
			}
			if lost := rs[i-1].Value - rs[i].Value; lost < rs[i-1].Value*4/100 {
				t.Errorf("dropped by %s from %s, want about 5 %%", lost, rs[i-1].Value)
			}
		}
	}
	if drops != 1 || !rb.RolledBack {
		t.Errorf("rollback happened %d times, flag %v; want once", drops, rb.RolledBack)
	}
}

func TestFleet_SilentStops(t *testing.T) {
	f := NewFleet(testGatewayKey, 3, t0, nil)
	silent := f.devices[2]
	rs := byDevice(f.Poll(t0.Add(10 * day)))[silent.Serial]
	last := rs[len(rs)-1].MeasuredAt
	if !last.Before(t0.Add(SpecialAfter)) || last.Before(t0.Add(SpecialAfter-time.Hour)) {
		t.Errorf("last reading at %v, want the last hour before %v", last, t0.Add(SpecialAfter))
	}
	if more := byDevice(f.Poll(t0.Add(20 * day)))[silent.Serial]; len(more) != 0 {
		t.Errorf("silent device spoke again: %v", more)
	}
}

// A restored fleet continues from the saved states: same values, no repeated
// hours, no decrease; the saved state survives JSON.
func TestFleet_Restore(t *testing.T) {
	f := NewFleet(testGatewayKey, 4, t0, nil)
	before := byDevice(f.Poll(t0.Add(day)))

	b, err := json.Marshal(f.States())
	if err != nil {
		t.Fatal(err)
	}
	var saved []DeviceState
	if err := json.Unmarshal(b, &saved); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(saved, f.States()) {
		t.Fatalf("states change through JSON:\n%+v\n%+v", saved, f.States())
	}

	restored := NewFleet(testGatewayKey, 4, t0.Add(day), saved) // start would be later, but saved wins
	after := byDevice(restored.Poll(t0.Add(2 * day)))
	for serial, rs := range before {
		last := rs[len(rs)-1]
		next := after[serial][0]
		if !next.MeasuredAt.Equal(last.MeasuredAt.Add(time.Hour)) || next.Value < last.Value {
			t.Errorf("%s: last %v %s, next after restore %v %s", serial, last.MeasuredAt, last.Value, next.MeasuredAt, next.Value)
		}
	}

	// More devices than saved: the new ones start fresh, the old continue.
	grown := NewFleet(testGatewayKey, 6, t0.Add(day), saved)
	states := grown.States()
	if states[0] != saved[0] || !states[5].Since.Equal(t0.Add(day)) || !states[5].Last.IsZero() {
		t.Errorf("grown fleet: %+v", states)
	}
}

func TestFleet_ValueIsCapped(t *testing.T) {
	f := NewFleet(testGatewayKey, 1, t0, []DeviceState{{
		Serial: NewFleet(testGatewayKey, 1, t0, nil).States()[0].Serial,
		Value:  domain.MaxValue - 1, Since: t0,
	}})
	for _, r := range f.Poll(t0.Add(3 * time.Hour)) {
		if r.Value > domain.MaxValue {
			t.Fatalf("value %s above the maximum", r.Value)
		}
	}
}

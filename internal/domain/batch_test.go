package domain

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"
)

var now = time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)

func ptr[T any](v T) *T { return &v }

func item(serial string, readings ...ReadingRequest) ItemRequest {
	return ItemRequest{DeviceSerial: serial, Model: "WaterMeter-X1", Readings: readings}
}

func rr(t time.Time, v string) ReadingRequest { return ReadingRequest{MeasuredAt: t, Value: v} }

func TestValidateBatch_Structure(t *testing.T) {
	ok := item("WM-1", rr(now, "1.000"))
	many := make([]ReadingRequest, MaxBatchReadings)
	for i := range many {
		many[i] = rr(now.Add(-time.Duration(i)*time.Minute), "1")
	}
	tests := []struct {
		name string
		req  BatchRequest
		ok   bool
	}{
		{"valid", BatchRequest{"gw-1-000001", []ItemRequest{ok}}, true},
		{"5000 readings in two items", BatchRequest{"b", []ItemRequest{item("A", many[:2500]...), item("B", many[2500:]...)}}, true},
		{"5001 readings in two items", BatchRequest{"b", []ItemRequest{item("A", many...), item("B", rr(now, "1"))}}, false},
		{"status only", BatchRequest{"b", []ItemRequest{{DeviceSerial: "A", Model: "M", DeviceStatus: &DeviceState{}}}}, true},
		{"neither readings nor status", BatchRequest{"b", []ItemRequest{{DeviceSerial: "A", Model: "M"}}}, false},
		{"empty items", BatchRequest{"b", nil}, false},
		{"empty batch_id", BatchRequest{"", []ItemRequest{ok}}, false},
		{"batch_id with space", BatchRequest{"gw 1", []ItemRequest{ok}}, false},
		{"batch_id 64", BatchRequest{strings.Repeat("b", 64), []ItemRequest{ok}}, true},
		{"batch_id 65", BatchRequest{strings.Repeat("b", 65), []ItemRequest{ok}}, false},
		{"empty serial", BatchRequest{"b", []ItemRequest{item("", rr(now, "1"))}}, false},
		{"serial 65", BatchRequest{"b", []ItemRequest{item(strings.Repeat("s", 65), rr(now, "1"))}}, false},
		{"serial not ASCII", BatchRequest{"b", []ItemRequest{item("WМ-1", rr(now, "1"))}}, false}, // Cyrillic М
		{"empty model", BatchRequest{"b", []ItemRequest{{DeviceSerial: "A", Model: "  ", Readings: ok.Readings}}}, false},
		{"model 64 runes", BatchRequest{"b", []ItemRequest{{DeviceSerial: "A", Model: strings.Repeat("ж", 64), Readings: ok.Readings}}}, true},
		{"model 65 runes", BatchRequest{"b", []ItemRequest{{DeviceSerial: "A", Model: strings.Repeat("ж", 65), Readings: ok.Readings}}}, false},
		{"model with control char", BatchRequest{"b", []ItemRequest{{DeviceSerial: "A", Model: "X\n1", Readings: ok.Readings}}}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, err := ValidateBatch(tt.req, now)
			if tt.ok != (err == nil) {
				t.Fatalf("err = %v, want ok=%v", err, tt.ok)
			}
			if err != nil && !errors.Is(err, ErrInvalid) {
				t.Errorf("error %v is not ErrInvalid", err)
			}
		})
	}
}

func TestValidateBatch_ReadingChecks(t *testing.T) {
	sec := time.Second
	readings := []ReadingRequest{
		rr(now.Add(FutureTolerance), "1"),           // 0: exactly +5m passes
		rr(now.Add(FutureTolerance+sec), "1"),       // 1: future_ts
		rr(now.Add(-MaxReadingAge), "1"),            // 2: exactly 90 days passes
		rr(now.Add(-MaxReadingAge-sec), "1"),        // 3: too_old
		rr(now.Add(-time.Hour), "1.2345"),           // 4: invalid_value
		rr(now.Add(-2*time.Hour), "-1"),             // 5: invalid_value
		rr(time.Time{}, "1"),                        // 6: missing measured_at is too_old
		rr(now.Add(FutureTolerance+sec), "garbage"), // 7: time is checked first
	}
	b, rej, err := ValidateBatch(BatchRequest{"b", []ItemRequest{item("WM-1", readings...)}}, now)
	if err != nil {
		t.Fatal(err)
	}
	wantRej := []Rejected{
		{"WM-1", 1, ReasonFutureTS},
		{"WM-1", 3, ReasonTooOld},
		{"WM-1", 4, ReasonInvalidValue},
		{"WM-1", 5, ReasonInvalidValue},
		{"WM-1", 6, ReasonTooOld},
		{"WM-1", 7, ReasonFutureTS},
	}
	if !slices.Equal(rej, wantRej) {
		t.Errorf("rejected = %v\nwant       %v", rej, wantRej)
	}
	// Kept readings are sorted by time and keep their original index.
	var idx []int
	for _, r := range b.Items[0].Readings {
		idx = append(idx, r.Index)
	}
	if !slices.Equal(idx, []int{2, 0}) {
		t.Errorf("kept indexes in time order = %v, want [2 0]", idx)
	}
}

func TestValidateBatch_DeviceState(t *testing.T) {
	tests := []struct {
		name    string
		in      DeviceState
		want    *DeviceState
		invalid bool
	}{
		{"all valid", DeviceState{ptr(92), ptr(-112), &[]string{"E1"}}, &DeviceState{ptr(92), ptr(-112), &[]string{"E1"}}, false},
		{"bounds", DeviceState{ptr(0), ptr(-150), nil}, &DeviceState{ptr(0), ptr(-150), nil}, false},
		{"bounds high", DeviceState{ptr(100), ptr(0), nil}, &DeviceState{ptr(100), ptr(0), nil}, false},
		{"empty object", DeviceState{}, nil, false},
		{"empty error_codes means no errors", DeviceState{ErrorCodes: &[]string{}}, &DeviceState{ErrorCodes: &[]string{}}, false},
		{"battery 101 dropped, rest kept", DeviceState{ptr(101), ptr(-90), nil}, &DeviceState{RSSI: ptr(-90)}, true},
		{"battery -1", DeviceState{BatteryPct: ptr(-1)}, nil, true},
		{"rssi 1", DeviceState{RSSI: ptr(1)}, nil, true},
		{"rssi -151", DeviceState{BatteryPct: ptr(50), RSSI: ptr(-151)}, &DeviceState{BatteryPct: ptr(50)}, true},
		{"16 codes", DeviceState{ErrorCodes: ptr(slices.Repeat([]string{"E"}, 16))}, &DeviceState{ErrorCodes: ptr(slices.Repeat([]string{"E"}, 16))}, false},
		{"17 codes", DeviceState{ErrorCodes: ptr(slices.Repeat([]string{"E"}, 17))}, nil, true},
		{"empty code", DeviceState{ErrorCodes: &[]string{""}}, nil, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			it := item("WM-1", rr(now, "1"))
			it.DeviceStatus = &tt.in
			b, rej, err := ValidateBatch(BatchRequest{"b", []ItemRequest{it}}, now)
			if err != nil {
				t.Fatal(err)
			}
			if got := b.Items[0].State; !sameState(got, tt.want) {
				t.Errorf("state = %s, want %s", fmtState(got), fmtState(tt.want))
			}
			wantRej := []Rejected(nil)
			if tt.invalid {
				wantRej = []Rejected{{"WM-1", StatusIndex, ReasonInvalidStatus}}
			}
			if !slices.Equal(rej, wantRej) {
				t.Errorf("rejected = %v, want %v", rej, wantRej)
			}
			if len(b.Items[0].Readings) != 1 {
				t.Error("readings must be accepted even with a bad device_status")
			}
		})
	}
}

func TestValidateBatch_NilVsEmptyErrorCodes(t *testing.T) {
	// JSON "error_codes": [] decodes to a pointer to an empty non-nil slice;
	// a pointer to a nil slice must also stay "empty", not "not sent".
	var nilSlice []string
	it := item("A", rr(now, "1"))
	it.DeviceStatus = &DeviceState{ErrorCodes: &nilSlice}
	b, _, err := ValidateBatch(BatchRequest{"b", []ItemRequest{it}}, now)
	if err != nil {
		t.Fatal(err)
	}
	if s := b.Items[0].State; s == nil || s.ErrorCodes == nil || *s.ErrorCodes == nil || len(*s.ErrorCodes) != 0 {
		t.Errorf("state = %s, want error_codes []", fmtState(s))
	}
}

func TestValidateBatch_MergeSameSerial(t *testing.T) {
	first := item("A", rr(now.Add(-time.Hour), "2"), rr(now.Add(-3*time.Hour), "1"))
	first.DeviceStatus = &DeviceState{BatteryPct: ptr(90), RSSI: ptr(-100)}
	second := ItemRequest{DeviceSerial: "A", Model: "Other", DeviceStatus: &DeviceState{BatteryPct: ptr(80)},
		Readings: []ReadingRequest{rr(now.Add(-2*time.Hour), "x"), rr(now.Add(-2*time.Hour), "1.5")}}
	b, rej, err := ValidateBatch(BatchRequest{"b", []ItemRequest{first, item("B", rr(now, "1")), second}}, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(b.Items) != 2 || b.Items[0].Serial != "A" || b.Items[1].Serial != "B" {
		t.Fatalf("items = %+v, want A then B", b.Items)
	}
	a := b.Items[0]
	if a.Model != "WaterMeter-X1" {
		t.Errorf("model = %q, the first item's model wins", a.Model)
	}
	if !sameState(a.State, &DeviceState{BatteryPct: ptr(80), RSSI: ptr(-100)}) {
		t.Errorf("state = %s, want battery 80 from the later item and rssi from the first", fmtState(a.State))
	}
	var got []string
	for _, r := range a.Readings {
		got = append(got, r.Value.String())
	}
	if !slices.Equal(got, []string{"1.000", "1.500", "2.000"}) {
		t.Errorf("merged readings in time order = %v", got)
	}
	if want := []Rejected{{"A", 0, ReasonInvalidValue}}; !slices.Equal(rej, want) {
		t.Errorf("rejected = %v, want %v (index within its own item)", rej, want)
	}
}

func sameState(a, b *DeviceState) bool {
	if a == nil || b == nil {
		return a == b
	}
	eqInt := func(x, y *int) bool { return (x == nil && y == nil) || (x != nil && y != nil && *x == *y) }
	eqCodes := func(x, y *[]string) bool {
		return (x == nil && y == nil) || (x != nil && y != nil && slices.Equal(*x, *y))
	}
	return eqInt(a.BatteryPct, b.BatteryPct) && eqInt(a.RSSI, b.RSSI) && eqCodes(a.ErrorCodes, b.ErrorCodes)
}

func fmtState(s *DeviceState) string {
	if s == nil {
		return "nil"
	}
	f := func(p *int) any {
		if p == nil {
			return "-"
		}
		return *p
	}
	codes := any("-")
	if s.ErrorCodes != nil {
		codes = *s.ErrorCodes
	}
	return fmt.Sprintf("{battery=%v rssi=%v codes=%v}", f(s.BatteryPct), f(s.RSSI), codes)
}

func TestValidateBatch_TimeIsTruncatedToMicroseconds(t *testing.T) {
	local := time.FixedZone("UTC+5", 5*3600)
	at := time.Date(2026, 11, 21, 15, 0, 0, 123456789, local)
	b, _, err := ValidateBatch(BatchRequest{"b", []ItemRequest{item("A", rr(at, "1"))}}, now)
	if err != nil {
		t.Fatal(err)
	}
	got := b.Items[0].Readings[0].MeasuredAt
	want := time.Date(2026, 11, 21, 10, 0, 0, 123456000, time.UTC)
	if !got.Equal(want) || got.Location() != time.UTC {
		t.Errorf("measured_at = %v, want %v in UTC", got, want)
	}
}

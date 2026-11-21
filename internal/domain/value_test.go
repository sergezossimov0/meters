package domain

import (
	"encoding/json"
	"errors"
	"testing"
)

func TestParseValue(t *testing.T) {
	tests := []struct {
		in   string
		want Value
		ok   bool
	}{
		{"0", 0, true},
		{"123", 123000, true},
		{"123.4", 123400, true},
		{"123.45", 123450, true},
		{"123.456", 123456, true},
		{"0.001", 1, true},
		{"007.5", 7500, true},
		{"999999999999.999", MaxValue, true},
		{"999999999999", 999999999999000, true},

		{"1000000000000", 0, false}, // 13 integer digits
		{"1.2345", 0, false},        // 4 fraction digits: never rounded
		{"-1", 0, false},
		{"-0", 0, false},
		{"+1", 0, false},
		{"1e3", 0, false},
		{".5", 0, false},
		{"1.", 0, false},
		{"", 0, false},
		{".", 0, false},
		{" 1", 0, false},
		{"1 ", 0, false},
		{"1,5", 0, false},
		{"1.2.3", 0, false},
		{"0x10", 0, false},
		{"١٢", 0, false}, // Arabic-Indic digits
		{"NaN", 0, false},
	}
	for _, tt := range tests {
		got, err := ParseValue(tt.in)
		if tt.ok != (err == nil) || got != tt.want {
			t.Errorf("ParseValue(%q) = %d, %v; want %d, ok=%v", tt.in, got, err, tt.want, tt.ok)
		}
		if err != nil && !errors.Is(err, ErrInvalid) {
			t.Errorf("ParseValue(%q) error %v is not ErrInvalid", tt.in, err)
		}
	}
}

func TestValueString(t *testing.T) {
	for v, want := range map[Value]string{
		0: "0.000", 1: "0.001", 123400: "123.400", MaxValue: "999999999999.999", -1500: "-1.500",
	} {
		if got := v.String(); got != want {
			t.Errorf("Value(%d).String() = %q, want %q", int64(v), got, want)
		}
	}
	// String and ParseValue round-trip.
	for _, s := range []string{"0.000", "42.042", "999999999999.999"} {
		v, err := ParseValue(s)
		if err != nil || v.String() != s {
			t.Errorf("round trip %q: got %q, %v", s, v.String(), err)
		}
	}
	b, err := json.Marshal(struct{ V Value }{123456})
	if err != nil || string(b) != `{"V":"123.456"}` {
		t.Errorf("JSON = %s, %v; want the value as a string", b, err)
	}
}

func TestValueJSONRoundTrip(t *testing.T) {
	type wrap struct{ V Value }
	for _, v := range []Value{0, 1, 123456, MaxValue} {
		b, err := json.Marshal(wrap{v})
		if err != nil {
			t.Fatal(err)
		}
		var got wrap
		if err := json.Unmarshal(b, &got); err != nil || got.V != v {
			t.Errorf("round trip of %d via %s: %d, %v", int64(v), b, int64(got.V), err)
		}
	}
	for _, bad := range []string{`{"V":"-1.000"}`, `{"V":"1.2345"}`, `{"V":12}`} {
		var got wrap
		if err := json.Unmarshal([]byte(bad), &got); err == nil {
			t.Errorf("%s must not decode", bad)
		}
	}
}

package config

import (
	"errors"
	"strconv"
	"time"
)

// flag.Value implementations bound to config fields. One Set is shared by the
// command line and the environment, so both are validated the same way.
// String must tolerate a nil pointer: flag.PrintDefaults calls it on a zero value.

type stringValue struct{ p *string }

func (v stringValue) String() string {
	if v.p == nil {
		return ""
	}
	return *v.p
}

func (v stringValue) Set(s string) error { *v.p = s; return nil }

// secretValue never shows the current value, not even the default in usage.
type secretValue struct{ p *Secret }

func (v secretValue) String() string     { return "" }
func (v secretValue) Set(s string) error { *v.p = Secret(s); return nil }

type intValue struct{ p *int }

func (v intValue) String() string {
	if v.p == nil {
		return "0"
	}
	return strconv.Itoa(*v.p)
}

func (v intValue) Set(s string) error {
	n, err := strconv.Atoi(s)
	if err != nil {
		return errors.New("must be an integer")
	}
	*v.p = n
	return nil
}

type int64Value struct{ p *int64 }

func (v int64Value) String() string {
	if v.p == nil {
		return "0"
	}
	return strconv.FormatInt(*v.p, 10)
}

func (v int64Value) Set(s string) error {
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return errors.New("must be an integer")
	}
	*v.p = n
	return nil
}

type boolValue struct{ p *bool }

func (v boolValue) IsBoolFlag() bool { return true } // allows bare -restore

func (v boolValue) String() string {
	if v.p == nil {
		return "false"
	}
	return strconv.FormatBool(*v.p)
}

func (v boolValue) Set(s string) error {
	b, err := strconv.ParseBool(s)
	if err != nil {
		return errors.New("must be a boolean (true/false)")
	}
	*v.p = b
	return nil
}

// durationValue uses time.Duration syntax: 24h, 720h, 90m.
type durationValue struct{ p *time.Duration }

func (v durationValue) String() string {
	if v.p == nil {
		return "0s"
	}
	return v.p.String()
}

func (v durationValue) Set(s string) error {
	d, err := time.ParseDuration(s)
	if err != nil {
		return errors.New("must be a duration like 24h or 90m")
	}
	*v.p = d
	return nil
}

// secondsValue is an interval in whole seconds, as in the course: -r 10.
// Negative values are rejected here; lower bounds (> 0) are checked in validation.
type secondsValue struct{ p *time.Duration }

func (v secondsValue) String() string {
	if v.p == nil {
		return "0"
	}
	return strconv.FormatInt(int64(*v.p/time.Second), 10)
}

func (v secondsValue) Set(s string) error {
	sec, err := strconv.ParseInt(s, 10, 32)
	if err != nil {
		return errors.New("must be a whole number of seconds")
	}
	if sec < 0 {
		return errors.New("must not be negative")
	}
	*v.p = time.Duration(sec) * time.Second
	return nil
}

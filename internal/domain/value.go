package domain

import (
	"fmt"
	"strconv"
)

// Value is a meter reading in thousandths of the unit: "123.456" is 123456.
// It fits numeric(15,3) exactly: at most 12 integer and 3 fraction digits.
// Integer thousandths keep readings exact without float or a decimal library.
type Value int64

// Value limits.
const (
	valueIntDigits  = 12
	valueFracDigits = 3
	// MaxValue is 999999999999.999, the largest numeric(15,3).
	MaxValue Value = 999_999_999_999_999
)

// ParseValue parses a non-negative decimal "123", "123.4" or "123.456".
// Anything else — a sign, an exponent, spaces, "1.", ".5", more than 3
// fraction or 12 integer digits — is an error: the value is never rounded.
func ParseValue(s string) (Value, error) {
	intPart, fracPart := s, ""
	for i := 0; i < len(s); i++ {
		if s[i] == '.' {
			intPart, fracPart = s[:i], s[i+1:]
			if fracPart == "" {
				return 0, Invalidf("value %q: no digits after the point", s)
			}
			break
		}
	}
	if intPart == "" || len(intPart) > valueIntDigits || !allDigits(intPart) {
		return 0, Invalidf("value %q: want 1 to %d digits before the point", s, valueIntDigits)
	}
	if len(fracPart) > valueFracDigits || !allDigits(fracPart) {
		return 0, Invalidf("value %q: want at most %d digits after the point", s, valueFracDigits)
	}
	for len(fracPart) < valueFracDigits {
		fracPart += "0"
	}
	// At most 15 digits: always fits int64, the errors cannot happen.
	n, _ := strconv.ParseInt(intPart, 10, 64)
	f, _ := strconv.ParseInt(fracPart, 10, 64)
	return Value(n*1000 + f), nil
}

func allDigits(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// String formats the value with exactly three fraction digits: "123.400".
// The same text is passed to PostgreSQL as numeric.
func (v Value) String() string {
	sign := ""
	if v < 0 { // not produced by ParseValue; differences may be negative
		sign, v = "-", -v
	}
	return fmt.Sprintf("%s%d.%03d", sign, v/1000, v%1000)
}

// MarshalText makes Value a JSON string, as the API requires (spec п. 6).
func (v Value) MarshalText() ([]byte, error) { return []byte(v.String()), nil }

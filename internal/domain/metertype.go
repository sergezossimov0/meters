package domain

import (
	"regexp"
	"strings"
	"unicode/utf8"
)

// MeterType is a kind of resource: water, gas, electricity, heat.
type MeterType struct {
	ID       int
	Code     string
	Name     string
	Subtypes []MeterSubtype // filled by listings, ordered by code
}

// MeterSubtype is what a device actually measures (cold_water, active_energy)
// and in which unit. Subtype codes are unique across all types.
type MeterSubtype struct {
	ID     int
	TypeID int
	Code   string
	Name   string
	Unit   string
}

// Reference data limits.
const (
	NameMaxLen = 100
	UnitMaxLen = 16
)

var codePattern = regexp.MustCompile(`^[a-z][a-z0-9_]{1,31}$`)

// ValidateCode checks a type or subtype code: 2–32 characters, a lower-case
// letter first, then letters, digits and '_'.
func ValidateCode(code string) error {
	if !codePattern.MatchString(code) {
		return Invalidf("code must be 2 to 32 characters: a-z first, then a-z, 0-9 or _")
	}
	return nil
}

// NormalizeName trims the name and checks it is 1–100 characters.
func NormalizeName(name string) (string, error) {
	n := strings.TrimSpace(name)
	if n == "" || utf8.RuneCountInString(n) > NameMaxLen {
		return "", Invalidf("name must be 1 to %d characters", NameMaxLen)
	}
	return n, nil
}

// NormalizeUnit trims the unit and checks it is 1–16 characters.
func NormalizeUnit(unit string) (string, error) {
	u := strings.TrimSpace(unit)
	if u == "" || utf8.RuneCountInString(u) > UnitMaxLen {
		return "", Invalidf("unit must be 1 to %d characters", UnitMaxLen)
	}
	return u, nil
}

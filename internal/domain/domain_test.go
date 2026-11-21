package domain

import (
	"errors"
	"strings"
	"testing"
)

func TestNormalizeLogin(t *testing.T) {
	tests := []struct {
		in, want string
		ok       bool
	}{
		{"ivan", "ivan", true},
		{"Ivan.Petrov_2", "ivan.petrov_2", true},
		{"a-b", "a-b", true},
		{strings.Repeat("x", 64), strings.Repeat("x", 64), true},
		{"ab", "", false},
		{strings.Repeat("x", 65), "", false},
		{"", "", false},
		{"ivan petrov", "", false},
		{"иван", "", false}, // no look-alike Unicode logins
		{"ivan@mail", "", false},
		{" ivan", "", false},
	}
	for _, tt := range tests {
		got, err := NormalizeLogin(tt.in)
		if tt.ok != (err == nil) || got != tt.want {
			t.Errorf("NormalizeLogin(%q) = %q, %v; want %q, ok=%v", tt.in, got, err, tt.want, tt.ok)
		}
		if err != nil && !errors.Is(err, ErrInvalid) {
			t.Errorf("NormalizeLogin(%q) error %v is not ErrInvalid", tt.in, err)
		}
	}
}

func TestValidatePassword(t *testing.T) {
	tests := []struct {
		in string
		ok bool
	}{
		{strings.Repeat("p", 8), true},
		{strings.Repeat("p", 72), true},
		{strings.Repeat("p", 7), false},
		{strings.Repeat("p", 73), false},
		{strings.Repeat("я", 36), true},  // 72 bytes
		{strings.Repeat("я", 37), false}, // 74 bytes: the limit is in bytes
		{"", false},
	}
	for _, tt := range tests {
		if err := ValidatePassword(tt.in); tt.ok != (err == nil) {
			t.Errorf("ValidatePassword(%d bytes) = %v, want ok=%v", len(tt.in), err, tt.ok)
		}
	}
}

func TestValidateCode(t *testing.T) {
	for code, ok := range map[string]bool{
		"water": true, "cold_water": true, "m3": true, "a" + strings.Repeat("b", 31): true,
		"a": false, "a" + strings.Repeat("b", 32): false, "Water": false, "1water": false,
		"_water": false, "cold-water": false, "": false,
	} {
		if err := ValidateCode(code); ok != (err == nil) {
			t.Errorf("ValidateCode(%q) = %v, want ok=%v", code, err, ok)
		}
	}
}

func TestNormalizeNameAndUnit(t *testing.T) {
	if got, err := NormalizeName("  Холодная вода "); err != nil || got != "Холодная вода" {
		t.Errorf("NormalizeName = %q, %v", got, err)
	}
	if _, err := NormalizeName("   "); err == nil {
		t.Error("blank name accepted")
	}
	if _, err := NormalizeName(strings.Repeat("ы", NameMaxLen)); err != nil {
		t.Errorf("100-rune name rejected: %v", err)
	}
	if _, err := NormalizeName(strings.Repeat("ы", NameMaxLen+1)); err == nil {
		t.Error("101-rune name accepted")
	}
	if got, err := NormalizeUnit(" m3 "); err != nil || got != "m3" {
		t.Errorf("NormalizeUnit = %q, %v", got, err)
	}
	if _, err := NormalizeUnit(""); err == nil {
		t.Error("empty unit accepted")
	}
	if _, err := NormalizeUnit(strings.Repeat("u", UnitMaxLen+1)); err == nil {
		t.Error("17-character unit accepted")
	}
}

func TestRole(t *testing.T) {
	if !RoleAdmin.Valid() || !RoleCustomer.Valid() || Role("root").Valid() || Role("").Valid() {
		t.Error("Role.Valid is wrong")
	}
}

func TestInvalidError(t *testing.T) {
	err := Invalidf("login must be %d characters", 3)
	if err.Error() != "login must be 3 characters" || !errors.Is(err, ErrInvalid) {
		t.Errorf("Invalidf = %q, Is(ErrInvalid) = %v", err, errors.Is(err, ErrInvalid))
	}
}

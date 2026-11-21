package auth

import (
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"
)

func newTestPasswords(t *testing.T) *Passwords {
	t.Helper()
	p, err := NewPasswords(bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestPasswords(t *testing.T) {
	p := newTestPasswords(t)
	const pw = "correct horse battery"

	hash, err := p.Hash(pw)
	if err != nil {
		t.Fatal(err)
	}
	if hash == pw || !strings.HasPrefix(hash, "$2a$") {
		t.Errorf("hash %q does not look like bcrypt", hash)
	}
	if !p.Check(hash, pw) {
		t.Error("correct password rejected")
	}
	if p.Check(hash, pw+"!") {
		t.Error("wrong password accepted")
	}
	if p.Check("not-a-bcrypt-hash", pw) {
		t.Error("malformed hash accepted")
	}

	again, err := p.Hash(pw)
	if err != nil || again == hash {
		t.Errorf("same password hashed twice to the same value: salts must differ (err %v)", err)
	}
}

func TestPasswords_TooLong(t *testing.T) {
	if _, err := newTestPasswords(t).Hash(strings.Repeat("p", 73)); err == nil {
		t.Error("73-byte password hashed: bcrypt would ignore the tail")
	}
}

func TestPasswords_CheckMissing(t *testing.T) {
	p := newTestPasswords(t)
	p.CheckMissing("anything") // builds the dummy hash once
	p.CheckMissing("anything else")
}

func TestNewPasswords_BadCost(t *testing.T) {
	for _, cost := range []int{bcrypt.MinCost - 1, bcrypt.MaxCost + 1} {
		if _, err := NewPasswords(cost); err == nil {
			t.Errorf("cost %d accepted", cost)
		}
	}
}

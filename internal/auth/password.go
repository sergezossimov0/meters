// Package auth hashes passwords (bcrypt) and issues and checks access
// tokens (JWT, HS256). It knows nothing about HTTP or storage.
package auth

import (
	"fmt"
	"sync"

	"golang.org/x/crypto/bcrypt"
)

// Passwords hashes and checks passwords with bcrypt at a fixed cost.
type Passwords struct {
	cost  int
	dummy func() []byte // hash of a random string, built once on first use
}

// NewPasswords returns a hasher with the given bcrypt cost: use
// bcrypt.DefaultCost in production and bcrypt.MinCost in tests.
func NewPasswords(cost int) (*Passwords, error) {
	if cost < bcrypt.MinCost || cost > bcrypt.MaxCost {
		return nil, fmt.Errorf("bcrypt cost %d out of range %d..%d", cost, bcrypt.MinCost, bcrypt.MaxCost)
	}
	p := &Passwords{cost: cost}
	p.dummy = sync.OnceValue(func() []byte {
		h, err := bcrypt.GenerateFromPassword([]byte("no such user: timing equalizer"), cost)
		if err != nil {
			panic(fmt.Sprintf("bcrypt dummy hash: %v", err)) // only on an invalid cost, checked above
		}
		return h
	})
	return p, nil
}

// Hash returns the bcrypt hash of password. Passwords over 72 bytes are an
// error: bcrypt would silently ignore the tail.
func (p *Passwords) Hash(password string) (string, error) {
	h, err := bcrypt.GenerateFromPassword([]byte(password), p.cost)
	if err != nil {
		return "", fmt.Errorf("hash password: %w", err)
	}
	return string(h), nil
}

// Check reports whether password matches hash. A malformed hash is a
// mismatch, not an error: the caller answers 401 either way.
func (p *Passwords) Check(hash, password string) bool {
	err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(password))
	return err == nil
}

// CheckMissing spends the same time as Check for a login that does not
// exist, so response time does not reveal which logins are registered.
func (p *Passwords) CheckMissing(password string) {
	_ = bcrypt.CompareHashAndPassword(p.dummy(), []byte(password)) // always a mismatch; only the time matters
}

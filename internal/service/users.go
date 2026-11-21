// Package service implements the use cases on top of the domain and the
// repositories: registration, login, and (later) devices, readings, bills.
package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/sergezossimov0/meters/internal/auth"
	"github.com/sergezossimov0/meters/internal/domain"
	"github.com/sergezossimov0/meters/internal/storage"
)

// ErrBadCredentials is the single answer to a failed login: unknown login,
// wrong password and malformed login look the same to the client.
var ErrBadCredentials = errors.New("invalid login or password")

// Users registers and authenticates users.
type Users struct {
	repo      storage.UserRepository
	passwords *auth.Passwords
	tokens    *auth.Tokens
}

// NewUsers wires the service.
func NewUsers(repo storage.UserRepository, passwords *auth.Passwords, tokens *auth.Tokens) *Users {
	return &Users{repo: repo, passwords: passwords, tokens: tokens}
}

// Register creates a customer and returns an access token. Errors:
// domain.ErrInvalid (with the reason) for a bad login or password,
// domain.ErrConflict if the login is taken.
func (s *Users) Register(ctx context.Context, login, password string) (string, error) {
	l, err := domain.NormalizeLogin(login)
	if err != nil {
		return "", err
	}
	if err := domain.ValidatePassword(password); err != nil {
		return "", err
	}
	hash, err := s.passwords.Hash(password)
	if err != nil {
		return "", err
	}
	u, err := s.repo.Create(ctx, domain.User{Login: l, PasswordHash: hash, Role: domain.RoleCustomer})
	if err != nil {
		return "", fmt.Errorf("register: %w", err)
	}
	return s.tokens.Issue(u.ID, u.Role)
}

// Login checks the pair and returns an access token, or ErrBadCredentials.
// Every rejected attempt spends one bcrypt comparison, so response time does
// not tell whether the login exists.
func (s *Users) Login(ctx context.Context, login, password string) (string, error) {
	l, err := domain.NormalizeLogin(login)
	if err != nil {
		s.passwords.CheckMissing(password)
		return "", ErrBadCredentials
	}
	u, err := s.repo.ByLogin(ctx, l)
	switch {
	case errors.Is(err, domain.ErrNotFound):
		s.passwords.CheckMissing(password)
		return "", ErrBadCredentials
	case err != nil:
		return "", fmt.Errorf("login: %w", err)
	}
	if !s.passwords.Check(u.PasswordHash, password) {
		return "", ErrBadCredentials
	}
	return s.tokens.Issue(u.ID, u.Role)
}

// AdminResult says what EnsureAdmin did.
type AdminResult int

// EnsureAdmin outcomes.
const (
	AdminCreated AdminResult = iota + 1
	AdminExists
)

// EnsureAdmin creates the first administrator from ADMIN_LOGIN and
// ADMIN_PASSWORD. It is idempotent:
//   - no such login: an admin is created;
//   - the login is an admin: nothing changes, the password is NOT reset;
//   - the login is a customer: an error, the server must not start
//     (silently promoting a customer to admin would be a hole).
//
// Two servers starting at once are safe: the loser of the INSERT race reads
// the winner's row and applies the same rules.
func (s *Users) EnsureAdmin(ctx context.Context, login, password string) (AdminResult, error) {
	l, err := domain.NormalizeLogin(login)
	if err != nil {
		return 0, fmt.Errorf("ADMIN_LOGIN: %w", err)
	}
	if err := domain.ValidatePassword(password); err != nil {
		return 0, fmt.Errorf("first admin password: %w", err)
	}

	for range 2 { // second pass only after losing a concurrent INSERT
		u, err := s.repo.ByLogin(ctx, l)
		switch {
		case err == nil && u.Role == domain.RoleAdmin:
			return AdminExists, nil
		case err == nil:
			return 0, fmt.Errorf("ADMIN_LOGIN %q is already registered as %s; choose another login", l, u.Role)
		case !errors.Is(err, domain.ErrNotFound):
			return 0, fmt.Errorf("find admin: %w", err)
		}

		hash, err := s.passwords.Hash(password)
		if err != nil {
			return 0, err
		}
		_, err = s.repo.Create(ctx, domain.User{Login: l, PasswordHash: hash, Role: domain.RoleAdmin})
		switch {
		case err == nil:
			return AdminCreated, nil
		case !errors.Is(err, domain.ErrConflict):
			return 0, fmt.Errorf("create admin: %w", err)
		}
	}
	return 0, fmt.Errorf("create admin %q: login keeps changing concurrently", l)
}

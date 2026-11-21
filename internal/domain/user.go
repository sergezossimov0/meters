package domain

import (
	"regexp"
	"strings"
	"time"
)

// Role is a user's role (spec п. 4).
type Role string

// Roles.
const (
	RoleAdmin    Role = "admin"
	RoleCustomer Role = "customer"
)

// Valid reports whether r is a known role.
func (r Role) Valid() bool { return r == RoleAdmin || r == RoleCustomer }

// User is an account. PasswordHash is a bcrypt hash, never the password.
type User struct {
	ID           int64
	Login        string
	PasswordHash string
	Role         Role
	CreatedAt    time.Time
}

// Login and password limits.
const (
	LoginMinLen       = 3
	LoginMaxLen       = 64
	PasswordMinBytes  = 8
	PasswordMaxBytes  = 72 // bcrypt ignores everything after 72 bytes
	loginAllowedChars = "a-z 0-9 . _ -"
)

var loginPattern = regexp.MustCompile(`^[a-z0-9._-]+$`)

// NormalizeLogin lower-cases the login and checks it: 3–64 characters from
// a-z, 0-9, '.', '_' and '-'. "Ivan" and "ivan" are the same user.
func NormalizeLogin(login string) (string, error) {
	l := strings.ToLower(login)
	switch {
	case len(l) < LoginMinLen || len(l) > LoginMaxLen:
		return "", Invalidf("login must be %d to %d characters", LoginMinLen, LoginMaxLen)
	case !loginPattern.MatchString(l):
		return "", Invalidf("login may contain only %s", loginAllowedChars)
	}
	return l, nil
}

// ValidatePassword checks the length in bytes: bcrypt silently truncates
// longer input, so a longer password would not be checked in full.
func ValidatePassword(password string) error {
	if n := len(password); n < PasswordMinBytes || n > PasswordMaxBytes {
		return Invalidf("password must be %d to %d bytes", PasswordMinBytes, PasswordMaxBytes)
	}
	return nil
}

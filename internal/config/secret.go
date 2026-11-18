package config

import (
	"net/url"
	"regexp"
	"strings"
)

const redacted = "[REDACTED]"

// Secret is a sensitive setting (keys, passwords, DSN). It never prints its
// value: fmt, %#v, encoding/json and zap's reflection all see "[REDACTED]".
// Use Value to get the real string.
type Secret string

// Value returns the secret itself.
func (s Secret) Value() string { return string(s) }

// IsSet reports whether the secret is non-empty.
func (s Secret) IsSet() bool { return s != "" }

func (s Secret) String() string {
	if s == "" {
		return ""
	}
	return redacted
}

func (s Secret) GoString() string { return `config.Secret("` + s.String() + `")` }

// MarshalText also covers encoding/json, which prefers TextMarshaler for strings.
func (s Secret) MarshalText() ([]byte, error) { return []byte(s.String()), nil }

// kvPassword matches the password parameter of a key/value DSN, quoted or not.
var kvPassword = regexp.MustCompile(`(?i)(\bpassword\s*=\s*)('(?:[^'\\]|\\.)*'|\S+)`)

// RedactDSN hides the password in a PostgreSQL DSN, both in URL form (user
// info and the password query parameter) and in key/value form, keeping host,
// port and database for the logs.
func RedactDSN(dsn string) string {
	if strings.Contains(dsn, "://") {
		u, err := url.Parse(dsn)
		if err != nil {
			return redacted // unparsable: may contain anything
		}
		if q := u.Query(); q.Has("password") {
			q.Set("password", "xxxxx")
			u.RawQuery = q.Encode()
		}
		return u.Redacted()
	}
	return kvPassword.ReplaceAllString(dsn, "${1}xxxxx")
}

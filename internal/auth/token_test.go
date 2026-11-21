package auth

import (
	"errors"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/sergezossimov0/meters/internal/domain"
)

// Dummy test keys, not real secrets.
var (
	testKey  = []byte(strings.Repeat("k", MinSecretBytes))
	otherKey = []byte(strings.Repeat("o", MinSecretBytes))
)

func newTestTokens(t *testing.T, key []byte) *Tokens {
	t.Helper()
	tok, err := NewTokens(key, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	return tok
}

// sign builds a token by hand, to feed Parse what Issue never produces.
func sign(t *testing.T, method jwt.SigningMethod, key any, claims jwt.Claims) string {
	t.Helper()
	s, err := jwt.NewWithClaims(method, claims).SignedString(key)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func validClaims(sub, role string) tokenClaims {
	now := time.Now()
	return tokenClaims{Role: role, RegisteredClaims: jwt.RegisteredClaims{
		Subject:   sub,
		IssuedAt:  jwt.NewNumericDate(now),
		ExpiresAt: jwt.NewNumericDate(now.Add(time.Hour)),
	}}
}

func TestTokens_RoundTrip(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		tok := newTestTokens(t, testKey)
		s, err := tok.Issue(42, domain.RoleAdmin)
		if err != nil {
			t.Fatal(err)
		}
		c, err := tok.Parse(s)
		if err != nil {
			t.Fatalf("Parse: %v", err)
		}
		if c.UserID != 42 || c.Role != domain.RoleAdmin || !c.ExpiresAt.Equal(time.Now().Add(time.Hour)) {
			t.Errorf("claims = %+v", c)
		}
	})
}

func TestTokens_Expiry(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		tok := newTestTokens(t, testKey)
		s, err := tok.Issue(1, domain.RoleCustomer)
		if err != nil {
			t.Fatal(err)
		}

		time.Sleep(time.Hour - time.Second)
		if _, err := tok.Parse(s); err != nil {
			t.Fatalf("token rejected a second before expiry: %v", err)
		}
		time.Sleep(2 * time.Second)
		if _, err := tok.Parse(s); !errors.Is(err, ErrInvalidToken) || !errors.Is(err, jwt.ErrTokenExpired) {
			t.Errorf("expired token: err = %v, want ErrInvalidToken wrapping ErrTokenExpired", err)
		}
	})
}

func TestTokens_Rejects(t *testing.T) {
	tok := newTestTokens(t, testKey)
	noExp := tokenClaims{Role: "customer", RegisteredClaims: jwt.RegisteredClaims{Subject: "1"}}
	future := validClaims("1", "customer")
	future.IssuedAt = jwt.NewNumericDate(time.Now().Add(time.Hour))

	tests := map[string]string{
		"garbage":          "not.a.token",
		"empty":            "",
		"other key":        sign(t, jwt.SigningMethodHS256, otherKey, validClaims("1", "customer")),
		"alg none":         sign(t, jwt.SigningMethodNone, jwt.UnsafeAllowNoneSignatureType, validClaims("1", "admin")),
		"HS512, same key":  sign(t, jwt.SigningMethodHS512, testKey, validClaims("1", "admin")),
		"no exp":           sign(t, jwt.SigningMethodHS256, testKey, noExp),
		"issued in future": sign(t, jwt.SigningMethodHS256, testKey, future),
		"unknown role":     sign(t, jwt.SigningMethodHS256, testKey, validClaims("1", "root")),
		"empty role":       sign(t, jwt.SigningMethodHS256, testKey, validClaims("1", "")),
		"non-numeric sub":  sign(t, jwt.SigningMethodHS256, testKey, validClaims("ivan", "customer")),
		"zero sub":         sign(t, jwt.SigningMethodHS256, testKey, validClaims("0", "customer")),
		"negative sub":     sign(t, jwt.SigningMethodHS256, testKey, validClaims("-5", "customer")),
	}
	for name, s := range tests {
		t.Run(name, func(t *testing.T) {
			if c, err := tok.Parse(s); !errors.Is(err, ErrInvalidToken) {
				t.Errorf("Parse = %+v, %v; want ErrInvalidToken", c, err)
			}
		})
	}

	// A valid token with one character of the signature changed.
	good, err := tok.Issue(1, domain.RoleCustomer)
	if err != nil {
		t.Fatal(err)
	}
	// The middle of the signature: every bit there is significant (the last
	// base64 character may carry only padding bits).
	i := strings.LastIndexByte(good, '.') + 10
	replacement := byte('A')
	if good[i] == 'A' {
		replacement = 'B'
	}
	tampered := good[:i] + string(replacement) + good[i+1:]
	if _, err := tok.Parse(tampered); !errors.Is(err, ErrInvalidToken) {
		t.Errorf("tampered signature accepted: %v", err)
	}
}

func TestNewTokens_Validation(t *testing.T) {
	if _, err := NewTokens(testKey[:MinSecretBytes-1], time.Hour); err == nil {
		t.Error("31-byte key accepted")
	}
	if _, err := NewTokens(testKey, 0); err == nil {
		t.Error("zero TTL accepted")
	}
}

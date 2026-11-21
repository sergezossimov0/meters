package auth

import (
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/sergezossimov0/meters/internal/domain"
)

// ErrInvalidToken covers every reason to reject a token: bad signature,
// wrong algorithm, expired, malformed claims. Details are in the chain for
// logs; the client only gets 401.
var ErrInvalidToken = errors.New("invalid token")

// MinSecretBytes is the minimum HS256 key length (RFC 7518 §3.2).
const MinSecretBytes = 32

// Claims is what a valid token says about its bearer.
type Claims struct {
	UserID    int64
	Role      domain.Role
	ExpiresAt time.Time
}

// tokenClaims is the JWT payload: sub (user ID), role, iat, exp.
type tokenClaims struct {
	Role string `json:"role"`
	jwt.RegisteredClaims
}

// Tokens issues and checks HS256 tokens.
type Tokens struct {
	secret []byte
	ttl    time.Duration
	parser *jwt.Parser
}

// NewTokens returns a token service with the given key and lifetime.
func NewTokens(secret []byte, ttl time.Duration) (*Tokens, error) {
	if len(secret) < MinSecretBytes {
		return nil, fmt.Errorf("JWT secret must be at least %d bytes", MinSecretBytes)
	}
	if ttl <= 0 {
		return nil, errors.New("JWT lifetime must be positive")
	}
	return &Tokens{
		secret: secret,
		ttl:    ttl,
		parser: jwt.NewParser(
			jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}), // no "none", no algorithm swap
			jwt.WithExpirationRequired(),
			jwt.WithIssuedAt(),
		),
	}, nil
}

// Issue returns a signed token for the user, valid for the configured TTL.
func (t *Tokens) Issue(userID int64, role domain.Role) (string, error) {
	now := time.Now()
	claims := tokenClaims{
		Role: string(role),
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   strconv.FormatInt(userID, 10),
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(t.ttl)),
		},
	}
	signed, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(t.secret)
	if err != nil {
		return "", fmt.Errorf("sign token: %w", err)
	}
	return signed, nil
}

// Parse checks the token and returns its claims, or an error wrapping
// ErrInvalidToken.
func (t *Tokens) Parse(token string) (Claims, error) {
	var c tokenClaims
	if _, err := t.parser.ParseWithClaims(token, &c, func(*jwt.Token) (any, error) { return t.secret, nil }); err != nil {
		return Claims{}, fmt.Errorf("%w: %w", ErrInvalidToken, err)
	}
	id, err := strconv.ParseInt(c.Subject, 10, 64)
	if err != nil || id <= 0 {
		return Claims{}, fmt.Errorf("%w: bad subject %q", ErrInvalidToken, c.Subject)
	}
	role := domain.Role(c.Role)
	if !role.Valid() {
		return Claims{}, fmt.Errorf("%w: unknown role %q", ErrInvalidToken, c.Role)
	}
	return Claims{UserID: id, Role: role, ExpiresAt: c.ExpiresAt.Time}, nil
}

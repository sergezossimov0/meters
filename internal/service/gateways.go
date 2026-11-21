package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"strings"

	"github.com/sergezossimov0/meters/internal/domain"
	"github.com/sergezossimov0/meters/internal/storage"
)

// Gateway API keys: "gwk_" and 32 random bytes in unpadded base64url.
const (
	GatewayKeyPrefix = "gwk_"
	gatewayKeyBytes  = 32
)

var gatewayKeyLen = len(GatewayKeyPrefix) + base64.RawURLEncoding.EncodedLen(gatewayKeyBytes)

// Gateways registers gateways, issues their keys and authenticates them.
type Gateways struct {
	repo storage.GatewayRepository
}

// NewGateways wires the service.
func NewGateways(repo storage.GatewayRepository) *Gateways {
	return &Gateways{repo: repo}
}

// Create registers a gateway and returns it with its API key. The key is
// shown only here: the repository receives its sha256. Errors:
// domain.ErrInvalid for a bad name.
func (s *Gateways) Create(ctx context.Context, name string) (domain.Gateway, string, error) {
	n, err := domain.NormalizeName(name)
	if err != nil {
		return domain.Gateway{}, "", err
	}
	key := newGatewayKey()
	g, err := s.repo.Create(ctx, domain.Gateway{Name: n}, HashGatewayKey(key))
	if err != nil {
		return domain.Gateway{}, "", fmt.Errorf("create gateway: %w", err)
	}
	return g, key, nil
}

// List returns all gateways ordered by ID.
func (s *Gateways) List(ctx context.Context) ([]domain.Gateway, error) {
	list, err := s.repo.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("list gateways: %w", err)
	}
	return list, nil
}

// Block stops the gateway's requests (403); blocking again is not an error.
// An unknown id gives domain.ErrNotFound.
func (s *Gateways) Block(ctx context.Context, id int64) (domain.Gateway, error) {
	return s.setBlocked(ctx, id, true)
}

// Unblock lets the gateway in again; idempotent like Block.
func (s *Gateways) Unblock(ctx context.Context, id int64) (domain.Gateway, error) {
	return s.setBlocked(ctx, id, false)
}

func (s *Gateways) setBlocked(ctx context.Context, id int64, blocked bool) (domain.Gateway, error) {
	g, err := s.repo.SetBlocked(ctx, id, blocked)
	if err != nil {
		return domain.Gateway{}, fmt.Errorf("set gateway %d blocked=%v: %w", id, blocked, err)
	}
	return g, nil
}

// Authenticate finds the gateway by its API key. Errors: domain.ErrNotFound
// for a missing or unknown key (401), domain.ErrGatewayBlocked (403). A key of the
// wrong shape is refused without a database lookup.
func (s *Gateways) Authenticate(ctx context.Context, key string) (domain.Gateway, error) {
	if len(key) != gatewayKeyLen || !strings.HasPrefix(key, GatewayKeyPrefix) {
		return domain.Gateway{}, domain.ErrNotFound
	}
	g, err := s.repo.ByKeyHash(ctx, HashGatewayKey(key))
	if err != nil {
		return domain.Gateway{}, fmt.Errorf("authenticate gateway: %w", err)
	}
	if g.Blocked {
		return domain.Gateway{}, domain.ErrGatewayBlocked
	}
	return g, nil
}

// HashGatewayKey is what the database stores instead of the key. A plain
// sha256 is enough: the key has 256 random bits, nothing to brute-force.
func HashGatewayKey(key string) []byte {
	h := sha256.Sum256([]byte(key))
	return h[:]
}

func newGatewayKey() string {
	b := make([]byte, gatewayKeyBytes)
	_, _ = rand.Read(b) // never fails since Go 1.24
	return GatewayKeyPrefix + base64.RawURLEncoding.EncodeToString(b)
}

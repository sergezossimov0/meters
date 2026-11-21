package service

import (
	"bytes"
	"context"
	"errors"
	"regexp"
	"strings"
	"testing"

	"github.com/sergezossimov0/meters/internal/domain"
	"github.com/sergezossimov0/meters/internal/storage/memory"
)

// spyGateways records what reaches the repository.
type spyGateways struct {
	*memory.Gateways
	hashes [][]byte
}

func (s *spyGateways) Create(ctx context.Context, g domain.Gateway, keyHash []byte) (domain.Gateway, error) {
	s.hashes = append(s.hashes, keyHash)
	return s.Gateways.Create(ctx, g, keyHash)
}

var keyPattern = regexp.MustCompile(`^gwk_[A-Za-z0-9_-]{43}$`)

func TestGateways_Create(t *testing.T) {
	ctx := t.Context()
	repo := &spyGateways{Gateways: memory.NewGateways()}
	s := NewGateways(repo)

	g, key, err := s.Create(ctx, "  Шлюз 17 ")
	if err != nil {
		t.Fatal(err)
	}
	if g.ID <= 0 || g.Name != "Шлюз 17" || g.Blocked {
		t.Errorf("gateway = %+v: want an id, the trimmed name, not blocked", g)
	}
	if !keyPattern.MatchString(key) {
		t.Errorf("key %q does not match %s", key, keyPattern)
	}
	// Only the sha256 of the key reaches the repository.
	if len(repo.hashes) != 1 || !bytes.Equal(repo.hashes[0], HashGatewayKey(key)) ||
		bytes.Contains(repo.hashes[0], []byte(key)) {
		t.Errorf("repository received %x, want sha256 of the key", repo.hashes)
	}

	_, key2, err := s.Create(ctx, "other")
	if err != nil || key2 == key {
		t.Errorf("second key %q, %v: keys must differ", key2, err)
	}

	for _, bad := range []string{"", "   ", strings.Repeat("я", domain.NameMaxLen+1)} {
		if _, _, err := s.Create(ctx, bad); !errors.Is(err, domain.ErrInvalid) {
			t.Errorf("Create(%d runes) error = %v, want ErrInvalid", len([]rune(bad)), err)
		}
	}
	if _, _, err := s.Create(ctx, strings.Repeat("я", domain.NameMaxLen)); err != nil {
		t.Errorf("name of %d runes: %v", domain.NameMaxLen, err)
	}
}

func TestGateways_Authenticate(t *testing.T) {
	ctx := t.Context()
	s := NewGateways(memory.NewGateways())
	g, key, err := s.Create(ctx, "gw")
	if err != nil {
		t.Fatal(err)
	}

	got, err := s.Authenticate(ctx, key)
	if err != nil || got.ID != g.ID {
		t.Fatalf("Authenticate = %+v, %v", got, err)
	}

	other := GatewayKeyPrefix + strings.Repeat("A", len(key)-len(GatewayKeyPrefix))
	for name, bad := range map[string]string{
		"empty": "", "unknown": other, "no prefix": "xxx_" + key[4:], "truncated": key[:len(key)-1], "longer": key + "A",
	} {
		if _, err := s.Authenticate(ctx, bad); !errors.Is(err, domain.ErrNotFound) {
			t.Errorf("%s key: error = %v, want ErrNotFound", name, err)
		}
	}

	if _, err := s.Block(ctx, g.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Authenticate(ctx, key); !errors.Is(err, domain.ErrGatewayBlocked) {
		t.Errorf("blocked gateway: error = %v, want ErrGatewayBlocked", err)
	}
	if _, err := s.Unblock(ctx, g.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Authenticate(ctx, key); err != nil {
		t.Errorf("after unblock: %v", err)
	}
}

func TestGateways_BlockListUnknown(t *testing.T) {
	ctx := t.Context()
	s := NewGateways(memory.NewGateways())
	a, _, _ := s.Create(ctx, "a")
	b, _, _ := s.Create(ctx, "b")

	for range 2 { // repeated block is not an error
		got, err := s.Block(ctx, b.ID)
		if err != nil || !got.Blocked {
			t.Fatalf("Block = %+v, %v", got, err)
		}
	}
	list, err := s.List(ctx)
	if err != nil || len(list) != 2 || list[0].ID != a.ID || list[0].Blocked || !list[1].Blocked {
		t.Errorf("List = %+v, %v", list, err)
	}

	if _, err := s.Block(ctx, 999); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("Block(999) error = %v, want ErrNotFound", err)
	}
	if _, err := s.Unblock(ctx, 999); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("Unblock(999) error = %v, want ErrNotFound", err)
	}
}

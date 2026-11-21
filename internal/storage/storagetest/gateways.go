package storagetest

import (
	"crypto/sha256"
	"errors"
	"testing"
	"time"

	"github.com/sergezossimov0/meters/internal/domain"
	"github.com/sergezossimov0/meters/internal/storage"
)

func keyHash(s string) []byte {
	h := sha256.Sum256([]byte(s))
	return h[:]
}

// GatewayRepository checks a storage.GatewayRepository. newRepo must return
// a repository without gateways.
func GatewayRepository(t *testing.T, newRepo func(t *testing.T) storage.GatewayRepository) {
	t.Run("empty list is not nil", func(t *testing.T) {
		list, err := newRepo(t).List(t.Context())
		if err != nil || list == nil || len(list) != 0 {
			t.Errorf("List = %#v, %v; want empty non-nil slice", list, err)
		}
	})

	t.Run("create, find by key hash, list in id order", func(t *testing.T) {
		repo := newRepo(t)
		ctx := t.Context()
		before := time.Now().Add(-time.Minute)
		a := mustGateway(t, repo, "gw-a", "key-a")
		b := mustGateway(t, repo, "gw-b", "key-b")
		if a.ID <= 0 || a.ID == b.ID || a.CreatedAt.Before(before) || a.Blocked || a.LastHeartbeatAt != nil {
			t.Fatalf("Create returned %+v and %+v", a, b)
		}

		got, err := repo.ByKeyHash(ctx, keyHash("key-b"))
		if err != nil || !sameGateway(got, b) {
			t.Errorf("ByKeyHash = %+v, %v; want %+v", got, err, b)
		}
		list, err := repo.List(ctx)
		if err != nil || len(list) != 2 || !sameGateway(list[0], a) || !sameGateway(list[1], b) {
			t.Errorf("List = %+v, %v; want a, b", list, err)
		}
	})

	t.Run("unknown key hash", func(t *testing.T) {
		repo := newRepo(t)
		mustGateway(t, repo, "gw", "key")
		if _, err := repo.ByKeyHash(t.Context(), keyHash("other")); !errors.Is(err, domain.ErrNotFound) {
			t.Errorf("error = %v, want ErrNotFound", err)
		}
	})

	t.Run("duplicate key hash", func(t *testing.T) {
		repo := newRepo(t)
		mustGateway(t, repo, "gw", "key")
		_, err := repo.Create(t.Context(), domain.Gateway{Name: "other"}, keyHash("key"))
		if !errors.Is(err, domain.ErrConflict) {
			t.Errorf("error = %v, want ErrConflict", err)
		}
	})

	t.Run("block and unblock are idempotent", func(t *testing.T) {
		repo := newRepo(t)
		ctx := t.Context()
		g := mustGateway(t, repo, "gw", "key")
		for _, blocked := range []bool{true, true, false, false} {
			got, err := repo.SetBlocked(ctx, g.ID, blocked)
			if err != nil || got.Blocked != blocked || got.ID != g.ID || got.Name != "gw" {
				t.Fatalf("SetBlocked(%v) = %+v, %v", blocked, got, err)
			}
			found, _ := repo.ByKeyHash(ctx, keyHash("key"))
			if found.Blocked != blocked {
				t.Fatalf("after SetBlocked(%v) the stored flag is %v", blocked, found.Blocked)
			}
		}
	})

	t.Run("block unknown gateway", func(t *testing.T) {
		if _, err := newRepo(t).SetBlocked(t.Context(), 424242, true); !errors.Is(err, domain.ErrNotFound) {
			t.Errorf("error = %v, want ErrNotFound", err)
		}
	})
}

func mustGateway(t *testing.T, repo storage.GatewayRepository, name, key string) domain.Gateway {
	t.Helper()
	g, err := repo.Create(t.Context(), domain.Gateway{Name: name}, keyHash(key))
	if err != nil {
		t.Fatalf("Create(%s): %v", name, err)
	}
	if g.Name != name {
		t.Fatalf("Create(%s) returned %+v", name, g)
	}
	return g
}

func sameGateway(a, b domain.Gateway) bool {
	return a.ID == b.ID && a.Name == b.Name && a.Blocked == b.Blocked &&
		(a.LastHeartbeatAt == nil) == (b.LastHeartbeatAt == nil) &&
		a.CreatedAt.Truncate(time.Microsecond).Equal(b.CreatedAt.Truncate(time.Microsecond))
}

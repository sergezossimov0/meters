// Package storagetest holds behaviour tests that every storage
// implementation must pass: memory in unit tests, postgres in integration
// tests. The same checks keep the in-memory fakes honest.
package storagetest

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/sergezossimov0/meters/internal/domain"
	"github.com/sergezossimov0/meters/internal/storage"
)

// UserRepository checks a storage.UserRepository. newRepo must return an
// empty repository for each call.
func UserRepository(t *testing.T, newRepo func(t *testing.T) storage.UserRepository) {
	t.Run("create and find", func(t *testing.T) {
		repo := newRepo(t)
		ctx := t.Context()
		before := time.Now().Add(-time.Minute)

		u, err := repo.Create(ctx, domain.User{Login: "ivan", PasswordHash: "hash-1", Role: domain.RoleCustomer})
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		if u.ID <= 0 || u.CreatedAt.Before(before) || u.Login != "ivan" || u.Role != domain.RoleCustomer {
			t.Fatalf("Create returned %+v", u)
		}

		byLogin, err := repo.ByLogin(ctx, "ivan")
		if err != nil || !sameUser(byLogin, u) {
			t.Errorf("ByLogin = %+v, %v; want %+v", byLogin, err, u)
		}
		byID, err := repo.ByID(ctx, u.ID)
		if err != nil || !sameUser(byID, u) {
			t.Errorf("ByID = %+v, %v; want %+v", byID, err, u)
		}
	})

	t.Run("ids are distinct", func(t *testing.T) {
		repo := newRepo(t)
		a, errA := repo.Create(t.Context(), domain.User{Login: "anna", PasswordHash: "h", Role: domain.RoleCustomer})
		b, errB := repo.Create(t.Context(), domain.User{Login: "boris", PasswordHash: "h", Role: domain.RoleAdmin})
		if errA != nil || errB != nil || a.ID == b.ID {
			t.Errorf("ids %d and %d, errors %v, %v", a.ID, b.ID, errA, errB)
		}
	})

	t.Run("not found", func(t *testing.T) {
		repo := newRepo(t)
		if _, err := repo.ByLogin(t.Context(), "nobody"); !errors.Is(err, domain.ErrNotFound) {
			t.Errorf("ByLogin(nobody) error = %v, want ErrNotFound", err)
		}
		if _, err := repo.ByID(t.Context(), 424242); !errors.Is(err, domain.ErrNotFound) {
			t.Errorf("ByID(424242) error = %v, want ErrNotFound", err)
		}
	})

	t.Run("duplicate login", func(t *testing.T) {
		repo := newRepo(t)
		if _, err := repo.Create(t.Context(), domain.User{Login: "ivan", PasswordHash: "h", Role: domain.RoleCustomer}); err != nil {
			t.Fatal(err)
		}
		_, err := repo.Create(t.Context(), domain.User{Login: "ivan", PasswordHash: "other", Role: domain.RoleAdmin})
		if !errors.Is(err, domain.ErrConflict) {
			t.Errorf("second Create error = %v, want ErrConflict", err)
		}
		u, _ := repo.ByLogin(t.Context(), "ivan")
		if u.PasswordHash != "h" || u.Role != domain.RoleCustomer {
			t.Errorf("first user was overwritten: %+v", u)
		}
	})

	t.Run("concurrent registration of one login", func(t *testing.T) {
		repo := newRepo(t)
		const n = 8
		errs := make([]error, n)
		var wg sync.WaitGroup
		for i := range n {
			wg.Go(func() {
				_, errs[i] = repo.Create(t.Context(), domain.User{Login: "race", PasswordHash: "h", Role: domain.RoleCustomer})
			})
		}
		wg.Wait()
		ok, conflicts := 0, 0
		for _, err := range errs {
			switch {
			case err == nil:
				ok++
			case errors.Is(err, domain.ErrConflict):
				conflicts++
			default:
				t.Errorf("unexpected error: %v", err)
			}
		}
		if ok != 1 || conflicts != n-1 {
			t.Errorf("%d succeeded, %d conflicts; want 1 and %d", ok, conflicts, n-1)
		}
	})
}

// sameUser compares users; CreatedAt is compared to the microsecond, the
// precision of PostgreSQL timestamptz.
func sameUser(a, b domain.User) bool {
	return a.ID == b.ID && a.Login == b.Login && a.PasswordHash == b.PasswordHash && a.Role == b.Role &&
		a.CreatedAt.Truncate(time.Microsecond).Equal(b.CreatedAt.Truncate(time.Microsecond))
}

// MeterTypeRepository checks a storage.MeterTypeRepository. newRepo must
// return a repository without types and subtypes.
func MeterTypeRepository(t *testing.T, newRepo func(t *testing.T) storage.MeterTypeRepository) {
	t.Run("empty list is not nil", func(t *testing.T) {
		list, err := newRepo(t).List(t.Context())
		if err != nil || list == nil || len(list) != 0 {
			t.Errorf("List = %#v, %v; want empty non-nil slice", list, err)
		}
	})

	t.Run("types and subtypes are listed in code order", func(t *testing.T) {
		repo := newRepo(t)
		ctx := t.Context()
		water := mustType(t, repo, "water", "Вода")
		gas := mustType(t, repo, "gas", "Газ")
		mustSubtype(t, repo, "water", "hot_water", "Горячая вода", "m3")
		mustSubtype(t, repo, "water", "cold_water", "Холодная вода", "m3")

		list, err := repo.List(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if len(list) != 2 || list[0].Code != "gas" || list[1].Code != "water" {
			t.Fatalf("List = %+v, want gas, water", list)
		}
		if list[0].ID != gas.ID || list[0].Subtypes == nil || len(list[0].Subtypes) != 0 {
			t.Errorf("gas = %+v, want its ID and an empty non-nil subtype list", list[0])
		}
		ws := list[1].Subtypes
		if len(ws) != 2 || ws[0].Code != "cold_water" || ws[1].Code != "hot_water" {
			t.Fatalf("water subtypes = %+v, want cold_water, hot_water", ws)
		}
		if ws[0].TypeID != water.ID || ws[0].Name != "Холодная вода" || ws[0].Unit != "m3" || ws[0].ID <= 0 {
			t.Errorf("cold_water = %+v", ws[0])
		}
	})

	t.Run("duplicate type code", func(t *testing.T) {
		repo := newRepo(t)
		mustType(t, repo, "water", "Вода")
		if _, err := repo.CreateType(t.Context(), domain.MeterType{Code: "water", Name: "Ещё вода"}); !errors.Is(err, domain.ErrConflict) {
			t.Errorf("error = %v, want ErrConflict", err)
		}
	})

	t.Run("subtype of unknown type", func(t *testing.T) {
		repo := newRepo(t)
		_, err := repo.CreateSubtype(t.Context(), "nope", domain.MeterSubtype{Code: "x_sub", Name: "X", Unit: "m3"})
		if !errors.Is(err, domain.ErrNotFound) {
			t.Errorf("error = %v, want ErrNotFound", err)
		}
	})

	t.Run("subtype codes are unique across types", func(t *testing.T) {
		repo := newRepo(t)
		mustType(t, repo, "water", "Вода")
		mustType(t, repo, "heat", "Тепло")
		mustSubtype(t, repo, "water", "hot_water", "Горячая вода", "m3")
		_, err := repo.CreateSubtype(t.Context(), "heat", domain.MeterSubtype{Code: "hot_water", Name: "Другое", Unit: "Gcal"})
		if !errors.Is(err, domain.ErrConflict) {
			t.Errorf("error = %v, want ErrConflict", err)
		}
	})

	t.Run("type and subtype may share a code", func(t *testing.T) {
		repo := newRepo(t)
		mustType(t, repo, "heat", "Тепло")
		mustSubtype(t, repo, "heat", "heat", "Тепловая энергия", "Gcal")
	})
}

func mustType(t *testing.T, repo storage.MeterTypeRepository, code, name string) domain.MeterType {
	t.Helper()
	mt, err := repo.CreateType(t.Context(), domain.MeterType{Code: code, Name: name})
	if err != nil {
		t.Fatalf("CreateType(%s): %v", code, err)
	}
	if mt.ID <= 0 || mt.Code != code || mt.Name != name {
		t.Fatalf("CreateType(%s) returned %+v", code, mt)
	}
	return mt
}

func mustSubtype(t *testing.T, repo storage.MeterTypeRepository, typeCode, code, name, unit string) domain.MeterSubtype {
	t.Helper()
	s, err := repo.CreateSubtype(t.Context(), typeCode, domain.MeterSubtype{Code: code, Name: name, Unit: unit})
	if err != nil {
		t.Fatalf("CreateSubtype(%s/%s): %v", typeCode, code, err)
	}
	if s.ID <= 0 || s.TypeID <= 0 || s.Code != code || s.Unit != unit {
		t.Fatalf("CreateSubtype(%s/%s) returned %+v", typeCode, code, s)
	}
	return s
}

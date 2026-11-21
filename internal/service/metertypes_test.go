package service

import (
	"errors"
	"strings"
	"testing"

	"github.com/sergezossimov0/meters/internal/domain"
	"github.com/sergezossimov0/meters/internal/storage/memory"
)

func TestMeterTypes(t *testing.T) {
	ctx := t.Context()
	s := NewMeterTypes(memory.NewMeterTypes())

	typ, err := s.CreateType(ctx, "water", "  Вода ")
	if err != nil {
		t.Fatal(err)
	}
	if typ.Name != "Вода" || typ.Subtypes == nil {
		t.Errorf("type = %+v: want trimmed name and empty, non-nil subtypes", typ)
	}
	st, err := s.CreateSubtype(ctx, "water", "cold_water", "Холодная вода", " m3 ")
	if err != nil {
		t.Fatal(err)
	}
	if st.Unit != "m3" || st.TypeID != typ.ID {
		t.Errorf("subtype = %+v", st)
	}

	list, err := s.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || len(list[0].Subtypes) != 1 || list[0].Subtypes[0].Code != "cold_water" {
		t.Errorf("list = %+v", list)
	}
}

func TestMeterTypes_Errors(t *testing.T) {
	ctx := t.Context()
	s := NewMeterTypes(memory.NewMeterTypes())
	if _, err := s.CreateType(ctx, "water", "Вода"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateSubtype(ctx, "water", "cold_water", "Холодная вода", "m3"); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name string
		err  error
		want error
	}{
		{"type: bad code", second(s.CreateType(ctx, "Water", "Вода")), domain.ErrInvalid},
		{"type: empty name", second(s.CreateType(ctx, "gas", "   ")), domain.ErrInvalid},
		{"type: long name", second(s.CreateType(ctx, "gas", strings.Repeat("я", domain.NameMaxLen+1))), domain.ErrInvalid},
		{"type: taken", second(s.CreateType(ctx, "water", "Ещё вода")), domain.ErrConflict},
		{"subtype: bad code", second(s.CreateSubtype(ctx, "water", "1hot", "Горячая", "m3")), domain.ErrInvalid},
		{"subtype: no unit", second(s.CreateSubtype(ctx, "water", "hot_water", "Горячая", "")), domain.ErrInvalid},
		{"subtype: unknown type", second(s.CreateSubtype(ctx, "steam", "hot_steam", "Пар", "t")), domain.ErrNotFound},
		{"subtype: taken", second(s.CreateSubtype(ctx, "water", "cold_water", "Ещё", "m3")), domain.ErrConflict},
	}
	for _, tt := range tests {
		if !errors.Is(tt.err, tt.want) {
			t.Errorf("%s: err = %v, want %v", tt.name, tt.err, tt.want)
		}
	}
}

func second[T any](_ T, err error) error { return err }

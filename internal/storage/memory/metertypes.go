package memory

import (
	"cmp"
	"context"
	"slices"
	"sync"

	"github.com/sergezossimov0/meters/internal/domain"
	"github.com/sergezossimov0/meters/internal/storage"
)

var _ storage.MeterTypeRepository = (*MeterTypes)(nil)

// MeterTypes is an in-memory storage.MeterTypeRepository. It starts empty:
// unlike the database, it has no types from the migration.
type MeterTypes struct {
	mu       sync.RWMutex
	types    []domain.MeterType // without Subtypes
	subtypes []domain.MeterSubtype
}

// NewMeterTypes returns an empty repository.
func NewMeterTypes() *MeterTypes { return &MeterTypes{} }

// List implements storage.MeterTypeRepository.
func (r *MeterTypes) List(_ context.Context) ([]domain.MeterType, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]domain.MeterType, 0, len(r.types))
	for _, t := range r.types {
		t.Subtypes = []domain.MeterSubtype{}
		for _, s := range r.subtypes {
			if s.TypeID == t.ID {
				t.Subtypes = append(t.Subtypes, s)
			}
		}
		slices.SortFunc(t.Subtypes, func(a, b domain.MeterSubtype) int { return cmp.Compare(a.Code, b.Code) })
		out = append(out, t)
	}
	slices.SortFunc(out, func(a, b domain.MeterType) int { return cmp.Compare(a.Code, b.Code) })
	return out, nil
}

// CreateType implements storage.MeterTypeRepository.
func (r *MeterTypes) CreateType(_ context.Context, t domain.MeterType) (domain.MeterType, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if slices.ContainsFunc(r.types, func(x domain.MeterType) bool { return x.Code == t.Code }) {
		return domain.MeterType{}, domain.ErrConflict
	}
	t.ID = len(r.types) + 1
	t.Subtypes = nil
	r.types = append(r.types, t)
	return t, nil
}

// CreateSubtype implements storage.MeterTypeRepository.
func (r *MeterTypes) CreateSubtype(_ context.Context, typeCode string, s domain.MeterSubtype) (domain.MeterSubtype, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	i := slices.IndexFunc(r.types, func(x domain.MeterType) bool { return x.Code == typeCode })
	if i < 0 {
		return domain.MeterSubtype{}, domain.ErrNotFound
	}
	if slices.ContainsFunc(r.subtypes, func(x domain.MeterSubtype) bool { return x.Code == s.Code }) {
		return domain.MeterSubtype{}, domain.ErrConflict
	}
	s.ID = len(r.subtypes) + 1
	s.TypeID = r.types[i].ID
	r.subtypes = append(r.subtypes, s)
	return s, nil
}

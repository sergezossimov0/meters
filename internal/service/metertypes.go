package service

import (
	"context"
	"fmt"

	"github.com/sergezossimov0/meters/internal/domain"
	"github.com/sergezossimov0/meters/internal/storage"
)

// MeterTypes reads and extends the meter type catalogue.
type MeterTypes struct {
	repo storage.MeterTypeRepository
}

// NewMeterTypes wires the service.
func NewMeterTypes(repo storage.MeterTypeRepository) *MeterTypes {
	return &MeterTypes{repo: repo}
}

// List returns all types with their subtypes, ordered by code.
func (s *MeterTypes) List(ctx context.Context) ([]domain.MeterType, error) {
	types, err := s.repo.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("list meter types: %w", err)
	}
	return types, nil
}

// CreateType adds a type. Errors: domain.ErrInvalid (with the reason),
// domain.ErrConflict if the code is taken.
func (s *MeterTypes) CreateType(ctx context.Context, code, name string) (domain.MeterType, error) {
	if err := domain.ValidateCode(code); err != nil {
		return domain.MeterType{}, err
	}
	n, err := domain.NormalizeName(name)
	if err != nil {
		return domain.MeterType{}, err
	}
	t, err := s.repo.CreateType(ctx, domain.MeterType{Code: code, Name: n})
	if err != nil {
		return domain.MeterType{}, fmt.Errorf("create meter type %q: %w", code, err)
	}
	t.Subtypes = []domain.MeterSubtype{}
	return t, nil
}

// CreateSubtype adds a subtype to the type with typeCode. Errors:
// domain.ErrInvalid (with the reason), domain.ErrNotFound for an unknown
// type, domain.ErrConflict if the subtype code is taken.
func (s *MeterTypes) CreateSubtype(ctx context.Context, typeCode, code, name, unit string) (domain.MeterSubtype, error) {
	if err := domain.ValidateCode(code); err != nil {
		return domain.MeterSubtype{}, err
	}
	n, err := domain.NormalizeName(name)
	if err != nil {
		return domain.MeterSubtype{}, err
	}
	u, err := domain.NormalizeUnit(unit)
	if err != nil {
		return domain.MeterSubtype{}, err
	}
	st, err := s.repo.CreateSubtype(ctx, typeCode, domain.MeterSubtype{Code: code, Name: n, Unit: u})
	if err != nil {
		return domain.MeterSubtype{}, fmt.Errorf("create meter subtype %q of %q: %w", code, typeCode, err)
	}
	return st, nil
}

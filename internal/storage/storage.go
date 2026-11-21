// Package storage declares the repositories the services depend on.
// Implementations: postgres (production) and memory (unit tests).
// Repositories store what they are given: validation is the services' job.
// Errors are reported as domain.ErrNotFound / domain.ErrConflict.
package storage

import (
	"context"

	"github.com/sergezossimov0/meters/internal/domain"
)

// UserRepository keeps accounts.
type UserRepository interface {
	// Create stores u and returns it with ID and CreatedAt set.
	// A taken login gives domain.ErrConflict.
	Create(ctx context.Context, u domain.User) (domain.User, error)
	// ByLogin finds a user by the (already normalized) login.
	ByLogin(ctx context.Context, login string) (domain.User, error)
	// ByID finds a user by ID.
	ByID(ctx context.Context, id int64) (domain.User, error)
}

// MeterTypeRepository keeps the reference data of meter types and subtypes.
type MeterTypeRepository interface {
	// List returns all types ordered by code, each with its subtypes ordered
	// by code. An empty catalogue is an empty, non-nil slice.
	List(ctx context.Context) ([]domain.MeterType, error)
	// CreateType stores a type; a taken code gives domain.ErrConflict.
	CreateType(ctx context.Context, t domain.MeterType) (domain.MeterType, error)
	// CreateSubtype stores a subtype of the type with typeCode. An unknown
	// type gives domain.ErrNotFound, a taken subtype code domain.ErrConflict.
	CreateSubtype(ctx context.Context, typeCode string, s domain.MeterSubtype) (domain.MeterSubtype, error)
}

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

// GatewayRepository keeps gateways. The API key itself is never passed here,
// only its sha256.
type GatewayRepository interface {
	// Create stores g with keyHash and returns it with ID and CreatedAt set.
	// A taken keyHash gives domain.ErrConflict.
	Create(ctx context.Context, g domain.Gateway, keyHash []byte) (domain.Gateway, error)
	// List returns all gateways ordered by ID; none is an empty, non-nil slice.
	List(ctx context.Context) ([]domain.Gateway, error)
	// ByKeyHash finds the gateway whose key has this sha256.
	ByKeyHash(ctx context.Context, keyHash []byte) (domain.Gateway, error)
	// SetBlocked sets the flag and returns the gateway; setting the current
	// value again is not an error. An unknown id gives domain.ErrNotFound.
	SetBlocked(ctx context.Context, id int64, blocked bool) (domain.Gateway, error)
}

// ReadingRepository stores readings from gateways.
type ReadingRepository interface {
	// IngestBatch processes a checked batch from the gateway atomically
	// (spec п. 6.1, 10.3):
	//   - a batch_id already processed for this gateway returns the stored
	//     result with replayed = true and changes nothing;
	//   - unknown serials become pending devices with the model from b;
	//   - readings of non-active devices are rejected (domain.DeviceRejection);
	//   - readings of active devices go through domain.Classify: duplicates
	//     are counted, the rest stored with the decrease flag.
	// rejected are the rejections found while checking b; they become part of
	// the stored result so a replay answers exactly the same.
	IngestBatch(ctx context.Context, gatewayID int64, b domain.Batch, rejected []domain.Rejected) (res domain.BatchResult, replayed bool, err error)
}

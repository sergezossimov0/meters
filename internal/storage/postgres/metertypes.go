package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"

	"github.com/sergezossimov0/meters/internal/domain"
	"github.com/sergezossimov0/meters/internal/retry"
	"github.com/sergezossimov0/meters/internal/storage"
)

var _ storage.MeterTypeRepository = (*MeterTypes)(nil)

// MeterTypes is the PostgreSQL storage.MeterTypeRepository.
type MeterTypes struct {
	db    *pgxpool.Pool
	retry retry.Retrier
}

// NewMeterTypes returns a repository over pool; log receives retry warnings.
func NewMeterTypes(pool *pgxpool.Pool, log *zap.Logger) *MeterTypes {
	return &MeterTypes{db: pool, retry: newRetrier(log)}
}

// List implements storage.MeterTypeRepository with one query: types joined
// with their subtypes, both in code order.
func (r *MeterTypes) List(ctx context.Context) ([]domain.MeterType, error) {
	var types []domain.MeterType
	err := r.retry.Do(ctx, func(ctx context.Context) error {
		rows, err := r.db.Query(ctx, `
			SELECT t.id, t.code, t.name, s.id, s.code, s.name, s.unit
			FROM meter_types t
			LEFT JOIN meter_subtypes s ON s.type_id = t.id
			ORDER BY t.code, s.code`)
		if err != nil {
			return err
		}
		defer rows.Close()

		types = []domain.MeterType{} // reset: a retry starts over
		for rows.Next() {
			var (
				t                         domain.MeterType
				subID                     *int
				subCode, subName, subUnit *string
			)
			if err := rows.Scan(&t.ID, &t.Code, &t.Name, &subID, &subCode, &subName, &subUnit); err != nil {
				return err
			}
			if n := len(types); n == 0 || types[n-1].ID != t.ID {
				t.Subtypes = []domain.MeterSubtype{}
				types = append(types, t)
			}
			if subID != nil { // LEFT JOIN: a type without subtypes gives one row of NULLs
				last := &types[len(types)-1]
				last.Subtypes = append(last.Subtypes, domain.MeterSubtype{
					ID: *subID, TypeID: t.ID, Code: *subCode, Name: *subName, Unit: *subUnit,
				})
			}
		}
		return rows.Err()
	})
	if err != nil {
		return nil, fmt.Errorf("list meter types: %w", mapError(err))
	}
	return types, nil
}

// CreateType implements storage.MeterTypeRepository.
func (r *MeterTypes) CreateType(ctx context.Context, t domain.MeterType) (domain.MeterType, error) {
	err := r.retry.Do(ctx, func(ctx context.Context) error {
		return r.db.QueryRow(ctx,
			`INSERT INTO meter_types (code, name) VALUES ($1, $2) RETURNING id`,
			t.Code, t.Name,
		).Scan(&t.ID)
	})
	if err != nil {
		return domain.MeterType{}, fmt.Errorf("create meter type: %w", mapError(err))
	}
	t.Subtypes = nil
	return t, nil
}

// CreateSubtype implements storage.MeterTypeRepository. The type is looked
// up in the same statement, so there is no gap between check and insert:
// no row back means the type does not exist.
func (r *MeterTypes) CreateSubtype(ctx context.Context, typeCode string, s domain.MeterSubtype) (domain.MeterSubtype, error) {
	err := r.retry.Do(ctx, func(ctx context.Context) error {
		return r.db.QueryRow(ctx,
			`INSERT INTO meter_subtypes (type_id, code, name, unit)
			 SELECT id, $2, $3, $4 FROM meter_types WHERE code = $1
			 RETURNING id, type_id`,
			typeCode, s.Code, s.Name, s.Unit,
		).Scan(&s.ID, &s.TypeID)
	})
	if err != nil {
		return domain.MeterSubtype{}, fmt.Errorf("create meter subtype: %w", mapError(err))
	}
	return s, nil
}

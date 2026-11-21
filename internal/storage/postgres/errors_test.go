package postgres

import (
	"errors"
	"fmt"
	"testing"

	"github.com/jackc/pgerrcode"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/sergezossimov0/meters/internal/domain"
)

func TestMapError(t *testing.T) {
	plain := errors.New("boom")
	tests := []struct {
		name string
		err  error
		want error
	}{
		{"no rows", pgx.ErrNoRows, domain.ErrNotFound},
		{"wrapped no rows", fmt.Errorf("scan: %w", pgx.ErrNoRows), domain.ErrNotFound},
		{"unique violation", &pgconn.PgError{Code: pgerrcode.UniqueViolation}, domain.ErrConflict},
		{"foreign key violation", &pgconn.PgError{Code: pgerrcode.ForeignKeyViolation}, domain.ErrNotFound},
		{"other database error", &pgconn.PgError{Code: pgerrcode.CheckViolation}, nil},
		{"plain error", plain, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := mapError(tt.err)
			if !errors.Is(got, tt.err) {
				t.Errorf("mapError lost the original error: %v", got)
			}
			if tt.want != nil && !errors.Is(got, tt.want) {
				t.Errorf("mapError(%v) = %v, want %v", tt.err, got, tt.want)
			}
			if tt.want == nil && (errors.Is(got, domain.ErrNotFound) || errors.Is(got, domain.ErrConflict)) {
				t.Errorf("mapError(%v) = %v, want it unchanged", tt.err, got)
			}
		})
	}
	if mapError(nil) != nil {
		t.Error("mapError(nil) != nil")
	}
}

package postgres

import (
	"errors"
	"fmt"

	"github.com/jackc/pgerrcode"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/sergezossimov0/meters/internal/domain"
)

// mapError translates database errors into domain errors, keeping the
// original in the chain for logs:
//   - no rows → domain.ErrNotFound;
//   - unique violation → domain.ErrConflict;
//   - foreign key violation → domain.ErrNotFound (the referenced row is missing).
func mapError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("%w (%w)", domain.ErrNotFound, err)
	}
	if pgErr, ok := errors.AsType[*pgconn.PgError](err); ok {
		switch pgErr.Code {
		case pgerrcode.UniqueViolation:
			return fmt.Errorf("%w (%w)", domain.ErrConflict, err)
		case pgerrcode.ForeignKeyViolation:
			return fmt.Errorf("%w (%w)", domain.ErrNotFound, err)
		}
	}
	return err
}

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

var _ storage.UserRepository = (*Users)(nil)

// Users is the PostgreSQL storage.UserRepository. Every operation is one
// statement, repeated on connection errors.
//
// A retried INSERT may report ErrConflict for a user that the lost attempt
// did create: the connection broke after the commit. The caller then sees
// "login taken" for its own registration; logging in works.
type Users struct {
	db    *pgxpool.Pool
	retry retry.Retrier
}

// NewUsers returns a repository over pool; log receives retry warnings.
func NewUsers(pool *pgxpool.Pool, log *zap.Logger) *Users {
	return &Users{db: pool, retry: newRetrier(log)}
}

const userColumns = `id, login, password_hash, role, created_at`

// Create implements storage.UserRepository.
func (r *Users) Create(ctx context.Context, u domain.User) (domain.User, error) {
	err := r.retry.Do(ctx, func(ctx context.Context) error {
		return r.db.QueryRow(ctx,
			`INSERT INTO users (login, password_hash, role) VALUES ($1, $2, $3)
			 RETURNING id, created_at`,
			u.Login, u.PasswordHash, u.Role,
		).Scan(&u.ID, &u.CreatedAt)
	})
	if err != nil {
		return domain.User{}, fmt.Errorf("create user: %w", mapError(err))
	}
	return u, nil
}

// ByLogin implements storage.UserRepository.
func (r *Users) ByLogin(ctx context.Context, login string) (domain.User, error) {
	return r.one(ctx, `SELECT `+userColumns+` FROM users WHERE login = $1`, login)
}

// ByID implements storage.UserRepository.
func (r *Users) ByID(ctx context.Context, id int64) (domain.User, error) {
	return r.one(ctx, `SELECT `+userColumns+` FROM users WHERE id = $1`, id)
}

func (r *Users) one(ctx context.Context, query string, arg any) (domain.User, error) {
	var u domain.User
	err := r.retry.Do(ctx, func(ctx context.Context) error {
		return r.db.QueryRow(ctx, query, arg).Scan(&u.ID, &u.Login, &u.PasswordHash, &u.Role, &u.CreatedAt)
	})
	if err != nil {
		return domain.User{}, fmt.Errorf("get user: %w", mapError(err))
	}
	return u, nil
}

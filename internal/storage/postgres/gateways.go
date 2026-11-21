package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"

	"github.com/sergezossimov0/meters/internal/domain"
	"github.com/sergezossimov0/meters/internal/retry"
	"github.com/sergezossimov0/meters/internal/storage"
)

var _ storage.GatewayRepository = (*Gateways)(nil)

// Gateways is the PostgreSQL storage.GatewayRepository. Every operation is
// one statement, repeated on connection errors.
//
// A retried Create whose first attempt was committed before the connection
// broke reports ErrConflict (the key hash is taken by itself); the admin sees
// an error and an extra gateway with an unknown key, which can be blocked.
type Gateways struct {
	db    *pgxpool.Pool
	retry retry.Retrier
}

// NewGateways returns a repository over pool; log receives retry warnings.
func NewGateways(pool *pgxpool.Pool, log *zap.Logger) *Gateways {
	return &Gateways{db: pool, retry: newRetrier(log)}
}

const gatewayColumns = `id, name, blocked, last_heartbeat_at, created_at`

func scanGateway(row pgx.Row) (domain.Gateway, error) {
	var g domain.Gateway
	err := row.Scan(&g.ID, &g.Name, &g.Blocked, &g.LastHeartbeatAt, &g.CreatedAt)
	return g, err
}

// Create implements storage.GatewayRepository.
func (r *Gateways) Create(ctx context.Context, g domain.Gateway, keyHash []byte) (domain.Gateway, error) {
	var out domain.Gateway
	err := r.retry.Do(ctx, func(ctx context.Context) (err error) {
		out, err = scanGateway(r.db.QueryRow(ctx,
			`INSERT INTO gateways (name, key_hash, blocked) VALUES ($1, $2, $3)
			 RETURNING `+gatewayColumns,
			g.Name, keyHash, g.Blocked))
		return err
	})
	if err != nil {
		return domain.Gateway{}, fmt.Errorf("create gateway: %w", mapError(err))
	}
	return out, nil
}

// List implements storage.GatewayRepository.
func (r *Gateways) List(ctx context.Context) ([]domain.Gateway, error) {
	var list []domain.Gateway
	err := r.retry.Do(ctx, func(ctx context.Context) error {
		rows, err := r.db.Query(ctx, `SELECT `+gatewayColumns+` FROM gateways ORDER BY id`)
		if err != nil {
			return err
		}
		list, err = pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.Gateway, error) {
			return scanGateway(row)
		})
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("list gateways: %w", mapError(err))
	}
	if list == nil {
		list = []domain.Gateway{}
	}
	return list, nil
}

// ByKeyHash implements storage.GatewayRepository.
func (r *Gateways) ByKeyHash(ctx context.Context, keyHash []byte) (domain.Gateway, error) {
	var g domain.Gateway
	err := r.retry.Do(ctx, func(ctx context.Context) (err error) {
		g, err = scanGateway(r.db.QueryRow(ctx,
			`SELECT `+gatewayColumns+` FROM gateways WHERE key_hash = $1`, keyHash))
		return err
	})
	if err != nil {
		return domain.Gateway{}, fmt.Errorf("gateway by key: %w", mapError(err))
	}
	return g, nil
}

// SetBlocked implements storage.GatewayRepository.
func (r *Gateways) SetBlocked(ctx context.Context, id int64, blocked bool) (domain.Gateway, error) {
	var g domain.Gateway
	err := r.retry.Do(ctx, func(ctx context.Context) (err error) {
		g, err = scanGateway(r.db.QueryRow(ctx,
			`UPDATE gateways SET blocked = $2 WHERE id = $1 RETURNING `+gatewayColumns, id, blocked))
		return err
	})
	if err != nil {
		return domain.Gateway{}, fmt.Errorf("set gateway %d blocked=%v: %w", id, blocked, mapError(err))
	}
	return g, nil
}

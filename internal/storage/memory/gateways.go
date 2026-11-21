package memory

import (
	"context"
	"maps"
	"slices"
	"sync"
	"time"

	"github.com/sergezossimov0/meters/internal/domain"
	"github.com/sergezossimov0/meters/internal/storage"
)

var _ storage.GatewayRepository = (*Gateways)(nil)

// Gateways is an in-memory storage.GatewayRepository.
type Gateways struct {
	mu        sync.RWMutex
	byID      map[int64]domain.Gateway
	byKeyHash map[string]int64
	nextID    int64
}

// NewGateways returns an empty repository.
func NewGateways() *Gateways {
	return &Gateways{byID: map[int64]domain.Gateway{}, byKeyHash: map[string]int64{}}
}

// Create implements storage.GatewayRepository.
func (r *Gateways) Create(_ context.Context, g domain.Gateway, keyHash []byte) (domain.Gateway, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, taken := r.byKeyHash[string(keyHash)]; taken {
		return domain.Gateway{}, domain.ErrConflict
	}
	r.nextID++
	g.ID = r.nextID
	g.CreatedAt = time.Now().UTC()
	r.byID[g.ID] = g
	r.byKeyHash[string(keyHash)] = g.ID
	return g, nil
}

// List implements storage.GatewayRepository.
func (r *Gateways) List(_ context.Context) ([]domain.Gateway, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	list := make([]domain.Gateway, 0, len(r.byID))
	for _, id := range slices.Sorted(maps.Keys(r.byID)) {
		list = append(list, r.byID[id])
	}
	return list, nil
}

// ByKeyHash implements storage.GatewayRepository.
func (r *Gateways) ByKeyHash(_ context.Context, keyHash []byte) (domain.Gateway, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	id, ok := r.byKeyHash[string(keyHash)]
	if !ok {
		return domain.Gateway{}, domain.ErrNotFound
	}
	return r.byID[id], nil
}

// SetBlocked implements storage.GatewayRepository.
func (r *Gateways) SetBlocked(_ context.Context, id int64, blocked bool) (domain.Gateway, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	g, ok := r.byID[id]
	if !ok {
		return domain.Gateway{}, domain.ErrNotFound
	}
	g.Blocked = blocked
	r.byID[id] = g
	return g, nil
}

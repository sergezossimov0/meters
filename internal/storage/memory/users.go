// Package memory implements the storage interfaces in process memory, for
// unit tests of services and handlers. Safe for concurrent use.
package memory

import (
	"context"
	"sync"
	"time"

	"github.com/sergezossimov0/meters/internal/domain"
	"github.com/sergezossimov0/meters/internal/storage"
)

var _ storage.UserRepository = (*Users)(nil)

// Users is an in-memory storage.UserRepository.
type Users struct {
	mu      sync.RWMutex
	byID    map[int64]domain.User
	byLogin map[string]int64
	nextID  int64
}

// NewUsers returns an empty repository.
func NewUsers() *Users {
	return &Users{byID: map[int64]domain.User{}, byLogin: map[string]int64{}}
}

// Create implements storage.UserRepository.
func (r *Users) Create(_ context.Context, u domain.User) (domain.User, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, taken := r.byLogin[u.Login]; taken {
		return domain.User{}, domain.ErrConflict
	}
	r.nextID++
	u.ID = r.nextID
	u.CreatedAt = time.Now().UTC()
	r.byID[u.ID] = u
	r.byLogin[u.Login] = u.ID
	return u, nil
}

// ByLogin implements storage.UserRepository.
func (r *Users) ByLogin(_ context.Context, login string) (domain.User, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	id, ok := r.byLogin[login]
	if !ok {
		return domain.User{}, domain.ErrNotFound
	}
	return r.byID[id], nil
}

// ByID implements storage.UserRepository.
func (r *Users) ByID(_ context.Context, id int64) (domain.User, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	u, ok := r.byID[id]
	if !ok {
		return domain.User{}, domain.ErrNotFound
	}
	return u, nil
}

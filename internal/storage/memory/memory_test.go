package memory

import (
	"testing"

	"github.com/sergezossimov0/meters/internal/storage"
	"github.com/sergezossimov0/meters/internal/storage/storagetest"
)

func TestUsers(t *testing.T) {
	storagetest.UserRepository(t, func(*testing.T) storage.UserRepository { return NewUsers() })
}

func TestMeterTypes(t *testing.T) {
	storagetest.MeterTypeRepository(t, func(*testing.T) storage.MeterTypeRepository { return NewMeterTypes() })
}

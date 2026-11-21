package memory

import (
	"testing"

	"github.com/sergezossimov0/meters/internal/domain"

	"github.com/sergezossimov0/meters/internal/storage"
	"github.com/sergezossimov0/meters/internal/storage/storagetest"
)

func TestUsers(t *testing.T) {
	storagetest.UserRepository(t, func(*testing.T) storage.UserRepository { return NewUsers() })
}

func TestMeterTypes(t *testing.T) {
	storagetest.MeterTypeRepository(t, func(*testing.T) storage.MeterTypeRepository { return NewMeterTypes() })
}

func TestGateways(t *testing.T) {
	storagetest.GatewayRepository(t, func(*testing.T) storage.GatewayRepository { return NewGateways() })
}

type readingsFixture struct {
	r      *Readings
	nextGW int64
}

func (f *readingsFixture) NewGateway(*testing.T) int64 { f.nextGW++; return f.nextGW }

func (f *readingsFixture) SetDevice(t *testing.T, serial string, status domain.DeviceStatus, initial domain.Value) {
	t.Helper()
	if err := f.r.SetDevice(serial, status, initial); err != nil {
		t.Fatalf("SetDevice(%s): %v", serial, err)
	}
}

func (f *readingsFixture) Device(_ *testing.T, serial string) (domain.Device, bool) {
	return f.r.Device(serial)
}

func (f *readingsFixture) StoredReadings(_ *testing.T, serial string) []domain.Reading {
	return f.r.StoredReadings(serial)
}

func TestReadings(t *testing.T) {
	storagetest.ReadingRepository(t, func(*testing.T) (storage.ReadingRepository, storagetest.IngestFixture) {
		r := NewReadings()
		return r, &readingsFixture{r: r}
	})
}

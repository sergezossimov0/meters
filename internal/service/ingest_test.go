package service

import (
	"errors"
	"testing"
	"testing/synctest"
	"time"

	"github.com/sergezossimov0/meters/internal/domain"
	"github.com/sergezossimov0/meters/internal/storage/memory"
)

func TestIngest_Batch(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		repo := memory.NewReadings()
		s := NewIngest(repo)
		now := time.Now()
		req := domain.BatchRequest{BatchID: "b1", Items: []domain.ItemRequest{{
			DeviceSerial: "A", Model: "M",
			Readings: []domain.ReadingRequest{
				{MeasuredAt: now.Add(-time.Hour), Value: "1"},
				{MeasuredAt: now.Add(10 * time.Minute), Value: "2"}, // future_ts by the service clock
			},
		}}}
		res, replayed, err := s.Batch(t.Context(), 1, req)
		if err != nil || replayed {
			t.Fatalf("Batch = %+v, %v, %v", res, replayed, err)
		}
		want := []domain.Rejected{
			{DeviceSerial: "A", Index: 0, Reason: domain.ReasonDeviceNotActive},
			{DeviceSerial: "A", Index: 1, Reason: domain.ReasonFutureTS},
		}
		if res.Accepted != 0 || len(res.Rejected) != 2 || res.Rejected[0] != want[0] || res.Rejected[1] != want[1] {
			t.Errorf("result = %+v, want %v", res, want)
		}

		// A day later the same batch_id answers the same, though index 0
		// would now be checked against another clock.
		time.Sleep(24 * time.Hour)
		again, replayed, err := s.Batch(t.Context(), 1, req)
		if err != nil || !replayed || len(again.Rejected) != 2 || again.Rejected[1] != want[1] {
			t.Errorf("replay = %+v, %v, %v", again, replayed, err)
		}
	})
}

func TestIngest_Invalid(t *testing.T) {
	s := NewIngest(memory.NewReadings())
	_, _, err := s.Batch(t.Context(), 1, domain.BatchRequest{BatchID: "b1"})
	if !errors.Is(err, domain.ErrInvalid) {
		t.Errorf("empty items: error = %v, want ErrInvalid", err)
	}
}

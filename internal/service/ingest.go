package service

import (
	"context"
	"fmt"
	"time"

	"github.com/sergezossimov0/meters/internal/domain"
	"github.com/sergezossimov0/meters/internal/storage"
)

// Ingest accepts batches of readings from gateways (spec п. 6.1).
type Ingest struct {
	repo storage.ReadingRepository
}

// NewIngest wires the service.
func NewIngest(repo storage.ReadingRepository) *Ingest {
	return &Ingest{repo: repo}
}

// Batch checks req against the current time and stores it for the gateway.
// It returns the answer for the gateway and whether it is a replay of a
// batch_id processed before. Errors: domain.ErrInvalid (400) for a broken
// structure or a limit; anything else is internal.
func (s *Ingest) Batch(ctx context.Context, gatewayID int64, req domain.BatchRequest) (domain.BatchResult, bool, error) {
	b, rejected, err := domain.ValidateBatch(req, time.Now())
	if err != nil {
		return domain.BatchResult{}, false, err
	}
	res, replayed, err := s.repo.IngestBatch(ctx, gatewayID, b, rejected)
	if err != nil {
		return domain.BatchResult{}, false, fmt.Errorf("gateway %d: %w", gatewayID, err)
	}
	return res, replayed, nil
}

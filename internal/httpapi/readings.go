package httpapi

import (
	"context"
	"net/http"

	"go.uber.org/zap"

	"github.com/sergezossimov0/meters/internal/domain"
	"github.com/sergezossimov0/meters/internal/httpapi/middleware"
)

// IngestService is what the gateway readings handler needs; *service.Ingest
// implements it.
type IngestService interface {
	Batch(ctx context.Context, gatewayID int64, req domain.BatchRequest) (domain.BatchResult, bool, error)
}

// ingestReadings: POST /gateway/v1/readings → 200 {"accepted", "duplicates",
// "rejected"}; 400 for broken JSON, structure or limits. It sits behind
// GatewayAuth, which puts the gateway into the context.
func ingestReadings(log *zap.Logger, ingest IngestService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		gw, ok := middleware.GatewayFrom(r.Context())
		if !ok { // wiring mistake: deny rather than store anonymously
			writeInternal(w, r, log, errNoGateway)
			return
		}
		var req domain.BatchRequest
		if !decodeGatewayJSON(w, r, &req) {
			return
		}
		res, replayed, err := ingest.Batch(r.Context(), gw.ID, req)
		switch {
		case err == nil:
		case writeInvalid(w, err):
			log.Info("batch refused", zap.Int64("gateway_id", gw.ID), zap.Error(err))
			return
		default:
			writeInternal(w, r, log, err)
			return
		}
		log.Info("batch ingested",
			zap.Int64("gateway_id", gw.ID),
			zap.String("batch_id", req.BatchID),
			zap.Bool("replayed", replayed),
			zap.Int("accepted", res.Accepted),
			zap.Int("duplicates", res.Duplicates),
			zap.Int("rejected", len(res.Rejected)))
		writeJSON(w, log, http.StatusOK, res)
	}
}

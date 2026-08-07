package httpapi

import (
	"context"
	"errors"
	"io"
	"net/http"

	"github.com/afun-game/predictmarket-saas/internal/auth"
	"github.com/afun-game/predictmarket-saas/internal/sportsingest"
)

// boxrecIntaker persists captured BoxRec schedule documents into prediction
// events using the shared sports ingestion projection.
type boxrecIntaker interface {
	IngestBoxRec(ctx context.Context, data []byte) (sportsingest.Result, error)
}

// registerBoxRecIntakeRoutes wires the BoxRec schedule intake endpoint. It is
// registered only when a sports ingestion service is supplied as an optional
// service to the API handler.
func registerBoxRecIntakeRoutes(mux *http.ServeMux, service boxrecIntaker, adminAPIKey string) {
	handler := &boxrecIntakeHandler{service: service}
	mux.Handle(
		"POST /api/v1/ingest/boxrec",
		auth.RequireAdmin(adminAPIKey, http.HandlerFunc(handler.ingest)),
	)
}

type boxrecIntakeHandler struct {
	service boxrecIntaker
}

func (h *boxrecIntakeHandler) ingest(w http.ResponseWriter, r *http.Request) {
	if h.service == nil {
		writeError(w, http.StatusServiceUnavailable, "unavailable", "boxrec ingestion is not configured")
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, MaxRequestBodyBytes))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "request body too large or unreadable")
		return
	}
	result, err := h.service.IngestBoxRec(r.Context(), body)
	if err != nil {
		if errors.Is(err, sportsingest.ErrInvalidBoxRecJSON) {
			writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, "ingest_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"data": map[string]any{
			"fetched": result.Fetched,
			"synced":  result.Synced,
			"skipped": result.Skipped,
		},
	})
}

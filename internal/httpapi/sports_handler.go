package httpapi

import (
	"errors"
	"net/http"

	"github.com/afun-game/predictmarket-saas/internal/auth"
	"github.com/afun-game/predictmarket-saas/internal/merchant"
	"github.com/afun-game/predictmarket-saas/internal/sports"
)

type sportsHandler struct{ service sports.Service }

func registerSportsRoutes(mux *http.ServeMux, merchantService merchant.Service, service sports.Service, adminAPIKey string) {
	handler := &sportsHandler{service: service}
	mux.Handle("GET /api/v1/sports/events", auth.RequireMerchant(merchantService, http.HandlerFunc(handler.list)))
	mux.Handle("GET /api/v1/sports/events/{eventID}", auth.RequireMerchant(merchantService, http.HandlerFunc(handler.get)))
	mux.Handle("POST /api/v1/sports/sync", auth.RequireAdmin(adminAPIKey, http.HandlerFunc(handler.sync)))
}

func (h *sportsHandler) list(w http.ResponseWriter, r *http.Request) {
	page, err := queryInt(r, "page")
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	limit, err := queryInt(r, "limit")
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	filters := &sports.EventFilters{
		League: r.URL.Query().Get("league"), Team: r.URL.Query().Get("team"),
		Status: r.URL.Query().Get("status"), Page: page, Limit: limit,
	}
	values, total, err := h.service.ListEvents(r.Context(), filters)
	if err != nil {
		writeSportsServiceError(w, err)
		return
	}
	page, limit = eventPageDefaults(page, limit)
	writeJSON(w, http.StatusOK, map[string]any{
		"data": values,
		"meta": map[string]any{"pagination": newPagination(page, limit, total)},
	})
}

func (h *sportsHandler) get(w http.ResponseWriter, r *http.Request) {
	value, err := h.service.GetEvent(r.Context(), r.PathValue("eventID"))
	if err != nil {
		writeSportsServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": value})
}

func (h *sportsHandler) sync(w http.ResponseWriter, r *http.Request) {
	if err := h.service.SyncFromPolymarket(r.Context()); err != nil {
		writeSportsServiceError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func writeSportsServiceError(w http.ResponseWriter, err error) {
	var validationErr *sports.ValidationError
	switch {
	case errors.As(err, &validationErr):
		writeError(w, http.StatusBadRequest, "validation_error", validationErr.Error())
	case errors.Is(err, sports.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", "sports event was not found")
	default:
		writeError(w, http.StatusInternalServerError, "internal_error", "an internal error occurred")
	}
}

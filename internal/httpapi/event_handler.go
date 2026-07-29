package httpapi

import (
	"errors"
	"net/http"

	"github.com/afun-game/predictmarket-saas/internal/auth"
	"github.com/afun-game/predictmarket-saas/internal/event"
	"github.com/afun-game/predictmarket-saas/internal/merchant"
)

const (
	defaultEventPage  = 1
	defaultEventLimit = 20
)

type eventHandler struct {
	service event.Service
}

type eventStatusRequest struct {
	Status string `json:"status"`
}

type eventResolutionRequest struct {
	Outcome string `json:"outcome"`
}

type pagination struct {
	Page    int  `json:"page"`
	Limit   int  `json:"limit"`
	Total   int  `json:"total"`
	Pages   int  `json:"pages"`
	HasNext bool `json:"has_next"`
	HasPrev bool `json:"has_prev"`
}

func registerEventRoutes(
	mux *http.ServeMux,
	merchantService merchant.Service,
	eventService event.Service,
	adminAPIKey string,
) {
	handler := &eventHandler{service: eventService}
	mux.Handle(
		"GET /api/v1/events",
		auth.RequireMerchant(merchantService, http.HandlerFunc(handler.list)),
	)
	mux.Handle(
		"GET /api/v1/events/{eventID}",
		auth.RequireMerchant(merchantService, http.HandlerFunc(handler.get)),
	)
	mux.Handle(
		"POST /api/v1/events",
		auth.RequireAdmin(adminAPIKey, http.HandlerFunc(handler.create)),
	)
	mux.Handle(
		"PATCH /api/v1/events/{eventID}/status",
		auth.RequireAdmin(adminAPIKey, http.HandlerFunc(handler.updateStatus)),
	)
	mux.Handle(
		"POST /api/v1/events/{eventID}/resolve",
		auth.RequireAdmin(adminAPIKey, http.HandlerFunc(handler.resolve)),
	)
}

func (h *eventHandler) create(w http.ResponseWriter, r *http.Request) {
	var request event.CreateRequest
	if err := decodeJSON(w, r, &request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	created, err := h.service.Create(r.Context(), &request)
	if err != nil {
		writeEventServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"data": created})
}

func (h *eventHandler) list(w http.ResponseWriter, r *http.Request) {
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
	filters := &event.ListFilters{
		Category: r.URL.Query().Get("category"),
		Status:   r.URL.Query().Get("status"),
		Page:     page,
		Limit:    limit,
	}
	values, total, err := h.service.List(r.Context(), filters)
	if err != nil {
		writeEventServiceError(w, err)
		return
	}

	page, limit = eventPageDefaults(page, limit)
	writeJSON(w, http.StatusOK, map[string]any{
		"data": values,
		"meta": map[string]any{
			"pagination": newPagination(page, limit, total),
		},
	})
}

func (h *eventHandler) get(w http.ResponseWriter, r *http.Request) {
	value, err := h.service.Get(r.Context(), r.PathValue("eventID"))
	if err != nil {
		writeEventServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": value})
}

func (h *eventHandler) updateStatus(w http.ResponseWriter, r *http.Request) {
	var request eventStatusRequest
	if err := decodeJSON(w, r, &request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if err := h.service.UpdateStatus(
		r.Context(),
		r.PathValue("eventID"),
		request.Status,
	); err != nil {
		writeEventServiceError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *eventHandler) resolve(w http.ResponseWriter, r *http.Request) {
	var request eventResolutionRequest
	if err := decodeJSON(w, r, &request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if err := h.service.Resolve(
		r.Context(),
		r.PathValue("eventID"),
		request.Outcome,
	); err != nil {
		writeEventServiceError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func eventPageDefaults(page, limit int) (int, int) {
	if page == 0 {
		page = defaultEventPage
	}
	if limit == 0 {
		limit = defaultEventLimit
	}
	return page, limit
}

func newPagination(page, limit, total int) pagination {
	pages := 0
	if total > 0 {
		pages = (total + limit - 1) / limit
	}
	return pagination{
		Page:    page,
		Limit:   limit,
		Total:   total,
		Pages:   pages,
		HasNext: page < pages,
		HasPrev: page > 1,
	}
}

func writeEventServiceError(w http.ResponseWriter, err error) {
	var validationErr *event.ValidationError
	switch {
	case errors.As(err, &validationErr):
		writeError(w, http.StatusBadRequest, "validation_error", validationErr.Error())
	case errors.Is(err, event.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", "event was not found")
	case errors.Is(err, event.ErrAlreadyExists):
		writeError(w, http.StatusConflict, "already_exists", "event already exists")
	case errors.Is(err, event.ErrInvalidTransition):
		writeError(w, http.StatusConflict, "invalid_transition", "event status transition is not allowed")
	default:
		writeError(w, http.StatusInternalServerError, "internal_error", "an internal error occurred")
	}
}

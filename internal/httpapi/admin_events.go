package httpapi

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/http"

	"github.com/afun-game/predictmarket-saas/internal/auth"
	"github.com/afun-game/predictmarket-saas/internal/event"
	"github.com/afun-game/predictmarket-saas/internal/market"
)

// eventMarketView is the compact market summary attached to an event detail.
type eventMarketView struct {
	ID          string  `json:"id"`
	Question    string  `json:"question"`
	Status      string  `json:"status"`
	TotalVolume float64 `json:"total_volume"`
}

// listEvents returns a paginated event list for the console.
func (h *adminHandler) listEvents(w http.ResponseWriter, r *http.Request) {
	page, limit, err := queryPage(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	items, total, err := h.config.Queries.ListEvents(
		r.Context(),
		r.URL.Query().Get("q"),
		r.URL.Query().Get("category"),
		r.URL.Query().Get("status"),
		page,
		limit,
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "could not list events")
		return
	}
	adminList(w, items, total)
}

// getEvent returns one event with its related markets.
func (h *adminHandler) getEvent(w http.ResponseWriter, r *http.Request) {
	eventID := r.PathValue("eventID")
	value, err := h.events.Get(r.Context(), eventID)
	if err != nil {
		if errors.Is(err, event.ErrNotFound) {
			writeError(w, http.StatusNotFound, "not_found", "event was not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "internal_error", "could not load event")
		return
	}
	markets, _, err := h.markets.List(
		r.Context(),
		&market.ListFilters{EventID: eventID, Page: 1, Limit: 100},
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "could not load event markets")
		return
	}
	related := make([]eventMarketView, 0, len(markets))
	for _, item := range markets {
		related = append(related, eventMarketView{
			ID:          item.ID,
			Question:    item.Question,
			Status:      item.Status,
			TotalVolume: item.TotalVolume,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": map[string]any{
		"id":              value.ID,
		"source_type":     value.SourceType,
		"source_id":       value.SourceID,
		"title":           value.Title,
		"description":     value.Description,
		"category":        value.Category,
		"end_time":        value.EndTime,
		"resolution_time": value.ResolutionTime,
		"status":          value.Status,
		"outcome":         value.Outcome,
		"created_at":      value.CreatedAt,
		"updated_at":      value.UpdatedAt,
		"markets":         related,
	}})
}

// createEvent creates a custom event. Console-created events always carry
// source_type "custom" with a generated source_id when the client omits them.
func (h *adminHandler) createEvent(w http.ResponseWriter, r *http.Request) {
	request := event.CreateRequest{}
	if err := decodeJSON(w, r, &request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if request.SourceType == "" {
		request.SourceType = "custom"
	}
	if request.SourceID == "" && request.SourceType == "custom" {
		request.SourceID = customEventSourceID()
	}
	created, err := h.events.Create(r.Context(), &request)
	if err != nil {
		var validationErr *event.ValidationError
		switch {
		case errors.As(err, &validationErr):
			writeError(w, http.StatusBadRequest, "validation_error", validationErr.Error())
		case errors.Is(err, event.ErrAlreadyExists):
			writeError(w, http.StatusConflict, "already_exists", "an event with this source already exists")
		default:
			writeError(w, http.StatusInternalServerError, "internal_error", "could not create event")
		}
		return
	}
	if principal, ok := auth.AdminPrincipalFromContext(r.Context()); ok {
		h.adminAudit(principal, "create.event", "event", created.ID, nil, created, r)
	}
	writeJSON(w, http.StatusCreated, map[string]any{"data": created})
}

// updateEvent edits the editable fields of an event. Resolved events are
// immutable and return 409.
func (h *adminHandler) updateEvent(w http.ResponseWriter, r *http.Request) {
	eventID := r.PathValue("eventID")
	request := event.UpdateRequest{}
	if err := decodeJSON(w, r, &request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	before, err := h.events.Get(r.Context(), eventID)
	if err != nil {
		if errors.Is(err, event.ErrNotFound) {
			writeError(w, http.StatusNotFound, "not_found", "event was not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "internal_error", "could not load event")
		return
	}
	updated, err := h.events.Update(r.Context(), eventID, &request)
	if err != nil {
		var validationErr *event.ValidationError
		switch {
		case errors.As(err, &validationErr):
			writeError(w, http.StatusBadRequest, "validation_error", validationErr.Error())
		case errors.Is(err, event.ErrResolved):
			writeError(w, http.StatusConflict, "already_resolved", "resolved events cannot be edited")
		case errors.Is(err, event.ErrNotFound):
			writeError(w, http.StatusNotFound, "not_found", "event was not found")
		default:
			writeError(w, http.StatusInternalServerError, "internal_error", "could not update event")
		}
		return
	}
	if principal, ok := auth.AdminPrincipalFromContext(r.Context()); ok {
		h.adminAudit(principal, "update.event", "event", eventID, before, updated, r)
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": updated})
}

// updateEventStatus moves an event through its lifecycle: pending -> active
// -> closed. Transitions outside the lifecycle return 409.
func (h *adminHandler) updateEventStatus(w http.ResponseWriter, r *http.Request) {
	eventID := r.PathValue("eventID")
	request := struct {
		Status string `json:"status"`
	}{}
	if err := decodeJSON(w, r, &request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	before, err := h.events.Get(r.Context(), eventID)
	if err != nil {
		if errors.Is(err, event.ErrNotFound) {
			writeError(w, http.StatusNotFound, "not_found", "event was not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "internal_error", "could not load event")
		return
	}
	if err := h.events.UpdateStatus(r.Context(), eventID, request.Status); err != nil {
		var validationErr *event.ValidationError
		switch {
		case errors.As(err, &validationErr):
			writeError(w, http.StatusBadRequest, "validation_error", validationErr.Error())
		case errors.Is(err, event.ErrNotFound):
			writeError(w, http.StatusNotFound, "not_found", "event was not found")
		case errors.Is(err, event.ErrInvalidTransition):
			writeError(w, http.StatusConflict, "invalid_transition", "the requested status change is not allowed")
		default:
			writeError(w, http.StatusInternalServerError, "internal_error", "could not update event status")
		}
		return
	}
	status := request.Status
	after, err := h.events.Get(r.Context(), eventID)
	if err != nil {
		after = nil
	} else {
		status = after.Status
	}
	if principal, ok := auth.AdminPrincipalFromContext(r.Context()); ok {
		h.adminAudit(principal, "status.event", "event", eventID, before, after, r)
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": map[string]any{
		"id":     eventID,
		"status": status,
	}})
}

// resolveEvent resolves a closed event with a final outcome. The request
// body must carry confirm "resolve"; resolution is terminal.
func (h *adminHandler) resolveEvent(w http.ResponseWriter, r *http.Request) {
	eventID := r.PathValue("eventID")
	request := struct {
		Outcome string `json:"outcome"`
		Confirm string `json:"confirm"`
	}{}
	if !readConfirm(w, r, "resolve") {
		writeError(w, http.StatusBadRequest, "confirm_required", `confirm must be "resolve"`)
		return
	}
	if err := decodeJSON(w, r, &request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	before, err := h.events.Get(r.Context(), eventID)
	if err != nil {
		if errors.Is(err, event.ErrNotFound) {
			writeError(w, http.StatusNotFound, "not_found", "event was not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "internal_error", "could not load event")
		return
	}
	if err := h.events.Resolve(r.Context(), eventID, request.Outcome); err != nil {
		var validationErr *event.ValidationError
		switch {
		case errors.As(err, &validationErr):
			writeError(w, http.StatusBadRequest, "validation_error", validationErr.Error())
		case errors.Is(err, event.ErrNotFound):
			writeError(w, http.StatusNotFound, "not_found", "event was not found")
		case errors.Is(err, event.ErrInvalidTransition):
			writeError(w, http.StatusConflict, "invalid_transition", "only closed events can be resolved")
		default:
			writeError(w, http.StatusInternalServerError, "internal_error", "could not resolve event")
		}
		return
	}
	after, err := h.events.Get(r.Context(), eventID)
	if err != nil {
		after = nil
	}
	if principal, ok := auth.AdminPrincipalFromContext(r.Context()); ok {
		h.adminAudit(principal, "resolve.event", "event", eventID, before, after, r)
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": map[string]any{
		"id":      eventID,
		"status":  "resolved",
		"outcome": request.Outcome,
	}})
}

// customEventSourceID generates a unique source identifier for console-created
// events. crypto/rand.Read never fails on supported platforms.
func customEventSourceID() string {
	buffer := make([]byte, 8)
	_, _ = rand.Read(buffer)
	return "custom-" + hex.EncodeToString(buffer)
}

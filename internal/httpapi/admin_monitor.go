package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/afun-game/predictmarket-saas/internal/auth"
	"github.com/afun-game/predictmarket-saas/internal/market"
	"github.com/afun-game/predictmarket-saas/internal/settlement"
)

// listMarkets returns the paginated market list with console filters.
func (h *adminHandler) listMarkets(w http.ResponseWriter, r *http.Request) {
	page, limit, err := queryPage(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	query := r.URL.Query()
	items, total, err := h.config.Queries.ListMarkets(
		r.Context(),
		query.Get("merchant_id"),
		query.Get("event_id"),
		query.Get("status"),
		query.Get("q"),
		page,
		limit,
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "could not list markets")
		return
	}
	adminList(w, items, total)
}

// getMarket returns one market with its event title and order book.
func (h *adminHandler) getMarket(w http.ResponseWriter, r *http.Request) {
	value, err := h.markets.Get(r.Context(), r.PathValue("marketID"))
	if err != nil {
		writeMarketServiceError(w, err)
		return
	}
	orderbook, err := h.markets.GetOrderBook(r.Context(), value.ID)
	if err != nil {
		writeMarketServiceError(w, err)
		return
	}
	// The event title is cosmetic; a missing event never fails the request.
	title := ""
	if eventValue, err := h.events.Get(r.Context(), value.EventID); err == nil {
		title = eventValue.Title
	}
	payload := map[string]any{}
	if raw, err := json.Marshal(value); err == nil {
		_ = json.Unmarshal(raw, &payload)
	}
	payload["event_title"] = title
	payload["orderbook"] = orderbook
	writeJSON(w, http.StatusOK, map[string]any{"data": payload})
}

// createMarket creates a market from the admin console.
func (h *adminHandler) createMarket(w http.ResponseWriter, r *http.Request) {
	var request market.CreateRequest
	if err := decodeJSON(w, r, &request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	created, err := h.markets.Create(r.Context(), &request)
	if err != nil {
		writeMarketServiceError(w, err)
		return
	}
	if principal, ok := auth.AdminPrincipalFromContext(r.Context()); ok {
		h.adminAudit(principal, "create.market", "market", created.ID, nil, created, r)
	}
	writeJSON(w, http.StatusCreated, map[string]any{"data": created})
}

// updateMarketStatus transitions a market between console-managed statuses.
func (h *adminHandler) updateMarketStatus(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Status string `json:"status"`
	}
	if err := decodeJSON(w, r, &request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	marketID := r.PathValue("marketID")
	before, err := h.markets.Get(r.Context(), marketID)
	if err != nil {
		writeMarketServiceError(w, err)
		return
	}
	status := strings.ToLower(strings.TrimSpace(request.Status))
	if err := h.markets.UpdateStatus(r.Context(), marketID, status); err != nil {
		writeMarketServiceError(w, err)
		return
	}
	if principal, ok := auth.AdminPrincipalFromContext(r.Context()); ok {
		h.adminAudit(principal, "status.market", "market", marketID,
			map[string]any{"status": before.Status},
			map[string]any{"status": status},
			r,
		)
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": map[string]any{
		"id": marketID, "status": status,
	}})
}

// addMarketLiquidity tops up a market's liquidity pool after confirmation.
func (h *adminHandler) addMarketLiquidity(w http.ResponseWriter, r *http.Request) {
	if !readConfirm(w, r, "liquidity") {
		writeError(w, http.StatusBadRequest, "validation_error", "confirmation is required")
		return
	}
	// readConfirm restores the body, so the decode target must tolerate the
	// confirm field it carries alongside the payload.
	var request struct {
		Amount  float64 `json:"amount"`
		Confirm string  `json:"confirm"`
	}
	if err := decodeJSON(w, r, &request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	marketID := r.PathValue("marketID")
	before, err := h.markets.Get(r.Context(), marketID)
	if err != nil {
		writeMarketServiceError(w, err)
		return
	}
	if err := h.markets.AddLiquidity(r.Context(), marketID, request.Amount); err != nil {
		writeMarketServiceError(w, err)
		return
	}
	after, err := h.markets.Get(r.Context(), marketID)
	if err != nil {
		writeMarketServiceError(w, err)
		return
	}
	if principal, ok := auth.AdminPrincipalFromContext(r.Context()); ok {
		h.adminAudit(principal, "liquidity.market", "market", marketID,
			map[string]any{"liquidity_pool": before.LiquidityPool},
			map[string]any{"liquidity_pool": after.LiquidityPool},
			r,
		)
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": map[string]any{
		"id": marketID, "liquidity_pool": after.LiquidityPool,
	}})
}

// voidMarket refunds every order on an unsettled market and voids it.
func (h *adminHandler) voidMarket(w http.ResponseWriter, r *http.Request) {
	if h.config.Settlement == nil {
		writeError(w, http.StatusServiceUnavailable, "service_unavailable", "settlement service is not available")
		return
	}
	if !readConfirm(w, r, "void") {
		writeError(w, http.StatusBadRequest, "validation_error", "confirmation is required")
		return
	}
	marketID := r.PathValue("marketID")
	if err := h.config.Settlement.VoidMarket(r.Context(), marketID); err != nil {
		switch {
		case errors.Is(err, settlement.ErrMarketNotFound):
			writeError(w, http.StatusNotFound, "not_found", "market was not found")
		case errors.Is(err, settlement.ErrMarketAlreadySettled):
			writeError(w, http.StatusConflict, "already_settled", "market has already been settled or voided")
		default:
			writeError(w, http.StatusInternalServerError, "internal_error", "could not void market")
		}
		return
	}
	if principal, ok := auth.AdminPrincipalFromContext(r.Context()); ok {
		h.adminAudit(principal, "void.market", "market", marketID, nil,
			map[string]any{"id": marketID, "status": "voided"},
			r,
		)
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": map[string]any{
		"id": marketID, "status": "voided",
	}})
}

// listOrders returns the paginated order list with console filters.
func (h *adminHandler) listOrders(w http.ResponseWriter, r *http.Request) {
	page, limit, err := queryPage(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	query := r.URL.Query()
	items, total, err := h.config.Queries.ListOrders(
		r.Context(),
		query.Get("merchant_id"),
		query.Get("user_id"),
		query.Get("market_id"),
		query.Get("status"),
		page,
		limit,
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "could not list orders")
		return
	}
	adminList(w, items, total)
}

// listTransactions returns the paginated wallet transaction list with filters.
func (h *adminHandler) listTransactions(w http.ResponseWriter, r *http.Request) {
	page, limit, err := queryPage(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	query := r.URL.Query()
	items, total, err := h.config.Queries.ListTransactions(
		r.Context(),
		query.Get("merchant_id"),
		query.Get("user_id"),
		query.Get("type"),
		page,
		limit,
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "could not list transactions")
		return
	}
	adminList(w, items, total)
}

// listAuditLogs returns the paginated administrator action trail.
func (h *adminHandler) listAuditLogs(w http.ResponseWriter, r *http.Request) {
	page, limit, err := queryPage(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	items, total, err := h.config.Queries.ListAuditLogs(r.Context(), page, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "could not list audit logs")
		return
	}
	adminList(w, items, total)
}

// overview returns the dashboard aggregate snapshot.
func (h *adminHandler) overview(w http.ResponseWriter, r *http.Request) {
	value, err := h.config.Queries.GetOverview(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "could not load overview")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": value})
}

package httpapi

import (
	"context"
	"errors"
	"net/http"

	"github.com/afun-game/predictmarket-saas/internal/auth"
	"github.com/afun-game/predictmarket-saas/internal/market"
	"github.com/afun-game/predictmarket-saas/internal/merchant"
	"github.com/afun-game/predictmarket-saas/internal/order"
	"github.com/afun-game/predictmarket-saas/internal/v2query"
	"github.com/afun-game/predictmarket-saas/internal/wallet"
	"github.com/afun-game/predictmarket-saas/pkg/types"
	"github.com/nxsky/twill/runtime/middleware"
)

const (
	defaultOrderPage  = 1
	defaultOrderLimit = 20
)

type orderHandler struct {
	service order.Service
	// Optional market-title lookup shared with the hosted market endpoints;
	// when nil order lists keep their classic field set.
	queries v2query.Service
}

type orderCreateRequest struct {
	UserID      string  `json:"user_id"`
	MarketID    string  `json:"market_id"`
	Type        string  `json:"type"`
	Option      string  `json:"option"`
	Amount      float64 `json:"amount"`
	Currency    string  `json:"currency"`
	Price       float64 `json:"price"`
	TimeInForce string  `json:"time_in_force,omitempty"`
}

func registerOrderRoutes(
	mux *http.ServeMux,
	merchantService merchant.Service,
	orderService order.Service,
	queries v2query.Service,
) {
	handler := &orderHandler{service: orderService, queries: queries}
	mux.Handle(
		"POST /api/v1/orders",
		auth.RequireMerchant(
			merchantService,
			middleware.RequireIdempotencyKey(http.MethodPost)(http.HandlerFunc(handler.create)),
		),
	)
	mux.Handle(
		"GET /api/v1/orders",
		auth.RequireMerchant(merchantService, http.HandlerFunc(handler.list)),
	)
	mux.Handle(
		"GET /api/v1/orders/{orderID}",
		auth.RequireMerchant(merchantService, http.HandlerFunc(handler.get)),
	)
	mux.Handle(
		"DELETE /api/v1/orders/{orderID}",
		auth.RequireMerchant(merchantService, http.HandlerFunc(handler.cancel)),
	)
}

// orderListWithTitles attaches each order's market context when the
// enrichment service is available; otherwise the list keeps its classic
// shape.
func (h *orderHandler) orderListWithTitles(ctx context.Context, orders []*types.Order) []orderWithMarketTitle {
	ctxData := enrichOrders(ctx, h.queries, orders, nil)
	items := make([]orderWithMarketTitle, 0, len(orders))
	for _, order := range orders {
		items = append(items, orderViewWithContext(order, ctxData))
	}
	return items
}

func (h *orderHandler) create(w http.ResponseWriter, r *http.Request) {
	merchantValue, ok := authenticatedMerchant(w, r)
	if !ok {
		return
	}
	var request orderCreateRequest
	if err := decodeJSON(w, r, &request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	created, err := h.service.Create(r.Context(), &order.CreateRequest{
		MerchantID:     merchantValue.ID,
		UserID:         request.UserID,
		MarketID:       request.MarketID,
		Type:           request.Type,
		Option:         request.Option,
		Amount:         request.Amount,
		Currency:       request.Currency,
		Price:          request.Price,
		TimeInForce:    request.TimeInForce,
		IdempotencyKey: r.Header.Get(middleware.IdempotencyKeyHeader),
	})
	if err != nil {
		writeOrderServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"data": created})
}

func (h *orderHandler) list(w http.ResponseWriter, r *http.Request) {
	merchantValue, ok := authenticatedMerchant(w, r)
	if !ok {
		return
	}
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
	filters := &order.ListFilters{
		MerchantID: merchantValue.ID,
		UserID:     r.URL.Query().Get("user_id"),
		MarketID:   r.URL.Query().Get("market_id"),
		Status:     r.URL.Query().Get("status"),
		Page:       page,
		Limit:      limit,
	}
	if r.URL.Query().Has("cursor") {
		filters.Cursor = r.URL.Query().Get("cursor")
		cursorPage, err := h.service.ListCursor(r.Context(), filters)
		if err != nil {
			writeOrderServiceError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"data": h.orderListWithTitles(r.Context(), cursorPage.Orders),
			"meta": map[string]any{"next_cursor": cursorPage.NextCursor},
		})
		return
	}
	values, total, err := h.service.List(r.Context(), filters)
	if err != nil {
		writeOrderServiceError(w, err)
		return
	}
	page, limit = orderPageDefaults(page, limit)
	writeJSON(w, http.StatusOK, map[string]any{
		"data": h.orderListWithTitles(r.Context(), values),
		"meta": map[string]any{"pagination": newPagination(page, limit, total)},
	})
}

func (h *orderHandler) get(w http.ResponseWriter, r *http.Request) {
	value, ok := h.getAuthorizedOrder(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": value})
}

func (h *orderHandler) cancel(w http.ResponseWriter, r *http.Request) {
	value, ok := h.getAuthorizedOrder(w, r)
	if !ok {
		return
	}
	if err := h.service.Cancel(r.Context(), value.ID); err != nil {
		writeOrderServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"data": map[string]string{"id": value.ID, "status": "cancelled"},
	})
}

func (h *orderHandler) getAuthorizedOrder(
	w http.ResponseWriter,
	r *http.Request,
) (*types.Order, bool) {
	merchantValue, ok := authenticatedMerchant(w, r)
	if !ok {
		return nil, false
	}
	value, err := h.service.Get(r.Context(), r.PathValue("orderID"))
	if err != nil {
		writeOrderServiceError(w, err)
		return nil, false
	}
	if value.MerchantID != merchantValue.ID {
		writeError(w, http.StatusNotFound, "not_found", "order was not found")
		return nil, false
	}
	return value, true
}

func orderPageDefaults(page, limit int) (int, int) {
	if page == 0 {
		page = defaultOrderPage
	}
	if limit == 0 {
		limit = defaultOrderLimit
	}
	return page, limit
}

func writeOrderServiceError(w http.ResponseWriter, err error) {
	var validationErr *order.ValidationError
	switch {
	case errors.As(err, &validationErr):
		writeError(w, http.StatusBadRequest, "validation_error", validationErr.Error())
	case errors.Is(err, order.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", "order was not found")
	case errors.Is(err, order.ErrInvalidMarket), errors.Is(err, market.ErrNotFound):
		writeError(w, http.StatusUnprocessableEntity, "invalid_market", "market must exist, be active, and belong to the merchant")
	case errors.Is(err, order.ErrNotCancellable):
		writeError(w, http.StatusConflict, "not_cancellable", "order cannot be cancelled")
	case errors.Is(err, wallet.ErrNotFound):
		writeError(w, http.StatusConflict, "wallet_not_funded", "wallet must be funded before placing an order")
	case errors.Is(err, wallet.ErrInsufficientBalance):
		writeError(w, http.StatusConflict, "insufficient_balance", "available balance is insufficient")
	default:
		writeError(w, http.StatusInternalServerError, "internal_error", "an internal error occurred")
	}
}

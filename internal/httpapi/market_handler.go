package httpapi

import (
	"errors"
	"net/http"
	"time"

	"github.com/afun-game/predictmarket-saas/internal/auth"
	"github.com/afun-game/predictmarket-saas/internal/market"
	"github.com/afun-game/predictmarket-saas/internal/merchant"
	"github.com/afun-game/predictmarket-saas/internal/order"
	"github.com/afun-game/predictmarket-saas/internal/parimutuel"
	"github.com/afun-game/predictmarket-saas/internal/v2query"
	"github.com/afun-game/predictmarket-saas/pkg/types"
)

const (
	defaultMarketPage  = 1
	defaultMarketLimit = 20
)

type marketHandler struct {
	service      market.Service
	orderService order.Service
	// Optional enrichment services shared with the hosted market endpoints;
	// when nil the v1 responses keep their classic field set.
	parimutuelService parimutuel.Service
	queries           v2query.Service
}

// v1MarketView is the merchant market payload: the classic market fields
// plus the event context and行情 summaries shared with the hosted endpoints.
type v1MarketView struct {
	*types.Market
	ResolutionTime   time.Time              `json:"resolution_time,omitempty"`
	EventTitle       string                 `json:"event_title,omitempty"`
	EventDescription string                 `json:"event_description,omitempty"`
	League           string                 `json:"league,omitempty"`
	History          *v2query.MarketHistory `json:"history,omitempty"`
	Pool             map[string]any         `json:"pool,omitempty"`
	Book             []v2query.BookQuote    `json:"book,omitempty"`
}

func v1MarketViewFrom(
	value *types.Market,
	pool map[string]any,
	book []v2query.BookQuote,
	event v2query.MarketEventInfo,
	history *v2query.MarketHistory,
) v1MarketView {
	view := v1MarketView{
		Market:  value,
		History: history,
		Pool:    pool,
		Book:    book,
	}
	if value.ResolutionTime != nil {
		view.ResolutionTime = *value.ResolutionTime
	} else if event.Title != "" {
		view.ResolutionTime = event.ResolutionTime
	}
	if event.Title != "" {
		view.EventTitle = event.Title
		view.EventDescription = event.Description
		view.League = event.League
	}
	return view
}

type marketStatusRequest struct {
	Status string `json:"status"`
}

type marketLiquidityRequest struct {
	Amount float64 `json:"amount"`
}

func registerMarketRoutes(
	mux *http.ServeMux,
	merchantService merchant.Service,
	marketService market.Service,
	orderService order.Service,
	adminAPIKey string,
	parimutuelService parimutuel.Service,
	queries v2query.Service,
) {
	handler := &marketHandler{
		service:           marketService,
		orderService:      orderService,
		parimutuelService: parimutuelService,
		queries:           queries,
	}
	mux.Handle(
		"POST /api/v1/markets",
		auth.RequireAdmin(adminAPIKey, http.HandlerFunc(handler.create)),
	)
	mux.Handle(
		"GET /api/v1/markets",
		auth.RequireMerchant(merchantService, http.HandlerFunc(handler.list)),
	)
	mux.Handle(
		"GET /api/v1/markets/{marketID}",
		auth.RequireMerchant(merchantService, http.HandlerFunc(handler.get)),
	)
	mux.Handle(
		"GET /api/v1/markets/{marketID}/orderbook",
		auth.RequireMerchant(merchantService, http.HandlerFunc(handler.getOrderBook)),
	)
	mux.Handle(
		"PATCH /api/v1/markets/{marketID}/status",
		auth.RequireAdmin(adminAPIKey, http.HandlerFunc(handler.updateStatus)),
	)
	mux.Handle(
		"POST /api/v1/markets/{marketID}/liquidity",
		auth.RequireAdmin(adminAPIKey, http.HandlerFunc(handler.addLiquidity)),
	)
}

func (h *marketHandler) create(w http.ResponseWriter, r *http.Request) {
	var request market.CreateRequest
	if err := decodeJSON(w, r, &request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	created, err := h.service.Create(r.Context(), &request)
	if err != nil {
		writeMarketServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"data": created})
}

func (h *marketHandler) list(w http.ResponseWriter, r *http.Request) {
	authenticated, ok := auth.MerchantFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "a valid API key is required")
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
	filters := &market.ListFilters{
		MerchantID: authenticated.ID,
		EventID:    r.URL.Query().Get("event_id"),
		Category:   r.URL.Query().Get("category"),
		Status:     r.URL.Query().Get("status"),
		Sort:       r.URL.Query().Get("sort"),
		Page:       page,
		Limit:      limit,
	}
	values, total, err := h.service.List(r.Context(), filters)
	if err != nil {
		writeMarketServiceError(w, err)
		return
	}

	page, limit = marketPageDefaults(page, limit)
	result := make([]v1MarketView, 0, len(values))
	if len(values) > 0 {
		pools, book, events, history := marketSummaries(r.Context(), h.parimutuelService, h.queries, values)
		for _, value := range values {
			result = append(result, v1MarketViewFrom(value, pools[value.ID], book[value.ID], events[value.ID], history[value.ID]))
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"data": result,
		"meta": map[string]any{
			"pagination": newPagination(page, limit, total),
		},
	})
}

func (h *marketHandler) get(w http.ResponseWriter, r *http.Request) {
	value, ok := h.getAuthorizedMarket(w, r)
	if !ok {
		return
	}
	pools, book, events, history := marketSummaries(r.Context(), h.parimutuelService, h.queries, []*types.Market{value})
	writeJSON(w, http.StatusOK, map[string]any{"data": v1MarketViewFrom(value, pools[value.ID], book[value.ID], events[value.ID], history[value.ID])})
}

func (h *marketHandler) getOrderBook(w http.ResponseWriter, r *http.Request) {
	value, ok := h.getAuthorizedMarket(w, r)
	if !ok {
		return
	}
	book, err := h.orderService.GetOrderBook(r.Context(), value.ID)
	if err != nil {
		writeMarketServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": book})
}

func (h *marketHandler) updateStatus(w http.ResponseWriter, r *http.Request) {
	var request marketStatusRequest
	if err := decodeJSON(w, r, &request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if err := h.service.UpdateStatus(
		r.Context(),
		r.PathValue("marketID"),
		request.Status,
	); err != nil {
		writeMarketServiceError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *marketHandler) addLiquidity(w http.ResponseWriter, r *http.Request) {
	var request marketLiquidityRequest
	if err := decodeJSON(w, r, &request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if err := h.service.AddLiquidity(
		r.Context(),
		r.PathValue("marketID"),
		request.Amount,
	); err != nil {
		writeMarketServiceError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *marketHandler) getAuthorizedMarket(
	w http.ResponseWriter,
	r *http.Request,
) (*types.Market, bool) {
	authenticated, ok := auth.MerchantFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "a valid API key is required")
		return nil, false
	}
	value, err := h.service.Get(r.Context(), r.PathValue("marketID"))
	if err != nil {
		writeMarketServiceError(w, err)
		return nil, false
	}
	if value.MerchantID != authenticated.ID {
		writeError(w, http.StatusNotFound, "not_found", "market was not found")
		return nil, false
	}
	return value, true
}

func marketPageDefaults(page, limit int) (int, int) {
	if page == 0 {
		page = defaultMarketPage
	}
	if limit == 0 {
		limit = defaultMarketLimit
	}
	return page, limit
}

func writeMarketServiceError(w http.ResponseWriter, err error) {
	var validationErr *market.ValidationError
	switch {
	case errors.As(err, &validationErr):
		writeError(w, http.StatusBadRequest, "validation_error", validationErr.Error())
	case errors.Is(err, market.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", "market was not found")
	case errors.Is(err, market.ErrInvalidReference):
		writeError(
			w,
			http.StatusUnprocessableEntity,
			"invalid_reference",
			"merchant and event must exist and be active",
		)
	case errors.Is(err, market.ErrEventExpired):
		writeError(
			w,
			http.StatusUnprocessableEntity,
			"event_expired",
			"event resolution time has already passed; a market cannot be created on an expired event",
		)
	case errors.Is(err, market.ErrInvalidTransition):
		writeError(w, http.StatusConflict, "invalid_transition", "market operation is not allowed")
	default:
		writeError(w, http.StatusInternalServerError, "internal_error", "an internal error occurred")
	}
}

package httpapi

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/afun-game/predictmarket-saas/internal/audit"
	"github.com/afun-game/predictmarket-saas/internal/ratelimit"

	"github.com/afun-game/predictmarket-saas/internal/auth"
	"github.com/afun-game/predictmarket-saas/internal/callback"
	"github.com/afun-game/predictmarket-saas/internal/event"
	"github.com/afun-game/predictmarket-saas/internal/market"
	"github.com/afun-game/predictmarket-saas/internal/order"
	"github.com/afun-game/predictmarket-saas/internal/platformuser"
	"github.com/afun-game/predictmarket-saas/internal/session"
	"github.com/afun-game/predictmarket-saas/internal/v2query"
	"github.com/afun-game/predictmarket-saas/internal/wallet"
	"github.com/afun-game/predictmarket-saas/pkg/fixed"
	"github.com/afun-game/predictmarket-saas/pkg/types"
	"github.com/nxsky/twill/runtime/middleware"
)

const (
	defaultUserMarketLimit = 20
	maxUserMarketLimit     = 100

	merchantOrderPool   = "merchant:order"
	merchantQueryPool   = "merchant:query"
	userSessionPool     = "user:session"
	requestAuditTimeout = 2 * time.Second
)

// V3Config contains the infrastructure required for the V3 Launch and hosted
// read-only API. The V3 routes are not registered unless all fields are set.
type V3Config struct {
	Authenticator   auth.SignedMerchantValidator
	Sessions        *session.Manager
	PlatformUsers   platformuser.Repository
	Queries         v2query.Service
	Callbacks       callback.Service
	Seamless        *callback.SeamlessCoordinator
	HostedLaunchURL string
	// Audit records state-changing merchant requests (V3 §7.3).
	Audit audit.Store
	// MerchantOrderLimiter limits state-changing merchant API calls per key.
	MerchantOrderLimiter ratelimit.Limiter
	// MerchantQueryLimiter limits merchant query calls per key.
	MerchantQueryLimiter ratelimit.Limiter
	// UserSessionLimiter limits hosted /api/user/* calls per browser session.
	UserSessionLimiter ratelimit.Limiter
}

type v3Handler struct {
	sessions      *session.Manager
	platformUsers platformuser.Repository
	events        event.Service
	markets       market.Service
	orders        order.Service
	wallets       wallet.Service
	queries       v2query.Service
	callbacks     callback.Service
	seamless      *callback.SeamlessCoordinator
	launchURL     *url.URL
	audit         audit.Store
	orderLimiter  ratelimit.Limiter
	queryLimiter  ratelimit.Limiter
	userLimiter   ratelimit.Limiter
}

type v3SessionCreateRequest struct {
	UserID    string            `json:"user_id"`
	Currency  string            `json:"currency"`
	Balance   string            `json:"balance,omitempty"`
	Locale    string            `json:"locale"`
	ReturnURL string            `json:"return_url,omitempty"`
	IP        string            `json:"ip,omitempty"`
	Meta      map[string]string `json:"meta,omitempty"`
}

type v3SessionExchangeRequest struct {
	Token string `json:"token"`
}

type v3TransferRequest struct {
	MerchantTransactionID string `json:"merchant_txn_id"`
	Currency              string `json:"currency"`
	Amount                string `json:"amount"`
}

type v3TransferResponse struct {
	ID                    string    `json:"id"`
	MerchantTransactionID string    `json:"merchant_txn_id"`
	UserID                string    `json:"user_id"`
	Currency              string    `json:"currency"`
	Amount                string    `json:"amount"`
	Direction             string    `json:"direction"`
	Status                string    `json:"status"`
	TransactionID         string    `json:"transaction_id,omitempty"`
	CreatedAt             time.Time `json:"created_at"`
	UpdatedAt             time.Time `json:"updated_at"`
}

type v3UserMarket struct {
	ID          string     `json:"id"`
	EventID     string     `json:"event_id"`
	Type        string     `json:"type"`
	Question    string     `json:"question"`
	Options     []string   `json:"options"`
	Status      string     `json:"status"`
	TotalVolume string     `json:"total_volume"`
	CreatedAt   time.Time  `json:"created_at"`
	SettledAt   *time.Time `json:"settled_at,omitempty"`
}

type v3UserEvent struct {
	ID             string    `json:"id"`
	Title          string    `json:"title"`
	Description    string    `json:"description"`
	Category       string    `json:"category"`
	EndTime        time.Time `json:"end_time"`
	ResolutionTime time.Time `json:"resolution_time"`
	Status         string    `json:"status"`
	Outcome        *string   `json:"outcome,omitempty"`
}

func registerV3Routes(
	mux *http.ServeMux,
	config V3Config,
	eventService event.Service,
	marketService market.Service,
	orderService order.Service,
	walletService wallet.Service,
) {
	if config.Authenticator == nil || config.Sessions == nil || config.PlatformUsers == nil || eventService == nil {
		return
	}
	launchURL, err := validHostedLaunchURL(config.HostedLaunchURL)
	if err != nil {
		return
	}
	handler := &v3Handler{
		sessions:      config.Sessions,
		platformUsers: config.PlatformUsers,
		events:        eventService,
		markets:       marketService,
		orders:        orderService,
		wallets:       walletService,
		queries:       config.Queries,
		callbacks:     config.Callbacks,
		seamless:      config.Seamless,
		launchURL:     launchURL,
		audit:         config.Audit,
		orderLimiter:  config.MerchantOrderLimiter,
		queryLimiter:  config.MerchantQueryLimiter,
		userLimiter:   config.UserSessionLimiter,
	}
	policy := func(pool string, next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !handler.enforceMerchantPolicy(w, r, pool) {
				return
			}
			next.ServeHTTP(w, r)
		})
	}
	audited := func(next http.Handler) http.Handler {
		return handler.auditChange(next)
	}
	signed := func(next http.Handler) http.Handler {
		return auth.RequireSignedMerchant(config.Authenticator, config.Sessions, policy(merchantOrderPool, next))
	}
	transferSigned := func(next http.Handler) http.Handler {
		return auth.RequireSignedMerchantWithoutReplay(config.Authenticator, policy(merchantOrderPool, next))
	}
	v2Signed := func(pool string, next http.Handler) http.Handler {
		return auth.RequireSignedMerchantWithoutReplay(config.Authenticator, policy(pool, next))
	}
	userSession := func(next http.Handler) http.Handler {
		return auth.RequireUserSession(config.Sessions, handler.sessionLimited(next))
	}
	mux.Handle("POST /api/v2/sessions", audited(signed(http.HandlerFunc(handler.createSession))))
	mux.Handle("GET /api/v2/sessions/{sessionID}", signed(http.HandlerFunc(handler.getSession)))
	mux.Handle("DELETE /api/v2/sessions/{sessionID}", audited(signed(http.HandlerFunc(handler.revokeSession))))
	mux.Handle(
		"POST /api/v2/users/{userID}/deposits",
		audited(transferSigned(middleware.RequireIdempotencyKey(http.MethodPost)(http.HandlerFunc(handler.deposit)))),
	)
	mux.Handle(
		"POST /api/v2/users/{userID}/withdrawals",
		audited(transferSigned(middleware.RequireIdempotencyKey(http.MethodPost)(http.HandlerFunc(handler.withdraw)))),
	)
	mux.Handle("GET /api/v2/transfers/{merchantTransactionID}", transferSigned(http.HandlerFunc(handler.getTransfer)))
	mux.Handle("GET /api/v2/users/{userID}/balance", transferSigned(http.HandlerFunc(handler.getTransferBalance)))
	mux.Handle(
		"POST /api/v2/orders",
		audited(v2Signed(merchantOrderPool, middleware.RequireIdempotencyKey(http.MethodPost)(http.HandlerFunc(handler.createV2Order)))),
	)
	mux.Handle("GET /api/v2/orders", v2Signed(merchantQueryPool, http.HandlerFunc(handler.listV2Orders)))
	mux.Handle("GET /api/v2/orders/{orderID}", v2Signed(merchantQueryPool, http.HandlerFunc(handler.getV2Order)))
	mux.Handle(
		"DELETE /api/v2/orders/{orderID}",
		audited(v2Signed(merchantOrderPool, middleware.RequireIdempotencyKey(http.MethodDelete)(http.HandlerFunc(handler.cancelV2Order)))),
	)
	mux.Handle("GET /api/v2/trades", v2Signed(merchantQueryPool, http.HandlerFunc(handler.listV2Trades)))
	mux.Handle("GET /api/v2/transactions", v2Signed(merchantQueryPool, http.HandlerFunc(handler.listV2Transactions)))
	mux.Handle("GET /api/v2/settlements", v2Signed(merchantQueryPool, http.HandlerFunc(handler.listV2Settlements)))
	mux.Handle(
		"GET /api/v2/settlements/{marketID}/payouts",
		v2Signed(merchantQueryPool, http.HandlerFunc(handler.listV2Payouts)),
	)
	mux.Handle("GET /api/v2/reports/daily", v2Signed(merchantQueryPool, http.HandlerFunc(handler.dailyV2Report)))
	mux.Handle(
		"GET /api/v2/callbacks/{transactionID}",
		v2Signed(merchantQueryPool, http.HandlerFunc(handler.getCallbackTransaction)),
	)
	mux.Handle("POST /api/user/session/exchange", http.HandlerFunc(handler.exchangeSession))
	mux.Handle("POST /api/user/session/refresh", userSession(http.HandlerFunc(handler.refreshSession)))
	mux.Handle("GET /api/user/me", userSession(http.HandlerFunc(handler.me)))
	mux.Handle("GET /api/user/events", userSession(http.HandlerFunc(handler.listEvents)))
	mux.Handle("GET /api/user/events/{eventID}", userSession(http.HandlerFunc(handler.getEvent)))
	mux.Handle("GET /api/user/markets", userSession(http.HandlerFunc(handler.listMarkets)))
	mux.Handle("GET /api/user/markets/{marketID}", userSession(http.HandlerFunc(handler.getMarket)))
	mux.Handle("GET /api/user/markets/{marketID}/orderbook", userSession(http.HandlerFunc(handler.getOrderBook)))
	mux.Handle(
		"POST /api/user/orders",
		userSession(middleware.RequireIdempotencyKey(http.MethodPost)(http.HandlerFunc(handler.createUserOrder))),
	)
	mux.Handle("GET /api/user/orders", userSession(http.HandlerFunc(handler.listUserOrders)))
	mux.Handle(
		"GET /api/user/orders/{orderID}/trades",
		userSession(http.HandlerFunc(handler.listUserOrderTrades)),
	)
	mux.Handle(
		"DELETE /api/user/orders/{orderID}",
		userSession(middleware.RequireIdempotencyKey(http.MethodDelete)(http.HandlerFunc(handler.cancelUserOrder))),
	)
}

func (h *v3Handler) createV2Order(w http.ResponseWriter, r *http.Request) {
	merchantValue, ok := authenticatedMerchant(w, r)
	if !ok {
		return
	}
	request := orderCreateRequest{}
	if err := decodeJSON(w, r, &request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if !h.requireActivePlatformUser(w, r, merchantValue.ID, request.UserID) {
		return
	}
	h.createOrderForMode(w, r, merchantValue, request, "api")
}

func (h *v3Handler) listV2Orders(w http.ResponseWriter, r *http.Request) {
	merchantValue, ok := authenticatedMerchant(w, r)
	if !ok {
		return
	}
	filters, ok := v2OrderFilters(w, r, merchantValue.ID, r.URL.Query().Get("user_id"))
	if !ok {
		return
	}
	page, err := h.orders.ListCursor(r.Context(), filters)
	if err != nil {
		writeOrderServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": page.Orders, "meta": map[string]any{"next_cursor": page.NextCursor}})
}

func (h *v3Handler) getV2Order(w http.ResponseWriter, r *http.Request) {
	merchantValue, ok := authenticatedMerchant(w, r)
	if !ok {
		return
	}
	value, ok := h.merchantOrder(w, r, merchantValue.ID)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": value})
}

func (h *v3Handler) cancelV2Order(w http.ResponseWriter, r *http.Request) {
	merchantValue, ok := authenticatedMerchant(w, r)
	if !ok {
		return
	}
	value, ok := h.merchantOrder(w, r, merchantValue.ID)
	if !ok {
		return
	}
	h.cancelOrder(w, r, value)
}

func (h *v3Handler) listV2Trades(w http.ResponseWriter, r *http.Request) {
	merchantValue, ok := authenticatedMerchant(w, r)
	if !ok {
		return
	}
	filters, ok := v2TradeFilters(w, r, merchantValue.ID, "")
	if !ok {
		return
	}
	page, err := h.orders.ListTrades(r.Context(), filters)
	if err != nil {
		writeOrderServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": page.Trades, "meta": map[string]any{"next_cursor": page.NextCursor}})
}

func (h *v3Handler) listV2Transactions(w http.ResponseWriter, r *http.Request) {
	merchantValue, ok := authenticatedMerchant(w, r)
	if !ok {
		return
	}
	if !h.requireV2Queries(w) {
		return
	}
	from, to, limit, ok := v2QueryParameters(w, r)
	if !ok {
		return
	}
	page, err := h.queries.ListTransactions(r.Context(), v2query.TransactionFilters{
		MerchantID: merchantValue.ID,
		UserID:     r.URL.Query().Get("user_id"),
		Type:       r.URL.Query().Get("type"),
		From:       from,
		To:         to,
		Cursor:     r.URL.Query().Get("cursor"),
		Limit:      limit,
	})
	if err != nil {
		writeV2QueryError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": page.Transactions, "meta": map[string]any{"next_cursor": page.NextCursor}})
}

func (h *v3Handler) listV2Settlements(w http.ResponseWriter, r *http.Request) {
	merchantValue, ok := authenticatedMerchant(w, r)
	if !ok {
		return
	}
	if !h.requireV2Queries(w) {
		return
	}
	from, to, limit, ok := v2QueryParameters(w, r)
	if !ok {
		return
	}
	page, err := h.queries.ListSettlements(r.Context(), v2query.SettlementFilters{
		MerchantID: merchantValue.ID,
		From:       from,
		To:         to,
		Cursor:     r.URL.Query().Get("cursor"),
		Limit:      limit,
	})
	if err != nil {
		writeV2QueryError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": page.Settlements, "meta": map[string]any{"next_cursor": page.NextCursor}})
}

func (h *v3Handler) listV2Payouts(w http.ResponseWriter, r *http.Request) {
	merchantValue, ok := authenticatedMerchant(w, r)
	if !ok {
		return
	}
	if !h.requireV2Queries(w) {
		return
	}
	limit, err := queryInt(r, "limit")
	if err != nil {
		writeError(w, http.StatusBadRequest, "validation_error", err.Error())
		return
	}
	page, err := h.queries.ListPayouts(r.Context(), v2query.PayoutFilters{
		MerchantID: merchantValue.ID,
		MarketID:   r.PathValue("marketID"),
		Cursor:     r.URL.Query().Get("cursor"),
		Limit:      limit,
	})
	if err != nil {
		writeV2QueryError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": page.Payouts, "meta": map[string]any{"next_cursor": page.NextCursor}})
}

func (h *v3Handler) dailyV2Report(w http.ResponseWriter, r *http.Request) {
	merchantValue, ok := authenticatedMerchant(w, r)
	if !ok {
		return
	}
	if !h.requireV2Queries(w) {
		return
	}
	date, err := time.Parse("2006-01-02", strings.TrimSpace(r.URL.Query().Get("date")))
	if err != nil {
		writeError(w, http.StatusBadRequest, "validation_error", "date must be an ISO-8601 calendar date")
		return
	}
	currency := strings.TrimSpace(r.URL.Query().Get("currency"))
	if currency == "" {
		currency = merchantValue.Currency
	}
	report, err := h.queries.DailyReport(r.Context(), merchantValue.ID, date, currency)
	if err != nil {
		writeV2QueryError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": report})
}

func (h *v3Handler) requireV2Queries(w http.ResponseWriter) bool {
	if h.queries != nil {
		return true
	}
	writeError(w, http.StatusServiceUnavailable, "v2_queries_unavailable", "V2 query storage is not configured")
	return false
}

func v2QueryParameters(w http.ResponseWriter, r *http.Request) (*time.Time, *time.Time, int, bool) {
	from, err := queryTime(r, "from")
	if err != nil {
		writeError(w, http.StatusBadRequest, "validation_error", err.Error())
		return nil, nil, 0, false
	}
	to, err := queryTime(r, "to")
	if err != nil {
		writeError(w, http.StatusBadRequest, "validation_error", err.Error())
		return nil, nil, 0, false
	}
	limit, err := queryInt(r, "limit")
	if err != nil {
		writeError(w, http.StatusBadRequest, "validation_error", err.Error())
		return nil, nil, 0, false
	}
	return from, to, limit, true
}

type userOrderCreateRequest struct {
	MarketID    string  `json:"market_id"`
	Type        string  `json:"type"`
	Option      string  `json:"option"`
	Amount      float64 `json:"amount"`
	Price       float64 `json:"price"`
	TimeInForce string  `json:"time_in_force,omitempty"`
}

func (h *v3Handler) createUserOrder(w http.ResponseWriter, r *http.Request) {
	browserSession, ok := authenticatedUserSession(w, r)
	if !ok {
		return
	}
	if !h.requireActivePlatformUser(w, r, browserSession.MerchantID, browserSession.UserID) {
		return
	}
	request := userOrderCreateRequest{}
	if err := decodeJSON(w, r, &request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	h.createOrderForMode(w, r, &types.Merchant{
		ID:         browserSession.MerchantID,
		WalletMode: browserSession.WalletMode,
		Currency:   browserSession.Currency,
	}, orderCreateRequest{
		UserID:      browserSession.UserID,
		MarketID:    request.MarketID,
		Type:        request.Type,
		Option:      request.Option,
		Amount:      request.Amount,
		Currency:    browserSession.Currency,
		Price:       request.Price,
		TimeInForce: request.TimeInForce,
	}, "hosted")
}

func (h *v3Handler) listUserOrders(w http.ResponseWriter, r *http.Request) {
	browserSession, ok := authenticatedUserSession(w, r)
	if !ok {
		return
	}
	filters, ok := v2OrderFilters(w, r, browserSession.MerchantID, browserSession.UserID)
	if !ok {
		return
	}
	page, err := h.orders.ListCursor(r.Context(), filters)
	if err != nil {
		writeOrderServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": page.Orders, "meta": map[string]any{"next_cursor": page.NextCursor}})
}

func (h *v3Handler) listUserOrderTrades(w http.ResponseWriter, r *http.Request) {
	browserSession, ok := authenticatedUserSession(w, r)
	if !ok {
		return
	}
	if _, ok := h.userOrder(w, r, browserSession); !ok {
		return
	}
	filters, ok := v2TradeFilters(w, r, browserSession.MerchantID, r.PathValue("orderID"))
	if !ok {
		return
	}
	page, err := h.orders.ListTrades(r.Context(), filters)
	if err != nil {
		writeOrderServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": page.Trades, "meta": map[string]any{"next_cursor": page.NextCursor}})
}

func (h *v3Handler) cancelUserOrder(w http.ResponseWriter, r *http.Request) {
	browserSession, ok := authenticatedUserSession(w, r)
	if !ok {
		return
	}
	if !h.requireActivePlatformUser(w, r, browserSession.MerchantID, browserSession.UserID) {
		return
	}
	value, ok := h.userOrder(w, r, browserSession)
	if !ok {
		return
	}
	h.cancelOrder(w, r, value)
}

func (h *v3Handler) createOrder(w http.ResponseWriter, r *http.Request, merchantValue *types.Merchant, request orderCreateRequest) {
	h.createOrderForMode(w, r, merchantValue, request, "api")
}

func (h *v3Handler) createOrderForMode(
	w http.ResponseWriter,
	r *http.Request,
	merchantValue *types.Merchant,
	request orderCreateRequest,
	channel string,
) {
	createRequest := &order.CreateRequest{
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
		Channel:        channel,
	}
	if merchantValue.WalletMode == "seamless" {
		if h.seamless == nil {
			writeError(w, http.StatusServiceUnavailable, "seamless_unavailable", "seamless order placement is not configured")
			return
		}
		created, balance, err := h.seamless.PlaceWithBalance(r.Context(), createRequest)
		if err != nil {
			writeSeamlessOrderError(w, err, merchantValue.Currency)
			return
		}
		writeJSON(w, http.StatusCreated, map[string]any{
			"data": created,
			"meta": balanceMeta(balance, merchantValue.Currency),
		})
		return
	}
	created, err := h.orders.Create(r.Context(), createRequest)
	if err != nil {
		if errors.Is(err, wallet.ErrInsufficientBalance) {
			if balance, ok := h.currentPlatformBalance(r, merchantValue.ID, request.UserID, request.Currency); ok {
				writeErrorWithBalance(w, http.StatusConflict, "insufficient_balance", "insufficient available balance", balance, request.Currency)
				return
			}
		}
		writeOrderServiceError(w, err)
		return
	}
	response := map[string]any{"data": created}
	if balance, ok := h.currentPlatformBalance(r, merchantValue.ID, request.UserID, request.Currency); ok {
		response["meta"] = balanceMeta(balance, request.Currency)
	}
	writeJSON(w, http.StatusCreated, response)
}

func (h *v3Handler) getCallbackTransaction(w http.ResponseWriter, r *http.Request) {
	merchantValue, ok := authenticatedMerchant(w, r)
	if !ok {
		return
	}
	if h.callbacks == nil {
		writeError(w, http.StatusServiceUnavailable, "callbacks_unavailable", "callback history is not configured")
		return
	}
	value, err := h.callbacks.GetTransaction(r.Context(), merchantValue.ID, r.PathValue("transactionID"))
	if err != nil {
		if errors.Is(err, callback.ErrNotFound) {
			writeError(w, http.StatusNotFound, "not_found", "callback transaction was not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "internal_error", "could not load callback transaction")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": value})
}

func writeSeamlessOrderError(w http.ResponseWriter, err error, currency string) {
	if balance, ok := callback.BalanceFromError(err); ok {
		switch {
		case errors.Is(err, callback.ErrInsufficientFunds):
			writeErrorWithBalance(w, http.StatusPaymentRequired, "insufficient_funds", "merchant wallet reported insufficient funds", balance, currency)
			return
		case errors.Is(err, callback.ErrUserNotFound):
			writeErrorWithBalance(w, http.StatusNotFound, "user_not_found", "merchant wallet reported user not found", balance, currency)
			return
		case errors.Is(err, callback.ErrUserBlocked):
			writeErrorWithBalance(w, http.StatusForbidden, "user_blocked", "merchant wallet reported user blocked", balance, currency)
			return
		}
	}
	switch {
	case errors.Is(err, callback.ErrInsufficientFunds):
		writeError(w, http.StatusPaymentRequired, "insufficient_funds", "merchant wallet reported insufficient funds")
	case errors.Is(err, callback.ErrUserNotFound):
		writeError(w, http.StatusNotFound, "user_not_found", "merchant wallet reported user not found")
	case errors.Is(err, callback.ErrUserBlocked):
		writeError(w, http.StatusForbidden, "user_blocked", "merchant wallet reported user blocked")
	case errors.Is(err, callback.ErrDebitUnknown):
		writeError(w, http.StatusBadGateway, "debit_unknown", "merchant debit outcome is unknown; rollback has been queued")
	case errors.Is(err, callback.ErrSeamlessDisabled):
		writeError(w, http.StatusConflict, "wallet_mode_conflict", "seamless wallet mode is not fully configured")
	case errors.Is(err, callback.ErrMerchantDegraded):
		writeError(w, http.StatusServiceUnavailable, "merchant_wallet_degraded", "merchant wallet is degraded after repeated callback failures")
	case errors.Is(err, callback.ErrCallbackUnverified):
		writeError(w, http.StatusConflict, "callback_unverified", "merchant callback URL must be verified before seamless orders")
	default:
		writeOrderServiceError(w, err)
	}
}

func balanceMeta(balance, currency string) map[string]any {
	return map[string]any{
		"available_balance": balance,
		"currency":          strings.ToUpper(strings.TrimSpace(currency)),
	}
}

func writeErrorWithBalance(w http.ResponseWriter, status int, code, message, balance, currency string) {
	writeJSON(w, status, map[string]any{
		"error": map[string]string{"code": code, "message": message},
		"meta":  balanceMeta(balance, currency),
	})
}

func (h *v3Handler) currentPlatformBalance(r *http.Request, merchantID, userID, currency string) (string, bool) {
	available, _, err := h.wallets.GetBalance(r.Context(), merchantID, userID, currency)
	if err != nil {
		return "", false
	}
	return formatMoney(available), true
}

func (h *v3Handler) enforceMerchantPolicy(w http.ResponseWriter, r *http.Request, pool string) bool {
	merchantValue, ok := authenticatedMerchant(w, r)
	if !ok {
		return false
	}
	if len(merchantValue.AllowedIPs) > 0 && !allowedClientIP(clientIP(r), merchantValue.AllowedIPs) {
		writeError(w, http.StatusForbidden, "ip_not_allowed", "merchant IP address is not in the allowed list")
		return false
	}
	var limiter ratelimit.Limiter
	switch pool {
	case merchantOrderPool:
		limiter = h.orderLimiter
	case merchantQueryPool:
		limiter = h.queryLimiter
	case userSessionPool:
		limiter = h.userLimiter
	}
	if limiter == nil {
		return true
	}
	if err := limiter.Allow(r.Context(), pool+":"+merchantValue.ID); err != nil {
		if errors.Is(err, ratelimit.ErrLimited) {
			writeError(w, http.StatusTooManyRequests, "rate_limited", "rate limit exceeded")
			return false
		}
		// Infrastructure failures must never block trading; log and continue.
		slog.WarnContext(r.Context(), "rate limiter unavailable", "error", err, "pool", pool)
	}
	return true
}

func (h *v3Handler) sessionLimited(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		browserSession, ok := auth.UserSessionFromContext(r.Context())
		if !ok {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if h.userLimiter == nil {
			next.ServeHTTP(w, r)
			return
		}
		if err := h.userLimiter.Allow(r.Context(), userSessionPool+":"+browserSession.ID); err != nil {
			if errors.Is(err, ratelimit.ErrLimited) {
				writeError(w, http.StatusTooManyRequests, "rate_limited", "rate limit exceeded")
				return
			}
			slog.WarnContext(r.Context(), "session rate limiter unavailable", "error", err)
		}
		next.ServeHTTP(w, r)
	})
}

// auditChange records one state-changing merchant request after it completes.
// The write is fire-and-forget so auditing never affects request latency.
func (h *v3Handler) auditChange(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		recorder := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(recorder, r)
		if h.audit == nil || !isChangeMethod(r.Method) {
			return
		}
		merchantValue, ok := auth.MerchantFromContext(r.Context())
		if !ok {
			return
		}
		event := audit.Event{
			MerchantID:     merchantValue.ID,
			Method:         r.Method,
			Path:           r.URL.Path,
			RequestID:      requestIDFromContext(r.Context()),
			IdempotencyKey: r.Header.Get(middleware.IdempotencyKeyHeader),
			ClientIP:       clientIP(r),
			StatusCode:     recorder.status,
		}
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), requestAuditTimeout)
			defer cancel()
			if err := h.audit.Record(ctx, event); err != nil {
				slog.Warn("record merchant API audit failed", "error", err, "merchant_id", event.MerchantID)
			}
		}()
	})
}

// statusRecorder captures the status code written by a handler.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(status int) {
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}

func isChangeMethod(method string) bool {
	switch method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	default:
		return false
	}
}

// clientIP returns the first X-Forwarded-For hop or the remote address.
func clientIP(r *http.Request) string {
	if forwarded := strings.TrimSpace(r.Header.Get("X-Forwarded-For")); forwarded != "" {
		if first, _, ok := strings.Cut(forwarded, ","); ok {
			return strings.TrimSpace(first)
		}
		return strings.TrimSpace(forwarded)
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return strings.TrimSpace(r.RemoteAddr)
	}
	return host
}

func allowedClientIP(ip string, allowed []string) bool {
	value := net.ParseIP(strings.TrimSpace(ip))
	if value == nil {
		return false
	}
	for _, candidate := range allowed {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" {
			continue
		}
		if strings.Contains(candidate, "/") {
			_, network, err := net.ParseCIDR(candidate)
			if err != nil {
				continue
			}
			if network.Contains(value) {
				return true
			}
			continue
		}
		if net.ParseIP(candidate) != nil && net.ParseIP(candidate).Equal(value) {
			return true
		}
	}
	return false
}

func (h *v3Handler) cancelOrder(w http.ResponseWriter, r *http.Request, value *types.Order) {
	if value.Status != "cancelled" {
		if err := h.orders.Cancel(r.Context(), value.ID); err != nil {
			writeOrderServiceError(w, err)
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": map[string]string{"id": value.ID, "status": "cancelled"}})
}

func (h *v3Handler) merchantOrder(w http.ResponseWriter, r *http.Request, merchantID string) (*types.Order, bool) {
	value, err := h.orders.Get(r.Context(), r.PathValue("orderID"))
	if err != nil {
		writeOrderServiceError(w, err)
		return nil, false
	}
	if value.MerchantID != merchantID {
		writeError(w, http.StatusNotFound, "not_found", "order was not found")
		return nil, false
	}
	return value, true
}

func (h *v3Handler) userOrder(w http.ResponseWriter, r *http.Request, browserSession *auth.UserSession) (*types.Order, bool) {
	value, ok := h.merchantOrder(w, r, browserSession.MerchantID)
	if !ok {
		return nil, false
	}
	if value.UserID != browserSession.UserID {
		writeError(w, http.StatusNotFound, "not_found", "order was not found")
		return nil, false
	}
	return value, true
}

func (h *v3Handler) deposit(w http.ResponseWriter, r *http.Request) {
	h.transfer(w, r, "deposit")
}

func (h *v3Handler) withdraw(w http.ResponseWriter, r *http.Request) {
	h.transfer(w, r, "withdrawal")
}

func (h *v3Handler) transfer(w http.ResponseWriter, r *http.Request, direction string) {
	merchantValue, ok := h.transferMerchant(w, r)
	if !ok {
		return
	}
	request := v3TransferRequest{}
	if err := decodeJSON(w, r, &request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	input := &wallet.TransferRequest{
		MerchantID:            merchantValue.ID,
		MerchantTransactionID: request.MerchantTransactionID,
		UserID:                r.PathValue("userID"),
		Currency:              request.Currency,
		Amount:                request.Amount,
	}
	var value *wallet.Transfer
	var err error
	if direction == "deposit" {
		value, err = h.wallets.Deposit(r.Context(), input)
	} else {
		value, err = h.wallets.Withdraw(r.Context(), input)
	}
	if err != nil {
		writeWalletServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"data": transferResponse(value)})
}

func (h *v3Handler) getTransfer(w http.ResponseWriter, r *http.Request) {
	merchantValue, ok := h.transferMerchant(w, r)
	if !ok {
		return
	}
	value, err := h.wallets.GetTransfer(r.Context(), merchantValue.ID, r.PathValue("merchantTransactionID"))
	if err != nil {
		writeWalletServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": transferResponse(value)})
}

func (h *v3Handler) getTransferBalance(w http.ResponseWriter, r *http.Request) {
	merchantValue, ok := h.transferMerchant(w, r)
	if !ok {
		return
	}
	currency := strings.TrimSpace(r.URL.Query().Get("currency"))
	if currency == "" {
		currency = merchantValue.Currency
	}
	available, locked, err := h.wallets.GetBalance(
		r.Context(),
		merchantValue.ID,
		r.PathValue("userID"),
		currency,
	)
	if errors.Is(err, wallet.ErrNotFound) {
		available = 0
		locked = 0
		err = nil
	}
	if err != nil {
		writeWalletServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"data": map[string]any{
			"user_id":           r.PathValue("userID"),
			"currency":          strings.ToUpper(currency),
			"available_balance": formatMoney(available),
			"locked_balance":    formatMoney(locked),
			"total_balance":     formatMoney(available + locked),
		},
	})
}

func (h *v3Handler) transferMerchant(w http.ResponseWriter, r *http.Request) (*types.Merchant, bool) {
	merchantValue, ok := authenticatedMerchant(w, r)
	if !ok {
		return nil, false
	}
	if merchantValue.WalletMode != "transfer" {
		writeError(w, http.StatusConflict, "wallet_mode_conflict", "wallet transfers require transfer wallet mode")
		return nil, false
	}
	return merchantValue, true
}

func (h *v3Handler) createSession(w http.ResponseWriter, r *http.Request) {
	merchantValue, ok := authenticatedMerchant(w, r)
	if !ok {
		return
	}
	request := v3SessionCreateRequest{}
	if err := decodeJSON(w, r, &request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	request, err := normalizeV3SessionRequest(request, merchantValue.Currency)
	if err != nil {
		writeError(w, http.StatusBadRequest, "validation_error", err.Error())
		return
	}
	initialBalance := request.Balance
	if merchantValue.WalletMode == "seamless" {
		initialBalance, err = normalizeBalanceSnapshot(initialBalance)
		if err != nil {
			writeError(w, http.StatusBadRequest, "validation_error", "balance is required in seamless wallet mode and must be a non-negative amount with at most two decimals")
			return
		}
	} else if initialBalance != "" {
		initialBalance, err = normalizeBalanceSnapshot(initialBalance)
		if err != nil {
			writeError(w, http.StatusBadRequest, "validation_error", "balance must be a non-negative amount with at most two decimals")
			return
		}
	}
	if err := h.platformUsers.Upsert(r.Context(), platformuser.User{
		MerchantID:     merchantValue.ID,
		ExternalUserID: request.UserID,
		Locale:         request.Locale,
		Status:         "active",
	}); err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "could not provision platform user")
		return
	}
	// Blocked users must not receive launch tokens. The upsert preserves an
	// existing blocked status, so a fresh read reflects the current policy.
	if !h.requireActivePlatformUser(w, r, merchantValue.ID, request.UserID) {
		return
	}
	launchToken, launch, err := h.sessions.CreateLaunchWithBalance(
		r.Context(),
		merchantValue.ID,
		request.UserID,
		request.Currency,
		merchantValue.WalletMode,
		initialBalance,
		request.Locale,
		request.ReturnURL,
	)
	if err != nil {
		writeSessionError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"data": map[string]any{
			"session_id": launch.ID,
			"launch_url": h.launchURLFor(launchToken),
			"expires_at": launch.ExpiresAt,
		},
	})
}

func (h *v3Handler) getSession(w http.ResponseWriter, r *http.Request) {
	merchantValue, ok := authenticatedMerchant(w, r)
	if !ok {
		return
	}
	value, err := h.sessions.Get(r.Context(), merchantValue.ID, r.PathValue("sessionID"))
	if err != nil {
		writeSessionError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": sessionResponse(value)})
}

func (h *v3Handler) revokeSession(w http.ResponseWriter, r *http.Request) {
	merchantValue, ok := authenticatedMerchant(w, r)
	if !ok {
		return
	}
	if err := h.sessions.Revoke(r.Context(), merchantValue.ID, r.PathValue("sessionID")); err != nil {
		writeSessionError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *v3Handler) exchangeSession(w http.ResponseWriter, r *http.Request) {
	request := v3SessionExchangeRequest{}
	if err := decodeJSON(w, r, &request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	accessToken, value, err := h.sessions.Exchange(r.Context(), request.Token)
	if err != nil {
		writeSessionExchangeError(w, err)
		return
	}
	if !h.requireActivePlatformUser(w, r, value.MerchantID, value.UserID) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"data": map[string]any{
			"access_token": accessToken,
			"token_type":   "Bearer",
			"expires_at":   value.ExpiresAt,
			"session":      sessionResponse(value),
			"user":         sessionUserResponse(value),
		},
	})
}

func (h *v3Handler) refreshSession(w http.ResponseWriter, r *http.Request) {
	browserSession, ok := authenticatedUserSession(w, r)
	if !ok {
		return
	}
	accessToken, value, err := h.sessions.Refresh(r.Context(), browserSession.ID)
	if err != nil {
		writeUserSessionError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"data": map[string]any{
			"access_token": accessToken,
			"token_type":   "Bearer",
			"expires_at":   value.ExpiresAt,
			"session":      sessionResponse(value),
		},
	})
}

func (h *v3Handler) me(w http.ResponseWriter, r *http.Request) {
	browserSession, ok := authenticatedUserSession(w, r)
	if !ok {
		return
	}
	available, locked, err := h.wallets.GetBalance(
		r.Context(),
		browserSession.MerchantID,
		browserSession.UserID,
		browserSession.Currency,
	)
	if err != nil && !errors.Is(err, wallet.ErrNotFound) {
		writeWalletServiceError(w, err)
		return
	}
	availableBalance := formatMoney(available)
	if browserSession.WalletMode == "seamless" && h.callbacks != nil {
		// Prefer the real-time merchant balance query; fall back to the last
		// callback mirror so a slow or failing merchant never blocks the page.
		merchantBalance, balanceErr := h.callbacks.QueryBalance(
			r.Context(),
			browserSession.MerchantID,
			browserSession.UserID,
			browserSession.Currency,
		)
		if balanceErr != nil {
			merchantBalance, balanceErr = h.callbacks.GetLatestBalance(
				r.Context(),
				browserSession.MerchantID,
				browserSession.UserID,
				browserSession.Currency,
			)
		}
		if balanceErr == nil {
			availableBalance = merchantBalance
		} else if !errors.Is(balanceErr, callback.ErrNotFound) {
			slog.Warn("merchant balance mirror unavailable", "error", balanceErr, "merchant_id", browserSession.MerchantID)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"data": map[string]any{
			"user_id":           browserSession.UserID,
			"currency":          browserSession.Currency,
			"wallet_mode":       browserSession.WalletMode,
			"available_balance": availableBalance,
			"locked_balance":    formatMoney(locked),
			"locale":            browserSession.Locale,
		},
	})
}

func (h *v3Handler) listEvents(w http.ResponseWriter, r *http.Request) {
	if _, ok := authenticatedUserSession(w, r); !ok {
		return
	}
	limit, err := queryInt(r, "limit")
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if limit == 0 {
		limit = defaultUserMarketLimit
	}
	if limit < 1 || limit > maxUserMarketLimit {
		writeError(w, http.StatusBadRequest, "validation_error", "limit must be between 1 and 100")
		return
	}
	page, err := queryInt(r, "page")
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if page == 0 {
		page = 1
	}
	values, total, err := h.events.List(r.Context(), &event.ListFilters{
		Category: r.URL.Query().Get("category"),
		Status:   r.URL.Query().Get("status"),
		Page:     page,
		Limit:    limit,
	})
	if err != nil {
		writeEventServiceError(w, err)
		return
	}
	result := make([]v3UserEvent, 0, len(values))
	for _, value := range values {
		result = append(result, userEventFrom(value))
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"data": result,
		"meta": map[string]any{"pagination": newPagination(page, limit, total)},
	})
}

func (h *v3Handler) getEvent(w http.ResponseWriter, r *http.Request) {
	if _, ok := authenticatedUserSession(w, r); !ok {
		return
	}
	value, err := h.events.Get(r.Context(), r.PathValue("eventID"))
	if err != nil {
		writeEventServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": userEventFrom(value)})
}

func (h *v3Handler) listMarkets(w http.ResponseWriter, r *http.Request) {
	browserSession, ok := authenticatedUserSession(w, r)
	if !ok {
		return
	}
	limit, err := queryInt(r, "limit")
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if limit == 0 {
		limit = defaultUserMarketLimit
	}
	if limit < 1 || limit > maxUserMarketLimit {
		writeError(w, http.StatusBadRequest, "validation_error", "limit must be between 1 and 100")
		return
	}
	page, err := queryInt(r, "page")
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if page == 0 {
		page = 1
	}
	values, total, err := h.markets.List(r.Context(), &market.ListFilters{
		MerchantID: browserSession.MerchantID,
		EventID:    r.URL.Query().Get("event_id"),
		Status:     r.URL.Query().Get("status"),
		Page:       page,
		Limit:      limit,
	})
	if err != nil {
		writeMarketServiceError(w, err)
		return
	}
	result := make([]v3UserMarket, 0, len(values))
	for _, value := range values {
		result = append(result, userMarketFrom(value))
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"data": result,
		"meta": map[string]any{"pagination": newPagination(page, limit, total)},
	})
}

func (h *v3Handler) getMarket(w http.ResponseWriter, r *http.Request) {
	value, ok := h.authorizedUserMarket(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": userMarketFrom(value)})
}

func (h *v3Handler) getOrderBook(w http.ResponseWriter, r *http.Request) {
	value, ok := h.authorizedUserMarket(w, r)
	if !ok {
		return
	}
	book, err := h.orders.GetOrderBook(r.Context(), value.ID)
	if err != nil {
		writeMarketServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": book})
}

func (h *v3Handler) authorizedUserMarket(w http.ResponseWriter, r *http.Request) (*types.Market, bool) {
	browserSession, ok := authenticatedUserSession(w, r)
	if !ok {
		return nil, false
	}
	value, err := h.markets.Get(r.Context(), r.PathValue("marketID"))
	if err != nil {
		writeMarketServiceError(w, err)
		return nil, false
	}
	if value.MerchantID != browserSession.MerchantID {
		writeError(w, http.StatusNotFound, "not_found", "market was not found")
		return nil, false
	}
	return value, true
}

func v2OrderFilters(w http.ResponseWriter, r *http.Request, merchantID, userID string) (*order.ListFilters, bool) {
	from, err := queryTime(r, "from")
	if err != nil {
		writeError(w, http.StatusBadRequest, "validation_error", err.Error())
		return nil, false
	}
	to, err := queryTime(r, "to")
	if err != nil {
		writeError(w, http.StatusBadRequest, "validation_error", err.Error())
		return nil, false
	}
	limit, err := queryInt(r, "limit")
	if err != nil {
		writeError(w, http.StatusBadRequest, "validation_error", err.Error())
		return nil, false
	}
	return &order.ListFilters{
		MerchantID: merchantID,
		UserID:     userID,
		MarketID:   r.URL.Query().Get("market_id"),
		Status:     r.URL.Query().Get("status"),
		Cursor:     r.URL.Query().Get("cursor"),
		From:       from,
		To:         to,
		Limit:      limit,
	}, true
}

func v2TradeFilters(w http.ResponseWriter, r *http.Request, merchantID, orderID string) (*order.TradeListFilters, bool) {
	from, err := queryTime(r, "from")
	if err != nil {
		writeError(w, http.StatusBadRequest, "validation_error", err.Error())
		return nil, false
	}
	to, err := queryTime(r, "to")
	if err != nil {
		writeError(w, http.StatusBadRequest, "validation_error", err.Error())
		return nil, false
	}
	limit, err := queryInt(r, "limit")
	if err != nil {
		writeError(w, http.StatusBadRequest, "validation_error", err.Error())
		return nil, false
	}
	return &order.TradeListFilters{
		MerchantID: merchantID,
		OrderID:    orderID,
		Cursor:     r.URL.Query().Get("cursor"),
		From:       from,
		To:         to,
		Limit:      limit,
	}, true
}

func queryTime(r *http.Request, name string) (*time.Time, error) {
	value := strings.TrimSpace(r.URL.Query().Get(name))
	if value == "" {
		return nil, nil
	}
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return nil, fmt.Errorf("%s must be an RFC3339 timestamp", name)
	}
	parsed = parsed.UTC()
	return &parsed, nil
}

func (h *v3Handler) launchURLFor(token string) string {
	value := *h.launchURL
	query := value.Query()
	query.Set("token", token)
	value.RawQuery = query.Encode()
	return value.String()
}

func validHostedLaunchURL(rawURL string) (*url.URL, error) {
	value, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || value.Scheme != "https" || value.Host == "" {
		return nil, errors.New("hosted launch URL must be an absolute HTTPS URL")
	}
	return value, nil
}

func normalizeV3SessionRequest(request v3SessionCreateRequest, merchantCurrency string) (v3SessionCreateRequest, error) {
	request.UserID = strings.TrimSpace(request.UserID)
	request.Currency = strings.ToUpper(strings.TrimSpace(request.Currency))
	request.Balance = strings.TrimSpace(request.Balance)
	request.Locale = strings.TrimSpace(request.Locale)
	request.ReturnURL = strings.TrimSpace(request.ReturnURL)
	if request.UserID == "" || len(request.UserID) > 255 {
		return v3SessionCreateRequest{}, errors.New("user_id is required and must not exceed 255 characters")
	}
	if request.Currency == "" || request.Currency != merchantCurrency {
		return v3SessionCreateRequest{}, errors.New("currency must match the merchant currency")
	}
	if request.Locale == "" {
		request.Locale = "en-US"
	}
	if len(request.Locale) > 35 {
		return v3SessionCreateRequest{}, errors.New("locale must not exceed 35 characters")
	}
	if request.ReturnURL == "" {
		return request, nil
	}
	value, err := url.Parse(request.ReturnURL)
	if err != nil || value.Scheme != "https" || value.Host == "" {
		return v3SessionCreateRequest{}, errors.New("return_url must be an absolute HTTPS URL")
	}
	return request, nil
}

func normalizeBalanceSnapshot(value string) (string, error) {
	value = strings.TrimSpace(value)
	switch value {
	case "0", "0.0", "0.00":
		return "0.00", nil
	}
	cents, err := fixed.CentsFromString(value)
	if err != nil {
		return "", err
	}
	return fixed.FormatCents(cents), nil
}

func authenticatedUserSession(w http.ResponseWriter, r *http.Request) (*auth.UserSession, bool) {
	value, ok := auth.UserSessionFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "a valid browser session is required")
		return nil, false
	}
	return value, true
}

// requireActivePlatformUser rejects blocked merchant users at session and
// order boundaries. Unknown users are treated as active: they are provisioned
// at session creation and a lookup miss must not break existing flows.
func (h *v3Handler) requireActivePlatformUser(w http.ResponseWriter, r *http.Request, merchantID, userID string) bool {
	user, err := h.platformUsers.Get(r.Context(), merchantID, userID)
	if err != nil {
		if errors.Is(err, platformuser.ErrUserNotFound) {
			return true
		}
		writeError(w, http.StatusInternalServerError, "internal_error", "could not verify user status")
		return false
	}
	if user.Status == "blocked" {
		writeError(w, http.StatusForbidden, "user_blocked", "user is blocked")
		return false
	}
	return true
}

func sessionResponse(value session.BrowserSession) map[string]any {
	return map[string]any{
		"id":          value.ID,
		"user_id":     value.UserID,
		"currency":    value.Currency,
		"wallet_mode": value.WalletMode,
		"locale":      value.Locale,
		"created_at":  value.CreatedAt,
		"expires_at":  value.ExpiresAt,
		"return_url":  value.ReturnURL,
	}
}

func sessionUserResponse(value session.BrowserSession) map[string]any {
	return map[string]any{
		"user_id":           value.UserID,
		"currency":          value.Currency,
		"wallet_mode":       value.WalletMode,
		"available_balance": value.Balance,
		"locked_balance":    "0.00",
		"locale":            value.Locale,
	}
}

func userMarketFrom(value *types.Market) v3UserMarket {
	return v3UserMarket{
		ID:          value.ID,
		EventID:     value.EventID,
		Type:        value.Type,
		Question:    value.Question,
		Options:     append([]string{}, value.Options...),
		Status:      value.Status,
		TotalVolume: fmt.Sprintf("%.6f", value.TotalVolume),
		CreatedAt:   value.CreatedAt,
		SettledAt:   value.SettledAt,
	}
}

func userEventFrom(value *types.Event) v3UserEvent {
	return v3UserEvent{
		ID:             value.ID,
		Title:          value.Title,
		Description:    value.Description,
		Category:       value.Category,
		EndTime:        value.EndTime,
		ResolutionTime: value.ResolutionTime,
		Status:         value.Status,
		Outcome:        value.Outcome,
	}
}

func formatMoney(value float64) string {
	return fmt.Sprintf("%.2f", value)
}

func transferResponse(value *wallet.Transfer) v3TransferResponse {
	return v3TransferResponse{
		ID:                    value.ID,
		MerchantTransactionID: value.MerchantTransactionID,
		UserID:                value.UserID,
		Currency:              value.Currency,
		Amount:                formatMoney(value.Amount),
		Direction:             value.Direction,
		Status:                value.Status,
		TransactionID:         value.TransactionID,
		CreatedAt:             value.CreatedAt,
		UpdatedAt:             value.UpdatedAt,
	}
}

func writeSessionError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, session.ErrInvalidInput):
		writeError(w, http.StatusBadRequest, "validation_error", "session request is invalid")
	case errors.Is(err, session.ErrExpired), errors.Is(err, session.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", "session was not found")
	case errors.Is(err, session.ErrUnauthorized):
		writeError(w, http.StatusNotFound, "not_found", "session was not found")
	default:
		writeError(w, http.StatusInternalServerError, "internal_error", "an internal error occurred")
	}
}

func writeSessionExchangeError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, session.ErrInvalidInput):
		writeError(w, http.StatusBadRequest, "validation_error", "token is required")
	case errors.Is(err, session.ErrExpired),
		errors.Is(err, session.ErrUnauthorized),
		errors.Is(err, session.ErrNotFound):
		w.Header().Set("WWW-Authenticate", `Bearer error="invalid_token"`)
		writeError(w, http.StatusUnauthorized, "invalid_token", "launch token is invalid, expired, or already used")
	default:
		writeError(w, http.StatusInternalServerError, "internal_error", "an internal error occurred")
	}
}

func writeUserSessionError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, session.ErrExpired):
		w.Header().Set("WWW-Authenticate", `Bearer error="invalid_token"`)
		writeError(w, http.StatusUnauthorized, "session_expired", "user session has expired")
	case errors.Is(err, session.ErrUnauthorized), errors.Is(err, session.ErrNotFound):
		w.Header().Set("WWW-Authenticate", `Bearer error="invalid_token"`)
		writeError(w, http.StatusUnauthorized, "invalid_token", "user session is invalid")
	case errors.Is(err, session.ErrInvalidInput):
		writeError(w, http.StatusBadRequest, "validation_error", "session request is invalid")
	default:
		writeError(w, http.StatusInternalServerError, "internal_error", "an internal error occurred")
	}
}

func writeV2QueryError(w http.ResponseWriter, err error) {
	var validationErr *v2query.ValidationError
	switch {
	case errors.As(err, &validationErr):
		writeError(w, http.StatusBadRequest, "validation_error", validationErr.Error())
	case errors.Is(err, v2query.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", "V2 query subject was not found")
	default:
		writeError(w, http.StatusInternalServerError, "internal_error", "could not query V2 ledger data")
	}
}

func requestIDFromContext(ctx context.Context) string {
	value, _ := middleware.RequestIDFromContext(ctx)
	return value
}

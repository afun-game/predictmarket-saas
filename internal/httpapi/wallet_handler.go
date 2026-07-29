package httpapi

import (
	"errors"
	"net/http"
	"strings"

	"github.com/nxsky/twill/runtime/middleware"
	"github.com/afun-game/predictmarket-saas/internal/auth"
	"github.com/afun-game/predictmarket-saas/internal/merchant"
	"github.com/afun-game/predictmarket-saas/internal/wallet"
	"github.com/afun-game/predictmarket-saas/pkg/types"
)

const (
	defaultWalletPage  = 1
	defaultWalletLimit = 20
)

type walletHandler struct {
	service wallet.Service
}

type walletCreditRequest struct {
	Currency string  `json:"currency"`
	Amount   float64 `json:"amount"`
	Type     string  `json:"type,omitempty"`
}

type walletBalance struct {
	Currency  string  `json:"currency"`
	Available float64 `json:"available"`
	Locked    float64 `json:"locked"`
	Total     float64 `json:"total"`
}

func registerWalletRoutes(
	mux *http.ServeMux,
	merchantService merchant.Service,
	walletService wallet.Service,
) {
	handler := &walletHandler{service: walletService}
	mux.Handle(
		"GET /api/v1/wallets/{userID}",
		auth.RequireMerchant(merchantService, http.HandlerFunc(handler.getBalance)),
	)
	mux.Handle(
		"POST /api/v1/wallets/{userID}/credit",
		auth.RequireMerchant(
			merchantService,
			middleware.RequireIdempotencyKey(http.MethodPost)(http.HandlerFunc(handler.credit)),
		),
	)
	mux.Handle(
		"GET /api/v1/wallets/{userID}/transactions",
		auth.RequireMerchant(merchantService, http.HandlerFunc(handler.listTransactions)),
	)
}

func (h *walletHandler) getBalance(w http.ResponseWriter, r *http.Request) {
	merchantValue, ok := authenticatedMerchant(w, r)
	if !ok {
		return
	}
	currency := strings.TrimSpace(r.URL.Query().Get("currency"))
	if currency == "" {
		currency = merchantValue.Currency
	}
	available, locked, err := h.service.GetBalance(
		r.Context(),
		merchantValue.ID,
		r.PathValue("userID"),
		currency,
	)
	if err != nil {
		writeWalletServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"data": map[string]any{
			"user_id": r.PathValue("userID"),
			"balances": []walletBalance{{
				Currency:  strings.ToUpper(currency),
				Available: available,
				Locked:    locked,
				Total:     available + locked,
			}},
		},
	})
}

func (h *walletHandler) credit(w http.ResponseWriter, r *http.Request) {
	merchantValue, ok := authenticatedMerchant(w, r)
	if !ok {
		return
	}
	var request walletCreditRequest
	if err := decodeJSON(w, r, &request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	transactionType := strings.ToLower(strings.TrimSpace(request.Type))
	if transactionType != "" && transactionType != "credit" && transactionType != "admin_credit" {
		writeError(
			w,
			http.StatusBadRequest,
			"validation_error",
			"invalid type: must be credit or admin_credit",
		)
		return
	}
	if err := h.service.CreditWithIdempotency(
		r.Context(),
		merchantValue.ID,
		r.PathValue("userID"),
		request.Currency,
		request.Amount,
		"credit",
		r.Header.Get(middleware.IdempotencyKeyHeader),
	); err != nil {
		writeWalletServiceError(w, err)
		return
	}
	value, err := h.service.Get(
		r.Context(),
		merchantValue.ID,
		r.PathValue("userID"),
		request.Currency,
	)
	if err != nil {
		writeWalletServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": value})
}

func (h *walletHandler) listTransactions(w http.ResponseWriter, r *http.Request) {
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
	values, total, err := h.service.ListTransactions(
		r.Context(),
		merchantValue.ID,
		r.PathValue("userID"),
		page,
		limit,
	)
	if err != nil {
		writeWalletServiceError(w, err)
		return
	}
	page, limit = walletPageDefaults(page, limit)
	writeJSON(w, http.StatusOK, map[string]any{
		"data": values,
		"meta": map[string]any{
			"pagination": newPagination(page, limit, total),
		},
	})
}

func authenticatedMerchant(w http.ResponseWriter, r *http.Request) (*types.Merchant, bool) {
	merchantValue, ok := auth.MerchantFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "a valid API key is required")
		return nil, false
	}
	return merchantValue, true
}

func walletPageDefaults(page, limit int) (int, int) {
	if page == 0 {
		page = defaultWalletPage
	}
	if limit == 0 {
		limit = defaultWalletLimit
	}
	return page, limit
}

func writeWalletServiceError(w http.ResponseWriter, err error) {
	var validationErr *wallet.ValidationError
	switch {
	case errors.As(err, &validationErr):
		writeError(w, http.StatusBadRequest, "validation_error", validationErr.Error())
	case errors.Is(err, wallet.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", "wallet was not found")
	case errors.Is(err, wallet.ErrAlreadyExists):
		writeError(w, http.StatusConflict, "already_exists", "wallet already exists")
	case errors.Is(err, wallet.ErrInvalidMerchant):
		writeError(w, http.StatusUnprocessableEntity, "invalid_merchant", "merchant must exist and be active")
	case errors.Is(err, wallet.ErrInsufficientBalance):
		writeError(w, http.StatusConflict, "insufficient_balance", "available balance is insufficient")
	case errors.Is(err, wallet.ErrInsufficientLocked):
		writeError(w, http.StatusConflict, "insufficient_locked_balance", "locked balance is insufficient")
	default:
		writeError(w, http.StatusInternalServerError, "internal_error", "an internal error occurred")
	}
}

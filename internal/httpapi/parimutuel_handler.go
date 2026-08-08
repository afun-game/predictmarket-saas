package httpapi

import (
	"errors"
	"net/http"
	"strings"

	"github.com/afun-game/predictmarket-saas/internal/auth"
	"github.com/afun-game/predictmarket-saas/internal/market"
	"github.com/afun-game/predictmarket-saas/internal/merchant"
	"github.com/afun-game/predictmarket-saas/internal/parimutuel"
	"github.com/afun-game/predictmarket-saas/internal/wallet"
	"github.com/nxsky/twill/runtime/middleware"
)

// registerParimutuelRoutes exposes merchant-scoped betting for parimutuel
// markets. The AdminConfig carries the parimutuel service.
func registerParimutuelRoutes(
	mux *http.ServeMux,
	merchantService merchant.Service,
	marketService market.Service,
	walletService wallet.Service,
	service parimutuel.Service,
) {
	handler := &parimutuelHandler{
		merchants: merchantService,
		markets:   marketService,
		wallets:   walletService,
		service:   service,
	}
	mux.Handle(
		"POST /api/v1/bets",
		auth.RequireMerchant(
			merchantService,
			middleware.RequireIdempotencyKey(http.MethodPost)(http.HandlerFunc(handler.createBet)),
		),
	)
	mux.Handle(
		"GET /api/v1/bets",
		auth.RequireMerchant(merchantService, http.HandlerFunc(handler.listBets)),
	)
}

type parimutuelHandler struct {
	merchants merchant.Service
	markets   market.Service
	wallets   wallet.Service
	service   parimutuel.Service
}

func (h *parimutuelHandler) createBet(w http.ResponseWriter, r *http.Request) {
	merchantValue, ok := auth.MerchantFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "a valid merchant API key is required")
		return
	}
	request := struct {
		MarketID string  `json:"market_id"`
		UserID   string  `json:"user_id"`
		Option   string  `json:"option"`
		Amount   float64 `json:"amount"`
		Currency string  `json:"currency"`
	}{}
	if err := decodeJSON(w, r, &request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	request.MarketID = strings.TrimSpace(request.MarketID)
	request.UserID = strings.TrimSpace(request.UserID)
	request.Currency = strings.ToUpper(strings.TrimSpace(request.Currency))
	if request.MarketID == "" || request.UserID == "" {
		writeError(w, http.StatusBadRequest, "validation_error", "market_id and user_id are required")
		return
	}

	marketValue, err := h.markets.Get(r.Context(), request.MarketID)
	if err != nil {
		writeMarketServiceError(w, err)
		return
	}
	if marketValue.MerchantID != merchantValue.ID {
		writeError(w, http.StatusForbidden, "forbidden", "merchant access is not allowed")
		return
	}
	if marketValue.Type != "parimutuel" {
		writeError(w, http.StatusBadRequest, "validation_error", "market is not a parimutuel market")
		return
	}
	if request.Currency == "" {
		request.Currency = merchantValue.Currency
	}
	if request.Currency != merchantValue.Currency {
		writeError(w, http.StatusBadRequest, "validation_error", "currency does not match the merchant currency")
		return
	}

	// The stake leaves the wallet immediately and joins the pool. If the bet
	// cannot be recorded the debit is refunded.
	if err := h.wallets.Debit(r.Context(), merchantValue.ID, request.UserID, request.Currency, request.Amount, "bet"); err != nil {
		if errors.Is(err, wallet.ErrInsufficientBalance) {
			writeError(w, http.StatusBadRequest, "insufficient_balance", "insufficient available balance")
			return
		}
		writeError(w, http.StatusInternalServerError, "internal_error", "could not debit wallet")
		return
	}
	bet, err := h.service.PlaceBet(r.Context(), parimutuel.Bet{
		MarketID:   request.MarketID,
		MerchantID: merchantValue.ID,
		UserID:     request.UserID,
		Option:     request.Option,
		Stake:      request.Amount,
		Currency:   request.Currency,
	})
	if err != nil {
		// Best-effort refund: the debit already left the wallet.
		_ = h.wallets.Credit(r.Context(), merchantValue.ID, request.UserID, request.Currency, request.Amount, "bet_refund")
		writeParimutuelServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"data": bet})
}

func (h *parimutuelHandler) listBets(w http.ResponseWriter, r *http.Request) {
	merchantValue, ok := auth.MerchantFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "a valid merchant API key is required")
		return
	}
	page, limit, err := queryPage(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	items, total, err := h.service.ListBets(r.Context(), parimutuel.ListFilters{
		MerchantID: merchantValue.ID,
		MarketID:   strings.TrimSpace(r.URL.Query().Get("market_id")),
		UserID:     strings.TrimSpace(r.URL.Query().Get("user_id")),
		Page:       page,
		Limit:      limit,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "could not list bets")
		return
	}
	adminList(w, items, total)
}

func writeParimutuelServiceError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, parimutuel.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", "market was not found")
	case errors.Is(err, parimutuel.ErrNotParimutuel):
		writeError(w, http.StatusBadRequest, "validation_error", "market is not a parimutuel market")
	case errors.Is(err, parimutuel.ErrMarketInactive):
		writeError(w, http.StatusConflict, "market_inactive", "market is not active")
	case errors.Is(err, parimutuel.ErrEventSettled):
		writeError(w, http.StatusConflict, "event_settled", "the event is no longer open for betting")
	case errors.Is(err, parimutuel.ErrInvalidOption):
		writeError(w, http.StatusBadRequest, "validation_error", "option is not offered by the market")
	case errors.Is(err, parimutuel.ErrInvalidBet):
		writeError(w, http.StatusBadRequest, "validation_error", "invalid bet")
	case errors.Is(err, parimutuel.ErrPoolNotInitialized):
		writeError(w, http.StatusConflict, "pool_not_initialized", "the parimutuel pool is not initialized")
	case errors.Is(err, parimutuel.ErrBetAmountTooLarge):
		writeError(w, http.StatusUnprocessableEntity, "bet_amount_too_large", "stake exceeds the configured bet limit")
	case errors.Is(err, parimutuel.ErrUserExposureTooHigh):
		writeError(w, http.StatusConflict, "user_exposure_too_high", "bet would exceed the configured user exposure limit")
	default:
		var validationErr *parimutuel.ValidationError
		if errors.As(err, &validationErr) {
			writeError(w, http.StatusBadRequest, "validation_error", validationErr.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, "internal_error", "could not place bet")
	}
}

// Package httpapi exposes the public PredictMarket HTTP API.
package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/afun-game/predictmarket-saas/internal/analytics"
	"github.com/afun-game/predictmarket-saas/internal/auth"
	"github.com/afun-game/predictmarket-saas/internal/currency"
	"github.com/afun-game/predictmarket-saas/internal/event"
	"github.com/afun-game/predictmarket-saas/internal/market"
	"github.com/afun-game/predictmarket-saas/internal/merchant"
	"github.com/afun-game/predictmarket-saas/internal/order"
	"github.com/afun-game/predictmarket-saas/internal/sports"
	"github.com/afun-game/predictmarket-saas/internal/wallet"
	"github.com/afun-game/predictmarket-saas/pkg/types"
	"github.com/nxsky/twill/runtime/middleware"
)

const MaxRequestBodyBytes = 1 << 20

const (
	globalRateLimit       = 600
	registrationRateLimit = 10
	rateLimitWindow       = time.Minute
)

type merchantHandler struct {
	service merchant.Service
}

type merchantConfig struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Email    string `json:"email"`
	Status   string `json:"status"`
	Currency string `json:"currency"`
	Timezone string `json:"timezone"`
}

// NewHandler constructs the HTTP API with merchant and administrator authentication.
func NewHandler(
	merchantService merchant.Service,
	eventService event.Service,
	marketService market.Service,
	walletService wallet.Service,
	orderService order.Service,
	currencyService currency.Service,
	adminAPIKey string,
	optionalServices ...any,
) http.Handler {
	handler := &merchantHandler{service: merchantService}
	mux := http.NewServeMux()
	mux.Handle(
		"POST /api/v1/merchants/register",
		middleware.RateLimit(registrationRateLimit, rateLimitWindow)(http.HandlerFunc(handler.register)),
	)
	mux.Handle(
		"GET /api/v1/merchants/{merchantID}/config",
		auth.RequireMerchant(merchantService, http.HandlerFunc(handler.getConfig)),
	)
	mux.Handle(
		"PATCH /api/v1/merchants/{merchantID}/config",
		auth.RequireMerchant(merchantService, http.HandlerFunc(handler.updateConfig)),
	)
	mux.Handle(
		"GET /api/v1/merchants",
		auth.RequireAdmin(adminAPIKey, http.HandlerFunc(handler.list)),
	)
	registerEventRoutes(mux, merchantService, eventService, adminAPIKey)
	registerMarketRoutes(mux, merchantService, marketService, orderService, adminAPIKey)
	registerWalletRoutes(mux, merchantService, walletService)
	registerOrderRoutes(mux, merchantService, orderService)
	registerCurrencyRoutes(mux, merchantService, currencyService, adminAPIKey)
	for _, optionalService := range optionalServices {
		switch service := optionalService.(type) {
		case sports.Service:
			registerSportsRoutes(mux, merchantService, service, adminAPIKey)
		case analytics.Service:
			registerAnalyticsRoutes(
				mux,
				merchantService,
				marketService,
				service,
				adminAPIKey,
			)
		}
	}
	return middleware.RateLimit(globalRateLimit, rateLimitWindow)(mux)
}

func (h *merchantHandler) register(w http.ResponseWriter, r *http.Request) {
	var request merchant.RegisterRequest
	if err := decodeJSON(w, r, &request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}

	registered, err := h.service.Register(r.Context(), &request)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"data": map[string]string{
			"merchant_id": registered.ID,
			"api_key":     registered.APIKey,
			"api_secret":  registered.APISecret,
		},
	})
}

func (h *merchantHandler) getConfig(w http.ResponseWriter, r *http.Request) {
	merchantID := r.PathValue("merchantID")
	if !authorizedForMerchant(r, merchantID) {
		writeError(w, http.StatusForbidden, "forbidden", "merchant access is not allowed")
		return
	}

	result, err := h.service.Get(r.Context(), merchantID)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": configFromMerchant(result)})
}

func (h *merchantHandler) updateConfig(w http.ResponseWriter, r *http.Request) {
	merchantID := r.PathValue("merchantID")
	if !authorizedForMerchant(r, merchantID) {
		writeError(w, http.StatusForbidden, "forbidden", "merchant access is not allowed")
		return
	}

	var request merchant.UpdateRequest
	if err := decodeJSON(w, r, &request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	result, err := h.service.Update(r.Context(), merchantID, &request)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": configFromMerchant(result)})
}

func (h *merchantHandler) list(w http.ResponseWriter, r *http.Request) {
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

	merchants, err := h.service.List(r.Context(), page, limit)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	configs := make([]merchantConfig, 0, len(merchants))
	for _, item := range merchants {
		configs = append(configs, configFromMerchant(item))
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": configs})
}

func authorizedForMerchant(r *http.Request, merchantID string) bool {
	authenticated, ok := auth.MerchantFromContext(r.Context())
	return ok && authenticated.ID == merchantID
}

func configFromMerchant(value *types.Merchant) merchantConfig {
	return merchantConfig{
		ID:       value.ID,
		Name:     value.Name,
		Email:    value.Email,
		Status:   value.Status,
		Currency: value.Currency,
		Timezone: value.Timezone,
	}
}

func decodeJSON(w http.ResponseWriter, r *http.Request, destination any) error {
	r.Body = http.MaxBytesReader(w, r.Body, MaxRequestBodyBytes)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("request body must contain one JSON object")
	}
	return nil
}

func queryInt(r *http.Request, name string) (int, error) {
	value := r.URL.Query().Get(name)
	if value == "" {
		return 0, nil
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return 0, &merchant.ValidationError{Field: name, Message: "must be an integer"}
	}
	return parsed, nil
}

func writeServiceError(w http.ResponseWriter, err error) {
	var validationErr *merchant.ValidationError
	switch {
	case errors.As(err, &validationErr):
		writeError(w, http.StatusBadRequest, "validation_error", validationErr.Error())
	case errors.Is(err, merchant.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", "merchant was not found")
	case errors.Is(err, merchant.ErrUnauthorized):
		writeError(w, http.StatusUnauthorized, "unauthorized", "a valid API key is required")
	default:
		writeError(w, http.StatusInternalServerError, "internal_error", "an internal error occurred")
	}
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]any{
		"error": map[string]string{
			"code":    code,
			"message": message,
		},
	})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(value); err != nil {
		return
	}
}

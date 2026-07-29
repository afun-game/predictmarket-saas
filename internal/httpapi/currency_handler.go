package httpapi

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/afun-game/predictmarket-saas/internal/auth"
	"github.com/afun-game/predictmarket-saas/internal/currency"
	"github.com/afun-game/predictmarket-saas/internal/merchant"
)

type currencyHandler struct {
	service currency.Service
}

type conversionRequest struct {
	Amount float64 `json:"amount"`
	From   string  `json:"from"`
	To     string  `json:"to"`
}

type timeConversionRequest struct {
	Timestamp string `json:"timestamp"`
	Timezone  string `json:"timezone"`
}

func registerCurrencyRoutes(
	mux *http.ServeMux,
	merchantService merchant.Service,
	currencyService currency.Service,
	adminAPIKey string,
) {
	handler := &currencyHandler{service: currencyService}
	mux.Handle(
		"GET /api/v1/currencies",
		auth.RequireMerchant(merchantService, http.HandlerFunc(handler.listSupported)),
	)
	mux.Handle(
		"GET /api/v1/currencies/rate",
		auth.RequireMerchant(merchantService, http.HandlerFunc(handler.getRate)),
	)
	mux.Handle(
		"POST /api/v1/currencies/convert",
		auth.RequireMerchant(merchantService, http.HandlerFunc(handler.convert)),
	)
	mux.Handle(
		"POST /api/v1/currencies/time",
		auth.RequireMerchant(merchantService, http.HandlerFunc(handler.convertTime)),
	)
	mux.Handle(
		"POST /api/v1/currencies/refresh",
		auth.RequireAdmin(adminAPIKey, http.HandlerFunc(handler.refresh)),
	)
}

func (h *currencyHandler) listSupported(w http.ResponseWriter, r *http.Request) {
	values, err := h.service.ListSupported(r.Context())
	if err != nil {
		writeCurrencyServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": values})
}

func (h *currencyHandler) getRate(w http.ResponseWriter, r *http.Request) {
	from := r.URL.Query().Get("from")
	to := r.URL.Query().Get("to")
	rate, err := h.service.GetRate(r.Context(), from, to)
	if err != nil {
		writeCurrencyServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"data": map[string]any{
			"from": strings.ToUpper(strings.TrimSpace(from)),
			"to":   strings.ToUpper(strings.TrimSpace(to)),
			"rate": rate,
		},
	})
}

func (h *currencyHandler) convert(w http.ResponseWriter, r *http.Request) {
	var request conversionRequest
	if err := decodeJSON(w, r, &request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	converted, err := h.service.Convert(
		r.Context(),
		request.Amount,
		request.From,
		request.To,
	)
	if err != nil {
		writeCurrencyServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"data": map[string]any{
			"amount":           request.Amount,
			"from":             strings.ToUpper(strings.TrimSpace(request.From)),
			"to":               strings.ToUpper(strings.TrimSpace(request.To)),
			"converted_amount": converted,
		},
	})
}

func (h *currencyHandler) convertTime(w http.ResponseWriter, r *http.Request) {
	var request timeConversionRequest
	if err := decodeJSON(w, r, &request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	timestamp, err := time.Parse(time.RFC3339, request.Timestamp)
	if err != nil {
		writeError(w, http.StatusBadRequest, "validation_error", "timestamp must be RFC3339")
		return
	}
	converted, err := h.service.ConvertTime(r.Context(), timestamp, request.Timezone)
	if err != nil {
		writeCurrencyServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"data": map[string]string{
			"timestamp": converted.Format(time.RFC3339),
			"timezone":  request.Timezone,
		},
	})
}

func (h *currencyHandler) refresh(w http.ResponseWriter, r *http.Request) {
	if err := h.service.RefreshRates(r.Context()); err != nil {
		writeCurrencyServiceError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func writeCurrencyServiceError(w http.ResponseWriter, err error) {
	var validationErr *currency.ValidationError
	switch {
	case errors.As(err, &validationErr):
		writeError(w, http.StatusBadRequest, "validation_error", validationErr.Error())
	case errors.Is(err, currency.ErrRateNotFound):
		writeError(w, http.StatusNotFound, "rate_not_found", "exchange rate was not found")
	case errors.Is(err, currency.ErrProviderUnavailable):
		writeError(w, http.StatusServiceUnavailable, "provider_unavailable", "exchange rate provider is unavailable")
	default:
		writeError(w, http.StatusInternalServerError, "internal_error", "an internal error occurred")
	}
}

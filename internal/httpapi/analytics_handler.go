package httpapi

import (
	"errors"
	"net/http"

	"github.com/afun-game/predictmarket-saas/internal/analytics"
	"github.com/afun-game/predictmarket-saas/internal/auth"
	"github.com/afun-game/predictmarket-saas/internal/market"
	"github.com/afun-game/predictmarket-saas/internal/merchant"
)

type analyticsHandler struct {
	service       analytics.Service
	marketService market.Service
}

func registerAnalyticsRoutes(
	mux *http.ServeMux,
	merchantService merchant.Service,
	marketService market.Service,
	service analytics.Service,
	adminAPIKey string,
) {
	handler := &analyticsHandler{service: service, marketService: marketService}
	mux.Handle(
		"GET /api/v1/analytics/merchant",
		auth.RequireMerchant(merchantService, http.HandlerFunc(handler.merchant)),
	)
	mux.Handle(
		"GET /api/v1/analytics/markets/{marketID}",
		auth.RequireMerchant(merchantService, http.HandlerFunc(handler.market)),
	)
	mux.Handle(
		"GET /api/v1/analytics/users/{userID}",
		auth.RequireMerchant(merchantService, http.HandlerFunc(handler.user)),
	)
	mux.Handle(
		"GET /api/v1/analytics/platform",
		auth.RequireAdmin(adminAPIKey, http.HandlerFunc(handler.platform)),
	)
}

func (h *analyticsHandler) merchant(w http.ResponseWriter, r *http.Request) {
	merchantValue, ok := authenticatedMerchant(w, r)
	if !ok {
		return
	}
	result, err := h.service.GetMerchantStats(
		r.Context(),
		merchantValue.ID,
		r.URL.Query().Get("time_range"),
	)
	if err != nil {
		writeAnalyticsServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": result})
}

func (h *analyticsHandler) market(w http.ResponseWriter, r *http.Request) {
	merchantValue, ok := authenticatedMerchant(w, r)
	if !ok {
		return
	}
	marketValue, err := h.marketService.Get(r.Context(), r.PathValue("marketID"))
	if err != nil || marketValue.MerchantID != merchantValue.ID {
		writeError(w, http.StatusNotFound, "not_found", "market analytics were not found")
		return
	}
	result, err := h.service.GetMarketStats(r.Context(), marketValue.ID)
	if err != nil {
		writeAnalyticsServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": result})
}

func (h *analyticsHandler) user(w http.ResponseWriter, r *http.Request) {
	merchantValue, ok := authenticatedMerchant(w, r)
	if !ok {
		return
	}
	result, err := h.service.GetUserStats(
		r.Context(),
		merchantValue.ID,
		r.PathValue("userID"),
	)
	if err != nil {
		writeAnalyticsServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": result})
}

func (h *analyticsHandler) platform(w http.ResponseWriter, r *http.Request) {
	result, err := h.service.GetPlatformStats(r.Context(), r.URL.Query().Get("time_range"))
	if err != nil {
		writeAnalyticsServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": result})
}

func writeAnalyticsServiceError(w http.ResponseWriter, err error) {
	var validationErr *analytics.ValidationError
	switch {
	case errors.As(err, &validationErr):
		writeError(w, http.StatusBadRequest, "validation_error", validationErr.Error())
	case errors.Is(err, analytics.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", "analytics subject was not found")
	default:
		writeError(w, http.StatusInternalServerError, "internal_error", "an internal error occurred")
	}
}

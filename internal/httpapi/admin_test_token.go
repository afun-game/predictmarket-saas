package httpapi

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/afun-game/predictmarket-saas/internal/auth"
	"github.com/afun-game/predictmarket-saas/internal/platformuser"
)

// createTestToken issues a one-time launch token for frontend testing. It is
// a super_admin console action: the console already knows the merchant, and
// the generated launch_url opens the hosted trading page directly, so frontend
// engineers can test without merchant signing credentials.
func (h *adminHandler) createTestToken(w http.ResponseWriter, r *http.Request) {
	if h.config.Sessions == nil {
		writeError(w, http.StatusServiceUnavailable, "v3_not_configured", "hosted sessions are not configured")
		return
	}
	request := struct {
		MerchantID string `json:"merchant_id"`
		UserID     string `json:"user_id,omitempty"`
		Currency   string `json:"currency,omitempty"`
		Locale     string `json:"locale,omitempty"`
		ReturnURL  string `json:"return_url,omitempty"`
	}{}
	if err := decodeJSON(w, r, &request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	request.MerchantID = strings.TrimSpace(request.MerchantID)
	if request.MerchantID == "" {
		writeError(w, http.StatusBadRequest, "validation_error", "merchant_id is required")
		return
	}
	merchantValue, err := h.merchants.Get(r.Context(), request.MerchantID)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	if merchantValue.Status != "active" {
		writeError(w, http.StatusConflict, "merchant_inactive", "merchant is not active")
		return
	}
	userID := strings.TrimSpace(request.UserID)
	if userID == "" {
		userID = "test-user"
	}
	if len(userID) > 255 {
		writeError(w, http.StatusBadRequest, "validation_error", "user_id must be at most 255 characters")
		return
	}
	currency := strings.ToUpper(strings.TrimSpace(request.Currency))
	if currency == "" {
		currency = merchantValue.Currency
	}
	locale := strings.TrimSpace(request.Locale)
	if locale == "" {
		locale = "zh-CN"
	}
	returnURL := strings.TrimSpace(request.ReturnURL)

	// Keep the platform user table consistent with session creation and
	// refuse blocked users, mirroring the merchant-side session endpoint.
	if h.config.PlatformUsers != nil {
		if err := h.config.PlatformUsers.Upsert(r.Context(), platformuser.User{
			MerchantID:     merchantValue.ID,
			ExternalUserID: userID,
			Locale:         locale,
			Status:         "active",
		}); err != nil {
			writeError(w, http.StatusInternalServerError, "internal_error", "could not provision platform user")
			return
		}
		user, lookupErr := h.config.PlatformUsers.Get(r.Context(), merchantValue.ID, userID)
		if lookupErr == nil && user.Status == "blocked" {
			writeError(w, http.StatusForbidden, "user_blocked", "user is blocked")
			return
		}
	}

	launchToken, launch, err := h.config.Sessions.CreateLaunch(
		r.Context(),
		merchantValue.ID,
		userID,
		currency,
		merchantValue.WalletMode,
		locale,
		returnURL,
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "could not create launch token")
		return
	}
	launchURL := testLaunchURL(h.config.HostedLaunchURL, launchToken)

	if principal, ok := auth.AdminPrincipalFromContext(r.Context()); ok {
		h.adminAudit(principal, "create.test_token", "merchant", merchantValue.ID, nil, map[string]any{
			"merchant_id": merchantValue.ID,
			"user_id":     userID,
			"currency":    currency,
			"locale":      locale,
		}, r)
	}
	writeJSON(w, http.StatusCreated, map[string]any{"data": map[string]any{
		"launch_url":  launchURL,
		"token":       launchToken,
		"expires_at":  launch.ExpiresAt,
		"merchant_id": merchantValue.ID,
		"user_id":     userID,
		"currency":    currency,
		"wallet_mode": merchantValue.WalletMode,
	}})
}

func testLaunchURL(hostedLaunchURL, token string) string {
	value, err := url.Parse(strings.TrimSpace(hostedLaunchURL))
	if err != nil || value.Scheme == "" || value.Host == "" {
		return ""
	}
	query := value.Query()
	query.Set("token", token)
	value.RawQuery = query.Encode()
	return value.String()
}

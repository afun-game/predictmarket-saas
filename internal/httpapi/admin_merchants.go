package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/afun-game/predictmarket-saas/internal/adminquery"
	"github.com/afun-game/predictmarket-saas/internal/auth"
	"github.com/afun-game/predictmarket-saas/internal/merchant"
	"github.com/afun-game/predictmarket-saas/internal/platformuser"
)

// createMerchant opens a new merchant account. The API key and secret are
// returned in cleartext exactly once, mirroring the self-service register
// endpoint; the console shows them in a save-once dialog. An optional
// wallet_mode (transfer/seamless) is applied via the admin-only integration
// configuration; seamless generates a callback secret that is returned once.
func (h *adminHandler) createMerchant(w http.ResponseWriter, r *http.Request) {
	request := struct {
		Name        string  `json:"name"`
		Email       string  `json:"email"`
		Currency    string  `json:"currency"`
		Timezone    string  `json:"timezone"`
		WalletMode  *string `json:"wallet_mode,omitempty"`
		CallbackURL *string `json:"callback_url,omitempty"`
	}{}
	if err := decodeJSON(w, r, &request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	registered, err := h.merchants.Register(r.Context(), &merchant.RegisterRequest{
		Name:     request.Name,
		Email:    request.Email,
		Currency: request.Currency,
		Timezone: request.Timezone,
	})
	if err != nil {
		writeServiceError(w, err)
		return
	}
	payload := merchantState(registered)
	payload["api_key"] = registered.APIKey
	payload["api_secret"] = registered.APISecret
	if request.WalletMode != nil && strings.TrimSpace(*request.WalletMode) != "" {
		mode := strings.ToLower(strings.TrimSpace(*request.WalletMode))
		integration := &merchant.IntegrationRequest{WalletMode: &mode}
		if request.CallbackURL != nil {
			urlValue := strings.TrimSpace(*request.CallbackURL)
			integration.CallbackURL = &urlValue
		}
		configured, configureErr := h.merchants.ConfigureIntegration(r.Context(), registered.ID, integration)
		if configureErr != nil {
			writeServiceError(w, configureErr)
			return
		}
		payload["wallet_mode"] = configured.WalletMode
		if configured.CallbackSecret != "" {
			// Seamless mode generated a callback secret; it is shown once.
			payload["callback_secret"] = configured.CallbackSecret
		}
	}
	if principal, ok := auth.AdminPrincipalFromContext(r.Context()); ok {
		h.adminAudit(principal, "create.merchant", "merchant", registered.ID, nil, merchantState(registered), r)
	}
	writeJSON(w, http.StatusCreated, map[string]any{"data": payload})
}

// listMerchants serves GET /api/v1/admin/merchants.
func (h *adminHandler) listMerchants(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	page, limit, _ := queryPage(r)
	items, total, err := h.config.Queries.ListMerchants(r.Context(), q, page, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "could not list merchants")
		return
	}
	adminList(w, items, total)
}

// getMerchant serves GET /api/v1/admin/merchants/{merchantID}.
func (h *adminHandler) getMerchant(w http.ResponseWriter, r *http.Request) {
	merchantID := r.PathValue("merchantID")
	current, err := h.merchants.Get(r.Context(), merchantID)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	stats, err := h.config.Queries.GetMerchantStats(r.Context(), merchantID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "could not load merchant statistics")
		return
	}
	state := merchantState(current)
	state["stats"] = stats
	writeJSON(w, http.StatusOK, map[string]any{"data": state})
}

// updateMerchant serves PATCH /api/v1/admin/merchants/{merchantID}. Basic
// fields go through merchant.Update; wallet_mode is applied through the
// admin-only integration configuration (switching to seamless generates a
// callback secret that is returned once).
func (h *adminHandler) updateMerchant(w http.ResponseWriter, r *http.Request) {
	merchantID := r.PathValue("merchantID")
	var request struct {
		merchant.UpdateRequest
		WalletMode  *string `json:"wallet_mode,omitempty"`
		CallbackURL *string `json:"callback_url,omitempty"`
	}
	if err := decodeJSON(w, r, &request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	current, err := h.merchants.Get(r.Context(), merchantID)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	updated, err := h.merchants.Update(r.Context(), merchantID, &request.UpdateRequest)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	payload := merchantState(updated)
	if request.WalletMode != nil && strings.TrimSpace(*request.WalletMode) != "" {
		mode := strings.ToLower(strings.TrimSpace(*request.WalletMode))
		integration := &merchant.IntegrationRequest{WalletMode: &mode}
		if request.CallbackURL != nil {
			urlValue := strings.TrimSpace(*request.CallbackURL)
			integration.CallbackURL = &urlValue
		}
		configured, configureErr := h.merchants.ConfigureIntegration(r.Context(), merchantID, integration)
		if configureErr != nil {
			writeServiceError(w, configureErr)
			return
		}
		payload["wallet_mode"] = configured.WalletMode
		if configured.CallbackSecret != "" {
			payload["callback_secret"] = configured.CallbackSecret
		}
	}
	if principal, ok := auth.AdminPrincipalFromContext(r.Context()); ok {
		h.adminAudit(
			principal,
			"update.merchant",
			"merchant",
			merchantID,
			merchantState(current),
			payload,
			r,
		)
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": payload})
}

// updateMerchantStatus serves PATCH /api/v1/admin/merchants/{merchantID}/status.
func (h *adminHandler) updateMerchantStatus(w http.ResponseWriter, r *http.Request) {
	merchantID := r.PathValue("merchantID")
	request, ok := readStatusPayload(w, r, "active", "suspended")
	if !ok {
		return
	}
	current, err := h.merchants.Get(r.Context(), merchantID)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	updated, err := h.merchants.UpdateStatus(r.Context(), merchantID, request.Status)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	if principal, ok := auth.AdminPrincipalFromContext(r.Context()); ok {
		h.adminAudit(
			principal,
			"status.merchant",
			"merchant",
			merchantID,
			merchantState(current),
			merchantState(updated),
			r,
		)
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": map[string]string{
		"id":     updated.ID,
		"status": updated.Status,
	}})
}

// reissueMerchantSecret rotates the merchant's V3 signing secret and returns
// the new cleartext exactly once. The merchant must pass the confirmation
// word before the rotation happens.
func (h *adminHandler) reissueMerchantSecret(w http.ResponseWriter, r *http.Request) {
	merchantID := r.PathValue("merchantID")
	if !readConfirm(w, r, "reissue") {
		writeError(w, http.StatusBadRequest, "validation_error", "confirmation is required")
		return
	}
	current, err := h.merchants.Get(r.Context(), merchantID)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	result, err := h.merchants.ReissueV3Secret(r.Context(), merchantID)
	if err != nil {
		if errors.Is(err, merchant.ErrV3Unavailable) {
			writeError(w, http.StatusServiceUnavailable, "v3_not_configured", "V3 secret encryption is not configured")
			return
		}
		writeServiceError(w, err)
		return
	}
	if principal, ok := auth.AdminPrincipalFromContext(r.Context()); ok {
		h.adminAudit(principal, "reissue.merchant_secret", "merchant", merchantID, merchantState(current), merchantState(result), r)
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": map[string]any{
		"merchant_id": result.ID,
		"api_secret":  result.APISecret,
	}})
}

// listUsers serves GET /api/v1/admin/users.
func (h *adminHandler) listUsers(w http.ResponseWriter, r *http.Request) {
	merchantID := strings.TrimSpace(r.URL.Query().Get("merchant_id"))
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	status := strings.TrimSpace(r.URL.Query().Get("status"))
	page, limit, _ := queryPage(r)
	items, total, err := h.config.Queries.ListUsers(r.Context(), merchantID, q, status, page, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "could not list users")
		return
	}
	adminList(w, items, total)
}

// getUser serves GET /api/v1/admin/users/{merchantID}/{userID}.
func (h *adminHandler) getUser(w http.ResponseWriter, r *http.Request) {
	merchantID := r.PathValue("merchantID")
	userID := r.PathValue("userID")
	detail, err := h.config.Queries.GetUser(r.Context(), merchantID, userID)
	if err != nil {
		if errors.Is(err, adminquery.ErrNotFound) {
			writeError(w, http.StatusNotFound, "user_not_found", "user was not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "internal_error", "could not load user")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": detail})
}

// listUserTransactions serves GET /api/v1/admin/users/{merchantID}/{userID}/transactions.
func (h *adminHandler) listUserTransactions(w http.ResponseWriter, r *http.Request) {
	merchantID := r.PathValue("merchantID")
	userID := r.PathValue("userID")
	page, limit, _ := queryPage(r)
	items, total, err := h.config.Queries.ListTransactions(r.Context(), merchantID, userID, "", page, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "could not list transactions")
		return
	}
	adminList(w, items, total)
}

// updateUserStatus serves PATCH /api/v1/admin/users/{merchantID}/{userID}/status.
func (h *adminHandler) updateUserStatus(w http.ResponseWriter, r *http.Request) {
	merchantID := r.PathValue("merchantID")
	userID := r.PathValue("userID")
	request, ok := readStatusPayload(w, r, "active", "blocked")
	if !ok {
		return
	}
	current, err := h.config.PlatformUsers.Get(r.Context(), merchantID, userID)
	if err != nil {
		writeUserServiceError(w, err)
		return
	}
	if err := h.config.PlatformUsers.UpdateStatus(r.Context(), merchantID, userID, request.Status); err != nil {
		writeUserServiceError(w, err)
		return
	}
	if principal, ok := auth.AdminPrincipalFromContext(r.Context()); ok {
		h.adminAudit(
			principal,
			"status.user",
			"user",
			merchantID+"/"+userID,
			userState(current),
			userStateFrom(merchantID, userID, request.Status),
			r,
		)
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": map[string]string{
		"merchant_id":      merchantID,
		"external_user_id": userID,
		"status":           request.Status,
	}})
}

// statusPayload is the double-confirm payload shared by the status
// endpoints; the confirmation word must equal the new status.
type statusPayload struct {
	Status string `json:"status"`
}

// readStatusPayload decodes a {status, confirm} payload and verifies the
// double-confirm word. The confirmation word must equal the new status, so
// the payload is peeked before readConfirm consumes (and restores) the
// body; the restored body keeps the payload readable by the follow-up.
// Invalid statuses and confirmation mismatches are 400 validation_error.
func readStatusPayload(w http.ResponseWriter, r *http.Request, allowed ...string) (statusPayload, bool) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, MaxRequestBodyBytes))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "could not read request body")
		return statusPayload{}, false
	}
	_ = r.Body.Close()
	r.Body = io.NopCloser(strings.NewReader(string(body)))

	request := struct {
		Status  string `json:"status"`
		Confirm string `json:"confirm"`
	}{}
	decoder := json.NewDecoder(strings.NewReader(string(body)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return statusPayload{}, false
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "invalid_request", "request body must contain one JSON object")
		return statusPayload{}, false
	}
	valid := false
	for _, candidate := range allowed {
		if request.Status == candidate {
			valid = true
			break
		}
	}
	if !valid {
		writeError(w, http.StatusBadRequest, "validation_error", "status must be one of "+strings.Join(allowed, ", "))
		return statusPayload{}, false
	}
	if !readConfirm(w, r, request.Status) {
		writeError(w, http.StatusBadRequest, "validation_error", "confirmation word must match the new status")
		return statusPayload{}, false
	}
	return statusPayload{Status: request.Status}, true
}

// writeUserServiceError maps platform-user service errors to console
// responses; service internals never leak to clients.
func writeUserServiceError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, platformuser.ErrUserNotFound):
		writeError(w, http.StatusNotFound, "user_not_found", "user was not found")
	case errors.Is(err, platformuser.ErrInvalidUser):
		writeError(w, http.StatusBadRequest, "validation_error", err.Error())
	default:
		writeError(w, http.StatusInternalServerError, "internal_error", "an internal error occurred")
	}
}

// userState renders the audit snapshot of one platform user.
func userState(user platformuser.User) map[string]any {
	return map[string]any{
		"merchant_id":      user.MerchantID,
		"external_user_id": user.ExternalUserID,
		"status":           user.Status,
	}
}

// userStateFrom renders the audit snapshot after a status change.
func userStateFrom(merchantID, userID, status string) map[string]any {
	return map[string]any{
		"merchant_id":      merchantID,
		"external_user_id": userID,
		"status":           status,
	}
}

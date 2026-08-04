package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/afun-game/predictmarket-saas/internal/adminauth"
	"github.com/afun-game/predictmarket-saas/internal/adminquery"
	"github.com/afun-game/predictmarket-saas/internal/auth"
	"github.com/afun-game/predictmarket-saas/internal/event"
	"github.com/afun-game/predictmarket-saas/internal/market"
	"github.com/afun-game/predictmarket-saas/internal/merchant"
	"github.com/afun-game/predictmarket-saas/internal/order"
	"github.com/afun-game/predictmarket-saas/internal/parimutuel"
	"github.com/afun-game/predictmarket-saas/internal/platformuser"
	"github.com/afun-game/predictmarket-saas/internal/settlement"
	"github.com/afun-game/predictmarket-saas/internal/wallet"
	"github.com/afun-game/predictmarket-saas/pkg/types"
	"github.com/nxsky/twill/runtime/middleware"
)

const (
	adminLoginRateLimit = 30
	adminLoginWindow    = time.Minute
	maxAdminListLimit   = 100
)

// AdminConfig wires the admin console backend into the HTTP API. It is
// appended to NewHandler's optionalServices and registered on a type switch.
type AdminConfig struct {
	Accounts      *adminauth.Manager
	Queries       *adminquery.Service
	PlatformUsers platformuser.Repository
	Settlement    settlement.Service
	Parimutuel    parimutuel.Service
}

type adminHandler struct {
	config    AdminConfig
	merchants merchant.Service
	events    event.Service
	markets   market.Service
	orders    order.Service
	wallets   wallet.Service
}

func registerAdminRoutes(
	mux *http.ServeMux,
	config AdminConfig,
	merchantService merchant.Service,
	eventService event.Service,
	marketService market.Service,
	orderService order.Service,
	walletService wallet.Service,
) {
	handler := &adminHandler{
		config:    config,
		merchants: merchantService,
		events:    eventService,
		markets:   marketService,
		orders:    orderService,
		wallets:   walletService,
	}
	session := func(next http.Handler) http.Handler {
		return auth.RequireAdminSession(config.Accounts, next)
	}
	super := func(next http.Handler) http.Handler {
		return auth.RequireAdminRole(adminauth.RoleSuperAdmin, next)
	}

	mux.Handle(
		"POST /api/v1/admin/login",
		middleware.RateLimit(adminLoginRateLimit, adminLoginWindow)(http.HandlerFunc(handler.login)),
	)
	mux.Handle("POST /api/v1/admin/logout", http.HandlerFunc(handler.logout))
	mux.Handle("GET /api/v1/admin/me", session(http.HandlerFunc(handler.me)))

	mux.Handle("GET /api/v1/admin/merchants", session(http.HandlerFunc(handler.listMerchants)))
	mux.Handle("POST /api/v1/admin/merchants", session(super(http.HandlerFunc(handler.createMerchant))))
	mux.Handle("GET /api/v1/admin/merchants/{merchantID}", session(http.HandlerFunc(handler.getMerchant)))
	mux.Handle("PATCH /api/v1/admin/merchants/{merchantID}", session(super(http.HandlerFunc(handler.updateMerchant))))
	mux.Handle("PATCH /api/v1/admin/merchants/{merchantID}/status", session(super(http.HandlerFunc(handler.updateMerchantStatus))))
	mux.Handle("POST /api/v1/admin/merchants/{merchantID}/api-secret/reissue", session(super(http.HandlerFunc(handler.reissueMerchantSecret))))

	mux.Handle("GET /api/v1/admin/users", session(http.HandlerFunc(handler.listUsers)))
	mux.Handle("GET /api/v1/admin/users/{merchantID}/{userID}", session(http.HandlerFunc(handler.getUser)))
	mux.Handle("GET /api/v1/admin/users/{merchantID}/{userID}/transactions", session(http.HandlerFunc(handler.listUserTransactions)))
	mux.Handle("PATCH /api/v1/admin/users/{merchantID}/{userID}/status", session(super(http.HandlerFunc(handler.updateUserStatus))))

	mux.Handle("GET /api/v1/admin/events", session(http.HandlerFunc(handler.listEvents)))
	mux.Handle("GET /api/v1/admin/events/{eventID}", session(http.HandlerFunc(handler.getEvent)))
	mux.Handle("POST /api/v1/admin/events", session(http.HandlerFunc(handler.createEvent)))
	mux.Handle("PATCH /api/v1/admin/events/{eventID}", session(http.HandlerFunc(handler.updateEvent)))
	mux.Handle("PATCH /api/v1/admin/events/{eventID}/status", session(http.HandlerFunc(handler.updateEventStatus)))
	mux.Handle("POST /api/v1/admin/events/{eventID}/resolve", session(super(http.HandlerFunc(handler.resolveEvent))))

	mux.Handle("GET /api/v1/admin/markets", session(http.HandlerFunc(handler.listMarkets)))
	mux.Handle("GET /api/v1/admin/markets/{marketID}", session(http.HandlerFunc(handler.getMarket)))
	mux.Handle("POST /api/v1/admin/markets", session(http.HandlerFunc(handler.createMarket)))
	mux.Handle("PATCH /api/v1/admin/markets/{marketID}/status", session(http.HandlerFunc(handler.updateMarketStatus)))
	mux.Handle("POST /api/v1/admin/markets/{marketID}/liquidity", session(super(http.HandlerFunc(handler.addMarketLiquidity))))
	mux.Handle("POST /api/v1/admin/markets/{marketID}/void", session(super(http.HandlerFunc(handler.voidMarket))))

	mux.Handle("GET /api/v1/admin/orders", session(http.HandlerFunc(handler.listOrders)))
	mux.Handle("GET /api/v1/admin/transactions", session(http.HandlerFunc(handler.listTransactions)))
	mux.Handle("GET /api/v1/admin/audit-logs", session(http.HandlerFunc(handler.listAuditLogs)))
	mux.Handle("GET /api/v1/admin/overview", session(http.HandlerFunc(handler.overview)))

	if config.Parimutuel != nil {
		registerParimutuelRoutes(mux, merchantService, marketService, walletService, config.Parimutuel)
	}
}

// login issues a session cookie after credential verification. The account
// lockout policy lives in adminauth.
func (h *adminHandler) login(w http.ResponseWriter, r *http.Request) {
	request := struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}{}
	if err := decodeJSON(w, r, &request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	token, principal, err := h.config.Accounts.Login(r.Context(), request.Username, request.Password)
	if err != nil {
		switch {
		case errors.Is(err, adminauth.ErrAccountLocked):
			writeError(w, http.StatusLocked, "account_locked", "account is temporarily locked")
		case errors.Is(err, adminauth.ErrAccountDisabled):
			writeError(w, http.StatusForbidden, "account_disabled", "account is disabled")
		case errors.Is(err, adminauth.ErrInvalidCredentials):
			writeError(w, http.StatusUnauthorized, "invalid_credentials", "invalid username or password")
		default:
			writeError(w, http.StatusInternalServerError, "internal_error", "could not log in")
		}
		return
	}
	auth.SetAdminSessionCookie(w, token, isSecureRequest(r))
	writeJSON(w, http.StatusOK, map[string]any{"data": principal})
}

func (h *adminHandler) logout(w http.ResponseWriter, r *http.Request) {
	auth.ClearAdminSessionCookie(w)
	w.WriteHeader(http.StatusNoContent)
}

func (h *adminHandler) me(w http.ResponseWriter, r *http.Request) {
	principal, ok := auth.AdminPrincipalFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "a valid admin session is required")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": principal})
}

// adminAudit records one administrator change best-effort; a failed write
// never fails the original request.
func (h *adminHandler) adminAudit(
	principal *auth.AdminPrincipal,
	action, resource, resourceID string,
	before, after any,
	r *http.Request,
) {
	_ = h.config.Accounts.RecordAction(context.WithoutCancel(r.Context()), adminauth.Action{
		AdminID:    principal.ID,
		Action:     action,
		Resource:   resource,
		ResourceID: resourceID,
		Before:     before,
		After:      after,
		ClientIP:   clientIP(r),
	})
}

// queryPage parses page/limit query parameters with admin list defaults.
func queryPage(r *http.Request) (int, int, error) {
	page, err := queryInt(r, "page")
	if err != nil || page < 1 {
		page = 1
	}
	limit, err := queryInt(r, "limit")
	if err != nil || limit < 1 {
		limit = 20
	}
	if limit > maxAdminListLimit {
		limit = maxAdminListLimit
	}
	return page, limit, nil
}

// adminList writes a paginated list payload.
func adminList(w http.ResponseWriter, items any, total int) {
	writeJSON(w, http.StatusOK, map[string]any{
		"data": map[string]any{"items": items, "total": total},
	})
}

// merchantState renders the admin-facing merchant view (secrets never leave).
func merchantState(merchant *types.Merchant) map[string]any {
	return map[string]any{
		"id":             merchant.ID,
		"name":           merchant.Name,
		"email":          merchant.Email,
		"status":         merchant.Status,
		"currency":       merchant.Currency,
		"timezone":       merchant.Timezone,
		"wallet_mode":    merchant.WalletMode,
		"fee_rate":       merchant.FeeRate,
		"api_key_prefix": merchant.APIKeyPrefix,
		"callback_url":   merchant.CallbackURL,
		"created_at":     merchant.CreatedAt,
	}
}

// readConfirm decodes the confirm field from the request body and restores
// the body for the follow-up decode, so the same JSON can carry the
// confirmation word and the payload.
func readConfirm(w http.ResponseWriter, r *http.Request, expected string) bool {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, MaxRequestBodyBytes))
	if err != nil {
		return false
	}
	_ = r.Body.Close()
	r.Body = io.NopCloser(strings.NewReader(string(body)))
	request := struct {
		Confirm string `json:"confirm"`
	}{}
	if err := json.Unmarshal(body, &request); err != nil {
		return false
	}
	return strings.TrimSpace(request.Confirm) == expected
}

func isSecureRequest(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	return strings.EqualFold(strings.TrimSpace(r.Header.Get("X-Forwarded-Proto")), "https")
}

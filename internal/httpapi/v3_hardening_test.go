package httpapi

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/afun-game/predictmarket-saas/internal/callback"
	"github.com/afun-game/predictmarket-saas/internal/currency"
	"github.com/afun-game/predictmarket-saas/internal/event"
	"github.com/afun-game/predictmarket-saas/internal/market"
	"github.com/afun-game/predictmarket-saas/internal/merchant"
	"github.com/afun-game/predictmarket-saas/internal/order"
	"github.com/afun-game/predictmarket-saas/internal/platformuser"
	"github.com/afun-game/predictmarket-saas/internal/wallet"
	"github.com/afun-game/predictmarket-saas/pkg/types"
)

func TestAllowedClientIP(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		ip      string
		allowed []string
		want    bool
	}{
		{name: "exact match", ip: "203.0.113.9", allowed: []string{"203.0.113.9"}, want: true},
		{name: "cidr match", ip: "10.20.30.40", allowed: []string{"10.0.0.0/8"}, want: true},
		{name: "outside cidr", ip: "172.16.0.1", allowed: []string{"10.0.0.0/8"}, want: false},
		{name: "empty list disabled", ip: "1.2.3.4", allowed: nil, want: false},
		{name: "invalid ip", ip: "not-an-ip", allowed: []string{"1.2.3.4"}, want: false},
		{name: "invalid candidate ignored", ip: "1.2.3.4", allowed: []string{"garbage", "1.2.3.4"}, want: true},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := allowedClientIP(tc.ip, tc.allowed); got != tc.want {
				t.Fatalf("allowedClientIP(%q, %v) = %v, want %v", tc.ip, tc.allowed, got, tc.want)
			}
		})
	}
}

func TestV3IPWhitelistRejectsForeignIP(t *testing.T) {
	t.Parallel()
	handler := newV3HardeningHandler(t, &types.Merchant{
		ID:         "merchant-1",
		Status:     "active",
		Currency:   "USD",
		WalletMode: "transfer",
		AllowedIPs: []string{"203.0.113.0/24"},
	}, nil)
	body := []byte(`{"user_id":"site-user-1","currency":"USD"}`)
	recorder := signedV3Request(t, handler, http.MethodPost, "/api/v2/sessions", body, "nonce-1")
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("session status = %d, want 403; body = %s", recorder.Code, recorder.Body.String())
	}
}

func TestV3IPWhitelistAllowsMatchingIP(t *testing.T) {
	t.Parallel()
	handler := newV3HardeningHandler(t, &types.Merchant{
		ID:         "merchant-1",
		Status:     "active",
		Currency:   "USD",
		WalletMode: "transfer",
		AllowedIPs: []string{"127.0.0.1"},
	}, nil)
	body := []byte(`{"user_id":"site-user-1","currency":"USD"}`)
	request := httptest.NewRequest(http.MethodPost, "/api/v2/sessions", bytes.NewReader(body))
	request.RemoteAddr = "127.0.0.1:54321"
	request.Header.Set("Authorization", "Bearer v3-api-key")
	request.Header.Set("X-PM-Timestamp", strconv.FormatInt(time.Now().Unix(), 10))
	request.Header.Set("X-PM-Signature", "not-checked-by-stub")
	request.Header.Set("Idempotency-Key", "nonce-1")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("session status = %d, want 201; body = %s", recorder.Code, recorder.Body.String())
	}
}

func TestV3SeamlessLaunchRequiresBalance(t *testing.T) {
	t.Parallel()
	handler := newV3HardeningHandler(t, &types.Merchant{
		ID:         "merchant-1",
		Status:     "active",
		Currency:   "USD",
		WalletMode: "seamless",
	}, nil)
	recorder := signedV3Request(
		t,
		handler,
		http.MethodPost,
		"/api/v2/sessions",
		[]byte(`{"user_id":"site-user-1","currency":"USD"}`),
		"nonce-1",
	)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("session status = %d, want 400; body = %s", recorder.Code, recorder.Body.String())
	}
}

func TestWriteSeamlessOrderErrorMapsHardeningErrors(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		err  error
		want int
	}{
		{name: "degraded", err: callback.ErrMerchantDegraded, want: http.StatusServiceUnavailable},
		{name: "unverified", err: callback.ErrCallbackUnverified, want: http.StatusConflict},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			recorder := httptest.NewRecorder()
			writeSeamlessOrderError(recorder, tc.err, "USD")
			if recorder.Code != tc.want {
				t.Fatalf("writeSeamlessOrderError(%v) status = %d, want %d", tc.err, recorder.Code, tc.want)
			}
		})
	}
}

type fakeSettlementService struct {
	voided []string
}

func (f *fakeSettlementService) SettleEvent(context.Context, string) error { return nil }

func (f *fakeSettlementService) VoidMarket(_ context.Context, marketID string) error {
	f.voided = append(f.voided, marketID)
	return nil
}

func TestAdminVoidMarketEndpoint(t *testing.T) {
	t.Parallel()
	settler := &fakeSettlementService{}
	merchantService := merchant.NewService()
	eventService := event.NewService()
	marketService := market.NewService()
	walletService := wallet.NewService()
	orderService := order.NewServiceWithDependencies(marketService, walletService)
	manager, token, _ := newAdminSession(t)
	handler := NewHandler(
		merchantService,
		eventService,
		marketService,
		walletService,
		orderService,
		currency.NewService(),
		"admin-secret",
		AdminConfig{Accounts: manager, Settlement: settler},
	)
	recorder := adminRequest(
		t,
		handler,
		http.MethodPost,
		"/api/v1/admin/markets/00000000-0000-0000-0000-000000000000/void",
		[]byte(`{"confirm":"void"}`),
		token,
	)
	if recorder.Code != http.StatusOK {
		t.Fatalf("void market status = %d, want 200; body = %s", recorder.Code, recorder.Body.String())
	}
	if len(settler.voided) != 1 {
		t.Fatalf("voided markets = %v", settler.voided)
	}
}

func newV3HardeningHandler(t *testing.T, signerMerchant *types.Merchant, extra ...any) http.Handler {
	t.Helper()
	merchantService := merchant.NewService()
	eventService := event.NewService()
	marketService := market.NewService()
	walletService := wallet.NewService()
	orderService := order.NewServiceWithDependencies(marketService, walletService)
	signer := &signedMerchantStub{merchant: signerMerchant}
	optional := []any{V3Config{
		Authenticator:   signer,
		Sessions:        v3TestSessionManager(t),
		PlatformUsers:   platformuser.NewMemoryRepository(),
		HostedLaunchURL: "https://play.example/launch",
	}}
	optional = append(optional, extra...)
	return NewHandler(
		merchantService,
		eventService,
		marketService,
		walletService,
		orderService,
		currency.NewService(),
		"admin-secret",
		optional...,
	)
}

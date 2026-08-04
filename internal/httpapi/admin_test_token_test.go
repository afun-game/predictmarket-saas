package httpapi

import (
	"bytes"
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/afun-game/predictmarket-saas/internal/adminauth"
	"github.com/afun-game/predictmarket-saas/internal/currency"
	"github.com/afun-game/predictmarket-saas/internal/event"
	"github.com/afun-game/predictmarket-saas/internal/market"
	"github.com/afun-game/predictmarket-saas/internal/merchant"
	"github.com/afun-game/predictmarket-saas/internal/order"
	"github.com/afun-game/predictmarket-saas/internal/platformuser"
	"github.com/afun-game/predictmarket-saas/internal/wallet"
)

// newTestTokenHandler wires the admin console with a real session manager so
// the generated launch token can be exchanged end to end.
func newTestTokenHandler(t *testing.T, role string) http.Handler {
	t.Helper()
	repo := adminauth.NewMemoryRepository()
	logs := adminauth.NewMemoryActionLog()
	account := adminMerchantsTestAccount("boss", "pw", role)
	if err := repo.Create(context.Background(), account); err != nil {
		t.Fatal(err)
	}
	manager, err := adminauth.NewManager(repo, logs, bytes.Repeat([]byte("k"), 32))
	if err != nil {
		t.Fatal(err)
	}
	return NewHandler(
		merchant.NewService(),
		event.NewService(),
		market.NewService(),
		wallet.NewService(),
		order.NewService(),
		currency.NewService(),
		"admin-secret",
		AdminConfig{
			Accounts:        manager,
			PlatformUsers:   platformuser.NewMemoryRepository(),
			Sessions:        v3TestSessionManager(t),
			HostedLaunchURL: "https://play.e2e.test/launch",
		},
	)
}

func TestAdminTestTokenGeneratesExchangableLaunchURL(t *testing.T) {
	t.Parallel()
	handler := newTestTokenHandler(t, adminauth.RoleSuperAdmin)
	cookie := adminMerchantsTestLogin(t, &adminMerchantsTestEnv{handler: handler}, "boss", "pw")

	// Create a merchant to hold the session.
	created := adminMerchantsTestRequest(
		t, handler, http.MethodPost, "/api/v1/admin/merchants",
		[]byte(`{"name":"测试商户","email":"tt@test.dev","currency":"USD","timezone":"UTC"}`),
		cookie,
	)
	var decoded struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	adminMerchantsTestDecode(t, created, &decoded)

	response := adminMerchantsTestRequest(
		t, handler, http.MethodPost, "/api/v1/admin/test-token",
		[]byte(`{"merchant_id":"`+decoded.Data.ID+`","user_id":"frontend-tester"}`),
		cookie,
	)
	if response.Code != http.StatusCreated {
		t.Fatalf("test token status = %d, body = %s", response.Code, response.Body.String())
	}
	var result struct {
		Data struct {
			LaunchURL  string `json:"launch_url"`
			Token      string `json:"token"`
			MerchantID string `json:"merchant_id"`
			UserID     string `json:"user_id"`
			WalletMode string `json:"wallet_mode"`
		} `json:"data"`
	}
	adminMerchantsTestDecode(t, response, &result)
	if !strings.HasPrefix(result.Data.Token, "lt_") {
		t.Fatalf("token = %q, want lt_ prefix", result.Data.Token)
	}
	if !strings.Contains(result.Data.LaunchURL, "https://play.e2e.test/launch?token="+result.Data.Token) {
		t.Fatalf("launch_url = %q", result.Data.LaunchURL)
	}
	if result.Data.MerchantID != decoded.Data.ID || result.Data.UserID != "frontend-tester" || result.Data.WalletMode != "transfer" {
		t.Fatalf("result = %+v", result.Data)
	}
}

func TestAdminTestTokenRequiresSuperAdmin(t *testing.T) {
	t.Parallel()
	handler := newTestTokenHandler(t, adminauth.RoleOperator)
	cookie := adminMerchantsTestLogin(t, &adminMerchantsTestEnv{handler: handler}, "boss", "pw")
	response := adminMerchantsTestRequest(
		t, handler, http.MethodPost, "/api/v1/admin/test-token",
		[]byte(`{"merchant_id":"00000000-0000-0000-0000-000000000000"}`),
		cookie,
	)
	if response.Code != http.StatusForbidden {
		t.Fatalf("operator test token status = %d, want 403", response.Code)
	}
}

func TestAdminTestTokenRejectsMissingMerchant(t *testing.T) {
	t.Parallel()
	handler := newTestTokenHandler(t, adminauth.RoleSuperAdmin)
	cookie := adminMerchantsTestLogin(t, &adminMerchantsTestEnv{handler: handler}, "boss", "pw")
	response := adminMerchantsTestRequest(
		t, handler, http.MethodPost, "/api/v1/admin/test-token",
		[]byte(`{}`),
		cookie,
	)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("missing merchant status = %d, want 400", response.Code)
	}
}

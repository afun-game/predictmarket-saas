package httpapi

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/afun-game/predictmarket-saas/internal/currency"
	"github.com/afun-game/predictmarket-saas/internal/event"
	"github.com/afun-game/predictmarket-saas/internal/market"
	"github.com/afun-game/predictmarket-saas/internal/merchant"
	"github.com/afun-game/predictmarket-saas/internal/order"
	"github.com/afun-game/predictmarket-saas/internal/wallet"
)

func TestWalletHTTPFlow(t *testing.T) {
	t.Parallel()

	handler := NewHandler(
		merchant.NewService(),
		event.NewService(),
		market.NewService(),
		wallet.NewService(),
		order.NewService(),
		currency.NewService(),
		"admin-secret",
	)
	credentials := registerMerchant(t, handler, "Wallet Tenant", "wallet-http@example.test")
	authorization := "Bearer " + credentials.Data.APIKey

	response := performRequest(
		t,
		handler,
		http.MethodGet,
		"/api/v1/wallets/user-1",
		nil,
		authorization,
	)
	if response.Code != http.StatusNotFound {
		t.Fatalf("missing wallet status = %d, want %d", response.Code, http.StatusNotFound)
	}

	response = performRequest(
		t,
		handler,
		http.MethodPost,
		"/api/v1/wallets/user-1/credit",
		[]byte(`{"currency":"USD","amount":100,"type":"admin_credit"}`),
		authorization,
	)
	if response.Code != http.StatusOK {
		t.Fatalf("credit USD status = %d, body = %s", response.Code, response.Body.String())
	}
	assertCreditedWallet(t, response.Body.Bytes(), "USD", 100)
	response = performRequest(
		t,
		handler,
		http.MethodPost,
		"/api/v1/wallets/user-1/credit",
		[]byte(`{"currency":"EUR","amount":25.50,"type":"credit"}`),
		authorization,
	)
	if response.Code != http.StatusOK {
		t.Fatalf("credit EUR status = %d, body = %s", response.Code, response.Body.String())
	}

	response = performRequest(
		t,
		handler,
		http.MethodGet,
		"/api/v1/wallets/user-1",
		nil,
		authorization,
	)
	if response.Code != http.StatusOK {
		t.Fatalf("get default balance status = %d, body = %s", response.Code, response.Body.String())
	}
	assertBalanceResponse(t, response.Body.Bytes(), "user-1", "USD", 100, 0)
	response = performRequest(
		t,
		handler,
		http.MethodGet,
		"/api/v1/wallets/user-1?currency=EUR",
		nil,
		authorization,
	)
	if response.Code != http.StatusOK {
		t.Fatalf("get EUR balance status = %d, body = %s", response.Code, response.Body.String())
	}
	assertBalanceResponse(t, response.Body.Bytes(), "user-1", "EUR", 25.50, 0)

	response = performRequest(
		t,
		handler,
		http.MethodGet,
		"/api/v1/wallets/user-1/transactions?page=1&limit=1",
		nil,
		authorization,
	)
	if response.Code != http.StatusOK {
		t.Fatalf("list transactions status = %d, body = %s", response.Code, response.Body.String())
	}
	assertTransactionResponse(t, response.Body.Bytes(), 1, 2)
}

func TestWalletHTTPAuthorizationAndTenantIsolation(t *testing.T) {
	t.Parallel()

	handler := NewHandler(
		merchant.NewService(),
		event.NewService(),
		market.NewService(),
		wallet.NewService(),
		order.NewService(),
		currency.NewService(),
		"admin-secret",
	)
	first := registerMerchant(t, handler, "First Wallet Tenant", "first-wallet@example.test")
	second := registerMerchant(t, handler, "Second Wallet Tenant", "second-wallet@example.test")
	firstAuthorization := "Bearer " + first.Data.APIKey
	secondAuthorization := "Bearer " + second.Data.APIKey

	response := performRequest(
		t,
		handler,
		http.MethodPost,
		"/api/v1/wallets/shared-user/credit",
		[]byte(`{"currency":"USD","amount":10}`),
		firstAuthorization,
	)
	if response.Code != http.StatusOK {
		t.Fatalf("credit status = %d, body = %s", response.Code, response.Body.String())
	}
	response = performRequest(
		t,
		handler,
		http.MethodGet,
		"/api/v1/wallets/shared-user",
		nil,
		secondAuthorization,
	)
	if response.Code != http.StatusNotFound {
		t.Errorf("cross-tenant balance status = %d, want %d", response.Code, http.StatusNotFound)
	}
	response = performRequest(
		t,
		handler,
		http.MethodGet,
		"/api/v1/wallets/shared-user/transactions",
		nil,
		secondAuthorization,
	)
	if response.Code != http.StatusOK {
		t.Fatalf("cross-tenant history status = %d, body = %s", response.Code, response.Body.String())
	}
	assertTransactionResponse(t, response.Body.Bytes(), 0, 0)

	response = performRequest(
		t,
		handler,
		http.MethodPost,
		"/api/v1/wallets/shared-user/credit",
		[]byte(`{"currency":"USD","amount":1}`),
		"",
	)
	if response.Code != http.StatusUnauthorized {
		t.Errorf("unauthenticated credit status = %d, want %d", response.Code, http.StatusUnauthorized)
	}
	response = performRequest(
		t,
		handler,
		http.MethodPost,
		"/api/v1/wallets/shared-user/credit",
		[]byte(`{"currency":"USD","amount":1,"type":"win"}`),
		firstAuthorization,
	)
	if response.Code != http.StatusBadRequest {
		t.Errorf("invalid credit type status = %d, want %d", response.Code, http.StatusBadRequest)
	}
	response = performRequest(
		t,
		handler,
		http.MethodPost,
		"/api/v1/wallets/shared-user/credit",
		[]byte(`{"currency":"USD","amount":0}`),
		firstAuthorization,
	)
	if response.Code != http.StatusBadRequest {
		t.Errorf("invalid credit amount status = %d, want %d", response.Code, http.StatusBadRequest)
	}
}

func TestWalletHTTPRequiresAndReusesIdempotencyKey(t *testing.T) {
	t.Parallel()
	handler := NewHandler(
		merchant.NewService(),
		event.NewService(),
		market.NewService(),
		wallet.NewService(),
		order.NewService(),
		currency.NewService(),
		"admin-secret",
	)
	credentials := registerMerchant(t, handler, "Idempotent Wallet Tenant", "idempotent-wallet@example.test")
	authorization := "Bearer " + credentials.Data.APIKey
	body := []byte(`{"currency":"USD","amount":10}`)
	path := "/api/v1/wallets/idempotent-user/credit"

	missingKey := performRequestWithIdempotency(t, handler, http.MethodPost, path, body, authorization, "")
	if missingKey.Code != http.StatusPreconditionRequired {
		t.Fatalf("missing idempotency key status = %d, body = %s", missingKey.Code, missingKey.Body.String())
	}
	first := performRequestWithIdempotency(t, handler, http.MethodPost, path, body, authorization, "wallet-retry-key")
	second := performRequestWithIdempotency(t, handler, http.MethodPost, path, body, authorization, "wallet-retry-key")
	if first.Code != http.StatusOK || second.Code != http.StatusOK {
		t.Fatalf("idempotent credit statuses = (%d, %d)", first.Code, second.Code)
	}
	response := performRequest(
		t,
		handler,
		http.MethodGet,
		"/api/v1/wallets/idempotent-user",
		nil,
		authorization,
	)
	assertBalanceResponse(t, response.Body.Bytes(), "idempotent-user", "USD", 10, 0)
	transactions := performRequest(
		t,
		handler,
		http.MethodGet,
		"/api/v1/wallets/idempotent-user/transactions",
		nil,
		authorization,
	)
	assertTransactionResponse(t, transactions.Body.Bytes(), 1, 1)
}

func assertCreditedWallet(t *testing.T, body []byte, currency string, balance float64) {
	t.Helper()
	var response struct {
		Data struct {
			Currency string  `json:"currency"`
			Balance  float64 `json:"balance"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		t.Fatalf("decode credited wallet: %v", err)
	}
	if response.Data.Currency != currency || response.Data.Balance != balance {
		t.Errorf("credited wallet = %#v", response.Data)
	}
}

func assertBalanceResponse(
	t *testing.T,
	body []byte,
	userID string,
	currency string,
	available float64,
	locked float64,
) {
	t.Helper()
	var response struct {
		Data struct {
			UserID   string          `json:"user_id"`
			Balances []walletBalance `json:"balances"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		t.Fatalf("decode wallet balance: %v", err)
	}
	if response.Data.UserID != userID || len(response.Data.Balances) != 1 {
		t.Fatalf("wallet balance response = %#v", response.Data)
	}
	balance := response.Data.Balances[0]
	validAmounts := balance.Available == available && balance.Locked == locked
	if balance.Currency != currency || !validAmounts || balance.Total != available+locked {
		t.Errorf("wallet balance = %#v", balance)
	}
}

func assertTransactionResponse(t *testing.T, body []byte, length, total int) {
	t.Helper()
	var response struct {
		Data []struct {
			Type   string  `json:"type"`
			Amount float64 `json:"amount"`
		} `json:"data"`
		Meta struct {
			Pagination pagination `json:"pagination"`
		} `json:"meta"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		t.Fatalf("decode wallet transactions: %v", err)
	}
	if len(response.Data) != length || response.Meta.Pagination.Total != total {
		t.Errorf("transactions len = %d, pagination = %#v", len(response.Data), response.Meta.Pagination)
	}
	for _, transaction := range response.Data {
		if transaction.Type != "credit" || transaction.Amount <= 0 {
			t.Errorf("transaction = %#v", transaction)
		}
	}
}

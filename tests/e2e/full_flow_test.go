package e2e

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/afun-game/predictmarket-saas/internal/auth"
	"time"

	"github.com/google/uuid"
	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/afun-game/predictmarket-saas/pkg/fixed"
)

const defaultDatabaseURL = "postgres://predictmarket:password@localhost:5432/predictmarket?sslmode=disable"

func TestFullTradingSettlementFlow(t *testing.T) {
	if os.Getenv("E2E_TEST") != "1" {
		t.Skip("set E2E_TEST=1 and start the application to run end-to-end tests")
	}
	baseURL := environmentOrDefault("E2E_BASE_URL", "http://localhost:8080")
	adminKey := environmentOrDefault("ADMIN_API_KEY", "e2e-admin-secret")
	suffix := uuid.NewString()
	email := "e2e-" + suffix + "@example.test"
	sourceID := "e2e-" + suffix
	database := openDatabase(t)
	t.Cleanup(func() { cleanupFixture(t, database, email, sourceID) })

	merchant := registerMerchant(t, baseURL, email)
	eventID := createEvent(t, baseURL, adminKey, sourceID)
	activateEvent(t, baseURL, adminKey, eventID)
	marketID := createMarket(t, baseURL, adminKey, merchant.ID, eventID)

	users := []tradingUser{
		{ID: "usd-winner", Currency: "USD", Side: "buy"},
		{ID: "usd-loser", Currency: "USD", Side: "sell"},
		{ID: "eur-winner", Currency: "EUR", Side: "buy"},
		{ID: "eur-loser", Currency: "EUR", Side: "sell"},
	}
	for _, user := range users {
		creditWallet(t, baseURL, merchant.APIKey, user, 100)
	}
	for _, currency := range []string{"USD", "EUR"} {
		placeOrder(t, baseURL, merchant.APIKey, marketID, tradingUser{
			ID: currencyUser(currency, "loser"), Currency: currency, Side: "sell",
		}, 0.4)
		placeOrder(t, baseURL, merchant.APIKey, marketID, tradingUser{
			ID: currencyUser(currency, "winner"), Currency: currency, Side: "buy",
		}, 0.6)
	}

	closeAndResolveEvent(t, baseURL, adminKey, eventID, "Yes")
	waitForSettlement(t, baseURL, merchant.APIKey, marketID)

	for _, user := range users {
		wantAvailable := 94.0
		if user.Side == "buy" {
			wantAvailable = 106
		}
		assertWallet(t, baseURL, merchant.APIKey, user, wantAvailable, 0)
	}
	assertSettlementAudit(t, database, marketID)
}

func TestHostedLaunchFlow(t *testing.T) {
	if os.Getenv("E2E_TEST") != "1" {
		t.Skip("set E2E_TEST=1 and start the application to run end-to-end tests")
	}
	baseURL := environmentOrDefault("E2E_BASE_URL", "http://localhost:8080")
	adminKey := environmentOrDefault("ADMIN_API_KEY", "e2e-admin-secret")
	suffix := uuid.NewString()
	email := "e2e-hosted-" + suffix + "@example.test"
	sourceID := "e2e-hosted-" + suffix
	database := openDatabase(t)
	t.Cleanup(func() { cleanupFixture(t, database, email, sourceID) })

	merchant := registerMerchant(t, baseURL, email)
	eventID := createEvent(t, baseURL, adminKey, sourceID)
	activateEvent(t, baseURL, adminKey, eventID)
	marketID := createMarket(t, baseURL, adminKey, merchant.ID, eventID)
	sessionID, launchToken := createLaunch(t, baseURL, merchant, "hosted-user")
	accessToken := exchangeLaunch(t, baseURL, launchToken)
	transferDeposit(t, baseURL, merchant, "hosted-user", "hosted-deposit", "25.00")

	var profile struct {
		Data struct {
			UserID     string `json:"user_id"`
			WalletMode string `json:"wallet_mode"`
		} `json:"data"`
	}
	doJSON(t, requestSpec{
		Method: http.MethodGet, URL: baseURL + "/api/user/me", Token: accessToken,
		WantStatus: http.StatusOK, Destination: &profile,
	})
	if profile.Data.UserID != "hosted-user" || profile.Data.WalletMode != "transfer" {
		t.Errorf("hosted profile = %#v", profile.Data)
	}

	var events struct {
		Data []struct {
			ID       string `json:"id"`
			Category string `json:"category"`
		} `json:"data"`
	}
	doJSON(t, requestSpec{
		Method: http.MethodGet, URL: baseURL + "/api/user/events?status=active&limit=100", Token: accessToken,
		WantStatus: http.StatusOK, Destination: &events,
	})
	if !containsHostedEvent(events.Data, eventID, "sports") {
		t.Errorf("hosted events did not include fixture event %q: %#v", eventID, events.Data)
	}

	var markets struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	doJSON(t, requestSpec{
		Method: http.MethodGet, URL: baseURL + "/api/user/markets?event_id=" + eventID, Token: accessToken,
		WantStatus: http.StatusOK, Destination: &markets,
	})
	if len(markets.Data) != 1 || markets.Data[0].ID != marketID {
		t.Errorf("hosted markets = %#v, want market %q", markets.Data, marketID)
	}

	var createdOrder struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	doJSON(t, requestSpec{
		Method: http.MethodPost, URL: baseURL + "/api/user/orders", Token: accessToken,
		IdempotencyKey: "hosted-order-001",
		Body: map[string]any{
			"market_id": marketID, "type": "buy", "option": "Yes", "amount": 10,
			"price": 0.5, "time_in_force": "gtc",
		},
		WantStatus: http.StatusCreated, Destination: &createdOrder,
	})
	if createdOrder.Data.ID == "" {
		t.Fatal("hosted order response has no ID")
	}
	var hostedOrders struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	doJSON(t, requestSpec{
		Method: http.MethodGet, URL: baseURL + "/api/user/orders?limit=500", Token: accessToken,
		WantStatus: http.StatusOK, Destination: &hostedOrders,
	})
	if len(hostedOrders.Data) != 1 || hostedOrders.Data[0].ID != createdOrder.Data.ID {
		t.Errorf("hosted orders = %#v", hostedOrders.Data)
	}
	doJSON(t, requestSpec{
		Method: http.MethodGet, URL: baseURL + "/api/user/orders/" + createdOrder.Data.ID + "/trades", Token: accessToken,
		WantStatus: http.StatusOK,
	})
	assertSignedV2Status(t, http.MethodGet, baseURL+"/api/v2/orders?user_id=hosted-user&limit=500", merchant, "", nil, http.StatusOK)
	assertSignedV2Status(t, http.MethodGet, baseURL+"/api/v2/transactions?user_id=hosted-user&limit=500", merchant, "", nil, http.StatusOK)
	assertSignedV2Status(
		t,
		http.MethodGet,
		baseURL+"/api/v2/reports/daily?date="+time.Now().UTC().Format("2006-01-02")+"&currency=USD",
		merchant,
		"",
		nil,
		http.StatusOK,
	)
	doJSON(t, requestSpec{
		Method: http.MethodDelete, URL: baseURL + "/api/user/orders/" + createdOrder.Data.ID, Token: accessToken,
		IdempotencyKey: "hosted-cancel-001", WantStatus: http.StatusOK,
	})

	revokeLaunchSession(t, baseURL, merchant, sessionID)
	status := doJSONStatus(t, requestSpec{Method: http.MethodGet, URL: baseURL + "/api/user/me", Token: accessToken})
	if status != http.StatusUnauthorized {
		t.Errorf("hosted profile after revoke status = %d, want %d", status, http.StatusUnauthorized)
	}
}

func TestVoidMarketRefundsAndWebhooks(t *testing.T) {
	if os.Getenv("E2E_TEST") != "1" {
		t.Skip("set E2E_TEST=1 and start the application to run end-to-end tests")
	}
	baseURL := environmentOrDefault("E2E_BASE_URL", "http://localhost:8080")
	adminKey := environmentOrDefault("ADMIN_API_KEY", "e2e-admin-secret")
	suffix := uuid.NewString()
	email := "e2e-void-" + suffix + "@example.test"
	sourceID := "e2e-void-" + suffix
	database := openDatabase(t)
	t.Cleanup(func() { cleanupFixture(t, database, email, sourceID) })

	merchant := registerMerchant(t, baseURL, email)
	eventID := createEvent(t, baseURL, adminKey, sourceID)
	activateEvent(t, baseURL, adminKey, eventID)
	marketID := createMarket(t, baseURL, adminKey, merchant.ID, eventID)
	transferDeposit(t, baseURL, merchant, "void-user", "void-deposit-001", "50.00")
	configureTransferWebhooks(t, baseURL, adminKey, merchant.ID)

	orderBody, err := json.Marshal(map[string]any{
		"market_id": marketID, "user_id": "void-user", "type": "buy", "option": "Yes",
		"amount": 10, "currency": "USD", "price": 0.5, "time_in_force": "gtc",
	})
	if err != nil {
		t.Fatalf("marshal void order: %v", err)
	}
	var created struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	createdResponse := signedV3Request(t, http.MethodPost, baseURL+"/api/v2/orders", merchant, "void-order-001", orderBody)
	createdBody, _ := io.ReadAll(createdResponse.Body)
	_ = createdResponse.Body.Close()
	if createdResponse.StatusCode != http.StatusCreated {
		t.Fatalf("create void order status = %d, body = %s", createdResponse.StatusCode, createdBody)
	}
	if err := json.Unmarshal(createdBody, &created); err != nil || created.Data.ID == "" {
		t.Fatalf("decode void order response: %v body=%s", err, createdBody)
	}

	doJSON(t, requestSpec{
		Method: http.MethodPost, URL: baseURL + "/api/v1/admin/markets/" + marketID + "/void",
		Cookie: adminSession(t, baseURL), Body: map[string]any{"confirm": "void"}, WantStatus: http.StatusOK,
	})
	// Voiding twice must be idempotently rejected.
	doJSON(t, requestSpec{
		Method: http.MethodPost, URL: baseURL + "/api/v1/admin/markets/" + marketID + "/void",
		Cookie: adminSession(t, baseURL), Body: map[string]any{"confirm": "void"}, WantStatus: http.StatusConflict,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var orderStatus string
	if err := database.QueryRowContext(ctx, `
SELECT status FROM orders WHERE id = $1`, created.Data.ID).Scan(&orderStatus); err != nil {
		t.Fatalf("query voided order status: %v", err)
	}
	if orderStatus != "voided" {
		t.Fatalf("voided order status = %q, want voided", orderStatus)
	}
	var marketStatus string
	if err := database.QueryRowContext(ctx, `
SELECT status FROM markets WHERE id = $1`, marketID).Scan(&marketStatus); err != nil {
		t.Fatalf("query voided market status: %v", err)
	}
	if marketStatus != "voided" {
		t.Fatalf("voided market status = %q, want voided", marketStatus)
	}
	var settlementType string
	if err := database.QueryRowContext(ctx, `
SELECT settlement_type FROM market_settlements WHERE market_id = $1`, marketID).Scan(&settlementType); err != nil {
		t.Fatalf("query market settlement type: %v", err)
	}
	if settlementType != "void" {
		t.Fatalf("market settlement type = %q, want void", settlementType)
	}
	var webhookTypes []string
	rows, err := database.QueryContext(ctx, `
SELECT event_type FROM webhook_outbox
WHERE merchant_id = $1 AND event_type IN ('order.voided', 'market.voided')`, merchant.ID)
	if err != nil {
		t.Fatalf("query void webhooks: %v", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var eventType string
		if err := rows.Scan(&eventType); err != nil {
			t.Fatalf("scan void webhook: %v", err)
		}
		webhookTypes = append(webhookTypes, eventType)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate void webhooks: %v", err)
	}
	if !containsString(webhookTypes, "order.voided") || !containsString(webhookTypes, "market.voided") {
		t.Fatalf("void webhooks = %v, want order.voided and market.voided", webhookTypes)
	}

	var settlementPage struct {
		Data []struct {
			MarketID       string `json:"market_id"`
			SettlementType string `json:"settlement_type"`
		} `json:"data"`
	}
	settlementResponse := signedV3Request(
		t, http.MethodGet, baseURL+"/api/v2/settlements?limit=500", merchant, "", nil,
	)
	settlementBody, _ := io.ReadAll(settlementResponse.Body)
	_ = settlementResponse.Body.Close()
	if settlementResponse.StatusCode != http.StatusOK {
		t.Fatalf("settlements status = %d, body = %s", settlementResponse.StatusCode, settlementBody)
	}
	if err := json.Unmarshal(settlementBody, &settlementPage); err != nil {
		t.Fatalf("decode settlements: %v", err)
	}
	foundVoid := false
	for _, settlementValue := range settlementPage.Data {
		if settlementValue.MarketID == marketID && settlementValue.SettlementType == "void" {
			foundVoid = true
		}
	}
	if !foundVoid {
		t.Fatalf("settlement page did not include voided market %s: %#v", marketID, settlementPage.Data)
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func TestSeamlessConcurrentOrderLoad(t *testing.T) {
	if os.Getenv("E2E_TEST") != "1" {
		t.Skip("set E2E_TEST=1 and start the application to run end-to-end tests")
	}
	if !environmentBool("V3_ALLOW_PRIVATE_CALLBACK_URLS") {
		t.Skip("set V3_ALLOW_PRIVATE_CALLBACK_URLS=1 to run the local seamless load test")
	}
	baseURL := environmentOrDefault("E2E_BASE_URL", "http://localhost:8080")
	adminKey := environmentOrDefault("ADMIN_API_KEY", "e2e-admin-secret")
	suffix := uuid.NewString()
	email := "e2e-load-" + suffix + "@example.test"
	sourceID := "e2e-load-" + suffix
	database := openDatabase(t)
	t.Cleanup(func() { cleanupFixture(t, database, email, sourceID) })

	merchant := registerMerchant(t, baseURL, email)
	eventID := createEvent(t, baseURL, adminKey, sourceID)
	activateEvent(t, baseURL, adminKey, eventID)
	marketID := createMarket(t, baseURL, adminKey, merchant.ID, eventID)

	loadState := &seamlessLoadState{balanceCents: 100_000}
	callbackServer := httptest.NewTLSServer(http.HandlerFunc(loadState.handle))
	t.Cleanup(callbackServer.Close)
	configureSeamlessIntegration(t, baseURL, adminKey, merchant.ID, callbackServer.URL)

	const totalOrders = 1000
	postSeamlessLoadOrders(t, baseURL, merchant, marketID, totalOrders)

	waitForShadowDriftZero(t, baseURL)
	assertSeamlessLoadState(t, database, merchant.ID, marketID, totalOrders)

	loadState.mu.Lock()
	requests := loadState.requests
	balance := loadState.balanceCents
	loadState.mu.Unlock()
	if requests != totalOrders {
		t.Fatalf("callback requests = %d, want %d", requests, totalOrders)
	}
	if balance != 50_000 {
		t.Fatalf("merchant balance = %d cents, want 50000", balance)
	}
}

func transferDeposit(
	t *testing.T,
	baseURL string,
	merchant merchantCredentials,
	userID string,
	merchantTransactionID string,
	amount string,
) {
	t.Helper()
	body, err := json.Marshal(map[string]string{
		"merchant_txn_id": merchantTransactionID,
		"currency":        "USD",
		"amount":          amount,
	})
	if err != nil {
		t.Fatalf("marshal transfer deposit: %v", err)
	}
	response := signedV3Request(
		t,
		http.MethodPost,
		baseURL+"/api/v2/users/"+userID+"/deposits",
		merchant,
		"transfer-header-"+merchantTransactionID,
		body,
	)
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusCreated {
		encoded, _ := io.ReadAll(response.Body)
		t.Fatalf("transfer deposit status = %d, body = %s", response.StatusCode, encoded)
	}
}

func assertSignedV2Status(
	t *testing.T,
	method string,
	requestURL string,
	merchant merchantCredentials,
	idempotencyKey string,
	body []byte,
	wantStatus int,
) {
	t.Helper()
	response := signedV3Request(t, method, requestURL, merchant, idempotencyKey, body)
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != wantStatus {
		encoded, _ := io.ReadAll(response.Body)
		t.Fatalf("signed V2 %s status = %d, want %d, body = %s", requestURL, response.StatusCode, wantStatus, encoded)
	}
}

type merchantCredentials struct {
	ID        string
	APIKey    string
	APISecret string
}

type tradingUser struct {
	ID       string
	Currency string
	Side     string
}

func registerMerchant(t *testing.T, baseURL, email string) merchantCredentials {
	t.Helper()
	var response struct {
		Data struct {
			MerchantID string `json:"merchant_id"`
			APIKey     string `json:"api_key"`
			APISecret  string `json:"api_secret"`
		} `json:"data"`
	}
	doJSON(t, requestSpec{
		Method: http.MethodPost, URL: baseURL + "/api/v1/merchants/register",
		Body: map[string]any{
			"name": "E2E Merchant", "email": email, "currency": "USD", "timezone": "UTC",
		},
		WantStatus: http.StatusCreated, Destination: &response,
	})
	return merchantCredentials{
		ID:        response.Data.MerchantID,
		APIKey:    response.Data.APIKey,
		APISecret: response.Data.APISecret,
	}
}

func createLaunch(t *testing.T, baseURL string, merchant merchantCredentials, userID string) (string, string) {
	t.Helper()
	body, err := json.Marshal(map[string]string{
		"user_id":  userID,
		"currency": "USD",
		"locale":   "zh-CN",
	})
	if err != nil {
		t.Fatalf("marshal launch request: %v", err)
	}
	response := signedV3Request(t, http.MethodPost, baseURL+"/api/v2/sessions", merchant, uuid.NewString(), body)
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusCreated {
		encoded, _ := io.ReadAll(response.Body)
		t.Fatalf("create launch status = %d, body = %s", response.StatusCode, encoded)
	}
	var payload struct {
		Data struct {
			SessionID string `json:"session_id"`
			LaunchURL string `json:"launch_url"`
		} `json:"data"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		t.Fatalf("decode launch response: %v", err)
	}
	launchURL, err := url.Parse(payload.Data.LaunchURL)
	if err != nil {
		t.Fatalf("parse launch URL: %v", err)
	}
	launchToken := launchURL.Query().Get("token")
	if payload.Data.SessionID == "" || launchToken == "" {
		t.Fatalf("launch response = %#v", payload.Data)
	}
	return payload.Data.SessionID, launchToken
}

func exchangeLaunch(t *testing.T, baseURL, launchToken string) string {
	t.Helper()
	var payload struct {
		Data struct {
			AccessToken string `json:"access_token"`
		} `json:"data"`
	}
	doJSON(t, requestSpec{
		Method: http.MethodPost, URL: baseURL + "/api/user/session/exchange",
		Body: map[string]string{"token": launchToken}, WantStatus: http.StatusOK, Destination: &payload,
	})
	if payload.Data.AccessToken == "" {
		t.Fatal("exchange response has no access token")
	}
	return payload.Data.AccessToken
}

func revokeLaunchSession(t *testing.T, baseURL string, merchant merchantCredentials, sessionID string) {
	t.Helper()
	response := signedV3Request(t, http.MethodDelete, baseURL+"/api/v2/sessions/"+sessionID, merchant, uuid.NewString(), []byte{})
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusNoContent {
		encoded, _ := io.ReadAll(response.Body)
		t.Fatalf("revoke launch session status = %d, body = %s", response.StatusCode, encoded)
	}
}

func signedV3Request(t *testing.T, method, requestURL string, merchant merchantCredentials, idempotencyKey string, body []byte) *http.Response {
	t.Helper()
	timestamp := strconv.FormatInt(time.Now().Unix(), 10)
	mac := hmac.New(sha256.New, []byte(merchant.APISecret))
	_, _ = mac.Write([]byte(timestamp + "."))
	_, _ = mac.Write(body)
	request, err := http.NewRequestWithContext(context.Background(), method, requestURL, bytes.NewReader(body))
	if err != nil {
		t.Fatalf("create signed V3 request: %v", err)
	}
	request.Header.Set("Authorization", "Bearer "+merchant.APIKey)
	request.Header.Set("X-PM-Timestamp", timestamp)
	request.Header.Set("X-PM-Signature", hex.EncodeToString(mac.Sum(nil)))
	if idempotencyKey != "" {
		request.Header.Set("Idempotency-Key", idempotencyKey)
	}
	response, err := (&http.Client{Timeout: 15 * time.Second}).Do(request)
	if err != nil {
		t.Fatalf("perform signed V3 request: %v", err)
	}
	return response
}

func containsHostedEvent(values []struct {
	ID       string `json:"id"`
	Category string `json:"category"`
}, eventID, category string) bool {
	for _, value := range values {
		if value.ID == eventID && value.Category == category {
			return true
		}
	}
	return false
}

func createEvent(t *testing.T, baseURL, adminKey, sourceID string) string {
	t.Helper()
	var response struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	endTime := time.Now().UTC().Add(time.Hour)
	doJSON(t, requestSpec{
		Method: http.MethodPost, URL: baseURL + "/api/v1/events", Token: adminKey,
		Body: map[string]any{
			"source_type": "custom", "source_id": sourceID,
			"title": "E2E settlement event", "description": "end-to-end fixture",
			"category": "sports", "end_time": endTime.Format(time.RFC3339),
			"resolution_time": endTime.Add(time.Minute).Format(time.RFC3339),
		},
		WantStatus: http.StatusCreated, Destination: &response,
	})
	return response.Data.ID
}

func activateEvent(t *testing.T, baseURL, adminKey, eventID string) {
	t.Helper()
	doJSON(t, requestSpec{
		Method: http.MethodPatch, URL: baseURL + "/api/v1/events/" + eventID + "/status",
		Token: adminKey, Body: map[string]string{"status": "active"},
		WantStatus: http.StatusNoContent,
	})
}

func createMarket(t *testing.T, baseURL, adminKey, merchantID, eventID string) string {
	t.Helper()
	var response struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	doJSON(t, requestSpec{
		Method: http.MethodPost, URL: baseURL + "/api/v1/markets", Token: adminKey,
		Body: map[string]any{
			"merchant_id": merchantID, "event_id": eventID, "type": "binary",
			"question": "Will the E2E flow settle?", "options": []string{"Yes", "No"},
			"liquidity_pool": 1000,
		},
		WantStatus: http.StatusCreated, Destination: &response,
	})
	return response.Data.ID
}

func creditWallet(
	t *testing.T,
	baseURL string,
	apiKey string,
	user tradingUser,
	amount float64,
) {
	t.Helper()
	doJSON(t, requestSpec{
		Method: http.MethodPost, URL: baseURL + "/api/v1/wallets/" + user.ID + "/credit",
		Token:          apiKey,
		IdempotencyKey: uuid.NewString(),
		Body:           map[string]any{"currency": user.Currency, "amount": amount, "type": "admin_credit"},
		WantStatus:     http.StatusOK,
	})
}

func placeOrder(
	t *testing.T,
	baseURL string,
	apiKey string,
	marketID string,
	user tradingUser,
	price float64,
) {
	t.Helper()
	doJSON(t, requestSpec{
		Method: http.MethodPost, URL: baseURL + "/api/v1/orders", Token: apiKey, IdempotencyKey: uuid.NewString(),
		Body: map[string]any{
			"market_id": marketID, "user_id": user.ID, "type": user.Side,
			"option": "Yes", "amount": 10, "currency": user.Currency,
			"price": price, "time_in_force": "gtc",
		},
		WantStatus: http.StatusCreated,
	})
}

func closeAndResolveEvent(t *testing.T, baseURL, adminKey, eventID, outcome string) {
	t.Helper()
	doJSON(t, requestSpec{
		Method: http.MethodPatch, URL: baseURL + "/api/v1/events/" + eventID + "/status",
		Token: adminKey, Body: map[string]string{"status": "closed"},
		WantStatus: http.StatusNoContent,
	})
	doJSON(t, requestSpec{
		Method: http.MethodPost, URL: baseURL + "/api/v1/events/" + eventID + "/resolve",
		Token: adminKey, Body: map[string]string{"outcome": outcome},
		WantStatus: http.StatusNoContent,
	})
}

func waitForSettlement(t *testing.T, baseURL, apiKey, marketID string) {
	t.Helper()
	deadline := time.Now().Add(12 * time.Second)
	for time.Now().Before(deadline) {
		var response struct {
			Data struct {
				Status string `json:"status"`
			} `json:"data"`
		}
		status := doJSONStatus(t, requestSpec{
			Method: http.MethodGet, URL: baseURL + "/api/v1/markets/" + marketID,
			Token: apiKey, Destination: &response,
		})
		if status == http.StatusOK && response.Data.Status == "settled" {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatal("market was not settled through NATS before timeout")
}

func assertWallet(
	t *testing.T,
	baseURL string,
	apiKey string,
	user tradingUser,
	wantAvailable float64,
	wantLocked float64,
) {
	t.Helper()
	var response struct {
		Data struct {
			Balances []struct {
				Available float64 `json:"available"`
				Locked    float64 `json:"locked"`
			} `json:"balances"`
		} `json:"data"`
	}
	doJSON(t, requestSpec{
		Method: http.MethodGet,
		URL:    baseURL + "/api/v1/wallets/" + user.ID + "?currency=" + user.Currency,
		Token:  apiKey, WantStatus: http.StatusOK, Destination: &response,
	})
	if len(response.Data.Balances) != 1 {
		t.Fatalf("wallet %s balances = %#v", user.ID, response.Data.Balances)
	}
	balance := response.Data.Balances[0]
	if balance.Available != wantAvailable || balance.Locked != wantLocked {
		t.Errorf(
			"wallet %s = (available %.2f, locked %.2f), want (%.2f, %.2f)",
			user.ID,
			balance.Available,
			balance.Locked,
			wantAvailable,
			wantLocked,
		)
	}
}

func assertSettlementAudit(t *testing.T, database *sql.DB, marketID string) {
	t.Helper()
	rows, err := database.QueryContext(context.Background(), `
SELECT currency, COUNT(*), SUM(stake)::float8, SUM(payout)::float8
FROM settlement_payouts WHERE market_id = $1
GROUP BY currency ORDER BY currency`, marketID)
	if err != nil {
		t.Fatalf("query settlement audit: %v", err)
	}
	defer func() { _ = rows.Close() }()
	seen := map[string]bool{}
	for rows.Next() {
		var currency string
		var count int
		var stake, payout float64
		if err := rows.Scan(&currency, &count, &stake, &payout); err != nil {
			t.Fatalf("scan settlement audit: %v", err)
		}
		seen[currency] = true
		if count != 2 || stake != 10 || payout != 10 {
			t.Errorf("%s audit = (%d, %.2f, %.2f), want (2, 10, 10)", currency, count, stake, payout)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate settlement audit: %v", err)
	}
	if !seen["USD"] || !seen["EUR"] {
		t.Errorf("settlement audit currencies = %#v, want USD and EUR", seen)
	}
}

type seamlessLoadState struct {
	mu           sync.Mutex
	balanceCents int64
	requests     int
}

func (s *seamlessLoadState) handle(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/callback":
		s.handleCallback(w, r)
	case "/webhook":
		writeJSONResponse(w, http.StatusOK, map[string]string{"status": "ok"})
	default:
		http.NotFound(w, r)
	}
}

func (s *seamlessLoadState) handleCallback(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Type      string `json:"type"`
		Amount    string `json:"amount"`
		Challenge string `json:"challenge"`
	}
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		writeJSONResponse(w, http.StatusBadRequest, map[string]string{"error": "invalid callback JSON"})
		return
	}
	if request.Type == "callback.verify" {
		writeJSONResponse(w, http.StatusOK, map[string]string{
			"status":    "ok",
			"challenge": request.Challenge,
		})
		return
	}
	amountCents, err := fixed.CentsFromString(request.Amount)
	if err != nil {
		writeJSONResponse(w, http.StatusBadRequest, map[string]string{"error": "invalid amount"})
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.requests++

	switch request.Type {
	case "debit":
		if s.balanceCents < amountCents {
			writeJSONResponse(w, http.StatusOK, map[string]string{
				"status":  "insufficient_funds",
				"balance": fixed.FormatCents(s.balanceCents),
			})
			return
		}
		s.balanceCents -= amountCents
	case "credit", "rollback":
		s.balanceCents += amountCents
	default:
		writeJSONResponse(w, http.StatusOK, map[string]string{"status": "user_blocked"})
		return
	}
	writeJSONResponse(w, http.StatusOK, map[string]string{
		"status":  "ok",
		"balance": fixed.FormatCents(s.balanceCents),
	})
}

func configureSeamlessIntegration(t *testing.T, baseURL, adminKey, merchantID, callbackURL string) {
	t.Helper()
	var response struct {
		Data struct {
			CallbackSecret string `json:"callback_secret"`
		} `json:"data"`
	}
	doJSON(t, requestSpec{
		Method: http.MethodPut,
		URL:    baseURL + "/api/v1/merchants/" + merchantID + "/integration",
		Token:  adminKey,
		Body: map[string]any{
			"wallet_mode":    "seamless",
			"callback_url":   callbackURL + "/callback",
			"webhook_url":    callbackURL + "/webhook",
			"webhook_events": []string{"order.settled", "market.settled"},
		},
		WantStatus:  http.StatusOK,
		Destination: &response,
	})
	if response.Data.CallbackSecret == "" {
		t.Fatal("seamless integration did not return a callback secret")
	}
	verifyCallbackURL(t, baseURL, adminKey, merchantID)
}

// configureTransferWebhooks enables webhook delivery for a transfer-mode
// merchant so void/settlement outbox rows are produced in tests.
func configureTransferWebhooks(t *testing.T, baseURL, adminKey, merchantID string) {
	t.Helper()
	doJSON(t, requestSpec{
		Method: http.MethodPut,
		URL:    baseURL + "/api/v1/merchants/" + merchantID + "/integration",
		Token:  adminKey,
		Body: map[string]any{
			"webhook_url":    "https://merchant.example/webhook",
			"webhook_events": []string{"order.voided", "market.voided"},
		},
		WantStatus: http.StatusOK,
	})
}

// verifyCallbackURL proves callback URL ownership through the challenge echo.
func verifyCallbackURL(t *testing.T, baseURL, adminKey, merchantID string) {
	t.Helper()
	doJSON(t, requestSpec{
		Method:     http.MethodPost,
		URL:        baseURL + "/api/v1/merchants/" + merchantID + "/integration/verify-callback",
		Token:      adminKey,
		WantStatus: http.StatusOK,
	})
}

func postSeamlessLoadOrders(t *testing.T, baseURL string, merchant merchantCredentials, marketID string, total int) {
	t.Helper()
	sem := make(chan struct{}, 50)
	errCh := make(chan error, total)
	var wg sync.WaitGroup
	client := &http.Client{Timeout: 15 * time.Second}
	for i := 0; i < total; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			body, err := json.Marshal(map[string]any{
				"market_id":     marketID,
				"user_id":       "load-user",
				"type":          "buy",
				"option":        "Yes",
				"amount":        1,
				"currency":      "USD",
				"price":         0.5,
				"time_in_force": "gtc",
			})
			if err != nil {
				errCh <- fmt.Errorf("marshal order payload: %w", err)
				return
			}
			timestamp := strconv.FormatInt(time.Now().Unix(), 10)
			mac := hmac.New(sha256.New, []byte(merchant.APISecret))
			_, _ = mac.Write([]byte(timestamp + "."))
			_, _ = mac.Write(body)
			request, err := http.NewRequestWithContext(context.Background(), http.MethodPost, baseURL+"/api/v2/orders", bytes.NewReader(body))
			if err != nil {
				errCh <- fmt.Errorf("create load order request: %w", err)
				return
			}
			request.Header.Set("Authorization", "Bearer "+merchant.APIKey)
			request.Header.Set("X-PM-Timestamp", timestamp)
			request.Header.Set("X-PM-Signature", hex.EncodeToString(mac.Sum(nil)))
			request.Header.Set("Idempotency-Key", fmt.Sprintf("load-order-%04d", i))
			response, err := client.Do(request)
			if err != nil {
				errCh <- fmt.Errorf("perform load order %d: %w", i, err)
				return
			}
			defer func() { _ = response.Body.Close() }()
			if response.StatusCode != http.StatusCreated {
				encoded, _ := io.ReadAll(response.Body)
				errCh <- fmt.Errorf("order %d status = %d, body = %s", i, response.StatusCode, encoded)
			}
		}()
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		if err != nil {
			t.Fatal(err)
		}
	}
}

func waitForShadowDriftZero(t *testing.T, baseURL string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		response, err := http.Get(baseURL + "/metrics")
		if err != nil {
			time.Sleep(100 * time.Millisecond)
			continue
		}
		body, readErr := io.ReadAll(response.Body)
		_ = response.Body.Close()
		if readErr != nil {
			time.Sleep(100 * time.Millisecond)
			continue
		}
		for _, line := range strings.Split(string(body), "\n") {
			if !strings.HasPrefix(line, "predictmarket_shadow_wallet_drift_count ") {
				continue
			}
			fields := strings.Fields(line)
			if len(fields) != 2 {
				t.Fatalf("unexpected drift metric line: %q", line)
			}
			value, err := strconv.Atoi(fields[1])
			if err != nil {
				t.Fatalf("parse drift metric %q: %v", line, err)
			}
			if value == 0 {
				return
			}
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("shadow wallet drift metric did not return to zero")
}

func assertSeamlessLoadState(t *testing.T, database *sql.DB, merchantID, marketID string, wantOrders int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var shadowDrift int
	if err := database.QueryRowContext(ctx, `
SELECT COUNT(*)
FROM wallets
WHERE merchant_id = $1 AND kind = 'shadow' AND balance <> 0`, merchantID).Scan(&shadowDrift); err != nil {
		t.Fatalf("query shadow drift: %v", err)
	}
	if shadowDrift != 0 {
		t.Fatalf("shadow drift rows = %d, want 0", shadowDrift)
	}

	var pendingCallbacks int
	if err := database.QueryRowContext(ctx, `
SELECT COUNT(*)
FROM callback_outbox
WHERE merchant_id = $1 AND status = 'pending'`, merchantID).Scan(&pendingCallbacks); err != nil {
		t.Fatalf("query pending callback outbox: %v", err)
	}
	if pendingCallbacks != 0 {
		t.Fatalf("pending callback outbox rows = %d, want 0", pendingCallbacks)
	}

	var pendingWebhooks int
	if err := database.QueryRowContext(ctx, `
SELECT COUNT(*)
FROM webhook_outbox
WHERE merchant_id = $1 AND status = 'pending'`, merchantID).Scan(&pendingWebhooks); err != nil {
		t.Fatalf("query pending webhook outbox: %v", err)
	}
	if pendingWebhooks != 0 {
		t.Fatalf("pending webhook outbox rows = %d, want 0", pendingWebhooks)
	}

	var orderCount int
	if err := database.QueryRowContext(ctx, `
SELECT COUNT(*)
FROM orders
WHERE merchant_id = $1 AND market_id = $2 AND wallet_kind = 'shadow'`, merchantID, marketID).Scan(&orderCount); err != nil {
		t.Fatalf("query load orders: %v", err)
	}
	if orderCount != wantOrders {
		t.Fatalf("load orders = %d, want %d", orderCount, wantOrders)
	}

	var acceptedTransactions int
	if err := database.QueryRowContext(ctx, `
SELECT COUNT(*)
FROM seamless_transactions
WHERE merchant_id = $1 AND type = 'debit' AND status = 'accepted'`, merchantID).Scan(&acceptedTransactions); err != nil {
		t.Fatalf("query accepted seamless transactions: %v", err)
	}
	if acceptedTransactions != wantOrders {
		t.Fatalf("accepted seamless transactions = %d, want %d", acceptedTransactions, wantOrders)
	}

	var pendingTransactions int
	if err := database.QueryRowContext(ctx, `
SELECT COUNT(*)
FROM seamless_transactions
WHERE merchant_id = $1 AND status IN ('created', 'pending_delivery', 'unknown')`, merchantID).Scan(&pendingTransactions); err != nil {
		t.Fatalf("query pending seamless transactions: %v", err)
	}
	if pendingTransactions != 0 {
		t.Fatalf("pending seamless transactions = %d, want 0", pendingTransactions)
	}
}

func writeJSONResponse(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

type requestSpec struct {
	Method         string
	URL            string
	Token          string
	Cookie         string
	IdempotencyKey string
	Body           any
	WantStatus     int
	Destination    any
}

func doJSON(t *testing.T, spec requestSpec) {
	t.Helper()
	status := doJSONStatus(t, spec)
	if status != spec.WantStatus {
		t.Fatalf("%s %s status = %d, want %d", spec.Method, spec.URL, status, spec.WantStatus)
	}
}

func doJSONStatus(t *testing.T, spec requestSpec) int {
	t.Helper()
	var body io.Reader
	if spec.Body != nil {
		encoded, err := json.Marshal(spec.Body)
		if err != nil {
			t.Fatalf("marshal request: %v", err)
		}
		body = bytes.NewReader(encoded)
	}
	request, err := http.NewRequestWithContext(context.Background(), spec.Method, spec.URL, body)
	if err != nil {
		t.Fatalf("create request: %v", err)
	}
	if spec.Body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if spec.Token != "" {
		request.Header.Set("Authorization", "Bearer "+spec.Token)
	}
	if spec.Cookie != "" {
		request.Header.Set("Cookie", spec.Cookie)
	}
	if spec.IdempotencyKey != "" {
		request.Header.Set("Idempotency-Key", spec.IdempotencyKey)
	}
	client := &http.Client{Timeout: 3 * time.Second}
	response, err := client.Do(request)
	if err != nil {
		t.Fatalf("perform %s %s: %v", spec.Method, spec.URL, err)
	}
	defer func() { _ = response.Body.Close() }()
	encoded, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	if spec.WantStatus != 0 && response.StatusCode != spec.WantStatus {
		t.Fatalf(
			"%s %s status = %d, want %d, body = %s",
			spec.Method,
			spec.URL,
			response.StatusCode,
			spec.WantStatus,
			encoded,
		)
	}
	if spec.Destination != nil && len(encoded) > 0 && response.StatusCode < 300 {
		if err := json.Unmarshal(encoded, spec.Destination); err != nil {
			t.Fatalf("decode response %s: %v", encoded, err)
		}
	}
	return response.StatusCode
}

// adminSession logs into the admin console and returns the session cookie.
// The e2e API process must run with ADMIN_USERNAME/ADMIN_PASSWORD set.
func adminSession(t *testing.T, baseURL string) string {
	t.Helper()
	username := environmentOrDefault("ADMIN_USERNAME", "e2e-admin")
	password := environmentOrDefault("ADMIN_PASSWORD", "e2e-admin-password")
	request, err := http.NewRequestWithContext(context.Background(), http.MethodPost, baseURL+"/api/v1/admin/login",
		bytes.NewReader([]byte(fmt.Sprintf(`{"username":%q,"password":%q}`, username, password))))
	if err != nil {
		t.Fatalf("create admin login: %v", err)
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := (&http.Client{Timeout: 3 * time.Second}).Do(request)
	if err != nil {
		t.Fatalf("admin login: %v", err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("admin login status = %d", response.StatusCode)
	}
	cookie := ""
	for _, candidate := range response.Cookies() {
		if candidate.Name == auth.AdminSessionCookie {
			cookie = candidate.Name + "=" + candidate.Value
		}
	}
	if cookie == "" {
		t.Fatalf("admin login returned no session cookie")
	}
	return cookie
}

func currencyUser(currency, result string) string {
	return strings.ToLower(currency) + "-" + result
}

func openDatabase(t *testing.T) *sql.DB {
	t.Helper()
	database, err := sql.Open("pgx", environmentOrDefault("DATABASE_URL", defaultDatabaseURL))
	if err != nil {
		t.Fatalf("open PostgreSQL: %v", err)
	}
	if err := database.PingContext(context.Background()); err != nil {
		_ = database.Close()
		t.Fatalf("ping PostgreSQL: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	return database
}

func cleanupFixture(t *testing.T, database *sql.DB, email, sourceID string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	type cleanupStatement struct {
		query string
		args  []any
	}
	statements := []cleanupStatement{
		{
			query: `DELETE FROM callback_dead_letters WHERE merchant_id IN (
                SELECT id FROM merchants WHERE email = $1)`,
			args: []any{email},
		},
		{
			query: `DELETE FROM callback_outbox WHERE merchant_id IN (
                SELECT id FROM merchants WHERE email = $1)`,
			args: []any{email},
		},
		{
			query: `DELETE FROM webhook_outbox WHERE merchant_id IN (
                SELECT id FROM merchants WHERE email = $1)`,
			args: []any{email},
		},
		{
			query: `DELETE FROM seamless_transactions WHERE merchant_id IN (
                SELECT id FROM merchants WHERE email = $1)`,
			args: []any{email},
		},
		{
			query: `DELETE FROM trades WHERE market_id IN (
                SELECT ma.id FROM markets ma
                JOIN merchants m ON m.id = ma.merchant_id WHERE m.email = $1)`,
			args: []any{email},
		},
		{
			query: `DELETE FROM wallet_transfers WHERE merchant_id IN (
                SELECT id FROM merchants WHERE email = $1)`,
			args: []any{email},
		},
		{
			query: `DELETE FROM transactions WHERE wallet_id IN (
                SELECT w.id FROM wallets w
                JOIN merchants m ON m.id = w.merchant_id WHERE m.email = $1)`,
			args: []any{email},
		},
		{
			query: `DELETE FROM settlement_payouts WHERE market_id IN (
                SELECT ma.id FROM markets ma
                JOIN merchants m ON m.id = ma.merchant_id WHERE m.email = $1)`,
			args: []any{email},
		},
		{
			query: `DELETE FROM market_settlements WHERE market_id IN (
                SELECT ma.id FROM markets ma
                JOIN merchants m ON m.id = ma.merchant_id WHERE m.email = $1)`,
			args: []any{email},
		},
		{
			query: `DELETE FROM orders WHERE merchant_id IN (
                SELECT id FROM merchants WHERE email = $1)`,
			args: []any{email},
		},
		{
			query: `DELETE FROM wallets WHERE merchant_id IN (
                SELECT id FROM merchants WHERE email = $1)`,
			args: []any{email},
		},
		{
			query: `DELETE FROM markets WHERE merchant_id IN (
                SELECT id FROM merchants WHERE email = $1)`,
			args: []any{email},
		},
		{
			query: `DELETE FROM event_outbox WHERE event_id IN (
                SELECT id FROM events WHERE source_id = $1)`,
			args: []any{sourceID},
		},
		{query: `DELETE FROM events WHERE source_id = $1`, args: []any{sourceID}},
		{query: `DELETE FROM merchants WHERE email = $1`, args: []any{email}},
	}
	for _, statement := range statements {
		if _, err := database.ExecContext(ctx, statement.query, statement.args...); err != nil {
			t.Errorf("clean E2E fixture: %v", err)
		}
	}
}

func environmentOrDefault(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

func environmentBool(name string) bool {
	value := strings.ToLower(strings.TrimSpace(os.Getenv(name)))
	return value == "1" || value == "true" || value == "yes"
}

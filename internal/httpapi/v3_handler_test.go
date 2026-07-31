package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"
	"time"

	"github.com/afun-game/predictmarket-saas/internal/currency"
	"github.com/afun-game/predictmarket-saas/internal/event"
	"github.com/afun-game/predictmarket-saas/internal/market"
	"github.com/afun-game/predictmarket-saas/internal/merchant"
	"github.com/afun-game/predictmarket-saas/internal/order"
	"github.com/afun-game/predictmarket-saas/internal/platformuser"
	"github.com/afun-game/predictmarket-saas/internal/session"
	"github.com/afun-game/predictmarket-saas/internal/wallet"
	"github.com/afun-game/predictmarket-saas/pkg/types"
)

type signedMerchantStub struct {
	merchant *types.Merchant
}

func (s *signedMerchantStub) ValidateSignedRequest(
	_ context.Context,
	_ string,
	_ string,
	_ string,
	_ []byte,
) (*types.Merchant, error) {
	return s.merchant, nil
}

func TestV3LaunchExchangeAndTenantScopedMarkets(t *testing.T) {
	t.Parallel()

	merchantService := merchant.NewService()
	eventService := event.NewService()
	marketService := market.NewService()
	walletService := wallet.NewService()
	orderService := order.NewServiceWithDependencies(marketService, walletService)
	manager := v3TestSessionManager(t)
	signer := &signedMerchantStub{}
	handler := NewHandler(
		merchantService,
		eventService,
		marketService,
		walletService,
		orderService,
		currency.NewService(),
		"admin-secret",
		V3Config{
			Authenticator:   signer,
			Sessions:        manager,
			PlatformUsers:   platformuser.NewMemoryRepository(),
			HostedLaunchURL: "https://play.example/launch",
		},
	)

	first := registerMerchant(t, handler, "V3 Tenant", "v3@example.test")
	second := registerMerchant(t, handler, "Other Tenant", "other@example.test")
	signer.merchant = &types.Merchant{
		ID:         first.Data.MerchantID,
		Status:     "active",
		Currency:   "USD",
		WalletMode: "transfer",
	}
	eventID := createActiveEventForMarket(t, handler, "v3-launch-event")
	firstMarketID := createMarketForMerchant(t, handler, first.Data.MerchantID, eventID)
	secondMarketID := createMarketForMerchant(t, handler, second.Data.MerchantID, eventID)

	created := signedV3Request(
		t,
		handler,
		http.MethodPost,
		"/api/v2/sessions",
		[]byte(`{"user_id":"site-user-8801","currency":"USD","locale":"zh-CN","return_url":"https://merchant.example/lobby"}`),
		"launch-request-1",
	)
	if created.Code != http.StatusCreated {
		t.Fatalf("create session status = %d, body = %s", created.Code, created.Body.String())
	}
	launch := struct {
		Data struct {
			SessionID string `json:"session_id"`
			LaunchURL string `json:"launch_url"`
		} `json:"data"`
	}{}
	if err := json.Unmarshal(created.Body.Bytes(), &launch); err != nil {
		t.Fatalf("decode launch response: %v", err)
	}
	if launch.Data.SessionID == "" {
		t.Fatal("session_id is empty")
	}
	launchURL, err := url.Parse(launch.Data.LaunchURL)
	if err != nil {
		t.Fatalf("parse launch URL: %v", err)
	}
	launchToken := launchURL.Query().Get("token")
	if launchToken == "" {
		t.Fatalf("launch URL has no token: %q", launch.Data.LaunchURL)
	}

	replayed := signedV3Request(
		t,
		handler,
		http.MethodPost,
		"/api/v2/sessions",
		[]byte(`{"user_id":"site-user-8801","currency":"USD"}`),
		"launch-request-1",
	)
	if replayed.Code != http.StatusUnauthorized {
		t.Errorf("replayed create session status = %d, want %d", replayed.Code, http.StatusUnauthorized)
	}

	exchanged := v3Request(
		t,
		handler,
		http.MethodPost,
		"/api/user/session/exchange",
		[]byte(`{"token":`+strconv.Quote(launchToken)+`}`),
		"",
	)
	if exchanged.Code != http.StatusOK {
		t.Fatalf("exchange session status = %d, body = %s", exchanged.Code, exchanged.Body.String())
	}
	exchange := struct {
		Data struct {
			AccessToken string `json:"access_token"`
		} `json:"data"`
	}{}
	if err := json.Unmarshal(exchanged.Body.Bytes(), &exchange); err != nil {
		t.Fatalf("decode exchange response: %v", err)
	}
	if exchange.Data.AccessToken == "" {
		t.Fatal("access token is empty")
	}

	me := v3Request(t, handler, http.MethodGet, "/api/user/me", nil, "Bearer "+exchange.Data.AccessToken)
	if me.Code != http.StatusOK {
		t.Fatalf("get user profile status = %d, body = %s", me.Code, me.Body.String())
	}
	profile := struct {
		Data struct {
			UserID           string `json:"user_id"`
			AvailableBalance string `json:"available_balance"`
			WalletMode       string `json:"wallet_mode"`
			Locale           string `json:"locale"`
		} `json:"data"`
	}{}
	if err := json.Unmarshal(me.Body.Bytes(), &profile); err != nil {
		t.Fatalf("decode profile response: %v", err)
	}
	if profile.Data.UserID != "site-user-8801" || profile.Data.AvailableBalance != "0.00" || profile.Data.WalletMode != "transfer" || profile.Data.Locale != "zh-CN" {
		t.Errorf("profile = %#v", profile.Data)
	}
	refreshed := v3Request(
		t,
		handler,
		http.MethodPost,
		"/api/user/session/refresh",
		nil,
		"Bearer "+exchange.Data.AccessToken,
	)
	if refreshed.Code != http.StatusOK {
		t.Fatalf("refresh session status = %d, body = %s", refreshed.Code, refreshed.Body.String())
	}
	refresh := struct {
		Data struct {
			AccessToken string `json:"access_token"`
		} `json:"data"`
	}{}
	if err := json.Unmarshal(refreshed.Body.Bytes(), &refresh); err != nil {
		t.Fatalf("decode refresh response: %v", err)
	}
	if refresh.Data.AccessToken == "" {
		t.Fatal("refreshed access token is empty")
	}
	events := v3Request(t, handler, http.MethodGet, "/api/user/events?limit=10", nil, "Bearer "+exchange.Data.AccessToken)
	if events.Code != http.StatusOK {
		t.Fatalf("list user events status = %d, body = %s", events.Code, events.Body.String())
	}
	listedEvents := struct {
		Data []v3UserEvent `json:"data"`
	}{}
	if err := json.Unmarshal(events.Body.Bytes(), &listedEvents); err != nil {
		t.Fatalf("decode event list: %v", err)
	}
	if len(listedEvents.Data) != 1 || listedEvents.Data[0].ID != eventID {
		t.Errorf("listed events = %#v", listedEvents.Data)
	}

	marketList := v3Request(t, handler, http.MethodGet, "/api/user/markets?limit=10", nil, "Bearer "+exchange.Data.AccessToken)
	if marketList.Code != http.StatusOK {
		t.Fatalf("list user markets status = %d, body = %s", marketList.Code, marketList.Body.String())
	}
	listed := struct {
		Data []v3UserMarket `json:"data"`
	}{}
	if err := json.Unmarshal(marketList.Body.Bytes(), &listed); err != nil {
		t.Fatalf("decode market list: %v", err)
	}
	if len(listed.Data) != 1 || listed.Data[0].ID != firstMarketID || listed.Data[0].TotalVolume != "0.000000" {
		t.Errorf("listed markets = %#v", listed.Data)
	}

	otherMarket := v3Request(
		t,
		handler,
		http.MethodGet,
		"/api/user/markets/"+secondMarketID,
		nil,
		"Bearer "+exchange.Data.AccessToken,
	)
	if otherMarket.Code != http.StatusNotFound {
		t.Errorf("other tenant market status = %d, want %d", otherMarket.Code, http.StatusNotFound)
	}

	status := signedV3Request(t, handler, http.MethodGet, "/api/v2/sessions/"+launch.Data.SessionID, nil, "")
	if status.Code != http.StatusOK {
		t.Fatalf("get session status = %d, body = %s", status.Code, status.Body.String())
	}
	revoked := signedV3Request(t, handler, http.MethodDelete, "/api/v2/sessions/"+launch.Data.SessionID, nil, "revoke-request-1")
	if revoked.Code != http.StatusNoContent {
		t.Fatalf("revoke session status = %d, body = %s", revoked.Code, revoked.Body.String())
	}
	afterRevoke := v3Request(t, handler, http.MethodGet, "/api/user/me", nil, "Bearer "+refresh.Data.AccessToken)
	if afterRevoke.Code != http.StatusUnauthorized {
		t.Errorf("profile after revoke status = %d, want %d", afterRevoke.Code, http.StatusUnauthorized)
	}
}

func TestV3TransferWalletAPI(t *testing.T) {
	t.Parallel()

	merchantService := merchant.NewService()
	walletService := wallet.NewService()
	signer := &signedMerchantStub{}
	handler := NewHandler(
		merchantService,
		event.NewService(),
		market.NewService(),
		walletService,
		order.NewServiceWithDependencies(market.NewService(), walletService),
		currency.NewService(),
		"admin-secret",
		V3Config{
			Authenticator:   signer,
			Sessions:        v3TestSessionManager(t),
			PlatformUsers:   platformuser.NewMemoryRepository(),
			HostedLaunchURL: "https://play.example/launch",
		},
	)
	credentials := registerMerchant(t, handler, "Transfer Tenant", "transfer@example.test")
	signer.merchant = &types.Merchant{
		ID:         credentials.Data.MerchantID,
		Status:     "active",
		Currency:   "USD",
		WalletMode: "transfer",
	}

	depositBody := []byte(`{"merchant_txn_id":"deposit-001","currency":"USD","amount":"10.25"}`)
	first := signedV3Request(
		t,
		handler,
		http.MethodPost,
		"/api/v2/users/site-user/deposits",
		depositBody,
		"header-retry-001",
	)
	if first.Code != http.StatusCreated {
		t.Fatalf("deposit status = %d, body = %s", first.Code, first.Body.String())
	}
	retry := signedV3Request(
		t,
		handler,
		http.MethodPost,
		"/api/v2/users/site-user/deposits",
		depositBody,
		"header-retry-001",
	)
	if retry.Code != http.StatusCreated {
		t.Fatalf("idempotent deposit status = %d, body = %s", retry.Code, retry.Body.String())
	}
	transfer := struct {
		Data v3TransferResponse `json:"data"`
	}{}
	if err := json.Unmarshal(retry.Body.Bytes(), &transfer); err != nil {
		t.Fatalf("decode deposit: %v", err)
	}
	if transfer.Data.Amount != "10.25" || transfer.Data.Direction != "deposit" || transfer.Data.Status != "completed" {
		t.Errorf("deposit = %#v", transfer.Data)
	}

	balance := signedV3Request(
		t,
		handler,
		http.MethodGet,
		"/api/v2/users/site-user/balance",
		nil,
		"",
	)
	if balance.Code != http.StatusOK {
		t.Fatalf("balance status = %d, body = %s", balance.Code, balance.Body.String())
	}
	balanceData := struct {
		Data struct {
			Available string `json:"available_balance"`
			Locked    string `json:"locked_balance"`
			Total     string `json:"total_balance"`
		} `json:"data"`
	}{}
	if err := json.Unmarshal(balance.Body.Bytes(), &balanceData); err != nil {
		t.Fatalf("decode balance: %v", err)
	}
	if balanceData.Data.Available != "10.25" || balanceData.Data.Locked != "0.00" || balanceData.Data.Total != "10.25" {
		t.Errorf("balance = %#v", balanceData.Data)
	}

	withdrawal := signedV3Request(
		t,
		handler,
		http.MethodPost,
		"/api/v2/users/site-user/withdrawals",
		[]byte(`{"merchant_txn_id":"withdrawal-001","currency":"USD","amount":"4.20"}`),
		"header-withdrawal-001",
	)
	if withdrawal.Code != http.StatusCreated {
		t.Fatalf("withdrawal status = %d, body = %s", withdrawal.Code, withdrawal.Body.String())
	}

	conflict := signedV3Request(
		t,
		handler,
		http.MethodPost,
		"/api/v2/users/site-user/deposits",
		[]byte(`{"merchant_txn_id":"deposit-001","currency":"USD","amount":"10.26"}`),
		"header-conflict-001",
	)
	if conflict.Code != http.StatusConflict {
		t.Errorf("conflicting transfer status = %d, want %d; body = %s", conflict.Code, http.StatusConflict, conflict.Body.String())
	}

	found := signedV3Request(
		t,
		handler,
		http.MethodGet,
		"/api/v2/transfers/deposit-001",
		nil,
		"",
	)
	if found.Code != http.StatusOK {
		t.Fatalf("get transfer status = %d, body = %s", found.Code, found.Body.String())
	}

	signer.merchant.WalletMode = "seamless"
	seamless := signedV3Request(
		t,
		handler,
		http.MethodGet,
		"/api/v2/users/site-user/balance",
		nil,
		"",
	)
	if seamless.Code != http.StatusConflict {
		t.Errorf("seamless balance status = %d, want %d", seamless.Code, http.StatusConflict)
	}
}

func TestV3HostedAndServerOrderAPIs(t *testing.T) {
	t.Parallel()

	merchantService := merchant.NewService()
	eventService := event.NewService()
	marketService := market.NewService()
	walletService := wallet.NewService()
	orderService := order.NewServiceWithDependencies(marketService, walletService)
	manager := v3TestSessionManager(t)
	signer := &signedMerchantStub{}
	handler := NewHandler(
		merchantService,
		eventService,
		marketService,
		walletService,
		orderService,
		currency.NewService(),
		"admin-secret",
		V3Config{
			Authenticator:   signer,
			Sessions:        manager,
			PlatformUsers:   platformuser.NewMemoryRepository(),
			HostedLaunchURL: "https://play.example/launch",
		},
	)
	credentials := registerMerchant(t, handler, "Order Tenant", "order-v3@example.test")
	signer.merchant = &types.Merchant{
		ID:         credentials.Data.MerchantID,
		Status:     "active",
		Currency:   "USD",
		WalletMode: "transfer",
	}
	eventID := createActiveEventForMarket(t, handler, "v3-hosted-order")
	marketID := createMarketForMerchant(t, handler, credentials.Data.MerchantID, eventID)
	for _, userID := range []string{"hosted-user", "server-user"} {
		if _, err := walletService.Deposit(context.Background(), &wallet.TransferRequest{
			MerchantID:            credentials.Data.MerchantID,
			MerchantTransactionID: "fund-" + userID,
			UserID:                userID,
			Currency:              "USD",
			Amount:                "20.00",
		}); err != nil {
			t.Fatalf("fund %s: %v", userID, err)
		}
	}

	launchToken, _, err := manager.CreateLaunch(
		context.Background(),
		credentials.Data.MerchantID,
		"hosted-user",
		"USD",
		"transfer",
		"en-US",
		"",
	)
	if err != nil {
		t.Fatalf("CreateLaunch() error = %v", err)
	}
	accessToken, _, err := manager.Exchange(context.Background(), launchToken)
	if err != nil {
		t.Fatalf("Exchange() error = %v", err)
	}
	hostedCreate := httptest.NewRequest(
		http.MethodPost,
		"/api/user/orders",
		bytes.NewBufferString(`{"market_id":"`+marketID+`","type":"buy","option":"Yes","amount":10,"price":0.5}`),
	)
	hostedCreate.Header.Set("Authorization", "Bearer "+accessToken)
	hostedCreate.Header.Set("Idempotency-Key", "hosted-order-001")
	hostedRecorder := httptest.NewRecorder()
	handler.ServeHTTP(hostedRecorder, hostedCreate)
	if hostedRecorder.Code != http.StatusCreated {
		t.Fatalf("hosted create status = %d, body = %s", hostedRecorder.Code, hostedRecorder.Body.String())
	}
	created := struct {
		Data struct {
			ID     string `json:"id"`
			UserID string `json:"user_id"`
		} `json:"data"`
	}{}
	if err := json.Unmarshal(hostedRecorder.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode hosted order: %v", err)
	}
	if created.Data.ID == "" || created.Data.UserID != "hosted-user" {
		t.Errorf("hosted order = %#v", created.Data)
	}

	serverOrder := signedV3Request(
		t,
		handler,
		http.MethodPost,
		"/api/v2/orders",
		[]byte(`{"user_id":"server-user","market_id":"`+marketID+`","type":"sell","option":"Yes","amount":10,"currency":"USD","price":0.6}`),
		"server-order-001",
	)
	if serverOrder.Code != http.StatusCreated {
		t.Fatalf("server create status = %d, body = %s", serverOrder.Code, serverOrder.Body.String())
	}

	list := v3Request(t, handler, http.MethodGet, "/api/user/orders?limit=500", nil, "Bearer "+accessToken)
	if list.Code != http.StatusOK {
		t.Fatalf("hosted list status = %d, body = %s", list.Code, list.Body.String())
	}
	trades := v3Request(t, handler, http.MethodGet, "/api/user/orders/"+created.Data.ID+"/trades", nil, "Bearer "+accessToken)
	if trades.Code != http.StatusOK {
		t.Fatalf("hosted trades status = %d, body = %s", trades.Code, trades.Body.String())
	}
	merchantOrders := signedV3Request(t, handler, http.MethodGet, "/api/v2/orders?limit=500", nil, "")
	if merchantOrders.Code != http.StatusOK {
		t.Fatalf("v2 orders status = %d, body = %s", merchantOrders.Code, merchantOrders.Body.String())
	}
	merchantTrades := signedV3Request(t, handler, http.MethodGet, "/api/v2/trades?limit=500", nil, "")
	if merchantTrades.Code != http.StatusOK {
		t.Fatalf("v2 trades status = %d, body = %s", merchantTrades.Code, merchantTrades.Body.String())
	}

	foreignLaunch, _, err := manager.CreateLaunch(
		context.Background(),
		credentials.Data.MerchantID,
		"other-user",
		"USD",
		"transfer",
		"en-US",
		"",
	)
	if err != nil {
		t.Fatalf("CreateLaunch(other user) error = %v", err)
	}
	foreignToken, _, err := manager.Exchange(context.Background(), foreignLaunch)
	if err != nil {
		t.Fatalf("Exchange(other user) error = %v", err)
	}
	foreignCancel := httptest.NewRequest(http.MethodDelete, "/api/user/orders/"+created.Data.ID, nil)
	foreignCancel.Header.Set("Authorization", "Bearer "+foreignToken)
	foreignCancel.Header.Set("Idempotency-Key", "foreign-cancel")
	foreignRecorder := httptest.NewRecorder()
	handler.ServeHTTP(foreignRecorder, foreignCancel)
	if foreignRecorder.Code != http.StatusNotFound {
		t.Errorf("foreign cancel status = %d, want %d", foreignRecorder.Code, http.StatusNotFound)
	}

	cancel := httptest.NewRequest(http.MethodDelete, "/api/user/orders/"+created.Data.ID, nil)
	cancel.Header.Set("Authorization", "Bearer "+accessToken)
	cancel.Header.Set("Idempotency-Key", "hosted-cancel-001")
	cancelRecorder := httptest.NewRecorder()
	handler.ServeHTTP(cancelRecorder, cancel)
	if cancelRecorder.Code != http.StatusOK {
		t.Fatalf("hosted cancel status = %d, body = %s", cancelRecorder.Code, cancelRecorder.Body.String())
	}
}

func signedV3Request(
	t *testing.T,
	handler http.Handler,
	method string,
	path string,
	body []byte,
	idempotencyKey string,
) *httptest.ResponseRecorder {
	t.Helper()
	requestTimestamp := strconv.FormatInt(time.Now().Unix(), 10)
	request := httptest.NewRequest(method, path, bytes.NewReader(body))
	request.Header.Set("Authorization", "Bearer v3-api-key")
	request.Header.Set("X-PM-Timestamp", requestTimestamp)
	request.Header.Set("X-PM-Signature", "not-checked-by-stub")
	if idempotencyKey != "" {
		request.Header.Set("Idempotency-Key", idempotencyKey)
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	return recorder
}

func v3Request(
	t *testing.T,
	handler http.Handler,
	method string,
	path string,
	body []byte,
	authorization string,
) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, path, bytes.NewReader(body))
	if authorization != "" {
		request.Header.Set("Authorization", authorization)
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	return recorder
}

func v3TestSessionManager(t *testing.T) *session.Manager {
	t.Helper()
	secret := make([]byte, 32)
	for index := range secret {
		secret[index] = byte(index + 1)
	}
	manager, err := session.NewManager(session.NewMemoryStore(), secret)
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}
	return manager
}

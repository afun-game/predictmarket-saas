package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
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
	"github.com/afun-game/predictmarket-saas/internal/parimutuel"
	"github.com/afun-game/predictmarket-saas/internal/platformuser"
	"github.com/afun-game/predictmarket-saas/internal/session"
	"github.com/afun-game/predictmarket-saas/internal/wallet"
	"github.com/afun-game/predictmarket-saas/pkg/types"
)

type signedMerchantStub struct {
	merchant *types.Merchant
}

// TestRequireActivePlatformUserBlocksBlockedUsers pins the blocked-user
// enforcement at session and order boundaries.
func TestRequireActivePlatformUserBlocksBlockedUsers(t *testing.T) {
	t.Parallel()
	repo := platformuser.NewMemoryRepository()
	if err := repo.Upsert(context.Background(), platformuser.User{MerchantID: "m1", ExternalUserID: "u1", Locale: "en-US", Status: "active"}); err != nil {
		t.Fatal(err)
	}
	if err := repo.Upsert(context.Background(), platformuser.User{MerchantID: "m1", ExternalUserID: "u2", Locale: "en-US", Status: "blocked"}); err != nil {
		t.Fatal(err)
	}
	handler := &v3Handler{platformUsers: repo}

	activeRec := httptest.NewRecorder()
	if !handler.requireActivePlatformUser(activeRec, httptest.NewRequest(http.MethodGet, "/", nil), "m1", "u1") {
		t.Fatalf("active user was rejected: %s", activeRec.Body.String())
	}

	blockedRec := httptest.NewRecorder()
	if handler.requireActivePlatformUser(blockedRec, httptest.NewRequest(http.MethodGet, "/", nil), "m1", "u2") {
		t.Fatal("blocked user was accepted")
	}
	if blockedRec.Code != http.StatusForbidden {
		t.Fatalf("blocked user status = %d, want 403", blockedRec.Code)
	}

	// Unknown users are treated as active: they are provisioned at session
	// creation, and a lookup miss must not break existing flows.
	unknownRec := httptest.NewRecorder()
	if !handler.requireActivePlatformUser(unknownRec, httptest.NewRequest(http.MethodGet, "/", nil), "m1", "nobody") {
		t.Fatalf("unknown user was rejected: %s", unknownRec.Body.String())
	}
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
		[]byte(`{"user_id":"site-user-8801","currency":"USD","balance":"88.50","locale":"zh-CN","return_url":"https://merchant.example/lobby"}`),
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
			User        struct {
				AvailableBalance string `json:"available_balance"`
			} `json:"user"`
		} `json:"data"`
	}{}
	if err := json.Unmarshal(exchanged.Body.Bytes(), &exchange); err != nil {
		t.Fatalf("decode exchange response: %v", err)
	}
	if exchange.Data.AccessToken == "" {
		t.Fatal("access token is empty")
	}
	if exchange.Data.User.AvailableBalance != "88.50" {
		t.Fatalf("exchange balance = %q, want 88.50", exchange.Data.User.AvailableBalance)
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
	crossingCreate := httptest.NewRequest(
		http.MethodPost,
		"/api/user/orders",
		bytes.NewBufferString(`{"market_id":"`+marketID+`","type":"buy","option":"Yes","amount":5,"price":0.65}`),
	)
	crossingCreate.Header.Set("Authorization", "Bearer "+accessToken)
	crossingCreate.Header.Set("Idempotency-Key", "hosted-order-002")
	crossingRecorder := httptest.NewRecorder()
	handler.ServeHTTP(crossingRecorder, crossingCreate)
	if crossingRecorder.Code != http.StatusCreated {
		t.Fatalf("crossing hosted create status = %d, body = %s", crossingRecorder.Code, crossingRecorder.Body.String())
	}
	crossingOrder := struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}{}
	if err := json.Unmarshal(crossingRecorder.Body.Bytes(), &crossingOrder); err != nil {
		t.Fatalf("decode crossing hosted order: %v", err)
	}
	userTrades := v3Request(
		t,
		handler,
		http.MethodGet,
		"/api/user/orders/"+crossingOrder.Data.ID+"/trades?limit=500",
		nil,
		"Bearer "+accessToken,
	)
	if userTrades.Code != http.StatusOK {
		t.Fatalf("hosted matched trades status = %d, body = %s", userTrades.Code, userTrades.Body.String())
	}
	assertEnrichedTradeResponse(t, userTrades.Body.Bytes(), "server-user", "hosted-user")
	merchantTrades = signedV3Request(t, handler, http.MethodGet, "/api/v2/trades?limit=500", nil, "")
	if merchantTrades.Code != http.StatusOK {
		t.Fatalf("v2 matched trades status = %d, body = %s", merchantTrades.Code, merchantTrades.Body.String())
	}
	assertEnrichedTradeResponse(t, merchantTrades.Body.Bytes(), "server-user", "hosted-user")

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

func assertEnrichedTradeResponse(t *testing.T, body []byte, makerUserID, takerUserID string) {
	t.Helper()
	page := struct {
		Data []*types.Trade `json:"data"`
	}{}
	if err := json.Unmarshal(body, &page); err != nil {
		t.Fatalf("decode trade response: %v", err)
	}
	if len(page.Data) != 1 {
		t.Fatalf("trades = %#v, want one execution", page.Data)
	}
	trade := page.Data[0]
	validParticipants := trade.MakerUserID == makerUserID && trade.MakerType == "sell" &&
		trade.TakerUserID == takerUserID && trade.TakerType == "buy"
	validAmounts := trade.MakerTradeAmount == "2.00" && trade.TakerTradeAmount == "3.00"
	if trade.Option != "Yes" || trade.Currency != "USD" || !validParticipants || !validAmounts ||
		trade.ImpliedDecimalOdds != 1.666667 {
		t.Errorf("enriched trade = %#v", trade)
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

// TestV3UserParimutuelBetFlow pins the hosted 奖池 flow: a parimutuel market
// must quote pool odds (not an order book) and accept stakes through
// /api/user/bets, debiting the wallet and joining the pool. Order-book
// markets must reject pool bets before any debit.
func TestV3UserParimutuelBetFlow(t *testing.T) {
	t.Parallel()

	merchantService := merchant.NewService()
	eventService := event.NewService()
	marketService := market.NewService()
	walletService := wallet.NewService()
	orderService := order.NewServiceWithDependencies(marketService, walletService)
	// The in-memory parimutuel repository validates bets against seeded
	// markets (the Postgres repository locks the real markets row).
	parimutuelRepo := parimutuel.NewMemoryRepository()
	parimutuelService := parimutuel.NewServiceWithRepository(parimutuelRepo)
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
			Parimutuel:      parimutuelService,
		},
	)
	credentials := registerMerchant(t, handler, "Pool Tenant", "pool-v3@example.test")
	signer.merchant = &types.Merchant{
		ID:         credentials.Data.MerchantID,
		Status:     "active",
		Currency:   "USD",
		WalletMode: "transfer",
	}
	eventID := createActiveEventForMarket(t, handler, "v3-pool-event")
	poolMarketID := createParimutuelMarketForMerchant(t, handler, credentials.Data.MerchantID, eventID)
	binaryMarketID := createMarketForMerchant(t, handler, credentials.Data.MerchantID, eventID)
	// The admin console seeds the pool row at market creation; the v3
	// handler shares the service, so seed it here for the test universe.
	parimutuelRepo.SeedMarket(poolMarketID, "parimutuel", "active", "active", []string{"Yes", "No"})
	if err := parimutuelService.CreatePools(context.Background(), poolMarketID, "USD"); err != nil {
		t.Fatalf("CreatePools() error = %v", err)
	}
	if _, err := walletService.Deposit(context.Background(), &wallet.TransferRequest{
		MerchantID:            credentials.Data.MerchantID,
		MerchantTransactionID: "fund-pool-user",
		UserID:                "pool-user",
		Currency:              "USD",
		Amount:                "20.00",
	}); err != nil {
		t.Fatalf("fund pool user: %v", err)
	}

	launchToken, _, err := manager.CreateLaunch(
		context.Background(),
		credentials.Data.MerchantID,
		"pool-user",
		"USD",
		"transfer",
		"zh-CN",
		"",
	)
	if err != nil {
		t.Fatalf("CreateLaunch() error = %v", err)
	}
	accessToken, _, err := manager.Exchange(context.Background(), launchToken)
	if err != nil {
		t.Fatalf("Exchange() error = %v", err)
	}

	initial := decodePoolsResponse(t, userPoolsRequest(t, handler, poolMarketID, accessToken).Body.Bytes())
	if initial.TotalStake != "0.00" || len(initial.Options) != 0 || initial.Currency != "USD" {
		t.Errorf("initial pools = %#v", initial)
	}

	first := userBetRequest(t, handler, poolMarketID, "Yes", 5, accessToken, "pool-bet-001")
	if first.Code != http.StatusCreated {
		t.Fatalf("first bet status = %d, body = %s", first.Code, first.Body.String())
	}
	firstBet := decodeBetResponse(t, first.Body.Bytes())
	if firstBet.Data.ID == "" || firstBet.Data.Option != "Yes" || firstBet.Data.Stake != 5 {
		t.Errorf("first bet = %#v", firstBet.Data)
	}
	if firstBet.Meta.AvailableBalance != "15.00" {
		t.Errorf("balance after first bet = %q, want 15.00", firstBet.Meta.AvailableBalance)
	}

	updated := decodePoolsResponse(t, userPoolsRequest(t, handler, poolMarketID, accessToken).Body.Bytes())
	if updated.TotalStake != "5.00" || len(updated.Options) != 1 || updated.Options[0].Option != "Yes" || updated.Options[0].Stake != 5 {
		t.Errorf("pools after first bet = %#v", updated)
	}
	if updated.Options[0].Odds != "1.00" {
		t.Errorf("odds after first bet = %q, want 1.00 (pool entirely on Yes)", updated.Options[0].Odds)
	}
	// The bet response carries the post-bet pool snapshot so the UI can show
	// the updated return rate without a second request.
	if firstBet.Meta.Pool.TotalStake != "5.00" || len(firstBet.Meta.Pool.Options) != 1 || firstBet.Meta.Pool.Options[0].Odds != "1.00" {
		t.Errorf("first bet meta.pool = %#v", firstBet.Meta.Pool)
	}

	second := userBetRequest(t, handler, poolMarketID, "Yes", 7.5, accessToken, "pool-bet-002")
	if second.Code != http.StatusCreated {
		t.Fatalf("second bet status = %d, body = %s", second.Code, second.Body.String())
	}
	// A No-side bet makes the per-option return rates distinct: the pool of
	// 15.00 splits 12.50 (Yes) vs 2.50 (No).
	noBet := userBetRequest(t, handler, poolMarketID, "No", 2.5, accessToken, "pool-bet-003-no")
	if noBet.Code != http.StatusCreated {
		t.Fatalf("no-side bet status = %d, body = %s", noBet.Code, noBet.Body.String())
	}
	final := decodePoolsResponse(t, userPoolsRequest(t, handler, poolMarketID, accessToken).Body.Bytes())
	if final.TotalStake != "15.00" || len(final.Options) != 2 {
		t.Fatalf("pools after third bet = %#v", final)
	}
	if final.Options[0].Option == "Yes" && (final.Options[0].Odds != "1.20" || final.Options[1].Odds != "6.00") {
		t.Errorf("pools odds = %s %s / %s %s, want Yes 1.20, No 6.00",
			final.Options[0].Option, final.Options[0].Odds, final.Options[1].Option, final.Options[1].Odds)
	}

	// The market list embeds the pool summary so list pages render stakes and
	// return rates without per-market requests.
	listed := v3Request(t, handler, http.MethodGet, "/api/user/markets?status=active", nil, "Bearer "+accessToken)
	marketList := struct {
		Data []struct {
			ID   string         `json:"id"`
			Pool map[string]any `json:"pool"`
		} `json:"data"`
	}{}
	if err := json.Unmarshal(listed.Body.Bytes(), &marketList); err != nil {
		t.Fatalf("decode market list: %v", err)
	}
	var listedPool map[string]any
	for _, item := range marketList.Data {
		if item.ID == poolMarketID {
			listedPool = item.Pool
			break
		}
	}
	if listedPool == nil {
		t.Fatalf("market list item %s is missing the pool summary", poolMarketID)
	}
	if listedPool["total_stake"] != "15.00" {
		t.Errorf("list pool total_stake = %v, want 15.00", listedPool["total_stake"])
	}
	rawOptions, ok := listedPool["options"].([]any)
	if !ok || len(rawOptions) != 2 {
		t.Fatalf("list pool options = %#v", listedPool["options"])
	}
	oddsByOption := map[string]string{}
	for _, raw := range rawOptions {
		entry := raw.(map[string]any)
		oddsByOption[entry["option"].(string)] = entry["odds"].(string)
	}
	if oddsByOption["Yes"] != "1.20" || oddsByOption["No"] != "6.00" {
		t.Errorf("list pool odds = %v, want Yes 1.20, No 6.00", oddsByOption)
	}

	profile := v3Request(t, handler, http.MethodGet, "/api/user/me", nil, "Bearer "+accessToken)
	me := struct {
		Data struct {
			AvailableBalance string `json:"available_balance"`
		} `json:"data"`
	}{}
	if err := json.Unmarshal(profile.Body.Bytes(), &me); err != nil {
		t.Fatalf("decode profile: %v", err)
	}
	if me.Data.AvailableBalance != "5.00" {
		t.Errorf("balance after third bet = %q, want 5.00", me.Data.AvailableBalance)
	}

	// Order-book markets refuse pool bets before any wallet debit.
	rejected := userBetRequest(t, handler, binaryMarketID, "Yes", 5, accessToken, "pool-bet-003")
	if rejected.Code != http.StatusBadRequest {
		t.Errorf("binary bet status = %d, want %d; body = %s", rejected.Code, http.StatusBadRequest, rejected.Body.String())
	}
	binaryPools := userPoolsRequest(t, handler, binaryMarketID, accessToken)
	if binaryPools.Code != http.StatusBadRequest {
		t.Errorf("binary pools status = %d, want %d; body = %s", binaryPools.Code, http.StatusBadRequest, binaryPools.Body.String())
	}

	// Bets never leak into the order-book order history.
	orders := v3Request(t, handler, http.MethodGet, "/api/user/orders?limit=500", nil, "Bearer "+accessToken)
	orderList := struct {
		Data []map[string]any `json:"data"`
	}{}
	if err := json.Unmarshal(orders.Body.Bytes(), &orderList); err != nil {
		t.Fatalf("decode order list: %v", err)
	}
	if len(orderList.Data) != 0 {
		t.Errorf("order list = %#v, want empty", orderList.Data)
	}
}

func createParimutuelMarketForMerchant(
	t *testing.T,
	handler http.Handler,
	merchantID string,
	eventID string,
) string {
	t.Helper()
	response := performRequest(
		t,
		handler,
		http.MethodPost,
		"/api/v1/markets",
		[]byte(fmt.Sprintf(`{
			"merchant_id":%q,
			"event_id":%q,
			"type":"parimutuel",
			"question":"Pool market",
			"options":["Yes","No"],
			"liquidity_pool":0
		}`, merchantID, eventID)),
		"Bearer admin-secret",
	)
	if response.Code != http.StatusCreated {
		t.Fatalf("create parimutuel market status = %d, body = %s", response.Code, response.Body.String())
	}
	return decodeMarketResponse(t, response.Body.Bytes()).Data.ID
}

func userPoolsRequest(
	t *testing.T,
	handler http.Handler,
	marketID string,
	token string,
) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, "/api/user/markets/"+marketID+"/pools", nil)
	request.Header.Set("Authorization", "Bearer "+token)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	return recorder
}

func userBetRequest(
	t *testing.T,
	handler http.Handler,
	marketID string,
	option string,
	amount float64,
	token string,
	idempotencyKey string,
) *httptest.ResponseRecorder {
	t.Helper()
	body := fmt.Sprintf(`{"market_id":%q,"option":%q,"amount":%v}`, marketID, option, amount)
	request := httptest.NewRequest(http.MethodPost, "/api/user/bets", bytes.NewBufferString(body))
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Idempotency-Key", idempotencyKey)
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	return recorder
}

type userPoolView struct {
	MarketID   string `json:"market_id"`
	Currency   string `json:"currency"`
	TotalStake string `json:"total_stake"`
	Options    []struct {
		Option string  `json:"option"`
		Stake  float64 `json:"stake"`
		Odds   string  `json:"odds"`
	} `json:"options"`
}

func decodePoolsResponse(t *testing.T, body []byte) userPoolView {
	t.Helper()
	var response struct {
		Data userPoolView `json:"data"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		t.Fatalf("decode pools response: %v", err)
	}
	return response.Data
}

type userBetView struct {
	Data struct {
		ID     string  `json:"id"`
		Option string  `json:"option"`
		Stake  float64 `json:"stake"`
	} `json:"data"`
	Meta struct {
		AvailableBalance string       `json:"available_balance"`
		Pool             userPoolView `json:"pool"`
	} `json:"meta"`
}

func decodeBetResponse(t *testing.T, body []byte) userBetView {
	t.Helper()
	var response userBetView
	if err := json.Unmarshal(body, &response); err != nil {
		t.Fatalf("decode bet response: %v", err)
	}
	return response
}

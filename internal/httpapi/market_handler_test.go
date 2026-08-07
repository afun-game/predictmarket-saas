package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/afun-game/predictmarket-saas/internal/currency"
	"github.com/afun-game/predictmarket-saas/internal/event"
	"github.com/afun-game/predictmarket-saas/internal/market"
	"github.com/afun-game/predictmarket-saas/internal/merchant"
	"github.com/afun-game/predictmarket-saas/internal/order"
	"github.com/afun-game/predictmarket-saas/internal/parimutuel"
	"github.com/afun-game/predictmarket-saas/internal/wallet"
)

type marketResponse struct {
	Data struct {
		ID            string   `json:"id"`
		MerchantID    string   `json:"merchant_id"`
		EventID       string   `json:"event_id"`
		Options       []string `json:"options"`
		Status        string   `json:"status"`
		LiquidityPool float64  `json:"liquidity_pool"`
	} `json:"data"`
}

func TestMarketHTTPFlow(t *testing.T) {
	t.Parallel()

	merchantService := merchant.NewService()
	eventService := event.NewService()
	marketService := market.NewService()
	walletService := wallet.NewService()
	orderService := order.NewServiceWithDependencies(marketService, walletService)
	handler := NewHandler(
		merchantService,
		eventService,
		marketService,
		walletService,
		orderService,
		currency.NewService(),
		"admin-secret",
	)
	credentials := registerMerchant(t, handler, "Market Tenant", "market@example.test")
	eventID := createActiveEventForMarket(t, handler, "market-flow-event")

	response := performRequest(
		t,
		handler,
		http.MethodPost,
		"/api/v1/markets",
		[]byte(fmt.Sprintf(`{
			"merchant_id":%q,
			"event_id":%q,
			"type":"binary",
			"question":"Will this market flow pass?",
			"options":["Yes","No"],
			"liquidity_pool":100
		}`, credentials.Data.MerchantID, eventID)),
		"Bearer admin-secret",
	)
	if response.Code != http.StatusCreated {
		t.Fatalf("create market status = %d, body = %s", response.Code, response.Body.String())
	}
	created := decodeMarketResponse(t, response.Body.Bytes())
	validCreation := created.Data.ID != "" && created.Data.Status == "active"
	if !validCreation || created.Data.MerchantID != credentials.Data.MerchantID {
		t.Fatalf("created market = %#v", created.Data)
	}

	authorization := "Bearer " + credentials.Data.APIKey
	response = performRequest(
		t,
		handler,
		http.MethodGet,
		"/api/v1/markets?event_id="+eventID+"&status=active&page=1&limit=10",
		nil,
		authorization,
	)
	if response.Code != http.StatusOK {
		t.Fatalf("list markets status = %d, body = %s", response.Code, response.Body.String())
	}
	assertMarketListResponse(t, response.Body.Bytes(), created.Data.ID)

	response = performRequest(
		t,
		handler,
		http.MethodGet,
		"/api/v1/markets?sort=popular",
		nil,
		authorization,
	)
	if response.Code != http.StatusOK {
		t.Fatalf("list popular markets status = %d, body = %s", response.Code, response.Body.String())
	}
	assertMarketListResponse(t, response.Body.Bytes(), created.Data.ID)

	response = performRequest(
		t,
		handler,
		http.MethodGet,
		"/api/v1/markets?sort=oldest",
		nil,
		authorization,
	)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("invalid market sort status = %d, body = %s", response.Code, response.Body.String())
	}

	response = performRequest(
		t,
		handler,
		http.MethodGet,
		"/api/v1/markets/"+created.Data.ID,
		nil,
		authorization,
	)
	if response.Code != http.StatusOK {
		t.Fatalf("get market status = %d, body = %s", response.Code, response.Body.String())
	}
	response = performRequest(
		t,
		handler,
		http.MethodGet,
		"/api/v1/markets/"+created.Data.ID+"/orderbook",
		nil,
		authorization,
	)
	if response.Code != http.StatusOK {
		t.Fatalf("get order book status = %d, body = %s", response.Code, response.Body.String())
	}
	assertEmptyOrderBook(t, response.Body.Bytes(), created.Data.ID)

	response = performRequest(
		t,
		handler,
		http.MethodPost,
		"/api/v1/markets/"+created.Data.ID+"/liquidity",
		[]byte(`{"amount":25.50}`),
		"Bearer admin-secret",
	)
	if response.Code != http.StatusNoContent {
		t.Fatalf("add liquidity status = %d, body = %s", response.Code, response.Body.String())
	}

	for _, status := range []string{"suspended", "active", "closed"} {
		response = performRequest(
			t,
			handler,
			http.MethodPatch,
			"/api/v1/markets/"+created.Data.ID+"/status",
			[]byte(`{"status":"`+status+`"}`),
			"Bearer admin-secret",
		)
		if response.Code != http.StatusNoContent {
			t.Fatalf("update market to %s status = %d, body = %s", status, response.Code, response.Body.String())
		}
	}
	response = performRequest(
		t,
		handler,
		http.MethodPost,
		"/api/v1/markets/"+created.Data.ID+"/settle",
		[]byte(`{"winning_option":"Yes"}`),
		"Bearer admin-secret",
	)
	if response.Code != http.StatusNotFound {
		t.Fatalf("manual settlement status = %d, want %d", response.Code, http.StatusNotFound)
	}

	response = performRequest(
		t,
		handler,
		http.MethodGet,
		"/api/v1/markets/"+created.Data.ID,
		nil,
		authorization,
	)
	closed := decodeMarketResponse(t, response.Body.Bytes())
	if closed.Data.Status != "closed" {
		t.Errorf("closed market = %#v", closed.Data)
	}
	if closed.Data.LiquidityPool != 125.50 {
		t.Errorf("LiquidityPool = %v, want 125.50", closed.Data.LiquidityPool)
	}
}

func TestMarketHTTPAuthorizationAndTenantIsolation(t *testing.T) {
	t.Parallel()

	merchantService := merchant.NewService()
	marketService := market.NewService()
	walletService := wallet.NewService()
	handler := NewHandler(
		merchantService,
		event.NewService(),
		marketService,
		walletService,
		order.NewServiceWithDependencies(marketService, walletService),
		currency.NewService(),
		"admin-secret",
	)
	first := registerMerchant(t, handler, "First Market Tenant", "first-market@example.test")
	second := registerMerchant(t, handler, "Second Market Tenant", "second-market@example.test")
	eventID := createActiveEventForMarket(t, handler, "market-auth-event")
	marketID := createMarketForMerchant(t, handler, first.Data.MerchantID, eventID)

	response := performRequest(t, handler, http.MethodGet, "/api/v1/markets", nil, "")
	if response.Code != http.StatusUnauthorized {
		t.Errorf("unauthenticated list status = %d, want %d", response.Code, http.StatusUnauthorized)
	}
	response = performRequest(
		t,
		handler,
		http.MethodPost,
		"/api/v1/markets",
		[]byte(`{}`),
		"Bearer wrong-key",
	)
	if response.Code != http.StatusUnauthorized {
		t.Errorf("non-admin create status = %d, want %d", response.Code, http.StatusUnauthorized)
	}
	response = performRequest(
		t,
		handler,
		http.MethodGet,
		"/api/v1/markets/"+marketID,
		nil,
		"Bearer "+second.Data.APIKey,
	)
	if response.Code != http.StatusNotFound {
		t.Errorf("cross-tenant get status = %d, want %d", response.Code, http.StatusNotFound)
	}
	response = performRequest(
		t,
		handler,
		http.MethodGet,
		"/api/v1/markets",
		nil,
		"Bearer "+second.Data.APIKey,
	)
	assertMarketListResponse(t, response.Body.Bytes(), "")

	response = performRequest(
		t,
		handler,
		http.MethodPost,
		"/api/v1/markets",
		[]byte(`{"merchant_id":"bad"}`),
		"Bearer admin-secret",
	)
	if response.Code != http.StatusBadRequest {
		t.Errorf("invalid create status = %d, want %d", response.Code, http.StatusBadRequest)
	}
	response = performRequest(
		t,
		handler,
		http.MethodPost,
		"/api/v1/markets/"+marketID+"/settle",
		[]byte(`{"winning_option":"Yes"}`),
		"Bearer admin-secret",
	)
	if response.Code != http.StatusNotFound {
		t.Errorf("manual settlement status = %d, want %d", response.Code, http.StatusNotFound)
	}
}

func createActiveEventForMarket(t *testing.T, handler http.Handler, sourceID string) string {
	t.Helper()
	response := performRequest(
		t,
		handler,
		http.MethodPost,
		"/api/v1/events",
		[]byte(fmt.Sprintf(`{
			"source_type":"custom",
			"source_id":%q,
			"title":"Market HTTP event",
			"category":"test",
			"end_time":"2027-08-10T12:00:00Z",
			"resolution_time":"2027-08-10T13:00:00Z"
		}`, sourceID)),
		"Bearer admin-secret",
	)
	if response.Code != http.StatusCreated {
		t.Fatalf("create event status = %d, body = %s", response.Code, response.Body.String())
	}
	var created eventResponse
	if err := json.Unmarshal(response.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode event response: %v", err)
	}
	response = performRequest(
		t,
		handler,
		http.MethodPatch,
		"/api/v1/events/"+created.Data.ID+"/status",
		[]byte(`{"status":"active"}`),
		"Bearer admin-secret",
	)
	if response.Code != http.StatusNoContent {
		t.Fatalf("activate event status = %d, body = %s", response.Code, response.Body.String())
	}
	return created.Data.ID
}

func createMarketForMerchant(
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
			"type":"binary",
			"question":"Tenant market",
			"options":["Yes","No"],
			"liquidity_pool":0
		}`, merchantID, eventID)),
		"Bearer admin-secret",
	)
	if response.Code != http.StatusCreated {
		t.Fatalf("create market status = %d, body = %s", response.Code, response.Body.String())
	}
	return decodeMarketResponse(t, response.Body.Bytes()).Data.ID
}

func decodeMarketResponse(t *testing.T, body []byte) marketResponse {
	t.Helper()
	var response marketResponse
	if err := json.Unmarshal(body, &response); err != nil {
		t.Fatalf("decode market response: %v", err)
	}
	return response
}

func assertMarketListResponse(t *testing.T, body []byte, marketID string) {
	t.Helper()
	var response struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
		Meta struct {
			Pagination pagination `json:"pagination"`
		} `json:"meta"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		t.Fatalf("decode market list: %v", err)
	}
	wantLength := 0
	if marketID != "" {
		wantLength = 1
	}
	if len(response.Data) != wantLength {
		t.Fatalf("market list = %#v", response.Data)
	}
	if marketID != "" && response.Data[0].ID != marketID {
		t.Errorf("market list ID = %q, want %q", response.Data[0].ID, marketID)
	}
	if response.Meta.Pagination.Total != wantLength {
		t.Errorf("pagination = %#v", response.Meta.Pagination)
	}
}

func assertEmptyOrderBook(t *testing.T, body []byte, marketID string) {
	t.Helper()
	var response struct {
		Data struct {
			MarketID string            `json:"market_id"`
			Bids     []json.RawMessage `json:"bids"`
			Asks     []json.RawMessage `json:"asks"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		t.Fatalf("decode order book: %v", err)
	}
	validBook := response.Data.MarketID == marketID
	emptyBook := response.Data.Bids != nil && response.Data.Asks != nil
	if !validBook || !emptyBook || len(response.Data.Bids) != 0 || len(response.Data.Asks) != 0 {
		t.Errorf("order book = %#v", response.Data)
	}
}

func TestWriteMarketServiceErrorMapsExpiredEvent(t *testing.T) {
	t.Parallel()

	recorder := httptest.NewRecorder()
	writeMarketServiceError(recorder, market.ErrEventExpired)
	if recorder.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusUnprocessableEntity)
	}
	var payload struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode error body: %v", err)
	}
	if payload.Error.Code != "event_expired" {
		t.Errorf("code = %q, want event_expired", payload.Error.Code)
	}
}

// TestV1MarketListEmbedsPoolSummary pins the merchant market list carrying
// the same parimutuel pool summary as the hosted endpoints when the V3
// enrichment services are wired.
func TestV1MarketListEmbedsPoolSummary(t *testing.T) {
	t.Parallel()

	merchantService := merchant.NewService()
	eventService := event.NewService()
	marketService := market.NewService()
	walletService := wallet.NewService()
	orderService := order.NewServiceWithDependencies(marketService, walletService)
	parimutuelRepo := parimutuel.NewMemoryRepository()
	parimutuelService := parimutuel.NewServiceWithRepository(parimutuelRepo)
	handler := NewHandler(
		merchantService,
		eventService,
		marketService,
		walletService,
		orderService,
		currency.NewService(),
		"admin-secret",
		V3Config{Parimutuel: parimutuelService},
	)
	credentials := registerMerchant(t, handler, "V1 Pool Tenant", "v1-pool@example.test")
	eventID := createActiveEventForMarket(t, handler, "v1-pool-event")
	marketID := createParimutuelMarketForMerchant(t, handler, credentials.Data.MerchantID, eventID)
	parimutuelRepo.SeedMarket(marketID, "parimutuel", "active", "active", []string{"Yes", "No"})
	if err := parimutuelService.CreatePools(context.Background(), marketID, "USD"); err != nil {
		t.Fatalf("CreatePools() error = %v", err)
	}
	if _, err := parimutuelService.PlaceBet(context.Background(), parimutuel.Bet{
		MarketID: marketID, MerchantID: credentials.Data.MerchantID, UserID: "v1-pool-user",
		Option: "Yes", Stake: 20, Currency: "USD",
	}); err != nil {
		t.Fatalf("PlaceBet() error = %v", err)
	}

	response := performRequest(t, handler, http.MethodGet, "/api/v1/markets", nil, "Bearer "+credentials.Data.APIKey)
	if response.Code != http.StatusOK {
		t.Fatalf("v1 markets status = %d, body = %s", response.Code, response.Body.String())
	}
	var payload struct {
		Data []struct {
			ID   string         `json:"id"`
			Pool map[string]any `json:"pool"`
		} `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode v1 markets: %v", err)
	}
	var found bool
	for _, item := range payload.Data {
		if item.ID != marketID {
			continue
		}
		found = true
		if item.Pool == nil {
			t.Fatal("v1 market item is missing the pool summary")
		}
		if item.Pool["total_stake"] != "20.00" {
			t.Errorf("v1 pool total_stake = %v, want 20.00", item.Pool["total_stake"])
		}
		options, ok := item.Pool["options"].([]any)
		if !ok || len(options) != 1 || options[0].(map[string]any)["odds"] != "1.00" {
			t.Errorf("v1 pool options = %#v", item.Pool["options"])
		}
	}
	if !found {
		t.Fatalf("v1 market list did not include %s", marketID)
	}
}

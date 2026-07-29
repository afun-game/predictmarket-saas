package httpapi

import (
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
	"github.com/afun-game/predictmarket-saas/internal/wallet"
)

type orderResponse struct {
	Data struct {
		ID           string  `json:"id"`
		MerchantID   string  `json:"merchant_id"`
		UserID       string  `json:"user_id"`
		Status       string  `json:"status"`
		Amount       float64 `json:"amount"`
		FilledAmount float64 `json:"filled_amount"`
	} `json:"data"`
}

func TestOrderHTTPMatchingAndOrderBook(t *testing.T) {
	t.Parallel()
	handler := newOrderHTTPTestHandler()
	credentials := registerMerchant(t, handler, "Order Tenant", "order-http@example.test")
	authorization := "Bearer " + credentials.Data.APIKey
	eventID := createActiveEventForMarket(t, handler, "order-http-event")
	marketID := createMarketForMerchant(t, handler, credentials.Data.MerchantID, eventID)
	creditHTTPWallet(t, handler, authorization, "seller", 100)
	creditHTTPWallet(t, handler, authorization, "buyer", 100)

	seller := createHTTPOrder(t, handler, authorization, marketID, "seller", "sell", 30, 0.50, "gtc")
	if seller.Data.Status != "pending" {
		t.Fatalf("seller order = %#v", seller.Data)
	}
	bookResponse := performRequest(
		t,
		handler,
		http.MethodGet,
		"/api/v1/markets/"+marketID+"/orderbook",
		nil,
		authorization,
	)
	assertOrderBookLevel(t, bookResponse, "asks", "Yes", 0.50, 30, 1)

	buyer := createHTTPOrder(t, handler, authorization, marketID, "buyer", "buy", 20, 0.60, "gtc")
	if buyer.Data.Status != "filled" || buyer.Data.FilledAmount != 20 {
		t.Fatalf("buyer order = %#v", buyer.Data)
	}
	response := performRequest(
		t,
		handler,
		http.MethodGet,
		"/api/v1/orders/"+seller.Data.ID,
		nil,
		authorization,
	)
	updatedSeller := decodeOrderResponse(t, response)
	if updatedSeller.Data.Status != "partial" || updatedSeller.Data.FilledAmount != 20 {
		t.Errorf("updated seller = %#v", updatedSeller.Data)
	}
	bookResponse = performRequest(
		t,
		handler,
		http.MethodGet,
		"/api/v1/markets/"+marketID+"/orderbook",
		nil,
		authorization,
	)
	assertOrderBookLevel(t, bookResponse, "asks", "Yes", 0.50, 10, 1)

	response = performRequest(
		t,
		handler,
		http.MethodDelete,
		"/api/v1/orders/"+seller.Data.ID,
		nil,
		authorization,
	)
	if response.Code != http.StatusOK {
		t.Fatalf("cancel order status = %d, body = %s", response.Code, response.Body.String())
	}
	bookResponse = performRequest(
		t,
		handler,
		http.MethodGet,
		"/api/v1/markets/"+marketID+"/orderbook",
		nil,
		authorization,
	)
	assertEmptyOrderBook(t, bookResponse.Body.Bytes(), marketID)
	response = performRequest(
		t,
		handler,
		http.MethodGet,
		"/api/v1/wallets/seller",
		nil,
		authorization,
	)
	assertBalanceResponse(t, response.Body.Bytes(), "seller", "USD", 90, 10)

	response = performRequest(
		t,
		handler,
		http.MethodGet,
		"/api/v1/orders?user_id=seller&status=cancelled&page=1&limit=10",
		nil,
		authorization,
	)
	assertOrderListResponse(t, response, seller.Data.ID, 1)
}

func TestOrderHTTPIOCAndTenantIsolation(t *testing.T) {
	t.Parallel()
	handler := newOrderHTTPTestHandler()
	first := registerMerchant(t, handler, "First Order Tenant", "first-order@example.test")
	second := registerMerchant(t, handler, "Second Order Tenant", "second-order@example.test")
	firstAuthorization := "Bearer " + first.Data.APIKey
	secondAuthorization := "Bearer " + second.Data.APIKey
	eventID := createActiveEventForMarket(t, handler, "order-http-auth-event")
	marketID := createMarketForMerchant(t, handler, first.Data.MerchantID, eventID)
	creditHTTPWallet(t, handler, firstAuthorization, "ioc-user", 100)
	ioc := createHTTPOrder(t, handler, firstAuthorization, marketID, "ioc-user", "buy", 25, 0.40, "ioc")
	if ioc.Data.Status != "cancelled" || ioc.Data.FilledAmount != 0 {
		t.Errorf("IOC order = %#v", ioc.Data)
	}
	response := performRequest(
		t,
		handler,
		http.MethodGet,
		"/api/v1/wallets/ioc-user",
		nil,
		firstAuthorization,
	)
	assertBalanceResponse(t, response.Body.Bytes(), "ioc-user", "USD", 100, 0)

	response = performRequest(
		t,
		handler,
		http.MethodGet,
		"/api/v1/orders/"+ioc.Data.ID,
		nil,
		secondAuthorization,
	)
	if response.Code != http.StatusNotFound {
		t.Errorf("cross-tenant get status = %d, want %d", response.Code, http.StatusNotFound)
	}
	response = performRequest(
		t,
		handler,
		http.MethodDelete,
		"/api/v1/orders/"+ioc.Data.ID,
		nil,
		secondAuthorization,
	)
	if response.Code != http.StatusNotFound {
		t.Errorf("cross-tenant cancel status = %d, want %d", response.Code, http.StatusNotFound)
	}
	response = performRequest(
		t,
		handler,
		http.MethodPost,
		"/api/v1/orders",
		[]byte(`{}`),
		"",
	)
	if response.Code != http.StatusUnauthorized {
		t.Errorf("unauthenticated create status = %d, want %d", response.Code, http.StatusUnauthorized)
	}
	response = performRequest(
		t,
		handler,
		http.MethodPost,
		"/api/v1/orders",
		[]byte(fmt.Sprintf(`{
			"user_id":"other-user","market_id":%q,"type":"buy","option":"Yes",
			"amount":10,"currency":"USD","price":0.5
		}`, marketID)),
		secondAuthorization,
	)
	if response.Code != http.StatusUnprocessableEntity {
		t.Errorf("cross-tenant create status = %d, want %d", response.Code, http.StatusUnprocessableEntity)
	}
}

func TestOrderHTTPRequiresAndReusesIdempotencyKey(t *testing.T) {
	t.Parallel()
	handler := newOrderHTTPTestHandler()
	credentials := registerMerchant(t, handler, "Idempotent Order Tenant", "idempotent-order@example.test")
	authorization := "Bearer " + credentials.Data.APIKey
	eventID := createActiveEventForMarket(t, handler, "idempotent-order-event")
	marketID := createMarketForMerchant(t, handler, credentials.Data.MerchantID, eventID)
	creditHTTPWallet(t, handler, authorization, "idempotent-user", 100)
	body := []byte(fmt.Sprintf(`{
		"user_id":"idempotent-user","market_id":%q,"type":"buy","option":"Yes",
		"amount":10,"currency":"USD","price":0.5
	}`, marketID))

	missingKey := performRequestWithIdempotency(
		t,
		handler,
		http.MethodPost,
		"/api/v1/orders",
		body,
		authorization,
		"",
	)
	if missingKey.Code != http.StatusPreconditionRequired {
		t.Fatalf("missing idempotency key status = %d, body = %s", missingKey.Code, missingKey.Body.String())
	}

	first := performRequestWithIdempotency(
		t,
		handler,
		http.MethodPost,
		"/api/v1/orders",
		body,
		authorization,
		"order-retry-key",
	)
	second := performRequestWithIdempotency(
		t,
		handler,
		http.MethodPost,
		"/api/v1/orders",
		body,
		authorization,
		"order-retry-key",
	)
	if first.Code != http.StatusCreated || second.Code != http.StatusCreated {
		t.Fatalf("idempotent order statuses = (%d, %d)", first.Code, second.Code)
	}
	firstOrder := decodeOrderResponse(t, first)
	secondOrder := decodeOrderResponse(t, second)
	if firstOrder.Data.ID != secondOrder.Data.ID {
		t.Errorf("idempotent order IDs = (%q, %q)", firstOrder.Data.ID, secondOrder.Data.ID)
	}
	response := performRequest(
		t,
		handler,
		http.MethodGet,
		"/api/v1/wallets/idempotent-user",
		nil,
		authorization,
	)
	assertBalanceResponse(t, response.Body.Bytes(), "idempotent-user", "USD", 95, 5)
}

func newOrderHTTPTestHandler() http.Handler {
	merchantService := merchant.NewService()
	eventService := event.NewService()
	marketService := market.NewService()
	walletService := wallet.NewService()
	orderService := order.NewServiceWithDependencies(marketService, walletService)
	return NewHandler(
		merchantService,
		eventService,
		marketService,
		walletService,
		orderService,
		currency.NewService(),
		"admin-secret",
	)
}

func creditHTTPWallet(
	t *testing.T,
	handler http.Handler,
	authorization string,
	userID string,
	amount float64,
) {
	t.Helper()
	response := performRequest(
		t,
		handler,
		http.MethodPost,
		"/api/v1/wallets/"+userID+"/credit",
		[]byte(fmt.Sprintf(`{"currency":"USD","amount":%v}`, amount)),
		authorization,
	)
	if response.Code != http.StatusOK {
		t.Fatalf("credit wallet status = %d, body = %s", response.Code, response.Body.String())
	}
}

func createHTTPOrder(
	t *testing.T,
	handler http.Handler,
	authorization string,
	marketID string,
	userID string,
	side string,
	amount float64,
	price float64,
	timeInForce string,
) orderResponse {
	t.Helper()
	body := []byte(fmt.Sprintf(`{
		"user_id":%q,"market_id":%q,"type":%q,"option":"Yes",
		"amount":%v,"currency":"USD","price":%v,"time_in_force":%q
	}`, userID, marketID, side, amount, price, timeInForce))
	response := performRequest(t, handler, http.MethodPost, "/api/v1/orders", body, authorization)
	if response.Code != http.StatusCreated {
		t.Fatalf("create order status = %d, body = %s", response.Code, response.Body.String())
	}
	return decodeOrderResponse(t, response)
}

func decodeOrderResponse(t *testing.T, response *httptest.ResponseRecorder) orderResponse {
	t.Helper()
	var value orderResponse
	if err := json.Unmarshal(response.Body.Bytes(), &value); err != nil {
		t.Fatalf("decode order response: %v", err)
	}
	return value
}

func assertOrderBookLevel(
	t *testing.T,
	response *httptest.ResponseRecorder,
	side string,
	option string,
	price float64,
	amount float64,
	orders int,
) {
	t.Helper()
	if response.Code != http.StatusOK {
		t.Fatalf("order book status = %d, body = %s", response.Code, response.Body.String())
	}
	var value struct {
		Data struct {
			Bids []market.OrderBookEntry `json:"bids"`
			Asks []market.OrderBookEntry `json:"asks"`
		} `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &value); err != nil {
		t.Fatalf("decode order book response: %v", err)
	}
	levels := value.Data.Bids
	if side == "asks" {
		levels = value.Data.Asks
	}
	if len(levels) != 1 {
		t.Fatalf("%s = %#v", side, levels)
	}
	level := levels[0]
	validIdentity := level.Option == option && level.Price == price
	if !validIdentity || level.Amount != amount || level.Orders != orders {
		t.Errorf("order book level = %#v", level)
	}
}

func assertOrderListResponse(
	t *testing.T,
	response *httptest.ResponseRecorder,
	orderID string,
	total int,
) {
	t.Helper()
	if response.Code != http.StatusOK {
		t.Fatalf("list orders status = %d, body = %s", response.Code, response.Body.String())
	}
	var value struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
		Meta struct {
			Pagination pagination `json:"pagination"`
		} `json:"meta"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &value); err != nil {
		t.Fatalf("decode order list: %v", err)
	}
	if len(value.Data) != total || value.Meta.Pagination.Total != total {
		t.Fatalf("order list = %#v, pagination = %#v", value.Data, value.Meta.Pagination)
	}
	if total > 0 && value.Data[0].ID != orderID {
		t.Errorf("order list ID = %q, want %q", value.Data[0].ID, orderID)
	}
}

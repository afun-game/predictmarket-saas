package order

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/afun-game/predictmarket-saas/internal/market"
	"github.com/afun-game/predictmarket-saas/internal/wallet"
	"github.com/afun-game/predictmarket-saas/pkg/types"
)

const (
	testMerchantID = "11111111-1111-4111-8111-111111111111"
	testEventID    = "22222222-2222-4222-8222-222222222222"
)

func TestPriceTimePriorityMatching(t *testing.T) {
	fixture := newOrderFixture(t)
	firstTime := time.Date(2027, time.January, 1, 10, 0, 0, 0, time.UTC)
	fixture.service.now = func() time.Time { return firstTime }
	firstMaker := fixture.createOrder(t, "maker-first", "sell", 30, 0.60, "gtc")
	fixture.service.now = func() time.Time { return firstTime.Add(time.Minute) }
	bestMaker := fixture.createOrder(t, "maker-best", "sell", 40, 0.50, "gtc")
	fixture.service.now = func() time.Time { return firstTime.Add(2 * time.Minute) }
	incoming := fixture.createOrder(t, "taker", "buy", 50, 0.65, "gtc")

	if incoming.Status != "filled" || incoming.FilledAmount != 50 || incoming.FilledAt == nil {
		t.Fatalf("incoming order = %#v", incoming)
	}
	bestMaker = fixture.getOrder(t, bestMaker.ID)
	if bestMaker.Status != "filled" || bestMaker.FilledAmount != 40 {
		t.Errorf("best-price maker = %#v", bestMaker)
	}
	firstMaker = fixture.getOrder(t, firstMaker.ID)
	if firstMaker.Status != "partial" || firstMaker.FilledAmount != 10 {
		t.Errorf("later-price maker = %#v", firstMaker)
	}

	book, err := fixture.service.GetOrderBook(context.Background(), fixture.marketID)
	if err != nil {
		t.Fatalf("GetOrderBook() error = %v", err)
	}
	if len(book.Bids) != 0 || len(book.Asks) != 1 {
		t.Fatalf("order book = %#v", book)
	}
	ask := book.Asks[0]
	if ask.Option != "Yes" || ask.Price != 0.60 || ask.Amount != 20 || ask.Orders != 1 {
		t.Errorf("ask level = %#v", ask)
	}

	trades, err := fixture.service.ListTrades(context.Background(), &TradeListFilters{
		MerchantID: testMerchantID,
		OrderID:    incoming.ID,
		Limit:      10,
	})
	if err != nil {
		t.Fatalf("ListTrades() error = %v", err)
	}
	if len(trades.Trades) != 2 {
		t.Fatalf("trades = %#v, want two executions", trades.Trades)
	}
	assertTradeDetails(t, trades.Trades, tradeDetailsExpectation{
		MakerUserID: "maker-best",
		MakerAmount: "20.00",
		TakerAmount: "20.00",
		ImpliedOdds: 2,
	})
	assertTradeDetails(t, trades.Trades, tradeDetailsExpectation{
		MakerUserID: "maker-first",
		MakerAmount: "4.00",
		TakerAmount: "6.00",
		ImpliedOdds: 1.666667,
	})
}

type tradeDetailsExpectation struct {
	MakerUserID string
	MakerAmount string
	TakerAmount string
	ImpliedOdds float64
}

func assertTradeDetails(
	t *testing.T,
	trades []*types.Trade,
	want tradeDetailsExpectation,
) {
	t.Helper()
	for _, trade := range trades {
		if trade.MakerUserID != want.MakerUserID {
			continue
		}
		validMarket := trade.Option == "Yes" && trade.Currency == "USD"
		validParties := trade.MakerType == "sell" &&
			trade.TakerUserID == "taker" && trade.TakerType == "buy"
		validAmounts := trade.MakerTradeAmount == want.MakerAmount &&
			trade.TakerTradeAmount == want.TakerAmount
		if !validMarket || !validParties || !validAmounts ||
			trade.ImpliedDecimalOdds != want.ImpliedOdds {
			t.Errorf("trade details = %#v", trade)
		}
		return
	}
	t.Errorf("trade for maker %q not found in %#v", want.MakerUserID, trades)
}

func TestEnrichTradeComputesBothParticipantAmounts(t *testing.T) {
	tests := []struct {
		name        string
		makerType   string
		takerType   string
		makerAmount string
		takerAmount string
	}{
		{
			name:        "maker buys selected option",
			makerType:   "buy",
			takerType:   "sell",
			makerAmount: "6.00",
			takerAmount: "4.00",
		},
		{
			name:        "maker sells selected option",
			makerType:   "sell",
			takerType:   "buy",
			makerAmount: "4.00",
			takerAmount: "6.00",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			trade := &types.Trade{
				MakerType:    test.makerType,
				TakerType:    test.takerType,
				Shares:       10,
				MatchedPrice: 0.6,
			}
			enrichTrade(trade)
			if trade.MakerTradeAmount != test.makerAmount ||
				trade.TakerTradeAmount != test.takerAmount || trade.ImpliedDecimalOdds != 1.666667 {
				t.Errorf("enriched trade = %#v", trade)
			}
		})
	}
}

func TestSamePriceUsesCreationTime(t *testing.T) {
	fixture := newOrderFixture(t)
	baseTime := time.Date(2027, time.January, 1, 10, 0, 0, 0, time.UTC)
	fixture.service.now = func() time.Time { return baseTime }
	first := fixture.createOrder(t, "first", "sell", 10, 0.50, "gtc")
	fixture.service.now = func() time.Time { return baseTime.Add(time.Second) }
	second := fixture.createOrder(t, "second", "sell", 10, 0.50, "gtc")
	fixture.service.now = func() time.Time { return baseTime.Add(2 * time.Second) }
	fixture.createOrder(t, "taker", "buy", 15, 0.50, "gtc")

	first = fixture.getOrder(t, first.ID)
	second = fixture.getOrder(t, second.ID)
	if first.FilledAmount != 10 || second.FilledAmount != 5 {
		t.Errorf("time priority fills = (%v, %v), want (10, 5)", first.FilledAmount, second.FilledAmount)
	}
}

func TestMatchingAccumulatesOneThousandPartialFillsExactly(t *testing.T) {
	repository := newMemoryRepository()
	ctx := context.Background()
	createdAt := time.Date(2027, time.January, 1, 10, 0, 0, 0, time.UTC)
	for index := range 1_000 {
		maker := &types.Order{
			ID:          fmt.Sprintf("maker-%04d", index),
			MerchantID:  testMerchantID,
			UserID:      fmt.Sprintf("maker-%04d", index),
			MarketID:    testEventID,
			Type:        "sell",
			Option:      "Yes",
			Amount:      0.001,
			Currency:    "USD",
			Price:       0.50,
			TimeInForce: "gtc",
			Status:      "pending",
			CreatedAt:   createdAt.Add(time.Duration(index) * time.Nanosecond),
		}
		if _, err := repository.Place(ctx, maker); err != nil {
			t.Fatalf("Place(maker %d) error = %v", index, err)
		}
	}
	incoming := &types.Order{
		ID:          "taker",
		MerchantID:  testMerchantID,
		UserID:      "taker",
		MarketID:    testEventID,
		Type:        "buy",
		Option:      "Yes",
		Amount:      1,
		Currency:    "USD",
		Price:       0.50,
		TimeInForce: "gtc",
		Status:      "pending",
		CreatedAt:   createdAt.Add(time.Second),
	}
	if _, err := repository.Place(ctx, incoming); err != nil {
		t.Fatalf("Place(taker) error = %v", err)
	}
	if incoming.Status != "filled" || incoming.FilledAmount != incoming.Amount {
		t.Errorf("taker = %#v, want filled amount exactly equal to amount", incoming)
	}
}

func TestIOCAndCancellationUnlockRemainder(t *testing.T) {
	fixture := newOrderFixture(t)
	ctx := context.Background()
	ioc := fixture.createOrder(t, "ioc-user", "buy", 25, 0.40, "ioc")
	if ioc.Status != "cancelled" || ioc.FilledAmount != 0 {
		t.Fatalf("IOC order = %#v", ioc)
	}
	available, locked, err := fixture.wallets.GetBalance(ctx, testMerchantID, "ioc-user", "USD")
	if err != nil {
		t.Fatalf("GetBalance(IOC) error = %v", err)
	}
	if available != 100 || locked != 0 {
		t.Errorf("IOC balance = (%v, %v), want (100, 0)", available, locked)
	}

	orderValue := fixture.createOrder(t, "cancel-user", "buy", 30, 0.40, "gtc")
	if err := fixture.service.Cancel(ctx, orderValue.ID); err != nil {
		t.Fatalf("Cancel() error = %v", err)
	}
	available, locked, err = fixture.wallets.GetBalance(ctx, testMerchantID, "cancel-user", "USD")
	if err != nil {
		t.Fatalf("GetBalance(cancelled) error = %v", err)
	}
	if available != 100 || locked != 0 {
		t.Errorf("cancelled balance = (%v, %v), want (100, 0)", available, locked)
	}
	if err := fixture.service.Cancel(ctx, orderValue.ID); !errors.Is(err, ErrNotCancellable) {
		t.Errorf("Cancel(again) error = %v, want ErrNotCancellable", err)
	}
}

func TestBinaryOrderCollateral(t *testing.T) {
	fixture := newOrderFixture(t)
	ctx := context.Background()

	fixture.createOrder(t, "buyer", "buy", 10, 0.30, "gtc")
	available, locked, err := fixture.wallets.GetBalance(ctx, testMerchantID, "buyer", "USD")
	if err != nil {
		t.Fatalf("GetBalance(buyer) error = %v", err)
	}
	if available != 97 || locked != 3 {
		t.Fatalf("buyer balance = (%v, %v), want (97, 3)", available, locked)
	}

	fixture.createOrder(t, "seller", "sell", 10, 0.30, "gtc")
	available, locked, err = fixture.wallets.GetBalance(ctx, testMerchantID, "seller", "USD")
	if err != nil {
		t.Fatalf("GetBalance(seller) error = %v", err)
	}
	if available != 93 || locked != 7 {
		t.Fatalf("seller balance = (%v, %v), want (93, 7)", available, locked)
	}
}

func TestCreateWithIdempotencyKeyDoesNotDoubleLockCollateral(t *testing.T) {
	fixture := newOrderFixture(t)
	ctx := context.Background()
	if err := fixture.wallets.Credit(ctx, testMerchantID, "idempotent-user", "USD", 100, "credit"); err != nil {
		t.Fatalf("fund wallet: %v", err)
	}
	request := fixture.request("idempotent-user", "buy", 10, 0.50, "gtc")
	request.IdempotencyKey = "order-retry-key"
	first, err := fixture.service.Create(ctx, request)
	if err != nil {
		t.Fatalf("first Create() error = %v", err)
	}
	second, err := fixture.service.Create(ctx, request)
	if err != nil {
		t.Fatalf("second Create() error = %v", err)
	}
	if first.ID != second.ID {
		t.Errorf("idempotent order IDs = (%q, %q)", first.ID, second.ID)
	}
	available, locked, err := fixture.wallets.GetBalance(ctx, testMerchantID, "idempotent-user", "USD")
	if err != nil {
		t.Fatalf("GetBalance() error = %v", err)
	}
	if available != 95 || locked != 5 {
		t.Errorf("balance = (%v, %v), want (95, 5)", available, locked)
	}
	_, total, err := fixture.service.ListByUser(ctx, testMerchantID, "idempotent-user", 1, 10)
	if err != nil {
		t.Fatalf("ListByUser() error = %v", err)
	}
	if total != 1 {
		t.Errorf("order count = %d, want 1", total)
	}
}

func TestSelfOrdersDoNotMatch(t *testing.T) {
	fixture := newOrderFixture(t)
	sell := fixture.createOrder(t, "same-user", "sell", 10, 0.40, "gtc")
	buy := fixture.createOrder(t, "same-user", "buy", 10, 0.60, "gtc")
	if sell.Status != "pending" || buy.Status != "pending" {
		t.Errorf("self orders matched: sell=%#v buy=%#v", sell, buy)
	}
}

func TestOrderValidationAndDependencies(t *testing.T) {
	fixture := newOrderFixture(t)
	tests := []struct {
		name   string
		mutate func(*CreateRequest)
	}{
		{name: "merchant", mutate: func(req *CreateRequest) { req.MerchantID = "bad" }},
		{name: "market", mutate: func(req *CreateRequest) { req.MarketID = "bad" }},
		{name: "user", mutate: func(req *CreateRequest) { req.UserID = " " }},
		{name: "type", mutate: func(req *CreateRequest) { req.Type = "hold" }},
		{name: "option", mutate: func(req *CreateRequest) { req.Option = " " }},
		{name: "amount", mutate: func(req *CreateRequest) { req.Amount = 0 }},
		{name: "currency", mutate: func(req *CreateRequest) { req.Currency = "BTC" }},
		{name: "price", mutate: func(req *CreateRequest) { req.Price = 1 }},
		{name: "time in force", mutate: func(req *CreateRequest) { req.TimeInForce = "fok" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := fixture.request("validation-user", "buy", 10, 0.50, "gtc")
			test.mutate(request)
			_, err := fixture.service.Create(context.Background(), request)
			var validationErr *ValidationError
			if !errors.As(err, &validationErr) {
				t.Fatalf("Create() error = %v, want ValidationError", err)
			}
		})
	}

	invalidOption := fixture.request("validation-user", "buy", 10, 0.50, "gtc")
	invalidOption.Option = "Maybe"
	if _, err := fixture.service.Create(context.Background(), invalidOption); !errors.Is(err, ErrInvalidMarket) {
		t.Errorf("Create(invalid option) error = %v, want ErrInvalidMarket", err)
	}
	request := fixture.request("unfunded-user", "buy", 10, 0.50, "gtc")
	if _, err := fixture.service.Create(context.Background(), request); !errors.Is(err, wallet.ErrNotFound) {
		t.Errorf("Create(unfunded) error = %v, want wallet.ErrNotFound", err)
	}
}

func TestListFilters(t *testing.T) {
	fixture := newOrderFixture(t)
	first := fixture.createOrder(t, "list-user", "buy", 10, 0.40, "gtc")
	fixture.createOrder(t, "other-user", "buy", 10, 0.40, "gtc")
	values, total, err := fixture.service.ListByUser(
		context.Background(),
		testMerchantID,
		"list-user",
		1,
		10,
	)
	if err != nil {
		t.Fatalf("ListByUser() error = %v", err)
	}
	if total != 1 || len(values) != 1 || values[0].ID != first.ID {
		t.Errorf("ListByUser() values = %#v, total = %d", values, total)
	}
}

func TestListCursorAvoidsOffsetAndContinuesFromStableKey(t *testing.T) {
	fixture := newOrderFixture(t)
	first := fixture.createOrder(t, "cursor-user", "buy", 1, 0.40, "gtc")
	second := fixture.createOrder(t, "cursor-user", "buy", 1, 0.41, "gtc")
	third := fixture.createOrder(t, "cursor-user", "buy", 1, 0.42, "gtc")

	page, err := fixture.service.ListCursor(context.Background(), &ListFilters{
		MerchantID: testMerchantID,
		UserID:     "cursor-user",
		Limit:      2,
	})
	if err != nil {
		t.Fatalf("ListCursor(first page) error = %v", err)
	}
	if len(page.Orders) != 2 || page.NextCursor == "" {
		t.Fatalf("first cursor page = %#v", page)
	}
	secondPage, err := fixture.service.ListCursor(context.Background(), &ListFilters{
		MerchantID: testMerchantID,
		UserID:     "cursor-user",
		Limit:      2,
		Cursor:     page.NextCursor,
	})
	if err != nil {
		t.Fatalf("ListCursor(second page) error = %v", err)
	}
	if len(secondPage.Orders) != 1 || secondPage.NextCursor != "" {
		t.Fatalf("second cursor page = %#v", secondPage)
	}
	seen := map[string]bool{}
	for _, value := range append(page.Orders, secondPage.Orders...) {
		seen[value.ID] = true
	}
	if len(seen) != 3 || !seen[first.ID] || !seen[second.ID] || !seen[third.ID] {
		t.Errorf("cursor pages lost or duplicated orders: %#v", seen)
	}
}

type orderFixture struct {
	service  *implementation
	wallets  wallet.Service
	marketID string
}

func newOrderFixture(t *testing.T) *orderFixture {
	t.Helper()
	ctx := context.Background()
	marketService := market.NewService()
	marketValue, err := marketService.Create(ctx, &market.CreateRequest{
		MerchantID:    testMerchantID,
		EventID:       testEventID,
		Type:          "binary",
		Question:      "Will matching work?",
		Options:       []string{"Yes", "No"},
		LiquidityPool: 0,
	})
	if err != nil {
		t.Fatalf("create fixture market: %v", err)
	}
	walletService := wallet.NewService()
	return &orderFixture{
		service:  newService(newMemoryRepository(), marketService, walletService),
		wallets:  walletService,
		marketID: marketValue.ID,
	}
}

func (f *orderFixture) createOrder(
	t *testing.T,
	userID string,
	side string,
	amount float64,
	price float64,
	timeInForce string,
) *types.Order {
	t.Helper()
	ctx := context.Background()
	if _, _, err := f.wallets.GetBalance(ctx, testMerchantID, userID, "USD"); errors.Is(err, wallet.ErrNotFound) {
		if err := f.wallets.Credit(ctx, testMerchantID, userID, "USD", 100, "credit"); err != nil {
			t.Fatalf("fund wallet: %v", err)
		}
	} else if err != nil {
		t.Fatalf("get wallet balance: %v", err)
	}
	value, err := f.service.Create(ctx, f.request(userID, side, amount, price, timeInForce))
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	return value
}

func (f *orderFixture) request(
	userID string,
	side string,
	amount float64,
	price float64,
	timeInForce string,
) *CreateRequest {
	return &CreateRequest{
		MerchantID:  testMerchantID,
		UserID:      userID,
		MarketID:    f.marketID,
		Type:        side,
		Option:      "Yes",
		Amount:      amount,
		Currency:    "USD",
		Price:       price,
		TimeInForce: timeInForce,
	}
}

func (f *orderFixture) getOrder(t *testing.T, orderID string) *types.Order {
	t.Helper()
	value, err := f.service.Get(context.Background(), orderID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	return value
}

func TestListFiltersAcceptAllStoredStatuses(t *testing.T) {
	t.Parallel()

	// voided is written by market settlement; the list filter must accept
	// every status that can exist in storage.
	for _, status := range []string{"pending", "partial", "filled", "cancelled", "voided"} {
		normalized, err := normalizeFilters(&ListFilters{Status: status})
		if err != nil {
			t.Fatalf("normalizeFilters(status=%q) error = %v", status, err)
		}
		if normalized.Status != status {
			t.Errorf("normalizeFilters(status=%q) = %q", status, normalized.Status)
		}
	}
	if _, err := normalizeFilters(&ListFilters{Status: "unknown"}); err == nil {
		t.Fatal("normalizeFilters accepted an unknown status")
	}
}

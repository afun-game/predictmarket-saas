package marketmaker

import (
	"context"
	"testing"
	"time"

	"github.com/afun-game/predictmarket-saas/internal/event"
	"github.com/afun-game/predictmarket-saas/internal/market"
	"github.com/afun-game/predictmarket-saas/internal/merchant"
	"github.com/afun-game/predictmarket-saas/internal/order"
	"github.com/afun-game/predictmarket-saas/internal/wallet"
	"github.com/afun-game/predictmarket-saas/pkg/types"
)

type makerFixture struct {
	ctx        context.Context
	maker      Service
	orders     order.Service
	wallets    wallet.Service
	markets    market.Service
	events     event.Service
	merchants  merchant.Service
	funds      *MemoryRepository
	merchantID string
	eventID    string
	marketID   string
}

func newMakerFixture(t *testing.T) *makerFixture {
	t.Helper()
	ctx := context.Background()
	merchants := merchant.NewService()
	registered, err := merchants.Register(ctx, &merchant.RegisterRequest{
		Name: "MM 商户", Email: "mm@test.dev", Currency: "USD", Timezone: "UTC",
	})
	if err != nil {
		t.Fatal(err)
	}
	events := event.NewService()
	createdEvent, err := events.Create(ctx, &event.CreateRequest{
		SourceType: "custom", SourceID: "mm-event-1", Title: "MM 事件",
		Category: "sports", EndTime: "2027-01-01T00:00:00Z", ResolutionTime: "2027-01-02T00:00:00Z",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := events.UpdateStatus(ctx, createdEvent.ID, "active"); err != nil {
		t.Fatal(err)
	}
	markets := market.NewService()
	createdMarket, err := markets.Create(ctx, &market.CreateRequest{
		MerchantID: registered.ID, EventID: createdEvent.ID, Type: "binary",
		Question: "MM 是否会做市？", Options: []string{"Yes", "No"}, LiquidityPool: 500,
	})
	if err != nil {
		t.Fatal(err)
	}
	wallets := wallet.NewService()
	orders := order.NewServiceWithDependencies(markets, wallets)
	funds := NewMemoryRepository()
	maker := NewService(orders, wallets, merchants, markets, events, funds).(*implementation)
	maker.now = func() time.Time { return time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC) }
	return &makerFixture{
		ctx: ctx, maker: maker, orders: orders, wallets: wallets, markets: markets,
		events: events, merchants: merchants, funds: funds,
		merchantID: registered.ID, eventID: createdEvent.ID, marketID: createdMarket.ID,
	}
}

func (f *makerFixture) pendingOrders(t *testing.T) []*types.Order {
	t.Helper()
	items, _, err := f.orders.List(f.ctx, &order.ListFilters{MerchantID: f.merchantID, MarketID: f.marketID, Status: "pending"})
	if err != nil {
		t.Fatal(err)
	}
	return items
}

func TestTickFundsWalletAndPlacesTwoSidedQuotes(t *testing.T) {
	fixture := newMakerFixture(t)
	actions, err := fixture.maker.Tick(fixture.ctx)
	if err != nil {
		t.Fatalf("tick: %v", err)
	}
	if actions != 2 {
		t.Fatalf("tick actions = %d, want 2", actions)
	}
	committed, err := fixture.funds.GetCommitted(fixture.ctx, fixture.marketID)
	if err != nil {
		t.Fatalf("committed funds: %v", err)
	}
	if committed != 500 {
		t.Fatalf("committed = %v, want 500", committed)
	}
	available, locked, err := fixture.wallets.GetBalance(fixture.ctx, fixture.merchantID, MakerUserID, "USD")
	if err != nil {
		t.Fatal(err)
	}
	// The two quotes lock their side's collateral; the total must equal the
	// committed funds.
	if available+locked != 500 {
		t.Fatalf("maker wallet available+locked = %v+%v, want 500", available, locked)
	}
	orders := fixture.pendingOrders(t)
	if len(orders) != 2 {
		t.Fatalf("pending orders = %d, want 2", len(orders))
	}
	sides := map[string]float64{}
	for _, value := range orders {
		if value.UserID != MakerUserID {
			t.Fatalf("order user = %q, want %q", value.UserID, MakerUserID)
		}
		sides[value.Type] = value.Price
	}
	if sides["buy"] != 0.45 {
		t.Errorf("buy level = %v, want 0.45", sides["buy"])
	}
	if sides["sell"] != 0.55 {
		t.Errorf("sell level = %v, want 0.55", sides["sell"])
	}
}

func TestTickIsIdempotent(t *testing.T) {
	fixture := newMakerFixture(t)
	if _, err := fixture.maker.Tick(fixture.ctx); err != nil {
		t.Fatal(err)
	}
	if actions, err := fixture.maker.Tick(fixture.ctx); err != nil || actions != 0 {
		t.Fatalf("second tick actions = %d, err = %v; want 0, nil", actions, err)
	}
}

func TestTickReplacesFilledLevel(t *testing.T) {
	fixture := newMakerFixture(t)
	if _, err := fixture.maker.Tick(fixture.ctx); err != nil {
		t.Fatal(err)
	}
	for _, value := range fixture.pendingOrders(t) {
		if value.Type == "sell" {
			if err := fixture.orders.Cancel(fixture.ctx, value.ID); err != nil {
				t.Fatalf("cancel ask: %v", err)
			}
		}
	}
	if actions, err := fixture.maker.Tick(fixture.ctx); err != nil || actions != 1 {
		t.Fatalf("replacement tick actions = %d, err = %v; want 1, nil", actions, err)
	}
	sells := 0
	for _, value := range fixture.pendingOrders(t) {
		if value.Type == "sell" {
			sells++
		}
	}
	if sells != 1 {
		t.Fatalf("sell orders after replacement = %d, want 1", sells)
	}
}

func TestTickSkipsNearResolution(t *testing.T) {
	fixture := newMakerFixture(t)
	// Force the event's resolution into the stop window.
	eventValue, err := fixture.events.Get(fixture.ctx, fixture.eventID)
	if err != nil {
		t.Fatal(err)
	}
	fixture.maker.(*implementation).now = func() time.Time {
		return eventValue.ResolutionTime.Add(-time.Minute)
	}
	actions, err := fixture.maker.Tick(fixture.ctx)
	if err != nil {
		t.Fatal(err)
	}
	if actions != 0 {
		t.Fatalf("tick near resolution actions = %d, want 0", actions)
	}
	if orders := fixture.pendingOrders(t); len(orders) != 0 {
		t.Fatalf("orders placed near resolution: %d", len(orders))
	}
}

func TestTickSkipsParimutuelMarkets(t *testing.T) {
	fixture := newMakerFixture(t)
	created, err := fixture.markets.Create(fixture.ctx, &market.CreateRequest{
		MerchantID: fixture.merchantID, EventID: fixture.eventID, Type: "parimutuel",
		Question: "奖池市场", Options: []string{"Yes", "No"}, LiquidityPool: 500,
	})
	if err != nil {
		t.Fatal(err)
	}
	actions, err := fixture.maker.Tick(fixture.ctx)
	if err != nil {
		t.Fatal(err)
	}
	if actions != 2 {
		t.Fatalf("tick actions = %d, want 2 (only the binary market quoted)", actions)
	}
	items, _, err := fixture.orders.List(fixture.ctx, &order.ListFilters{MerchantID: fixture.merchantID, MarketID: created.ID})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 0 {
		t.Fatalf("parimutuel market got %d orders, want 0", len(items))
	}
}

func abs(value float64) float64 {
	if value < 0 {
		return -value
	}
	return value
}

func TestYesEquivalentLevelsFoldsNoSide(t *testing.T) {
	book := &market.OrderBook{
		Bids: []market.OrderBookEntry{{Option: "Yes", Price: 0.40}, {Option: "No", Price: 0.45}},
		Asks: []market.OrderBookEntry{{Option: "Yes", Price: 0.70}, {Option: "No", Price: 0.55}},
	}
	bestBid, bestAsk, hasBid, hasAsk := yesEquivalentLevels(book)
	// NO ask 0.55 = YES bid 0.45; NO bid 0.45 = YES ask 0.55.
	const eps = 1e-9
	if !hasBid || abs(bestBid-0.45) > eps {
		t.Errorf("bestBid = %v (has=%v), want 0.45 (true)", bestBid, hasBid)
	}
	if !hasAsk || abs(bestAsk-0.55) > eps {
		t.Errorf("bestAsk = %v (has=%v), want 0.55 (true)", bestAsk, hasAsk)
	}
}

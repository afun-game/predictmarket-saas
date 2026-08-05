package parimutuel

import (
	"context"
	"errors"
	"testing"
)

func newFixture(t *testing.T) (Service, *MemoryRepository) {
	t.Helper()
	repo := NewMemoryRepository()
	repo.SeedMarket("m-1", "parimutuel", "active", "active", []string{"Yes", "No"})
	repo.SeedMarket("m-binary", "binary", "active", "active", []string{"Yes", "No"})
	service := NewServiceWithRepository(repo)
	if err := service.CreatePools(context.Background(), "m-1", "USD"); err != nil {
		t.Fatal(err)
	}
	return service, repo
}

func TestPlaceBetJoinsPool(t *testing.T) {
	service, _ := newFixture(t)
	bet, err := service.PlaceBet(context.Background(), Bet{
		MarketID: "m-1", MerchantID: "merchant-1", UserID: "user-1",
		Option: "Yes", Stake: 100, Currency: "USD",
	})
	if err != nil {
		t.Fatalf("place bet: %v", err)
	}
	if bet.ID == "" || bet.Status != StatusActive {
		t.Fatalf("bet = %+v", bet)
	}
	pools, err := service.GetPools(context.Background(), "m-1")
	if err != nil || len(pools) != 1 {
		t.Fatalf("pools = %v, err = %v", pools, err)
	}
	if pools[0].TotalStake != 100 {
		t.Fatalf("pool total = %v, want 100", pools[0].TotalStake)
	}
}

func TestPlaceBetRejectsInvalidMarkets(t *testing.T) {
	service, _ := newFixture(t)
	cases := []struct {
		name    string
		market  string
		option  string
		wantErr error
	}{
		{"binary market", "m-binary", "Yes", ErrNotParimutuel},
		{"unknown market", "m-missing", "Yes", ErrNotFound},
		{"invalid option", "m-1", "Maybe", ErrInvalidOption},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := service.PlaceBet(context.Background(), Bet{
				MarketID: tc.market, MerchantID: "merchant-1", UserID: "user-1",
				Option: tc.option, Stake: 10, Currency: "USD",
			})
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("err = %v, want %v", err, tc.wantErr)
			}
		})
	}
}

func TestPlaceBetRejectsInactiveAndResolved(t *testing.T) {
	repo := NewMemoryRepository()
	repo.SeedMarket("m-inactive", "parimutuel", "suspended", "active", []string{"Yes", "No"})
	repo.SeedMarket("m-resolved", "parimutuel", "active", "resolved", []string{"Yes", "No"})
	service := NewServiceWithRepository(repo)
	for _, marketID := range []string{"m-inactive", "m-resolved"} {
		_, err := service.PlaceBet(context.Background(), Bet{
			MarketID: marketID, MerchantID: "merchant-1", UserID: "user-1",
			Option: "Yes", Stake: 10, Currency: "USD",
		})
		if err == nil {
			t.Fatalf("market %s accepted a bet", marketID)
		}
	}
}

func TestPlaceBetRejectsCurrencyMismatchAndUninitializedPool(t *testing.T) {
	service, _ := newFixture(t)
	_, err := service.PlaceBet(context.Background(), Bet{
		MarketID: "m-1", MerchantID: "merchant-1", UserID: "user-1",
		Option: "Yes", Stake: 10, Currency: "EUR",
	})
	if err == nil {
		t.Fatal("currency mismatch accepted")
	}
	repo := NewMemoryRepository()
	repo.SeedMarket("m-nopool", "parimutuel", "active", "active", []string{"Yes", "No"})
	uninit := NewServiceWithRepository(repo)
	_, err = uninit.PlaceBet(context.Background(), Bet{
		MarketID: "m-nopool", MerchantID: "merchant-1", UserID: "user-1",
		Option: "Yes", Stake: 10, Currency: "USD",
	})
	if !errors.Is(err, ErrPoolNotInitialized) {
		t.Fatalf("err = %v, want pool not initialized", err)
	}
}

func TestListBetsIsMerchantScoped(t *testing.T) {
	service, _ := newFixture(t)
	ctx := context.Background()
	for _, bet := range []Bet{
		{MarketID: "m-1", MerchantID: "merchant-1", UserID: "user-1", Option: "Yes", Stake: 10, Currency: "USD"},
		{MarketID: "m-1", MerchantID: "merchant-2", UserID: "user-2", Option: "No", Stake: 20, Currency: "USD"},
	} {
		if _, err := service.PlaceBet(ctx, bet); err != nil {
			t.Fatal(err)
		}
	}
	items, total, err := service.ListBets(ctx, ListFilters{MerchantID: "merchant-1"})
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 || len(items) != 1 || items[0].UserID != "user-1" {
		t.Fatalf("merchant-1 bets = %+v (total %d)", items, total)
	}
	items, total, err = service.ListBets(ctx, ListFilters{MerchantID: "merchant-1", UserID: "user-2"})
	if err != nil {
		t.Fatal(err)
	}
	if total != 0 {
		t.Fatalf("cross-tenant bets leaked: %+v", items)
	}
}

func TestOptionStakesAggregatesActiveBets(t *testing.T) {
	service, _ := newFixture(t)
	for _, bet := range []Bet{
		{MarketID: "m-1", MerchantID: "merchant-1", UserID: "u1", Option: "Yes", Stake: 10, Currency: "USD"},
		{MarketID: "m-1", MerchantID: "merchant-1", UserID: "u2", Option: "Yes", Stake: 5, Currency: "USD"},
		{MarketID: "m-1", MerchantID: "merchant-1", UserID: "u3", Option: "No", Stake: 20, Currency: "USD"},
	} {
		if _, err := service.PlaceBet(context.Background(), bet); err != nil {
			t.Fatalf("place bet: %v", err)
		}
	}
	stakes, err := service.OptionStakes(context.Background(), "m-1")
	if err != nil {
		t.Fatalf("OptionStakes() error = %v", err)
	}
	totals := map[string]float64{}
	for _, item := range stakes {
		totals[item.Option] = item.Stake
	}
	if totals["Yes"] != 15 || totals["No"] != 20 {
		t.Errorf("option stakes = %v, want Yes=15 No=20", totals)
	}

	empty, err := service.OptionStakes(context.Background(), "m-binary")
	if err != nil {
		t.Fatalf("OptionStakes(binary) error = %v", err)
	}
	if len(empty) != 0 {
		t.Errorf("binary market stakes = %v, want empty", empty)
	}
}

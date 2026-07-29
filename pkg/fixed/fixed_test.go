package fixed

import "testing"

func TestFixedPointConversions(t *testing.T) {
	shares, err := SharesFromFloat(12.345678)
	if err != nil {
		t.Fatalf("SharesFromFloat() error = %v", err)
	}
	if got := FormatShares(shares); got != "12.345678" {
		t.Errorf("FormatShares() = %q, want 12.345678", got)
	}
	if _, err := SharesFromFloat(0.0000001); err == nil {
		t.Fatal("SharesFromFloat() accepted more than six decimals")
	}
	if _, err := CentsFromFloat(90_071_992_547_410); err == nil {
		t.Fatal("CentsFromFloat() accepted an out-of-range amount")
	}
}

func TestBinaryCollateralConservesPayout(t *testing.T) {
	shares, err := SharesFromFloat(10)
	if err != nil {
		t.Fatalf("SharesFromFloat() error = %v", err)
	}
	price, err := PriceFromFloat(0.333333)
	if err != nil {
		t.Fatalf("PriceFromFloat() error = %v", err)
	}
	buy, err := CollateralCents("buy", shares, price)
	if err != nil {
		t.Fatalf("buyer collateral error = %v", err)
	}
	sell, err := CollateralCents("sell", shares, price)
	if err != nil {
		t.Fatalf("seller collateral error = %v", err)
	}
	payout, err := PayoutCents(shares)
	if err != nil {
		t.Fatalf("PayoutCents() error = %v", err)
	}
	if buy+sell != payout {
		t.Errorf("binary collateral = %d + %d, payout = %d", buy, sell, payout)
	}
}

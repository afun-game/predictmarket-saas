//go:build v2regression

package order

import (
	"context"
	"testing"
)

// TestShareModelPriceImprovementRefund is intentionally red until Sprint 1.
// A buy order for ten shares limited at 0.35 that fills at 0.30 must retain
// only 3.00 of collateral and return the 0.50 price improvement.
func TestShareModelPriceImprovementRefund(t *testing.T) {
	fixture := newOrderFixture(t)
	fixture.createOrder(t, "maker", "sell", 10, 0.30, "gtc")
	fixture.createOrder(t, "buyer", "buy", 10, 0.35, "gtc")

	available, locked, err := fixture.wallets.GetBalance(
		context.Background(),
		testMerchantID,
		"buyer",
		"USD",
	)
	if err != nil {
		t.Fatalf("GetBalance() error = %v", err)
	}
	if available != 97 || locked != 3 {
		t.Fatalf(
			"buyer balance = (%v, %v), want (97, 3) after price improvement",
			available,
			locked,
		)
	}
}

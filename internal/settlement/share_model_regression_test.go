//go:build v2regression

package settlement

import "testing"

// TestShareModelWinningSharesRedeemAtPar is intentionally red until Sprint 1.
// A winning share must redeem at one currency unit, independently of its price.
func TestShareModelWinningSharesRedeemAtPar(t *testing.T) {
	orders := []*settlementOrder{
		newTestOrder("buyer", "buy", "Yes", "filled", 10_000, 10_000, "USD"),
		newTestOrder("seller", "sell", "Yes", "filled", 10_000, 10_000, "USD"),
	}

	calculatePayouts(orders, "Yes")

	assertBigInt(t, "winning-share payout", orders[0].payout, 10_000)
	assertBigInt(t, "losing-share payout", orders[1].payout, 0)
}

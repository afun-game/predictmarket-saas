package order

import "github.com/afun-game/predictmarket-saas/pkg/fixed"

// requiredCollateral returns the currency collateral for an order's shares.
// In the binary model, a sell order represents the complementary outcome.
func requiredCollateral(side string, shares, price float64) float64 {
	return fixed.CentsToFloat(requiredCollateralCents(side, shares, price))
}

// priceImprovementRefund returns the taker's released collateral when it fills
// at the maker's better price.
func priceImprovementRefund(side string, shares, limitPrice, matchedPrice float64) float64 {
	return fixed.CentsToFloat(priceImprovementRefundCents(side, shares, limitPrice, matchedPrice))
}

func requiredCollateralCents(side string, shares, price float64) int64 {
	shareUnits := storedShareUnits(shares)
	priceUnits := storedPriceUnits(price)
	cents, err := fixed.CollateralCents(side, shareUnits, priceUnits)
	if err != nil {
		panic(err)
	}
	return cents
}

func priceImprovementRefundCents(side string, shares, limitPrice, matchedPrice float64) int64 {
	shareUnits := storedShareUnits(shares)
	limitUnits := storedPriceUnits(limitPrice)
	matchedUnits := storedPriceUnits(matchedPrice)
	cents, err := fixed.PriceImprovementCents(side, shareUnits, limitUnits, matchedUnits)
	if err != nil {
		panic(err)
	}
	return cents
}

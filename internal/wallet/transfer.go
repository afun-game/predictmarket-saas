package wallet

import "github.com/afun-game/predictmarket-saas/pkg/fixed"

func cloneTransfer(value *Transfer) *Transfer {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

func sameTransfer(left, right *Transfer) bool {
	leftAmount, leftErr := fixed.CentsFromFloat(left.Amount)
	rightAmount, rightErr := fixed.CentsFromFloat(right.Amount)
	return leftErr == nil && rightErr == nil &&
		left.MerchantID == right.MerchantID &&
		left.UserID == right.UserID &&
		left.Currency == right.Currency &&
		leftAmount == rightAmount &&
		left.Direction == right.Direction
}

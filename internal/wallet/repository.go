package wallet

import (
	"context"
	"time"

	"github.com/afun-game/predictmarket-saas/pkg/types"
)

type walletKey struct {
	MerchantID string
	UserID     string
	Currency   string
}

// Repository stores wallets and transaction history atomically.
type Repository interface {
	ValidateMerchant(ctx context.Context, merchantID string) error
	Create(ctx context.Context, wallet *types.Wallet) error
	Get(ctx context.Context, key walletKey) (*types.Wallet, error)
	Credit(ctx context.Context, wallet *types.Wallet, transaction *types.Transaction) error
	Debit(
		ctx context.Context,
		key walletKey,
		amount float64,
		transaction *types.Transaction,
		updatedAt time.Time,
	) error
	Lock(ctx context.Context, key walletKey, amount float64, updatedAt time.Time) error
	Unlock(ctx context.Context, key walletKey, amount float64, updatedAt time.Time) error
	ListTransactions(
		ctx context.Context,
		merchantID string,
		userID string,
		offset int,
		limit int,
	) ([]*types.Transaction, int, error)
}

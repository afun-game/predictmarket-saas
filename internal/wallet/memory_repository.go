package wallet

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/afun-game/predictmarket-saas/pkg/fixed"
	"github.com/afun-game/predictmarket-saas/pkg/types"
)

type memoryRepository struct {
	mu                       sync.RWMutex
	byID                     map[string]*types.Wallet
	idByKey                  map[walletKey]string
	transactions             map[string]*types.Transaction
	transactionByIdempotency map[transactionIdempotencyKey]string
}

type transactionIdempotencyKey struct {
	walletID string
	key      string
}

func newMemoryRepository() *memoryRepository {
	return &memoryRepository{
		byID:                     map[string]*types.Wallet{},
		idByKey:                  map[walletKey]string{},
		transactions:             map[string]*types.Transaction{},
		transactionByIdempotency: map[transactionIdempotencyKey]string{},
	}
}

func (r *memoryRepository) ValidateMerchant(ctx context.Context, _ string) error {
	return ctx.Err()
}

func (r *memoryRepository) Create(ctx context.Context, value *types.Wallet) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	key := keyFromWallet(value)
	if _, exists := r.idByKey[key]; exists {
		return ErrAlreadyExists
	}
	if _, exists := r.byID[value.ID]; exists {
		return fmt.Errorf("wallet ID already exists: %s", value.ID)
	}
	r.byID[value.ID] = cloneWallet(value)
	r.idByKey[key] = value.ID
	return nil
}

func (r *memoryRepository) Get(ctx context.Context, key walletKey) (*types.Wallet, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	r.mu.RLock()
	defer r.mu.RUnlock()

	walletID, exists := r.idByKey[key]
	if !exists {
		return nil, ErrNotFound
	}
	return cloneWallet(r.byID[walletID]), nil
}

func (r *memoryRepository) Credit(
	ctx context.Context,
	wallet *types.Wallet,
	transaction *types.Transaction,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	key := keyFromWallet(wallet)
	walletID, exists := r.idByKey[key]
	if !exists {
		walletID = wallet.ID
		r.byID[walletID] = cloneWallet(wallet)
		r.idByKey[key] = walletID
	}
	if _, exists := r.transactions[transaction.ID]; exists {
		return fmt.Errorf("transaction ID already exists: %s", transaction.ID)
	}
	if transaction.IdempotencyKey != "" {
		idempotencyKey := transactionIdempotencyKey{walletID: walletID, key: transaction.IdempotencyKey}
		if _, exists := r.transactionByIdempotency[idempotencyKey]; exists {
			return ErrAlreadyExists
		}
		r.transactionByIdempotency[idempotencyKey] = transaction.ID
	}
	stored := r.byID[walletID]
	stored.Balance = addMoney(stored.Balance, transaction.Amount)
	stored.UpdatedAt = transaction.CreatedAt
	transaction.WalletID = walletID
	r.transactions[transaction.ID] = cloneTransaction(transaction)
	return nil
}

func (r *memoryRepository) Debit(
	ctx context.Context,
	key walletKey,
	amount float64,
	transaction *types.Transaction,
	updatedAt time.Time,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	walletID, exists := r.idByKey[key]
	if !exists {
		return ErrNotFound
	}
	stored := r.byID[walletID]
	if moneyLessThan(stored.Balance, amount) {
		return ErrInsufficientBalance
	}
	if _, exists := r.transactions[transaction.ID]; exists {
		return fmt.Errorf("transaction ID already exists: %s", transaction.ID)
	}
	stored.Balance = subtractMoney(stored.Balance, amount)
	stored.UpdatedAt = updatedAt
	transaction.WalletID = walletID
	r.transactions[transaction.ID] = cloneTransaction(transaction)
	return nil
}

func (r *memoryRepository) Lock(
	ctx context.Context,
	key walletKey,
	amount float64,
	updatedAt time.Time,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	stored, err := r.walletForUpdate(key)
	if err != nil {
		return err
	}
	if moneyLessThan(stored.Balance, amount) {
		return ErrInsufficientBalance
	}
	stored.Balance = subtractMoney(stored.Balance, amount)
	stored.LockedBalance = addMoney(stored.LockedBalance, amount)
	stored.UpdatedAt = updatedAt
	return nil
}

func (r *memoryRepository) Unlock(
	ctx context.Context,
	key walletKey,
	amount float64,
	updatedAt time.Time,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	stored, err := r.walletForUpdate(key)
	if err != nil {
		return err
	}
	if moneyLessThan(stored.LockedBalance, amount) {
		return ErrInsufficientLocked
	}
	stored.LockedBalance = subtractMoney(stored.LockedBalance, amount)
	stored.Balance = addMoney(stored.Balance, amount)
	stored.UpdatedAt = updatedAt
	return nil
}

func (r *memoryRepository) ListTransactions(
	ctx context.Context,
	merchantID string,
	userID string,
	offset int,
	limit int,
) ([]*types.Transaction, int, error) {
	if err := ctx.Err(); err != nil {
		return nil, 0, err
	}
	r.mu.RLock()
	defer r.mu.RUnlock()

	values := make([]*types.Transaction, 0, len(r.transactions))
	for _, transaction := range r.transactions {
		wallet := r.byID[transaction.WalletID]
		if wallet.MerchantID == merchantID && wallet.UserID == userID {
			values = append(values, cloneTransaction(transaction))
		}
	}
	sort.Slice(values, func(i, j int) bool {
		if values[i].CreatedAt.Equal(values[j].CreatedAt) {
			return values[i].ID < values[j].ID
		}
		return values[i].CreatedAt.After(values[j].CreatedAt)
	})

	total := len(values)
	if offset >= total {
		return []*types.Transaction{}, total, nil
	}
	end := min(offset+limit, total)
	return values[offset:end], total, nil
}

func (r *memoryRepository) walletForUpdate(key walletKey) (*types.Wallet, error) {
	walletID, exists := r.idByKey[key]
	if !exists {
		return nil, ErrNotFound
	}
	return r.byID[walletID], nil
}

func keyFromWallet(value *types.Wallet) walletKey {
	return walletKey{
		MerchantID: value.MerchantID,
		UserID:     value.UserID,
		Currency:   value.Currency,
	}
}

func cloneWallet(value *types.Wallet) *types.Wallet {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

func cloneTransaction(value *types.Transaction) *types.Transaction {
	if value == nil {
		return nil
	}
	clone := *value
	if value.RelatedOrderID != nil {
		orderID := *value.RelatedOrderID
		clone.RelatedOrderID = &orderID
	}
	return &clone
}

func addMoney(current, amount float64) float64 {
	return fixed.CentsToFloat(storedCents(current) + storedCents(amount))
}

func subtractMoney(current, amount float64) float64 {
	return fixed.CentsToFloat(storedCents(current) - storedCents(amount))
}

func moneyLessThan(current, amount float64) bool {
	return storedCents(current) < storedCents(amount)
}

func storedCents(value float64) int64 {
	if value == 0 {
		return 0
	}
	cents, err := fixed.CentsFromFloat(value)
	if err != nil {
		panic(fmt.Sprintf("stored wallet amount is invalid: %v", err))
	}
	return cents
}

package wallet

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/afun-game/predictmarket-saas/pkg/types"
)

type postgresRepository struct {
	database *sql.DB
}

func newPostgresRepository(database *sql.DB) *postgresRepository {
	return &postgresRepository{database: database}
}

func (r *postgresRepository) ValidateMerchant(ctx context.Context, merchantID string) error {
	const query = `SELECT EXISTS (
    SELECT 1 FROM merchants WHERE id = $1 AND status = 'active'
)`
	var active bool
	if err := r.database.QueryRowContext(ctx, query, merchantID).Scan(&active); err != nil {
		return fmt.Errorf("query wallet merchant: %w", err)
	}
	if !active {
		return ErrInvalidMerchant
	}
	return nil
}

func (r *postgresRepository) Create(ctx context.Context, value *types.Wallet) error {
	const query = `
INSERT INTO wallets (
    id, merchant_id, user_id, currency, balance, locked_balance, updated_at
) VALUES ($1, $2, $3, $4, $5, $6, $7)`
	_, err := r.database.ExecContext(
		ctx,
		query,
		value.ID,
		value.MerchantID,
		value.UserID,
		value.Currency,
		value.Balance,
		value.LockedBalance,
		value.UpdatedAt,
	)
	if postgresErrorCode(err) == "23505" {
		return ErrAlreadyExists
	}
	if postgresErrorCode(err) == "23503" {
		return ErrInvalidMerchant
	}
	if err != nil {
		return fmt.Errorf("insert wallet: %w", err)
	}
	return nil
}

func (r *postgresRepository) Get(ctx context.Context, key walletKey) (*types.Wallet, error) {
	const query = `
SELECT id, merchant_id, user_id, currency, balance, locked_balance, updated_at
FROM wallets
WHERE merchant_id = $1 AND user_id = $2 AND currency = $3`
	return scanWallet(r.database.QueryRowContext(
		ctx,
		query,
		key.MerchantID,
		key.UserID,
		key.Currency,
	))
}

func (r *postgresRepository) Credit(
	ctx context.Context,
	wallet *types.Wallet,
	transaction *types.Transaction,
) error {
	databaseTx, err := r.database.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin credit transaction: %w", err)
	}
	defer func() {
		_ = databaseTx.Rollback() // No-op after commit; best-effort rollback on earlier returns.
	}()

	const createQuery = `
INSERT INTO wallets (
    id, merchant_id, user_id, currency, balance, locked_balance, updated_at
) VALUES ($1, $2, $3, $4, 0, 0, $5)
ON CONFLICT (merchant_id, user_id, currency) DO NOTHING`
	_, err = databaseTx.ExecContext(
		ctx,
		createQuery,
		wallet.ID,
		wallet.MerchantID,
		wallet.UserID,
		wallet.Currency,
		wallet.UpdatedAt,
	)
	if postgresErrorCode(err) == "23503" {
		return ErrInvalidMerchant
	}
	if err != nil {
		return fmt.Errorf("ensure credited wallet: %w", err)
	}

	const creditQuery = `
UPDATE wallets
SET balance = balance + $4, updated_at = $5
WHERE merchant_id = $1 AND user_id = $2 AND currency = $3
RETURNING id`
	if err := databaseTx.QueryRowContext(
		ctx,
		creditQuery,
		wallet.MerchantID,
		wallet.UserID,
		wallet.Currency,
		transaction.Amount,
		wallet.UpdatedAt,
	).Scan(&transaction.WalletID); err != nil {
		return fmt.Errorf("update credited wallet: %w", err)
	}
	if err := insertTransaction(ctx, databaseTx, transaction); err != nil {
		return err
	}
	if err := databaseTx.Commit(); err != nil {
		return fmt.Errorf("commit credit transaction: %w", err)
	}
	return nil
}

func (r *postgresRepository) Debit(
	ctx context.Context,
	key walletKey,
	amount float64,
	transaction *types.Transaction,
	updatedAt time.Time,
) error {
	databaseTx, err := r.database.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin debit transaction: %w", err)
	}
	defer func() {
		_ = databaseTx.Rollback() // No-op after commit; best-effort rollback on earlier returns.
	}()

	const query = `
UPDATE wallets
SET balance = balance - $4, updated_at = $5
WHERE merchant_id = $1 AND user_id = $2 AND currency = $3 AND balance >= $4
RETURNING id`
	err = databaseTx.QueryRowContext(
		ctx,
		query,
		key.MerchantID,
		key.UserID,
		key.Currency,
		amount,
		updatedAt,
	).Scan(&transaction.WalletID)
	if errors.Is(err, sql.ErrNoRows) {
		return classifyBalanceFailure(ctx, databaseTx, key, false)
	}
	if err != nil {
		return fmt.Errorf("update debited wallet: %w", err)
	}
	if err := insertTransaction(ctx, databaseTx, transaction); err != nil {
		return err
	}
	if err := databaseTx.Commit(); err != nil {
		return fmt.Errorf("commit debit transaction: %w", err)
	}
	return nil
}

func (r *postgresRepository) Lock(
	ctx context.Context,
	key walletKey,
	amount float64,
	updatedAt time.Time,
) error {
	const query = `
UPDATE wallets
SET balance = balance - $4,
    locked_balance = locked_balance + $4,
    updated_at = $5
WHERE merchant_id = $1 AND user_id = $2 AND currency = $3 AND balance >= $4`
	result, err := r.database.ExecContext(
		ctx,
		query,
		key.MerchantID,
		key.UserID,
		key.Currency,
		amount,
		updatedAt,
	)
	if err != nil {
		return fmt.Errorf("update locked wallet: %w", err)
	}
	return requireWalletUpdate(ctx, r.database, result, key, false)
}

func (r *postgresRepository) Unlock(
	ctx context.Context,
	key walletKey,
	amount float64,
	updatedAt time.Time,
) error {
	const query = `
UPDATE wallets
SET balance = balance + $4,
    locked_balance = locked_balance - $4,
    updated_at = $5
WHERE merchant_id = $1 AND user_id = $2 AND currency = $3 AND locked_balance >= $4`
	result, err := r.database.ExecContext(
		ctx,
		query,
		key.MerchantID,
		key.UserID,
		key.Currency,
		amount,
		updatedAt,
	)
	if err != nil {
		return fmt.Errorf("update unlocked wallet: %w", err)
	}
	return requireWalletUpdate(ctx, r.database, result, key, true)
}

func (r *postgresRepository) ListTransactions(
	ctx context.Context,
	merchantID string,
	userID string,
	offset int,
	limit int,
) ([]*types.Transaction, int, error) {
	const countQuery = `
SELECT COUNT(*)
FROM transactions AS t
JOIN wallets AS w ON w.id = t.wallet_id
WHERE w.merchant_id = $1 AND w.user_id = $2`
	var total int
	if err := r.database.QueryRowContext(ctx, countQuery, merchantID, userID).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count wallet transactions: %w", err)
	}

	const query = `
SELECT t.id, t.wallet_id, t.type, t.amount, t.currency,
       t.related_order_id, t.status, t.created_at
FROM transactions AS t
JOIN wallets AS w ON w.id = t.wallet_id
WHERE w.merchant_id = $1 AND w.user_id = $2
ORDER BY t.created_at DESC, t.id
LIMIT $3 OFFSET $4`
	rows, err := r.database.QueryContext(ctx, query, merchantID, userID, limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("query wallet transactions: %w", err)
	}
	defer func() {
		_ = rows.Close() // Best-effort cleanup on early returns; checked below on the happy path.
	}()

	values := make([]*types.Transaction, 0, limit)
	for rows.Next() {
		value, err := scanTransaction(rows)
		if err != nil {
			return nil, 0, err
		}
		values = append(values, value)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("iterate wallet transactions: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, 0, fmt.Errorf("close wallet transaction rows: %w", err)
	}
	return values, total, nil
}

type rowScanner interface {
	Scan(dest ...any) error
}

type queryRower interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

func scanWallet(row rowScanner) (*types.Wallet, error) {
	value := &types.Wallet{}
	err := row.Scan(
		&value.ID,
		&value.MerchantID,
		&value.UserID,
		&value.Currency,
		&value.Balance,
		&value.LockedBalance,
		&value.UpdatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("scan wallet: %w", err)
	}
	return value, nil
}

func scanTransaction(row rowScanner) (*types.Transaction, error) {
	value := &types.Transaction{}
	if err := row.Scan(
		&value.ID,
		&value.WalletID,
		&value.Type,
		&value.Amount,
		&value.Currency,
		&value.RelatedOrderID,
		&value.Status,
		&value.CreatedAt,
	); err != nil {
		return nil, fmt.Errorf("scan wallet transaction: %w", err)
	}
	return value, nil
}

func insertTransaction(
	ctx context.Context,
	databaseTx *sql.Tx,
	transaction *types.Transaction,
) error {
	const query = `
INSERT INTO transactions (
    id, wallet_id, type, amount, currency, related_order_id, idempotency_key, status, created_at
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`
	_, err := databaseTx.ExecContext(
		ctx,
		query,
		transaction.ID,
		transaction.WalletID,
		transaction.Type,
		transaction.Amount,
		transaction.Currency,
		transaction.RelatedOrderID,
		nullableIdempotencyKey(transaction.IdempotencyKey),
		transaction.Status,
		transaction.CreatedAt,
	)
	if postgresErrorCode(err) == "23505" {
		return ErrAlreadyExists
	}
	if err != nil {
		return fmt.Errorf("insert wallet transaction: %w", err)
	}
	return nil
}

func nullableIdempotencyKey(key string) any {
	if key == "" {
		return nil
	}
	return key
}

func requireWalletUpdate(
	ctx context.Context,
	querier queryRower,
	result sql.Result,
	key walletKey,
	locked bool,
) error {
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("count updated wallet rows: %w", err)
	}
	if rowsAffected > 0 {
		return nil
	}
	return classifyBalanceFailure(ctx, querier, key, locked)
}

func classifyBalanceFailure(
	ctx context.Context,
	querier queryRower,
	key walletKey,
	locked bool,
) error {
	const query = `
SELECT balance, locked_balance
FROM wallets
WHERE merchant_id = $1 AND user_id = $2 AND currency = $3`
	var balance float64
	var lockedBalance float64
	err := querier.QueryRowContext(
		ctx,
		query,
		key.MerchantID,
		key.UserID,
		key.Currency,
	).Scan(&balance, &lockedBalance)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("classify wallet balance update: %w", err)
	}
	if locked {
		return ErrInsufficientLocked
	}
	return ErrInsufficientBalance
}

func postgresErrorCode(err error) string {
	var postgresErr *pgconn.PgError
	if errors.As(err, &postgresErr) {
		return postgresErr.Code
	}
	return ""
}

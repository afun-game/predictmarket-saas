package wallet

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/afun-game/predictmarket-saas/pkg/fixed"
	"github.com/afun-game/predictmarket-saas/pkg/types"
	"github.com/jackc/pgx/v5/pgconn"
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
    id, merchant_id, user_id, currency, kind, balance, locked_balance, updated_at
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`
	_, err := r.database.ExecContext(
		ctx,
		query,
		value.ID,
		value.MerchantID,
		value.UserID,
		value.Currency,
		value.Kind,
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
SELECT id, merchant_id, user_id, currency, kind, balance, locked_balance, updated_at
FROM wallets
WHERE merchant_id = $1 AND user_id = $2 AND currency = $3 AND kind = $4`
	return scanWallet(r.database.QueryRowContext(
		ctx,
		query,
		key.MerchantID,
		key.UserID,
		key.Currency,
		key.Kind,
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
    id, merchant_id, user_id, currency, kind, balance, locked_balance, updated_at
) VALUES ($1, $2, $3, $4, $5, 0, 0, $6)
ON CONFLICT (merchant_id, user_id, currency, kind) DO NOTHING`
	_, err = databaseTx.ExecContext(
		ctx,
		createQuery,
		wallet.ID,
		wallet.MerchantID,
		wallet.UserID,
		wallet.Currency,
		wallet.Kind,
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
SET balance = balance + $5::numeric, updated_at = $6
WHERE merchant_id = $1 AND user_id = $2 AND currency = $3 AND kind = $4
RETURNING id`
	if err := databaseTx.QueryRowContext(
		ctx,
		creditQuery,
		wallet.MerchantID,
		wallet.UserID,
		wallet.Currency,
		wallet.Kind,
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

func (r *postgresRepository) Transfer(
	ctx context.Context,
	value *Transfer,
	wallet *types.Wallet,
	transaction *types.Transaction,
	amountCents int64,
) (*Transfer, error) {
	databaseTx, err := r.database.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin wallet transfer: %w", err)
	}
	defer func() {
		_ = databaseTx.Rollback() // No-op after commit; best-effort rollback on earlier returns.
	}()

	reserved, err := reserveTransfer(ctx, databaseTx, value)
	if err != nil {
		return nil, err
	}
	if !reserved {
		existing, err := getTransfer(ctx, databaseTx, value.MerchantID, value.MerchantTransactionID)
		if err != nil {
			return nil, fmt.Errorf("get existing wallet transfer: %w", err)
		}
		if !sameTransfer(existing, value) {
			return nil, ErrTransferConflict
		}
		if err := databaseTx.Commit(); err != nil {
			return nil, fmt.Errorf("commit existing wallet transfer: %w", err)
		}
		return existing, nil
	}

	amount := fixed.FormatCents(amountCents)
	key := keyFromWallet(wallet)
	switch value.Direction {
	case "deposit":
		if err := ensureTransferWallet(ctx, databaseTx, wallet); err != nil {
			return nil, err
		}
		if err := creditTransferWallet(ctx, databaseTx, wallet, amount, transaction); err != nil {
			return nil, err
		}
	case "withdrawal":
		if err := debitTransferWallet(ctx, databaseTx, key, amount, transaction, value.UpdatedAt); err != nil {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("unsupported wallet transfer direction %q", value.Direction)
	}
	if err := insertTransactionWithAmount(ctx, databaseTx, transaction, amount); err != nil {
		return nil, err
	}
	if err := completeTransfer(ctx, databaseTx, value); err != nil {
		return nil, err
	}
	if err := databaseTx.Commit(); err != nil {
		return nil, fmt.Errorf("commit wallet transfer: %w", err)
	}
	return cloneTransfer(value), nil
}

func (r *postgresRepository) GetTransfer(
	ctx context.Context,
	merchantID string,
	merchantTransactionID string,
) (*Transfer, error) {
	return getTransfer(ctx, r.database, merchantID, merchantTransactionID)
}

func reserveTransfer(ctx context.Context, databaseTx *sql.Tx, value *Transfer) (bool, error) {
	const query = `
INSERT INTO wallet_transfers (
    id, merchant_id, merchant_txn_id, user_id, currency, amount, direction,
    status, transaction_id, created_at, updated_at
) VALUES ($1, $2, $3, $4, $5, $6, $7, 'pending', NULL, $8, $9)
ON CONFLICT (merchant_id, merchant_txn_id) DO NOTHING
RETURNING id`
	amountCents, err := fixed.CentsFromFloat(value.Amount)
	if err != nil {
		return false, fmt.Errorf("normalize wallet transfer amount: %w", err)
	}
	var transferID string
	err = databaseTx.QueryRowContext(
		ctx,
		query,
		value.ID,
		value.MerchantID,
		value.MerchantTransactionID,
		value.UserID,
		value.Currency,
		fixed.FormatCents(amountCents),
		value.Direction,
		value.CreatedAt,
		value.UpdatedAt,
	).Scan(&transferID)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("reserve wallet transfer: %w", err)
	}
	return true, nil
}

func ensureTransferWallet(ctx context.Context, databaseTx *sql.Tx, wallet *types.Wallet) error {
	const query = `
INSERT INTO wallets (
    id, merchant_id, user_id, currency, kind, balance, locked_balance, updated_at
) VALUES ($1, $2, $3, $4, $5, 0, 0, $6)
ON CONFLICT (merchant_id, user_id, currency, kind) DO NOTHING`
	_, err := databaseTx.ExecContext(
		ctx,
		query,
		wallet.ID,
		wallet.MerchantID,
		wallet.UserID,
		wallet.Currency,
		wallet.Kind,
		wallet.UpdatedAt,
	)
	if postgresErrorCode(err) == "23503" {
		return ErrInvalidMerchant
	}
	if err != nil {
		return fmt.Errorf("ensure transfer wallet: %w", err)
	}
	return nil
}

func creditTransferWallet(
	ctx context.Context,
	databaseTx *sql.Tx,
	wallet *types.Wallet,
	amount string,
	transaction *types.Transaction,
) error {
	const query = `
UPDATE wallets
SET balance = balance + $5::numeric, updated_at = $6
WHERE merchant_id = $1 AND user_id = $2 AND currency = $3 AND kind = $4
RETURNING id`
	if err := databaseTx.QueryRowContext(
		ctx,
		query,
		wallet.MerchantID,
		wallet.UserID,
		wallet.Currency,
		wallet.Kind,
		amount,
		wallet.UpdatedAt,
	).Scan(&transaction.WalletID); err != nil {
		return fmt.Errorf("update transfer wallet credit: %w", err)
	}
	return nil
}

func debitTransferWallet(
	ctx context.Context,
	databaseTx *sql.Tx,
	key walletKey,
	amount string,
	transaction *types.Transaction,
	updatedAt time.Time,
) error {
	const query = `
UPDATE wallets
SET balance = balance - $5::numeric, updated_at = $6
WHERE merchant_id = $1 AND user_id = $2 AND currency = $3 AND kind = $4 AND balance >= $5::numeric
RETURNING id`
	err := databaseTx.QueryRowContext(
		ctx,
		query,
		key.MerchantID,
		key.UserID,
		key.Currency,
		key.Kind,
		amount,
		updatedAt,
	).Scan(&transaction.WalletID)
	if errors.Is(err, sql.ErrNoRows) {
		return classifyBalanceFailure(ctx, databaseTx, key, false)
	}
	if err != nil {
		return fmt.Errorf("update transfer wallet debit: %w", err)
	}
	return nil
}

func completeTransfer(ctx context.Context, databaseTx *sql.Tx, value *Transfer) error {
	const query = `
UPDATE wallet_transfers
SET status = 'completed', transaction_id = $2, updated_at = $3
WHERE id = $1
RETURNING updated_at`
	if err := databaseTx.QueryRowContext(
		ctx,
		query,
		value.ID,
		value.TransactionID,
		value.UpdatedAt,
	).Scan(&value.UpdatedAt); err != nil {
		return fmt.Errorf("complete wallet transfer: %w", err)
	}
	value.Status = "completed"
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
SET balance = balance - $5::numeric, updated_at = $6
WHERE merchant_id = $1 AND user_id = $2 AND currency = $3 AND kind = $4 AND balance >= $5::numeric
RETURNING id`
	err = databaseTx.QueryRowContext(
		ctx,
		query,
		key.MerchantID,
		key.UserID,
		key.Currency,
		key.Kind,
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
SET balance = balance - $5::numeric,
    locked_balance = locked_balance + $5::numeric,
    updated_at = $6
WHERE merchant_id = $1 AND user_id = $2 AND currency = $3 AND kind = $4 AND balance >= $5::numeric`
	result, err := r.database.ExecContext(
		ctx,
		query,
		key.MerchantID,
		key.UserID,
		key.Currency,
		key.Kind,
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
SET balance = balance + $5::numeric,
    locked_balance = locked_balance - $5::numeric,
    updated_at = $6
WHERE merchant_id = $1 AND user_id = $2 AND currency = $3 AND kind = $4 AND locked_balance >= $5::numeric`
	result, err := r.database.ExecContext(
		ctx,
		query,
		key.MerchantID,
		key.UserID,
		key.Currency,
		key.Kind,
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
WHERE w.merchant_id = $1 AND w.user_id = $2 AND w.kind = 'user'`
	var total int
	if err := r.database.QueryRowContext(ctx, countQuery, merchantID, userID).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count wallet transactions: %w", err)
	}

	const query = `
SELECT t.id, t.wallet_id, t.type, t.amount, t.currency,
       t.related_order_id, t.status, t.created_at
FROM transactions AS t
JOIN wallets AS w ON w.id = t.wallet_id
WHERE w.merchant_id = $1 AND w.user_id = $2 AND w.kind = 'user'
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
		&value.Kind,
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

func getTransfer(
	ctx context.Context,
	querier queryRower,
	merchantID string,
	merchantTransactionID string,
) (*Transfer, error) {
	const query = `
SELECT id, merchant_id, merchant_txn_id, user_id, currency, amount, direction,
       status, transaction_id, created_at, updated_at
FROM wallet_transfers
WHERE merchant_id = $1 AND merchant_txn_id = $2`
	return scanTransfer(querier.QueryRowContext(ctx, query, merchantID, merchantTransactionID))
}

func scanTransfer(row rowScanner) (*Transfer, error) {
	value := &Transfer{}
	var transactionID sql.NullString
	err := row.Scan(
		&value.ID,
		&value.MerchantID,
		&value.MerchantTransactionID,
		&value.UserID,
		&value.Currency,
		&value.Amount,
		&value.Direction,
		&value.Status,
		&transactionID,
		&value.CreatedAt,
		&value.UpdatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrTransferNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("scan wallet transfer: %w", err)
	}
	value.TransactionID = transactionID.String
	return value, nil
}

func insertTransaction(
	ctx context.Context,
	databaseTx *sql.Tx,
	transaction *types.Transaction,
) error {
	return insertTransactionWithAmount(ctx, databaseTx, transaction, transaction.Amount)
}

func insertTransactionWithAmount(
	ctx context.Context,
	databaseTx *sql.Tx,
	transaction *types.Transaction,
	amount any,
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
		amount,
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
WHERE merchant_id = $1 AND user_id = $2 AND currency = $3 AND kind = $4`
	var balance float64
	var lockedBalance float64
	err := querier.QueryRowContext(
		ctx,
		query,
		key.MerchantID,
		key.UserID,
		key.Currency,
		key.Kind,
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

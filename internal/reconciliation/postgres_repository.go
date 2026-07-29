package reconciliation

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/afun-game/predictmarket-saas/pkg/fixed"
)

type postgresRepository struct {
	database *sql.DB
}

type strandedWallet struct {
	id       string
	currency string
	locked   int64
}

func newPostgresRepository(database *sql.DB) *postgresRepository {
	return &postgresRepository{database: database}
}

func (r *postgresRepository) Reconcile(ctx context.Context) (*Result, error) {
	databaseTx, err := r.database.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin wallet reconciliation: %w", err)
	}
	defer func() { _ = databaseTx.Rollback() }()

	const selectStranded = `
SELECT w.id, w.currency, w.locked_balance
FROM wallets AS w
WHERE w.locked_balance > 0
  AND NOT EXISTS (
      SELECT 1
      FROM orders AS o
      WHERE o.merchant_id = w.merchant_id
        AND o.user_id = w.user_id
        AND o.currency = w.currency
        AND o.status IN ('pending', 'partial', 'filled')
  )
ORDER BY w.id
FOR UPDATE OF w SKIP LOCKED`
	rows, err := databaseTx.QueryContext(ctx, selectStranded)
	if err != nil {
		return nil, fmt.Errorf("lock stranded wallets: %w", err)
	}
	defer func() { _ = rows.Close() }()

	wallets := []strandedWallet{}
	for rows.Next() {
		var wallet strandedWallet
		var locked float64
		if err := rows.Scan(&wallet.id, &wallet.currency, &locked); err != nil {
			return nil, fmt.Errorf("scan stranded wallet: %w", err)
		}
		cents, err := fixed.CentsFromFloat(locked)
		if err != nil {
			return nil, fmt.Errorf("parse stranded wallet %s balance: %w", wallet.id, err)
		}
		wallet.locked = cents
		wallets = append(wallets, wallet)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate stranded wallets: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("close stranded wallet rows: %w", err)
	}

	var recoveredCents int64
	for _, wallet := range wallets {
		if err := recoverWalletLock(ctx, databaseTx, wallet); err != nil {
			return nil, err
		}
		recoveredCents += wallet.locked
	}
	if err := databaseTx.Commit(); err != nil {
		return nil, fmt.Errorf("commit wallet reconciliation: %w", err)
	}
	return &Result{
		WalletsRecovered: len(wallets),
		AmountRecovered:  fixed.CentsToFloat(recoveredCents),
	}, nil
}

func recoverWalletLock(ctx context.Context, databaseTx *sql.Tx, wallet strandedWallet) error {
	const updateWallet = `
UPDATE wallets
SET balance = balance + $2::numeric,
    locked_balance = 0,
    updated_at = NOW()
WHERE id = $1 AND locked_balance = $2::numeric`
	amount := fixed.FormatCents(wallet.locked)
	result, err := databaseTx.ExecContext(ctx, updateWallet, wallet.id, amount)
	if err != nil {
		return fmt.Errorf("release stranded wallet %s: %w", wallet.id, err)
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("count released stranded wallet %s: %w", wallet.id, err)
	}
	if rowsAffected != 1 {
		return fmt.Errorf("stranded wallet %s changed during reconciliation", wallet.id)
	}
	const insertTransaction = `
INSERT INTO transactions (
    id, wallet_id, type, amount, currency, status, created_at
) VALUES (gen_random_uuid(), $1, 'reconciliation', $2::numeric, $3, 'completed', NOW())`
	if _, err := databaseTx.ExecContext(ctx, insertTransaction, wallet.id, amount, wallet.currency); err != nil {
		return fmt.Errorf("record stranded wallet recovery %s: %w", wallet.id, err)
	}
	return nil
}

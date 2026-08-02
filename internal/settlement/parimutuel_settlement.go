package settlement

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math/big"
	"time"
)

// marketSettlementContext returns the market's type and owning merchant while
// its row is already locked by the caller.
func marketSettlementContext(
	ctx context.Context,
	databaseTx *sql.Tx,
	marketID string,
) (string, string, error) {
	const query = `SELECT type, merchant_id FROM markets WHERE id = $1`
	var marketType, merchantID string
	err := databaseTx.QueryRowContext(ctx, query, marketID).Scan(&marketType, &merchantID)
	if err != nil {
		return "", "", fmt.Errorf("get settlement market type: %w", err)
	}
	return marketType, merchantID, nil
}

type parimutuelBetRow struct {
	id        string
	userID    string
	option    string
	stakeCents *big.Int
}

// settleParimutuelMarket splits the whole pool among winning bets in
// proportion to their stakes (pari-mutuel). When nobody bet the winning
// option, every active bet is refunded in full.
func settleParimutuelMarket(
	ctx context.Context,
	databaseTx *sql.Tx,
	marketID string,
	merchantID string,
	eventID string,
	winningOption string,
	settledAt time.Time,
) error {
	bets, currency, totalCents, err := lockParimutuelBets(ctx, databaseTx, marketID)
	if err != nil {
		return err
	}
	winnerCents := new(big.Int)
	for _, bet := range bets {
		if bet.option == winningOption {
			winnerCents.Add(winnerCents, bet.stakeCents)
		}
	}

	settlementType := "parimutuel"
	if winnerCents.Sign() == 0 {
		// No winning stakes: refund the pool. A market where every bet lost
		// has no legitimate winner, so keeping the money would be a windfall.
		settlementType = "refund"
		winnerCents.Set(totalCents)
	}

	for _, bet := range bets {
		payout := new(big.Int)
		if bet.option == winningOption || settlementType == "refund" {
			// payout = totalCents * stakeCents / winnerCents
			payout = divideRounded(
				new(big.Int).Mul(totalCents, bet.stakeCents),
				winnerCents,
			)
		}
		if err := applyParimutuelBetSettlement(
			ctx, databaseTx, marketID, eventID, merchantID, bet, payout, currency, settledAt,
		); err != nil {
			return err
		}
	}
	return finalizeParimutuelSettlement(
		ctx, databaseTx, marketID, merchantID, eventID, winningOption, settlementType, settledAt,
	)
}

// lockParimutuelBets locks the pool row and every active bet, and returns
// the bets with their stakes in cents alongside the pool currency and total.
func lockParimutuelBets(
	ctx context.Context,
	databaseTx *sql.Tx,
	marketID string,
) ([]*parimutuelBetRow, string, *big.Int, error) {
	const poolQuery = `
SELECT currency, total_stake::text FROM parimutuel_pools
WHERE market_id = $1 FOR UPDATE`
	var currency, totalStakeText string
	err := databaseTx.QueryRowContext(ctx, poolQuery, marketID).Scan(&currency, &totalStakeText)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, "", nil, ErrMarketNotFound
	}
	if err != nil {
		return nil, "", nil, fmt.Errorf("lock parimutuel pool: %w", err)
	}
	totalCents, err := parseFixed(totalStakeText, 2)
	if err != nil {
		return nil, "", nil, fmt.Errorf("parse parimutuel pool total: %w", err)
	}

	const betQuery = `
SELECT id, user_id, option, stake::text
FROM parimutuel_bets
WHERE market_id = $1 AND status = 'active'
ORDER BY id
FOR UPDATE`
	rows, err := databaseTx.QueryContext(ctx, betQuery, marketID)
	if err != nil {
		return nil, "", nil, fmt.Errorf("lock parimutuel bets: %w", err)
	}
	defer func() { _ = rows.Close() }()
	bets := []*parimutuelBetRow{}
	sumCents := new(big.Int)
	for rows.Next() {
		bet := &parimutuelBetRow{}
		var stakeText string
		if err := rows.Scan(&bet.id, &bet.userID, &bet.option, &stakeText); err != nil {
			return nil, "", nil, fmt.Errorf("scan parimutuel bet: %w", err)
		}
		bet.stakeCents, err = parseFixed(stakeText, 2)
		if err != nil {
			return nil, "", nil, fmt.Errorf("parse parimutuel stake: %w", err)
		}
		bets = append(bets, bet)
		sumCents.Add(sumCents, bet.stakeCents)
	}
	if err := rows.Err(); err != nil {
		return nil, "", nil, err
	}
	if sumCents.Cmp(totalCents) != 0 {
		return nil, "", nil, fmt.Errorf(
			"parimutuel pool %s sums to %s but bets total %s",
			marketID, formatCents(totalCents), formatCents(sumCents),
		)
	}
	return bets, currency, totalCents, nil
}

// applyParimutuelBetSettlement credits a bet's wallet (when it won) and marks
// the bet settled. The pool already holds the stake; only the payout moves.
func applyParimutuelBetSettlement(
	ctx context.Context,
	databaseTx *sql.Tx,
	marketID string,
	eventID string,
	merchantID string,
	bet *parimutuelBetRow,
	payout *big.Int,
	currency string,
	settledAt time.Time,
) error {
	if payout.Sign() > 0 {
		var walletID string
		const walletQuery = `
SELECT id FROM wallets
WHERE merchant_id = $1 AND user_id = $2 AND currency = $3 AND kind = 'user'
FOR UPDATE`
		err := databaseTx.QueryRowContext(ctx, walletQuery, merchantID, bet.userID, currency).Scan(&walletID)
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("%w: parimutuel bet %s", ErrOrderWalletNotFound, bet.id)
		}
		if err != nil {
			return fmt.Errorf("lock parimutuel bet wallet: %w", err)
		}
		if _, err := databaseTx.ExecContext(
			ctx,
			"UPDATE wallets SET balance = balance + $2::numeric, updated_at = $3 WHERE id = $1",
			walletID, formatCents(payout), settledAt,
		); err != nil {
			return fmt.Errorf("credit parimutuel payout wallet: %w", err)
		}
		if err := insertSettlementTransaction(
			ctx, databaseTx, walletID, "win", payout, currency, "", settledAt,
		); err != nil {
			return err
		}
	}
	const updateBet = `
UPDATE parimutuel_bets SET status = 'settled', settled_at = $2 WHERE id = $1`
	if _, err := databaseTx.ExecContext(ctx, updateBet, bet.id, settledAt); err != nil {
		return fmt.Errorf("settle parimutuel bet: %w", err)
	}
	return nil
}

func finalizeParimutuelSettlement(
	ctx context.Context,
	databaseTx *sql.Tx,
	marketID string,
	merchantID string,
	eventID string,
	winningOption string,
	settlementType string,
	settledAt time.Time,
) error {
	const insertSettlement = `
INSERT INTO market_settlements (market_id, event_id, winning_option, settlement_type, settled_at)
VALUES ($1, $2, $3, $4, $5)`
	if _, err := databaseTx.ExecContext(
		ctx, insertSettlement, marketID, eventID, winningOption, settlementType, settledAt,
	); err != nil {
		return fmt.Errorf("insert parimutuel market settlement: %w", err)
	}
	if _, err := databaseTx.ExecContext(
		ctx,
		"UPDATE markets SET status = 'settled', settled_at = $2 WHERE id = $1",
		marketID,
		settledAt,
	); err != nil {
		return fmt.Errorf("mark parimutuel market settled: %w", err)
	}
	// The webhook payload only needs the merchant identity for fan-out.
	merchants := []*settlementOrder{{merchantID: merchantID}}
	if err := enqueueMarketSettledWebhook(
		ctx, databaseTx, marketID, eventID, winningOption, settledAt, merchants,
	); err != nil {
		return err
	}
	return nil
}

// voidParimutuelMarket refunds every active bet when its market is voided.
func voidParimutuelMarket(
	ctx context.Context,
	databaseTx *sql.Tx,
	marketID string,
	merchantID string,
	eventID string,
	voidedAt time.Time,
) error {
	bets, currency, _, err := lockParimutuelBets(ctx, databaseTx, marketID)
	if err != nil {
		return err
	}
	for _, bet := range bets {
		var walletID string
		const walletQuery = `
SELECT id FROM wallets
WHERE merchant_id = $1 AND user_id = $2 AND currency = $3 AND kind = 'user'
FOR UPDATE`
		err := databaseTx.QueryRowContext(ctx, walletQuery, merchantID, bet.userID, currency).Scan(&walletID)
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("%w: parimutuel bet %s", ErrOrderWalletNotFound, bet.id)
		}
		if err != nil {
			return fmt.Errorf("lock parimutuel void wallet: %w", err)
		}
		if _, err := databaseTx.ExecContext(
			ctx,
			"UPDATE wallets SET balance = balance + $2::numeric, updated_at = $3 WHERE id = $1",
			walletID, formatCents(bet.stakeCents), voidedAt,
		); err != nil {
			return fmt.Errorf("refund parimutuel void wallet: %w", err)
		}
		if err := insertSettlementTransaction(
			ctx, databaseTx, walletID, "bet_refund", bet.stakeCents, currency, "", voidedAt,
		); err != nil {
			return err
		}
		const updateBet = `
UPDATE parimutuel_bets SET status = 'voided', settled_at = $2 WHERE id = $1`
		if _, err := databaseTx.ExecContext(ctx, updateBet, bet.id, voidedAt); err != nil {
			return fmt.Errorf("void parimutuel bet: %w", err)
		}
	}
	const insertVoid = `
INSERT INTO market_settlements (market_id, event_id, winning_option, settlement_type, settled_at)
VALUES ($1, $2, NULL, 'void', $3)`
	if _, err := databaseTx.ExecContext(ctx, insertVoid, marketID, eventID, voidedAt); err != nil {
		return fmt.Errorf("insert parimutuel market void: %w", err)
	}
	if _, err := databaseTx.ExecContext(
		ctx,
		"UPDATE markets SET status = 'voided', settled_at = $2 WHERE id = $1",
		marketID,
		voidedAt,
	); err != nil {
		return fmt.Errorf("mark parimutuel market voided: %w", err)
	}
	merchants := []*settlementOrder{{merchantID: merchantID}}
	if err := enqueueMarketVoidedWebhook(ctx, databaseTx, marketID, eventID, voidedAt, merchants); err != nil {
		return err
	}
	return nil
}

package settlement

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math/big"
	"time"
)

// marketSettlementContext returns the market's type, owning merchant, and fee
// rates while its row is already locked by the caller.
func marketSettlementContext(
	ctx context.Context,
	databaseTx *sql.Tx,
	marketID string,
) (string, string, *big.Int, *big.Int, error) {
	const query = `SELECT type, merchant_id, merchant_fee_rate::text, platform_fee_rate::text FROM markets WHERE id = $1`
	var marketType, merchantID, merchantFeeRateStr, platformFeeRateStr string
	err := databaseTx.QueryRowContext(ctx, query, marketID).Scan(&marketType, &merchantID, &merchantFeeRateStr, &platformFeeRateStr)
	if err != nil {
		return "", "", nil, nil, fmt.Errorf("get settlement market type: %w", err)
	}
	merchantFeeRate, err := parseFixed(merchantFeeRateStr, 6)
	if err != nil {
		return "", "", nil, nil, fmt.Errorf("parse merchant fee rate: %w", err)
	}
	platformFeeRate, err := parseFixed(platformFeeRateStr, 6)
	if err != nil {
		return "", "", nil, nil, fmt.Errorf("parse platform fee rate: %w", err)
	}
	return marketType, merchantID, merchantFeeRate, platformFeeRate, nil
}

type parimutuelBetRow struct {
	id               string
	userID           string
	option           string
	stakeCents       *big.Int
	walletKind       string
	merchantFeeRate  *big.Int
	platformFeeRate  *big.Int
	merchantFeeCents *big.Int
	platformFeeCents *big.Int
	netPayout        *big.Int
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
	merchantFeeRate *big.Int,
	platformFeeRate *big.Int,
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
		grossPayout := new(big.Int)
		if bet.option == winningOption || settlementType == "refund" {
			// grossPayout = totalCents * stakeCents / winnerCents
			grossPayout = divideRounded(
				new(big.Int).Mul(totalCents, bet.stakeCents),
				winnerCents,
			)
		}

		// Calculate fees on gross payout (only when there's a payout)
		bet.merchantFeeRate = merchantFeeRate
		bet.platformFeeRate = platformFeeRate
		bet.merchantFeeCents = new(big.Int)
		bet.platformFeeCents = new(big.Int)
		bet.netPayout = new(big.Int)

		if grossPayout.Sign() > 0 && settlementType != "refund" {
			bet.merchantFeeCents = calculateFee(grossPayout, merchantFeeRate)
			bet.platformFeeCents = calculateFee(grossPayout, platformFeeRate)
			bet.netPayout.Set(grossPayout)
			bet.netPayout.Sub(bet.netPayout, bet.merchantFeeCents)
			bet.netPayout.Sub(bet.netPayout, bet.platformFeeCents)
		} else {
			// Refunds have no fees
			bet.netPayout.Set(grossPayout)
		}

		if err := applyParimutuelBetSettlement(
			ctx, databaseTx, marketID, eventID, merchantID, bet, currency, settlementType, settledAt,
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
SELECT id, user_id, option, stake::text, wallet_kind
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
		if err := rows.Scan(&bet.id, &bet.userID, &bet.option, &stakeText, &bet.walletKind); err != nil {
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
// Shadow (seamless) bets are paid through credit callbacks instead of the
// platform wallet.
func applyParimutuelBetSettlement(
	ctx context.Context,
	databaseTx *sql.Tx,
	marketID string,
	eventID string,
	merchantID string,
	bet *parimutuelBetRow,
	currency string,
	settlementType string,
	settledAt time.Time,
) error {
	walletKind := "user"
	if bet.walletKind == "shadow" {
		walletKind = "shadow"
	}
	var walletID string
	const walletQuery = `
SELECT id FROM wallets
WHERE merchant_id = $1 AND user_id = $2 AND currency = $3 AND kind = $4
FOR UPDATE`
	err := databaseTx.QueryRowContext(ctx, walletQuery, merchantID, bet.userID, currency, walletKind).Scan(&walletID)
	if errors.Is(err, sql.ErrNoRows) {
		if bet.netPayout.Sign() > 0 {
			return fmt.Errorf("%w: parimutuel bet %s", ErrOrderWalletNotFound, bet.id)
		}
	} else if err != nil {
		return fmt.Errorf("lock parimutuel bet wallet: %w", err)
	}
	// One audit row per settled bet, mirroring order-book settlement_payouts:
	// winning and losing bets both appear so GET /api/v2/settlements/{id}/payouts
	// reflects the whole settlement. order_id carries the bet ID (migration 033
	// removed the orders FK for this reference); the payout is the net amount
	// the user actually receives after fees.
	if walletID != "" {
		const payoutQuery = `
INSERT INTO settlement_payouts (
    market_id, order_id, wallet_id, currency, stake, payout, created_at
) VALUES ($1, $2, $3, $4, $5, $6, $7)`
		if _, err := databaseTx.ExecContext(
			ctx, payoutQuery, marketID, bet.id, walletID, currency,
			formatCents(bet.stakeCents), formatCents(bet.netPayout), settledAt,
		); err != nil {
			return fmt.Errorf("insert parimutuel settlement payout: %w", err)
		}
	}
	// Every shadow participant releases exactly their own stake; winners are
	// then paid their net payout through a signed credit callback. Releasing
	// the loser's stake too is what keeps the winners' shadow wallets funded
	// for the pool-wide payout.
	if bet.walletKind == "shadow" && walletID != "" && bet.stakeCents.Sign() > 0 {
		if err := releaseShadowStake(ctx, databaseTx, walletID, bet.stakeCents, settledAt); err != nil {
			return err
		}
	}
	if bet.netPayout.Sign() > 0 && bet.walletKind == "shadow" {
		reason := "payout"
		if settlementType == "refund" {
			reason = "refund_cancel"
		}
		if err := enqueueSettlementShadowCredit(
			ctx, databaseTx, merchantID, bet.userID, currency, bet.id, walletID,
			marketID, eventID, bet.netPayout, reason, settledAt,
		); err != nil {
			return err
		}
	} else if bet.netPayout.Sign() > 0 {
		if _, err := databaseTx.ExecContext(
			ctx,
			"UPDATE wallets SET balance = balance + $2::numeric, updated_at = $3 WHERE id = $1",
			walletID, formatCents(bet.netPayout), settledAt,
		); err != nil {
			return fmt.Errorf("credit parimutuel payout wallet: %w", err)
		}
		if err := insertSettlementTransaction(
			ctx, databaseTx, walletID, "win", bet.netPayout, currency, "", settledAt,
		); err != nil {
			return err
		}
	}

	if bet.merchantFeeCents.Sign() > 0 {
		if err := recordFee(
			ctx, databaseTx, merchantID, marketID,
			"merchant", bet.merchantFeeRate, bet.merchantFeeCents, currency, settledAt,
		); err != nil {
			return err
		}
	}
	if bet.platformFeeCents.Sign() > 0 {
		if err := recordFee(
			ctx, databaseTx, merchantID, marketID,
			"platform", bet.platformFeeRate, bet.platformFeeCents, currency, settledAt,
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
		if bet.walletKind == "shadow" {
			var walletID string
			const shadowWalletQuery = `
SELECT id FROM wallets
WHERE merchant_id = $1 AND user_id = $2 AND currency = $3 AND kind = 'shadow'
FOR UPDATE`
			err := databaseTx.QueryRowContext(ctx, shadowWalletQuery, merchantID, bet.userID, currency).Scan(&walletID)
			if errors.Is(err, sql.ErrNoRows) {
				return fmt.Errorf("%w: parimutuel bet %s", ErrOrderWalletNotFound, bet.id)
			}
			if err != nil {
				return fmt.Errorf("lock parimutuel void shadow wallet: %w", err)
			}
			// Seamless stakes are refunded to the merchant wallet through a
			// signed credit callback (reason void); release the mirror first.
			if err := releaseShadowStake(ctx, databaseTx, walletID, bet.stakeCents, voidedAt); err != nil {
				return err
			}
			if err := enqueueSettlementShadowCredit(
				ctx, databaseTx, merchantID, bet.userID, currency, bet.id, walletID,
				marketID, eventID, bet.stakeCents, "void", voidedAt,
			); err != nil {
				return err
			}
		} else {
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

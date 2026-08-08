// Package settlement atomically settles resolved events and their markets.
package settlement

import (
	"context"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/nxsky/twill"
)

var (
	ErrEventNotFound        = errors.New("settlement event not found")
	ErrEventUnresolved      = errors.New("event is not resolved")
	ErrOutcomeNotOption     = errors.New("event outcome is not a market option")
	ErrOrderWalletNotFound  = errors.New("settlement order wallet not found")
	ErrMarketNotFound       = errors.New("settlement market not found")
	ErrMarketAlreadySettled = errors.New("settlement market has already been settled or voided")
)

// Service settles every market associated with a resolved event and voids
// unsettled markets when a resolution is withdrawn.
type Service interface {
	SettleEvent(ctx context.Context, eventID string) error
	// SettleMarket settles a single market immediately with the given
	// winning option, without resolving the owning event. Used by the admin
	// console for one-market settlement while the event stays open.
	SettleMarket(ctx context.Context, marketID, winningOption string) error
	// VoidMarket refunds every order on an unsettled market in full and marks
	// the market voided (V3 §5.1 order.voided / market.voided events).
	VoidMarket(ctx context.Context, marketID string) error
}

type implementation struct {
	twill.Implements[Service]

	database   twill.Database `twill:"primary-db"`
	repository Repository
	now        func() time.Time
}

// NewServiceWithRepository creates a settlement service for tests and explicit wiring.
func NewServiceWithRepository(repository Repository) Service {
	return &implementation{repository: repository, now: time.Now}
}

// NewPostgresService creates a settlement service backed by the provided database.
func NewPostgresService(database *sql.DB) Service {
	return NewServiceWithRepository(newPostgresRepository(database))
}

func (s *implementation) Init(context.Context) error {
	if s.repository == nil {
		database := s.database.Get()
		if database == nil || database.StdDB() == nil {
			return errors.New("primary database is not configured")
		}
		s.repository = newPostgresRepository(database.StdDB())
	}
	if s.now == nil {
		s.now = time.Now
	}
	return nil
}

func (s *implementation) SettleEvent(ctx context.Context, eventID string) error {
	eventID = strings.TrimSpace(eventID)
	if !isUUID(eventID) {
		return fmt.Errorf("invalid event_id: must be a UUID")
	}
	if err := s.repository.SettleEvent(ctx, eventID, s.now().UTC()); err != nil {
		return fmt.Errorf("settle event: %w", err)
	}
	return nil
}

func (s *implementation) SettleMarket(ctx context.Context, marketID, winningOption string) error {
	marketID = strings.TrimSpace(marketID)
	if !isUUID(marketID) {
		return fmt.Errorf("invalid market_id: must be a UUID")
	}
	if strings.TrimSpace(winningOption) == "" {
		return errors.New("winning_option is required")
	}
	if err := s.repository.SettleMarket(ctx, marketID, strings.TrimSpace(winningOption), s.now().UTC()); err != nil {
		return fmt.Errorf("settle market: %w", err)
	}
	return nil
}

func (s *implementation) VoidMarket(ctx context.Context, marketID string) error {
	marketID = strings.TrimSpace(marketID)
	if !isUUID(marketID) {
		return fmt.Errorf("invalid market_id: must be a UUID")
	}
	if err := s.repository.VoidMarket(ctx, marketID, s.now().UTC()); err != nil {
		return fmt.Errorf("void market: %w", err)
	}
	return nil
}

// Repository settles an event as independently atomic market transactions.
type Repository interface {
	SettleEvent(ctx context.Context, eventID string, settledAt time.Time) error
	SettleMarket(ctx context.Context, marketID, winningOption string, settledAt time.Time) error
	VoidMarket(ctx context.Context, marketID string, voidedAt time.Time) error
}

type settlementOrder struct {
	id               string
	walletID         string
	merchantID       string
	userID           string
	side             string
	option           string
	currency         string
	status           string
	walletKind       string
	amount           *big.Int
	filled           *big.Int
	price            *big.Int
	stake            *big.Int
	payout           *big.Int
	refund           *big.Int
	lockedUse        *big.Int
	merchantFeeRate  *big.Int // decimal(10,6) as fixed-point integer (6 decimals)
	platformFeeRate  *big.Int // decimal(10,6) as fixed-point integer (6 decimals)
	merchantFeeCents *big.Int
	platformFeeCents *big.Int
}

func calculatePayouts(orders []*settlementOrder, winningOption string) {
	for _, order := range orders {
		order.payout = new(big.Int)
		order.refund = new(big.Int)
		order.lockedUse = new(big.Int)
		order.merchantFeeCents = new(big.Int)
		order.platformFeeCents = new(big.Int)

		if order.stake != nil {
			order.lockedUse.Set(order.stake)
		}
		unfilledShares := new(big.Int).Sub(order.amount, order.filled)
		if unfilledShares.Sign() > 0 {
			order.refund = collateralCents(order.side, unfilledShares, order.price)
			order.lockedUse.Add(order.lockedUse, order.refund)
		}
		if orderWins(order, winningOption) {
			grossPayout := payoutCents(order.filled)

			// Calculate fees on gross payout
			merchantFee := calculateFee(grossPayout, order.merchantFeeRate)
			platformFee := calculateFee(grossPayout, order.platformFeeRate)

			order.merchantFeeCents.Set(merchantFee)
			order.platformFeeCents.Set(platformFee)

			// Net payout = gross - merchant fee - platform fee
			order.payout.Set(grossPayout)
			order.payout.Sub(order.payout, merchantFee)
			order.payout.Sub(order.payout, platformFee)
		}
	}
}

// calculateFee computes fee = amount * rate / 1_000_000 (rate is 6-decimal fixed-point)
func calculateFee(amountCents *big.Int, feeRate *big.Int) *big.Int {
	if feeRate.Sign() == 0 {
		return new(big.Int)
	}
	fee := new(big.Int).Mul(amountCents, feeRate)
	fee.Div(fee, big.NewInt(1_000_000))
	return fee
}

func orderWins(order *settlementOrder, winningOption string) bool {
	return (order.side == "buy" && order.option == winningOption) ||
		(order.side == "sell" && order.option != winningOption)
}

func parseCents(value string) (*big.Int, error) {
	return parseFixed(value, 2)
}

func parseShares(value string) (*big.Int, error) {
	return parseFixed(value, 6)
}

func parseFixed(input string, decimals int) (*big.Int, error) {
	parts := strings.Split(input, ".")
	if len(parts) > 2 || parts[0] == "" {
		return nil, fmt.Errorf("invalid fixed-point value %q", input)
	}
	fraction := ""
	if len(parts) == 2 {
		fraction = parts[1]
	}
	if len(fraction) > decimals {
		return nil, fmt.Errorf("fixed-point value %q has more than %d decimals", input, decimals)
	}
	fraction += strings.Repeat("0", decimals-len(fraction))
	value, ok := new(big.Int).SetString(parts[0]+fraction, 10)
	if !ok || value.Sign() < 0 {
		return nil, fmt.Errorf("invalid fixed-point value %q", input)
	}
	return value, nil
}

var (
	shareScale = big.NewInt(1_000_000)
	priceScale = big.NewInt(1_000_000)
	centsScale = big.NewInt(100)
)

func collateralCents(side string, shares *big.Int, price *big.Int) *big.Int {
	exposurePrice := new(big.Int).Set(price)
	if side == "sell" {
		exposurePrice.Sub(priceScale, exposurePrice)
	}
	product := new(big.Int).Mul(shares, exposurePrice)
	product.Mul(product, centsScale)
	denominator := new(big.Int).Mul(shareScale, priceScale)
	return divideRounded(product, denominator)
}

func payoutCents(shares *big.Int) *big.Int {
	return divideRounded(new(big.Int).Mul(shares, centsScale), shareScale)
}

func divideRounded(value, denominator *big.Int) *big.Int {
	quotient, remainder := new(big.Int), new(big.Int)
	quotient.QuoRem(value, denominator, remainder)
	if remainder.Lsh(remainder, 1).Cmp(denominator) >= 0 {
		quotient.Add(quotient, big.NewInt(1))
	}
	return quotient
}

func formatCents(cents *big.Int) string {
	value := cents.String()
	if len(value) < 3 {
		value = strings.Repeat("0", 3-len(value)) + value
	}
	return value[:len(value)-2] + "." + value[len(value)-2:]
}

// formatRate renders a 6-decimal fixed-point rate for fee_ledger.rate.
func formatRate(rate *big.Int) string {
	value := rate.String()
	if len(value) < 7 {
		value = strings.Repeat("0", 7-len(value)) + value
	}
	return value[:len(value)-6] + "." + value[len(value)-6:]
}

// recordFee accumulates a settled fee into the ledger. fee_ledger holds one
// row per (market, currency, recipient) — see its UNIQUE constraint — so
// per-order fees are summed into that row rather than inserted separately.
func recordFee(
	ctx context.Context,
	databaseTx *sql.Tx,
	merchantID string,
	marketID string,
	recipient string,
	rate *big.Int,
	feeCents *big.Int,
	currency string,
	collectedAt time.Time,
) error {
	const query = `
INSERT INTO fee_ledger (market_id, merchant_id, currency, recipient, rate, amount, created_at)
VALUES ($1, $2, $3, $4, $5::numeric, $6::numeric, $7)
ON CONFLICT (market_id, currency, recipient)
DO UPDATE SET amount = fee_ledger.amount + EXCLUDED.amount`
	if _, err := databaseTx.ExecContext(
		ctx, query,
		marketID, merchantID, currency, recipient,
		formatRate(rate), formatCents(feeCents), collectedAt,
	); err != nil {
		return fmt.Errorf("record %s fee: %w", recipient, err)
	}
	return nil
}

func isUUID(value string) bool {
	if len(value) != 36 || value[8] != '-' || value[13] != '-' || value[18] != '-' || value[23] != '-' {
		return false
	}
	_, err := hex.DecodeString(strings.ReplaceAll(value, "-", ""))
	return err == nil
}

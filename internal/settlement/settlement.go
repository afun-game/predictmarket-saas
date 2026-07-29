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
	ErrEventNotFound       = errors.New("settlement event not found")
	ErrEventUnresolved     = errors.New("event is not resolved")
	ErrOutcomeNotOption    = errors.New("event outcome is not a market option")
	ErrOrderWalletNotFound = errors.New("settlement order wallet not found")
)

// Service settles every market associated with a resolved event.
type Service interface {
	SettleEvent(ctx context.Context, eventID string) error
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

// Repository settles an event as independently atomic market transactions.
type Repository interface {
	SettleEvent(ctx context.Context, eventID string, settledAt time.Time) error
}

type settlementOrder struct {
	id        string
	walletID  string
	side      string
	option    string
	currency  string
	status    string
	amount    *big.Int
	filled    *big.Int
	price     *big.Int
	stake     *big.Int
	payout    *big.Int
	refund    *big.Int
	lockedUse *big.Int
}

func calculatePayouts(orders []*settlementOrder, winningOption string) {
	for _, order := range orders {
		order.payout = new(big.Int)
		order.refund = new(big.Int)
		order.lockedUse = new(big.Int)
		if order.stake != nil {
			order.lockedUse.Set(order.stake)
		}
		unfilledShares := new(big.Int).Sub(order.amount, order.filled)
		if unfilledShares.Sign() > 0 {
			order.refund = collateralCents(order.side, unfilledShares, order.price)
			order.lockedUse.Add(order.lockedUse, order.refund)
		}
		if orderWins(order, winningOption) {
			order.payout.Set(payoutCents(order.filled))
		}
	}
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

func isUUID(value string) bool {
	if len(value) != 36 || value[8] != '-' || value[13] != '-' || value[18] != '-' || value[23] != '-' {
		return false
	}
	_, err := hex.DecodeString(strings.ReplaceAll(value, "-", ""))
	return err == nil
}

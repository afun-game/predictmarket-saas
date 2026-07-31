// Package v2query exposes tenant-scoped read models used by the V2
// reconciliation API. Amounts are represented as fixed decimal strings.
package v2query

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

const defaultLimit = 100
const maxLimit = 500

var ErrNotFound = errors.New("V2 query subject not found")

// Service reads tenant-owned financial history without OFFSET pagination.
type Service interface {
	ListTransactions(ctx context.Context, filters TransactionFilters) (*TransactionPage, error)
	ListSettlements(ctx context.Context, filters SettlementFilters) (*SettlementPage, error)
	ListPayouts(ctx context.Context, filters PayoutFilters) (*PayoutPage, error)
	DailyReport(ctx context.Context, merchantID string, date time.Time, currency string) (*DailyReport, error)
}

// TransactionFilters scopes the platform wallet ledger to one merchant.
type TransactionFilters struct {
	MerchantID string
	UserID     string
	Type       string
	From       *time.Time
	To         *time.Time
	Cursor     string
	Limit      int
}

// SettlementFilters scopes market settlement records to one merchant.
type SettlementFilters struct {
	MerchantID string
	From       *time.Time
	To         *time.Time
	Cursor     string
	Limit      int
}

// PayoutFilters scopes settlement payout rows to one tenant market.
type PayoutFilters struct {
	MerchantID string
	MarketID   string
	Cursor     string
	Limit      int
}

// Transaction is a wallet ledger entry with its owning user attached.
type Transaction struct {
	ID             string    `json:"id"`
	WalletID       string    `json:"wallet_id"`
	UserID         string    `json:"user_id"`
	Type           string    `json:"type"`
	Amount         string    `json:"amount"`
	Currency       string    `json:"currency"`
	RelatedOrderID *string   `json:"related_order_id,omitempty"`
	Status         string    `json:"status"`
	CreatedAt      time.Time `json:"created_at"`
}

// TransactionPage contains a stable newest-first keyset page.
type TransactionPage struct {
	Transactions []Transaction `json:"transactions"`
	NextCursor   string        `json:"next_cursor,omitempty"`
}

// Settlement is one settled market outcome.
type Settlement struct {
	MarketID       string    `json:"market_id"`
	EventID        string    `json:"event_id"`
	WinningOption  string    `json:"winning_option,omitempty"`
	SettlementType string    `json:"settlement_type"` // settle | void
	SettledAt      time.Time `json:"settled_at"`
}

// SettlementPage contains a stable newest-first settlement page.
type SettlementPage struct {
	Settlements []Settlement `json:"settlements"`
	NextCursor  string       `json:"next_cursor,omitempty"`
}

// Payout describes one order's settled stake and payout.
type Payout struct {
	MarketID  string    `json:"market_id"`
	OrderID   string    `json:"order_id"`
	WalletID  string    `json:"wallet_id"`
	UserID    string    `json:"user_id"`
	Currency  string    `json:"currency"`
	Stake     string    `json:"stake"`
	Payout    string    `json:"payout"`
	CreatedAt time.Time `json:"created_at"`
}

// PayoutPage contains a stable newest-first payout page.
type PayoutPage struct {
	Payouts    []Payout `json:"payouts"`
	NextCursor string   `json:"next_cursor,omitempty"`
}

// DailyReport contains platform ledger totals for one UTC calendar day.
type DailyReport struct {
	Date                string `json:"date"`
	Currency            string `json:"currency"`
	Bets                string `json:"bets"`
	Refunds             string `json:"refunds"`
	Payouts             string `json:"payouts"`
	GGR                 string `json:"ggr"`
	Fees                string `json:"fees"`
	TransferDeposits    string `json:"transfer_deposits"`
	TransferWithdrawals string `json:"transfer_withdrawals"`
}

type implementation struct{ database *sql.DB }

// New creates a V2 query service backed by the application's primary database.
func New(database *sql.DB) Service {
	return &implementation{database: database}
}

type ValidationError struct {
	Field   string
	Message string
}

func (e *ValidationError) Error() string {
	return fmt.Sprintf("invalid %s: %s", e.Field, e.Message)
}

type cursor struct {
	CreatedAt time.Time
	ID        string
}

type encodedCursor struct {
	CreatedAt string `json:"created_at"`
	ID        string `json:"id"`
}

func normalizeTransactionFilters(filters TransactionFilters) (TransactionFilters, *cursor, error) {
	filters.MerchantID = strings.TrimSpace(filters.MerchantID)
	filters.UserID = strings.TrimSpace(filters.UserID)
	filters.Type = strings.ToLower(strings.TrimSpace(filters.Type))
	if !isUUID(filters.MerchantID) {
		return TransactionFilters{}, nil, &ValidationError{Field: "merchant_id", Message: "must be a UUID"}
	}
	if len(filters.UserID) > 255 {
		return TransactionFilters{}, nil, &ValidationError{Field: "user_id", Message: "must not exceed 255 characters"}
	}
	if filters.Type != "" && !validTransactionType(filters.Type) {
		return TransactionFilters{}, nil, &ValidationError{Field: "type", Message: "is not supported"}
	}
	cursor, err := decodeCursor(filters.Cursor)
	if err != nil {
		return TransactionFilters{}, nil, err
	}
	if err := validateTimes(filters.From, filters.To); err != nil {
		return TransactionFilters{}, nil, err
	}
	filters.Limit, err = normalizeLimit(filters.Limit)
	if err != nil {
		return TransactionFilters{}, nil, err
	}
	return filters, cursor, nil
}

func normalizeSettlementFilters(filters SettlementFilters) (SettlementFilters, *cursor, error) {
	filters.MerchantID = strings.TrimSpace(filters.MerchantID)
	if !isUUID(filters.MerchantID) {
		return SettlementFilters{}, nil, &ValidationError{Field: "merchant_id", Message: "must be a UUID"}
	}
	cursor, err := decodeCursor(filters.Cursor)
	if err != nil {
		return SettlementFilters{}, nil, err
	}
	if err := validateTimes(filters.From, filters.To); err != nil {
		return SettlementFilters{}, nil, err
	}
	filters.Limit, err = normalizeLimit(filters.Limit)
	if err != nil {
		return SettlementFilters{}, nil, err
	}
	return filters, cursor, nil
}

func normalizePayoutFilters(filters PayoutFilters) (PayoutFilters, *cursor, error) {
	filters.MerchantID = strings.TrimSpace(filters.MerchantID)
	filters.MarketID = strings.TrimSpace(filters.MarketID)
	if !isUUID(filters.MerchantID) {
		return PayoutFilters{}, nil, &ValidationError{Field: "merchant_id", Message: "must be a UUID"}
	}
	if !isUUID(filters.MarketID) {
		return PayoutFilters{}, nil, &ValidationError{Field: "market_id", Message: "must be a UUID"}
	}
	cursor, err := decodeCursor(filters.Cursor)
	if err != nil {
		return PayoutFilters{}, nil, err
	}
	filters.Limit, err = normalizeLimit(filters.Limit)
	if err != nil {
		return PayoutFilters{}, nil, err
	}
	return filters, cursor, nil
}

func normalizeLimit(limit int) (int, error) {
	if limit == 0 {
		return defaultLimit, nil
	}
	if limit < 1 || limit > maxLimit {
		return 0, &ValidationError{Field: "limit", Message: "must be between 1 and 500"}
	}
	return limit, nil
}

func validateTimes(from, to *time.Time) error {
	if from != nil && to != nil && from.After(*to) {
		return &ValidationError{Field: "from", Message: "must not be after to"}
	}
	return nil
}

func decodeCursor(value string) (*cursor, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, nil
	}
	decoded, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return nil, &ValidationError{Field: "cursor", Message: "is invalid"}
	}
	encoded := encodedCursor{}
	if err := json.Unmarshal(decoded, &encoded); err != nil {
		return nil, &ValidationError{Field: "cursor", Message: "is invalid"}
	}
	createdAt, err := time.Parse(time.RFC3339Nano, encoded.CreatedAt)
	if err != nil || !isUUID(encoded.ID) {
		return nil, &ValidationError{Field: "cursor", Message: "is invalid"}
	}
	return &cursor{CreatedAt: createdAt.UTC(), ID: encoded.ID}, nil
}

func encodeCursor(createdAt time.Time, id string) string {
	encoded, err := json.Marshal(encodedCursor{CreatedAt: createdAt.UTC().Format(time.RFC3339Nano), ID: id})
	if err != nil {
		panic(fmt.Errorf("marshal V2 query cursor: %w", err))
	}
	return base64.RawURLEncoding.EncodeToString(encoded)
}

func isUUID(value string) bool {
	if len(value) != 36 || value[8] != '-' || value[13] != '-' || value[18] != '-' || value[23] != '-' {
		return false
	}
	_, err := hex.DecodeString(strings.ReplaceAll(value, "-", ""))
	return err == nil
}

func validTransactionType(value string) bool {
	switch value {
	case "credit", "debit", "bet", "win", "fee", "transfer_deposit", "transfer_withdrawal":
		return true
	default:
		return false
	}
}

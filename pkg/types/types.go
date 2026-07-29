package types

import (
	"time"

	"github.com/nxsky/twill"
)

// Merchant represents a tenant in the SaaS platform
type Merchant struct {
	twill.AutoMarshal

	ID     string `json:"id"`
	Name   string `json:"name"`
	Email  string `json:"email"`
	APIKey string `json:"api_key"`
	// APIKeyPrefix is a non-secret lookup hint; the complete key is bcrypt-hashed.
	APIKeyPrefix string `json:"-"`
	APISecret    string `json:"-"`        // Never expose in JSON
	Status       string `json:"status"`   // active, suspended, inactive
	Currency     string `json:"currency"` // USD, EUR, CNY, etc.
	Timezone     string `json:"timezone"` // IANA timezone
	// FeeRate mirrors the legacy column for persistence compatibility. It is
	// fixed at zero until administrator fee configuration is available.
	FeeRate   float64   `json:"-"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Event represents a prediction event
type Event struct {
	twill.AutoMarshal

	ID             string    `json:"id"`
	SourceType     string    `json:"source_type"` // polymarket, custom
	SourceID       string    `json:"source_id"`
	Title          string    `json:"title"`
	Description    string    `json:"description"`
	Category       string    `json:"category"` // politics, sports, crypto, entertainment
	EndTime        time.Time `json:"end_time"`
	ResolutionTime time.Time `json:"resolution_time"`
	Status         string    `json:"status"` // pending, active, closed, resolved
	Outcome        *string   `json:"outcome,omitempty"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

// Market represents a prediction market
type Market struct {
	twill.AutoMarshal

	ID            string   `json:"id"`
	MerchantID    string   `json:"merchant_id"`
	EventID       string   `json:"event_id"`
	Type          string   `json:"type"` // binary
	Question      string   `json:"question"`
	Options       []string `json:"options"`
	Status        string   `json:"status"` // active, suspended, closed, settled
	TotalVolume   float64  `json:"total_volume"`
	LiquidityPool float64  `json:"liquidity_pool"`
	// Fee rates are immutable market terms. They are kept internal until the
	// administrator fee-configuration API is available.
	MerchantFeeRate float64    `json:"-"`
	PlatformFeeRate float64    `json:"-"`
	CreatedAt       time.Time  `json:"created_at"`
	SettledAt       *time.Time `json:"settled_at,omitempty"`
}

// Order represents a prediction order
type Order struct {
	twill.AutoMarshal

	ID             string     `json:"id"`
	MerchantID     string     `json:"merchant_id"`
	UserID         string     `json:"user_id"`
	MarketID       string     `json:"market_id"`
	Type           string     `json:"type"` // buy, sell
	Option         string     `json:"option"`
	Amount         float64    `json:"amount"`
	FilledAmount   float64    `json:"filled_amount"`
	Currency       string     `json:"currency"`
	Price          float64    `json:"price"`
	TimeInForce    string     `json:"time_in_force"` // gtc, ioc
	Status         string     `json:"status"`        // pending, filled, partial, cancelled
	IdempotencyKey string     `json:"-"`
	CreatedAt      time.Time  `json:"created_at"`
	FilledAt       *time.Time `json:"filled_at,omitempty"`
}

// Wallet represents a virtual credit wallet (Play Money)
type Wallet struct {
	twill.AutoMarshal

	ID            string    `json:"id"`
	MerchantID    string    `json:"merchant_id"`
	UserID        string    `json:"user_id"`
	Currency      string    `json:"currency"`
	Balance       float64   `json:"balance"`
	LockedBalance float64   `json:"locked_balance"` // Locked for pending orders
	UpdatedAt     time.Time `json:"updated_at"`
}

// Transaction represents a wallet transaction
type Transaction struct {
	twill.AutoMarshal

	ID             string    `json:"id"`
	WalletID       string    `json:"wallet_id"`
	Type           string    `json:"type"` // credit, debit, bet, win, fee
	Amount         float64   `json:"amount"`
	Currency       string    `json:"currency"`
	RelatedOrderID *string   `json:"related_order_id,omitempty"`
	IdempotencyKey string    `json:"-"`
	Status         string    `json:"status"` // pending, completed, failed
	CreatedAt      time.Time `json:"created_at"`
}

// ExchangeRate represents a currency exchange rate
type ExchangeRate struct {
	twill.AutoMarshal

	ID           string    `json:"id"`
	FromCurrency string    `json:"from_currency"`
	ToCurrency   string    `json:"to_currency"`
	Rate         float64   `json:"rate"`
	Provider     string    `json:"provider"`
	Timestamp    time.Time `json:"timestamp"`
}

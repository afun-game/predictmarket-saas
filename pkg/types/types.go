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
	APIKeyPrefix                string     `json:"-"`
	APISecret                   string     `json:"-"` // Never expose in JSON.
	APISecretEncrypted          string     `json:"-"` // AES-256-GCM ciphertext for V3 HMAC verification.
	APISecretSecondaryEncrypted string     `json:"-"` // Previous V3 HMAC secret during a bounded rotation window.
	APISecretSecondaryExpiresAt *time.Time `json:"-"`
	Status                      string     `json:"status"`   // active, suspended, inactive
	Currency                    string     `json:"currency"` // USD, EUR, CNY, etc.
	Timezone                    string     `json:"timezone"` // IANA timezone
	WalletMode                  string     `json:"wallet_mode"`
	CallbackURL                 string     `json:"-"`
	CallbackSecret              string     `json:"-"` // Cleartext only when issued.
	CallbackSecretEncrypted     string     `json:"-"`
	WebhookURL                  string     `json:"-"`
	WebhookEvents               []string   `json:"-"`
	AllowedIPs                  []string   `json:"-"`
	// FeeRate mirrors the legacy column for persistence compatibility. It is
	// fixed at zero until administrator fee configuration is available.
	FeeRate float64 `json:"-"`
	// SeamlessDegraded is set by the callback dispatcher after repeated merchant
	// callback failures; seamless order placement is refused while set.
	SeamlessDegraded bool `json:"-"`
	// CallbackVerifiedAt records when an administrator proved callback URL
	// ownership by echoing a signed verification challenge.
	CallbackVerifiedAt *time.Time `json:"-"`
	// Risk limits are decimal strings in the merchant currency; an empty
	// string means the column is NULL and no limit is enforced. They are
	// read by order and parimutuel bet placement.
	MaxBetAmount      string    `json:"max_bet_amount,omitempty"`
	MaxUserExposure   string    `json:"max_user_exposure,omitempty"`
	MaxMarketExposure string    `json:"max_market_exposure,omitempty"`
	CreatedAt         time.Time `json:"created_at"`
	UpdatedAt         time.Time `json:"updated_at"`
}

// Event represents a prediction event
type Event struct {
	twill.AutoMarshal

	ID             string    `json:"id"`
	SourceType     string    `json:"source_type"` // polymarket, lmb, custom
	SourceID       string    `json:"source_id"`
	Title          string    `json:"title"`
	Description    string    `json:"description"`
	Category       string    `json:"category"` // politics, sports, crypto, entertainment
	EndTime        time.Time `json:"end_time"`
	ResolutionTime time.Time `json:"resolution_time"`
	Status         string    `json:"status"` // pending, active, closed, resolved
	Outcome        *string   `json:"outcome,omitempty"`
	// Translations holds the event title/description in additional locales,
	// keyed by BCP-47 language tag (e.g. "en", "zh-CN"). The default-locale
	// title/description live in Title/Description.
	Translations map[string]EventTranslation `json:"translations,omitempty"`
	CreatedAt    time.Time                   `json:"created_at"`
	UpdatedAt    time.Time                   `json:"updated_at"`
}

// EventTranslation is an event's title and description in one locale.
type EventTranslation struct {
	twill.AutoMarshal

	Title       string `json:"title"`
	Description string `json:"description,omitempty"`
}

// Market represents a prediction market
type Market struct {
	twill.AutoMarshal

	ID            string   `json:"id"`
	MerchantID    string   `json:"merchant_id"`
	EventID       string   `json:"event_id"`
	Type          string   `json:"type"` // binary
	Category      string   `json:"category"`
	Question      string   `json:"question"`
	Options       []string `json:"options"`
	Status        string   `json:"status"` // active, suspended, closed, settled
	TotalVolume   float64  `json:"total_volume"`
	LiquidityPool float64  `json:"liquidity_pool"`
	// ResolutionTime is the market's settlement time; creation defaults it
	// to the owning event's resolution time.
	ResolutionTime *time.Time `json:"resolution_time,omitempty"`
	// Fee rates are immutable market terms configured at creation time.
	// They are fractions of gross payout withheld at settlement (e.g.
	// 0.005 means 0.5%).
	MerchantFeeRate float64 `json:"merchant_fee_rate,omitempty"`
	PlatformFeeRate float64 `json:"platform_fee_rate,omitempty"`
	// Translations holds the market question and options in additional
	// locales, keyed by BCP-47 language tag (e.g. "en", "zh-CN"). The
	// default-locale question/options live in Question/Options.
	Translations map[string]MarketTranslation `json:"translations,omitempty"`
	CreatedAt    time.Time                    `json:"created_at"`
	SettledAt    *time.Time                   `json:"settled_at,omitempty"`
}

// MarketTranslation is a market's question and options in one locale.
type MarketTranslation struct {
	twill.AutoMarshal

	Question string   `json:"question"`
	Options  []string `json:"options"`
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
	WalletKind     string     `json:"-"`
	Channel        string     `json:"-"`
	Status         string     `json:"status"` // pending, filled, partial, cancelled
	IdempotencyKey string     `json:"-"`
	CreatedAt      time.Time  `json:"created_at"`
	FilledAt       *time.Time `json:"filled_at,omitempty"`
}

// Trade represents one persisted execution between a maker and taker order.
type Trade struct {
	twill.AutoMarshal

	ID                 string    `json:"id"`
	MarketID           string    `json:"market_id"`
	Option             string    `json:"option"`
	Currency           string    `json:"currency"`
	MakerOrderID       string    `json:"maker_order_id"`
	MakerUserID        string    `json:"maker_user_id"`
	MakerType          string    `json:"maker_type"`
	MakerTradeAmount   string    `json:"maker_trade_amount"`
	TakerOrderID       string    `json:"taker_order_id"`
	TakerUserID        string    `json:"taker_user_id"`
	TakerType          string    `json:"taker_type"`
	TakerTradeAmount   string    `json:"taker_trade_amount"`
	Shares             float64   `json:"shares"`
	MatchedPrice       float64   `json:"matched_price"`
	ImpliedDecimalOdds float64   `json:"implied_decimal_odds"`
	CreatedAt          time.Time `json:"created_at"`
}

// Wallet represents a virtual credit wallet (Play Money)
type Wallet struct {
	twill.AutoMarshal

	ID            string    `json:"id"`
	MerchantID    string    `json:"merchant_id"`
	UserID        string    `json:"user_id"`
	Currency      string    `json:"currency"`
	Kind          string    `json:"kind"`
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

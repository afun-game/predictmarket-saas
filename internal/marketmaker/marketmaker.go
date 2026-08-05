// Package marketmaker turns a binary market's liquidity_pool into real
// market-making capital: it funds a dedicated platform wallet once per market
// and keeps two-sided limit quotes at a configured spread around the book
// mid. The maker quotes YES on both sides (buy YES = bid, sell YES = ask),
// never crosses the book, and stops near the event resolution time.
package marketmaker

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"os"
	"runtime/debug"
	"strconv"
	"strings"
	"time"

	"github.com/afun-game/predictmarket-saas/internal/event"
	"github.com/afun-game/predictmarket-saas/internal/market"
	"github.com/afun-game/predictmarket-saas/internal/merchant"
	"github.com/afun-game/predictmarket-saas/internal/order"
	"github.com/afun-game/predictmarket-saas/internal/wallet"
	"github.com/afun-game/predictmarket-saas/pkg/types"
	"github.com/nxsky/twill"
)

const (
	// MakerUserID is the reserved platform identity for market-making orders.
	MakerUserID = "__liquidity__"

	defaultSchedule      = "@every 10s"
	defaultHalfSpread    = 0.05
	defaultLevelFraction = 0.25
	defaultMinFunds      = 100.0
	defaultStopBefore    = 5 * time.Minute
	defaultFundingTxType = "liquidity"
	minimumLevelSize     = 1.0
	maximumLevelSize     = 100000.0
	jobName              = "market-maker"
)

// Service executes one market-making pass over active binary markets.
type Service interface {
	Tick(ctx context.Context) (int, error)
}

// Repository tracks the platform's committed funds per market.
type Repository interface {
	// GetCommitted returns the amount already funded for the market.
	GetCommitted(ctx context.Context, marketID string) (float64, error)
	// SetCommitted records the newly funded amount.
	SetCommitted(ctx context.Context, marketID string, committed float64) error
}

type implementation struct {
	twill.Implements[Service]

	database twill.Database `twill:"primary-db"`
	cron     twill.Cron     `twill:"market-maker"`

	ordersRef    twill.Ref[order.Service]
	walletsRef   twill.Ref[wallet.Service]
	merchantsRef twill.Ref[merchant.Service]
	marketsRef   twill.Ref[market.Service]
	eventsRef    twill.Ref[event.Service]

	orders    order.Service
	wallets   wallet.Service
	merchants merchant.Service
	markets   market.Service
	events    event.Service

	repository Repository

	schedule      string
	halfSpread    float64
	levelFraction float64
	minFunds      float64
	stopBefore    time.Duration
	enabled       bool
	now           func() time.Time
}

// NewService creates a market maker for tests with explicit dependencies.
func NewService(
	orders order.Service,
	wallets wallet.Service,
	merchants merchant.Service,
	markets market.Service,
	events event.Service,
	repository Repository,
) Service {
	return newService(orders, wallets, merchants, markets, events, repository)
}

func newService(
	orders order.Service,
	wallets wallet.Service,
	merchants merchant.Service,
	markets market.Service,
	events event.Service,
	repository Repository,
) *implementation {
	return &implementation{
		orders:        orders,
		wallets:       wallets,
		merchants:     merchants,
		markets:       markets,
		events:        events,
		repository:    repository,
		schedule:      defaultSchedule,
		halfSpread:    defaultHalfSpread,
		levelFraction: defaultLevelFraction,
		minFunds:      defaultMinFunds,
		stopBefore:    defaultStopBefore,
		enabled:       true,
		now:           time.Now,
	}
}

// Init resolves services and registers the cron job.
func (s *implementation) Init(ctx context.Context) error {
	s.loadConfiguration()
	if s.orders == nil {
		s.orders = s.ordersRef.Get()
	}
	if s.wallets == nil {
		s.wallets = s.walletsRef.Get()
	}
	if s.merchants == nil {
		s.merchants = s.merchantsRef.Get()
	}
	if s.markets == nil {
		s.markets = s.marketsRef.Get()
	}
	if s.events == nil {
		s.events = s.eventsRef.Get()
	}
	if s.repository == nil {
		database := s.database.Get()
		if database == nil || database.StdDB() == nil {
			return errors.New("primary database is not configured")
		}
		s.repository = NewPostgresRepository(database.StdDB())
	}
	if s.orders == nil || s.wallets == nil || s.merchants == nil || s.markets == nil || s.events == nil || s.repository == nil {
		return errors.New("market maker dependencies are not configured")
	}
	if !s.enabled {
		slog.Info("market maker is disabled")
		return nil
	}
	scheduler := s.cron.Get()
	if scheduler == nil {
		return errors.New("market maker cron is not configured")
	}
	if err := scheduler.Add(ctx, jobName, s.schedule, func(jobCtx context.Context) {
		s.runSafely(jobCtx, func(ctx context.Context) {
			if _, err := s.Tick(ctx); err != nil {
				slog.Error("market maker tick failed", "error", err)
			}
		})
	}); err != nil {
		return fmt.Errorf("register market maker job: %w", err)
	}
	return nil
}

func (s *implementation) loadConfiguration() {
	// Twill constructs the service as a zero value; defaults must be applied
	// before the environment overrides.
	s.enabled = true
	if s.schedule == "" {
		s.schedule = defaultSchedule
	}
	if s.halfSpread <= 0 {
		s.halfSpread = defaultHalfSpread
	}
	if s.levelFraction <= 0 {
		s.levelFraction = defaultLevelFraction
	}
	if s.minFunds < 0 {
		s.minFunds = defaultMinFunds
	}
	if s.stopBefore <= 0 {
		s.stopBefore = defaultStopBefore
	}
	if s.now == nil {
		s.now = time.Now
	}
	s.schedule = environmentOrDefault("MARKET_MAKER_SCHEDULE", s.schedule)
	if value := environmentFloat("MARKET_MAKER_HALF_SPREAD", s.halfSpread); value > 0 && value < 0.5 {
		s.halfSpread = value
	}
	if value := environmentFloat("MARKET_MAKER_LEVEL_FRACTION", s.levelFraction); value > 0 && value <= 1 {
		s.levelFraction = value
	}
	if value := environmentFloat("MARKET_MAKER_MIN_FUNDS", s.minFunds); value >= 0 {
		s.minFunds = value
	}
	if value := environmentFloat("MARKET_MAKER_STOP_BEFORE_MINUTES", s.stopBefore.Minutes()); value >= 0 {
		s.stopBefore = time.Duration(value * float64(time.Minute))
	}
	if raw := strings.TrimSpace(os.Getenv("MARKET_MAKER_ENABLED")); raw != "" {
		s.enabled = raw != "false" && raw != "0"
	}
}

// Tick runs one market-making pass and returns the number of actions taken.
func (s *implementation) Tick(ctx context.Context) (int, error) {
	if !s.enabled {
		return 0, nil
	}
	actions := 0
	page := 1
	for {
		markets, total, err := s.markets.List(ctx, &market.ListFilters{Status: "active", Page: page, Limit: 100})
		if err != nil {
			return actions, fmt.Errorf("list markets for market making: %w", err)
		}
		for _, marketValue := range markets {
			if marketValue.Type != "binary" {
				continue
			}
			taken, err := s.makeMarket(ctx, marketValue)
			if err != nil {
				slog.Warn("market maker skipped market", "market_id", marketValue.ID, "error", err)
				continue
			}
			actions += taken
		}
		if page*100 >= total {
			break
		}
		page++
	}
	return actions, nil
}

func (s *implementation) makeMarket(ctx context.Context, marketValue *types.Market) (int, error) {
	if marketValue.LiquidityPool <= 0 {
		return 0, nil
	}
	merchantValue, err := s.merchants.Get(ctx, marketValue.MerchantID)
	if err != nil {
		return 0, fmt.Errorf("get market maker merchant: %w", err)
	}
	currency := merchantValue.Currency
	if currency == "" {
		currency = "USD"
	}

	// Fund the maker wallet with the committed amount exactly once.
	committed, err := s.repository.GetCommitted(ctx, marketValue.ID)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return 0, fmt.Errorf("get committed funds: %w", err)
	}
	if marketValue.LiquidityPool > committed {
		topUp := marketValue.LiquidityPool - committed
		if _, err := s.wallets.Create(ctx, marketValue.MerchantID, MakerUserID, currency); err != nil && !errors.Is(err, wallet.ErrAlreadyExists) {
			// The maker wallet is shared per merchant+currency across every
			// binary market; an existing wallet is the normal case for a
			// second market, not a failure.
			return 0, fmt.Errorf("create market maker wallet: %w", err)
		}
		if err := s.wallets.Credit(ctx, marketValue.MerchantID, MakerUserID, currency, topUp, defaultFundingTxType); err != nil {
			return 0, fmt.Errorf("fund market maker wallet: %w", err)
		}
		if err := s.repository.SetCommitted(ctx, marketValue.ID, marketValue.LiquidityPool); err != nil {
			return 0, fmt.Errorf("record committed funds: %w", err)
		}
	}

	// Stop quoting near the resolution time.
	eventValue, err := s.events.Get(ctx, marketValue.EventID)
	if err != nil {
		return 0, fmt.Errorf("get maker event: %w", err)
	}
	if eventValue.Status != "active" {
		return 0, nil
	}
	if s.now().UTC().Add(s.stopBefore).After(eventValue.ResolutionTime) {
		return 0, nil
	}

	available, _, err := s.wallets.GetBalance(ctx, marketValue.MerchantID, MakerUserID, currency)
	if err != nil {
		return 0, fmt.Errorf("get maker balance: %w", err)
	}
	if available < s.minFunds {
		return 0, nil
	}

	book, err := s.orders.GetOrderBook(ctx, marketValue.ID)
	if err != nil {
		return 0, fmt.Errorf("get maker order book: %w", err)
	}
	bestBid, bestAsk, hasBid, hasAsk := yesEquivalentLevels(book)
	mid := 0.5
	if hasBid && hasAsk {
		mid = (bestBid + bestAsk) / 2
	} else if hasBid {
		mid = bestBid + s.halfSpread
	} else if hasAsk {
		mid = bestAsk - s.halfSpread
	}
	bidLevel := roundPrice(mid - s.halfSpread)
	askLevel := roundPrice(mid + s.halfSpread)
	if bidLevel < 0.01 || askLevel > 0.99 {
		return 0, nil
	}
	// Never cross the book.
	if hasAsk && bidLevel >= bestAsk {
		return 0, nil
	}
	if hasBid && askLevel <= bestBid {
		return 0, nil
	}

	levelSize := math.Min(math.Max(available*s.levelFraction, minimumLevelSize), maximumLevelSize)
	existing, _, err := s.orders.List(ctx, &order.ListFilters{MerchantID: marketValue.MerchantID, MarketID: marketValue.ID, Status: "pending", Page: 1, Limit: 100})
	if err != nil {
		return 0, fmt.Errorf("list maker orders: %w", err)
	}

	actions := 0
	quotes := []order.CreateRequest{
		{MerchantID: marketValue.MerchantID, UserID: MakerUserID, MarketID: marketValue.ID, Type: "buy", Option: "Yes", Amount: levelSize, Currency: currency, Price: bidLevel, TimeInForce: "gtc", Channel: "mm"},
		{MerchantID: marketValue.MerchantID, UserID: MakerUserID, MarketID: marketValue.ID, Type: "sell", Option: "Yes", Amount: levelSize, Currency: currency, Price: askLevel, TimeInForce: "gtc", Channel: "mm"},
	}
	for _, quote := range quotes {
		if hasMakerQuote(existing, quote.Type, quote.Price) {
			continue
		}
		if _, err := s.orders.Create(ctx, &quote); err != nil {
			slog.Warn("market maker quote failed", "market_id", marketValue.ID, "side", quote.Type, "price", quote.Price, "error", err)
			continue
		}
		actions++
	}
	return actions, nil
}

// hasMakerQuote reports whether the maker already rests at the level.
func hasMakerQuote(existing []*types.Order, side string, price float64) bool {
	for _, value := range existing {
		if value.UserID != MakerUserID || value.Type != side || value.Status == "cancelled" {
			continue
		}
		if value.Price == price {
			return true
		}
	}
	return false
}

// yesEquivalentLevels folds both options' books into YES-side best bid/ask.
// A NO ask at p is a YES bid at 1-p; a NO bid at p is a YES ask at 1-p.
func yesEquivalentLevels(book *market.OrderBook) (bestBid, bestAsk float64, hasBid, hasAsk bool) {
	bestBid, bestAsk = 0.0, 1.0
	for _, entry := range book.Bids {
		if entry.Option == "No" {
			// Buying NO is selling YES.
			if price := 1 - entry.Price; price < bestAsk {
				bestAsk = price
				hasAsk = true
			}
			continue
		}
		if entry.Price > bestBid {
			bestBid = entry.Price
			hasBid = true
		}
	}
	for _, entry := range book.Asks {
		if entry.Option == "No" {
			// Selling NO is buying YES.
			if price := 1 - entry.Price; price > bestBid {
				bestBid = price
				hasBid = true
			}
			continue
		}
		if entry.Price < bestAsk {
			bestAsk = entry.Price
			hasAsk = true
		}
	}
	return bestBid, bestAsk, hasBid, hasAsk
}

func roundPrice(value float64) float64 {
	return math.Round(value*100) / 100
}

func (s *implementation) runSafely(ctx context.Context, run func(context.Context)) {
	defer func() {
		if recovered := recover(); recovered != nil {
			slog.Error("market maker panic", "panic", recovered, "stack", string(debug.Stack()))
		}
	}()
	run(ctx)
}

func environmentOrDefault(name, fallback string) string {
	if raw := strings.TrimSpace(os.Getenv(name)); raw != "" {
		return raw
	}
	return fallback
}

func environmentFloat(name string, fallback float64) float64 {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback
	}
	value, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return fallback
	}
	return value
}

// ErrNotFound reports missing committed-fund records.
var ErrNotFound = errors.New("committed funds were not found")

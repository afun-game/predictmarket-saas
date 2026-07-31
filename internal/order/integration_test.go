package order

import (
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/afun-game/predictmarket-saas/internal/market"
	"github.com/afun-game/predictmarket-saas/internal/wallet"
	"github.com/afun-game/predictmarket-saas/pkg/types"
	_ "github.com/jackc/pgx/v5/stdlib"
)

const defaultIntegrationDatabaseURL = "postgres://predictmarket:password@localhost:5432/predictmarket?sslmode=disable"

func TestOrderPostgresIntegration(t *testing.T) {
	if os.Getenv("INTEGRATION_TEST") != "1" {
		t.Skip("set INTEGRATION_TEST=1 to run PostgreSQL integration tests")
	}
	fixture := newOrderIntegrationFixture(t)
	ctx := context.Background()

	firstMaker := fixture.createOrder(t, "seller-one", "sell", 20, 0.60, "gtc")
	bestMaker := fixture.createOrder(t, "seller-two", "sell", 30, 0.50, "gtc")
	incoming := fixture.createOrder(t, "buyer", "buy", 40, 0.65, "gtc")
	if incoming.Status != "filled" || incoming.FilledAmount != 40 {
		t.Fatalf("incoming order = %#v", incoming)
	}
	bestMaker = fixture.getOrder(t, bestMaker.ID)
	firstMaker = fixture.getOrder(t, firstMaker.ID)
	if bestMaker.Status != "filled" || firstMaker.Status != "partial" {
		t.Errorf("makers: best=%#v first=%#v", bestMaker, firstMaker)
	}
	if err := fixture.service.Cancel(ctx, firstMaker.ID); err != nil {
		t.Fatalf("Cancel(partial) error = %v", err)
	}
	ioc := fixture.createOrder(t, "ioc-buyer", "buy", 10, 0.40, "ioc")
	if ioc.Status != "cancelled" || ioc.FilledAmount != 0 {
		t.Errorf("IOC order = %#v", ioc)
	}
	fixture.assertBalance(t, "seller-one", 96, 4)
	fixture.assertBalance(t, "seller-two", 85, 15)
	fixture.assertBalance(t, "buyer", 79, 21)
	fixture.assertBalance(t, "ioc-buyer", 100, 0)

	book, err := fixture.service.GetOrderBook(ctx, fixture.marketID)
	if err != nil {
		t.Fatalf("GetOrderBook() error = %v", err)
	}
	if len(book.Bids) != 0 || len(book.Asks) != 0 {
		t.Errorf("order book after cancellation = %#v", book)
	}
	fixture.assertConcurrentCross(t)

	var volume float64
	if err := fixture.database.QueryRowContext(
		ctx,
		"SELECT total_volume FROM markets WHERE id = $1",
		fixture.marketID,
	).Scan(&volume); err != nil {
		t.Fatalf("query market volume: %v", err)
	}
	if volume != 50 {
		t.Errorf("market volume = %v, want 50", volume)
	}
	_, total, err := fixture.service.ListByMarket(ctx, fixture.marketID, 1, 100)
	if err != nil {
		t.Fatalf("ListByMarket() error = %v", err)
	}
	if total != 6 {
		t.Errorf("order total = %d, want 6", total)
	}
	var tradeCount int
	if err := fixture.database.QueryRowContext(
		ctx,
		"SELECT COUNT(*) FROM trades WHERE market_id = $1",
		fixture.marketID,
	).Scan(&tradeCount); err != nil {
		t.Fatalf("count trades: %v", err)
	}
	if tradeCount != 3 {
		t.Errorf("trade count = %d, want 3", tradeCount)
	}
	trades, err := fixture.service.ListTrades(ctx, &TradeListFilters{
		MerchantID: fixture.merchantID,
		Limit:      2,
	})
	if err != nil {
		t.Fatalf("ListTrades() error = %v", err)
	}
	if len(trades.Trades) != 2 || trades.NextCursor == "" {
		t.Errorf("first trade cursor page = %#v", trades)
	}
	secondTradePage, err := fixture.service.ListTrades(ctx, &TradeListFilters{
		MerchantID: fixture.merchantID,
		Cursor:     trades.NextCursor,
		Limit:      2,
	})
	if err != nil {
		t.Fatalf("ListTrades(second page) error = %v", err)
	}
	if len(secondTradePage.Trades) != 1 || secondTradePage.NextCursor != "" {
		t.Errorf("second trade cursor page = %#v", secondTradePage)
	}
	request := &CreateRequest{
		MerchantID:     fixture.merchantID,
		UserID:         "idempotent-user",
		MarketID:       fixture.marketID,
		Type:           "buy",
		Option:         "Yes",
		Amount:         10,
		Currency:       "USD",
		Price:          0.10,
		TimeInForce:    "gtc",
		IdempotencyKey: "order-postgres-retry-key",
	}
	first, err := fixture.service.Create(ctx, request)
	if err != nil {
		t.Fatalf("first idempotent Create() error = %v", err)
	}
	second, err := fixture.service.Create(ctx, request)
	if err != nil {
		t.Fatalf("second idempotent Create() error = %v", err)
	}
	if first.ID != second.ID {
		t.Errorf("idempotent order IDs = (%q, %q)", first.ID, second.ID)
	}
	fixture.assertBalance(t, "idempotent-user", 99, 1)
	_, total, err = fixture.service.ListByMarket(ctx, fixture.marketID, 1, 100)
	if err != nil {
		t.Fatalf("ListByMarket(idempotent) error = %v", err)
	}
	if total != 7 {
		t.Errorf("order total after idempotent retry = %d, want 7", total)
	}
}

func TestOrderPostgresPlacementFailureDoesNotLeaveLockedFunds(t *testing.T) {
	if os.Getenv("INTEGRATION_TEST") != "1" {
		t.Skip("set INTEGRATION_TEST=1 to run PostgreSQL integration tests")
	}
	fixture := newOrderIntegrationFixture(t)
	orderID := integrationUUID(t)
	if _, err := fixture.database.ExecContext(context.Background(), `
INSERT INTO orders (
    id, merchant_id, user_id, market_id, type, option, amount, filled_amount,
    currency, price, time_in_force, status
) VALUES ($1, $2, 'atomic-failure', $3, 'buy', 'Yes', 10, 0, 'USD', 0.5, 'gtc', 'pending')`,
		orderID,
		fixture.merchantID,
		fixture.marketID,
	); err != nil {
		t.Fatalf("insert conflicting order: %v", err)
	}
	fixture.service.random = bytes.NewReader(uuidBytes(t, orderID))

	_, err := fixture.service.Create(context.Background(), &CreateRequest{
		MerchantID:  fixture.merchantID,
		UserID:      "atomic-failure",
		MarketID:    fixture.marketID,
		Type:        "buy",
		Option:      "Yes",
		Amount:      10,
		Currency:    "USD",
		Price:       0.5,
		TimeInForce: "gtc",
	})
	if !errors.Is(err, ErrAlreadyExists) {
		t.Fatalf("Create() error = %v, want ErrAlreadyExists", err)
	}
	fixture.assertBalance(t, "atomic-failure", 100, 0)
}

type orderIntegrationFixture struct {
	database   *sql.DB
	service    *implementation
	marketID   string
	merchantID string
	eventID    string
	suffix     string
}

func newOrderIntegrationFixture(t *testing.T) *orderIntegrationFixture {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		databaseURL = defaultIntegrationDatabaseURL
	}
	database, err := sql.Open("pgx", databaseURL)
	if err != nil {
		t.Fatalf("open PostgreSQL: %v", err)
	}
	if err := database.PingContext(ctx); err != nil {
		_ = database.Close()
		t.Fatalf("ping PostgreSQL: %v", err)
	}
	merchantID := integrationUUID(t)
	eventID := integrationUUID(t)
	marketID := integrationUUID(t)
	marketValue := &types.Market{
		ID:         marketID,
		MerchantID: merchantID,
		EventID:    eventID,
		Type:       "binary",
		Question:   "Will PostgreSQL matching work?",
		Options:    []string{"Yes", "No"},
		Status:     "active",
		CreatedAt:  time.Now().UTC(),
	}
	fixture := &orderIntegrationFixture{
		database:   database,
		marketID:   marketID,
		merchantID: merchantID,
		eventID:    eventID,
		suffix:     fmt.Sprintf("%d", time.Now().UnixNano()),
	}
	fixture.service = newService(
		newPostgresRepository(database),
		&integrationMarketService{value: marketValue},
		&integrationWalletService{database: database},
	)
	t.Cleanup(fixture.cleanup)
	fixture.insertReferences(t, ctx)
	for _, userID := range []string{
		"seller-one", "seller-two", "buyer", "ioc-buyer", "concurrent-seller", "concurrent-buyer", "atomic-failure", "idempotent-user",
	} {
		fixture.insertWallet(t, ctx, userID)
	}
	return fixture
}

func (f *orderIntegrationFixture) insertReferences(t *testing.T, ctx context.Context) {
	t.Helper()
	_, err := f.database.ExecContext(
		ctx,
		`INSERT INTO merchants (
    id, name, email, api_key, api_key_prefix, api_secret, status, currency, timezone
) VALUES ($1, $2, $3, $4, LEFT('pk_' || gen_random_uuid()::text, 16), 'secret-hash', 'active', 'USD', 'UTC')`,
		f.merchantID,
		"Order integration merchant",
		"order-integration-"+f.suffix+"@example.com",
		"pk_order_integration_"+f.suffix,
	)
	if err != nil {
		t.Fatalf("insert merchant fixture: %v", err)
	}
	_, err = f.database.ExecContext(
		ctx,
		`INSERT INTO events (
    id, source_type, source_id, title, description, category,
    end_time, resolution_time, status
) VALUES ($1, 'custom', $2, 'Order integration event', '', 'integration', $3, $4, 'active')`,
		f.eventID,
		"order-integration-"+f.suffix,
		time.Now().UTC().Add(time.Hour),
		time.Now().UTC().Add(2*time.Hour),
	)
	if err != nil {
		t.Fatalf("insert event fixture: %v", err)
	}
	_, err = f.database.ExecContext(
		ctx,
		`INSERT INTO markets (
    id, merchant_id, event_id, type, question, options, status, created_at
) VALUES ($1, $2, $3, 'binary', 'Order integration market', '["Yes","No"]', 'active', $4)`,
		f.marketID,
		f.merchantID,
		f.eventID,
		time.Now().UTC(),
	)
	if err != nil {
		t.Fatalf("insert market fixture: %v", err)
	}
}

func (f *orderIntegrationFixture) insertWallet(t *testing.T, ctx context.Context, userID string) {
	t.Helper()
	_, err := f.database.ExecContext(
		ctx,
		`INSERT INTO wallets (
    id, merchant_id, user_id, currency, balance, locked_balance
) VALUES ($1, $2, $3, 'USD', 100, 0)`,
		integrationUUID(t),
		f.merchantID,
		userID,
	)
	if err != nil {
		t.Fatalf("insert wallet %q: %v", userID, err)
	}
}

func (f *orderIntegrationFixture) createOrder(
	t *testing.T,
	userID string,
	side string,
	amount float64,
	price float64,
	timeInForce string,
) *types.Order {
	t.Helper()
	value, err := f.service.Create(context.Background(), &CreateRequest{
		MerchantID:  f.merchantID,
		UserID:      userID,
		MarketID:    f.marketID,
		Type:        side,
		Option:      "Yes",
		Amount:      amount,
		Currency:    "USD",
		Price:       price,
		TimeInForce: timeInForce,
	})
	if err != nil {
		t.Fatalf("Create(%s) error = %v", userID, err)
	}
	return value
}

func (f *orderIntegrationFixture) getOrder(t *testing.T, orderID string) *types.Order {
	t.Helper()
	value, err := f.service.Get(context.Background(), orderID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	return value
}

func (f *orderIntegrationFixture) assertBalance(
	t *testing.T,
	userID string,
	available float64,
	locked float64,
) {
	t.Helper()
	var gotAvailable float64
	var gotLocked float64
	if err := f.database.QueryRowContext(
		context.Background(),
		`SELECT balance, locked_balance FROM wallets
WHERE merchant_id = $1 AND user_id = $2 AND currency = 'USD'`,
		f.merchantID,
		userID,
	).Scan(&gotAvailable, &gotLocked); err != nil {
		t.Fatalf("query wallet balance: %v", err)
	}
	if gotAvailable != available || gotLocked != locked {
		t.Errorf("%s balance = (%v, %v), want (%v, %v)", userID, gotAvailable, gotLocked, available, locked)
	}
}

func (f *orderIntegrationFixture) assertConcurrentCross(t *testing.T) {
	t.Helper()
	requests := []*CreateRequest{
		{
			MerchantID: f.merchantID, UserID: "concurrent-seller", MarketID: f.marketID,
			Type: "sell", Option: "Yes", Amount: 10, Currency: "USD", Price: 0.50,
		},
		{
			MerchantID: f.merchantID, UserID: "concurrent-buyer", MarketID: f.marketID,
			Type: "buy", Option: "Yes", Amount: 10, Currency: "USD", Price: 0.50,
		},
	}
	results := make(chan *types.Order, len(requests))
	errorsChannel := make(chan error, len(requests))
	var waitGroup sync.WaitGroup
	for _, request := range requests {
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()
			value, err := f.service.Create(context.Background(), request)
			if err != nil {
				errorsChannel <- err
				return
			}
			results <- value
		}()
	}
	waitGroup.Wait()
	close(results)
	close(errorsChannel)
	for err := range errorsChannel {
		t.Errorf("concurrent Create() error = %v", err)
	}
	for value := range results {
		stored := f.getOrder(t, value.ID)
		if stored.Status != "filled" || stored.FilledAmount != 10 {
			t.Errorf("concurrent order = %#v", stored)
		}
	}
}

func (f *orderIntegrationFixture) cleanup() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, _ = f.database.ExecContext(ctx, "DELETE FROM trades WHERE market_id = $1", f.marketID)
	_, _ = f.database.ExecContext(ctx, "DELETE FROM orders WHERE merchant_id = $1", f.merchantID)
	_, _ = f.database.ExecContext(ctx, "DELETE FROM wallets WHERE merchant_id = $1", f.merchantID)
	_, _ = f.database.ExecContext(ctx, "DELETE FROM markets WHERE id = $1", f.marketID)
	_, _ = f.database.ExecContext(ctx, "DELETE FROM events WHERE id = $1", f.eventID)
	_, _ = f.database.ExecContext(ctx, "DELETE FROM merchants WHERE id = $1", f.merchantID)
	_ = f.database.Close()
}

func integrationUUID(t *testing.T) string {
	t.Helper()
	value, err := generateUUID(rand.Reader)
	if err != nil {
		t.Fatalf("generate integration UUID: %v", err)
	}
	return value
}

func uuidBytes(t *testing.T, value string) []byte {
	t.Helper()
	encoded, err := hex.DecodeString(strings.ReplaceAll(value, "-", ""))
	if err != nil {
		t.Fatalf("decode UUID %q: %v", value, err)
	}
	return encoded
}

type integrationMarketService struct {
	value *types.Market
}

func (s *integrationMarketService) Create(context.Context, *market.CreateRequest) (*types.Market, error) {
	return nil, errors.New("not implemented in integration adapter")
}
func (s *integrationMarketService) Get(context.Context, string) (*types.Market, error) {
	clone := *s.value
	clone.Options = append([]string{}, s.value.Options...)
	return &clone, nil
}
func (s *integrationMarketService) List(context.Context, *market.ListFilters) ([]*types.Market, int, error) {
	return nil, 0, errors.New("not implemented in integration adapter")
}
func (s *integrationMarketService) GetOrderBook(context.Context, string) (*market.OrderBook, error) {
	return nil, errors.New("not implemented in integration adapter")
}
func (s *integrationMarketService) UpdateStatus(context.Context, string, string) error {
	return errors.New("not implemented in integration adapter")
}
func (s *integrationMarketService) AddLiquidity(context.Context, string, float64) error {
	return errors.New("not implemented in integration adapter")
}

type integrationWalletService struct {
	database *sql.DB
}

func (s *integrationWalletService) Lock(
	ctx context.Context,
	merchantID string,
	userID string,
	currency string,
	amount float64,
) error {
	result, err := s.database.ExecContext(
		ctx,
		`UPDATE wallets
SET balance = balance - $4, locked_balance = locked_balance + $4
WHERE merchant_id = $1 AND user_id = $2 AND currency = $3 AND balance >= $4`,
		merchantID, userID, currency, amount,
	)
	if err != nil {
		return err
	}
	rows, _ := result.RowsAffected()
	if rows == 0 {
		return errors.New("insufficient balance")
	}
	return nil
}

func (s *integrationWalletService) Unlock(
	ctx context.Context,
	merchantID string,
	userID string,
	currency string,
	amount float64,
) error {
	_, err := s.database.ExecContext(
		ctx,
		`UPDATE wallets
SET balance = balance + $4, locked_balance = locked_balance - $4
WHERE merchant_id = $1 AND user_id = $2 AND currency = $3 AND locked_balance >= $4`,
		merchantID, userID, currency, amount,
	)
	return err
}

func (s *integrationWalletService) Create(context.Context, string, string, string) (*types.Wallet, error) {
	return nil, errors.New("not implemented in integration adapter")
}
func (s *integrationWalletService) Get(context.Context, string, string, string) (*types.Wallet, error) {
	return nil, errors.New("not implemented in integration adapter")
}
func (s *integrationWalletService) GetBalance(context.Context, string, string, string) (float64, float64, error) {
	return 0, 0, errors.New("not implemented in integration adapter")
}
func (s *integrationWalletService) Credit(context.Context, string, string, string, float64, string) error {
	return errors.New("not implemented in integration adapter")
}
func (s *integrationWalletService) CreditWithIdempotency(context.Context, string, string, string, float64, string, string) error {
	return errors.New("not implemented in integration adapter")
}
func (s *integrationWalletService) Deposit(context.Context, *wallet.TransferRequest) (*wallet.Transfer, error) {
	return nil, errors.New("not implemented in integration adapter")
}
func (s *integrationWalletService) Withdraw(context.Context, *wallet.TransferRequest) (*wallet.Transfer, error) {
	return nil, errors.New("not implemented in integration adapter")
}
func (s *integrationWalletService) GetTransfer(context.Context, string, string) (*wallet.Transfer, error) {
	return nil, errors.New("not implemented in integration adapter")
}
func (s *integrationWalletService) Debit(context.Context, string, string, string, float64, string) error {
	return errors.New("not implemented in integration adapter")
}
func (s *integrationWalletService) ListTransactions(context.Context, string, string, int, int) ([]*types.Transaction, int, error) {
	return nil, 0, errors.New("not implemented in integration adapter")
}

package callback

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/afun-game/predictmarket-saas/internal/order"
	"github.com/afun-game/predictmarket-saas/internal/parimutuel"
	"github.com/afun-game/predictmarket-saas/pkg/fixed"
	"github.com/afun-game/predictmarket-saas/pkg/types"
)

// Seamless errors classify merchant wallet outcomes for HTTP mapping.
var (
	ErrInsufficientFunds = errors.New("merchant reported insufficient funds")
	ErrUserNotFound      = errors.New("merchant reported user not found")
	ErrUserBlocked       = errors.New("merchant reported user blocked")
	ErrDebitUnknown      = errors.New("merchant debit outcome is unknown")
	ErrSeamlessDisabled  = errors.New("seamless wallet mode is not configured")
	// ErrMerchantDegraded rejects new orders while a merchant's callbacks are
	// repeatedly failing (V3 §10.1 circuit breaker).
	ErrMerchantDegraded = errors.New("merchant wallet is degraded after repeated callback failures")
	// ErrCallbackUnverified rejects seamless orders until an administrator
	// proves ownership of the merchant callback URL (V3 §7.2).
	ErrCallbackUnverified = errors.New("merchant callback URL has not been verified")
)

// BalanceError preserves a merchant-authoritative balance on a rejected debit.
type BalanceError struct {
	err     error
	balance string
}

func (e *BalanceError) Error() string { return e.err.Error() }
func (e *BalanceError) Unwrap() error { return e.err }

// BalanceFromError returns the balance supplied with a terminal merchant error.
func BalanceFromError(err error) (string, bool) {
	var balanceErr *BalanceError
	if !errors.As(err, &balanceErr) || balanceErr.balance == "" {
		return "", false
	}
	return balanceErr.balance, true
}

func withBalance(err error, balance string) error {
	if balance == "" {
		return err
	}
	return &BalanceError{err: err, balance: balance}
}

// SeamlessCoordinator places shadow-funded orders after a synchronous merchant debit.
type SeamlessCoordinator struct {
	database  *sql.DB
	placer    *order.SeamlessPlacer
	client    *Client
	protector secretDecryptor
	worker    rollbackEnqueuer
	bets      parimutuel.Service
	now       func() time.Time
	random    io.Reader
}

type secretDecryptor interface {
	Decrypt(ciphertext string) (string, error)
}

type rollbackEnqueuer interface {
	EnqueueRollback(
		ctx context.Context,
		merchantID string,
		userID string,
		currency string,
		amountCents int64,
		transactionID string,
		orderID string,
		marketID string,
	) error
}

// seamlessDebit is the synchronous merchant debit context shared by the
// order and parimutuel paths. Parimutuel bets have no order row, so orderID
// stays empty and the transaction_id is the authoritative idempotency key.
type seamlessDebit struct {
	MerchantID  string
	UserID      string
	Currency    string
	AmountCents int64
	OrderID     string
	MarketID    string
}

// NewSeamlessCoordinator wires the synchronous debit + place path. The
// parimutuel service powers seamless pool betting (PlaceBetWithBalance).
func NewSeamlessCoordinator(
	database *sql.DB,
	protector secretDecryptor,
	worker rollbackEnqueuer,
	allowPrivateURLs bool,
	bets parimutuel.Service,
) (*SeamlessCoordinator, error) {
	if database == nil {
		return nil, errors.New("seamless coordinator database is not configured")
	}
	if protector == nil {
		return nil, errors.New("seamless coordinator secret protector is not configured")
	}
	placer, err := order.NewSeamlessPlacer(database)
	if err != nil {
		return nil, err
	}
	return &SeamlessCoordinator{
		database:  database,
		placer:    placer,
		client:    newClient(defaultTimeout, allowPrivateURLs),
		protector: protector,
		worker:    worker,
		bets:      bets,
		now:       time.Now,
		random:    rand.Reader,
	}, nil
}

// Place debits the merchant wallet, then funds and places a shadow order.
func (c *SeamlessCoordinator) Place(
	ctx context.Context,
	request *order.CreateRequest,
) (*types.Order, error) {
	created, _, err := c.PlaceWithBalance(ctx, request)
	return created, err
}

// PlaceWithBalance places an order and returns the merchant's post-debit balance.
func (c *SeamlessCoordinator) PlaceWithBalance(
	ctx context.Context,
	request *order.CreateRequest,
) (*types.Order, string, error) {
	if request == nil {
		return nil, "", errors.New("seamless order request is required")
	}
	request.WalletKind = "shadow"
	prepared, err := c.placer.Prepare(ctx, request)
	if err != nil {
		return nil, "", err
	}
	if prepared.Existing {
		balance, _ := newRepository(c.database).GetLatestBalance(ctx, request.MerchantID, request.UserID, request.Currency)
		return prepared.Order, balance, nil
	}
	debit := seamlessDebit{
		MerchantID:  prepared.Order.MerchantID,
		UserID:      prepared.Order.UserID,
		Currency:    prepared.Order.Currency,
		AmountCents: prepared.CollateralCents,
		OrderID:     prepared.Order.ID,
		MarketID:    prepared.Order.MarketID,
	}
	transactionID, balance, err := c.debit(ctx, debit)
	if err != nil {
		return nil, "", err
	}
	if err := c.placer.Place(ctx, prepared); err != nil {
		_ = c.enqueueRollback(ctx, debit, transactionID)
		return nil, "", err
	}
	_ = c.attachDebitOrder(ctx, transactionID, prepared.Order.ID, c.now().UTC())
	return prepared.Order, balance, nil
}

// PlaceBetWithBalance places a parimutuel stake through the seamless wallet:
// the merchant wallet is debited synchronously, the stake mirrors into the
// shadow wallet, and the bet joins the pool. On a rejected or unknown debit
// the merchant is refunded through the rollback outbox, exactly like orders.
// The merchant's post-debit balance is returned for the hosted UI meta.
func (c *SeamlessCoordinator) PlaceBetWithBalance(
	ctx context.Context,
	bet parimutuel.Bet,
) (*parimutuel.Bet, string, error) {
	bet.MarketID = strings.TrimSpace(bet.MarketID)
	bet.MerchantID = strings.TrimSpace(bet.MerchantID)
	bet.UserID = strings.TrimSpace(bet.UserID)
	bet.Currency = strings.ToUpper(strings.TrimSpace(bet.Currency))
	if bet.MarketID == "" || bet.MerchantID == "" || bet.UserID == "" || bet.Currency == "" || bet.Stake < 0.01 {
		return nil, "", errors.New("seamless bet request is invalid")
	}
	amountCents, err := fixed.CentsFromFloat(bet.Stake)
	if err != nil {
		return nil, "", errors.New("seamless bet amount is invalid")
	}
	// The bet ID is generated before the debit so the wallet callback ref
	// carries a well-formed order_id; merchants reject callbacks whose
	// order_id is missing or empty. The same ID is persisted with the bet.
	betID, err := generateUUID(c.random)
	if err != nil {
		return nil, "", fmt.Errorf("generate parimutuel bet ID: %w", err)
	}
	debit := seamlessDebit{
		MerchantID:  bet.MerchantID,
		UserID:      bet.UserID,
		Currency:    bet.Currency,
		AmountCents: amountCents,
		OrderID:     betID,
		MarketID:    bet.MarketID,
	}
	transactionID, balance, err := c.debit(ctx, debit)
	if err != nil {
		return nil, "", err
	}
	if err := c.fundShadowBetWallet(ctx, bet, amountCents); err != nil {
		_ = c.enqueueRollback(ctx, debit, transactionID)
		return nil, "", err
	}
	bet.ID = betID
	bet.WalletKind = parimutuel.WalletKindShadow
	placed, err := c.bets.PlaceBet(ctx, bet)
	if err != nil {
		// Compensate the mirror: pull the stake back out of the shadow wallet
		// and refund the merchant through the rollback outbox.
		_ = c.refundShadowBetWallet(ctx, bet, amountCents)
		_ = c.enqueueRollback(ctx, debit, transactionID)
		return nil, "", err
	}
	return placed, balance, nil
}

// fundShadowBetWallet mirrors a seamless debit into the user's shadow wallet
// so settlement can pay out (or refund) from it through credit callbacks.
func (c *SeamlessCoordinator) fundShadowBetWallet(
	ctx context.Context,
	bet parimutuel.Bet,
	amountCents int64,
) error {
	databaseTx, err := c.database.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin shadow bet funding: %w", err)
	}
	defer func() { _ = databaseTx.Rollback() }()
	const ensureQuery = `
INSERT INTO wallets (
    id, merchant_id, user_id, currency, kind, balance, locked_balance, updated_at
) VALUES (gen_random_uuid(), $1, $2, $3, 'shadow', 0, 0, $4)
ON CONFLICT (merchant_id, user_id, currency, kind) DO NOTHING`
	if _, err := databaseTx.ExecContext(ctx, ensureQuery, bet.MerchantID, bet.UserID, bet.Currency, c.now().UTC()); err != nil {
		return fmt.Errorf("ensure shadow bet wallet: %w", err)
	}
	const creditQuery = `
UPDATE wallets
SET balance = balance + $4::numeric, updated_at = $5
WHERE merchant_id = $1 AND user_id = $2 AND currency = $3 AND kind = 'shadow'`
	result, err := databaseTx.ExecContext(
		ctx,
		creditQuery,
		bet.MerchantID,
		bet.UserID,
		bet.Currency,
		fixed.FormatCents(amountCents),
		c.now().UTC(),
	)
	if err != nil {
		return fmt.Errorf("credit shadow bet wallet: %w", err)
	}
	if rows, err := result.RowsAffected(); err != nil || rows != 1 {
		return errors.New("shadow bet wallet is unavailable")
	}
	return databaseTx.Commit()
}

// refundShadowBetWallet pulls a mirrored stake back out of the shadow wallet
// when the bet could not join the pool.
func (c *SeamlessCoordinator) refundShadowBetWallet(
	ctx context.Context,
	bet parimutuel.Bet,
	amountCents int64,
) error {
	const query = `
UPDATE wallets
SET balance = balance - $4::numeric, updated_at = $5
WHERE merchant_id = $1 AND user_id = $2 AND currency = $3 AND kind = 'shadow' AND balance >= $4::numeric`
	result, err := c.database.ExecContext(
		ctx,
		query,
		bet.MerchantID,
		bet.UserID,
		bet.Currency,
		fixed.FormatCents(amountCents),
		c.now().UTC(),
	)
	if err != nil {
		return fmt.Errorf("refund shadow bet wallet: %w", err)
	}
	if rows, err := result.RowsAffected(); err != nil || rows != 1 {
		return errors.New("shadow bet wallet refund is unavailable")
	}
	return nil
}

// debit performs the synchronous seamless merchant debit: endpoint
// verification, transaction insert, signed callback delivery, and status
// bookkeeping. It returns the transaction id (the idempotency key for the
// merchant) and the merchant's post-debit balance.
func (c *SeamlessCoordinator) debit(
	ctx context.Context,
	debit seamlessDebit,
) (string, string, error) {
	endpoint, err := newRepository(c.database).MerchantEndpoint(ctx, debit.MerchantID)
	if err != nil {
		return "", "", err
	}
	if endpoint.WalletMode != "seamless" {
		return "", "", ErrSeamlessDisabled
	}
	if strings.TrimSpace(endpoint.CallbackURL) == "" || strings.TrimSpace(endpoint.CallbackSecretEnc) == "" {
		return "", "", ErrSeamlessDisabled
	}
	if endpoint.SeamlessDegraded {
		return "", "", ErrMerchantDegraded
	}
	if endpoint.CallbackVerifiedAt == nil {
		return "", "", ErrCallbackUnverified
	}
	secret, err := c.protector.Decrypt(endpoint.CallbackSecretEnc)
	if err != nil {
		return "", "", fmt.Errorf("decrypt merchant callback secret: %w", err)
	}
	transactionID, err := generateUUID(c.random)
	if err != nil {
		return "", "", fmt.Errorf("generate seamless transaction ID: %w", err)
	}
	now := c.now().UTC()
	if err := c.insertDebitTransaction(ctx, transactionID, debit, now); err != nil {
		return "", "", err
	}
	callbackID, err := generateUUID(c.random)
	if err != nil {
		return "", "", fmt.Errorf("generate callback ID: %w", err)
	}
	response, deliverErr := c.client.DeliverCallback(ctx, endpoint.CallbackURL, debit.MerchantID, secret, CallbackRequest{
		CallbackID:    callbackID,
		Type:          "debit",
		TransactionID: transactionID,
		UserID:        debit.UserID,
		Currency:      debit.Currency,
		Amount:        fixed.FormatCents(debit.AmountCents),
		Reason:        "bet",
		Ref: map[string]any{
			"order_id":  debit.OrderID,
			"market_id": debit.MarketID,
		},
		CreatedAt: now,
	})
	if deliverErr != nil {
		return "", "", c.handleDebitFailure(ctx, debit, transactionID, response, deliverErr)
	}
	if err := c.markDebitAccepted(ctx, transactionID, response, now); err != nil {
		_ = c.enqueueRollback(ctx, debit, transactionID)
		return "", "", err
	}
	return transactionID, response.Balance, nil
}

func (c *SeamlessCoordinator) handleDebitFailure(
	ctx context.Context,
	debit seamlessDebit,
	transactionID string,
	response *CallbackResponse,
	deliverErr error,
) error {
	now := c.now().UTC()
	if response != nil {
		switch response.Status {
		case StatusInsufficientFunds:
			_ = c.markDebitRejected(ctx, transactionID, response, now)
			return withBalance(ErrInsufficientFunds, response.Balance)
		case StatusUserNotFound:
			_ = c.markDebitRejected(ctx, transactionID, response, now)
			return withBalance(ErrUserNotFound, response.Balance)
		case StatusUserBlocked:
			_ = c.markDebitRejected(ctx, transactionID, response, now)
			return withBalance(ErrUserBlocked, response.Balance)
		}
	}
	if errors.Is(deliverErr, ErrPermanent) && response != nil {
		_ = c.markDebitRejected(ctx, transactionID, response, now)
		switch response.Status {
		case StatusInsufficientFunds:
			return withBalance(ErrInsufficientFunds, response.Balance)
		case StatusUserNotFound:
			return withBalance(ErrUserNotFound, response.Balance)
		case StatusUserBlocked:
			return withBalance(ErrUserBlocked, response.Balance)
		}
		return deliverErr
	}
	_ = c.enqueueRollback(ctx, debit, transactionID)
	return fmt.Errorf("%w: %v", ErrDebitUnknown, deliverErr)
}

func (c *SeamlessCoordinator) enqueueRollback(
	ctx context.Context,
	debit seamlessDebit,
	transactionID string,
) error {
	if c.worker == nil {
		return errors.New("rollback enqueuer is not configured")
	}
	// The order (or bet) is never persisted for an unknown/failed debit, so it
	// must not be referenced by the rollback outbox row (FK would reject the
	// insert). The order_id reference is still carried in the callback ref so
	// merchants that require a well-formed order_id accept the rollback; the
	// transaction_id is the authoritative idempotency key for the merchant.
	return c.worker.EnqueueRollback(
		ctx,
		debit.MerchantID,
		debit.UserID,
		debit.Currency,
		debit.AmountCents,
		transactionID,
		debit.OrderID,
		debit.MarketID,
	)
}

func (c *SeamlessCoordinator) insertDebitTransaction(
	ctx context.Context,
	transactionID string,
	debit seamlessDebit,
	now time.Time,
) error {
	const query = `
INSERT INTO seamless_transactions (
    transaction_id, merchant_id, user_id, currency, type, reason, amount,
    status, created_at, updated_at
) VALUES (
    $1, $2, $3, $4, 'debit', 'bet', $5::numeric,
    'created', $6, $6
)`
	if _, err := c.database.ExecContext(
		ctx,
		query,
		transactionID,
		debit.MerchantID,
		debit.UserID,
		debit.Currency,
		fixed.FormatCents(debit.AmountCents),
		now,
	); err != nil {
		return fmt.Errorf("insert seamless debit transaction: %w", err)
	}
	return nil
}

func (c *SeamlessCoordinator) attachDebitOrder(
	ctx context.Context,
	transactionID string,
	orderID string,
	now time.Time,
) error {
	const query = `
UPDATE seamless_transactions
SET order_id = $2, updated_at = $3
WHERE transaction_id = $1`
	if _, err := c.database.ExecContext(ctx, query, transactionID, orderID, now); err != nil {
		return fmt.Errorf("attach seamless debit order: %w", err)
	}
	return nil
}

func (c *SeamlessCoordinator) markDebitAccepted(
	ctx context.Context,
	transactionID string,
	response *CallbackResponse,
	now time.Time,
) error {
	responseJSON, err := json.Marshal(response)
	if err != nil {
		return fmt.Errorf("marshal debit response: %w", err)
	}
	const query = `
UPDATE seamless_transactions
SET status = 'accepted', callback_response = $2::jsonb, updated_at = $3
WHERE transaction_id = $1`
	if _, err := c.database.ExecContext(ctx, query, transactionID, string(responseJSON), now); err != nil {
		return fmt.Errorf("accept seamless debit: %w", err)
	}
	return nil
}

func (c *SeamlessCoordinator) markDebitRejected(
	ctx context.Context,
	transactionID string,
	response *CallbackResponse,
	now time.Time,
) error {
	responseJSON, err := json.Marshal(response)
	if err != nil {
		return fmt.Errorf("marshal rejected debit response: %w", err)
	}
	const query = `
UPDATE seamless_transactions
SET status = 'rejected', callback_response = $2::jsonb, updated_at = $3
WHERE transaction_id = $1`
	if _, err := c.database.ExecContext(ctx, query, transactionID, string(responseJSON), now); err != nil {
		return fmt.Errorf("reject seamless debit: %w", err)
	}
	return nil
}

func generateUUID(random io.Reader) (string, error) {
	buffer := make([]byte, 16)
	if _, err := io.ReadFull(random, buffer); err != nil {
		return "", err
	}
	buffer[6] = (buffer[6] & 0x0f) | 0x40
	buffer[8] = (buffer[8] & 0x3f) | 0x80
	return fmt.Sprintf(
		"%x-%x-%x-%x-%x",
		buffer[0:4],
		buffer[4:6],
		buffer[6:8],
		buffer[8:10],
		buffer[10:16],
	), nil
}

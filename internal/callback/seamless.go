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

// NewSeamlessCoordinator wires the synchronous debit + place path.
func NewSeamlessCoordinator(
	database *sql.DB,
	protector secretDecryptor,
	worker rollbackEnqueuer,
	allowPrivateURLs bool,
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
	endpoint, err := newRepository(c.database).MerchantEndpoint(ctx, request.MerchantID)
	if err != nil {
		return nil, "", err
	}
	if endpoint.WalletMode != "seamless" {
		return nil, "", ErrSeamlessDisabled
	}
	if strings.TrimSpace(endpoint.CallbackURL) == "" || strings.TrimSpace(endpoint.CallbackSecretEnc) == "" {
		return nil, "", ErrSeamlessDisabled
	}
	if endpoint.SeamlessDegraded {
		return nil, "", ErrMerchantDegraded
	}
	if endpoint.CallbackVerifiedAt == nil {
		return nil, "", ErrCallbackUnverified
	}
	secret, err := c.protector.Decrypt(endpoint.CallbackSecretEnc)
	if err != nil {
		return nil, "", fmt.Errorf("decrypt merchant callback secret: %w", err)
	}
	transactionID, err := generateUUID(c.random)
	if err != nil {
		return nil, "", fmt.Errorf("generate seamless transaction ID: %w", err)
	}
	now := c.now().UTC()
	if err := c.insertDebitTransaction(ctx, transactionID, prepared, now); err != nil {
		return nil, "", err
	}
	callbackID, err := generateUUID(c.random)
	if err != nil {
		return nil, "", fmt.Errorf("generate callback ID: %w", err)
	}
	response, deliverErr := c.client.DeliverCallback(ctx, endpoint.CallbackURL, request.MerchantID, secret, CallbackRequest{
		CallbackID:    callbackID,
		Type:          "debit",
		TransactionID: transactionID,
		UserID:        prepared.Order.UserID,
		Currency:      prepared.Order.Currency,
		Amount:        fixed.FormatCents(prepared.CollateralCents),
		Reason:        "bet",
		Ref: map[string]any{
			"order_id":  prepared.Order.ID,
			"market_id": prepared.Order.MarketID,
		},
		CreatedAt: now,
	})
	if deliverErr != nil {
		return nil, "", c.handleDebitFailure(ctx, prepared, transactionID, response, deliverErr)
	}
	if err := c.markDebitAccepted(ctx, transactionID, response, now); err != nil {
		_ = c.enqueueRollback(ctx, prepared, transactionID)
		return nil, "", err
	}
	if err := c.placer.Place(ctx, prepared); err != nil {
		_ = c.enqueueRollback(ctx, prepared, transactionID)
		return nil, "", err
	}
	_ = c.attachDebitOrder(ctx, transactionID, prepared.Order.ID, c.now().UTC())
	return prepared.Order, response.Balance, nil
}

func (c *SeamlessCoordinator) handleDebitFailure(
	ctx context.Context,
	prepared *order.PreparedSeamlessOrder,
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
	_ = c.enqueueRollback(ctx, prepared, transactionID)
	return fmt.Errorf("%w: %v", ErrDebitUnknown, deliverErr)
}

func (c *SeamlessCoordinator) enqueueRollback(
	ctx context.Context,
	prepared *order.PreparedSeamlessOrder,
	transactionID string,
) error {
	if c.worker == nil {
		return errors.New("rollback enqueuer is not configured")
	}
	// The order is never persisted for an unknown/failed debit, so it must not
	// be referenced by the rollback outbox row (FK would reject the insert).
	// The transaction_id is the authoritative idempotency key for the merchant.
	return c.worker.EnqueueRollback(
		ctx,
		prepared.Order.MerchantID,
		prepared.Order.UserID,
		prepared.Order.Currency,
		prepared.CollateralCents,
		transactionID,
		"",
		prepared.Order.MarketID,
	)
}

func (c *SeamlessCoordinator) insertDebitTransaction(
	ctx context.Context,
	transactionID string,
	prepared *order.PreparedSeamlessOrder,
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
		prepared.Order.MerchantID,
		prepared.Order.UserID,
		prepared.Order.Currency,
		fixed.FormatCents(prepared.CollateralCents),
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

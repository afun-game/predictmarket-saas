package order

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/afun-game/predictmarket-saas/pkg/types"
)

// SeamlessPlacer prepares and persists orders funded by a merchant callback.
// It deliberately bypasses the Twill transport wrapper so the callback
// coordinator can use one PostgreSQL transaction for shadow funding and order
// placement.
type SeamlessPlacer struct {
	repository *postgresRepository
	now        func() time.Time
}

// PreparedSeamlessOrder is validated before the synchronous merchant debit.
type PreparedSeamlessOrder struct {
	Order           *types.Order
	CollateralCents int64
	Existing        bool
}

// NewSeamlessPlacer creates a PostgreSQL-only placement helper.
func NewSeamlessPlacer(database *sql.DB) (*SeamlessPlacer, error) {
	if database == nil {
		return nil, errors.New("seamless order database is not configured")
	}
	return &SeamlessPlacer{repository: newPostgresRepository(database), now: time.Now}, nil
}

// Prepare validates an order and verifies that its market is active before a
// merchant wallet is debited. Retrying an existing idempotency key is a no-op.
func (p *SeamlessPlacer) Prepare(
	ctx context.Context,
	request *CreateRequest,
) (*PreparedSeamlessOrder, error) {
	input, err := validateCreateRequest(request)
	if err != nil {
		return nil, err
	}
	input.WalletKind = "shadow"
	if input.IdempotencyKey != "" {
		existing, err := p.repository.GetByIdempotency(ctx, input.MerchantID, input.IdempotencyKey)
		if err == nil {
			return &PreparedSeamlessOrder{Order: existing, Existing: true}, nil
		}
		if !errors.Is(err, ErrNotFound) {
			return nil, fmt.Errorf("get idempotent seamless order: %w", err)
		}
	}
	if err := p.validateMarket(ctx, input); err != nil {
		return nil, err
	}
	orderID, err := generateUUID(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generate seamless order ID: %w", err)
	}
	value := &types.Order{
		ID:             orderID,
		MerchantID:     input.MerchantID,
		UserID:         input.UserID,
		MarketID:       input.MarketID,
		Type:           input.Type,
		Option:         input.Option,
		Amount:         input.Amount,
		Currency:       input.Currency,
		Price:          input.Price,
		TimeInForce:    input.TimeInForce,
		IdempotencyKey: input.IdempotencyKey,
		WalletKind:     "shadow",
		Channel:        input.Channel,
		Status:         "pending",
		CreatedAt:      p.now().UTC(),
	}
	return &PreparedSeamlessOrder{
		Order:           value,
		CollateralCents: requiredCollateralCents(input.Type, input.Amount, input.Price),
	}, nil
}

// Place atomically funds and locks the shadow wallet after the merchant has
// acknowledged the matching debit callback.
func (p *SeamlessPlacer) Place(ctx context.Context, prepared *PreparedSeamlessOrder) error {
	if prepared == nil || prepared.Order == nil || prepared.Existing {
		return errors.New("seamless order is not placeable")
	}
	if err := p.repository.PlaceWithFundedCollateral(ctx, prepared.Order, prepared.CollateralCents); err != nil {
		return fmt.Errorf("place funded seamless order: %w", err)
	}
	return nil
}

func (p *SeamlessPlacer) validateMarket(ctx context.Context, input *CreateRequest) error {
	const query = `
SELECT status, options
FROM markets
WHERE id = $1 AND merchant_id = $2`
	var status string
	var rawOptions []byte
	err := p.repository.database.QueryRowContext(ctx, query, input.MarketID, input.MerchantID).Scan(&status, &rawOptions)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrInvalidMarket
	}
	if err != nil {
		return fmt.Errorf("get seamless order market: %w", err)
	}
	options := []string{}
	if err := json.Unmarshal(rawOptions, &options); err != nil {
		return fmt.Errorf("decode seamless order market options: %w", err)
	}
	if status != "active" || !containsOption(options, input.Option) {
		return ErrInvalidMarket
	}
	return nil
}

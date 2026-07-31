package merchant

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/afun-game/predictmarket-saas/pkg/types"
)

const merchantColumns = `
    id,
    name,
    email,
	api_key,
	api_key_prefix,
	api_secret,
	api_secret_enc,
	api_secret_secondary_enc,
	api_secret_secondary_expires_at,
	status,
	currency,
	timezone,
	wallet_mode,
	COALESCE(callback_url, ''),
	COALESCE(callback_secret_enc, ''),
	COALESCE(webhook_url, ''),
	COALESCE(array_to_string(webhook_events, ','), ''),
	COALESCE(array_to_string(allowed_ips, ','), ''),
	callback_verified_at,
    fee_rate,
    created_at,
    updated_at`

type postgresRepository struct {
	database *sql.DB
}

func newPostgresRepository(database *sql.DB) *postgresRepository {
	return &postgresRepository{database: database}
}

func (r *postgresRepository) Create(ctx context.Context, merchant *types.Merchant) error {
	const query = `
INSERT INTO merchants (
    id,
    name,
    email,
	api_key,
	api_key_prefix,
	api_secret,
	api_secret_enc,
	api_secret_secondary_enc,
	api_secret_secondary_expires_at,
	status,
	currency,
	timezone,
	wallet_mode,
    fee_rate,
    created_at,
	updated_at
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16)`

	_, err := r.database.ExecContext(
		ctx,
		query,
		merchant.ID,
		merchant.Name,
		merchant.Email,
		merchant.APIKey,
		merchant.APIKeyPrefix,
		merchant.APISecret,
		merchant.APISecretEncrypted,
		merchant.APISecretSecondaryEncrypted,
		merchant.APISecretSecondaryExpiresAt,
		merchant.Status,
		merchant.Currency,
		merchant.Timezone,
		merchant.WalletMode,
		merchant.FeeRate,
		merchant.CreatedAt,
		merchant.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("insert merchant: %w", err)
	}
	return nil
}

func (r *postgresRepository) GetByID(
	ctx context.Context,
	merchantID string,
) (*types.Merchant, error) {
	query := "SELECT " + merchantColumns + " FROM merchants WHERE id = $1"
	return scanMerchant(r.database.QueryRowContext(ctx, query, merchantID))
}

func (r *postgresRepository) GetByAPIKeyPrefix(
	ctx context.Context,
	prefix string,
) (*types.Merchant, error) {
	query := "SELECT " + merchantColumns + " FROM merchants WHERE api_key_prefix = $1"
	return scanMerchant(r.database.QueryRowContext(ctx, query, prefix))
}

func (r *postgresRepository) UpdateAPIKey(
	ctx context.Context,
	merchantID string,
	prefix string,
	keyHash string,
) error {
	const query = `
UPDATE merchants
SET api_key = $2, api_key_prefix = $3, updated_at = NOW()
WHERE id = $1`
	result, err := r.database.ExecContext(ctx, query, merchantID, keyHash, prefix)
	if err != nil {
		return fmt.Errorf("update API key hash: %w", err)
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("count API key hash update: %w", err)
	}
	if rowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *postgresRepository) Update(ctx context.Context, merchant *types.Merchant) error {
	const query = `
UPDATE merchants
SET name = $2,
    email = $3,
    api_key = $4,
    api_key_prefix = $5,
    api_secret = $6,
    api_secret_enc = $7,
    api_secret_secondary_enc = $8,
    api_secret_secondary_expires_at = $9,
    status = $10,
    currency = $11,
    timezone = $12,
    wallet_mode = $13,
    callback_url = NULLIF($14, ''),
    callback_secret_enc = NULLIF($15, ''),
    webhook_url = NULLIF($16, ''),
    webhook_events = COALESCE(string_to_array(NULLIF($17, ''), ','), '{}'),
    allowed_ips = COALESCE(string_to_array(NULLIF($18, ''), ','), '{}'),
    callback_verified_at = $19,
    fee_rate = $20,
    updated_at = $21
WHERE id = $1`

	result, err := r.database.ExecContext(
		ctx,
		query,
		merchant.ID,
		merchant.Name,
		merchant.Email,
		merchant.APIKey,
		merchant.APIKeyPrefix,
		merchant.APISecret,
		merchant.APISecretEncrypted,
		merchant.APISecretSecondaryEncrypted,
		merchant.APISecretSecondaryExpiresAt,
		merchant.Status,
		merchant.Currency,
		merchant.Timezone,
		merchant.WalletMode,
		merchant.CallbackURL,
		merchant.CallbackSecretEncrypted,
		merchant.WebhookURL,
		strings.Join(merchant.WebhookEvents, ","),
		strings.Join(merchant.AllowedIPs, ","),
		merchant.CallbackVerifiedAt,
		merchant.FeeRate,
		merchant.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("update merchant row: %w", err)
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("count updated merchant rows: %w", err)
	}
	if rowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *postgresRepository) List(
	ctx context.Context,
	offset int,
	limit int,
) ([]*types.Merchant, error) {
	query := "SELECT " + merchantColumns + `
FROM merchants
ORDER BY created_at, id
OFFSET $1 LIMIT $2`

	rows, err := r.database.QueryContext(ctx, query, offset, limit)
	if err != nil {
		return nil, fmt.Errorf("query merchants: %w", err)
	}
	defer func() {
		_ = rows.Close() // Best-effort cleanup on early returns; checked below on the happy path.
	}()

	merchants := make([]*types.Merchant, 0, limit)
	for rows.Next() {
		merchant, err := scanMerchant(rows)
		if err != nil {
			return nil, err
		}
		merchants = append(merchants, merchant)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate merchants: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("close merchant rows: %w", err)
	}
	return merchants, nil
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanMerchant(row rowScanner) (*types.Merchant, error) {
	merchant := &types.Merchant{}
	var encryptedSecret sql.NullString
	var secondaryEncryptedSecret sql.NullString
	var secondaryExpiresAt sql.NullTime
	var callbackVerifiedAt sql.NullTime
	var webhookEventsCSV string
	var allowedIPsCSV string
	err := row.Scan(
		&merchant.ID,
		&merchant.Name,
		&merchant.Email,
		&merchant.APIKey,
		&merchant.APIKeyPrefix,
		&merchant.APISecret,
		&encryptedSecret,
		&secondaryEncryptedSecret,
		&secondaryExpiresAt,
		&merchant.Status,
		&merchant.Currency,
		&merchant.Timezone,
		&merchant.WalletMode,
		&merchant.CallbackURL,
		&merchant.CallbackSecretEncrypted,
		&merchant.WebhookURL,
		&webhookEventsCSV,
		&allowedIPsCSV,
		&callbackVerifiedAt,
		&merchant.FeeRate,
		&merchant.CreatedAt,
		&merchant.UpdatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("scan merchant: %w", err)
	}
	if encryptedSecret.Valid {
		merchant.APISecretEncrypted = encryptedSecret.String
	}
	if secondaryEncryptedSecret.Valid {
		merchant.APISecretSecondaryEncrypted = secondaryEncryptedSecret.String
	}
	if secondaryExpiresAt.Valid {
		expiresAt := secondaryExpiresAt.Time
		merchant.APISecretSecondaryExpiresAt = &expiresAt
	}
	if webhookEventsCSV != "" {
		merchant.WebhookEvents = strings.Split(webhookEventsCSV, ",")
	}
	if allowedIPsCSV != "" {
		merchant.AllowedIPs = strings.Split(allowedIPsCSV, ",")
	}
	if callbackVerifiedAt.Valid {
		verifiedAt := callbackVerifiedAt.Time
		merchant.CallbackVerifiedAt = &verifiedAt
	}
	return merchant, nil
}

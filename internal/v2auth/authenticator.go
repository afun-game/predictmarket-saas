// Package v2auth verifies signed merchant API requests against encrypted credentials.
package v2auth

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/afun-game/predictmarket-saas/internal/credentials"
	"github.com/afun-game/predictmarket-saas/pkg/types"
	"golang.org/x/crypto/bcrypt"
)

const apiKeyPrefixLength = 16

var ErrUnauthorized = errors.New("invalid merchant request credentials")

// Authenticator verifies V3 HMAC signatures after API key lookup.
type Authenticator struct {
	database  *sql.DB
	protector *credentials.Protector
}

// NewAuthenticator constructs a verifier from a database and the configured
// base64url AES-256-GCM key.
func NewAuthenticator(database *sql.DB, encodedEncryptionKey string) (*Authenticator, error) {
	if database == nil {
		return nil, errors.New("V3 authentication database is not configured")
	}
	protector, err := credentials.NewProtector(encodedEncryptionKey)
	if err != nil {
		return nil, fmt.Errorf("configure merchant secret encryption: %w", err)
	}
	return &Authenticator{database: database, protector: protector}, nil
}

// ValidateSignedRequest resolves the active merchant and verifies the exact
// `timestamp + "." + raw_body` V3 signature in constant time.
func (a *Authenticator) ValidateSignedRequest(
	ctx context.Context,
	apiKey string,
	timestamp string,
	signature string,
	body []byte,
) (*types.Merchant, error) {
	apiKey = strings.TrimSpace(apiKey)
	prefix, err := apiKeyPrefix(apiKey)
	if err != nil {
		return nil, ErrUnauthorized
	}
	merchant, keyHash, secrets, err := a.getMerchant(ctx, prefix)
	if err != nil {
		return nil, err
	}
	if bcrypt.CompareHashAndPassword([]byte(keyHash), []byte(apiKey)) != nil {
		return nil, ErrUnauthorized
	}
	provided, err := hex.DecodeString(strings.TrimSpace(signature))
	if err != nil {
		return nil, ErrUnauthorized
	}
	if a.validSignature(secrets.primary, timestamp, body, provided) {
		return merchant, nil
	}
	if secrets.secondary != "" && time.Now().UTC().Before(secrets.secondaryExpiresAt) && a.validSignature(secrets.secondary, timestamp, body, provided) {
		return merchant, nil
	}
	return nil, ErrUnauthorized
}

func (a *Authenticator) validSignature(encryptedSecret, timestamp string, body, provided []byte) bool {
	secret, err := a.protector.Decrypt(encryptedSecret)
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(timestamp))
	_, _ = mac.Write([]byte("."))
	_, _ = mac.Write(body)
	return subtle.ConstantTimeCompare(provided, mac.Sum(nil)) == 1
}

type merchantSecrets struct {
	primary            string
	secondary          string
	secondaryExpiresAt time.Time
}

func (a *Authenticator) getMerchant(
	ctx context.Context,
	prefix string,
) (*types.Merchant, string, merchantSecrets, error) {
	const query = `
SELECT id, api_key, api_secret_enc, api_secret_secondary_enc, api_secret_secondary_expires_at,
       status, currency, timezone, wallet_mode,
       COALESCE(array_to_string(allowed_ips, ','), ''),
       COALESCE(seamless_degraded, FALSE)
FROM merchants
WHERE api_key_prefix = $1`
	merchant := &types.Merchant{}
	var keyHash string
	var encryptedSecret sql.NullString
	var secondaryEncryptedSecret sql.NullString
	var secondaryExpiresAt sql.NullTime
	var allowedIPsCSV string
	err := a.database.QueryRowContext(ctx, query, prefix).Scan(
		&merchant.ID,
		&keyHash,
		&encryptedSecret,
		&secondaryEncryptedSecret,
		&secondaryExpiresAt,
		&merchant.Status,
		&merchant.Currency,
		&merchant.Timezone,
		&merchant.WalletMode,
		&allowedIPsCSV,
		&merchant.SeamlessDegraded,
	)
	if allowedIPsCSV != "" {
		merchant.AllowedIPs = strings.Split(allowedIPsCSV, ",")
	}
	if errors.Is(err, sql.ErrNoRows) {
		return nil, "", merchantSecrets{}, ErrUnauthorized
	}
	if err != nil {
		return nil, "", merchantSecrets{}, fmt.Errorf("look up merchant API key: %w", err)
	}
	if merchant.Status != "active" || !encryptedSecret.Valid || encryptedSecret.String == "" {
		return nil, "", merchantSecrets{}, ErrUnauthorized
	}
	secrets := merchantSecrets{primary: encryptedSecret.String}
	if secondaryEncryptedSecret.Valid && secondaryExpiresAt.Valid {
		secrets.secondary = secondaryEncryptedSecret.String
		secrets.secondaryExpiresAt = secondaryExpiresAt.Time.UTC()
	}
	return merchant, keyHash, secrets, nil
}

func apiKeyPrefix(apiKey string) (string, error) {
	if len(apiKey) < apiKeyPrefixLength {
		return "", ErrUnauthorized
	}
	return apiKey[:apiKeyPrefixLength], nil
}

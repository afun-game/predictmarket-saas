package v2auth

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/afun-game/predictmarket-saas/internal/credentials"
	"golang.org/x/crypto/bcrypt"
)

func TestValidateSignedRequest(t *testing.T) {
	t.Parallel()

	database, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New() error = %v", err)
	}
	defer database.Close()

	encodedKey := testEncryptionKey()
	protector, err := credentials.NewProtector(encodedKey)
	if err != nil {
		t.Fatalf("NewProtector() error = %v", err)
	}
	secret := "sk_live_test"
	encrypted, err := protector.Encrypt(secret)
	if err != nil {
		t.Fatalf("Encrypt() error = %v", err)
	}
	apiKey := "pk_live_0123456789abcdefghijklmnopqrstuvwxyz"
	keyHash, err := bcrypt.GenerateFromPassword([]byte(apiKey), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("GenerateFromPassword() error = %v", err)
	}
	requestBody := []byte(`{"user_id":"site-user-1"}`)
	timestamp := "1785398400"
	signature := sign(secret, timestamp, requestBody)

	mock.ExpectQuery("SELECT id, api_key, api_secret_enc, api_secret_secondary_enc, api_secret_secondary_expires_at,\\s+status, currency, timezone, wallet_mode,\\s+COALESCE\\(array_to_string\\(allowed_ips, ','\\), ''\\),\\s+COALESCE\\(seamless_degraded, FALSE\\)").
		WithArgs(apiKey[:apiKeyPrefixLength]).
		WillReturnRows(sqlmock.NewRows([]string{"id", "api_key", "api_secret_enc", "api_secret_secondary_enc", "api_secret_secondary_expires_at", "status", "currency", "timezone", "wallet_mode", "allowed_ips", "seamless_degraded"}).AddRow(
			"merchant-1", string(keyHash), encrypted, nil, nil, "active", "USD", "UTC", "transfer", "", false,
		))

	authenticator, err := NewAuthenticator(database, encodedKey)
	if err != nil {
		t.Fatalf("NewAuthenticator() error = %v", err)
	}
	merchant, err := authenticator.ValidateSignedRequest(context.Background(), apiKey, timestamp, signature, requestBody)
	if err != nil {
		t.Fatalf("ValidateSignedRequest() error = %v", err)
	}
	if merchant.ID != "merchant-1" || merchant.WalletMode != "transfer" {
		t.Errorf("merchant = %#v", merchant)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestValidateSignedRequestRejectsBadSignature(t *testing.T) {
	t.Parallel()

	database, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New() error = %v", err)
	}
	defer database.Close()

	encodedKey := testEncryptionKey()
	protector, err := credentials.NewProtector(encodedKey)
	if err != nil {
		t.Fatalf("NewProtector() error = %v", err)
	}
	encrypted, err := protector.Encrypt("sk_live_test")
	if err != nil {
		t.Fatalf("Encrypt() error = %v", err)
	}
	apiKey := "pk_live_0123456789abcdefghijklmnopqrstuvwxyz"
	keyHash, err := bcrypt.GenerateFromPassword([]byte(apiKey), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("GenerateFromPassword() error = %v", err)
	}
	mock.ExpectQuery("SELECT id, api_key, api_secret_enc, api_secret_secondary_enc, api_secret_secondary_expires_at,\\s+status, currency, timezone, wallet_mode,\\s+COALESCE\\(array_to_string\\(allowed_ips, ','\\), ''\\),\\s+COALESCE\\(seamless_degraded, FALSE\\)").
		WithArgs(apiKey[:apiKeyPrefixLength]).
		WillReturnRows(sqlmock.NewRows([]string{"id", "api_key", "api_secret_enc", "api_secret_secondary_enc", "api_secret_secondary_expires_at", "status", "currency", "timezone", "wallet_mode", "allowed_ips", "seamless_degraded"}).AddRow(
			"merchant-1", string(keyHash), encrypted, nil, nil, "active", "USD", "UTC", "transfer", "", false,
		))

	authenticator, err := NewAuthenticator(database, encodedKey)
	if err != nil {
		t.Fatalf("NewAuthenticator() error = %v", err)
	}
	_, err = authenticator.ValidateSignedRequest(context.Background(), apiKey, "1785398400", "00", []byte("{}"))
	if err != ErrUnauthorized {
		t.Errorf("ValidateSignedRequest() error = %v, want %v", err, ErrUnauthorized)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestValidateSignedRequestAcceptsUnexpiredSecondarySecret(t *testing.T) {
	t.Parallel()

	database, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New() error = %v", err)
	}
	defer database.Close()

	encodedKey := testEncryptionKey()
	protector, err := credentials.NewProtector(encodedKey)
	if err != nil {
		t.Fatalf("NewProtector() error = %v", err)
	}
	primary, err := protector.Encrypt("sk_live_new")
	if err != nil {
		t.Fatalf("Encrypt(primary) error = %v", err)
	}
	secondarySecret := "sk_live_previous"
	secondary, err := protector.Encrypt(secondarySecret)
	if err != nil {
		t.Fatalf("Encrypt(secondary) error = %v", err)
	}
	apiKey := "pk_live_0123456789abcdefghijklmnopqrstuvwxyz"
	keyHash, err := bcrypt.GenerateFromPassword([]byte(apiKey), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("GenerateFromPassword() error = %v", err)
	}
	body := []byte(`{"user_id":"site-user-1"}`)
	timestamp := "1785398400"
	mock.ExpectQuery("SELECT id, api_key, api_secret_enc, api_secret_secondary_enc, api_secret_secondary_expires_at,\\s+status, currency, timezone, wallet_mode,\\s+COALESCE\\(array_to_string\\(allowed_ips, ','\\), ''\\),\\s+COALESCE\\(seamless_degraded, FALSE\\)").
		WithArgs(apiKey[:apiKeyPrefixLength]).
		WillReturnRows(sqlmock.NewRows([]string{"id", "api_key", "api_secret_enc", "api_secret_secondary_enc", "api_secret_secondary_expires_at", "status", "currency", "timezone", "wallet_mode", "allowed_ips", "seamless_degraded"}).AddRow(
			"merchant-1", string(keyHash), primary, secondary, time.Now().UTC().Add(time.Hour), "active", "USD", "UTC", "transfer", "", false,
		))

	authenticator, err := NewAuthenticator(database, encodedKey)
	if err != nil {
		t.Fatalf("NewAuthenticator() error = %v", err)
	}
	merchant, err := authenticator.ValidateSignedRequest(context.Background(), apiKey, timestamp, sign(secondarySecret, timestamp, body), body)
	if err != nil {
		t.Fatalf("ValidateSignedRequest() error = %v", err)
	}
	if merchant.ID != "merchant-1" {
		t.Errorf("merchant = %#v", merchant)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func sign(secret string, timestamp string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(timestamp + "."))
	_, _ = mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

func testEncryptionKey() string {
	key := make([]byte, 32)
	for index := range key {
		key[index] = byte(index + 1)
	}
	return base64.RawURLEncoding.EncodeToString(key)
}

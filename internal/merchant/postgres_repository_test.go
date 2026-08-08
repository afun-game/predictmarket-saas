package merchant

import (
	"context"
	"database/sql"
	"errors"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/afun-game/predictmarket-saas/pkg/types"
)

func TestPostgresRepositoryCreate(t *testing.T) {
	t.Parallel()

	database, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New() error = %v", err)
	}
	defer database.Close()

	now := time.Date(2026, time.July, 28, 9, 0, 0, 0, time.UTC)
	value := &types.Merchant{
		ID:                 "0b445166-6da5-469b-b88d-f076d1ef4199",
		Name:               "Acme",
		Email:              "admin@example.com",
		APIKey:             "pk_live_test",
		APIKeyPrefix:       "pk_live_test",
		APISecret:          "hashed-secret",
		APISecretEncrypted: "encrypted-secret",
		Status:             "active",
		Currency:           "USD",
		Timezone:           "UTC",
		WalletMode:         "transfer",
		FeeRate:            0,
		CreatedAt:          now,
		UpdatedAt:          now,
	}
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO merchants")).
		WithArgs(
			value.ID,
			value.Name,
			value.Email,
			value.APIKey,
			value.APIKeyPrefix,
			value.APISecret,
			value.APISecretEncrypted,
			value.APISecretSecondaryEncrypted,
			value.APISecretSecondaryExpiresAt,
			value.Status,
			value.Currency,
			value.Timezone,
			value.WalletMode,
			value.FeeRate,
			value.CreatedAt,
			value.UpdatedAt,
		).
		WillReturnResult(sqlmock.NewResult(0, 1))

	repository := newPostgresRepository(database)
	if err := repository.Create(context.Background(), value); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestPostgresRepositoryGetByID(t *testing.T) {
	t.Parallel()

	database, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New() error = %v", err)
	}
	defer database.Close()

	now := time.Date(2026, time.July, 28, 9, 0, 0, 0, time.UTC)
	columns := []string{
		"id",
		"name",
		"email",
		"api_key",
		"api_key_prefix",
		"api_secret",
		"api_secret_enc",
		"api_secret_secondary_enc",
		"api_secret_secondary_expires_at",
		"status",
		"currency",
		"timezone",
		"wallet_mode",
		"callback_url",
		"callback_secret_enc",
		"webhook_url",
		"webhook_events",
		"allowed_ips",
		"callback_verified_at",
		"fee_rate",
		"max_bet_amount",
		"max_user_exposure",
		"max_market_exposure",
		"created_at",
		"updated_at",
	}
	mock.ExpectQuery("SELECT (.+) FROM merchants WHERE id = \\$1").
		WithArgs("merchant-1").
		WillReturnRows(sqlmock.NewRows(columns).AddRow(
			"merchant-1",
			"Acme",
			"admin@example.com",
			"pk_live_test",
			"pk_live_test",
			"hashed-secret",
			"encrypted-secret",
			nil,
			nil,
			"active",
			"USD",
			"UTC",
			"transfer",
			"",
			"",
			"",
			"",
			"",
			nil,
			0.02,
			"500.00",
			"",
			"",
			now,
			now,
		))

	repository := newPostgresRepository(database)
	merchant, err := repository.GetByID(context.Background(), "merchant-1")
	if err != nil {
		t.Fatalf("GetByID() error = %v", err)
	}
	if merchant.ID != "merchant-1" || merchant.Email != "admin@example.com" {
		t.Errorf("GetByID() = %#v", merchant)
	}
	if merchant.MaxBetAmount != "500.00" {
		t.Errorf("MaxBetAmount = %q, want 500.00", merchant.MaxBetAmount)
	}
	if merchant.MaxUserExposure != "" || merchant.MaxMarketExposure != "" {
		t.Errorf("NULL limits should scan empty, got %q and %q",
			merchant.MaxUserExposure, merchant.MaxMarketExposure)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestPostgresRepositoryMapsMissingMerchant(t *testing.T) {
	t.Parallel()

	database, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New() error = %v", err)
	}
	defer database.Close()

	mock.ExpectQuery("SELECT (.+) FROM merchants WHERE api_key_prefix = \\$1").
		WithArgs("unknown").
		WillReturnError(sql.ErrNoRows)

	repository := newPostgresRepository(database)
	_, err = repository.GetByAPIKeyPrefix(context.Background(), "unknown")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("GetByAPIKeyPrefix() error = %v, want ErrNotFound", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

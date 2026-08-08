package order

import (
	"context"
	"database/sql"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/afun-game/predictmarket-saas/pkg/types"
)

func TestCheckBetAmountLimit(t *testing.T) {
	tests := []struct {
		name            string
		collateralCents int64
		marketMaxBet    sql.NullString
		merchantMaxBet  sql.NullString
		wantErr         bool
	}{
		{
			name:            "no_limit",
			collateralCents: 10000,
			marketMaxBet:    sql.NullString{Valid: false},
			merchantMaxBet:  sql.NullString{Valid: false},
			wantErr:         false,
		},
		{
			name:            "under_merchant_limit",
			collateralCents: 5000,
			merchantMaxBet:  sql.NullString{String: "100.00", Valid: true},
			wantErr:         false,
		},
		{
			name:            "exceeds_merchant_limit",
			collateralCents: 15000,
			merchantMaxBet:  sql.NullString{String: "100.00", Valid: true},
			wantErr:         true,
		},
		{
			name:            "market_limit_overrides_merchant",
			collateralCents: 6000,
			marketMaxBet:    sql.NullString{String: "50.00", Valid: true},
			merchantMaxBet:  sql.NullString{String: "100.00", Valid: true},
			wantErr:         true,
		},
		{
			name:            "under_market_override",
			collateralCents: 4000,
			marketMaxBet:    sql.NullString{String: "50.00", Valid: true},
			merchantMaxBet:  sql.NullString{String: "100.00", Valid: true},
			wantErr:         false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := checkBetAmountLimit(tt.collateralCents, tt.marketMaxBet, tt.merchantMaxBet)
			if (err != nil) != tt.wantErr {
				t.Errorf("checkBetAmountLimit() error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr && err != ErrBetAmountTooLarge {
				t.Errorf("expected ErrBetAmountTooLarge, got %v", err)
			}
		})
	}
}

func testOrder() *types.Order {
	return &types.Order{
		MerchantID: "merchant-1",
		UserID:     "user-1",
		MarketID:   "market-1",
		Currency:   "USD",
		WalletKind: "user",
	}
}

// withTx runs body inside a mocked transaction so the exposure checks can be
// exercised without a live database.
func withTx(t *testing.T, arrange func(sqlmock.Sqlmock), body func(*sql.Tx)) {
	t.Helper()
	database, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New() error = %v", err)
	}
	defer func() { _ = database.Close() }()

	mock.ExpectBegin()
	arrange(mock)
	databaseTx, err := database.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatalf("BeginTx() error = %v", err)
	}
	body(databaseTx)
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("SQL expectations: %v", err)
	}
}

func TestCheckUserExposureLimit(t *testing.T) {
	tests := []struct {
		name            string
		locked          string
		collateralCents int64
		limit           string
		wantErr         error
	}{
		{name: "under_limit", locked: "40.00", collateralCents: 5000, limit: "100.00"},
		{name: "at_limit", locked: "50.00", collateralCents: 5000, limit: "100.00"},
		{
			name:            "exceeds_limit",
			locked:          "60.00",
			collateralCents: 5000,
			limit:           "100.00",
			wantErr:         ErrUserExposureTooHigh,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			withTx(t, func(mock sqlmock.Sqlmock) {
				mock.ExpectQuery("FROM wallets").
					WithArgs("merchant-1", "user-1", "USD", "user").
					WillReturnRows(sqlmock.NewRows([]string{"locked"}).AddRow(tt.locked))
			}, func(databaseTx *sql.Tx) {
				err := checkUserExposureLimit(
					context.Background(), databaseTx, testOrder(), tt.collateralCents, tt.limit,
				)
				if err != tt.wantErr {
					t.Fatalf("checkUserExposureLimit() error = %v, want %v", err, tt.wantErr)
				}
			})
		})
	}
}

func TestCheckMarketExposureLimit(t *testing.T) {
	tests := []struct {
		name            string
		locked          string
		collateralCents int64
		limit           string
		wantErr         error
	}{
		{name: "under_limit", locked: "100.00", collateralCents: 20000, limit: "500.00"},
		{
			name:            "exceeds_limit",
			locked:          "450.00",
			collateralCents: 6000,
			limit:           "500.00",
			wantErr:         ErrMarketExposureTooHigh,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			withTx(t, func(mock sqlmock.Sqlmock) {
				mock.ExpectQuery("FROM orders").
					WithArgs("market-1", "merchant-1", "USD").
					WillReturnRows(sqlmock.NewRows([]string{"locked"}).AddRow(tt.locked))
			}, func(databaseTx *sql.Tx) {
				err := checkMarketExposureLimit(
					context.Background(), databaseTx, testOrder(), tt.collateralCents, tt.limit,
				)
				if err != tt.wantErr {
					t.Fatalf("checkMarketExposureLimit() error = %v, want %v", err, tt.wantErr)
				}
			})
		})
	}
}

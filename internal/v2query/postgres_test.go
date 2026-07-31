package v2query

import (
	"context"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestListTransactionsIncludesSeamlessRows(t *testing.T) {
	database, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New() error = %v", err)
	}
	defer func() { _ = database.Close() }()

	merchantID := "11111111-1111-4111-8111-111111111111"
	mock.ExpectQuery(`SELECT EXISTS \(SELECT 1 FROM merchants`).
		WithArgs(merchantID).
		WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(true))
	mock.ExpectQuery("WITH ledger AS").
		WithArgs(merchantID, "user-1", "", nil, nil, nil, nil, 101).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "wallet_id", "user_id", "type", "amount", "currency",
			"related_order_id", "status", "created_at",
		}).
			AddRow("22222222-2222-4222-8222-222222222222", "", "user-1", "debit", "3.00", "USD", nil, "accepted", time.Date(2026, 7, 31, 0, 0, 0, 0, time.UTC)).
			AddRow("33333333-3333-4333-8333-333333333333", "44444444-4444-4444-8444-444444444444", "user-1", "bet", "3.00", "USD", nil, "completed", time.Date(2026, 7, 30, 0, 0, 0, 0, time.UTC)))

	page, err := New(database).ListTransactions(context.Background(), TransactionFilters{
		MerchantID: merchantID,
		UserID:     "user-1",
		Limit:      100,
	})
	if err != nil {
		t.Fatalf("ListTransactions() error = %v", err)
	}
	if len(page.Transactions) != 2 {
		t.Fatalf("transactions = %#v, want two rows", page.Transactions)
	}
	if page.Transactions[0].Type != "debit" || page.Transactions[0].WalletID != "" {
		t.Errorf("seamless transaction = %#v", page.Transactions[0])
	}
	if page.Transactions[1].Type != "bet" || page.Transactions[1].WalletID == "" {
		t.Errorf("platform transaction = %#v", page.Transactions[1])
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("SQL expectations: %v", err)
	}
}

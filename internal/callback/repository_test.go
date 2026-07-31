package callback

import (
	"context"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestGetLatestBalance(t *testing.T) {
	database, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New() error = %v", err)
	}
	defer func() { _ = database.Close() }()

	mock.ExpectQuery("SELECT callback_response->>'balance'").
		WithArgs("merchant-1", "user-1", "USD").
		WillReturnRows(sqlmock.NewRows([]string{"balance"}).AddRow("70.00"))

	value, err := newRepository(database).GetLatestBalance(
		context.Background(),
		"merchant-1",
		"user-1",
		"USD",
	)
	if err != nil {
		t.Fatalf("GetLatestBalance() error = %v", err)
	}
	if value != "70.00" {
		t.Fatalf("GetLatestBalance() = %q, want 70.00", value)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("SQL expectations: %v", err)
	}
}

func TestGetLatestBalanceRejectsMalformedValue(t *testing.T) {
	database, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New() error = %v", err)
	}
	defer func() { _ = database.Close() }()

	mock.ExpectQuery("SELECT callback_response->>'balance'").
		WithArgs("merchant-1", "user-1", "USD").
		WillReturnRows(sqlmock.NewRows([]string{"balance"}).AddRow("not-money"))

	if _, err := newRepository(database).GetLatestBalance(
		context.Background(),
		"merchant-1",
		"user-1",
		"USD",
	); err == nil {
		t.Fatal("GetLatestBalance() error = nil, want malformed balance error")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("SQL expectations: %v", err)
	}
}

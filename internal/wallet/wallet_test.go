package wallet

import (
	"context"
	"errors"
	"math"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

const testMerchantID = "11111111-1111-4111-8111-111111111111"

func TestCreateGetAndDuplicate(t *testing.T) {
	service := newService(newMemoryRepository())
	ctx := context.Background()
	created, err := service.Create(ctx, testMerchantID, "user-1", "usd")
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	validWallet := created.ID != "" && created.Currency == "USD"
	if !validWallet || created.Balance != 0 || created.LockedBalance != 0 {
		t.Fatalf("Create() wallet = %#v", created)
	}
	if _, err := service.Create(ctx, testMerchantID, "user-1", "USD"); !errors.Is(err, ErrAlreadyExists) {
		t.Fatalf("Create(duplicate) error = %v, want ErrAlreadyExists", err)
	}
	available, locked, err := service.GetBalance(ctx, testMerchantID, "user-1", "USD")
	if err != nil {
		t.Fatalf("GetBalance() error = %v", err)
	}
	if available != 0 || locked != 0 {
		t.Errorf("GetBalance() = (%v, %v), want (0, 0)", available, locked)
	}
}

func TestCreditDebitLockUnlockAndTransactions(t *testing.T) {
	service := newService(newMemoryRepository())
	ctx := context.Background()
	if err := service.Credit(ctx, testMerchantID, "user-1", "USD", 100, "credit"); err != nil {
		t.Fatalf("Credit() error = %v", err)
	}
	if err := service.Debit(ctx, testMerchantID, "user-1", "USD", 25, "bet"); err != nil {
		t.Fatalf("Debit() error = %v", err)
	}
	if err := service.Lock(ctx, testMerchantID, "user-1", "USD", 30); err != nil {
		t.Fatalf("Lock() error = %v", err)
	}
	assertBalance(t, service, 45, 30)
	if err := service.Unlock(ctx, testMerchantID, "user-1", "USD", 10); err != nil {
		t.Fatalf("Unlock() error = %v", err)
	}
	assertBalance(t, service, 55, 20)

	transactions, total, err := service.ListTransactions(ctx, testMerchantID, "user-1", 1, 20)
	if err != nil {
		t.Fatalf("ListTransactions() error = %v", err)
	}
	if total != 2 || len(transactions) != 2 {
		t.Fatalf("ListTransactions() len = %d, total = %d", len(transactions), total)
	}
	for _, transaction := range transactions {
		if transaction.WalletID == "" || transaction.Status != "completed" {
			t.Errorf("transaction = %#v", transaction)
		}
	}
}

func TestCreditWithIdempotencyKeyDoesNotDoubleCredit(t *testing.T) {
	service := newService(newMemoryRepository())
	ctx := context.Background()
	for range 2 {
		if err := service.CreditWithIdempotency(
			ctx,
			testMerchantID,
			"idempotent-user",
			"USD",
			10,
			"credit",
			"wallet-retry-key",
		); err != nil {
			t.Fatalf("CreditWithIdempotency() error = %v", err)
		}
	}
	available, locked, err := service.GetBalance(ctx, testMerchantID, "idempotent-user", "USD")
	if err != nil {
		t.Fatalf("GetBalance() error = %v", err)
	}
	if available != 10 || locked != 0 {
		t.Errorf("balance = (%v, %v), want (10, 0)", available, locked)
	}
	_, total, err := service.ListTransactions(ctx, testMerchantID, "idempotent-user", 1, 10)
	if err != nil {
		t.Fatalf("ListTransactions() error = %v", err)
	}
	if total != 1 {
		t.Errorf("transaction count = %d, want 1", total)
	}
}

func TestTransfersAreIdempotentAndPreserveBalances(t *testing.T) {
	service := newService(newMemoryRepository())
	ctx := context.Background()
	depositRequest := &TransferRequest{
		MerchantID:            testMerchantID,
		MerchantTransactionID: "deposit-001",
		UserID:                "transfer-user",
		Currency:              "usd",
		Amount:                "10.25",
	}
	first, err := service.Deposit(ctx, depositRequest)
	if err != nil {
		t.Fatalf("Deposit() error = %v", err)
	}
	retry, err := service.Deposit(ctx, depositRequest)
	if err != nil {
		t.Fatalf("Deposit(retry) error = %v", err)
	}
	if first.ID != retry.ID || first.TransactionID != retry.TransactionID || first.Amount != 10.25 {
		t.Errorf("idempotent deposits = %#v, %#v", first, retry)
	}
	assertTransferBalance(t, service, "transfer-user", 10.25)

	conflicting := *depositRequest
	conflicting.Amount = "10.26"
	if _, err := service.Deposit(ctx, &conflicting); !errors.Is(err, ErrTransferConflict) {
		t.Fatalf("Deposit(conflict) error = %v, want ErrTransferConflict", err)
	}

	withdrawal, err := service.Withdraw(ctx, &TransferRequest{
		MerchantID:            testMerchantID,
		MerchantTransactionID: "withdrawal-001",
		UserID:                "transfer-user",
		Currency:              "USD",
		Amount:                "4.20",
	})
	if err != nil {
		t.Fatalf("Withdraw() error = %v", err)
	}
	if withdrawal.Direction != "withdrawal" || withdrawal.Status != "completed" {
		t.Errorf("withdrawal = %#v", withdrawal)
	}
	assertTransferBalance(t, service, "transfer-user", 6.05)

	found, err := service.GetTransfer(ctx, testMerchantID, "deposit-001")
	if err != nil {
		t.Fatalf("GetTransfer() error = %v", err)
	}
	if found.ID != first.ID {
		t.Errorf("GetTransfer() = %#v, want ID %q", found, first.ID)
	}
	if _, err := service.Withdraw(ctx, &TransferRequest{
		MerchantID:            testMerchantID,
		MerchantTransactionID: "withdrawal-insufficient",
		UserID:                "transfer-user",
		Currency:              "USD",
		Amount:                "6.06",
	}); !errors.Is(err, ErrInsufficientBalance) {
		t.Fatalf("Withdraw(insufficient) error = %v, want ErrInsufficientBalance", err)
	}
	assertTransferBalance(t, service, "transfer-user", 6.05)
}

func TestBalanceFailuresDoNotCreateTransactions(t *testing.T) {
	service := newService(newMemoryRepository())
	ctx := context.Background()
	if err := service.Credit(ctx, testMerchantID, "user-1", "USD", 10, "credit"); err != nil {
		t.Fatalf("Credit() error = %v", err)
	}
	if err := service.Debit(ctx, testMerchantID, "user-1", "USD", 11, "debit"); !errors.Is(err, ErrInsufficientBalance) {
		t.Fatalf("Debit() error = %v, want ErrInsufficientBalance", err)
	}
	if err := service.Lock(ctx, testMerchantID, "user-1", "USD", 11); !errors.Is(err, ErrInsufficientBalance) {
		t.Fatalf("Lock() error = %v, want ErrInsufficientBalance", err)
	}
	if err := service.Unlock(ctx, testMerchantID, "user-1", "USD", 1); !errors.Is(err, ErrInsufficientLocked) {
		t.Fatalf("Unlock() error = %v, want ErrInsufficientLocked", err)
	}
	_, total, err := service.ListTransactions(ctx, testMerchantID, "user-1", 1, 20)
	if err != nil {
		t.Fatalf("ListTransactions() error = %v", err)
	}
	if total != 1 {
		t.Errorf("transaction total = %d, want 1", total)
	}
}

func TestConcurrentOperationsPreventOverdraft(t *testing.T) {
	service := newService(newMemoryRepository())
	ctx := context.Background()
	const credits = 100
	var waitGroup sync.WaitGroup
	for range credits {
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()
			if err := service.Credit(ctx, testMerchantID, "concurrent-user", "USD", 1, "credit"); err != nil {
				t.Errorf("Credit() error = %v", err)
			}
		}()
	}
	waitGroup.Wait()

	const debitAttempts = 150
	var successfulDebits atomic.Int32
	for range debitAttempts {
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()
			err := service.Debit(ctx, testMerchantID, "concurrent-user", "USD", 1, "debit")
			switch {
			case err == nil:
				successfulDebits.Add(1)
			case errors.Is(err, ErrInsufficientBalance):
			default:
				t.Errorf("Debit() error = %v", err)
			}
		}()
	}
	waitGroup.Wait()
	if successfulDebits.Load() != credits {
		t.Errorf("successful debits = %d, want %d", successfulDebits.Load(), credits)
	}
	available, locked, err := service.GetBalance(ctx, testMerchantID, "concurrent-user", "USD")
	if err != nil {
		t.Fatalf("GetBalance() error = %v", err)
	}
	if available != 0 || locked != 0 {
		t.Errorf("balance = (%v, %v), want (0, 0)", available, locked)
	}
	_, total, err := service.ListTransactions(ctx, testMerchantID, "concurrent-user", 1, 100)
	if err != nil {
		t.Fatalf("ListTransactions() error = %v", err)
	}
	if total != credits*2 {
		t.Errorf("transaction total = %d, want %d", total, credits*2)
	}
}

func TestTransactionPaginationAcrossCurrencies(t *testing.T) {
	service := newService(newMemoryRepository())
	ctx := context.Background()
	for _, currency := range []string{"USD", "EUR", "GBP"} {
		if err := service.Credit(ctx, testMerchantID, "user-1", currency, 10, "win"); err != nil {
			t.Fatalf("Credit(%s) error = %v", currency, err)
		}
	}
	transactions, total, err := service.ListTransactions(ctx, testMerchantID, "user-1", 2, 2)
	if err != nil {
		t.Fatalf("ListTransactions() error = %v", err)
	}
	if total != 3 || len(transactions) != 1 {
		t.Errorf("ListTransactions() len = %d, total = %d", len(transactions), total)
	}
}

func TestValidation(t *testing.T) {
	tests := []struct {
		name string
		call func(*implementation) error
	}{
		{name: "merchant", call: func(s *implementation) error {
			_, err := s.Create(context.Background(), "bad", "user", "USD")
			return err
		}},
		{name: "user", call: func(s *implementation) error {
			_, err := s.Create(context.Background(), testMerchantID, " ", "USD")
			return err
		}},
		{name: "long user", call: func(s *implementation) error {
			_, err := s.Create(context.Background(), testMerchantID, strings.Repeat("x", 256), "USD")
			return err
		}},
		{name: "currency", call: func(s *implementation) error {
			_, err := s.Create(context.Background(), testMerchantID, "user", "BTC")
			return err
		}},
		{name: "zero amount", call: func(s *implementation) error {
			return s.Credit(context.Background(), testMerchantID, "user", "USD", 0, "credit")
		}},
		{name: "fractional cent", call: func(s *implementation) error {
			return s.Credit(context.Background(), testMerchantID, "user", "USD", 1.001, "credit")
		}},
		{name: "non-finite", call: func(s *implementation) error {
			return s.Credit(context.Background(), testMerchantID, "user", "USD", math.Inf(1), "credit")
		}},
		{name: "credit type", call: func(s *implementation) error {
			return s.Credit(context.Background(), testMerchantID, "user", "USD", 1, "fee")
		}},
		{name: "debit type", call: func(s *implementation) error {
			return s.Debit(context.Background(), testMerchantID, "user", "USD", 1, "win")
		}},
		{name: "page", call: func(s *implementation) error {
			_, _, err := s.ListTransactions(context.Background(), testMerchantID, "user", -1, 20)
			return err
		}},
		{name: "limit", call: func(s *implementation) error {
			_, _, err := s.ListTransactions(context.Background(), testMerchantID, "user", 1, 101)
			return err
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var validationErr *ValidationError
			if err := test.call(newService(newMemoryRepository())); !errors.As(err, &validationErr) {
				t.Fatalf("error = %v, want ValidationError", err)
			}
		})
	}
}

func assertBalance(t *testing.T, service *implementation, available, locked float64) {
	t.Helper()
	gotAvailable, gotLocked, err := service.GetBalance(
		context.Background(),
		testMerchantID,
		"user-1",
		"USD",
	)
	if err != nil {
		t.Fatalf("GetBalance() error = %v", err)
	}
	if gotAvailable != available || gotLocked != locked {
		t.Errorf("balance = (%v, %v), want (%v, %v)", gotAvailable, gotLocked, available, locked)
	}
}

func assertTransferBalance(t *testing.T, service *implementation, userID string, available float64) {
	t.Helper()
	gotAvailable, gotLocked, err := service.GetBalance(context.Background(), testMerchantID, userID, "USD")
	if err != nil {
		t.Fatalf("GetBalance() error = %v", err)
	}
	if gotAvailable != available || gotLocked != 0 {
		t.Errorf("balance = (%v, %v), want (%v, 0)", gotAvailable, gotLocked, available)
	}
}

package session

import (
	"context"
	"errors"
	"testing"
)

func TestExchangeConsumesLaunchTokenOnce(t *testing.T) {
	t.Parallel()

	manager := testManager(t)
	launchToken, launch, err := manager.CreateLaunch(
		context.Background(),
		"merchant-1",
		"user-1",
		"USD",
		"transfer",
		"zh-CN",
		"https://merchant.example/lobby",
	)
	if err != nil {
		t.Fatalf("CreateLaunch() error = %v", err)
	}
	if launch.ExpiresAt.IsZero() {
		t.Fatal("launch expiry was not set")
	}
	token, value, err := manager.Exchange(context.Background(), launchToken)
	if err != nil {
		t.Fatalf("Exchange() error = %v", err)
	}
	if token == "" || value.ID == "" {
		t.Fatalf("Exchange() = token %q, session %#v", token, value)
	}
	if _, _, err := manager.Exchange(context.Background(), launchToken); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("second Exchange() error = %v, want ErrUnauthorized", err)
	}
	validated, err := manager.Validate(context.Background(), token)
	if err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	if validated.UserID != "user-1" || validated.MerchantID != "merchant-1" || validated.WalletMode != "transfer" {
		t.Errorf("validated session = %#v", validated)
	}
}

func TestExchangeReturnsLaunchBalance(t *testing.T) {
	t.Parallel()

	manager := testManager(t)
	launchToken, _, err := manager.CreateLaunchWithBalance(
		context.Background(), "merchant-1", "user-1", "USD", "seamless", "70.00", "en-US", "",
	)
	if err != nil {
		t.Fatalf("CreateLaunchWithBalance() error = %v", err)
	}
	_, value, err := manager.Exchange(context.Background(), launchToken)
	if err != nil {
		t.Fatalf("Exchange() error = %v", err)
	}
	if value.Balance != "70.00" {
		t.Fatalf("Exchange() balance = %q, want 70.00", value.Balance)
	}
}

func TestRefreshExtendsBrowserSession(t *testing.T) {
	t.Parallel()

	manager := testManager(t)
	launchToken, _, err := manager.CreateLaunch(context.Background(), "merchant-1", "user-1", "USD", "transfer", "en-US", "")
	if err != nil {
		t.Fatalf("CreateLaunch() error = %v", err)
	}
	_, before, err := manager.Exchange(context.Background(), launchToken)
	if err != nil {
		t.Fatalf("Exchange() error = %v", err)
	}
	refreshedToken, after, err := manager.Refresh(context.Background(), before.ID)
	if err != nil {
		t.Fatalf("Refresh() error = %v", err)
	}
	if refreshedToken == "" || !after.ExpiresAt.After(before.ExpiresAt) {
		t.Errorf("Refresh() = token %q, session %#v; before = %#v", refreshedToken, after, before)
	}
	if _, err := manager.Validate(context.Background(), refreshedToken); err != nil {
		t.Fatalf("Validate(refreshedToken) error = %v", err)
	}
}

func TestRevokeInvalidatesBrowserToken(t *testing.T) {
	t.Parallel()

	manager := testManager(t)
	launchToken, _, err := manager.CreateLaunch(context.Background(), "merchant-1", "user-1", "USD", "transfer", "en-US", "")
	if err != nil {
		t.Fatalf("CreateLaunch() error = %v", err)
	}
	token, value, err := manager.Exchange(context.Background(), launchToken)
	if err != nil {
		t.Fatalf("Exchange() error = %v", err)
	}
	if err := manager.Revoke(context.Background(), "merchant-1", value.ID); err != nil {
		t.Fatalf("Revoke() error = %v", err)
	}
	if _, err := manager.Validate(context.Background(), token); err == nil {
		t.Fatal("Validate() error = nil after revocation")
	}
}

func TestRevokeInvalidatesUnexchangedLaunchToken(t *testing.T) {
	t.Parallel()

	manager := testManager(t)
	launchToken, launch, err := manager.CreateLaunch(context.Background(), "merchant-1", "user-1", "USD", "transfer", "en-US", "")
	if err != nil {
		t.Fatalf("CreateLaunch() error = %v", err)
	}
	if err := manager.Revoke(context.Background(), "merchant-1", launch.ID); err != nil {
		t.Fatalf("Revoke() error = %v", err)
	}
	if _, _, err := manager.Exchange(context.Background(), launchToken); !errors.Is(err, ErrUnauthorized) {
		t.Errorf("Exchange() after pending revoke error = %v, want ErrUnauthorized", err)
	}
}

func TestExchangeClassifiesMissingAndUnknownTokens(t *testing.T) {
	t.Parallel()

	manager := testManager(t)
	if _, _, err := manager.Exchange(context.Background(), " "); !errors.Is(err, ErrInvalidInput) {
		t.Errorf("Exchange(empty) error = %v, want ErrInvalidInput", err)
	}
	if _, _, err := manager.Exchange(context.Background(), "lt_unknown"); !errors.Is(err, ErrUnauthorized) {
		t.Errorf("Exchange(unknown) error = %v, want ErrUnauthorized", err)
	}
}

func TestReserveNonceRejectsReplay(t *testing.T) {
	t.Parallel()

	manager := testManager(t)
	if err := manager.ReserveNonce(context.Background(), "merchant-1", "request-1"); err != nil {
		t.Fatalf("first ReserveNonce() error = %v", err)
	}
	if err := manager.ReserveNonce(context.Background(), "merchant-1", "request-1"); err != ErrReplay {
		t.Errorf("second ReserveNonce() error = %v, want %v", err, ErrReplay)
	}
}

func testManager(t *testing.T) *Manager {
	t.Helper()
	secret := make([]byte, minimumSecretLen)
	for index := range secret {
		secret[index] = byte(index + 1)
	}
	manager, err := NewManager(NewMemoryStore(), secret)
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}
	return manager
}

package adminauth

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"
)

func testManager(t *testing.T) (*Manager, *MemoryRepository, *MemoryActionLog) {
	t.Helper()
	repo := NewMemoryRepository()
	logs := NewMemoryActionLog()
	hash, err := bcrypt.GenerateFromPassword([]byte("correct-pw"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.Create(context.Background(), Account{
		Username:     "boss",
		PasswordHash: string(hash),
		Role:         RoleSuperAdmin,
		Status:       StatusActive,
	}); err != nil {
		t.Fatal(err)
	}
	manager, err := NewManager(repo, logs, bytes.Repeat([]byte("k"), 32))
	if err != nil {
		t.Fatal(err)
	}
	return manager, repo, logs
}

func TestLoginSuccess(t *testing.T) {
	manager, _, _ := testManager(t)
	token, principal, err := manager.Login(context.Background(), "boss", "correct-pw")
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	if token == "" || principal.Username != "boss" || principal.Role != RoleSuperAdmin {
		t.Fatalf("unexpected principal: %+v token=%q", principal, token)
	}
	validated, err := manager.Validate(context.Background(), token)
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	if validated.ID != principal.ID {
		t.Fatalf("validated id = %q want %q", validated.ID, principal.ID)
	}
}

func TestLoginRejectsWrongPasswordWithoutRevealingUser(t *testing.T) {
	manager, _, _ := testManager(t)
	for _, username := range []string{"boss", "nobody"} {
		_, _, err := manager.Login(context.Background(), username, "wrong")
		if !errors.Is(err, ErrInvalidCredentials) {
			t.Fatalf("username %q: err = %v want ErrInvalidCredentials", username, err)
		}
	}
}

func TestLoginLocksAfterFiveFailures(t *testing.T) {
	manager, _, _ := testManager(t)
	for attempt := 1; attempt <= 4; attempt++ {
		_, _, err := manager.Login(context.Background(), "boss", "wrong")
		if !errors.Is(err, ErrInvalidCredentials) {
			t.Fatalf("attempt %d: err = %v want ErrInvalidCredentials", attempt, err)
		}
	}
	_, _, err := manager.Login(context.Background(), "boss", "wrong")
	if !errors.Is(err, ErrAccountLocked) {
		t.Fatalf("fifth failure: err = %v want ErrAccountLocked", err)
	}
	// Even the correct password is refused while locked.
	if _, _, err := manager.Login(context.Background(), "boss", "correct-pw"); !errors.Is(err, ErrAccountLocked) {
		t.Fatalf("locked account accepted password: %v", err)
	}
}

func TestLoginResetsCounterAfterSuccess(t *testing.T) {
	manager, _, _ := testManager(t)
	for attempt := 1; attempt <= 4; attempt++ {
		_, _, _ = manager.Login(context.Background(), "boss", "wrong")
	}
	if _, _, err := manager.Login(context.Background(), "boss", "correct-pw"); err != nil {
		t.Fatalf("login after failures: %v", err)
	}
	// Counter is reset, so four more failures must not lock the account.
	for attempt := 1; attempt <= 4; attempt++ {
		_, _, err := manager.Login(context.Background(), "boss", "wrong")
		if !errors.Is(err, ErrInvalidCredentials) {
			t.Fatalf("attempt %d after reset: %v", attempt, err)
		}
	}
}

func TestDisabledAccountCannotLoginOrValidate(t *testing.T) {
	manager, repo, _ := testManager(t)
	token, _, err := manager.Login(context.Background(), "boss", "correct-pw")
	if err != nil {
		t.Fatal(err)
	}
	account, err := repo.GetByUsername(context.Background(), "boss")
	if err != nil {
		t.Fatal(err)
	}
	account.Status = StatusDisabled
	if err := repo.Create(context.Background(), *account); err == nil {
		t.Fatal("duplicate create should fail")
	}
	// Simulate disable by mutating the stored account.
	repo.mu.Lock()
	repo.accounts[account.ID].Status = StatusDisabled
	repo.mu.Unlock()
	if _, err := manager.Validate(context.Background(), token); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("validate disabled session: %v", err)
	}
}

func TestTamperedTokenRejected(t *testing.T) {
	manager, _, _ := testManager(t)
	token, _, err := manager.Login(context.Background(), "boss", "correct-pw")
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(token, ".")
	parts[2] = strings.Repeat("A", len(parts[2]))
	if _, err := manager.Validate(context.Background(), strings.Join(parts, ".")); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("tampered token: %v", err)
	}
}

func TestEnsureBootstrap(t *testing.T) {
	repo := NewMemoryRepository()
	logs := NewMemoryActionLog()
	manager, err := NewManager(repo, logs, bytes.Repeat([]byte("k"), 32))
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.EnsureBootstrap(context.Background(), "root", "boot-pw"); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	account, err := repo.GetByUsername(context.Background(), "root")
	if err != nil {
		t.Fatal(err)
	}
	if account.Role != RoleSuperAdmin {
		t.Fatalf("bootstrap role = %q want super_admin", account.Role)
	}
	// Second call is a no-op (an account exists).
	if err := manager.EnsureBootstrap(context.Background(), "other", "pw"); err != nil {
		t.Fatalf("second bootstrap: %v", err)
	}
	if _, err := repo.GetByUsername(context.Background(), "other"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unexpected second account: %v", err)
	}
	// Bootstrapped credentials actually log in.
	if _, _, err := manager.Login(context.Background(), "root", "boot-pw"); err != nil {
		t.Fatalf("bootstrap login: %v", err)
	}
}

func TestSessionExpiry(t *testing.T) {
	manager, _, _ := testManager(t)
	token, _, err := manager.Login(context.Background(), "boss", "correct-pw")
	if err != nil {
		t.Fatal(err)
	}
	future := manager.now().UTC().Add(2 * sessionTTL)
	manager.now = func() time.Time { return future }
	if _, err := manager.Validate(context.Background(), token); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("expired session: %v", err)
	}
}

func TestRecordActionBestEffort(t *testing.T) {
	manager, _, logs := testManager(t)
	if err := manager.RecordAction(context.Background(), Action{
		AdminID:    "admin-1",
		Action:     "status.merchant",
		Resource:   "merchant",
		ResourceID: "m-1",
		ClientIP:   "127.0.0.1",
	}); err != nil {
		t.Fatalf("record action: %v", err)
	}
	if got := len(logs.Actions()); got != 1 {
		t.Fatalf("actions = %d want 1", got)
	}
}

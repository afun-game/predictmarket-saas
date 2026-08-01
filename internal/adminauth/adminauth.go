// Package adminauth manages administrator accounts, password login with
// lockout, short-lived session JWTs, and the administrator action trail.
package adminauth

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/afun-game/predictmarket-saas/internal/auth"
	"golang.org/x/crypto/bcrypt"
)

const (
	// RoleSuperAdmin can perform every console action.
	RoleSuperAdmin = "super_admin"
	// RoleOperator handles daily operations; sensitive writes are denied.
	RoleOperator = "operator"

	// StatusActive and StatusDisabled control account access.
	StatusActive   = "active"
	StatusDisabled = "disabled"

	maxFailedAttempts   = 5
	lockoutDuration     = 15 * time.Minute
	sessionTTL          = 12 * time.Hour
	minimumSecretLength = 32
)

var (
	ErrNotFound           = errors.New("admin account was not found")
	ErrInvalidCredentials = errors.New("invalid credentials")
	ErrAccountLocked      = errors.New("account is locked")
	ErrAccountDisabled    = errors.New("account is disabled")
	ErrUnauthorized       = errors.New("admin session is invalid")
	ErrInvalidInput       = errors.New("invalid admin input")
)

// Account is one administrator row. PasswordHash never leaves the service.
type Account struct {
	ID             string
	Username       string
	PasswordHash   string
	Role           string
	Status         string
	FailedAttempts int
	LockedUntil    *time.Time
	LastLoginAt    *time.Time
	CreatedAt      time.Time
}

// Principal is the authenticated identity attached to a request context.
type Principal struct {
	ID       string `json:"id"`
	Username string `json:"username"`
	Role     string `json:"role"`
}

// Action is one administrator change for the immutable action trail.
type Action struct {
	AdminID    string
	Action     string
	Resource   string
	ResourceID string
	Before     any
	After      any
	ClientIP   string
}

// Repository persists administrator accounts.
type Repository interface {
	GetByUsername(ctx context.Context, username string) (*Account, error)
	GetByID(ctx context.Context, id string) (*Account, error)
	Create(ctx context.Context, account Account) error
	Count(ctx context.Context) (int, error)
	// TouchLogin records a login outcome. On failure it increments the
	// attempt counter and returns the new locked_until (non-nil once the
	// lockout threshold is reached); on success it resets the counter.
	TouchLogin(ctx context.Context, id string, success bool, now time.Time) (*time.Time, error)
}

// ActionLogStore persists the administrator action trail. Implementations
// must never fail the original request; callers use best-effort contexts.
type ActionLogStore interface {
	RecordAction(ctx context.Context, action Action) error
}

// Manager coordinates login, sessions, and action recording.
type Manager struct {
	repo   Repository
	logs   ActionLogStore
	key    []byte
	now    func() time.Time
	random io.Reader
}

// NewManager creates a Manager with a production random source.
func NewManager(repo Repository, logs ActionLogStore, secret []byte) (*Manager, error) {
	if repo == nil || logs == nil || len(secret) < minimumSecretLength {
		return nil, ErrInvalidInput
	}
	return &Manager{
		repo:   repo,
		logs:   logs,
		key:    append([]byte{}, secret...),
		now:    time.Now,
		random: rand.Reader,
	}, nil
}

// NewManagerFromEncodedSecret builds a Manager from a base64url secret, the
// same format as the browser session SESSION_JWT_SECRET.
func NewManagerFromEncodedSecret(repo Repository, logs ActionLogStore, encodedSecret string) (*Manager, error) {
	secret, err := base64.RawURLEncoding.DecodeString(strings.TrimSpace(encodedSecret))
	if err != nil || len(secret) < minimumSecretLength {
		return nil, errors.New("admin session secret must be a base64url value of at least 32 bytes")
	}
	return NewManager(repo, logs, secret)
}

// Login verifies credentials, applies the per-account lockout policy, and
// returns a session JWT. Username failures are indistinguishable from
// password failures to avoid account enumeration.
func (m *Manager) Login(ctx context.Context, username, password string) (string, Principal, error) {
	username = strings.TrimSpace(username)
	if username == "" || password == "" {
		return "", Principal{}, ErrInvalidCredentials
	}
	account, err := m.repo.GetByUsername(ctx, username)
	if err != nil {
		return "", Principal{}, ErrInvalidCredentials
	}
	if account.Status != StatusActive {
		return "", Principal{}, ErrAccountDisabled
	}
	now := m.now().UTC()
	if account.LockedUntil != nil && now.Before(*account.LockedUntil) {
		return "", Principal{}, ErrAccountLocked
	}
	if err := bcrypt.CompareHashAndPassword([]byte(account.PasswordHash), []byte(password)); err != nil {
		lockedUntil, touchErr := m.repo.TouchLogin(ctx, account.ID, false, now)
		if touchErr != nil {
			return "", Principal{}, ErrInvalidCredentials
		}
		if lockedUntil != nil {
			return "", Principal{}, ErrAccountLocked
		}
		return "", Principal{}, ErrInvalidCredentials
	}
	if _, err := m.repo.TouchLogin(ctx, account.ID, true, now); err != nil {
		return "", Principal{}, ErrInvalidCredentials
	}
	principal := Principal{ID: account.ID, Username: account.Username, Role: account.Role}
	token, err := m.sign(principal)
	if err != nil {
		return "", Principal{}, err
	}
	return token, principal, nil
}

// Validate verifies a session JWT and the account's current status.
func (m *Manager) Validate(ctx context.Context, token string) (Principal, error) {
	claims, err := m.parse(token)
	if err != nil {
		return Principal{}, err
	}
	if !m.now().UTC().Before(time.Unix(claims.ExpiresAt, 0)) {
		return Principal{}, ErrUnauthorized
	}
	account, err := m.repo.GetByID(ctx, claims.Subject)
	if err != nil {
		return Principal{}, ErrUnauthorized
	}
	if account.Status != StatusActive {
		return Principal{}, ErrUnauthorized
	}
	if account.Username != claims.SubjectName || account.Role != claims.Role {
		return Principal{}, ErrUnauthorized
	}
	return Principal{ID: account.ID, Username: account.Username, Role: account.Role}, nil
}

// ValidateAdminSession implements auth.AdminSessionValidator for HTTP middleware.
func (m *Manager) ValidateAdminSession(ctx context.Context, token string) (*auth.AdminPrincipal, error) {
	principal, err := m.Validate(ctx, token)
	if err != nil {
		return nil, err
	}
	return &auth.AdminPrincipal{
		ID:       principal.ID,
		Username: principal.Username,
		Role:     principal.Role,
	}, nil
}

// EnsureBootstrap creates the first super-admin when no accounts exist.
// It is a no-op once any account exists.
func (m *Manager) EnsureBootstrap(ctx context.Context, username, password string) error {
	username = strings.TrimSpace(username)
	if username == "" || len(username) > 64 || password == "" {
		return ErrInvalidInput
	}
	count, err := m.repo.Count(ctx)
	if err != nil {
		return err
	}
	if count > 0 {
		return nil
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return fmt.Errorf("hash bootstrap password: %w", err)
	}
	account := Account{
		Username:     username,
		PasswordHash: string(hash),
		Role:         RoleSuperAdmin,
		Status:       StatusActive,
	}
	if err := m.repo.Create(ctx, account); err != nil {
		return err
	}
	return nil
}

// RecordAction persists an administrator change best-effort.
func (m *Manager) RecordAction(ctx context.Context, action Action) error {
	return m.logs.RecordAction(ctx, action)
}

type tokenClaims struct {
	Issuer      string `json:"iss"`
	Subject     string `json:"sub"`
	SubjectName string `json:"name"`
	Role        string `json:"rol"`
	IssuedAt    int64  `json:"iat"`
	ExpiresAt   int64  `json:"exp"`
}

func (m *Manager) sign(principal Principal) (string, error) {
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"HS256","typ":"JWT"}`))
	now := m.now().UTC()
	claims, err := json.Marshal(tokenClaims{
		Issuer:      "predictmarket-admin",
		Subject:     principal.ID,
		SubjectName: principal.Username,
		Role:        principal.Role,
		IssuedAt:    now.Unix(),
		ExpiresAt:   now.Add(sessionTTL).Unix(),
	})
	if err != nil {
		return "", fmt.Errorf("encode admin claims: %w", err)
	}
	payload := base64.RawURLEncoding.EncodeToString(claims)
	signingInput := header + "." + payload
	mac := hmac.New(sha256.New, m.key)
	_, _ = mac.Write([]byte(signingInput))
	signature := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return signingInput + "." + signature, nil
}

func (m *Manager) parse(token string) (tokenClaims, error) {
	parts := strings.Split(strings.TrimSpace(token), ".")
	if len(parts) != 3 || parts[0] == "" || parts[1] == "" || parts[2] == "" {
		return tokenClaims{}, ErrUnauthorized
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return tokenClaims{}, ErrUnauthorized
	}
	mac := hmac.New(sha256.New, m.key)
	_, _ = mac.Write([]byte(parts[0] + "." + parts[1]))
	if !hmac.Equal(signature, mac.Sum(nil)) {
		return tokenClaims{}, ErrUnauthorized
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return tokenClaims{}, ErrUnauthorized
	}
	claims := tokenClaims{}
	if err := json.Unmarshal(payload, &claims); err != nil {
		return tokenClaims{}, ErrUnauthorized
	}
	if claims.Issuer != "predictmarket-admin" || claims.Subject == "" || claims.SubjectName == "" || claims.Role == "" || claims.ExpiresAt == 0 {
		return tokenClaims{}, ErrUnauthorized
	}
	return claims, nil
}

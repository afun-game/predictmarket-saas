// Package session manages one-time Launch tokens and browser sessions.
package session

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
)

const (
	launchTokenTTL            = time.Minute
	browserSessionTTL         = 2 * time.Hour
	browserSessionMaxLifetime = 12 * time.Hour
	nonceTTL                  = 5 * time.Minute
	minimumSecretLen          = 32
)

var (
	ErrNotFound     = errors.New("session was not found")
	ErrExpired      = errors.New("session has expired")
	ErrUnauthorized = errors.New("session is invalid")
	ErrReplay       = errors.New("request has already been processed")
	ErrInvalidInput = errors.New("invalid session input")
)

// Launch contains the merchant-bound state held behind a one-time launch token.
type Launch struct {
	ID         string    `json:"id"`
	MerchantID string    `json:"merchant_id"`
	UserID     string    `json:"user_id"`
	Currency   string    `json:"currency"`
	WalletMode string    `json:"wallet_mode"`
	Locale     string    `json:"locale"`
	ReturnURL  string    `json:"return_url,omitempty"`
	ExpiresAt  time.Time `json:"expires_at"`
}

// BrowserSession is the server-side state for a signed browser credential.
type BrowserSession struct {
	ID         string    `json:"id"`
	MerchantID string    `json:"merchant_id"`
	UserID     string    `json:"user_id"`
	Currency   string    `json:"currency"`
	WalletMode string    `json:"wallet_mode"`
	Locale     string    `json:"locale"`
	ReturnURL  string    `json:"return_url,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
	ExpiresAt  time.Time `json:"expires_at"`
}

// Store provides the atomic operations required for secure launch sessions.
type Store interface {
	CreateLaunch(ctx context.Context, token string, launch Launch, ttl time.Duration) error
	ConsumeLaunch(ctx context.Context, token string) (Launch, error)
	CreateBrowserSession(ctx context.Context, value BrowserSession, ttl time.Duration) error
	GetBrowserSession(ctx context.Context, sessionID string) (BrowserSession, error)
	RevokeBrowserSession(ctx context.Context, sessionID string) error
	RevokeLaunch(ctx context.Context, merchantID, sessionID string) error
	ReserveNonce(ctx context.Context, merchantID, nonce string, ttl time.Duration) error
}

// Manager coordinates launch and browser sessions over a Store.
type Manager struct {
	store  Store
	key    []byte
	now    func() time.Time
	random io.Reader
}

// NewManager creates a Manager with a random source suitable for production.
func NewManager(store Store, secret []byte) (*Manager, error) {
	if store == nil || len(secret) < minimumSecretLen {
		return nil, ErrInvalidInput
	}
	key := append([]byte{}, secret...)
	return &Manager{
		store:  store,
		key:    key,
		now:    time.Now,
		random: rand.Reader,
	}, nil
}

// NewManagerFromEncodedSecret creates a Manager from a base64url configuration value.
func NewManagerFromEncodedSecret(store Store, encodedSecret string) (*Manager, error) {
	secret, err := base64.RawURLEncoding.DecodeString(strings.TrimSpace(encodedSecret))
	if err != nil || len(secret) < minimumSecretLen {
		return nil, errors.New("session JWT secret must be a base64url value of at least 32 bytes")
	}
	return NewManager(store, secret)
}

// CreateLaunch creates a one-time token and its short-lived server-side state.
func (m *Manager) CreateLaunch(
	ctx context.Context,
	merchantID string,
	userID string,
	currency string,
	walletMode string,
	locale string,
	returnURL string,
) (string, Launch, error) {
	launch, err := m.newLaunch(merchantID, userID, currency, walletMode, locale, returnURL)
	if err != nil {
		return "", Launch{}, err
	}
	token, err := m.randomToken("lt_")
	if err != nil {
		return "", Launch{}, err
	}
	if err := m.store.CreateLaunch(ctx, token, launch, launchTokenTTL); err != nil {
		return "", Launch{}, fmt.Errorf("store launch token: %w", err)
	}
	return token, launch, nil
}

// Exchange consumes a launch token exactly once and returns a short-lived JWT.
func (m *Manager) Exchange(ctx context.Context, launchToken string) (string, BrowserSession, error) {
	launchToken = strings.TrimSpace(launchToken)
	if launchToken == "" {
		return "", BrowserSession{}, ErrInvalidInput
	}
	launch, err := m.store.ConsumeLaunch(ctx, launchToken)
	if err != nil {
		return "", BrowserSession{}, err
	}
	now := m.now().UTC()
	if !now.Before(launch.ExpiresAt) {
		return "", BrowserSession{}, ErrExpired
	}
	value := BrowserSession{
		ID:         launch.ID,
		MerchantID: launch.MerchantID,
		UserID:     launch.UserID,
		Currency:   launch.Currency,
		WalletMode: launch.WalletMode,
		Locale:     launch.Locale,
		ReturnURL:  launch.ReturnURL,
		CreatedAt:  now,
		ExpiresAt:  now.Add(browserSessionTTL),
	}
	if err := m.store.CreateBrowserSession(ctx, value, browserSessionTTL); err != nil {
		return "", BrowserSession{}, fmt.Errorf("store browser session: %w", err)
	}
	token, err := m.sign(value)
	if err != nil {
		return "", BrowserSession{}, err
	}
	return token, value, nil
}

// Refresh extends a browser session by up to its two-hour TTL, without
// exceeding the twelve-hour maximum lifetime from its initial exchange.
func (m *Manager) Refresh(ctx context.Context, sessionID string) (string, BrowserSession, error) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return "", BrowserSession{}, ErrInvalidInput
	}
	value, err := m.store.GetBrowserSession(ctx, sessionID)
	if err != nil {
		return "", BrowserSession{}, err
	}
	now := m.now().UTC()
	if !now.Before(value.ExpiresAt) {
		return "", BrowserSession{}, ErrExpired
	}
	maximumExpiry := value.CreatedAt.Add(browserSessionMaxLifetime)
	if !now.Before(maximumExpiry) {
		return "", BrowserSession{}, ErrExpired
	}
	expiresAt := now.Add(browserSessionTTL)
	if expiresAt.After(maximumExpiry) {
		expiresAt = maximumExpiry
	}
	value.ExpiresAt = expiresAt
	if err := m.store.CreateBrowserSession(ctx, value, expiresAt.Sub(now)); err != nil {
		return "", BrowserSession{}, fmt.Errorf("refresh browser session: %w", err)
	}
	token, err := m.sign(value)
	if err != nil {
		return "", BrowserSession{}, err
	}
	return token, value, nil
}

// Validate verifies a browser JWT and confirms that its server-side session remains active.
func (m *Manager) Validate(ctx context.Context, token string) (BrowserSession, error) {
	claims, err := m.parse(token)
	if err != nil {
		return BrowserSession{}, err
	}
	now := m.now().UTC()
	if !now.Before(time.Unix(claims.ExpiresAt, 0)) {
		return BrowserSession{}, ErrExpired
	}
	value, err := m.store.GetBrowserSession(ctx, claims.SessionID)
	if err != nil {
		return BrowserSession{}, err
	}
	if !now.Before(value.ExpiresAt) {
		return BrowserSession{}, ErrExpired
	}
	if value.MerchantID != claims.MerchantID || value.UserID != claims.UserID || value.Currency != claims.Currency || value.WalletMode != claims.WalletMode {
		return BrowserSession{}, ErrUnauthorized
	}
	return value, nil
}

// ValidateUserSession implements auth.UserSessionValidator for HTTP middleware.
func (m *Manager) ValidateUserSession(ctx context.Context, token string) (*auth.UserSession, error) {
	value, err := m.Validate(ctx, token)
	if err != nil {
		return nil, err
	}
	return &auth.UserSession{
		ID:         value.ID,
		MerchantID: value.MerchantID,
		UserID:     value.UserID,
		Currency:   value.Currency,
		WalletMode: value.WalletMode,
		Locale:     value.Locale,
	}, nil
}

// Get returns a merchant-owned browser session for status inspection.
func (m *Manager) Get(ctx context.Context, merchantID, sessionID string) (BrowserSession, error) {
	value, err := m.store.GetBrowserSession(ctx, strings.TrimSpace(sessionID))
	if err != nil {
		return BrowserSession{}, err
	}
	if value.MerchantID != strings.TrimSpace(merchantID) {
		return BrowserSession{}, ErrNotFound
	}
	return value, nil
}

// Revoke invalidates a browser session only for its owning merchant.
func (m *Manager) Revoke(ctx context.Context, merchantID, sessionID string) error {
	value, err := m.Get(ctx, merchantID, sessionID)
	if errors.Is(err, ErrNotFound) {
		return m.store.RevokeLaunch(ctx, strings.TrimSpace(merchantID), strings.TrimSpace(sessionID))
	}
	if err != nil {
		return err
	}
	if err := m.store.RevokeBrowserSession(ctx, value.ID); err != nil {
		return fmt.Errorf("revoke browser session: %w", err)
	}
	return nil
}

// ReserveNonce makes a signed state-changing request idempotent for its replay window.
func (m *Manager) ReserveNonce(ctx context.Context, merchantID, nonce string) error {
	nonce = strings.TrimSpace(nonce)
	if merchantID == "" || nonce == "" || len(nonce) > 255 {
		return ErrInvalidInput
	}
	return m.store.ReserveNonce(ctx, merchantID, nonce, nonceTTL)
}

func (m *Manager) newLaunch(
	merchantID string,
	userID string,
	currency string,
	walletMode string,
	locale string,
	returnURL string,
) (Launch, error) {
	merchantID = strings.TrimSpace(merchantID)
	userID = strings.TrimSpace(userID)
	currency = strings.ToUpper(strings.TrimSpace(currency))
	walletMode = strings.TrimSpace(walletMode)
	locale = strings.TrimSpace(locale)
	if merchantID == "" || userID == "" || len(userID) > 255 || currency == "" || !validWalletMode(walletMode) || locale == "" || len(locale) > 35 {
		return Launch{}, ErrInvalidInput
	}
	id, err := m.randomToken("ps_")
	if err != nil {
		return Launch{}, err
	}
	return Launch{
		ID:         id,
		MerchantID: merchantID,
		UserID:     userID,
		Currency:   currency,
		WalletMode: walletMode,
		Locale:     locale,
		ReturnURL:  strings.TrimSpace(returnURL),
		ExpiresAt:  m.now().UTC().Add(launchTokenTTL),
	}, nil
}

func (m *Manager) randomToken(prefix string) (string, error) {
	buffer := make([]byte, 32)
	if _, err := io.ReadFull(m.random, buffer); err != nil {
		return "", fmt.Errorf("generate session token: %w", err)
	}
	return prefix + base64.RawURLEncoding.EncodeToString(buffer), nil
}

type tokenClaims struct {
	Issuer     string `json:"iss"`
	SessionID  string `json:"sid"`
	MerchantID string `json:"mid"`
	UserID     string `json:"sub"`
	Currency   string `json:"cur"`
	WalletMode string `json:"wm"`
	Locale     string `json:"loc"`
	IssuedAt   int64  `json:"iat"`
	ExpiresAt  int64  `json:"exp"`
}

func (m *Manager) sign(value BrowserSession) (string, error) {
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"HS256","typ":"JWT"}`))
	claims, err := json.Marshal(tokenClaims{
		Issuer:     "predictmarket-hosted",
		SessionID:  value.ID,
		MerchantID: value.MerchantID,
		UserID:     value.UserID,
		Currency:   value.Currency,
		WalletMode: value.WalletMode,
		Locale:     value.Locale,
		IssuedAt:   value.CreatedAt.Unix(),
		ExpiresAt:  value.ExpiresAt.Unix(),
	})
	if err != nil {
		return "", fmt.Errorf("encode session claims: %w", err)
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
	if claims.Issuer != "predictmarket-hosted" || claims.SessionID == "" || claims.MerchantID == "" || claims.UserID == "" || claims.Currency == "" || !validWalletMode(claims.WalletMode) || claims.ExpiresAt == 0 {
		return tokenClaims{}, ErrUnauthorized
	}
	return claims, nil
}

func validWalletMode(value string) bool {
	return value == "transfer" || value == "seamless"
}

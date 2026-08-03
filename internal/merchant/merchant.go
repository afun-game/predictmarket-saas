package merchant

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/mail"
	"os"
	"strings"
	"time"

	"github.com/afun-game/predictmarket-saas/internal/credentials"
	"github.com/afun-game/predictmarket-saas/pkg/types"
	"github.com/nxsky/twill"
	"golang.org/x/crypto/bcrypt"
)

const (
	defaultCurrency        = "USD"
	defaultTimezone        = "UTC"
	defaultPage            = 1
	defaultLimit           = 20
	maxLimit               = 100
	maxPage                = 1000
	apiKeyPrefixLen        = 16
	v3SecretRotationWindow = 7 * 24 * time.Hour
)

var (
	ErrNotFound      = errors.New("merchant not found")
	ErrUnauthorized  = errors.New("invalid API key")
	ErrV3Unavailable = errors.New("V3 merchant secret encryption is not configured")
)

// Service manages merchants and their API credentials.
type Service interface {
	Register(ctx context.Context, req *RegisterRequest) (*types.Merchant, error)
	Get(ctx context.Context, merchantID string) (*types.Merchant, error)
	Update(ctx context.Context, merchantID string, req *UpdateRequest) (*types.Merchant, error)
	UpdateStatus(ctx context.Context, merchantID, status string) (*types.Merchant, error)
	ConfigureIntegration(ctx context.Context, merchantID string, req *IntegrationRequest) (*types.Merchant, error)
	ReissueV3Secret(ctx context.Context, merchantID string) (*types.Merchant, error)
	ValidateAPIKey(ctx context.Context, apiKey string) (*types.Merchant, error)
	List(ctx context.Context, page, limit int) ([]*types.Merchant, error)
}

type RegisterRequest struct {
	twill.AutoMarshal

	Name     string `json:"name"`
	Email    string `json:"email"`
	Currency string `json:"currency"`
	Timezone string `json:"timezone"`
}

type UpdateRequest struct {
	twill.AutoMarshal

	Name     *string  `json:"name,omitempty"`
	Currency *string  `json:"currency,omitempty"`
	Timezone *string  `json:"timezone,omitempty"`
	FeeRate  *float64 `json:"fee_rate,omitempty"`
}

// IntegrationRequest configures V3 wallet mode and merchant callback endpoints.
// Only administrators may apply these fields.
type IntegrationRequest struct {
	twill.AutoMarshal

	WalletMode           *string  `json:"wallet_mode,omitempty"`
	CallbackURL          *string  `json:"callback_url,omitempty"`
	WebhookURL           *string  `json:"webhook_url,omitempty"`
	WebhookEvents        []string `json:"webhook_events,omitempty"`
	RotateCallbackSecret bool     `json:"rotate_callback_secret,omitempty"`
}

// ValidationError identifies an invalid request field.
type ValidationError struct {
	Field   string
	Message string
}

func (e *ValidationError) Error() string {
	return fmt.Sprintf("invalid %s: %s", e.Field, e.Message)
}

type implementation struct {
	twill.Implements[Service]

	database      twill.Database `twill:"primary-db"`
	repository    Repository
	random        io.Reader
	now           func() time.Time
	encryptSecret func(string) (string, error)
}

// NewService creates a merchant service backed by an in-memory repository.
func NewService() Service {
	return newService(newMemoryRepository())
}

func newService(repository Repository) *implementation {
	return &implementation{
		repository:    repository,
		random:        rand.Reader,
		now:           time.Now,
		encryptSecret: encryptAPISecret,
	}
}

func (s *implementation) Init(context.Context) error {
	if s.repository == nil {
		database := s.database.Get()
		if database == nil || database.StdDB() == nil {
			return errors.New("primary database is not configured")
		}
		s.repository = newPostgresRepository(database.StdDB())
	}
	if s.random == nil {
		s.random = rand.Reader
	}
	if s.now == nil {
		s.now = time.Now
	}
	if s.encryptSecret == nil {
		s.encryptSecret = encryptAPISecret
	}
	return nil
}

func (s *implementation) Register(
	ctx context.Context,
	req *RegisterRequest,
) (*types.Merchant, error) {
	input, err := validateRegisterRequest(req)
	if err != nil {
		return nil, err
	}

	merchantID, err := generateUUID(s.random)
	if err != nil {
		return nil, fmt.Errorf("generate merchant ID: %w", err)
	}
	apiKey, err := generateCredential(s.random, "pk_live_")
	if err != nil {
		return nil, fmt.Errorf("generate API key: %w", err)
	}
	apiSecret, err := generateCredential(s.random, "sk_live_")
	if err != nil {
		return nil, fmt.Errorf("generate API secret: %w", err)
	}
	apiKeyHash, err := hashAPIKey(apiKey)
	if err != nil {
		return nil, fmt.Errorf("hash API key: %w", err)
	}
	apiKeyPrefix, err := keyPrefix(apiKey)
	if err != nil {
		return nil, fmt.Errorf("derive API key prefix: %w", err)
	}
	apiSecretEncrypted, err := s.encryptSecret(apiSecret)
	if err != nil {
		return nil, fmt.Errorf("encrypt API secret: %w", err)
	}

	now := s.now().UTC()
	stored := &types.Merchant{
		ID:                 merchantID,
		Name:               input.Name,
		Email:              input.Email,
		APIKey:             apiKeyHash,
		APIKeyPrefix:       apiKeyPrefix,
		APISecret:          hashSecret(apiSecret),
		APISecretEncrypted: apiSecretEncrypted,
		Status:             "active",
		Currency:           input.Currency,
		Timezone:           input.Timezone,
		WalletMode:         "transfer",
		FeeRate:            0,
		CreatedAt:          now,
		UpdatedAt:          now,
	}
	if err := s.repository.Create(ctx, stored); err != nil {
		return nil, fmt.Errorf("create merchant: %w", err)
	}

	result := cloneMerchant(stored)
	result.APIKey = apiKey
	result.APISecret = apiSecret
	return result, nil
}

func (s *implementation) Get(ctx context.Context, merchantID string) (*types.Merchant, error) {
	if strings.TrimSpace(merchantID) == "" {
		return nil, &ValidationError{Field: "merchant_id", Message: "is required"}
	}
	merchant, err := s.repository.GetByID(ctx, merchantID)
	if err != nil {
		return nil, fmt.Errorf("get merchant: %w", err)
	}
	merchant.APISecret = ""
	merchant.APIKey = ""
	return merchant, nil
}

func (s *implementation) Update(
	ctx context.Context,
	merchantID string,
	req *UpdateRequest,
) (*types.Merchant, error) {
	if strings.TrimSpace(merchantID) == "" {
		return nil, &ValidationError{Field: "merchant_id", Message: "is required"}
	}
	if req == nil {
		return nil, &ValidationError{Field: "request", Message: "is required"}
	}

	merchant, err := s.repository.GetByID(ctx, merchantID)
	if err != nil {
		return nil, fmt.Errorf("get merchant for update: %w", err)
	}
	if err := applyUpdate(merchant, req); err != nil {
		return nil, err
	}
	merchant.UpdatedAt = s.now().UTC()
	if err := s.repository.Update(ctx, merchant); err != nil {
		return nil, fmt.Errorf("update merchant: %w", err)
	}
	merchant.APISecret = ""
	merchant.APIKey = ""
	return merchant, nil
}

// UpdateStatus changes a merchant's lifecycle status (active, suspended,
// inactive). Suspended merchants are refused at every API authentication
// boundary (see ValidateAPIKey).
func (s *implementation) UpdateStatus(ctx context.Context, merchantID, status string) (*types.Merchant, error) {
	merchantID = strings.TrimSpace(merchantID)
	status = strings.ToLower(strings.TrimSpace(status))
	if merchantID == "" {
		return nil, &ValidationError{Field: "merchant_id", Message: "is required"}
	}
	if status != "active" && status != "suspended" && status != "inactive" {
		return nil, &ValidationError{Field: "status", Message: "must be active, suspended or inactive"}
	}
	merchant, err := s.repository.GetByID(ctx, merchantID)
	if err != nil {
		return nil, fmt.Errorf("get merchant for status update: %w", err)
	}
	if merchant.Status == status {
		merchant.APISecret = ""
		merchant.APIKey = ""
		return merchant, nil
	}
	merchant.Status = status
	merchant.UpdatedAt = s.now().UTC()
	if err := s.repository.Update(ctx, merchant); err != nil {
		return nil, fmt.Errorf("update merchant status: %w", err)
	}
	merchant.APISecret = ""
	merchant.APIKey = ""
	return merchant, nil
}

// ConfigureIntegration updates admin-only V3 integration parameters. When a
// callback secret is rotated, the cleartext value is returned exactly once.
func (s *implementation) ConfigureIntegration(
	ctx context.Context,
	merchantID string,
	req *IntegrationRequest,
) (*types.Merchant, error) {
	merchantID = strings.TrimSpace(merchantID)
	if merchantID == "" {
		return nil, &ValidationError{Field: "merchant_id", Message: "is required"}
	}
	if req == nil {
		return nil, &ValidationError{Field: "request", Message: "is required"}
	}
	stored, err := s.repository.GetByID(ctx, merchantID)
	if err != nil {
		return nil, fmt.Errorf("get merchant for integration update: %w", err)
	}
	callbackSecret := ""
	previousCallbackURL := stored.CallbackURL
	if err := applyIntegration(stored, req); err != nil {
		return nil, err
	}
	if req.CallbackURL != nil && strings.TrimSpace(*req.CallbackURL) != previousCallbackURL {
		// A changed callback address invalidates the ownership proof; the
		// merchant must pass verify-callback again before seamless orders resume.
		stored.CallbackVerifiedAt = nil
	}
	if req.RotateCallbackSecret || (stored.WalletMode == "seamless" && stored.CallbackSecretEncrypted == "") {
		callbackSecret, err = generateCredential(s.random, "cb_live_")
		if err != nil {
			return nil, fmt.Errorf("generate callback secret: %w", err)
		}
		encrypted, err := s.encryptSecret(callbackSecret)
		if err != nil {
			return nil, fmt.Errorf("encrypt callback secret: %w", err)
		}
		if encrypted == "" {
			return nil, ErrV3Unavailable
		}
		stored.CallbackSecretEncrypted = encrypted
	}
	stored.UpdatedAt = s.now().UTC()
	if err := s.repository.Update(ctx, stored); err != nil {
		return nil, fmt.Errorf("update merchant integration: %w", err)
	}
	result := cloneMerchant(stored)
	result.APIKey = ""
	result.APISecret = ""
	result.CallbackSecret = callbackSecret
	return result, nil
}

// ReissueV3Secret creates a new HMAC secret and permits the prior secret for
// a short migration window. The cleartext replacement is returned exactly once.
func (s *implementation) ReissueV3Secret(ctx context.Context, merchantID string) (*types.Merchant, error) {
	merchantID = strings.TrimSpace(merchantID)
	if merchantID == "" {
		return nil, &ValidationError{Field: "merchant_id", Message: "is required"}
	}
	stored, err := s.repository.GetByID(ctx, merchantID)
	if err != nil {
		return nil, fmt.Errorf("get merchant for V3 secret reissue: %w", err)
	}
	secret, err := generateCredential(s.random, "sk_live_")
	if err != nil {
		return nil, fmt.Errorf("generate V3 API secret: %w", err)
	}
	encrypted, err := s.encryptSecret(secret)
	if err != nil {
		return nil, fmt.Errorf("encrypt V3 API secret: %w", err)
	}
	if encrypted == "" {
		return nil, ErrV3Unavailable
	}
	now := s.now().UTC()
	if stored.APISecretEncrypted != "" {
		expiresAt := now.Add(v3SecretRotationWindow)
		stored.APISecretSecondaryEncrypted = stored.APISecretEncrypted
		stored.APISecretSecondaryExpiresAt = &expiresAt
	} else {
		stored.APISecretSecondaryEncrypted = ""
		stored.APISecretSecondaryExpiresAt = nil
	}
	stored.APISecret = hashSecret(secret)
	stored.APISecretEncrypted = encrypted
	stored.UpdatedAt = now
	if err := s.repository.Update(ctx, stored); err != nil {
		return nil, fmt.Errorf("update V3 API secret: %w", err)
	}
	result := cloneMerchant(stored)
	result.APIKey = ""
	result.APISecret = secret
	return result, nil
}

func (s *implementation) ValidateAPIKey(ctx context.Context, apiKey string) (*types.Merchant, error) {
	apiKey = strings.TrimSpace(apiKey)
	if apiKey == "" {
		return nil, ErrUnauthorized
	}

	prefix, err := keyPrefix(apiKey)
	if err != nil {
		return nil, ErrUnauthorized
	}
	merchant, err := s.repository.GetByAPIKeyPrefix(ctx, prefix)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, ErrUnauthorized
		}
		return nil, fmt.Errorf("validate API key: %w", err)
	}
	if err := bcrypt.CompareHashAndPassword([]byte(merchant.APIKey), []byte(apiKey)); err != nil {
		return nil, ErrUnauthorized
	}
	if merchant.Status != "active" {
		return nil, ErrUnauthorized
	}
	merchant.APISecret = ""
	merchant.APIKey = ""
	return merchant, nil
}

func (s *implementation) List(ctx context.Context, page, limit int) ([]*types.Merchant, error) {
	if page == 0 {
		page = defaultPage
	}
	if limit == 0 {
		limit = defaultLimit
	}
	if page < 1 {
		return nil, &ValidationError{Field: "page", Message: "must be at least 1"}
	}
	if page > maxPage {
		return nil, &ValidationError{Field: "page", Message: "must not exceed 1000"}
	}
	if limit < 1 || limit > maxLimit {
		return nil, &ValidationError{Field: "limit", Message: "must be between 1 and 100"}
	}

	merchants, err := s.repository.List(ctx, (page-1)*limit, limit)
	if err != nil {
		return nil, fmt.Errorf("list merchants: %w", err)
	}
	for _, merchant := range merchants {
		merchant.APISecret = ""
		merchant.APIKey = ""
	}
	return merchants, nil
}

func validateRegisterRequest(req *RegisterRequest) (*RegisterRequest, error) {
	if req == nil {
		return nil, &ValidationError{Field: "request", Message: "is required"}
	}

	input := *req
	input.Name = strings.TrimSpace(input.Name)
	input.Email = strings.ToLower(strings.TrimSpace(input.Email))
	input.Currency = strings.ToUpper(strings.TrimSpace(input.Currency))
	input.Timezone = strings.TrimSpace(input.Timezone)
	if input.Name == "" {
		return nil, &ValidationError{Field: "name", Message: "is required"}
	}
	if !validEmail(input.Email) {
		return nil, &ValidationError{Field: "email", Message: "must be a valid email address"}
	}
	if input.Currency == "" {
		input.Currency = defaultCurrency
	}
	if !validCurrency(input.Currency) {
		return nil, &ValidationError{Field: "currency", Message: "is not supported"}
	}
	if input.Timezone == "" {
		input.Timezone = defaultTimezone
	}
	if _, err := time.LoadLocation(input.Timezone); err != nil {
		return nil, &ValidationError{Field: "timezone", Message: "must be an IANA timezone"}
	}
	return &input, nil
}

func applyUpdate(merchant *types.Merchant, req *UpdateRequest) error {
	if req.Name != nil {
		name := strings.TrimSpace(*req.Name)
		if name == "" {
			return &ValidationError{Field: "name", Message: "cannot be empty"}
		}
		merchant.Name = name
	}
	if req.Currency != nil {
		currency := strings.ToUpper(strings.TrimSpace(*req.Currency))
		if !validCurrency(currency) {
			return &ValidationError{Field: "currency", Message: "is not supported"}
		}
		merchant.Currency = currency
	}
	if req.Timezone != nil {
		timezone := strings.TrimSpace(*req.Timezone)
		if _, err := time.LoadLocation(timezone); err != nil {
			return &ValidationError{Field: "timezone", Message: "must be an IANA timezone"}
		}
		merchant.Timezone = timezone
	}
	if req.FeeRate != nil {
		rate := *req.FeeRate
		if rate < 0 || rate > 1 {
			return &ValidationError{Field: "fee_rate", Message: "must be between 0 and 1"}
		}
		merchant.FeeRate = rate
	}
	return nil
}

func applyIntegration(merchant *types.Merchant, req *IntegrationRequest) error {
	if req.WalletMode != nil {
		mode := strings.ToLower(strings.TrimSpace(*req.WalletMode))
		if mode != "transfer" && mode != "seamless" {
			return &ValidationError{Field: "wallet_mode", Message: "must be transfer or seamless"}
		}
		merchant.WalletMode = mode
	}
	if req.CallbackURL != nil {
		urlValue := strings.TrimSpace(*req.CallbackURL)
		if urlValue != "" && !validHTTPSURL(urlValue) {
			return &ValidationError{Field: "callback_url", Message: "must be an absolute HTTPS URL"}
		}
		merchant.CallbackURL = urlValue
	}
	if req.WebhookURL != nil {
		urlValue := strings.TrimSpace(*req.WebhookURL)
		if urlValue != "" && !validHTTPSURL(urlValue) {
			return &ValidationError{Field: "webhook_url", Message: "must be an absolute HTTPS URL"}
		}
		merchant.WebhookURL = urlValue
	}
	if req.WebhookEvents != nil {
		events := make([]string, 0, len(req.WebhookEvents))
		for _, event := range req.WebhookEvents {
			event = strings.TrimSpace(event)
			switch event {
			case "order.settled", "order.voided", "market.settled", "market.voided":
				events = append(events, event)
			case "":
				continue
			default:
				return &ValidationError{Field: "webhook_events", Message: "contains an unsupported event"}
			}
		}
		merchant.WebhookEvents = events
	}
	if merchant.WalletMode == "seamless" && merchant.CallbackURL == "" {
		return &ValidationError{Field: "callback_url", Message: "is required for seamless wallet mode"}
	}
	return nil
}

func validHTTPSURL(value string) bool {
	return strings.HasPrefix(value, "https://") && !strings.Contains(value, " ")
}

func validEmail(value string) bool {
	address, err := mail.ParseAddress(value)
	return err == nil && address.Address == value
}

func validCurrency(value string) bool {
	switch value {
	case "USD", "EUR", "CNY", "JPY", "GBP", "MXN":
		return true
	default:
		return false
	}
}

func generateCredential(random io.Reader, prefix string) (string, error) {
	buffer := make([]byte, 32)
	if _, err := io.ReadFull(random, buffer); err != nil {
		return "", err
	}
	return prefix + base64.RawURLEncoding.EncodeToString(buffer), nil
}

func generateUUID(random io.Reader) (string, error) {
	buffer := make([]byte, 16)
	if _, err := io.ReadFull(random, buffer); err != nil {
		return "", err
	}
	buffer[6] = (buffer[6] & 0x0f) | 0x40
	buffer[8] = (buffer[8] & 0x3f) | 0x80
	return fmt.Sprintf(
		"%x-%x-%x-%x-%x",
		buffer[0:4],
		buffer[4:6],
		buffer[6:8],
		buffer[8:10],
		buffer[10:16],
	), nil
}

func hashSecret(secret string) string {
	digest := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(digest[:])
}

func encryptAPISecret(secret string) (string, error) {
	encodedKey := strings.TrimSpace(os.Getenv("MERCHANT_SECRET_ENCRYPTION_KEY"))
	if encodedKey == "" {
		return "", nil
	}
	protector, err := credentials.NewProtector(encodedKey)
	if err != nil {
		return "", err
	}
	return protector.Encrypt(secret)
}

func hashAPIKey(apiKey string) (string, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(apiKey), bcrypt.DefaultCost)
	if err != nil {
		return "", err
	}
	return string(hash), nil
}

func keyPrefix(apiKey string) (string, error) {
	if len(apiKey) < apiKeyPrefixLen {
		return "", ErrUnauthorized
	}
	return apiKey[:apiKeyPrefixLen], nil
}

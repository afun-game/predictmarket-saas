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
	"strings"
	"time"

	"github.com/nxsky/twill"
	"github.com/afun-game/predictmarket-saas/pkg/types"
	"golang.org/x/crypto/bcrypt"
)

const (
	defaultCurrency = "USD"
	defaultTimezone = "UTC"
	defaultPage     = 1
	defaultLimit    = 20
	maxLimit        = 100
	maxPage         = 1000
	apiKeyPrefixLen = 16
)

var (
	ErrNotFound     = errors.New("merchant not found")
	ErrUnauthorized = errors.New("invalid API key")
)

// Service manages merchants and their API credentials.
type Service interface {
	Register(ctx context.Context, req *RegisterRequest) (*types.Merchant, error)
	Get(ctx context.Context, merchantID string) (*types.Merchant, error)
	Update(ctx context.Context, merchantID string, req *UpdateRequest) (*types.Merchant, error)
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

	Name     *string `json:"name,omitempty"`
	Currency *string `json:"currency,omitempty"`
	Timezone *string `json:"timezone,omitempty"`
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

	database   twill.Database `twill:"primary-db"`
	repository Repository
	random     io.Reader
	now        func() time.Time
}

// NewService creates a merchant service backed by an in-memory repository.
func NewService() Service {
	return newService(newMemoryRepository())
}

func newService(repository Repository) *implementation {
	return &implementation{
		repository: repository,
		random:     rand.Reader,
		now:        time.Now,
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

	now := s.now().UTC()
	stored := &types.Merchant{
		ID:           merchantID,
		Name:         input.Name,
		Email:        input.Email,
		APIKey:       apiKeyHash,
		APIKeyPrefix: apiKeyPrefix,
		APISecret:    hashSecret(apiSecret),
		Status:       "active",
		Currency:     input.Currency,
		Timezone:     input.Timezone,
		FeeRate:      0,
		CreatedAt:    now,
		UpdatedAt:    now,
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
	return nil
}

func validEmail(value string) bool {
	address, err := mail.ParseAddress(value)
	return err == nil && address.Address == value
}

func validCurrency(value string) bool {
	switch value {
	case "USD", "EUR", "CNY", "JPY", "GBP":
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

package wallet

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/nxsky/twill"
	"github.com/afun-game/predictmarket-saas/pkg/fixed"
	"github.com/afun-game/predictmarket-saas/pkg/types"
)

const (
	defaultPage          = 1
	defaultLimit         = 20
	maxLimit             = 100
	maxPage              = 1000
	maxUserIDLen         = 255
	maxIdempotencyKeyLen = 255
)

var (
	ErrNotFound            = errors.New("wallet not found")
	ErrAlreadyExists       = errors.New("wallet already exists")
	ErrInvalidMerchant     = errors.New("merchant is not active")
	ErrInsufficientBalance = errors.New("insufficient available balance")
	ErrInsufficientLocked  = errors.New("insufficient locked balance")
)

// Service manages virtual credit wallets (Play Money).
type Service interface {
	Create(ctx context.Context, merchantID, userID, currency string) (*types.Wallet, error)
	Get(ctx context.Context, merchantID, userID, currency string) (*types.Wallet, error)
	GetBalance(ctx context.Context, merchantID, userID, currency string) (float64, float64, error)
	Credit(ctx context.Context, merchantID, userID, currency string, amount float64, txType string) error
	CreditWithIdempotency(
		ctx context.Context,
		merchantID string,
		userID string,
		currency string,
		amount float64,
		txType string,
		idempotencyKey string,
	) error
	Debit(ctx context.Context, merchantID, userID, currency string, amount float64, txType string) error
	Lock(ctx context.Context, merchantID, userID, currency string, amount float64) error
	Unlock(ctx context.Context, merchantID, userID, currency string, amount float64) error
	ListTransactions(ctx context.Context, merchantID, userID string, page, limit int) ([]*types.Transaction, int, error)
}

// ValidationError identifies an invalid wallet request field.
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

// NewService creates a Wallet Service backed by an in-memory repository.
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

func (s *implementation) Create(
	ctx context.Context,
	merchantID string,
	userID string,
	currency string,
) (*types.Wallet, error) {
	key, err := validateWalletKey(merchantID, userID, currency)
	if err != nil {
		return nil, err
	}
	if err := s.repository.ValidateMerchant(ctx, key.MerchantID); err != nil {
		return nil, fmt.Errorf("validate wallet merchant: %w", err)
	}
	walletID, err := generateUUID(s.random)
	if err != nil {
		return nil, fmt.Errorf("generate wallet ID: %w", err)
	}
	value := &types.Wallet{
		ID:            walletID,
		MerchantID:    key.MerchantID,
		UserID:        key.UserID,
		Currency:      key.Currency,
		Balance:       0,
		LockedBalance: 0,
		UpdatedAt:     s.now().UTC(),
	}
	if err := s.repository.Create(ctx, value); err != nil {
		return nil, fmt.Errorf("create wallet: %w", err)
	}
	return cloneWallet(value), nil
}

func (s *implementation) Get(
	ctx context.Context,
	merchantID string,
	userID string,
	currency string,
) (*types.Wallet, error) {
	key, err := validateWalletKey(merchantID, userID, currency)
	if err != nil {
		return nil, err
	}
	value, err := s.repository.Get(ctx, key)
	if err != nil {
		return nil, fmt.Errorf("get wallet: %w", err)
	}
	return value, nil
}

func (s *implementation) GetBalance(
	ctx context.Context,
	merchantID string,
	userID string,
	currency string,
) (float64, float64, error) {
	value, err := s.Get(ctx, merchantID, userID, currency)
	if err != nil {
		return 0, 0, err
	}
	return value.Balance, value.LockedBalance, nil
}

func (s *implementation) Credit(
	ctx context.Context,
	merchantID string,
	userID string,
	currency string,
	amount float64,
	txType string,
) error {
	return s.CreditWithIdempotency(ctx, merchantID, userID, currency, amount, txType, "")
}

func (s *implementation) CreditWithIdempotency(
	ctx context.Context,
	merchantID string,
	userID string,
	currency string,
	amount float64,
	txType string,
	idempotencyKey string,
) error {
	key, err := validateWalletKey(merchantID, userID, currency)
	if err != nil {
		return err
	}
	amount, err = normalizeAmount(amount)
	if err != nil {
		return err
	}
	txType, err = validateTransactionType(txType, true)
	if err != nil {
		return err
	}
	if err := s.repository.ValidateMerchant(ctx, key.MerchantID); err != nil {
		return fmt.Errorf("validate wallet merchant: %w", err)
	}
	idempotencyKey, err = validateIdempotencyKey(idempotencyKey)
	if err != nil {
		return err
	}

	walletID, transactionID, err := s.generateOperationIDs()
	if err != nil {
		return err
	}
	now := s.now().UTC()
	wallet := &types.Wallet{
		ID:         walletID,
		MerchantID: key.MerchantID,
		UserID:     key.UserID,
		Currency:   key.Currency,
		UpdatedAt:  now,
	}
	transaction := newTransaction(
		transactionID,
		key.Currency,
		txType,
		amount,
		idempotencyKey,
		now,
	)
	if err := s.repository.Credit(ctx, wallet, transaction); err != nil {
		if idempotencyKey != "" && errors.Is(err, ErrAlreadyExists) {
			return nil
		}
		return fmt.Errorf("credit wallet: %w", err)
	}
	return nil
}

func (s *implementation) Debit(
	ctx context.Context,
	merchantID string,
	userID string,
	currency string,
	amount float64,
	txType string,
) error {
	key, err := validateWalletKey(merchantID, userID, currency)
	if err != nil {
		return err
	}
	amount, err = normalizeAmount(amount)
	if err != nil {
		return err
	}
	txType, err = validateTransactionType(txType, false)
	if err != nil {
		return err
	}
	transactionID, err := generateUUID(s.random)
	if err != nil {
		return fmt.Errorf("generate transaction ID: %w", err)
	}
	now := s.now().UTC()
	transaction := newTransaction(transactionID, key.Currency, txType, amount, "", now)
	if err := s.repository.Debit(ctx, key, amount, transaction, now); err != nil {
		return fmt.Errorf("debit wallet: %w", err)
	}
	return nil
}

func (s *implementation) Lock(
	ctx context.Context,
	merchantID string,
	userID string,
	currency string,
	amount float64,
) error {
	key, err := validateWalletKey(merchantID, userID, currency)
	if err != nil {
		return err
	}
	amount, err = normalizeAmount(amount)
	if err != nil {
		return err
	}
	if err := s.repository.Lock(ctx, key, amount, s.now().UTC()); err != nil {
		return fmt.Errorf("lock wallet balance: %w", err)
	}
	return nil
}

func (s *implementation) Unlock(
	ctx context.Context,
	merchantID string,
	userID string,
	currency string,
	amount float64,
) error {
	key, err := validateWalletKey(merchantID, userID, currency)
	if err != nil {
		return err
	}
	amount, err = normalizeAmount(amount)
	if err != nil {
		return err
	}
	if err := s.repository.Unlock(ctx, key, amount, s.now().UTC()); err != nil {
		return fmt.Errorf("unlock wallet balance: %w", err)
	}
	return nil
}

func (s *implementation) ListTransactions(
	ctx context.Context,
	merchantID string,
	userID string,
	page int,
	limit int,
) ([]*types.Transaction, int, error) {
	merchantID, userID, err := validateOwner(merchantID, userID)
	if err != nil {
		return nil, 0, err
	}
	page, limit, err = normalizePagination(page, limit)
	if err != nil {
		return nil, 0, err
	}
	values, total, err := s.repository.ListTransactions(
		ctx,
		merchantID,
		userID,
		(page-1)*limit,
		limit,
	)
	if err != nil {
		return nil, 0, fmt.Errorf("list wallet transactions: %w", err)
	}
	return values, total, nil
}

func (s *implementation) generateOperationIDs() (string, string, error) {
	walletID, err := generateUUID(s.random)
	if err != nil {
		return "", "", fmt.Errorf("generate wallet ID: %w", err)
	}
	transactionID, err := generateUUID(s.random)
	if err != nil {
		return "", "", fmt.Errorf("generate transaction ID: %w", err)
	}
	return walletID, transactionID, nil
}

func newTransaction(
	id string,
	currency string,
	txType string,
	amount float64,
	idempotencyKey string,
	now time.Time,
) *types.Transaction {
	return &types.Transaction{
		ID:             id,
		Type:           txType,
		Amount:         amount,
		Currency:       currency,
		IdempotencyKey: idempotencyKey,
		Status:         "completed",
		CreatedAt:      now,
	}
}

func validateIdempotencyKey(value string) (string, error) {
	value = strings.TrimSpace(value)
	if len(value) > maxIdempotencyKeyLen {
		return "", &ValidationError{Field: "idempotency_key", Message: "must be at most 255 characters"}
	}
	return value, nil
}

func validateWalletKey(merchantID, userID, currency string) (walletKey, error) {
	merchantID, userID, err := validateOwner(merchantID, userID)
	if err != nil {
		return walletKey{}, err
	}
	currency = strings.ToUpper(strings.TrimSpace(currency))
	if !validCurrency(currency) {
		return walletKey{}, &ValidationError{Field: "currency", Message: "is not supported"}
	}
	return walletKey{MerchantID: merchantID, UserID: userID, Currency: currency}, nil
}

func validateOwner(merchantID, userID string) (string, string, error) {
	merchantID = strings.TrimSpace(merchantID)
	userID = strings.TrimSpace(userID)
	if !isUUID(merchantID) {
		return "", "", &ValidationError{Field: "merchant_id", Message: "must be a UUID"}
	}
	if userID == "" {
		return "", "", &ValidationError{Field: "user_id", Message: "is required"}
	}
	if len(userID) > maxUserIDLen {
		return "", "", &ValidationError{Field: "user_id", Message: "must be at most 255 characters"}
	}
	return merchantID, userID, nil
}

func validateAmount(amount float64) error {
	if _, err := normalizeAmount(amount); err != nil {
		return &ValidationError{Field: "amount", Message: "must be greater than 0 and within the supported range"}
	}
	return nil
}

func normalizeAmount(amount float64) (float64, error) {
	cents, err := fixed.CentsFromFloat(amount)
	if err != nil {
		return 0, &ValidationError{Field: "amount", Message: "must be greater than 0 with at most 2 decimal places"}
	}
	return fixed.CentsToFloat(cents), nil
}

func validateTransactionType(txType string, credit bool) (string, error) {
	txType = strings.ToLower(strings.TrimSpace(txType))
	valid := txType == "credit" || txType == "win"
	if !credit {
		valid = txType == "debit" || txType == "bet" || txType == "fee"
	}
	if !valid {
		return "", &ValidationError{Field: "transaction_type", Message: "is not supported for this operation"}
	}
	return txType, nil
}

func normalizePagination(page, limit int) (int, int, error) {
	if page == 0 {
		page = defaultPage
	}
	if limit == 0 {
		limit = defaultLimit
	}
	if page < 1 {
		return 0, 0, &ValidationError{Field: "page", Message: "must be at least 1"}
	}
	if page > maxPage {
		return 0, 0, &ValidationError{Field: "page", Message: "must not exceed 1000"}
	}
	if limit < 1 || limit > maxLimit {
		return 0, 0, &ValidationError{Field: "limit", Message: "must be between 1 and 100"}
	}
	return page, limit, nil
}

func validCurrency(currency string) bool {
	switch currency {
	case "USD", "EUR", "CNY", "JPY", "GBP":
		return true
	default:
		return false
	}
}

func isUUID(value string) bool {
	if len(value) != 36 || value[8] != '-' || value[13] != '-' || value[18] != '-' || value[23] != '-' {
		return false
	}
	compact := strings.ReplaceAll(value, "-", "")
	_, err := hex.DecodeString(compact)
	return err == nil
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

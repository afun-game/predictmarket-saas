package callback

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"github.com/afun-game/predictmarket-saas/internal/credentials"
	"github.com/afun-game/predictmarket-saas/pkg/fixed"
)

const (
	defaultBatchSize        = 20
	defaultDispatchEvery    = time.Second
	defaultMaxAttempts      = 5
	defaultRetryInitial     = time.Second
	defaultRetryMaxDelay    = 5 * time.Minute
	defaultDegradeThreshold = 5
)

// ErrNotFound indicates a missing callback transaction or dead-letter row.
var ErrNotFound = errors.New("callback record not found")

// Service delivers pending merchant callbacks/webhooks and supports runbook replay.
type Service interface {
	Init(ctx context.Context) error
	Dispatch(ctx context.Context) (int, error)
	GetTransaction(ctx context.Context, merchantID, transactionID string) (*TransactionHistory, error)
	GetLatestBalance(ctx context.Context, merchantID, userID, currency string) (string, error)
	ReplayDeadLetter(ctx context.Context, channel, outboxID string) error
	// VerifyCallback proves merchant ownership of the configured callback URL
	// by requiring an echo of a signed challenge (V3 §7.2).
	VerifyCallback(ctx context.Context, merchantID string) error
	// ResetDegraded clears a merchant's seamless degraded state after the
	// operator confirms callback health (V3 §10.1).
	ResetDegraded(ctx context.Context, merchantID string) error
	// QueryBalance asks the merchant for the authoritative user balance
	// (V3 §11.2 real-time balance callback).
	QueryBalance(ctx context.Context, merchantID, userID, currency string) (string, error)
	EnqueueRollback(
		ctx context.Context,
		merchantID string,
		userID string,
		currency string,
		amountCents int64,
		transactionID string,
		orderID string,
		marketID string,
	) error
}

type implementation struct {
	repository *repository
	client     *Client
	protector  *credentials.Protector
	now        func() time.Time
	interval   time.Duration
	batchSize  int
	maxAttempt int
	retryStart time.Duration
	retryMax   time.Duration

	degraded *degradedTracker
}

// NewWithDB constructs a callback service bound to a concrete SQL database.
func NewWithDB(database *sql.DB, encodedKey string, allowPrivateURLs bool) (Service, error) {
	if database == nil {
		return nil, errors.New("callback database is not configured")
	}
	protector, err := credentials.NewProtector(encodedKey)
	if err != nil {
		return nil, fmt.Errorf("configure callback secret encryption: %w", err)
	}
	service := &implementation{
		repository: newRepository(database),
		client:     newClient(defaultTimeout, allowPrivateURLs),
		protector:  protector,
		now:        time.Now,
		interval:   defaultDispatchEvery,
		batchSize:  defaultBatchSize,
		maxAttempt: defaultMaxAttempts,
		retryStart: defaultRetryInitial,
		retryMax:   defaultRetryMaxDelay,
	}
	service.degraded = &degradedTracker{
		persistence: service.repository,
		threshold:   defaultDegradeThreshold,
	}
	return service, nil
}

func (s *implementation) Init(ctx context.Context) error {
	if s.repository == nil || s.protector == nil {
		return errors.New("callback service is not configured")
	}
	if s.client == nil {
		s.client = newClient(defaultTimeout, false)
	}
	if s.now == nil {
		s.now = time.Now
	}
	if s.interval <= 0 {
		s.interval = defaultDispatchEvery
	}
	if s.batchSize <= 0 {
		s.batchSize = defaultBatchSize
	}
	if s.maxAttempt <= 0 {
		s.maxAttempt = defaultMaxAttempts
	}
	if s.retryStart <= 0 {
		s.retryStart = defaultRetryInitial
	}
	if s.retryMax <= 0 {
		s.retryMax = defaultRetryMaxDelay
	}
	if s.degraded == nil {
		s.degraded = &degradedTracker{
			persistence: s.repository,
			threshold:   defaultDegradeThreshold,
		}
	}
	go s.runSafely(ctx, "callback dispatcher", s.runDispatcher)
	return nil
}

func (s *implementation) runSafely(ctx context.Context, name string, run func(context.Context)) {
	defer func() {
		if recovered := recover(); recovered != nil {
			slog.ErrorContext(
				ctx,
				"background worker panic recovered",
				"worker", name,
				"panic", recovered,
				"stack", string(debug.Stack()),
			)
		}
	}()
	run(ctx)
}

func (s *implementation) runDispatcher(ctx context.Context) {
	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			count, err := s.Dispatch(ctx)
			if err != nil {
				slog.ErrorContext(ctx, "merchant callback dispatch failed", "error", err)
				continue
			}
			if count > 0 {
				slog.InfoContext(ctx, "merchant callbacks dispatched", "messages", count)
			}
		}
	}
}

func (s *implementation) Dispatch(ctx context.Context) (int, error) {
	if s.repository == nil || s.client == nil || s.protector == nil {
		return 0, errors.New("callback dispatcher is not configured")
	}
	now := s.now().UTC()
	callbacks, err := s.repository.ClaimCallbackBatch(ctx, s.batchSize, now)
	if err != nil {
		return 0, err
	}
	webhooks, err := s.repository.ClaimWebhookBatch(ctx, s.batchSize, now)
	if err != nil {
		return 0, err
	}
	delivered := 0
	for _, record := range callbacks {
		if err := s.deliverCallback(ctx, record, now); err != nil {
			s.recordFailure(ctx, record.MerchantID, now, err)
			slog.ErrorContext(ctx, "deliver merchant callback failed", "error", err, "outbox_id", record.ID)
			continue
		}
		s.recordSuccess(ctx, record.MerchantID, now)
		delivered++
	}
	for _, record := range webhooks {
		if err := s.deliverWebhook(ctx, record, now); err != nil {
			s.recordFailure(ctx, record.MerchantID, now, err)
			slog.ErrorContext(ctx, "deliver merchant webhook failed", "error", err, "outbox_id", record.ID)
			continue
		}
		s.recordSuccess(ctx, record.MerchantID, now)
		delivered++
	}
	return delivered, nil
}

func (s *implementation) deliverCallback(ctx context.Context, record OutboxRecord, now time.Time) error {
	endpoint, err := s.repository.MerchantEndpoint(ctx, record.MerchantID)
	if err != nil {
		return s.failCallback(ctx, record, err, now)
	}
	secret, err := s.protector.Decrypt(endpoint.CallbackSecretEnc)
	if err != nil {
		return s.failCallback(ctx, record, fmt.Errorf("decrypt callback secret: %w", err), now)
	}
	amountCents, err := fixed.CentsFromString(record.Amount)
	if err != nil {
		return s.failCallback(ctx, record, fmt.Errorf("parse callback amount: %w", err), now)
	}
	request := CallbackRequest{
		CallbackID:    record.ID,
		Type:          record.Type,
		TransactionID: record.TransactionID,
		UserID:        record.UserID,
		Currency:      record.Currency,
		Amount:        fixed.FormatCents(amountCents),
		Reason:        record.Reason,
		Ref:           callbackRef(record),
		CreatedAt:     record.CreatedAt.UTC(),
	}
	response, err := s.client.DeliverCallback(ctx, endpoint.CallbackURL, record.MerchantID, secret, request)
	if err == nil {
		return s.repository.MarkCallbackDelivered(ctx, record, response, now)
	}
	if errors.Is(err, ErrPermanent) || record.Attempts+1 >= s.maxAttempt {
		return s.repository.MarkCallbackDeadLetter(ctx, record, err, request, now)
	}
	return s.repository.MarkCallbackRetry(ctx, record, err, now.Add(s.backoff(record.Attempts+1)), now)
}

func (s *implementation) deliverWebhook(ctx context.Context, record WebhookRecord, now time.Time) error {
	endpoint, err := s.repository.MerchantEndpoint(ctx, record.MerchantID)
	if err != nil {
		return s.failWebhook(ctx, record, err, now)
	}
	secret, err := s.protector.Decrypt(endpoint.CallbackSecretEnc)
	if err != nil {
		// Webhooks reuse callback_secret when webhook secret is not split.
		return s.failWebhook(ctx, record, fmt.Errorf("decrypt webhook secret: %w", err), now)
	}
	target := endpoint.WebhookURL
	if target == "" {
		target = endpoint.CallbackURL
	}
	err = s.client.DeliverWebhook(ctx, target, record.MerchantID, secret, record.Payload)
	if err == nil {
		return s.repository.MarkWebhookDelivered(ctx, record, now)
	}
	if errors.Is(err, ErrPermanent) || record.Attempts+1 >= s.maxAttempt {
		return s.repository.MarkWebhookDeadLetter(ctx, record, err, now)
	}
	return s.repository.MarkWebhookRetry(ctx, record, err, now.Add(s.backoff(record.Attempts+1)), now)
}

func (s *implementation) failCallback(ctx context.Context, record OutboxRecord, err error, now time.Time) error {
	if record.Attempts+1 >= s.maxAttempt {
		return s.repository.MarkCallbackDeadLetter(ctx, record, err, record, now)
	}
	return s.repository.MarkCallbackRetry(ctx, record, err, now.Add(s.backoff(record.Attempts+1)), now)
}

func (s *implementation) failWebhook(ctx context.Context, record WebhookRecord, err error, now time.Time) error {
	if record.Attempts+1 >= s.maxAttempt {
		return s.repository.MarkWebhookDeadLetter(ctx, record, err, now)
	}
	return s.repository.MarkWebhookRetry(ctx, record, err, now.Add(s.backoff(record.Attempts+1)), now)
}

func (s *implementation) backoff(attempt int) time.Duration {
	delay := s.retryStart
	for i := 1; i < attempt; i++ {
		if delay >= s.retryMax/2 {
			return s.retryMax
		}
		delay *= 2
	}
	if delay > s.retryMax {
		return s.retryMax
	}
	return delay
}

func (s *implementation) GetTransaction(
	ctx context.Context,
	merchantID string,
	transactionID string,
) (*TransactionHistory, error) {
	if s.repository == nil {
		return nil, errors.New("callback repository is not configured")
	}
	return s.repository.GetTransactionHistory(ctx, merchantID, transactionID)
}

func (s *implementation) GetLatestBalance(
	ctx context.Context,
	merchantID string,
	userID string,
	currency string,
) (string, error) {
	if s.repository == nil {
		return "", errors.New("callback repository is not configured")
	}
	return s.repository.GetLatestBalance(ctx, merchantID, userID, currency)
}

func (s *implementation) ReplayDeadLetter(ctx context.Context, channel, outboxID string) error {
	if s.repository == nil {
		return errors.New("callback repository is not configured")
	}
	return s.repository.ReplayDeadLetter(ctx, channel, outboxID, s.now().UTC())
}

// EnqueueRollback is used by the seamless coordinator after an unknown debit.
func (s *implementation) EnqueueRollback(
	ctx context.Context,
	merchantID string,
	userID string,
	currency string,
	amountCents int64,
	transactionID string,
	orderID string,
	marketID string,
) error {
	if s.repository == nil {
		return errors.New("callback repository is not configured")
	}
	return s.repository.EnqueueRollback(
		ctx,
		merchantID,
		userID,
		currency,
		amountCents,
		transactionID,
		orderID,
		marketID,
		s.now().UTC(),
	)
}

func callbackRef(record OutboxRecord) map[string]any {
	ref := map[string]any{}
	if record.OrderID != "" {
		ref["order_id"] = record.OrderID
	}
	if record.MarketID != "" {
		ref["market_id"] = record.MarketID
	}
	if record.EventID != "" {
		ref["event_id"] = record.EventID
	}
	if record.OriginalTransactionID != "" {
		ref["original_transaction_id"] = record.OriginalTransactionID
	}
	if len(ref) == 0 {
		return nil
	}
	return ref
}

// recordFailure counts one consecutive callback/webhook failure for a merchant
// and marks the merchant degraded once the threshold is reached (V3 §10.1).
func (s *implementation) recordFailure(
	ctx context.Context,
	merchantID string,
	now time.Time,
	cause error,
) {
	if s == nil || s.degraded == nil {
		return
	}
	reason := "merchant callback failures exceeded threshold"
	if cause != nil {
		reason = cause.Error()
	}
	if len(reason) > 512 {
		reason = reason[:512]
	}
	s.degraded.recordFailure(ctx, merchantID, reason, now)
}

// recordSuccess resets a merchant's consecutive-failure counter and clears a
// previously set degraded state on the first healthy delivery.
func (s *implementation) recordSuccess(ctx context.Context, merchantID string, now time.Time) {
	if s == nil || s.degraded == nil {
		return
	}
	s.degraded.recordSuccess(ctx, merchantID, now)
}

// VerifyCallback proves callback URL ownership with a signed challenge echo.
func (s *implementation) VerifyCallback(ctx context.Context, merchantID string) error {
	if s == nil || s.repository == nil || s.protector == nil || s.client == nil {
		return errors.New("callback service is not configured")
	}
	endpoint, err := s.repository.MerchantEndpoint(ctx, merchantID)
	if err != nil {
		return err
	}
	if strings.TrimSpace(endpoint.CallbackURL) == "" {
		return errors.New("merchant callback URL is not configured")
	}
	if strings.TrimSpace(endpoint.CallbackSecretEnc) == "" {
		return errors.New("merchant callback secret is not configured")
	}
	secret, err := s.protector.Decrypt(endpoint.CallbackSecretEnc)
	if err != nil {
		return fmt.Errorf("decrypt merchant callback secret: %w", err)
	}
	challengeBytes := make([]byte, 16)
	if _, err := io.ReadFull(rand.Reader, challengeBytes); err != nil {
		return fmt.Errorf("generate callback verification challenge: %w", err)
	}
	challenge := hex.EncodeToString(challengeBytes)
	echoed, err := s.client.DeliverVerification(
		ctx, endpoint.CallbackURL, merchantID, secret, challenge,
	)
	if err != nil {
		return fmt.Errorf("callback verification challenge failed: %w", err)
	}
	if echoed != challenge {
		return fmt.Errorf("callback verification challenge was not echoed correctly")
	}
	if err := s.repository.MarkCallbackVerified(ctx, merchantID, s.now().UTC()); err != nil {
		return err
	}
	return nil
}

// ResetDegraded manually clears a degraded seamless merchant.
func (s *implementation) ResetDegraded(ctx context.Context, merchantID string) error {
	if s == nil || s.repository == nil {
		return errors.New("callback service is not configured")
	}
	if s.degraded != nil {
		s.degraded.clear(ctx, merchantID)
	}
	return s.repository.ClearSeamlessDegraded(ctx, merchantID, s.now().UTC())
}

// degradedPersistence records the shared degraded state of a merchant.
type degradedPersistence interface {
	MarkSeamlessDegraded(ctx context.Context, merchantID string, reason string, now time.Time) error
	ClearSeamlessDegraded(ctx context.Context, merchantID string, now time.Time) error
}

// degradedTracker counts consecutive callback failures per merchant and flips
// the shared degraded flag when a merchant crosses the threshold.
type degradedTracker struct {
	mu          sync.Mutex
	failures    map[string]int
	persistence degradedPersistence
	threshold   int
	now         func() time.Time
}

func (t *degradedTracker) recordFailure(ctx context.Context, merchantID string, reason string, now time.Time) {
	if t == nil || t.threshold <= 0 {
		return
	}
	if t.failures == nil {
		t.failures = map[string]int{}
	}
	t.mu.Lock()
	t.failures[merchantID]++
	count := t.failures[merchantID]
	t.mu.Unlock()
	if count < t.threshold || t.persistence == nil {
		return
	}
	if err := t.persistence.MarkSeamlessDegraded(ctx, merchantID, reason, now); err != nil {
		slog.ErrorContext(ctx, "mark merchant degraded failed", "error", err, "merchant_id", merchantID)
	}
}

func (t *degradedTracker) recordSuccess(ctx context.Context, merchantID string, now time.Time) {
	if t == nil {
		return
	}
	hadFailures := false
	t.mu.Lock()
	if t.failures != nil {
		_, hadFailures = t.failures[merchantID]
		delete(t.failures, merchantID)
	}
	t.mu.Unlock()
	if hadFailures && t.persistence != nil {
		if err := t.persistence.ClearSeamlessDegraded(ctx, merchantID, now); err != nil {
			slog.ErrorContext(ctx, "clear merchant degraded failed", "error", err, "merchant_id", merchantID)
		}
	}
}

// clear removes any in-memory failure counter for a merchant.
func (t *degradedTracker) clear(_ context.Context, merchantID string) {
	if t == nil {
		return
	}
	t.mu.Lock()
	delete(t.failures, merchantID)
	t.mu.Unlock()
}

// QueryBalance returns the merchant-authoritative balance for a user.
func (s *implementation) QueryBalance(
	ctx context.Context,
	merchantID string,
	userID string,
	currency string,
) (string, error) {
	if s == nil || s.repository == nil || s.protector == nil || s.client == nil {
		return "", errors.New("callback service is not configured")
	}
	endpoint, err := s.repository.MerchantEndpoint(ctx, merchantID)
	if err != nil {
		return "", err
	}
	if endpoint.WalletMode != "seamless" {
		return "", errors.New("balance queries require seamless wallet mode")
	}
	if strings.TrimSpace(endpoint.CallbackURL) == "" || strings.TrimSpace(endpoint.CallbackSecretEnc) == "" {
		return "", errors.New("merchant callback endpoint is not configured")
	}
	secret, err := s.protector.Decrypt(endpoint.CallbackSecretEnc)
	if err != nil {
		return "", fmt.Errorf("decrypt merchant callback secret: %w", err)
	}
	return s.client.DeliverBalanceQuery(ctx, endpoint.CallbackURL, merchantID, secret, userID, currency)
}

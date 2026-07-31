package callback

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/afun-game/predictmarket-saas/pkg/fixed"
)

// OutboxRecord is one pending merchant wallet callback.
type OutboxRecord struct {
	ID                    string
	MerchantID            string
	TransactionID         string
	OriginalTransactionID string
	UserID                string
	Currency              string
	Type                  string
	Reason                string
	Amount                string
	OrderID               string
	MarketID              string
	EventID               string
	Attempts              int
	CreatedAt             time.Time
}

// WebhookRecord is one pending settlement notification.
type WebhookRecord struct {
	ID         string
	MerchantID string
	EventType  string
	Payload    []byte
	Attempts   int
	CreatedAt  time.Time
}

// TransactionHistory is the durable delivery trail for one seamless transaction.
type TransactionHistory struct {
	TransactionID         string          `json:"transaction_id"`
	MerchantID            string          `json:"merchant_id"`
	UserID                string          `json:"user_id"`
	Currency              string          `json:"currency"`
	Type                  string          `json:"type"`
	Reason                string          `json:"reason"`
	Amount                string          `json:"amount"`
	OrderID               string          `json:"order_id,omitempty"`
	OriginalTransactionID string          `json:"original_transaction_id,omitempty"`
	Status                string          `json:"status"`
	CallbackResponse      json.RawMessage `json:"callback_response,omitempty"`
	CreatedAt             time.Time       `json:"created_at"`
	UpdatedAt             time.Time       `json:"updated_at"`
	Deliveries            []Delivery      `json:"deliveries"`
}

// Delivery is one outbox attempt history row for a transaction.
type Delivery struct {
	ID            string          `json:"id"`
	Type          string          `json:"type"`
	Status        string          `json:"status"`
	Attempts      int             `json:"attempts"`
	LastError     string          `json:"last_error,omitempty"`
	Response      json.RawMessage `json:"response,omitempty"`
	NextAttemptAt *time.Time      `json:"next_attempt_at,omitempty"`
	DeliveredAt   *time.Time      `json:"delivered_at,omitempty"`
	CreatedAt     time.Time       `json:"created_at"`
	UpdatedAt     time.Time       `json:"updated_at"`
}

// MerchantEndpoint holds callback/webhook configuration for one merchant.
type MerchantEndpoint struct {
	ID                 string
	CallbackURL        string
	CallbackSecretEnc  string
	WebhookURL         string
	WebhookEvents      []string
	WalletMode         string
	SeamlessDegraded   bool
	CallbackVerifiedAt *time.Time
}

type repository struct {
	database *sql.DB
}

func newRepository(database *sql.DB) *repository {
	return &repository{database: database}
}

func (r *repository) MerchantEndpoint(ctx context.Context, merchantID string) (*MerchantEndpoint, error) {
	const query = `
SELECT id, COALESCE(callback_url, ''), COALESCE(callback_secret_enc, ''),
       COALESCE(webhook_url, ''), COALESCE(array_to_string(webhook_events, ','), ''), wallet_mode,
       COALESCE(seamless_degraded, FALSE), callback_verified_at
FROM merchants
WHERE id = $1`
	value := &MerchantEndpoint{}
	var eventsCSV string
	if err := r.database.QueryRowContext(ctx, query, merchantID).Scan(
		&value.ID,
		&value.CallbackURL,
		&value.CallbackSecretEnc,
		&value.WebhookURL,
		&eventsCSV,
		&value.WalletMode,
		&value.SeamlessDegraded,
		&value.CallbackVerifiedAt,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("get merchant callback endpoint: %w", err)
	}
	if eventsCSV != "" {
		value.WebhookEvents = strings.Split(eventsCSV, ",")
	}
	return value, nil
}

func (r *repository) MarkSeamlessDegraded(
	ctx context.Context,
	merchantID string,
	reason string,
	now time.Time,
) error {
	const query = `
UPDATE merchants
SET seamless_degraded = TRUE,
    seamless_degraded_at = $2,
    seamless_degraded_reason = $3,
    updated_at = $2
WHERE id = $1 AND NOT seamless_degraded`
	if _, err := r.database.ExecContext(ctx, query, merchantID, now, reason); err != nil {
		return fmt.Errorf("mark merchant seamless degraded: %w", err)
	}
	return nil
}

func (r *repository) ClearSeamlessDegraded(ctx context.Context, merchantID string, now time.Time) error {
	const query = `
UPDATE merchants
SET seamless_degraded = FALSE,
    seamless_degraded_at = NULL,
    seamless_degraded_reason = NULL,
    updated_at = $2
WHERE id = $1 AND seamless_degraded`
	if _, err := r.database.ExecContext(ctx, query, merchantID, now); err != nil {
		return fmt.Errorf("clear merchant seamless degraded: %w", err)
	}
	return nil
}

func (r *repository) MarkCallbackVerified(ctx context.Context, merchantID string, verifiedAt time.Time) error {
	const query = `
UPDATE merchants
SET callback_verified_at = $2, updated_at = $2
WHERE id = $1`
	if _, err := r.database.ExecContext(ctx, query, merchantID, verifiedAt); err != nil {
		return fmt.Errorf("mark merchant callback verified: %w", err)
	}
	return nil
}

func (r *repository) ClaimCallbackBatch(ctx context.Context, limit int, now time.Time) ([]OutboxRecord, error) {
	databaseTx, err := r.database.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin callback claim: %w", err)
	}
	defer func() { _ = databaseTx.Rollback() }()

	const query = `
SELECT id, merchant_id, transaction_id, COALESCE(original_transaction_id::text, ''),
       user_id, currency, type, reason, amount::text, COALESCE(order_id::text, ''),
       COALESCE(market_id::text, ''), COALESCE(event_id::text, ''), attempts, created_at
FROM callback_outbox
WHERE status = 'pending' AND next_attempt_at <= $1
ORDER BY next_attempt_at, created_at, id
LIMIT $2
FOR UPDATE SKIP LOCKED`
	rows, err := databaseTx.QueryContext(ctx, query, now, limit)
	if err != nil {
		return nil, fmt.Errorf("claim callback outbox: %w", err)
	}
	defer func() { _ = rows.Close() }()

	records := make([]OutboxRecord, 0, limit)
	ids := make([]string, 0, limit)
	for rows.Next() {
		var record OutboxRecord
		if err := rows.Scan(
			&record.ID,
			&record.MerchantID,
			&record.TransactionID,
			&record.OriginalTransactionID,
			&record.UserID,
			&record.Currency,
			&record.Type,
			&record.Reason,
			&record.Amount,
			&record.OrderID,
			&record.MarketID,
			&record.EventID,
			&record.Attempts,
			&record.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan callback outbox: %w", err)
		}
		records = append(records, record)
		ids = append(ids, record.ID)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate callback outbox: %w", err)
	}
	if len(ids) == 0 {
		if err := databaseTx.Commit(); err != nil {
			return nil, fmt.Errorf("commit empty callback claim: %w", err)
		}
		return records, nil
	}
	// Push next_attempt_at forward so another worker does not race the same rows
	// while this batch is in flight.
	const bump = `
UPDATE callback_outbox
SET next_attempt_at = $2, updated_at = $2
WHERE id = $1`
	nextAttempt := now.Add(defaultTimeout + time.Second)
	for _, id := range ids {
		if _, err := databaseTx.ExecContext(ctx, bump, id, nextAttempt); err != nil {
			return nil, fmt.Errorf("bump claimed callback rows: %w", err)
		}
	}
	if err := databaseTx.Commit(); err != nil {
		return nil, fmt.Errorf("commit callback claim: %w", err)
	}
	return records, nil
}

func (r *repository) ClaimWebhookBatch(ctx context.Context, limit int, now time.Time) ([]WebhookRecord, error) {
	databaseTx, err := r.database.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin webhook claim: %w", err)
	}
	defer func() { _ = databaseTx.Rollback() }()

	const query = `
SELECT id, merchant_id, event_type, payload, attempts, created_at
FROM webhook_outbox
WHERE status = 'pending' AND next_attempt_at <= $1
ORDER BY next_attempt_at, created_at, id
LIMIT $2
FOR UPDATE SKIP LOCKED`
	rows, err := databaseTx.QueryContext(ctx, query, now, limit)
	if err != nil {
		return nil, fmt.Errorf("claim webhook outbox: %w", err)
	}
	defer func() { _ = rows.Close() }()

	records := make([]WebhookRecord, 0, limit)
	ids := make([]string, 0, limit)
	for rows.Next() {
		var record WebhookRecord
		if err := rows.Scan(
			&record.ID,
			&record.MerchantID,
			&record.EventType,
			&record.Payload,
			&record.Attempts,
			&record.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan webhook outbox: %w", err)
		}
		records = append(records, record)
		ids = append(ids, record.ID)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate webhook outbox: %w", err)
	}
	if len(ids) == 0 {
		if err := databaseTx.Commit(); err != nil {
			return nil, fmt.Errorf("commit empty webhook claim: %w", err)
		}
		return records, nil
	}
	const bump = `
UPDATE webhook_outbox
SET next_attempt_at = $2, updated_at = $2
WHERE id = $1`
	nextAttempt := now.Add(defaultTimeout + time.Second)
	for _, id := range ids {
		if _, err := databaseTx.ExecContext(ctx, bump, id, nextAttempt); err != nil {
			return nil, fmt.Errorf("bump claimed webhook rows: %w", err)
		}
	}
	if err := databaseTx.Commit(); err != nil {
		return nil, fmt.Errorf("commit webhook claim: %w", err)
	}
	return records, nil
}

func (r *repository) MarkCallbackDelivered(
	ctx context.Context,
	record OutboxRecord,
	response any,
	now time.Time,
) error {
	responseJSON, err := json.Marshal(response)
	if err != nil {
		return fmt.Errorf("marshal callback response: %w", err)
	}
	databaseTx, err := r.database.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin callback delivered: %w", err)
	}
	defer func() { _ = databaseTx.Rollback() }()

	const outboxQuery = `
UPDATE callback_outbox
SET status = 'delivered', attempts = attempts + 1, response = $2::jsonb,
    last_error = NULL, delivered_at = $3, updated_at = $3
WHERE id = $1 AND status = 'pending'`
	result, err := databaseTx.ExecContext(ctx, outboxQuery, record.ID, string(responseJSON), now)
	if err != nil {
		return fmt.Errorf("mark callback delivered: %w", err)
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("count delivered callback: %w", err)
	}
	if rowsAffected != 1 {
		return nil
	}
	const txnQuery = `
UPDATE seamless_transactions
SET status = CASE
        WHEN type = 'debit' AND $2 = 'rollback' THEN 'rolled_back'
        ELSE 'delivered'
    END,
    callback_response = $3::jsonb,
    updated_at = $4
WHERE transaction_id = $1`
	if _, err := databaseTx.ExecContext(
		ctx,
		txnQuery,
		record.TransactionID,
		record.Type,
		string(responseJSON),
		now,
	); err != nil {
		return fmt.Errorf("mark seamless transaction delivered: %w", err)
	}
	if err := databaseTx.Commit(); err != nil {
		return fmt.Errorf("commit callback delivered: %w", err)
	}
	return nil
}

func (r *repository) MarkCallbackRetry(
	ctx context.Context,
	record OutboxRecord,
	attemptError error,
	nextAttemptAt time.Time,
	now time.Time,
) error {
	const query = `
UPDATE callback_outbox
SET attempts = attempts + 1, next_attempt_at = $2, last_error = $3, updated_at = $4
WHERE id = $1 AND status = 'pending'`
	_, err := r.database.ExecContext(
		ctx,
		query,
		record.ID,
		nextAttemptAt,
		truncateError(attemptError),
		now,
	)
	if err != nil {
		return fmt.Errorf("schedule callback retry: %w", err)
	}
	return nil
}

func (r *repository) MarkCallbackDeadLetter(
	ctx context.Context,
	record OutboxRecord,
	attemptError error,
	payload any,
	now time.Time,
) error {
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal callback dead letter payload: %w", err)
	}
	databaseTx, err := r.database.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin callback dead letter: %w", err)
	}
	defer func() { _ = databaseTx.Rollback() }()

	const outboxQuery = `
UPDATE callback_outbox
SET status = 'dead_letter', attempts = attempts + 1, last_error = $2, updated_at = $3
WHERE id = $1 AND status = 'pending'`
	result, err := databaseTx.ExecContext(ctx, outboxQuery, record.ID, truncateError(attemptError), now)
	if err != nil {
		return fmt.Errorf("mark callback dead letter: %w", err)
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("count callback dead letter: %w", err)
	}
	if rowsAffected != 1 {
		return nil
	}
	const deadLetterQuery = `
INSERT INTO callback_dead_letters (
    channel, outbox_id, merchant_id, transaction_id, payload, attempts, last_error, created_at
) VALUES (
    'callback', $1, $2, $3, $4::jsonb, $5, $6, $7
)
ON CONFLICT (channel, outbox_id) DO NOTHING`
	if _, err := databaseTx.ExecContext(
		ctx,
		deadLetterQuery,
		record.ID,
		record.MerchantID,
		record.TransactionID,
		string(payloadJSON),
		record.Attempts+1,
		truncateError(attemptError),
		now,
	); err != nil {
		return fmt.Errorf("insert callback dead letter: %w", err)
	}
	const txnQuery = `
UPDATE seamless_transactions
SET status = 'dead_letter', updated_at = $2
WHERE transaction_id = $1`
	if _, err := databaseTx.ExecContext(ctx, txnQuery, record.TransactionID, now); err != nil {
		return fmt.Errorf("mark seamless transaction dead letter: %w", err)
	}
	if err := databaseTx.Commit(); err != nil {
		return fmt.Errorf("commit callback dead letter: %w", err)
	}
	return nil
}

func (r *repository) MarkWebhookDelivered(ctx context.Context, record WebhookRecord, now time.Time) error {
	const query = `
UPDATE webhook_outbox
SET status = 'delivered', attempts = attempts + 1, last_error = NULL,
    delivered_at = $2, updated_at = $2
WHERE id = $1 AND status = 'pending'`
	_, err := r.database.ExecContext(ctx, query, record.ID, now)
	if err != nil {
		return fmt.Errorf("mark webhook delivered: %w", err)
	}
	return nil
}

func (r *repository) MarkWebhookRetry(
	ctx context.Context,
	record WebhookRecord,
	attemptError error,
	nextAttemptAt time.Time,
	now time.Time,
) error {
	const query = `
UPDATE webhook_outbox
SET attempts = attempts + 1, next_attempt_at = $2, last_error = $3, updated_at = $4
WHERE id = $1 AND status = 'pending'`
	_, err := r.database.ExecContext(
		ctx,
		query,
		record.ID,
		nextAttemptAt,
		truncateError(attemptError),
		now,
	)
	if err != nil {
		return fmt.Errorf("schedule webhook retry: %w", err)
	}
	return nil
}

func (r *repository) MarkWebhookDeadLetter(
	ctx context.Context,
	record WebhookRecord,
	attemptError error,
	now time.Time,
) error {
	databaseTx, err := r.database.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin webhook dead letter: %w", err)
	}
	defer func() { _ = databaseTx.Rollback() }()

	const outboxQuery = `
UPDATE webhook_outbox
SET status = 'dead_letter', attempts = attempts + 1, last_error = $2, updated_at = $3
WHERE id = $1 AND status = 'pending'`
	result, err := databaseTx.ExecContext(ctx, outboxQuery, record.ID, truncateError(attemptError), now)
	if err != nil {
		return fmt.Errorf("mark webhook dead letter: %w", err)
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("count webhook dead letter: %w", err)
	}
	if rowsAffected != 1 {
		return nil
	}
	const deadLetterQuery = `
INSERT INTO callback_dead_letters (
    channel, outbox_id, merchant_id, transaction_id, payload, attempts, last_error, created_at
) VALUES (
    'webhook', $1, $2, NULL, $3::jsonb, $4, $5, $6
)
ON CONFLICT (channel, outbox_id) DO NOTHING`
	if _, err := databaseTx.ExecContext(
		ctx,
		deadLetterQuery,
		record.ID,
		record.MerchantID,
		string(record.Payload),
		record.Attempts+1,
		truncateError(attemptError),
		now,
	); err != nil {
		return fmt.Errorf("insert webhook dead letter: %w", err)
	}
	if err := databaseTx.Commit(); err != nil {
		return fmt.Errorf("commit webhook dead letter: %w", err)
	}
	return nil
}

func (r *repository) GetTransactionHistory(
	ctx context.Context,
	merchantID string,
	transactionID string,
) (*TransactionHistory, error) {
	const query = `
SELECT transaction_id, merchant_id, user_id, currency, type, reason, amount::text,
       COALESCE(order_id::text, ''), COALESCE(original_transaction_id::text, ''),
       status, COALESCE(callback_response, 'null'::jsonb), created_at, updated_at
FROM seamless_transactions
WHERE merchant_id = $1 AND transaction_id = $2`
	value := &TransactionHistory{}
	var response []byte
	err := r.database.QueryRowContext(ctx, query, merchantID, transactionID).Scan(
		&value.TransactionID,
		&value.MerchantID,
		&value.UserID,
		&value.Currency,
		&value.Type,
		&value.Reason,
		&value.Amount,
		&value.OrderID,
		&value.OriginalTransactionID,
		&value.Status,
		&response,
		&value.CreatedAt,
		&value.UpdatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get seamless transaction: %w", err)
	}
	if string(response) != "null" {
		value.CallbackResponse = json.RawMessage(response)
	}
	const deliveryQuery = `
SELECT id, type, status, attempts, COALESCE(last_error, ''), COALESCE(response, 'null'::jsonb),
       next_attempt_at, delivered_at, created_at, updated_at
FROM callback_outbox
WHERE merchant_id = $1 AND transaction_id = $2
ORDER BY created_at DESC, id DESC`
	rows, err := r.database.QueryContext(ctx, deliveryQuery, merchantID, transactionID)
	if err != nil {
		return nil, fmt.Errorf("list callback deliveries: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var delivery Delivery
		var deliveryResponse []byte
		var nextAttempt sql.NullTime
		var deliveredAt sql.NullTime
		if err := rows.Scan(
			&delivery.ID,
			&delivery.Type,
			&delivery.Status,
			&delivery.Attempts,
			&delivery.LastError,
			&deliveryResponse,
			&nextAttempt,
			&deliveredAt,
			&delivery.CreatedAt,
			&delivery.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan callback delivery: %w", err)
		}
		if string(deliveryResponse) != "null" {
			delivery.Response = json.RawMessage(deliveryResponse)
		}
		if nextAttempt.Valid {
			valueTime := nextAttempt.Time.UTC()
			delivery.NextAttemptAt = &valueTime
		}
		if deliveredAt.Valid {
			valueTime := deliveredAt.Time.UTC()
			delivery.DeliveredAt = &valueTime
		}
		value.Deliveries = append(value.Deliveries, delivery)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate callback deliveries: %w", err)
	}
	return value, nil
}

// GetLatestBalance returns the most recent merchant-authoritative balance
// observed in a successful callback response for the hosted user.
func (r *repository) GetLatestBalance(
	ctx context.Context,
	merchantID string,
	userID string,
	currency string,
) (string, error) {
	const query = `
SELECT callback_response->>'balance'
FROM seamless_transactions
WHERE merchant_id = $1
  AND user_id = $2
  AND currency = $3
  AND callback_response IS NOT NULL
  AND callback_response ? 'balance'
  AND NULLIF(callback_response->>'balance', '') IS NOT NULL
ORDER BY updated_at DESC, transaction_id DESC
LIMIT 1`
	var balance sql.NullString
	if err := r.database.QueryRowContext(ctx, query, merchantID, userID, currency).Scan(&balance); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", ErrNotFound
		}
		return "", fmt.Errorf("get latest merchant balance: %w", err)
	}
	if !balance.Valid || strings.TrimSpace(balance.String) == "" {
		return "", ErrNotFound
	}
	cents, err := fixed.CentsFromString(balance.String)
	if err != nil {
		return "", fmt.Errorf("parse latest merchant balance: %w", err)
	}
	return fixed.FormatCents(cents), nil
}

// EnqueueRollback inserts a rollback outbox row for an unknown/failed debit.
func (r *repository) EnqueueRollback(
	ctx context.Context,
	merchantID string,
	userID string,
	currency string,
	amountCents int64,
	transactionID string,
	orderID string,
	marketID string,
	now time.Time,
) error {
	const query = `
INSERT INTO callback_outbox (
    merchant_id, transaction_id, original_transaction_id, user_id, currency, type, reason,
    amount, order_id, market_id, status, next_attempt_at, created_at, updated_at
) VALUES (
    $1, $2, $2, $3, $4, 'rollback', 'void', $5::numeric,
    NULLIF($6, '')::uuid, NULLIF($7, '')::uuid, 'pending', $8, $8, $8
)
ON CONFLICT DO NOTHING`
	// There is no unique constraint on (transaction_id, type); guard with a
	// pre-check so repeated unknown debits do not spam rollback rows.
	const existsQuery = `
SELECT EXISTS (
    SELECT 1 FROM callback_outbox
    WHERE merchant_id = $1 AND transaction_id = $2 AND type = 'rollback'
)`
	var exists bool
	if err := r.database.QueryRowContext(ctx, existsQuery, merchantID, transactionID).Scan(&exists); err != nil {
		return fmt.Errorf("check existing rollback: %w", err)
	}
	if exists {
		return nil
	}
	if _, err := r.database.ExecContext(
		ctx,
		query,
		merchantID,
		transactionID,
		userID,
		currency,
		fixed.FormatCents(amountCents),
		orderID,
		marketID,
		now,
	); err != nil {
		return fmt.Errorf("enqueue rollback callback: %w", err)
	}
	const statusQuery = `
UPDATE seamless_transactions
SET status = 'unknown', updated_at = $2
WHERE transaction_id = $1 AND status IN ('created', 'accepted', 'unknown')`
	if _, err := r.database.ExecContext(ctx, statusQuery, transactionID, now); err != nil {
		return fmt.Errorf("mark seamless transaction unknown: %w", err)
	}
	return nil
}

// ReplayDeadLetter moves a dead-lettered outbox row back to pending.
func (r *repository) ReplayDeadLetter(ctx context.Context, channel, outboxID string, now time.Time) error {
	databaseTx, err := r.database.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin dead letter replay: %w", err)
	}
	defer func() { _ = databaseTx.Rollback() }()

	var table string
	switch channel {
	case "callback":
		table = "callback_outbox"
	case "webhook":
		table = "webhook_outbox"
	default:
		return fmt.Errorf("unsupported dead letter channel %q", channel)
	}
	query := fmt.Sprintf(`
UPDATE %s
SET status = 'pending', next_attempt_at = $2, last_error = NULL, updated_at = $2
WHERE id = $1 AND status = 'dead_letter'`, table)
	result, err := databaseTx.ExecContext(ctx, query, outboxID, now)
	if err != nil {
		return fmt.Errorf("replay %s dead letter: %w", channel, err)
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("count replayed dead letter: %w", err)
	}
	if rowsAffected != 1 {
		return ErrNotFound
	}
	const mark = `
UPDATE callback_dead_letters
SET replayed_at = $3
WHERE channel = $1 AND outbox_id = $2 AND replayed_at IS NULL`
	if _, err := databaseTx.ExecContext(ctx, mark, channel, outboxID, now); err != nil {
		return fmt.Errorf("mark dead letter replayed: %w", err)
	}
	if channel == "callback" {
		const txnQuery = `
UPDATE seamless_transactions AS st
SET status = 'pending_delivery', updated_at = $2
FROM callback_outbox AS o
WHERE o.id = $1 AND st.transaction_id = o.transaction_id`
		if _, err := databaseTx.ExecContext(ctx, txnQuery, outboxID, now); err != nil {
			return fmt.Errorf("restore seamless transaction after replay: %w", err)
		}
	}
	if err := databaseTx.Commit(); err != nil {
		return fmt.Errorf("commit dead letter replay: %w", err)
	}
	return nil
}

func truncateError(err error) string {
	if err == nil {
		return ""
	}
	message := err.Error()
	if len(message) > 1000 {
		return message[:1000]
	}
	return message
}

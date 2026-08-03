// Package adminquery provides database-backed read queries for the admin
// console. It owns a read-only connection and returns plain DTOs; all writes
// go through the existing service layer so invariants stay in one place.
package adminquery

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Service executes admin console read queries.
type Service struct {
	database *sql.DB
}

// New constructs a Service over the given database.
func New(database *sql.DB) *Service {
	return &Service{database: database}
}

// MerchantRow is one merchant in the console list.
type MerchantRow struct {
	ID         string    `json:"id"`
	Name       string    `json:"name"`
	Email      string    `json:"email"`
	Status     string    `json:"status"`
	Currency   string    `json:"currency"`
	Timezone   string    `json:"timezone"`
	WalletMode string    `json:"wallet_mode"`
	FeeRate    float64   `json:"fee_rate"`
	CreatedAt  time.Time `json:"created_at"`
}

// MerchantStats aggregates tenant activity for the merchant detail page.
type MerchantStats struct {
	UserCount   int     `json:"user_count"`
	MarketCount int     `json:"market_count"`
	OrderCount  int     `json:"order_count"`
	TotalVolume float64 `json:"total_volume"`
}

// ListMerchants returns a paginated merchant list, optionally filtered by a
// name/email/id keyword.
func (s *Service) ListMerchants(ctx context.Context, q string, page, limit int) ([]MerchantRow, int, error) {
	if s == nil || s.database == nil {
		return nil, 0, errors.New("admin query database is not configured")
	}
	q = strings.TrimSpace(q)
	const query = `
SELECT id, name, email, status, currency, timezone, wallet_mode, fee_rate, created_at
FROM merchants
WHERE $1 = '' OR name ILIKE '%' || $1 || '%' OR email ILIKE '%' || $1 || '%' OR id::text ILIKE '%' || $1 || '%'
ORDER BY created_at DESC
LIMIT $2 OFFSET $3`
	rows, err := s.database.QueryContext(ctx, query, q, limit, (page-1)*limit)
	if err != nil {
		return nil, 0, fmt.Errorf("list merchants: %w", err)
	}
	defer rows.Close()
	items := []MerchantRow{}
	for rows.Next() {
		item := MerchantRow{}
		if err := rows.Scan(
			&item.ID, &item.Name, &item.Email, &item.Status, &item.Currency,
			&item.Timezone, &item.WalletMode, &item.FeeRate, &item.CreatedAt,
		); err != nil {
			return nil, 0, fmt.Errorf("scan merchant: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	var total int
	if err := s.database.QueryRowContext(ctx, `
SELECT COUNT(*) FROM merchants
WHERE $1 = '' OR name ILIKE '%' || $1 || '%' OR email ILIKE '%' || $1 || '%' OR id::text ILIKE '%' || $1 || '%'`,
		q).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count merchants: %w", err)
	}
	return items, total, nil
}

// GetMerchantStats aggregates tenant activity for the merchant detail page.
func (s *Service) GetMerchantStats(ctx context.Context, merchantID string) (MerchantStats, error) {
	if s == nil || s.database == nil {
		return MerchantStats{}, errors.New("admin query database is not configured")
	}
	const query = `
SELECT
    (SELECT COUNT(*) FROM platform_users WHERE merchant_id::text = $1),
    (SELECT COUNT(*) FROM markets WHERE merchant_id::text = $1),
    (SELECT COUNT(*) FROM orders WHERE merchant_id::text = $1),
    COALESCE((SELECT SUM(total_volume) FROM markets WHERE merchant_id::text = $1), 0)`
	stats := MerchantStats{}
	if err := s.database.QueryRowContext(ctx, query, merchantID).Scan(
		&stats.UserCount, &stats.MarketCount, &stats.OrderCount, &stats.TotalVolume,
	); err != nil {
		return MerchantStats{}, fmt.Errorf("merchant stats: %w", err)
	}
	return stats, nil
}

// UserRow is one platform user in the console list.
type UserRow struct {
	MerchantID     string    `json:"merchant_id"`
	ExternalUserID string    `json:"external_user_id"`
	Locale         string    `json:"locale"`
	Status         string    `json:"status"`
	Currency       string    `json:"currency"`
	Balance        float64   `json:"balance"`
	LockedBalance  float64   `json:"locked_balance"`
	OrderCount     int       `json:"order_count"`
	CreatedAt      time.Time `json:"created_at"`
}

const userSelect = `
SELECT u.merchant_id, u.external_user_id, u.locale, u.status, u.created_at,
       COALESCE(w.currency, ''), COALESCE(w.balance, 0), COALESCE(w.locked_balance, 0),
       COALESCE((SELECT COUNT(*) FROM orders o WHERE o.merchant_id = u.merchant_id AND o.user_id = u.external_user_id), 0)
FROM platform_users u
LEFT JOIN LATERAL (
    SELECT w.currency, w.balance, w.locked_balance
    FROM wallets w
    WHERE w.merchant_id = u.merchant_id AND w.user_id = u.external_user_id
    ORDER BY w.currency LIMIT 1
) w ON true`

const userWhere = `
WHERE ($1 = '' OR u.merchant_id::text = $1)
  AND ($2 = '' OR u.status = $2)
  AND ($3 = '' OR u.external_user_id ILIKE '%' || $3 || '%' OR u.merchant_id::text ILIKE '%' || $3 || '%')`

// ListUsers returns paginated platform users with wallet and order context.
func (s *Service) ListUsers(ctx context.Context, merchantID, q, status string, page, limit int) ([]UserRow, int, error) {
	if s == nil || s.database == nil {
		return nil, 0, errors.New("admin query database is not configured")
	}
	merchantID = strings.TrimSpace(merchantID)
	q = strings.TrimSpace(q)
	status = strings.TrimSpace(status)
	rows, err := s.database.QueryContext(ctx,
		userSelect+" "+userWhere+" ORDER BY u.created_at DESC LIMIT $4 OFFSET $5",
		merchantID, status, q, limit, (page-1)*limit)
	if err != nil {
		return nil, 0, fmt.Errorf("list platform users: %w", err)
	}
	defer rows.Close()
	items := []UserRow{}
	for rows.Next() {
		item := UserRow{}
		if err := rows.Scan(
			&item.MerchantID, &item.ExternalUserID, &item.Locale, &item.Status,
			&item.CreatedAt, &item.Currency, &item.Balance, &item.LockedBalance,
			&item.OrderCount,
		); err != nil {
			return nil, 0, fmt.Errorf("scan platform user: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	var total int
	if err := s.database.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM platform_users u "+userWhere,
		merchantID, status, q).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count platform users: %w", err)
	}
	return items, total, nil
}

// WalletRow is one currency wallet on the user detail page.
type WalletRow struct {
	Currency      string  `json:"currency"`
	Balance       float64 `json:"balance"`
	LockedBalance float64 `json:"locked_balance"`
}

// UserDetail is the full user context for the detail page.
type UserDetail struct {
	MerchantID     string      `json:"merchant_id"`
	ExternalUserID string      `json:"external_user_id"`
	Locale         string      `json:"locale"`
	Status         string      `json:"status"`
	CreatedAt      time.Time   `json:"created_at"`
	Wallets        []WalletRow `json:"wallets"`
	OrderCount     int         `json:"order_count"`
	LastOrderAt    *time.Time  `json:"last_order_at,omitempty"`
}

// GetUser returns one user with wallets and order context.
func (s *Service) GetUser(ctx context.Context, merchantID, userID string) (*UserDetail, error) {
	if s == nil || s.database == nil {
		return nil, errors.New("admin query database is not configured")
	}
	const query = `
SELECT u.merchant_id, u.external_user_id, u.locale, u.status, u.created_at,
       COALESCE((SELECT COUNT(*) FROM orders o WHERE o.merchant_id = u.merchant_id AND o.user_id = u.external_user_id), 0),
       (SELECT MAX(o.created_at) FROM orders o WHERE o.merchant_id = u.merchant_id AND o.user_id = u.external_user_id)
FROM platform_users u
WHERE u.merchant_id::text = $1 AND u.external_user_id = $2`
	detail := &UserDetail{MerchantID: merchantID, ExternalUserID: userID}
	var lastOrderAt sql.NullTime
	if err := s.database.QueryRowContext(ctx, query, merchantID, userID).Scan(
		&detail.MerchantID, &detail.ExternalUserID, &detail.Locale, &detail.Status,
		&detail.CreatedAt, &detail.OrderCount, &lastOrderAt,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("get platform user: %w", err)
	}
	if lastOrderAt.Valid {
		detail.LastOrderAt = &lastOrderAt.Time
	}
	walletRows, err := s.database.QueryContext(ctx, `
SELECT currency, balance, locked_balance FROM wallets
WHERE merchant_id::text = $1 AND user_id = $2 ORDER BY currency`,
		merchantID, userID)
	if err != nil {
		return nil, fmt.Errorf("list user wallets: %w", err)
	}
	defer walletRows.Close()
	for walletRows.Next() {
		wallet := WalletRow{}
		if err := walletRows.Scan(&wallet.Currency, &wallet.Balance, &wallet.LockedBalance); err != nil {
			return nil, fmt.Errorf("scan user wallet: %w", err)
		}
		detail.Wallets = append(detail.Wallets, wallet)
	}
	return detail, walletRows.Err()
}

// TransactionRow is one wallet transaction in the console list.
type TransactionRow struct {
	ID        string    `json:"id"`
	WalletID  string    `json:"wallet_id"`
	Type      string    `json:"type"`
	Amount    float64   `json:"amount"`
	Currency  string    `json:"currency"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"created_at"`
}

// ListTransactions returns paginated wallet transactions, optionally scoped
// to a merchant, user, or transaction type.
func (s *Service) ListTransactions(ctx context.Context, merchantID, userID, txType string, page, limit int) ([]TransactionRow, int, error) {
	if s == nil || s.database == nil {
		return nil, 0, errors.New("admin query database is not configured")
	}
	merchantID = strings.TrimSpace(merchantID)
	userID = strings.TrimSpace(userID)
	txType = strings.TrimSpace(txType)
	const where = `
WHERE ($1 = '' OR w.merchant_id::text = $1)
  AND ($2 = '' OR w.user_id = $2)
  AND ($3 = '' OR t.type = $3)`
	const selectQuery = `
SELECT t.id, t.wallet_id, t.type, t.amount, t.currency, t.status, t.created_at
FROM transactions t
JOIN wallets w ON w.id = t.wallet_id` + where + `
ORDER BY t.created_at DESC
LIMIT $4 OFFSET $5`
	rows, err := s.database.QueryContext(ctx, selectQuery, merchantID, userID, txType, limit, (page-1)*limit)
	if err != nil {
		return nil, 0, fmt.Errorf("list transactions: %w", err)
	}
	defer rows.Close()
	items := []TransactionRow{}
	for rows.Next() {
		item := TransactionRow{}
		if err := rows.Scan(
			&item.ID, &item.WalletID, &item.Type, &item.Amount,
			&item.Currency, &item.Status, &item.CreatedAt,
		); err != nil {
			return nil, 0, fmt.Errorf("scan transaction: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	var total int
	if err := s.database.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM transactions t JOIN wallets w ON w.id = t.wallet_id "+where,
		merchantID, userID, txType).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count transactions: %w", err)
	}
	return items, total, nil
}

// OrderRow is one order in the console list.
type OrderRow struct {
	ID           string     `json:"id"`
	MerchantID   string     `json:"merchant_id"`
	UserID       string     `json:"user_id"`
	MarketID     string     `json:"market_id"`
	Type         string     `json:"type"`
	Option       string     `json:"option"`
	Amount       float64    `json:"amount"`
	FilledAmount float64    `json:"filled_amount"`
	Currency     string     `json:"currency"`
	Price        float64    `json:"price"`
	Status       string     `json:"status"`
	CreatedAt    time.Time  `json:"created_at"`
	FilledAt     *time.Time `json:"filled_at,omitempty"`
}

// ListOrders returns paginated orders, optionally scoped to a merchant,
// user, market, or status.
func (s *Service) ListOrders(ctx context.Context, merchantID, userID, marketID, status string, page, limit int) ([]OrderRow, int, error) {
	if s == nil || s.database == nil {
		return nil, 0, errors.New("admin query database is not configured")
	}
	merchantID = strings.TrimSpace(merchantID)
	userID = strings.TrimSpace(userID)
	marketID = strings.TrimSpace(marketID)
	status = strings.TrimSpace(status)
	const where = `
WHERE ($1 = '' OR merchant_id::text = $1)
  AND ($2 = '' OR user_id = $2)
  AND ($3 = '' OR market_id::text = $3)
  AND ($4 = '' OR status = $4)`
	const selectQuery = `
SELECT id, merchant_id, user_id, market_id, type, option, amount, filled_amount,
       currency, price, status, created_at, filled_at
FROM orders` + where + `
ORDER BY created_at DESC
LIMIT $5 OFFSET $6`
	rows, err := s.database.QueryContext(ctx, selectQuery, merchantID, userID, marketID, status, limit, (page-1)*limit)
	if err != nil {
		return nil, 0, fmt.Errorf("list orders: %w", err)
	}
	defer rows.Close()
	items := []OrderRow{}
	for rows.Next() {
		item := OrderRow{}
		var filledAt sql.NullTime
		if err := rows.Scan(
			&item.ID, &item.MerchantID, &item.UserID, &item.MarketID, &item.Type,
			&item.Option, &item.Amount, &item.FilledAmount, &item.Currency,
			&item.Price, &item.Status, &item.CreatedAt, &filledAt,
		); err != nil {
			return nil, 0, fmt.Errorf("scan order: %w", err)
		}
		if filledAt.Valid {
			item.FilledAt = &filledAt.Time
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	var total int
	if err := s.database.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM orders "+where,
		merchantID, userID, marketID, status).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count orders: %w", err)
	}
	return items, total, nil
}

// AuditRow is one administrator action in the console trail.
type AuditRow struct {
	ID            string          `json:"id"`
	AdminID       string          `json:"admin_id"`
	AdminUsername string          `json:"admin_username"`
	Action        string          `json:"action"`
	Resource      string          `json:"resource"`
	ResourceID    string          `json:"resource_id"`
	BeforeState   json.RawMessage `json:"before_state,omitempty"`
	AfterState    json.RawMessage `json:"after_state,omitempty"`
	ClientIP      string          `json:"client_ip"`
	CreatedAt     time.Time       `json:"created_at"`
}

// ListAuditLogs returns the paginated administrator action trail.
func (s *Service) ListAuditLogs(ctx context.Context, page, limit int) ([]AuditRow, int, error) {
	if s == nil || s.database == nil {
		return nil, 0, errors.New("admin query database is not configured")
	}
	const query = `
SELECT l.id, l.admin_id, COALESCE(a.username, ''), l.action, l.resource, l.resource_id,
       l.before_state, l.after_state, l.client_ip, l.created_at
FROM admin_action_logs l
LEFT JOIN admin_accounts a ON a.id = l.admin_id
ORDER BY l.created_at DESC
LIMIT $1 OFFSET $2`
	rows, err := s.database.QueryContext(ctx, query, limit, (page-1)*limit)
	if err != nil {
		return nil, 0, fmt.Errorf("list audit logs: %w", err)
	}
	defer rows.Close()
	items := []AuditRow{}
	for rows.Next() {
		item := AuditRow{}
		// JSONB columns are nullable (e.g. create actions carry no before
		// state); NULL cannot be scanned into json.RawMessage directly.
		var beforeState, afterState sql.NullString
		if err := rows.Scan(
			&item.ID, &item.AdminID, &item.AdminUsername, &item.Action, &item.Resource,
			&item.ResourceID, &beforeState, &afterState, &item.ClientIP,
			&item.CreatedAt,
		); err != nil {
			return nil, 0, fmt.Errorf("scan audit log: %w", err)
		}
		if beforeState.Valid {
			item.BeforeState = json.RawMessage(beforeState.String)
		}
		if afterState.Valid {
			item.AfterState = json.RawMessage(afterState.String)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	var total int
	if err := s.database.QueryRowContext(ctx, `SELECT COUNT(*) FROM admin_action_logs`).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count audit logs: %w", err)
	}
	return items, total, nil
}

// Overview is the dashboard aggregate snapshot.
type Overview struct {
	Merchants struct {
		Total     int `json:"total"`
		Active    int `json:"active"`
		Suspended int `json:"suspended"`
	} `json:"merchants"`
	Users struct {
		Total int `json:"total"`
	} `json:"users"`
	Markets struct {
		Total  int `json:"total"`
		Active int `json:"active"`
	} `json:"markets"`
	Orders struct {
		Today       int     `json:"today"`
		VolumeToday float64 `json:"volume_today"`
	} `json:"orders"`
	Fees struct {
		Today float64 `json:"today"`
	} `json:"fees"`
	Settlements struct {
		Pending int `json:"pending"`
	} `json:"settlements"`
	Series []SeriesPoint `json:"series"`
}

// SeriesPoint is one day of order activity in the 14-day series.
type SeriesPoint struct {
	Date   string  `json:"date"`
	Orders int     `json:"orders"`
	Volume float64 `json:"volume"`
}

// GetOverview aggregates platform totals for the dashboard.
func (s *Service) GetOverview(ctx context.Context) (Overview, error) {
	if s == nil || s.database == nil {
		return Overview{}, errors.New("admin query database is not configured")
	}
	overview := Overview{}
	const merchantsQuery = `
SELECT COUNT(*),
       COUNT(*) FILTER (WHERE status = 'active'),
       COUNT(*) FILTER (WHERE status = 'suspended')
FROM merchants`
	if err := s.database.QueryRowContext(ctx, merchantsQuery).Scan(
		&overview.Merchants.Total, &overview.Merchants.Active, &overview.Merchants.Suspended,
	); err != nil {
		return Overview{}, fmt.Errorf("overview merchants: %w", err)
	}
	if err := s.database.QueryRowContext(ctx, `SELECT COUNT(*) FROM platform_users`).Scan(&overview.Users.Total); err != nil {
		return Overview{}, fmt.Errorf("overview users: %w", err)
	}
	if err := s.database.QueryRowContext(ctx, `
SELECT COUNT(*), COUNT(*) FILTER (WHERE status = 'active') FROM markets`).Scan(
		&overview.Markets.Total, &overview.Markets.Active,
	); err != nil {
		return Overview{}, fmt.Errorf("overview markets: %w", err)
	}
	dayStart := time.Now().UTC().Truncate(24 * time.Hour)
	if err := s.database.QueryRowContext(ctx, `
SELECT COUNT(*), COALESCE(SUM(amount), 0) FROM orders WHERE created_at >= $1`,
		dayStart).Scan(&overview.Orders.Today, &overview.Orders.VolumeToday); err != nil {
		return Overview{}, fmt.Errorf("overview orders: %w", err)
	}
	if err := s.database.QueryRowContext(ctx, `
SELECT COALESCE(SUM(amount), 0) FROM fee_ledger
WHERE recipient = 'platform' AND created_at >= $1`,
		dayStart).Scan(&overview.Fees.Today); err != nil {
		return Overview{}, fmt.Errorf("overview fees: %w", err)
	}
	if err := s.database.QueryRowContext(ctx, `
SELECT COUNT(*) FROM markets m
JOIN events e ON e.id = m.event_id
WHERE e.status = 'resolved' AND m.settled_at IS NULL`).Scan(&overview.Settlements.Pending); err != nil {
		return Overview{}, fmt.Errorf("overview settlements: %w", err)
	}
	rows, err := s.database.QueryContext(ctx, `
SELECT to_char(date_trunc('day', created_at), 'YYYY-MM-DD') AS day,
       COUNT(*), COALESCE(SUM(amount), 0)
FROM orders
WHERE created_at >= date_trunc('day', NOW()) - INTERVAL '13 days'
GROUP BY 1`)
	if err != nil {
		return Overview{}, fmt.Errorf("overview series: %w", err)
	}
	defer rows.Close()
	byDay := map[string]SeriesPoint{}
	for rows.Next() {
		point := SeriesPoint{}
		if err := rows.Scan(&point.Date, &point.Orders, &point.Volume); err != nil {
			return Overview{}, fmt.Errorf("scan series: %w", err)
		}
		byDay[point.Date] = point
	}
	if err := rows.Err(); err != nil {
		return Overview{}, err
	}
	today := time.Now().UTC().Truncate(24 * time.Hour)
	for i := 13; i >= 0; i-- {
		date := today.AddDate(0, 0, -i).Format("2006-01-02")
		point, exists := byDay[date]
		if !exists {
			point = SeriesPoint{Date: date}
		}
		overview.Series = append(overview.Series, point)
	}
	return overview, nil
}

// EventRow is one event in the console list.
type EventRow struct {
	ID             string    `json:"id"`
	Title          string    `json:"title"`
	Description    string    `json:"description"`
	Category       string    `json:"category"`
	EndTime        time.Time `json:"end_time"`
	ResolutionTime time.Time `json:"resolution_time"`
	Status         string    `json:"status"`
	Outcome        *string   `json:"outcome,omitempty"`
	CreatedAt      time.Time `json:"created_at"`
	MarketCount    int       `json:"market_count"`
}

// ListEvents returns paginated events, optionally filtered by keyword,
// category, or status.
func (s *Service) ListEvents(ctx context.Context, q, category, status string, page, limit int) ([]EventRow, int, error) {
	if s == nil || s.database == nil {
		return nil, 0, errors.New("admin query database is not configured")
	}
	q = strings.TrimSpace(q)
	category = strings.TrimSpace(category)
	status = strings.TrimSpace(status)
	const where = `
WHERE ($1 = '' OR e.title ILIKE '%' || $1 || '%')
  AND ($2 = '' OR e.category = $2)
  AND ($3 = '' OR e.status = $3)`
	const selectQuery = `
SELECT e.id, e.title, e.description, e.category, e.end_time, e.resolution_time,
       e.status, e.outcome, e.created_at,
       (SELECT COUNT(*) FROM markets m WHERE m.event_id = e.id)
FROM events e` + where + `
ORDER BY e.created_at DESC
LIMIT $4 OFFSET $5`
	rows, err := s.database.QueryContext(ctx, selectQuery, q, category, status, limit, (page-1)*limit)
	if err != nil {
		return nil, 0, fmt.Errorf("list events: %w", err)
	}
	defer rows.Close()
	items := []EventRow{}
	for rows.Next() {
		item := EventRow{}
		if err := rows.Scan(
			&item.ID, &item.Title, &item.Description, &item.Category,
			&item.EndTime, &item.ResolutionTime, &item.Status, &item.Outcome,
			&item.CreatedAt, &item.MarketCount,
		); err != nil {
			return nil, 0, fmt.Errorf("scan event: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	var total int
	if err := s.database.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM events e "+where,
		q, category, status).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count events: %w", err)
	}
	return items, total, nil
}

// MarketRow is one market in the console list.
type MarketRow struct {
	ID            string     `json:"id"`
	MerchantID    string     `json:"merchant_id"`
	EventID       string     `json:"event_id"`
	Type          string     `json:"type"`
	Question      string     `json:"question"`
	Options       []string   `json:"options"`
	Status        string     `json:"status"`
	TotalVolume   float64    `json:"total_volume"`
	LiquidityPool float64    `json:"liquidity_pool"`
	CreatedAt     time.Time  `json:"created_at"`
	SettledAt     *time.Time `json:"settled_at,omitempty"`
}

// ListMarkets returns paginated markets, optionally filtered by merchant,
// event, status, or keyword.
func (s *Service) ListMarkets(ctx context.Context, merchantID, eventID, status, q string, page, limit int) ([]MarketRow, int, error) {
	if s == nil || s.database == nil {
		return nil, 0, errors.New("admin query database is not configured")
	}
	merchantID = strings.TrimSpace(merchantID)
	eventID = strings.TrimSpace(eventID)
	status = strings.TrimSpace(status)
	q = strings.TrimSpace(q)
	const where = `
WHERE ($1 = '' OR m.merchant_id::text = $1)
  AND ($2 = '' OR m.event_id::text = $2)
  AND ($3 = '' OR m.status = $3)
  AND ($4 = '' OR m.question ILIKE '%' || $4 || '%')`
	const selectQuery = `
SELECT m.id, m.merchant_id, m.event_id, m.type, m.question, m.options,
       m.status, m.total_volume, m.liquidity_pool, m.created_at, m.settled_at
FROM markets m` + where + `
ORDER BY m.created_at DESC
LIMIT $5 OFFSET $6`
	rows, err := s.database.QueryContext(ctx, selectQuery, merchantID, eventID, status, q, limit, (page-1)*limit)
	if err != nil {
		return nil, 0, fmt.Errorf("list markets: %w", err)
	}
	defer rows.Close()
	items := []MarketRow{}
	for rows.Next() {
		item := MarketRow{}
		var options []byte
		if err := rows.Scan(
			&item.ID, &item.MerchantID, &item.EventID, &item.Type, &item.Question,
			&options, &item.Status, &item.TotalVolume, &item.LiquidityPool,
			&item.CreatedAt, &item.SettledAt,
		); err != nil {
			return nil, 0, fmt.Errorf("scan market: %w", err)
		}
		if err := json.Unmarshal(options, &item.Options); err != nil {
			item.Options = []string{}
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	var total int
	if err := s.database.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM markets m "+where,
		merchantID, eventID, status, q).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count markets: %w", err)
	}
	return items, total, nil
}

// ErrNotFound indicates the requested resource does not exist.
var ErrNotFound = errors.New("resource was not found")

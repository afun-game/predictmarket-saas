package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/afun-game/predictmarket-saas/internal/adminauth"
	"github.com/afun-game/predictmarket-saas/internal/adminquery"
	"github.com/afun-game/predictmarket-saas/internal/auth"
	"github.com/afun-game/predictmarket-saas/internal/event"
	"github.com/afun-game/predictmarket-saas/internal/market"
	"github.com/afun-game/predictmarket-saas/internal/merchant"
	"github.com/afun-game/predictmarket-saas/internal/order"
	"github.com/afun-game/predictmarket-saas/internal/settlement"
	"github.com/afun-game/predictmarket-saas/internal/wallet"
	"golang.org/x/crypto/bcrypt"
)

// settlementStub lets voidMarket tests drive the settlement boundary.
type settlementStub struct {
	voidErr    error
	settleErr  error
	settledIDs []string
}

func (s settlementStub) SettleEvent(context.Context, string) error { return nil }
func (s settlementStub) SettleMarket(_ context.Context, marketID, _ string) error {
	if s.settleErr != nil {
		return s.settleErr
	}
	return nil
}
func (s settlementStub) SettledMarketIDs() []string               { return s.settledIDs }
func (s settlementStub) VoidMarket(context.Context, string) error { return s.voidErr }

// newAdminSession creates a super-admin account, logs it in, and returns the
// manager, session token, and in-memory action log.
func newAdminSession(t *testing.T) (*adminauth.Manager, string, *adminauth.MemoryActionLog) {
	t.Helper()
	repo := adminauth.NewMemoryRepository()
	logs := adminauth.NewMemoryActionLog()
	hash, err := bcrypt.GenerateFromPassword([]byte("pw"), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("hash password: %v", err)
	}
	if err := repo.Create(context.Background(), adminauth.Account{
		Username:     "boss",
		PasswordHash: string(hash),
		Role:         adminauth.RoleSuperAdmin,
		Status:       adminauth.StatusActive,
	}); err != nil {
		t.Fatalf("create admin account: %v", err)
	}
	manager, err := adminauth.NewManager(repo, logs, bytes.Repeat([]byte("k"), 32))
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}
	token, _, err := manager.Login(context.Background(), "boss", "pw")
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	return manager, token, logs
}

// newAdminTestHandler builds the full HTTP handler with the admin console
// routes registered for the provided services and config.
func newAdminTestHandler(
	t *testing.T,
	eventService event.Service,
	marketService market.Service,
	config AdminConfig,
) http.Handler {
	t.Helper()
	// The order service must share the market service so the admin order
	// book (order.Service.GetOrderBook) sees the same market universe.
	walletService := wallet.NewService()
	return NewHandler(
		merchant.NewService(),
		eventService,
		marketService,
		walletService,
		order.NewServiceWithDependencies(marketService, walletService),
		nil, // currency service is unused by the admin console
		"admin-secret",
		config,
	)
}

// adminRequest issues one authenticated admin console request.
func adminRequest(
	t *testing.T,
	handler http.Handler,
	method, path string,
	body []byte,
	token string,
) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, path, bytes.NewReader(body))
	if token != "" {
		request.AddCookie(&http.Cookie{Name: auth.AdminSessionCookie, Value: token})
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	return recorder
}

func decodeAdminData[T any](t *testing.T, recorder *httptest.ResponseRecorder) T {
	t.Helper()
	var payload struct {
		Data T `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode admin response %s: %v", recorder.Body.String(), err)
	}
	return payload.Data
}

type adminErrorPayload struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func decodeAdminError(t *testing.T, recorder *httptest.ResponseRecorder) string {
	t.Helper()
	var payload adminErrorPayload
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode admin error %s: %v", recorder.Body.String(), err)
	}
	return payload.Error.Code
}

func TestAdminMarketLifecycle(t *testing.T) {
	t.Parallel()

	eventService := event.NewService()
	marketService := market.NewService()
	manager, token, logs := newAdminSession(t)
	handler := newAdminTestHandler(t, eventService, marketService, AdminConfig{Accounts: manager})

	createdEvent, err := eventService.Create(context.Background(), &event.CreateRequest{
		SourceType:     "custom",
		SourceID:       "admin-monitor-lifecycle",
		Title:          "Admin monitor event",
		Category:       "test",
		EndTime:        "2027-08-10T12:00:00Z",
		ResolutionTime: "2027-08-10T13:00:00Z",
	})
	if err != nil {
		t.Fatalf("create event: %v", err)
	}

	merchantID := "11111111-1111-4111-8111-111111111111"
	createBody := fmt.Sprintf(
		`{"merchant_id":%q,"event_id":%q,"type":"binary","question":"Will it rain?","options":["Yes","No"],"liquidity_pool":100}`,
		merchantID, createdEvent.ID,
	)
	created := adminRequest(t, handler, http.MethodPost, "/api/v1/admin/markets", []byte(createBody), token)
	if created.Code != http.StatusCreated {
		t.Fatalf("create market status = %d, body = %s", created.Code, created.Body.String())
	}
	marketData := decodeAdminData[struct {
		ID            string   `json:"id"`
		MerchantID    string   `json:"merchant_id"`
		EventID       string   `json:"event_id"`
		Type          string   `json:"type"`
		Question      string   `json:"question"`
		Options       []string `json:"options"`
		Status        string   `json:"status"`
		LiquidityPool float64  `json:"liquidity_pool"`
	}](t, created)
	if marketData.ID == "" || marketData.Status != "active" {
		t.Fatalf("created market = %+v", marketData)
	}
	if marketData.MerchantID != merchantID || marketData.EventID != createdEvent.ID {
		t.Errorf("created market references = %+v", marketData)
	}
	if marketData.Type != "binary" || len(marketData.Options) != 2 || marketData.LiquidityPool != 100 {
		t.Errorf("created market terms = %+v", marketData)
	}

	// getMarket carries the event title and an (empty) order book.
	fetched := adminRequest(t, handler, http.MethodGet, "/api/v1/admin/markets/"+marketData.ID, nil, token)
	if fetched.Code != http.StatusOK {
		t.Fatalf("get market status = %d, body = %s", fetched.Code, fetched.Body.String())
	}
	detail := decodeAdminData[struct {
		ID         string `json:"id"`
		EventTitle string `json:"event_title"`
		Orderbook  struct {
			Bids []struct {
				Option string  `json:"option"`
				Price  float64 `json:"price"`
				Amount float64 `json:"amount"`
			} `json:"bids"`
			Asks []struct {
				Option string  `json:"option"`
				Price  float64 `json:"price"`
				Amount float64 `json:"amount"`
			} `json:"asks"`
		} `json:"orderbook"`
	}](t, fetched)
	if detail.EventTitle != "Admin monitor event" {
		t.Errorf("event_title = %q, want %q", detail.EventTitle, "Admin monitor event")
	}
	if len(detail.Orderbook.Bids) != 0 || len(detail.Orderbook.Asks) != 0 {
		t.Errorf("orderbook = %+v, want empty books", detail.Orderbook)
	}

	// Unknown market 404s.
	missing := adminRequest(t, handler, http.MethodGet, "/api/v1/admin/markets/22222222-2222-4222-8222-222222222222", nil, token)
	if missing.Code != http.StatusNotFound {
		t.Errorf("get missing market status = %d, want 404", missing.Code)
	}
	if code := decodeAdminError(t, missing); code != "not_found" {
		t.Errorf("get missing market error code = %q, want not_found", code)
	}

	// Status transitions: active -> suspended -> active.
	for _, status := range []string{"suspended", "active"} {
		transitioned := adminRequest(
			t, handler, http.MethodPatch,
			"/api/v1/admin/markets/"+marketData.ID+"/status",
			[]byte(fmt.Sprintf(`{"status":%q}`, status)),
			token,
		)
		if transitioned.Code != http.StatusOK {
			t.Fatalf("update market status %q code = %d, body = %s", status, transitioned.Code, transitioned.Body.String())
		}
		state := decodeAdminData[struct {
			ID     string `json:"id"`
			Status string `json:"status"`
		}](t, transitioned)
		if state.Status != status {
			t.Errorf("updated status = %q, want %q", state.Status, status)
		}
	}

	// Liquidity requires the exact confirmation word.
	wrongConfirm := adminRequest(
		t, handler, http.MethodPost,
		"/api/v1/admin/markets/"+marketData.ID+"/liquidity",
		[]byte(`{"amount":50,"confirm":"nope"}`),
		token,
	)
	if wrongConfirm.Code != http.StatusBadRequest {
		t.Errorf("liquidity wrong confirm status = %d, want 400", wrongConfirm.Code)
	}

	// Missing amount is a validation error even with the right confirm.
	noAmount := adminRequest(
		t, handler, http.MethodPost,
		"/api/v1/admin/markets/"+marketData.ID+"/liquidity",
		[]byte(`{"confirm":"liquidity"}`),
		token,
	)
	if noAmount.Code != http.StatusBadRequest {
		t.Errorf("liquidity no amount status = %d, want 400", noAmount.Code)
	}

	// Top up the pool while the market is active; the response carries the new balance.
	topped := adminRequest(
		t, handler, http.MethodPost,
		"/api/v1/admin/markets/"+marketData.ID+"/liquidity",
		[]byte(`{"amount":50,"confirm":"liquidity"}`),
		token,
	)
	if topped.Code != http.StatusOK {
		t.Fatalf("add liquidity status = %d, body = %s", topped.Code, topped.Body.String())
	}
	liquidity := decodeAdminData[struct {
		ID            string  `json:"id"`
		LiquidityPool float64 `json:"liquidity_pool"`
	}](t, topped)
	if liquidity.ID != marketData.ID || liquidity.LiquidityPool != 150 {
		t.Errorf("liquidity result = %+v, want pool 150", liquidity)
	}

	// Close the market: status is then terminal.
	closed := adminRequest(
		t, handler, http.MethodPatch,
		"/api/v1/admin/markets/"+marketData.ID+"/status",
		[]byte(`{"status":"closed"}`),
		token,
	)
	if closed.Code != http.StatusOK {
		t.Fatalf("close market status = %d, body = %s", closed.Code, closed.Body.String())
	}

	// Any further transition from closed is a conflict.
	conflict := adminRequest(
		t, handler, http.MethodPatch,
		"/api/v1/admin/markets/"+marketData.ID+"/status",
		[]byte(`{"status":"active"}`),
		token,
	)
	if conflict.Code != http.StatusConflict {
		t.Errorf("closed->active status = %d, want 409", conflict.Code)
	}
	if code := decodeAdminError(t, conflict); code != "invalid_transition" {
		t.Errorf("closed->active error code = %q, want invalid_transition", code)
	}

	// Unsupported status values are validation errors.
	invalid := adminRequest(
		t, handler, http.MethodPatch,
		"/api/v1/admin/markets/"+marketData.ID+"/status",
		[]byte(`{"status":"settled"}`),
		token,
	)
	if invalid.Code != http.StatusBadRequest {
		t.Errorf("settled status code = %d, want 400", invalid.Code)
	}
	if code := decodeAdminError(t, invalid); code != "validation_error" {
		t.Errorf("settled status error code = %q, want validation_error", code)
	}

	// Liquidity on a closed market is a conflict.
	closedLiquidity := adminRequest(
		t, handler, http.MethodPost,
		"/api/v1/admin/markets/"+marketData.ID+"/liquidity",
		[]byte(`{"amount":10,"confirm":"liquidity"}`),
		token,
	)
	if closedLiquidity.Code != http.StatusConflict {
		t.Errorf("closed liquidity status = %d, want 409", closedLiquidity.Code)
	}

	// Every state change above recorded an audit row.
	audited := map[string]int{}
	for _, action := range logs.Actions() {
		if action.Resource == "market" && action.ResourceID == marketData.ID {
			audited[action.Action]++
		}
	}
	for action, want := range map[string]int{"create.market": 1, "status.market": 3, "liquidity.market": 1} {
		if audited[action] != want {
			t.Errorf("audit count for %s = %d, want %d (all: %v)", action, audited[action], want, audited)
		}
	}
}

func TestAdminVoidMarket(t *testing.T) {
	t.Parallel()

	marketID := "22222222-2222-4222-8222-222222222222"

	t.Run("nil settlement returns 503", func(t *testing.T) {
		manager, token, _ := newAdminSession(t)
		handler := newAdminTestHandler(t, event.NewService(), market.NewService(), AdminConfig{Accounts: manager})
		recorder := adminRequest(
			t, handler, http.MethodPost,
			"/api/v1/admin/markets/"+marketID+"/void",
			[]byte(`{"confirm":"void"}`),
			token,
		)
		if recorder.Code != http.StatusServiceUnavailable {
			t.Errorf("void status = %d, want 503, body = %s", recorder.Code, recorder.Body.String())
		}
		if code := decodeAdminError(t, recorder); code != "service_unavailable" {
			t.Errorf("void error code = %q, want service_unavailable", code)
		}
	})

	t.Run("wrong confirm returns 400", func(t *testing.T) {
		manager, token, _ := newAdminSession(t)
		handler := newAdminTestHandler(
			t, event.NewService(), market.NewService(),
			AdminConfig{Accounts: manager, Settlement: settlementStub{}},
		)
		recorder := adminRequest(
			t, handler, http.MethodPost,
			"/api/v1/admin/markets/"+marketID+"/void",
			[]byte(`{"confirm":"wrong"}`),
			token,
		)
		if recorder.Code != http.StatusBadRequest {
			t.Errorf("void wrong confirm status = %d, want 400", recorder.Code)
		}
	})

	t.Run("already settled returns 409", func(t *testing.T) {
		manager, token, _ := newAdminSession(t)
		handler := newAdminTestHandler(
			t, event.NewService(), market.NewService(),
			AdminConfig{Accounts: manager, Settlement: settlementStub{voidErr: settlement.ErrMarketAlreadySettled}},
		)
		recorder := adminRequest(
			t, handler, http.MethodPost,
			"/api/v1/admin/markets/"+marketID+"/void",
			[]byte(`{"confirm":"void"}`),
			token,
		)
		if recorder.Code != http.StatusConflict {
			t.Errorf("void already settled status = %d, want 409", recorder.Code)
		}
		if code := decodeAdminError(t, recorder); code != "already_settled" {
			t.Errorf("void error code = %q, want already_settled", code)
		}
	})

	t.Run("missing market returns 404", func(t *testing.T) {
		manager, token, _ := newAdminSession(t)
		handler := newAdminTestHandler(
			t, event.NewService(), market.NewService(),
			AdminConfig{Accounts: manager, Settlement: settlementStub{voidErr: settlement.ErrMarketNotFound}},
		)
		recorder := adminRequest(
			t, handler, http.MethodPost,
			"/api/v1/admin/markets/"+marketID+"/void",
			[]byte(`{"confirm":"void"}`),
			token,
		)
		if recorder.Code != http.StatusNotFound {
			t.Errorf("void missing market status = %d, want 404", recorder.Code)
		}
		if code := decodeAdminError(t, recorder); code != "not_found" {
			t.Errorf("void error code = %q, want not_found", code)
		}
	})

	t.Run("success voids and audits", func(t *testing.T) {
		manager, token, logs := newAdminSession(t)
		handler := newAdminTestHandler(
			t, event.NewService(), market.NewService(),
			AdminConfig{Accounts: manager, Settlement: settlementStub{}},
		)
		recorder := adminRequest(
			t, handler, http.MethodPost,
			"/api/v1/admin/markets/"+marketID+"/void",
			[]byte(`{"confirm":"void"}`),
			token,
		)
		if recorder.Code != http.StatusOK {
			t.Fatalf("void status = %d, body = %s", recorder.Code, recorder.Body.String())
		}
		state := decodeAdminData[struct {
			ID     string `json:"id"`
			Status string `json:"status"`
		}](t, recorder)
		if state.ID != marketID || state.Status != "voided" {
			t.Errorf("void result = %+v, want {id market, status voided}", state)
		}
		audited := false
		for _, action := range logs.Actions() {
			if action.Action == "void.market" && action.Resource == "market" && action.ResourceID == marketID {
				audited = true
			}
		}
		if !audited {
			t.Errorf("no void.market audit row recorded: %+v", logs.Actions())
		}
	})
}

// Exact SQL for the adminquery read queries, mirroring internal/adminquery.
// QueryMatcherEqual compares byte-for-byte, so these must match the service
// query text exactly.
const (
	ordersWhere = `
WHERE ($1 = '' OR merchant_id::text = $1)
  AND ($2 = '' OR user_id = $2)
  AND ($3 = '' OR market_id::text = $3)
  AND ($4 = '' OR status = $4)`
	ordersSelect = `
SELECT id, merchant_id, user_id, market_id, type, option, amount, filled_amount,
       currency, price, status, created_at, filled_at
FROM (
    SELECT o.id, o.merchant_id::text, o.user_id, o.market_id::text, o.type, o.option,
           o.amount, o.filled_amount, o.currency, o.price, o.status, o.created_at, o.filled_at
    FROM orders o
    WHERE ($1 = '' OR o.merchant_id::text = $1)
      AND ($2 = '' OR o.user_id = $2)
      AND ($3 = '' OR o.market_id::text = $3)
      AND ($4 = '' OR o.status = $4)
    UNION ALL
    SELECT b.id, b.merchant_id::text, b.user_id, b.market_id::text, 'bet' AS type, b.option,
           b.stake, b.stake, b.currency, 0, b.status, b.created_at, b.settled_at
    FROM parimutuel_bets b
    WHERE ($1 = '' OR b.merchant_id::text = $1)
      AND ($2 = '' OR b.user_id = $2)
      AND ($3 = '' OR b.market_id::text = $3)
      AND ($4 = '' OR b.status = $4)
) AS merged
ORDER BY created_at DESC
LIMIT $5 OFFSET $6`

	ordersCount = `
SELECT COUNT(*) FROM (
    SELECT o.id FROM orders o
    WHERE ($1 = '' OR o.merchant_id::text = $1)
      AND ($2 = '' OR o.user_id = $2)
      AND ($3 = '' OR o.market_id::text = $3)
      AND ($4 = '' OR o.status = $4)
    UNION ALL
    SELECT b.id FROM parimutuel_bets b
    WHERE ($1 = '' OR b.merchant_id::text = $1)
      AND ($2 = '' OR b.user_id = $2)
      AND ($3 = '' OR b.market_id::text = $3)
      AND ($4 = '' OR b.status = $4)
) AS merged`

	transactionsWhere = `
WHERE ($1 = '' OR w.merchant_id::text = $1)
  AND ($2 = '' OR w.user_id = $2)
  AND ($3 = '' OR t.type = $3)`
	transactionsSelect = `
SELECT t.id, t.wallet_id, t.type, t.amount, t.currency, t.status, t.created_at
FROM transactions t
JOIN wallets w ON w.id = t.wallet_id` + transactionsWhere + `
ORDER BY t.created_at DESC
LIMIT $4 OFFSET $5`

	marketsWhere = `
WHERE ($1 = '' OR m.merchant_id::text = $1)
  AND ($2 = '' OR m.event_id::text = $2)
  AND ($3 = '' OR m.status = $3)
  AND ($4 = '' OR m.question ILIKE '%' || $4 || '%')`
	marketsSelect = `
SELECT m.id, m.merchant_id, m.event_id, m.type, m.category, m.question, m.options,
       m.status, m.total_volume, m.liquidity_pool, m.created_at, m.settled_at
FROM markets m` + marketsWhere + `
ORDER BY m.created_at DESC
LIMIT $5 OFFSET $6`

	eventsWhere = `
WHERE ($1 = '' OR e.title ILIKE '%' || $1 || '%')
  AND ($2 = '' OR e.category = $2)
  AND ($3 = '' OR e.status = $3)
  AND ($4 = '' OR e.source_type = $4)`
	eventsSelect = `
SELECT e.id, e.source_type, e.title, e.description, e.category, e.end_time, e.resolution_time,
       e.status, e.outcome, e.created_at,
       (SELECT COUNT(*) FROM markets m WHERE m.event_id = e.id)
FROM events e` + eventsWhere + `
ORDER BY e.created_at DESC
LIMIT $5 OFFSET $6`

	auditLogsSelect = `
SELECT l.id, l.admin_id, COALESCE(a.username, ''), l.action, l.resource, l.resource_id,
       l.before_state, l.after_state, l.client_ip, l.created_at
FROM admin_action_logs l
LEFT JOIN admin_accounts a ON a.id = l.admin_id
ORDER BY l.created_at DESC
LIMIT $1 OFFSET $2`

	overviewMerchantsQuery = `
SELECT COUNT(*),
       COUNT(*) FILTER (WHERE status = 'active'),
       COUNT(*) FILTER (WHERE status = 'suspended')
FROM merchants`
	overviewMarketsQuery = `
SELECT COUNT(*), COUNT(*) FILTER (WHERE status = 'active') FROM markets`
	overviewOrdersQuery = `
SELECT COUNT(*), COALESCE(SUM(amount), 0) FROM orders WHERE created_at >= $1`
	overviewFeesQuery = `
SELECT COALESCE(SUM(amount), 0) FROM fee_ledger
WHERE recipient = 'platform' AND created_at >= $1`
	overviewSettlementsQuery = `
SELECT COUNT(*) FROM markets m
JOIN events e ON e.id = m.event_id
WHERE e.status = 'resolved' AND m.settled_at IS NULL`
	overviewSeriesQuery = `
SELECT to_char(date_trunc('day', created_at), 'YYYY-MM-DD') AS day,
       COUNT(*), COALESCE(SUM(amount), 0)
FROM orders
WHERE created_at >= date_trunc('day', NOW()) - INTERVAL '13 days'
GROUP BY 1`
)

func TestAdminOverviewShape(t *testing.T) {
	t.Parallel()

	database, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer database.Close()

	mock.ExpectQuery(overviewMerchantsQuery).
		WillReturnRows(sqlmock.NewRows([]string{"count", "active", "suspended"}).AddRow(2, 1, 1))
	mock.ExpectQuery(`SELECT COUNT(*) FROM platform_users`).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(5))
	mock.ExpectQuery(overviewMarketsQuery).
		WillReturnRows(sqlmock.NewRows([]string{"count", "active"}).AddRow(4, 2))
	mock.ExpectQuery(overviewOrdersQuery).WithArgs(sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"count", "volume"}).AddRow(12, 340.5))
	mock.ExpectQuery(overviewFeesQuery).WithArgs(sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"fees"}).AddRow(25.75))
	mock.ExpectQuery(overviewSettlementsQuery).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
	mock.ExpectQuery(overviewSeriesQuery).
		WillReturnRows(sqlmock.NewRows([]string{"day", "orders", "volume"}).
			AddRow("2026-07-30", 3, 50.0))

	manager, token, _ := newAdminSession(t)
	handler := newAdminTestHandler(
		t, event.NewService(), market.NewService(),
		AdminConfig{Accounts: manager, Queries: adminquery.New(database)},
	)

	recorder := adminRequest(t, handler, http.MethodGet, "/api/v1/admin/overview", nil, token)
	if recorder.Code != http.StatusOK {
		t.Fatalf("overview status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	overview := decodeAdminData[struct {
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
		Series []struct {
			Date   string  `json:"date"`
			Orders int     `json:"orders"`
			Volume float64 `json:"volume"`
		} `json:"series"`
	}](t, recorder)

	if overview.Merchants.Total != 2 || overview.Merchants.Active != 1 || overview.Merchants.Suspended != 1 {
		t.Errorf("overview merchants = %+v", overview.Merchants)
	}
	if overview.Users.Total != 5 {
		t.Errorf("overview users.total = %d, want 5", overview.Users.Total)
	}
	if overview.Markets.Total != 4 || overview.Markets.Active != 2 {
		t.Errorf("overview markets = %+v", overview.Markets)
	}
	if overview.Orders.Today != 12 || overview.Orders.VolumeToday != 340.5 {
		t.Errorf("overview orders = (%d, %v), want (12, 340.5)", overview.Orders.Today, overview.Orders.VolumeToday)
	}
	if overview.Fees.Today != 25.75 {
		t.Errorf("overview fees.today = %v, want 25.75", overview.Fees.Today)
	}
	if overview.Settlements.Pending != 1 {
		t.Errorf("overview settlements.pending = %d, want 1", overview.Settlements.Pending)
	}
	if len(overview.Series) != 14 {
		t.Errorf("overview series length = %d, want 14", len(overview.Series))
	}
	dates := map[string]bool{}
	for _, point := range overview.Series {
		if point.Date == "" {
			t.Error("overview series contains a point without a date")
		}
		dates[point.Date] = true
	}
	if !dates["2026-07-30"] {
		t.Errorf("overview series is missing the reported day 2026-07-30: %v", overview.Series)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet sql expectations: %v", err)
	}
}

func TestAdminListEventsFiltersBySourceType(t *testing.T) {
	t.Parallel()

	database, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer database.Close()

	now := time.Now().UTC()
	mock.ExpectQuery(eventsSelect).WithArgs("cup", "sports", "active", "lmb", 20, 0).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "source_type", "title", "description", "category", "end_time", "resolution_time",
			"status", "outcome", "created_at", "market_count",
		}).AddRow("event-lmb-1", "lmb", "LMB cup", "", "sports", now, now, "active", nil, now, 2))
	mock.ExpectQuery("SELECT COUNT(*) FROM events e "+eventsWhere).
		WithArgs("cup", "sports", "active", "lmb").
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))

	manager, token, _ := newAdminSession(t)
	handler := newAdminTestHandler(
		t, event.NewService(), market.NewService(),
		AdminConfig{Accounts: manager, Queries: adminquery.New(database)},
	)

	recorder := adminRequest(
		t,
		handler,
		http.MethodGet,
		"/api/v1/admin/events?q=cup&category=sports&status=active&source_type=lmb",
		nil,
		token,
	)
	if recorder.Code != http.StatusOK {
		t.Fatalf("list events status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	list := decodeAdminData[struct {
		Items []struct {
			ID         string `json:"id"`
			SourceType string `json:"source_type"`
		} `json:"items"`
		Total int `json:"total"`
	}](t, recorder)
	if list.Total != 1 || len(list.Items) != 1 {
		t.Fatalf("event list = %+v, want one event", list)
	}
	if list.Items[0].ID != "event-lmb-1" || list.Items[0].SourceType != "lmb" {
		t.Errorf("event item = %+v, want event-lmb-1 from lmb", list.Items[0])
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet sql expectations: %v", err)
	}
}

func TestAdminListOrdersFilters(t *testing.T) {
	t.Parallel()

	database, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer database.Close()

	now := time.Now().UTC()
	mock.ExpectQuery(ordersSelect).WithArgs("m-1", "u-1", "mk-1", "filled", 50, 50).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "merchant_id", "user_id", "market_id", "type", "option",
			"amount", "filled_amount", "currency", "price", "status",
			"created_at", "filled_at",
		}).AddRow("ord-1", "m-1", "u-1", "mk-1", "buy", "Yes",
			100.0, 100.0, "USD", 0.65, "filled", now, now))
	mock.ExpectQuery(ordersCount).
		WithArgs("m-1", "u-1", "mk-1", "filled").
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))

	manager, token, _ := newAdminSession(t)
	handler := newAdminTestHandler(
		t, event.NewService(), market.NewService(),
		AdminConfig{Accounts: manager, Queries: adminquery.New(database)},
	)

	recorder := adminRequest(
		t, handler, http.MethodGet,
		"/api/v1/admin/orders?merchant_id=m-1&user_id=u-1&market_id=mk-1&status=filled&page=2&limit=50",
		nil, token,
	)
	if recorder.Code != http.StatusOK {
		t.Fatalf("list orders status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	list := decodeAdminData[struct {
		Items []struct {
			ID           string  `json:"id"`
			MerchantID   string  `json:"merchant_id"`
			UserID       string  `json:"user_id"`
			MarketID     string  `json:"market_id"`
			Type         string  `json:"type"`
			Option       string  `json:"option"`
			Amount       float64 `json:"amount"`
			FilledAmount float64 `json:"filled_amount"`
			Currency     string  `json:"currency"`
			Price        float64 `json:"price"`
			Status       string  `json:"status"`
		} `json:"items"`
		Total int `json:"total"`
	}](t, recorder)
	if list.Total != 1 || len(list.Items) != 1 {
		t.Fatalf("order list = %+v", list)
	}
	item := list.Items[0]
	if item.ID != "ord-1" || item.MerchantID != "m-1" || item.UserID != "u-1" || item.MarketID != "mk-1" {
		t.Errorf("order item = %+v", item)
	}
	if item.Status != "filled" || item.Price != 0.65 || item.Amount != 100.0 {
		t.Errorf("order terms = %+v", item)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet sql expectations: %v", err)
	}
}

func TestAdminListTransactionsFilters(t *testing.T) {
	t.Parallel()

	database, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer database.Close()

	now := time.Now().UTC()
	mock.ExpectQuery(transactionsSelect).WithArgs("m-1", "u-1", "deposit", 20, 0).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "wallet_id", "type", "amount", "currency", "status", "created_at",
		}).AddRow("txn-1", "wal-1", "deposit", 25.5, "USD", "completed", now))
	mock.ExpectQuery("SELECT COUNT(*) FROM transactions t JOIN wallets w ON w.id = t.wallet_id "+transactionsWhere).
		WithArgs("m-1", "u-1", "deposit").
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(7))

	manager, token, _ := newAdminSession(t)
	handler := newAdminTestHandler(
		t, event.NewService(), market.NewService(),
		AdminConfig{Accounts: manager, Queries: adminquery.New(database)},
	)

	recorder := adminRequest(
		t, handler, http.MethodGet,
		"/api/v1/admin/transactions?merchant_id=m-1&user_id=u-1&type=deposit",
		nil, token,
	)
	if recorder.Code != http.StatusOK {
		t.Fatalf("list transactions status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	list := decodeAdminData[struct {
		Items []struct {
			ID       string  `json:"id"`
			WalletID string  `json:"wallet_id"`
			Type     string  `json:"type"`
			Amount   float64 `json:"amount"`
			Currency string  `json:"currency"`
			Status   string  `json:"status"`
		} `json:"items"`
		Total int `json:"total"`
	}](t, recorder)
	if list.Total != 7 || len(list.Items) != 1 {
		t.Fatalf("transaction list = %+v", list)
	}
	item := list.Items[0]
	if item.ID != "txn-1" || item.WalletID != "wal-1" || item.Type != "deposit" ||
		item.Amount != 25.5 || item.Currency != "USD" || item.Status != "completed" {
		t.Errorf("transaction item = %+v", item)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet sql expectations: %v", err)
	}
}

func TestAdminListAuditLogs(t *testing.T) {
	t.Parallel()

	database, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer database.Close()

	now := time.Now().UTC()
	mock.ExpectQuery(auditLogsSelect).WithArgs(20, 0).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "admin_id", "admin_username", "action", "resource", "resource_id",
			"before_state", "after_state", "client_ip", "created_at",
		}).AddRow("log-1", "adm-1", "boss", "status.market", "market", "mk-1",
			[]byte(`{"status":"active"}`), []byte(`{"status":"suspended"}`), "10.0.0.1", now))
	mock.ExpectQuery(`SELECT COUNT(*) FROM admin_action_logs`).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))

	manager, token, _ := newAdminSession(t)
	handler := newAdminTestHandler(
		t, event.NewService(), market.NewService(),
		AdminConfig{Accounts: manager, Queries: adminquery.New(database)},
	)

	recorder := adminRequest(t, handler, http.MethodGet, "/api/v1/admin/audit-logs", nil, token)
	if recorder.Code != http.StatusOK {
		t.Fatalf("list audit logs status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	list := decodeAdminData[struct {
		Items []struct {
			ID            string `json:"id"`
			AdminID       string `json:"admin_id"`
			AdminUsername string `json:"admin_username"`
			Action        string `json:"action"`
			Resource      string `json:"resource"`
			ResourceID    string `json:"resource_id"`
			ClientIP      string `json:"client_ip"`
		} `json:"items"`
		Total int `json:"total"`
	}](t, recorder)
	if list.Total != 1 || len(list.Items) != 1 {
		t.Fatalf("audit list = %+v", list)
	}
	item := list.Items[0]
	if item.ID != "log-1" || item.AdminUsername != "boss" || item.Action != "status.market" ||
		item.Resource != "market" || item.ResourceID != "mk-1" || item.ClientIP != "10.0.0.1" {
		t.Errorf("audit item = %+v", item)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet sql expectations: %v", err)
	}
}

func TestAdminListMarketsFilters(t *testing.T) {
	t.Parallel()

	database, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer database.Close()

	now := time.Now().UTC()
	mock.ExpectQuery(marketsSelect).WithArgs("m-1", "ev-1", "active", "rain", 20, 0).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "merchant_id", "event_id", "type", "category", "question", "options",
			"status", "total_volume", "liquidity_pool", "created_at", "settled_at",
		}).AddRow("mk-1", "m-1", "ev-1", "binary", "sports", "Will it rain?",
			[]byte(`["Yes","No"]`), "active", 500.0, 1000.0, now, nil))
	mock.ExpectQuery("SELECT COUNT(*) FROM markets m "+marketsWhere).
		WithArgs("m-1", "ev-1", "active", "rain").
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))

	manager, token, _ := newAdminSession(t)
	handler := newAdminTestHandler(
		t, event.NewService(), market.NewService(),
		AdminConfig{Accounts: manager, Queries: adminquery.New(database)},
	)

	recorder := adminRequest(
		t, handler, http.MethodGet,
		"/api/v1/admin/markets?merchant_id=m-1&event_id=ev-1&status=active&q=rain",
		nil, token,
	)
	if recorder.Code != http.StatusOK {
		t.Fatalf("list markets status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	list := decodeAdminData[struct {
		Items []struct {
			ID            string   `json:"id"`
			MerchantID    string   `json:"merchant_id"`
			EventID       string   `json:"event_id"`
			Type          string   `json:"type"`
			Question      string   `json:"question"`
			Options       []string `json:"options"`
			Status        string   `json:"status"`
			TotalVolume   float64  `json:"total_volume"`
			LiquidityPool float64  `json:"liquidity_pool"`
		} `json:"items"`
		Total int `json:"total"`
	}](t, recorder)
	if list.Total != 1 || len(list.Items) != 1 {
		t.Fatalf("market list = %+v", list)
	}
	item := list.Items[0]
	if item.ID != "mk-1" || item.MerchantID != "m-1" || item.EventID != "ev-1" ||
		item.Status != "active" || item.Question != "Will it rain?" ||
		len(item.Options) != 2 || item.TotalVolume != 500.0 || item.LiquidityPool != 1000.0 {
		t.Errorf("market item = %+v", item)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet sql expectations: %v", err)
	}
}

func TestAdminSettleMarket(t *testing.T) {
	t.Parallel()

	marketID := "11111111-2222-4333-8444-555555555555"
	t.Run("settle success", func(t *testing.T) {
		manager, token, _ := newAdminSession(t)
		handler := newAdminTestHandler(
			t, event.NewService(), market.NewService(),
			AdminConfig{Accounts: manager, Settlement: settlementStub{}},
		)
		recorder := adminRequest(
			t, handler, http.MethodPost,
			"/api/v1/admin/markets/"+marketID+"/settle",
			[]byte(`{"winning_option":"Yes","confirm":"settle"}`),
			token,
		)
		if recorder.Code != http.StatusOK {
			t.Fatalf("settle status = %d, body = %s", recorder.Code, recorder.Body.String())
		}
		var payload struct {
			Data struct {
				ID            string `json:"id"`
				Status        string `json:"status"`
				WinningOption string `json:"winning_option"`
			} `json:"data"`
		}
		if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
			t.Fatalf("decode settle response: %v", err)
		}
		if payload.Data.ID != marketID || payload.Data.Status != "settled" || payload.Data.WinningOption != "Yes" {
			t.Errorf("settle response = %#v", payload.Data)
		}
	})

	t.Run("missing confirm returns 400", func(t *testing.T) {
		manager, token, _ := newAdminSession(t)
		handler := newAdminTestHandler(
			t, event.NewService(), market.NewService(),
			AdminConfig{Accounts: manager, Settlement: settlementStub{}},
		)
		recorder := adminRequest(
			t, handler, http.MethodPost,
			"/api/v1/admin/markets/"+marketID+"/settle",
			[]byte(`{"winning_option":"Yes"}`),
			token,
		)
		if recorder.Code != http.StatusBadRequest {
			t.Errorf("settle without confirm status = %d, want 400", recorder.Code)
		}
	})

	t.Run("already settled returns 409", func(t *testing.T) {
		manager, token, _ := newAdminSession(t)
		handler := newAdminTestHandler(
			t, event.NewService(), market.NewService(),
			AdminConfig{Accounts: manager, Settlement: settlementStub{settleErr: settlement.ErrMarketAlreadySettled}},
		)
		recorder := adminRequest(
			t, handler, http.MethodPost,
			"/api/v1/admin/markets/"+marketID+"/settle",
			[]byte(`{"winning_option":"Yes","confirm":"settle"}`),
			token,
		)
		if recorder.Code != http.StatusConflict {
			t.Errorf("settle already settled status = %d, want 409", recorder.Code)
		}
	})

	t.Run("invalid option returns 400", func(t *testing.T) {
		manager, token, _ := newAdminSession(t)
		handler := newAdminTestHandler(
			t, event.NewService(), market.NewService(),
			AdminConfig{Accounts: manager, Settlement: settlementStub{settleErr: settlement.ErrOutcomeNotOption}},
		)
		recorder := adminRequest(
			t, handler, http.MethodPost,
			"/api/v1/admin/markets/"+marketID+"/settle",
			[]byte(`{"winning_option":"Maybe","confirm":"settle"}`),
			token,
		)
		if recorder.Code != http.StatusBadRequest {
			t.Errorf("settle invalid option status = %d, want 400", recorder.Code)
		}
	})
}

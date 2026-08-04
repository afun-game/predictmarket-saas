package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/afun-game/predictmarket-saas/internal/adminauth"
	"github.com/afun-game/predictmarket-saas/internal/adminquery"
	"github.com/afun-game/predictmarket-saas/internal/auth"
	"github.com/afun-game/predictmarket-saas/internal/currency"
	"github.com/afun-game/predictmarket-saas/internal/event"
	"github.com/afun-game/predictmarket-saas/internal/market"
	"github.com/afun-game/predictmarket-saas/internal/merchant"
	"github.com/afun-game/predictmarket-saas/internal/order"
	"github.com/afun-game/predictmarket-saas/internal/platformuser"
	"github.com/afun-game/predictmarket-saas/internal/wallet"
	"golang.org/x/crypto/bcrypt"
)

// adminMerchantsTestEnv wires a full HTTP handler with the admin console
// backend: memory-backed services plus a sqlmock database for the
// adminquery reads.
type adminMerchantsTestEnv struct {
	handler http.Handler
	mock    sqlmock.Sqlmock
	logs    *adminauth.MemoryActionLog
	users   *platformuser.MemoryRepository
}

func newAdminMerchantsTestEnv(t *testing.T, accounts ...adminauth.Account) *adminMerchantsTestEnv {
	t.Helper()
	database, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New() error = %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })

	repo := adminauth.NewMemoryRepository()
	logs := adminauth.NewMemoryActionLog()
	for _, account := range accounts {
		if err := repo.Create(context.Background(), account); err != nil {
			t.Fatalf("create admin account: %v", err)
		}
	}
	manager, err := adminauth.NewManager(repo, logs, bytes.Repeat([]byte("k"), 32))
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	users := platformuser.NewMemoryRepository()
	handler := NewHandler(
		merchant.NewService(),
		event.NewService(),
		market.NewService(),
		wallet.NewService(),
		order.NewService(),
		currency.NewService(),
		"admin-secret",
		AdminConfig{
			Accounts:      manager,
			Queries:       adminquery.New(database),
			PlatformUsers: users,
		},
	)
	return &adminMerchantsTestEnv{handler: handler, mock: mock, logs: logs, users: users}
}

// adminMerchantsTestAccount builds a seeded admin account with a bcrypt
// password hash.
func adminMerchantsTestAccount(username, password, role string) adminauth.Account {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.MinCost)
	if err != nil {
		panic(err)
	}
	return adminauth.Account{
		Username:     username,
		PasswordHash: string(hash),
		Role:         role,
		Status:       adminauth.StatusActive,
	}
}

// adminMerchantsTestLogin logs in and returns the issued session cookie.
func adminMerchantsTestLogin(t *testing.T, env *adminMerchantsTestEnv, username, password string) *http.Cookie {
	t.Helper()
	response := performRequest(
		t,
		env.handler,
		http.MethodPost,
		"/api/v1/admin/login",
		[]byte(fmt.Sprintf(`{"username":%q,"password":%q}`, username, password)),
		"",
	)
	if response.Code != http.StatusOK {
		t.Fatalf("login status = %d, body = %s", response.Code, response.Body.String())
	}
	for _, cookie := range response.Result().Cookies() {
		if cookie.Name == auth.AdminSessionCookie {
			return cookie
		}
	}
	t.Fatalf("login response carried no %s cookie", auth.AdminSessionCookie)
	return nil
}

// adminMerchantsTestRequest performs one request against the admin handler.
func adminMerchantsTestRequest(
	t *testing.T,
	handler http.Handler,
	method, path string,
	body []byte,
	cookie *http.Cookie,
) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, path, bytes.NewReader(body))
	if cookie != nil {
		request.AddCookie(cookie)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func adminMerchantsTestDecode(t *testing.T, response *httptest.ResponseRecorder, destination any) {
	t.Helper()
	if err := json.Unmarshal(response.Body.Bytes(), destination); err != nil {
		t.Fatalf("decode response: %v; body = %s", err, response.Body.String())
	}
}

func adminMerchantsTestErrorCode(t *testing.T, response *httptest.ResponseRecorder) string {
	t.Helper()
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode error body: %v; body = %s", err, response.Body.String())
	}
	return body.Error.Code
}

func TestAdminMerchantsLogin(t *testing.T) {
	env := newAdminMerchantsTestEnv(t, adminMerchantsTestAccount("boss", "pw", adminauth.RoleSuperAdmin))

	response := performRequest(
		t,
		env.handler,
		http.MethodPost,
		"/api/v1/admin/login",
		[]byte(`{"username":"boss","password":"pw"}`),
		"",
	)
	if response.Code != http.StatusOK {
		t.Fatalf("login status = %d, body = %s", response.Code, response.Body.String())
	}
	var login struct {
		Data struct {
			ID       string `json:"id"`
			Username string `json:"username"`
			Role     string `json:"role"`
		} `json:"data"`
	}
	adminMerchantsTestDecode(t, response, &login)
	if login.Data.ID == "" || login.Data.Username != "boss" || login.Data.Role != adminauth.RoleSuperAdmin {
		t.Errorf("login data = %#v", login.Data)
	}
	cookieSeen := false
	for _, cookie := range response.Result().Cookies() {
		if cookie.Name == auth.AdminSessionCookie && cookie.Value != "" {
			cookieSeen = true
		}
	}
	if !cookieSeen {
		t.Error("login did not set the admin session cookie")
	}

	wrong := performRequest(
		t,
		env.handler,
		http.MethodPost,
		"/api/v1/admin/login",
		[]byte(`{"username":"boss","password":"nope"}`),
		"",
	)
	if wrong.Code != http.StatusUnauthorized || adminMerchantsTestErrorCode(t, wrong) != "invalid_credentials" {
		t.Errorf("wrong password status = %d, body = %s", wrong.Code, wrong.Body.String())
	}
}

func TestAdminMerchantsLoginLockout(t *testing.T) {
	env := newAdminMerchantsTestEnv(t, adminMerchantsTestAccount("boss", "pw", adminauth.RoleSuperAdmin))
	for attempt := 1; attempt <= 4; attempt++ {
		response := performRequest(
			t,
			env.handler,
			http.MethodPost,
			"/api/v1/admin/login",
			[]byte(`{"username":"boss","password":"nope"}`),
			"",
		)
		if response.Code != http.StatusUnauthorized {
			t.Fatalf("failed attempt %d status = %d, body = %s", attempt, response.Code, response.Body.String())
		}
	}
	locked := performRequest(
		t,
		env.handler,
		http.MethodPost,
		"/api/v1/admin/login",
		[]byte(`{"username":"boss","password":"nope"}`),
		"",
	)
	if locked.Code != http.StatusLocked || adminMerchantsTestErrorCode(t, locked) != "account_locked" {
		t.Errorf("lockout status = %d, body = %s", locked.Code, locked.Body.String())
	}
	correct := performRequest(
		t,
		env.handler,
		http.MethodPost,
		"/api/v1/admin/login",
		[]byte(`{"username":"boss","password":"pw"}`),
		"",
	)
	if correct.Code != http.StatusLocked {
		t.Errorf("login while locked status = %d, want %d; body = %s", correct.Code, http.StatusLocked, correct.Body.String())
	}
}

func TestAdminMerchantsMe(t *testing.T) {
	env := newAdminMerchantsTestEnv(t, adminMerchantsTestAccount("boss", "pw", adminauth.RoleSuperAdmin))

	anonymous := adminMerchantsTestRequest(t, env.handler, http.MethodGet, "/api/v1/admin/me", nil, nil)
	if anonymous.Code != http.StatusUnauthorized {
		t.Errorf("anonymous me status = %d, want %d", anonymous.Code, http.StatusUnauthorized)
	}

	cookie := adminMerchantsTestLogin(t, env, "boss", "pw")
	response := adminMerchantsTestRequest(t, env.handler, http.MethodGet, "/api/v1/admin/me", nil, cookie)
	if response.Code != http.StatusOK {
		t.Fatalf("me status = %d, body = %s", response.Code, response.Body.String())
	}
	var me struct {
		Data struct {
			Username string `json:"username"`
			Role     string `json:"role"`
		} `json:"data"`
	}
	adminMerchantsTestDecode(t, response, &me)
	if me.Data.Username != "boss" || me.Data.Role != adminauth.RoleSuperAdmin {
		t.Errorf("me data = %#v", me.Data)
	}
}

func TestAdminMerchantsRoutesRequireSession(t *testing.T) {
	env := newAdminMerchantsTestEnv(t, adminMerchantsTestAccount("boss", "pw", adminauth.RoleSuperAdmin))
	for _, attempt := range []struct {
		method string
		path   string
		body   string
	}{
		{http.MethodGet, "/api/v1/admin/merchants", ""},
		{http.MethodGet, "/api/v1/admin/merchants/merchant-1", ""},
		{http.MethodGet, "/api/v1/admin/users", ""},
		{http.MethodGet, "/api/v1/admin/users/merchant-1/user-1", ""},
		{http.MethodGet, "/api/v1/admin/users/merchant-1/user-1/transactions", ""},
		{http.MethodPatch, "/api/v1/admin/merchants/merchant-1", `{"name":"Renamed"}`},
		{http.MethodPatch, "/api/v1/admin/merchants/merchant-1/status", `{"status":"suspended","confirm":"suspended"}`},
		{http.MethodPatch, "/api/v1/admin/users/merchant-1/user-1/status", `{"status":"blocked","confirm":"blocked"}`},
	} {
		response := adminMerchantsTestRequest(t, env.handler, attempt.method, attempt.path, []byte(attempt.body), nil)
		if response.Code != http.StatusUnauthorized {
			t.Errorf("%s %s status = %d, want %d", attempt.method, attempt.path, response.Code, http.StatusUnauthorized)
		}
	}
}

func TestAdminMerchantsRBAC(t *testing.T) {
	env := newAdminMerchantsTestEnv(t,
		adminMerchantsTestAccount("boss", "pw", adminauth.RoleSuperAdmin),
		adminMerchantsTestAccount("op", "pw", adminauth.RoleOperator),
	)
	operator := adminMerchantsTestLogin(t, env, "op", "pw")

	denied := adminMerchantsTestRequest(
		t,
		env.handler,
		http.MethodPatch,
		"/api/v1/admin/merchants/merchant-1/status",
		[]byte(`{"status":"suspended","confirm":"suspended"}`),
		operator,
	)
	if denied.Code != http.StatusForbidden || adminMerchantsTestErrorCode(t, denied) != "forbidden" {
		t.Errorf("operator write status = %d, body = %s", denied.Code, denied.Body.String())
	}

	// Session-only reads are available to operators.
	env.mock.ExpectQuery("SELECT id, name, email, status, currency, timezone, wallet_mode, fee_rate, created_at").
		WithArgs("", 20, 0).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "name", "email", "status", "currency", "timezone", "wallet_mode", "fee_rate", "created_at",
		}))
	env.mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM merchants").
		WithArgs("").
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
	listed := adminMerchantsTestRequest(t, env.handler, http.MethodGet, "/api/v1/admin/merchants", nil, operator)
	if listed.Code != http.StatusOK {
		t.Errorf("operator list status = %d, body = %s", listed.Code, listed.Body.String())
	}
	if err := env.mock.ExpectationsWereMet(); err != nil {
		t.Errorf("sqlmock expectations: %v", err)
	}
}

func TestAdminMerchantsListAndSearch(t *testing.T) {
	env := newAdminMerchantsTestEnv(t, adminMerchantsTestAccount("boss", "pw", adminauth.RoleSuperAdmin))
	credentials := registerMerchant(t, env.handler, "Acme Corp", "admin@acme.test")
	cookie := adminMerchantsTestLogin(t, env, "boss", "pw")

	created := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
	env.mock.ExpectQuery("SELECT id, name, email, status, currency, timezone, wallet_mode, fee_rate, created_at").
		WithArgs("acme", 20, 0).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "name", "email", "status", "currency", "timezone", "wallet_mode", "fee_rate", "created_at",
		}).AddRow(credentials.Data.MerchantID, "Acme Corp", "admin@acme.test", "active", "USD", "UTC", "transfer", 0.0, created))
	env.mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM merchants").
		WithArgs("acme").
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))

	response := adminMerchantsTestRequest(
		t,
		env.handler,
		http.MethodGet,
		"/api/v1/admin/merchants?q=acme&page=1&limit=20",
		nil,
		cookie,
	)
	if response.Code != http.StatusOK {
		t.Fatalf("list merchants status = %d, body = %s", response.Code, response.Body.String())
	}
	var listed struct {
		Data struct {
			Items []adminquery.MerchantRow `json:"items"`
			Total int                      `json:"total"`
		} `json:"data"`
	}
	adminMerchantsTestDecode(t, response, &listed)
	if listed.Data.Total != 1 || len(listed.Data.Items) != 1 {
		t.Fatalf("merchant list = %#v", listed.Data)
	}
	item := listed.Data.Items[0]
	if item.ID != credentials.Data.MerchantID || item.Name != "Acme Corp" || item.Email != "admin@acme.test" ||
		item.Status != "active" || item.Currency != "USD" || item.Timezone != "UTC" {
		t.Errorf("merchant row = %#v", item)
	}
	if err := env.mock.ExpectationsWereMet(); err != nil {
		t.Errorf("sqlmock expectations: %v", err)
	}
}

func TestAdminMerchantsGet(t *testing.T) {
	env := newAdminMerchantsTestEnv(t, adminMerchantsTestAccount("boss", "pw", adminauth.RoleSuperAdmin))
	credentials := registerMerchant(t, env.handler, "Acme Corp", "admin@acme.test")
	cookie := adminMerchantsTestLogin(t, env, "boss", "pw")

	env.mock.ExpectQuery("SELECT\\s+\\(SELECT COUNT\\(\\*\\) FROM platform_users").
		WithArgs(credentials.Data.MerchantID).
		WillReturnRows(sqlmock.NewRows([]string{
			"user_count", "market_count", "order_count", "total_volume",
		}).AddRow(12, 3, 45, 1234.5))

	response := adminMerchantsTestRequest(
		t,
		env.handler,
		http.MethodGet,
		"/api/v1/admin/merchants/"+credentials.Data.MerchantID,
		nil,
		cookie,
	)
	if response.Code != http.StatusOK {
		t.Fatalf("get merchant status = %d, body = %s", response.Code, response.Body.String())
	}
	var detail struct {
		Data struct {
			ID     string `json:"id"`
			Name   string `json:"name"`
			Status string `json:"status"`
			Stats  struct {
				UserCount   int     `json:"user_count"`
				MarketCount int     `json:"market_count"`
				OrderCount  int     `json:"order_count"`
				TotalVolume float64 `json:"total_volume"`
			} `json:"stats"`
		} `json:"data"`
	}
	adminMerchantsTestDecode(t, response, &detail)
	if detail.Data.ID != credentials.Data.MerchantID || detail.Data.Name != "Acme Corp" || detail.Data.Status != "active" {
		t.Errorf("merchant detail = %#v", detail.Data)
	}
	if detail.Data.Stats.UserCount != 12 || detail.Data.Stats.MarketCount != 3 ||
		detail.Data.Stats.OrderCount != 45 || detail.Data.Stats.TotalVolume != 1234.5 {
		t.Errorf("merchant stats = %#v", detail.Data.Stats)
	}
	if err := env.mock.ExpectationsWereMet(); err != nil {
		t.Errorf("sqlmock expectations: %v", err)
	}
}

func TestAdminMerchantsGetNotFound(t *testing.T) {
	env := newAdminMerchantsTestEnv(t, adminMerchantsTestAccount("boss", "pw", adminauth.RoleSuperAdmin))
	cookie := adminMerchantsTestLogin(t, env, "boss", "pw")
	response := adminMerchantsTestRequest(
		t,
		env.handler,
		http.MethodGet,
		"/api/v1/admin/merchants/does-not-exist",
		nil,
		cookie,
	)
	if response.Code != http.StatusNotFound || adminMerchantsTestErrorCode(t, response) != "not_found" {
		t.Errorf("get missing merchant status = %d, body = %s", response.Code, response.Body.String())
	}
}

func TestAdminMerchantsUpdate(t *testing.T) {
	env := newAdminMerchantsTestEnv(t, adminMerchantsTestAccount("boss", "pw", adminauth.RoleSuperAdmin))
	credentials := registerMerchant(t, env.handler, "Acme Corp", "admin@acme.test")
	cookie := adminMerchantsTestLogin(t, env, "boss", "pw")

	response := adminMerchantsTestRequest(
		t,
		env.handler,
		http.MethodPatch,
		"/api/v1/admin/merchants/"+credentials.Data.MerchantID,
		[]byte(`{"name":"Acme International","currency":"EUR","timezone":"Europe/Paris","fee_rate":0.015}`),
		cookie,
	)
	if response.Code != http.StatusOK {
		t.Fatalf("update merchant status = %d, body = %s", response.Code, response.Body.String())
	}
	var updated struct {
		Data struct {
			ID       string  `json:"id"`
			Name     string  `json:"name"`
			Currency string  `json:"currency"`
			Timezone string  `json:"timezone"`
			FeeRate  float64 `json:"fee_rate"`
		} `json:"data"`
	}
	adminMerchantsTestDecode(t, response, &updated)
	if updated.Data.ID != credentials.Data.MerchantID || updated.Data.Name != "Acme International" ||
		updated.Data.Currency != "EUR" || updated.Data.Timezone != "Europe/Paris" || updated.Data.FeeRate != 0.015 {
		t.Errorf("updated merchant = %#v", updated.Data)
	}

	audited := false
	for _, action := range env.logs.Actions() {
		if action.Action == "update.merchant" && action.Resource == "merchant" &&
			action.ResourceID == credentials.Data.MerchantID {
			audited = true
		}
	}
	if !audited {
		t.Errorf("update.merchant audit action missing; actions = %#v", env.logs.Actions())
	}

	invalid := adminMerchantsTestRequest(
		t,
		env.handler,
		http.MethodPatch,
		"/api/v1/admin/merchants/"+credentials.Data.MerchantID,
		[]byte(`{"fee_rate":1.5}`),
		cookie,
	)
	if invalid.Code != http.StatusBadRequest || adminMerchantsTestErrorCode(t, invalid) != "validation_error" {
		t.Errorf("invalid fee rate status = %d, body = %s", invalid.Code, invalid.Body.String())
	}
}

func TestAdminMerchantsUpdateStatus(t *testing.T) {
	env := newAdminMerchantsTestEnv(t, adminMerchantsTestAccount("boss", "pw", adminauth.RoleSuperAdmin))
	credentials := registerMerchant(t, env.handler, "Acme Corp", "admin@acme.test")
	cookie := adminMerchantsTestLogin(t, env, "boss", "pw")
	path := "/api/v1/admin/merchants/" + credentials.Data.MerchantID + "/status"

	wrong := adminMerchantsTestRequest(
		t,
		env.handler,
		http.MethodPatch,
		path,
		[]byte(`{"status":"suspended","confirm":"active"}`),
		cookie,
	)
	if wrong.Code != http.StatusBadRequest || adminMerchantsTestErrorCode(t, wrong) != "validation_error" {
		t.Errorf("wrong confirm status = %d, body = %s", wrong.Code, wrong.Body.String())
	}

	invalid := adminMerchantsTestRequest(
		t,
		env.handler,
		http.MethodPatch,
		path,
		[]byte(`{"status":"deleted","confirm":"deleted"}`),
		cookie,
	)
	if invalid.Code != http.StatusBadRequest || adminMerchantsTestErrorCode(t, invalid) != "validation_error" {
		t.Errorf("invalid status = %d, body = %s", invalid.Code, invalid.Body.String())
	}

	response := adminMerchantsTestRequest(
		t,
		env.handler,
		http.MethodPatch,
		path,
		[]byte(`{"status":"suspended","confirm":"suspended"}`),
		cookie,
	)
	if response.Code != http.StatusOK {
		t.Fatalf("suspend merchant status = %d, body = %s", response.Code, response.Body.String())
	}
	var result struct {
		Data struct {
			ID     string `json:"id"`
			Status string `json:"status"`
		} `json:"data"`
	}
	adminMerchantsTestDecode(t, response, &result)
	if result.Data.ID != credentials.Data.MerchantID || result.Data.Status != "suspended" {
		t.Errorf("status result = %#v", result.Data)
	}

	// The service really switched the merchant.
	env.mock.ExpectQuery("SELECT\\s+\\(SELECT COUNT\\(\\*\\) FROM platform_users").
		WithArgs(credentials.Data.MerchantID).
		WillReturnRows(sqlmock.NewRows([]string{
			"user_count", "market_count", "order_count", "total_volume",
		}).AddRow(0, 0, 0, 0.0))
	recheck := adminMerchantsTestRequest(
		t,
		env.handler,
		http.MethodGet,
		"/api/v1/admin/merchants/"+credentials.Data.MerchantID,
		nil,
		cookie,
	)
	if recheck.Code != http.StatusOK {
		t.Fatalf("recheck merchant status = %d, body = %s", recheck.Code, recheck.Body.String())
	}
	var after struct {
		Data struct {
			Status string `json:"status"`
		} `json:"data"`
	}
	adminMerchantsTestDecode(t, recheck, &after)
	if after.Data.Status != "suspended" {
		t.Errorf("stored merchant status = %q, want %q", after.Data.Status, "suspended")
	}

	audited := false
	for _, action := range env.logs.Actions() {
		if action.Action == "status.merchant" && action.Resource == "merchant" &&
			action.ResourceID == credentials.Data.MerchantID {
			audited = true
		}
	}
	if !audited {
		t.Errorf("status.merchant audit action missing; actions = %#v", env.logs.Actions())
	}
	if err := env.mock.ExpectationsWereMet(); err != nil {
		t.Errorf("sqlmock expectations: %v", err)
	}

	missing := adminMerchantsTestRequest(
		t,
		env.handler,
		http.MethodPatch,
		"/api/v1/admin/merchants/does-not-exist/status",
		[]byte(`{"status":"suspended","confirm":"suspended"}`),
		cookie,
	)
	if missing.Code != http.StatusNotFound || adminMerchantsTestErrorCode(t, missing) != "not_found" {
		t.Errorf("missing merchant status = %d, body = %s", missing.Code, missing.Body.String())
	}
}

func TestAdminMerchantsListUsers(t *testing.T) {
	env := newAdminMerchantsTestEnv(t, adminMerchantsTestAccount("boss", "pw", adminauth.RoleSuperAdmin))
	cookie := adminMerchantsTestLogin(t, env, "boss", "pw")

	created := time.Date(2026, 7, 29, 9, 30, 0, 0, time.UTC)
	env.mock.ExpectQuery("SELECT u.merchant_id, u.external_user_id, u.locale, u.status, u.created_at").
		WithArgs("merchant-1", "active", "alice", 20, 0).
		WillReturnRows(sqlmock.NewRows([]string{
			"merchant_id", "external_user_id", "locale", "status", "created_at",
			"currency", "balance", "locked_balance", "order_count",
		}).AddRow("merchant-1", "alice", "zh-CN", "active", created, "USD", 25.5, 3.0, 7))
	env.mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM platform_users u").
		WithArgs("merchant-1", "active", "alice").
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))

	response := adminMerchantsTestRequest(
		t,
		env.handler,
		http.MethodGet,
		"/api/v1/admin/users?merchant_id=merchant-1&status=active&q=alice",
		nil,
		cookie,
	)
	if response.Code != http.StatusOK {
		t.Fatalf("list users status = %d, body = %s", response.Code, response.Body.String())
	}
	var listed struct {
		Data struct {
			Items []adminquery.UserRow `json:"items"`
			Total int                  `json:"total"`
		} `json:"data"`
	}
	adminMerchantsTestDecode(t, response, &listed)
	if listed.Data.Total != 1 || len(listed.Data.Items) != 1 {
		t.Fatalf("user list = %#v", listed.Data)
	}
	item := listed.Data.Items[0]
	if item.MerchantID != "merchant-1" || item.ExternalUserID != "alice" || item.Status != "active" ||
		item.Locale != "zh-CN" || item.Currency != "USD" || item.Balance != 25.5 || item.OrderCount != 7 {
		t.Errorf("user row = %#v", item)
	}
	if err := env.mock.ExpectationsWereMet(); err != nil {
		t.Errorf("sqlmock expectations: %v", err)
	}
}

func TestAdminMerchantsGetUser(t *testing.T) {
	env := newAdminMerchantsTestEnv(t, adminMerchantsTestAccount("boss", "pw", adminauth.RoleSuperAdmin))
	cookie := adminMerchantsTestLogin(t, env, "boss", "pw")

	created := time.Date(2026, 7, 28, 14, 0, 0, 0, time.UTC)
	lastOrder := time.Date(2026, 7, 30, 10, 0, 0, 0, time.UTC)
	env.mock.ExpectQuery("SELECT u.merchant_id, u.external_user_id, u.locale, u.status, u.created_at").
		WithArgs("merchant-1", "alice").
		WillReturnRows(sqlmock.NewRows([]string{
			"merchant_id", "external_user_id", "locale", "status", "created_at", "order_count", "last_order_at",
		}).AddRow("merchant-1", "alice", "zh-CN", "active", created, 4, lastOrder))
	env.mock.ExpectQuery("SELECT currency, balance, locked_balance FROM wallets").
		WithArgs("merchant-1", "alice").
		WillReturnRows(sqlmock.NewRows([]string{"currency", "balance", "locked_balance"}).
			AddRow("USD", 100.0, 10.0).
			AddRow("EUR", 50.0, 0.0))

	response := adminMerchantsTestRequest(
		t,
		env.handler,
		http.MethodGet,
		"/api/v1/admin/users/merchant-1/alice",
		nil,
		cookie,
	)
	if response.Code != http.StatusOK {
		t.Fatalf("get user status = %d, body = %s", response.Code, response.Body.String())
	}
	var detail struct {
		Data adminquery.UserDetail `json:"data"`
	}
	adminMerchantsTestDecode(t, response, &detail)
	if detail.Data.MerchantID != "merchant-1" || detail.Data.ExternalUserID != "alice" ||
		detail.Data.Status != "active" || detail.Data.OrderCount != 4 {
		t.Errorf("user detail = %#v", detail.Data)
	}
	if len(detail.Data.Wallets) != 2 || detail.Data.Wallets[0].Currency != "USD" ||
		detail.Data.Wallets[0].Balance != 100.0 || detail.Data.Wallets[1].Balance != 50.0 {
		t.Errorf("user wallets = %#v", detail.Data.Wallets)
	}
	if err := env.mock.ExpectationsWereMet(); err != nil {
		t.Errorf("sqlmock expectations: %v", err)
	}
}

func TestAdminMerchantsGetUserNotFound(t *testing.T) {
	env := newAdminMerchantsTestEnv(t, adminMerchantsTestAccount("boss", "pw", adminauth.RoleSuperAdmin))
	cookie := adminMerchantsTestLogin(t, env, "boss", "pw")

	env.mock.ExpectQuery("SELECT u.merchant_id, u.external_user_id, u.locale, u.status, u.created_at").
		WithArgs("merchant-1", "ghost").
		WillReturnRows(sqlmock.NewRows([]string{
			"merchant_id", "external_user_id", "locale", "status", "created_at", "order_count", "last_order_at",
		}))

	response := adminMerchantsTestRequest(
		t,
		env.handler,
		http.MethodGet,
		"/api/v1/admin/users/merchant-1/ghost",
		nil,
		cookie,
	)
	if response.Code != http.StatusNotFound || adminMerchantsTestErrorCode(t, response) != "user_not_found" {
		t.Errorf("get missing user status = %d, body = %s", response.Code, response.Body.String())
	}
	if err := env.mock.ExpectationsWereMet(); err != nil {
		t.Errorf("sqlmock expectations: %v", err)
	}
}

func TestAdminMerchantsListUserTransactions(t *testing.T) {
	env := newAdminMerchantsTestEnv(t, adminMerchantsTestAccount("boss", "pw", adminauth.RoleSuperAdmin))
	cookie := adminMerchantsTestLogin(t, env, "boss", "pw")

	created := time.Date(2026, 7, 30, 16, 0, 0, 0, time.UTC)
	env.mock.ExpectQuery("SELECT t.id, t.wallet_id, t.type, t.amount, t.currency, t.status, t.created_at").
		WithArgs("merchant-1", "alice", "", 20, 0).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "wallet_id", "type", "amount", "currency", "status", "created_at",
		}).AddRow("tx-1", "wallet-1", "deposit", 10.0, "USD", "completed", created))
	env.mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM transactions t").
		WithArgs("merchant-1", "alice", "").
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))

	response := adminMerchantsTestRequest(
		t,
		env.handler,
		http.MethodGet,
		"/api/v1/admin/users/merchant-1/alice/transactions",
		nil,
		cookie,
	)
	if response.Code != http.StatusOK {
		t.Fatalf("list transactions status = %d, body = %s", response.Code, response.Body.String())
	}
	var listed struct {
		Data struct {
			Items []adminquery.TransactionRow `json:"items"`
			Total int                         `json:"total"`
		} `json:"data"`
	}
	adminMerchantsTestDecode(t, response, &listed)
	if listed.Data.Total != 1 || len(listed.Data.Items) != 1 {
		t.Fatalf("transaction list = %#v", listed.Data)
	}
	item := listed.Data.Items[0]
	if item.ID != "tx-1" || item.Type != "deposit" || item.Amount != 10.0 ||
		item.Currency != "USD" || item.Status != "completed" {
		t.Errorf("transaction row = %#v", item)
	}
	if err := env.mock.ExpectationsWereMet(); err != nil {
		t.Errorf("sqlmock expectations: %v", err)
	}
}

func TestAdminMerchantsUpdateUserStatus(t *testing.T) {
	env := newAdminMerchantsTestEnv(t, adminMerchantsTestAccount("boss", "pw", adminauth.RoleSuperAdmin))
	cookie := adminMerchantsTestLogin(t, env, "boss", "pw")

	if err := env.users.Upsert(context.Background(), platformuser.User{
		MerchantID:     "merchant-1",
		ExternalUserID: "alice",
		Locale:         "zh-CN",
		Status:         "active",
	}); err != nil {
		t.Fatalf("seed platform user: %v", err)
	}
	path := "/api/v1/admin/users/merchant-1/alice/status"

	wrong := adminMerchantsTestRequest(
		t,
		env.handler,
		http.MethodPatch,
		path,
		[]byte(`{"status":"blocked","confirm":"active"}`),
		cookie,
	)
	if wrong.Code != http.StatusBadRequest || adminMerchantsTestErrorCode(t, wrong) != "validation_error" {
		t.Errorf("wrong confirm status = %d, body = %s", wrong.Code, wrong.Body.String())
	}

	invalid := adminMerchantsTestRequest(
		t,
		env.handler,
		http.MethodPatch,
		path,
		[]byte(`{"status":"banned","confirm":"banned"}`),
		cookie,
	)
	if invalid.Code != http.StatusBadRequest || adminMerchantsTestErrorCode(t, invalid) != "validation_error" {
		t.Errorf("invalid status = %d, body = %s", invalid.Code, invalid.Body.String())
	}

	response := adminMerchantsTestRequest(
		t,
		env.handler,
		http.MethodPatch,
		path,
		[]byte(`{"status":"blocked","confirm":"blocked"}`),
		cookie,
	)
	if response.Code != http.StatusOK {
		t.Fatalf("block user status = %d, body = %s", response.Code, response.Body.String())
	}
	var result struct {
		Data struct {
			MerchantID     string `json:"merchant_id"`
			ExternalUserID string `json:"external_user_id"`
			Status         string `json:"status"`
		} `json:"data"`
	}
	adminMerchantsTestDecode(t, response, &result)
	if result.Data.MerchantID != "merchant-1" || result.Data.ExternalUserID != "alice" || result.Data.Status != "blocked" {
		t.Errorf("status result = %#v", result.Data)
	}

	stored, err := env.users.Get(context.Background(), "merchant-1", "alice")
	if err != nil {
		t.Fatalf("get seeded user: %v", err)
	}
	if stored.Status != "blocked" {
		t.Errorf("stored status = %q, want %q", stored.Status, "blocked")
	}

	audited := false
	for _, action := range env.logs.Actions() {
		if action.Action == "status.user" && action.Resource == "user" && action.ResourceID == "merchant-1/alice" {
			audited = true
		}
	}
	if !audited {
		t.Errorf("status.user audit action missing; actions = %#v", env.logs.Actions())
	}

	missing := adminMerchantsTestRequest(
		t,
		env.handler,
		http.MethodPatch,
		"/api/v1/admin/users/merchant-1/ghost/status",
		[]byte(`{"status":"blocked","confirm":"blocked"}`),
		cookie,
	)
	if missing.Code != http.StatusNotFound || adminMerchantsTestErrorCode(t, missing) != "user_not_found" {
		t.Errorf("missing user status = %d, body = %s", missing.Code, missing.Body.String())
	}
}

func TestAdminMerchantsCreateIssuesCredentialsOnce(t *testing.T) {
	t.Parallel()
	env := newAdminMerchantsTestEnv(t, adminMerchantsTestAccount("boss", "pw", adminauth.RoleSuperAdmin))
	cookie := adminMerchantsTestLogin(t, env, "boss", "pw")

	response := adminMerchantsTestRequest(
		t,
		env.handler,
		http.MethodPost,
		"/api/v1/admin/merchants",
		[]byte(`{"name":"开户商户","email":"open@test.dev","currency":"MXN","timezone":"America/Mexico_City"}`),
		cookie,
	)
	if response.Code != http.StatusCreated {
		t.Fatalf("create merchant status = %d, body = %s", response.Code, response.Body.String())
	}
	var created struct {
		Data struct {
			ID           string `json:"id"`
			Name         string `json:"name"`
			Currency     string `json:"currency"`
			APIKey       string `json:"api_key"`
			APISecret    string `json:"api_secret"`
			APIKeyPrefix string `json:"api_key_prefix"`
		} `json:"data"`
	}
	adminMerchantsTestDecode(t, response, &created)
	if created.Data.ID == "" || created.Data.Name != "开户商户" || created.Data.Currency != "MXN" {
		t.Fatalf("created merchant = %+v", created.Data)
	}
	if created.Data.APIKey == "" || created.Data.APISecret == "" {
		t.Fatalf("cleartext credentials missing: key=%q secret=%q", created.Data.APIKey, created.Data.APISecret)
	}
	if !strings.HasPrefix(created.Data.APIKey, created.Data.APIKeyPrefix) {
		t.Errorf("api_key %q does not carry prefix %q", created.Data.APIKey, created.Data.APIKeyPrefix)
	}
	audited := false
	for _, action := range env.logs.Actions() {
		if action.Action == "create.merchant" && action.Resource == "merchant" && action.ResourceID == created.Data.ID {
			audited = true
		}
	}
	if !audited {
		t.Errorf("create.merchant audit action missing; actions = %#v", env.logs.Actions())
	}
}

func TestAdminMerchantsCreateRequiresSuperAdmin(t *testing.T) {
	t.Parallel()
	env := newAdminMerchantsTestEnv(t, adminMerchantsTestAccount("ops", "pw", adminauth.RoleOperator))
	cookie := adminMerchantsTestLogin(t, env, "ops", "pw")
	response := adminMerchantsTestRequest(
		t,
		env.handler,
		http.MethodPost,
		"/api/v1/admin/merchants",
		[]byte(`{"name":"越权商户","email":"x@test.dev","currency":"USD","timezone":"UTC"}`),
		cookie,
	)
	if response.Code != http.StatusForbidden {
		t.Fatalf("operator create status = %d, want 403", response.Code)
	}
}

func TestAdminMerchantsReissueSecret(t *testing.T) {
	t.Setenv("MERCHANT_SECRET_ENCRYPTION_KEY", "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY")
	env := newAdminMerchantsTestEnv(t, adminMerchantsTestAccount("boss", "pw", adminauth.RoleSuperAdmin))
	cookie := adminMerchantsTestLogin(t, env, "boss", "pw")

	created := adminMerchantsTestRequest(
		t, env.handler, http.MethodPost, "/api/v1/admin/merchants",
		[]byte(`{"name":"轮换商户","email":"rot@test.dev","currency":"USD","timezone":"UTC"}`),
		cookie,
	)
	var decoded struct {
		Data struct {
			ID        string `json:"id"`
			APISecret string `json:"api_secret"`
		} `json:"data"`
	}
	adminMerchantsTestDecode(t, created, &decoded)
	merchantID := decoded.Data.ID
	if merchantID == "" || decoded.Data.APISecret == "" {
		t.Fatalf("seed merchant = %+v", decoded.Data)
	}
	before := decoded.Data.APISecret

	// The confirmation word is mandatory.
	noConfirm := adminMerchantsTestRequest(
		t, env.handler, http.MethodPost, "/api/v1/admin/merchants/"+merchantID+"/api-secret/reissue",
		[]byte(`{"confirm":"nope"}`), cookie,
	)
	if noConfirm.Code != http.StatusBadRequest {
		t.Fatalf("reissue without confirm status = %d, want 400", noConfirm.Code)
	}

	reissued := adminMerchantsTestRequest(
		t, env.handler, http.MethodPost, "/api/v1/admin/merchants/"+merchantID+"/api-secret/reissue",
		[]byte(`{"confirm":"reissue"}`), cookie,
	)
	if reissued.Code != http.StatusOK {
		t.Fatalf("reissue status = %d, body = %s", reissued.Code, reissued.Body.String())
	}
	var result struct {
		Data struct {
			MerchantID string `json:"merchant_id"`
			APISecret  string `json:"api_secret"`
		} `json:"data"`
	}
	adminMerchantsTestDecode(t, reissued, &result)
	if result.Data.MerchantID != merchantID || result.Data.APISecret == "" {
		t.Fatalf("reissue result = %+v", result.Data)
	}
	if result.Data.APISecret == before {
		t.Error("reissued secret equals the previous secret")
	}
	audited := false
	for _, action := range env.logs.Actions() {
		if action.Action == "reissue.merchant_secret" && action.ResourceID == merchantID {
			audited = true
		}
	}
	if !audited {
		t.Errorf("reissue audit action missing; actions = %#v", env.logs.Actions())
	}
}

func TestAdminMerchantsCreateSeamlessIssuesCallbackSecret(t *testing.T) {
	t.Setenv("MERCHANT_SECRET_ENCRYPTION_KEY", "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY")
	env := newAdminMerchantsTestEnv(t, adminMerchantsTestAccount("boss", "pw", adminauth.RoleSuperAdmin))
	cookie := adminMerchantsTestLogin(t, env, "boss", "pw")

	response := adminMerchantsTestRequest(
		t,
		env.handler,
		http.MethodPost,
		"/api/v1/admin/merchants",
		[]byte(`{"name":"无缝商户","email":"seamless@test.dev","currency":"USD","timezone":"UTC","wallet_mode":"seamless","callback_url":"https://callback.example.com/hooks"}`),
		cookie,
	)
	if response.Code != http.StatusCreated {
		t.Fatalf("create seamless merchant status = %d, body = %s", response.Code, response.Body.String())
	}
	var created struct {
		Data struct {
			ID             string `json:"id"`
			WalletMode     string `json:"wallet_mode"`
			CallbackSecret string `json:"callback_secret"`
		} `json:"data"`
	}
	adminMerchantsTestDecode(t, response, &created)
	if created.Data.WalletMode != "seamless" {
		t.Fatalf("wallet_mode = %q, want seamless", created.Data.WalletMode)
	}
	if !strings.HasPrefix(created.Data.CallbackSecret, "cb_live_") {
		t.Fatalf("callback_secret = %q, want cb_live_...", created.Data.CallbackSecret)
	}
}

func TestAdminMerchantsUpdateWalletMode(t *testing.T) {
	t.Setenv("MERCHANT_SECRET_ENCRYPTION_KEY", "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY")
	env := newAdminMerchantsTestEnv(t, adminMerchantsTestAccount("boss", "pw", adminauth.RoleSuperAdmin))
	cookie := adminMerchantsTestLogin(t, env, "boss", "pw")

	created := adminMerchantsTestRequest(
		t, env.handler, http.MethodPost, "/api/v1/admin/merchants",
		[]byte(`{"name":"模式切换商户","email":"mode@test.dev","currency":"USD","timezone":"UTC"}`),
		cookie,
	)
	var decoded struct {
		Data struct {
			ID         string `json:"id"`
			WalletMode string `json:"wallet_mode"`
		} `json:"data"`
	}
	adminMerchantsTestDecode(t, created, &decoded)
	if decoded.Data.WalletMode != "transfer" {
		t.Fatalf("default wallet_mode = %q, want transfer", decoded.Data.WalletMode)
	}

	updated := adminMerchantsTestRequest(
		t, env.handler, http.MethodPatch, "/api/v1/admin/merchants/"+decoded.Data.ID,
		[]byte(`{"wallet_mode":"seamless","callback_url":"https://callback.example.com/hooks"}`), cookie,
	)
	if updated.Code != http.StatusOK {
		t.Fatalf("update wallet mode status = %d, body = %s", updated.Code, updated.Body.String())
	}
	var result struct {
		Data struct {
			WalletMode     string `json:"wallet_mode"`
			CallbackSecret string `json:"callback_secret"`
		} `json:"data"`
	}
	adminMerchantsTestDecode(t, updated, &result)
	if result.Data.WalletMode != "seamless" {
		t.Fatalf("updated wallet_mode = %q, want seamless", result.Data.WalletMode)
	}
	if !strings.HasPrefix(result.Data.CallbackSecret, "cb_live_") {
		t.Fatalf("updated callback_secret = %q, want cb_live_...", result.Data.CallbackSecret)
	}
}

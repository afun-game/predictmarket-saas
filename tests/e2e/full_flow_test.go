package e2e

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	_ "github.com/jackc/pgx/v5/stdlib"
)

const defaultDatabaseURL = "postgres://predictmarket:password@localhost:5432/predictmarket?sslmode=disable"

func TestFullTradingSettlementFlow(t *testing.T) {
	if os.Getenv("E2E_TEST") != "1" {
		t.Skip("set E2E_TEST=1 and start the application to run end-to-end tests")
	}
	baseURL := environmentOrDefault("E2E_BASE_URL", "http://localhost:8080")
	adminKey := environmentOrDefault("ADMIN_API_KEY", "e2e-admin-secret")
	suffix := uuid.NewString()
	email := "e2e-" + suffix + "@example.test"
	sourceID := "e2e-" + suffix
	database := openDatabase(t)
	t.Cleanup(func() { cleanupFixture(t, database, email, sourceID) })

	merchant := registerMerchant(t, baseURL, email)
	eventID := createEvent(t, baseURL, adminKey, sourceID)
	activateEvent(t, baseURL, adminKey, eventID)
	marketID := createMarket(t, baseURL, adminKey, merchant.ID, eventID)

	users := []tradingUser{
		{ID: "usd-winner", Currency: "USD", Side: "buy"},
		{ID: "usd-loser", Currency: "USD", Side: "sell"},
		{ID: "eur-winner", Currency: "EUR", Side: "buy"},
		{ID: "eur-loser", Currency: "EUR", Side: "sell"},
	}
	for _, user := range users {
		creditWallet(t, baseURL, merchant.APIKey, user, 100)
	}
	for _, currency := range []string{"USD", "EUR"} {
		placeOrder(t, baseURL, merchant.APIKey, marketID, tradingUser{
			ID: currencyUser(currency, "loser"), Currency: currency, Side: "sell",
		}, 0.4)
		placeOrder(t, baseURL, merchant.APIKey, marketID, tradingUser{
			ID: currencyUser(currency, "winner"), Currency: currency, Side: "buy",
		}, 0.6)
	}

	closeAndResolveEvent(t, baseURL, adminKey, eventID, "Yes")
	waitForSettlement(t, baseURL, merchant.APIKey, marketID)

	for _, user := range users {
		wantAvailable := 94.0
		if user.Side == "buy" {
			wantAvailable = 106
		}
		assertWallet(t, baseURL, merchant.APIKey, user, wantAvailable, 0)
	}
	assertSettlementAudit(t, database, marketID)
}

type merchantCredentials struct {
	ID     string
	APIKey string
}

type tradingUser struct {
	ID       string
	Currency string
	Side     string
}

func registerMerchant(t *testing.T, baseURL, email string) merchantCredentials {
	t.Helper()
	var response struct {
		Data struct {
			MerchantID string `json:"merchant_id"`
			APIKey     string `json:"api_key"`
		} `json:"data"`
	}
	doJSON(t, requestSpec{
		Method: http.MethodPost, URL: baseURL + "/api/v1/merchants/register",
		Body: map[string]any{
			"name": "E2E Merchant", "email": email, "currency": "USD", "timezone": "UTC",
		},
		WantStatus: http.StatusCreated, Destination: &response,
	})
	return merchantCredentials{ID: response.Data.MerchantID, APIKey: response.Data.APIKey}
}

func createEvent(t *testing.T, baseURL, adminKey, sourceID string) string {
	t.Helper()
	var response struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	endTime := time.Now().UTC().Add(time.Hour)
	doJSON(t, requestSpec{
		Method: http.MethodPost, URL: baseURL + "/api/v1/events", Token: adminKey,
		Body: map[string]any{
			"source_type": "custom", "source_id": sourceID,
			"title": "E2E settlement event", "description": "end-to-end fixture",
			"category": "sports", "end_time": endTime.Format(time.RFC3339),
			"resolution_time": endTime.Add(time.Minute).Format(time.RFC3339),
		},
		WantStatus: http.StatusCreated, Destination: &response,
	})
	return response.Data.ID
}

func activateEvent(t *testing.T, baseURL, adminKey, eventID string) {
	t.Helper()
	doJSON(t, requestSpec{
		Method: http.MethodPatch, URL: baseURL + "/api/v1/events/" + eventID + "/status",
		Token: adminKey, Body: map[string]string{"status": "active"},
		WantStatus: http.StatusNoContent,
	})
}

func createMarket(t *testing.T, baseURL, adminKey, merchantID, eventID string) string {
	t.Helper()
	var response struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	doJSON(t, requestSpec{
		Method: http.MethodPost, URL: baseURL + "/api/v1/markets", Token: adminKey,
		Body: map[string]any{
			"merchant_id": merchantID, "event_id": eventID, "type": "binary",
			"question": "Will the E2E flow settle?", "options": []string{"Yes", "No"},
			"liquidity_pool": 1000,
		},
		WantStatus: http.StatusCreated, Destination: &response,
	})
	return response.Data.ID
}

func creditWallet(
	t *testing.T,
	baseURL string,
	apiKey string,
	user tradingUser,
	amount float64,
) {
	t.Helper()
	doJSON(t, requestSpec{
		Method: http.MethodPost, URL: baseURL + "/api/v1/wallets/" + user.ID + "/credit",
		Token:          apiKey,
		IdempotencyKey: uuid.NewString(),
		Body:           map[string]any{"currency": user.Currency, "amount": amount, "type": "admin_credit"},
		WantStatus:     http.StatusOK,
	})
}

func placeOrder(
	t *testing.T,
	baseURL string,
	apiKey string,
	marketID string,
	user tradingUser,
	price float64,
) {
	t.Helper()
	doJSON(t, requestSpec{
		Method: http.MethodPost, URL: baseURL + "/api/v1/orders", Token: apiKey, IdempotencyKey: uuid.NewString(),
		Body: map[string]any{
			"market_id": marketID, "user_id": user.ID, "type": user.Side,
			"option": "Yes", "amount": 10, "currency": user.Currency,
			"price": price, "time_in_force": "gtc",
		},
		WantStatus: http.StatusCreated,
	})
}

func closeAndResolveEvent(t *testing.T, baseURL, adminKey, eventID, outcome string) {
	t.Helper()
	doJSON(t, requestSpec{
		Method: http.MethodPatch, URL: baseURL + "/api/v1/events/" + eventID + "/status",
		Token: adminKey, Body: map[string]string{"status": "closed"},
		WantStatus: http.StatusNoContent,
	})
	doJSON(t, requestSpec{
		Method: http.MethodPost, URL: baseURL + "/api/v1/events/" + eventID + "/resolve",
		Token: adminKey, Body: map[string]string{"outcome": outcome},
		WantStatus: http.StatusNoContent,
	})
}

func waitForSettlement(t *testing.T, baseURL, apiKey, marketID string) {
	t.Helper()
	deadline := time.Now().Add(12 * time.Second)
	for time.Now().Before(deadline) {
		var response struct {
			Data struct {
				Status string `json:"status"`
			} `json:"data"`
		}
		status := doJSONStatus(t, requestSpec{
			Method: http.MethodGet, URL: baseURL + "/api/v1/markets/" + marketID,
			Token: apiKey, Destination: &response,
		})
		if status == http.StatusOK && response.Data.Status == "settled" {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatal("market was not settled through NATS before timeout")
}

func assertWallet(
	t *testing.T,
	baseURL string,
	apiKey string,
	user tradingUser,
	wantAvailable float64,
	wantLocked float64,
) {
	t.Helper()
	var response struct {
		Data struct {
			Balances []struct {
				Available float64 `json:"available"`
				Locked    float64 `json:"locked"`
			} `json:"balances"`
		} `json:"data"`
	}
	doJSON(t, requestSpec{
		Method: http.MethodGet,
		URL:    baseURL + "/api/v1/wallets/" + user.ID + "?currency=" + user.Currency,
		Token:  apiKey, WantStatus: http.StatusOK, Destination: &response,
	})
	if len(response.Data.Balances) != 1 {
		t.Fatalf("wallet %s balances = %#v", user.ID, response.Data.Balances)
	}
	balance := response.Data.Balances[0]
	if balance.Available != wantAvailable || balance.Locked != wantLocked {
		t.Errorf(
			"wallet %s = (available %.2f, locked %.2f), want (%.2f, %.2f)",
			user.ID,
			balance.Available,
			balance.Locked,
			wantAvailable,
			wantLocked,
		)
	}
}

func assertSettlementAudit(t *testing.T, database *sql.DB, marketID string) {
	t.Helper()
	rows, err := database.QueryContext(context.Background(), `
SELECT currency, COUNT(*), SUM(stake)::float8, SUM(payout)::float8
FROM settlement_payouts WHERE market_id = $1
GROUP BY currency ORDER BY currency`, marketID)
	if err != nil {
		t.Fatalf("query settlement audit: %v", err)
	}
	defer func() { _ = rows.Close() }()
	seen := map[string]bool{}
	for rows.Next() {
		var currency string
		var count int
		var stake, payout float64
		if err := rows.Scan(&currency, &count, &stake, &payout); err != nil {
			t.Fatalf("scan settlement audit: %v", err)
		}
		seen[currency] = true
		if count != 2 || stake != 10 || payout != 10 {
			t.Errorf("%s audit = (%d, %.2f, %.2f), want (2, 10, 10)", currency, count, stake, payout)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate settlement audit: %v", err)
	}
	if !seen["USD"] || !seen["EUR"] {
		t.Errorf("settlement audit currencies = %#v, want USD and EUR", seen)
	}
}

type requestSpec struct {
	Method         string
	URL            string
	Token          string
	IdempotencyKey string
	Body           any
	WantStatus     int
	Destination    any
}

func doJSON(t *testing.T, spec requestSpec) {
	t.Helper()
	status := doJSONStatus(t, spec)
	if status != spec.WantStatus {
		t.Fatalf("%s %s status = %d, want %d", spec.Method, spec.URL, status, spec.WantStatus)
	}
}

func doJSONStatus(t *testing.T, spec requestSpec) int {
	t.Helper()
	var body io.Reader
	if spec.Body != nil {
		encoded, err := json.Marshal(spec.Body)
		if err != nil {
			t.Fatalf("marshal request: %v", err)
		}
		body = bytes.NewReader(encoded)
	}
	request, err := http.NewRequestWithContext(context.Background(), spec.Method, spec.URL, body)
	if err != nil {
		t.Fatalf("create request: %v", err)
	}
	if spec.Body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if spec.Token != "" {
		request.Header.Set("Authorization", "Bearer "+spec.Token)
	}
	if spec.IdempotencyKey != "" {
		request.Header.Set("Idempotency-Key", spec.IdempotencyKey)
	}
	client := &http.Client{Timeout: 3 * time.Second}
	response, err := client.Do(request)
	if err != nil {
		t.Fatalf("perform %s %s: %v", spec.Method, spec.URL, err)
	}
	defer func() { _ = response.Body.Close() }()
	encoded, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	if spec.WantStatus != 0 && response.StatusCode != spec.WantStatus {
		t.Fatalf(
			"%s %s status = %d, want %d, body = %s",
			spec.Method,
			spec.URL,
			response.StatusCode,
			spec.WantStatus,
			encoded,
		)
	}
	if spec.Destination != nil && len(encoded) > 0 && response.StatusCode < 300 {
		if err := json.Unmarshal(encoded, spec.Destination); err != nil {
			t.Fatalf("decode response %s: %v", encoded, err)
		}
	}
	return response.StatusCode
}

func currencyUser(currency, result string) string {
	return strings.ToLower(currency) + "-" + result
}

func openDatabase(t *testing.T) *sql.DB {
	t.Helper()
	database, err := sql.Open("pgx", environmentOrDefault("DATABASE_URL", defaultDatabaseURL))
	if err != nil {
		t.Fatalf("open PostgreSQL: %v", err)
	}
	if err := database.PingContext(context.Background()); err != nil {
		_ = database.Close()
		t.Fatalf("ping PostgreSQL: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	return database
}

func cleanupFixture(t *testing.T, database *sql.DB, email, sourceID string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	type cleanupStatement struct {
		query string
		args  []any
	}
	statements := []cleanupStatement{
		{
			query: `DELETE FROM trades WHERE market_id IN (
                SELECT ma.id FROM markets ma
                JOIN merchants m ON m.id = ma.merchant_id WHERE m.email = $1)`,
			args: []any{email},
		},
		{
			query: `DELETE FROM transactions WHERE wallet_id IN (
                SELECT w.id FROM wallets w
                JOIN merchants m ON m.id = w.merchant_id WHERE m.email = $1)`,
			args: []any{email},
		},
		{
			query: `DELETE FROM settlement_payouts WHERE market_id IN (
                SELECT ma.id FROM markets ma
                JOIN merchants m ON m.id = ma.merchant_id WHERE m.email = $1)`,
			args: []any{email},
		},
		{
			query: `DELETE FROM market_settlements WHERE market_id IN (
                SELECT ma.id FROM markets ma
                JOIN merchants m ON m.id = ma.merchant_id WHERE m.email = $1)`,
			args: []any{email},
		},
		{
			query: `DELETE FROM orders WHERE merchant_id IN (
                SELECT id FROM merchants WHERE email = $1)`,
			args: []any{email},
		},
		{
			query: `DELETE FROM wallets WHERE merchant_id IN (
                SELECT id FROM merchants WHERE email = $1)`,
			args: []any{email},
		},
		{
			query: `DELETE FROM markets WHERE merchant_id IN (
                SELECT id FROM merchants WHERE email = $1)`,
			args: []any{email},
		},
		{
			query: `DELETE FROM event_outbox WHERE event_id IN (
                SELECT id FROM events WHERE source_id = $1)`,
			args: []any{sourceID},
		},
		{query: `DELETE FROM events WHERE source_id = $1`, args: []any{sourceID}},
		{query: `DELETE FROM merchants WHERE email = $1`, args: []any{email}},
	}
	for _, statement := range statements {
		if _, err := database.ExecContext(ctx, statement.query, statement.args...); err != nil {
			t.Errorf("clean E2E fixture: %v", err)
		}
	}
}

func environmentOrDefault(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

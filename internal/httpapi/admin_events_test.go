package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/afun-game/predictmarket-saas/internal/adminauth"
	"github.com/afun-game/predictmarket-saas/internal/auth"
	"github.com/afun-game/predictmarket-saas/internal/currency"
	"github.com/afun-game/predictmarket-saas/internal/event"
	"github.com/afun-game/predictmarket-saas/internal/market"
	"github.com/afun-game/predictmarket-saas/internal/merchant"
	"github.com/afun-game/predictmarket-saas/internal/order"
	"github.com/afun-game/predictmarket-saas/internal/wallet"
	"golang.org/x/crypto/bcrypt"
)

const eventsValidEventBody = `{
	"title": "Will the launch succeed?",
	"description": "A custom event.",
	"category": "technology",
	"end_time": "2026-08-10T12:00:00Z",
	"resolution_time": "2026-08-10T13:00:00Z"
}`

// eventsTestServer bundles an admin-enabled handler with its in-memory
// services and the admin action trail.
type eventsTestServer struct {
	handler http.Handler
	manager *adminauth.Manager
	logs    *adminauth.MemoryActionLog
	events  event.Service
	markets market.Service
}

// newEventsTestServer builds a handler with an admin console session
// configured. The first account defaults to the super admin "boss".
func newEventsTestServer(t *testing.T, accounts ...adminauth.Account) *eventsTestServer {
	t.Helper()
	repo := adminauth.NewMemoryRepository()
	logs := adminauth.NewMemoryActionLog()
	hash, err := bcrypt.GenerateFromPassword([]byte("pw"), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("hash password: %v", err)
	}
	if len(accounts) == 0 {
		accounts = []adminauth.Account{{
			Username: "boss",
			Role:     adminauth.RoleSuperAdmin,
			Status:   adminauth.StatusActive,
		}}
	}
	for _, account := range accounts {
		if account.PasswordHash == "" {
			account.PasswordHash = string(hash)
		}
		if err := repo.Create(context.Background(), account); err != nil {
			t.Fatalf("create admin account %s: %v", account.Username, err)
		}
	}
	manager, err := adminauth.NewManager(repo, logs, bytes.Repeat([]byte("k"), 32))
	if err != nil {
		t.Fatalf("new admin manager: %v", err)
	}
	eventsService := event.NewService()
	marketsService := market.NewService()
	merchantsService := merchant.NewService()
	handler := NewHandler(
		merchantsService,
		eventsService,
		marketsService,
		wallet.NewService(),
		order.NewService(),
		currency.NewService(),
		"admin-secret",
		AdminConfig{Accounts: manager},
	)
	return &eventsTestServer{
		handler: handler,
		manager: manager,
		logs:    logs,
		events:  eventsService,
		markets: marketsService,
	}
}

// login returns a fresh session cookie value for the given username.
func (s *eventsTestServer) login(t *testing.T, username string) string {
	t.Helper()
	token, _, err := s.manager.Login(context.Background(), username, "pw")
	if err != nil {
		t.Fatalf("login %s: %v", username, err)
	}
	return token
}

// request performs an admin request with the session cookie attached.
func (s *eventsTestServer) request(
	t *testing.T,
	method string,
	path string,
	body []byte,
	token string,
) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, path, bytes.NewReader(body))
	if token != "" {
		request.AddCookie(&http.Cookie{Name: auth.AdminSessionCookie, Value: token})
	}
	response := httptest.NewRecorder()
	s.handler.ServeHTTP(response, request)
	return response
}

func (s *eventsTestServer) createEvent(t *testing.T, token string, body string) string {
	t.Helper()
	response := s.request(t, http.MethodPost, "/api/v1/admin/events", []byte(body), token)
	if response.Code != http.StatusCreated {
		t.Fatalf("create event status = %d, body = %s", response.Code, response.Body.String())
	}
	var payload struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode created event: %v", err)
	}
	if payload.Data.ID == "" {
		t.Fatalf("created event has no id: %s", response.Body.String())
	}
	return payload.Data.ID
}

func TestAdminEventsCRUDFlow(t *testing.T) {
	t.Parallel()

	server := newEventsTestServer(t)
	token := server.login(t, "boss")

	// Create without source_type/source_id: the handler must default to
	// custom with a generated source id.
	response := server.request(t, http.MethodPost, "/api/v1/admin/events", []byte(eventsValidEventBody), token)
	if response.Code != http.StatusCreated {
		t.Fatalf("create event status = %d, body = %s", response.Code, response.Body.String())
	}
	var created struct {
		Data struct {
			ID         string  `json:"id"`
			SourceType string  `json:"source_type"`
			SourceID   string  `json:"source_id"`
			Title      string  `json:"title"`
			Status     string  `json:"status"`
			Outcome    *string `json:"outcome"`
		} `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode created event: %v", err)
	}
	if created.Data.ID == "" {
		t.Fatalf("created event id is empty: %s", response.Body.String())
	}
	if created.Data.SourceType != "custom" {
		t.Errorf("created source_type = %q, want custom", created.Data.SourceType)
	}
	if !strings.HasPrefix(created.Data.SourceID, "custom-") {
		t.Errorf("created source_id = %q, want custom- prefix", created.Data.SourceID)
	}
	if created.Data.Status != "pending" {
		t.Errorf("created status = %q, want pending", created.Data.Status)
	}
	eventID := created.Data.ID

	// Get the created event.
	response = server.request(t, http.MethodGet, "/api/v1/admin/events/"+eventID, nil, token)
	if response.Code != http.StatusOK {
		t.Fatalf("get event status = %d, body = %s", response.Code, response.Body.String())
	}
	var detail struct {
		Data struct {
			ID      string `json:"id"`
			Title   string `json:"title"`
			Status  string `json:"status"`
			Markets []any  `json:"markets"`
		} `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &detail); err != nil {
		t.Fatalf("decode event detail: %v", err)
	}
	if detail.Data.ID != eventID || detail.Data.Status != "pending" {
		t.Errorf("event detail = %#v", detail.Data)
	}
	if len(detail.Data.Markets) != 0 {
		t.Errorf("new event markets = %#v, want empty", detail.Data.Markets)
	}

	// Update the editable fields.
	response = server.request(
		t,
		http.MethodPatch,
		"/api/v1/admin/events/"+eventID,
		[]byte(`{"title":"Updated title","description":"Updated description"}`),
		token,
	)
	if response.Code != http.StatusOK {
		t.Fatalf("update event status = %d, body = %s", response.Code, response.Body.String())
	}
	var updated struct {
		Data struct {
			Title       string `json:"title"`
			Description string `json:"description"`
		} `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &updated); err != nil {
		t.Fatalf("decode updated event: %v", err)
	}
	if updated.Data.Title != "Updated title" || updated.Data.Description != "Updated description" {
		t.Errorf("updated event = %#v", updated.Data)
	}

	// Lifecycle: pending -> active -> closed.
	response = server.request(
		t,
		http.MethodPatch,
		"/api/v1/admin/events/"+eventID+"/status",
		[]byte(`{"status":"active"}`),
		token,
	)
	if response.Code != http.StatusOK {
		t.Fatalf("activate status = %d, body = %s", response.Code, response.Body.String())
	}
	var statusPayload struct {
		Data struct {
			ID     string `json:"id"`
			Status string `json:"status"`
		} `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &statusPayload); err != nil {
		t.Fatalf("decode status response: %v", err)
	}
	if statusPayload.Data.ID != eventID || statusPayload.Data.Status != "active" {
		t.Errorf("status response = %#v", statusPayload.Data)
	}

	// active -> active is not a lifecycle transition.
	response = server.request(
		t,
		http.MethodPatch,
		"/api/v1/admin/events/"+eventID+"/status",
		[]byte(`{"status":"active"}`),
		token,
	)
	if response.Code != http.StatusConflict {
		t.Errorf("repeat activate status = %d, want 409; body = %s", response.Code, response.Body.String())
	}

	response = server.request(
		t,
		http.MethodPatch,
		"/api/v1/admin/events/"+eventID+"/status",
		[]byte(`{"status":"closed"}`),
		token,
	)
	if response.Code != http.StatusOK {
		t.Fatalf("close status = %d, body = %s", response.Code, response.Body.String())
	}

	// Resolve requires the confirm word.
	response = server.request(
		t,
		http.MethodPost,
		"/api/v1/admin/events/"+eventID+"/resolve",
		[]byte(`{"outcome":"Yes"}`),
		token,
	)
	if response.Code != http.StatusBadRequest {
		t.Errorf("resolve without confirm status = %d, want 400; body = %s", response.Code, response.Body.String())
	}

	// Resolve with the confirm word.
	response = server.request(
		t,
		http.MethodPost,
		"/api/v1/admin/events/"+eventID+"/resolve",
		[]byte(`{"outcome":"Yes","confirm":"resolve"}`),
		token,
	)
	if response.Code != http.StatusOK {
		t.Fatalf("resolve status = %d, body = %s", response.Code, response.Body.String())
	}
	var resolved struct {
		Data struct {
			ID      string `json:"id"`
			Status  string `json:"status"`
			Outcome string `json:"outcome"`
		} `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &resolved); err != nil {
		t.Fatalf("decode resolve response: %v", err)
	}
	if resolved.Data.ID != eventID || resolved.Data.Status != "resolved" || resolved.Data.Outcome != "Yes" {
		t.Errorf("resolve response = %#v", resolved.Data)
	}

	// A resolved event is terminal.
	response = server.request(
		t,
		http.MethodPost,
		"/api/v1/admin/events/"+eventID+"/resolve",
		[]byte(`{"outcome":"No","confirm":"resolve"}`),
		token,
	)
	if response.Code != http.StatusConflict {
		t.Errorf("re-resolve status = %d, want 409; body = %s", response.Code, response.Body.String())
	}

	response = server.request(
		t,
		http.MethodPatch,
		"/api/v1/admin/events/"+eventID,
		[]byte(`{"title":"Too late"}`),
		token,
	)
	if response.Code != http.StatusConflict {
		t.Errorf("update resolved status = %d, want 409; body = %s", response.Code, response.Body.String())
	}

	// The audit trail records the state-changing actions.
	actions := server.logs.Actions()
	recorded := map[string]bool{}
	for _, action := range actions {
		if action.ResourceID == eventID {
			recorded[action.Action] = true
		}
	}
	for _, want := range []string{"create.event", "update.event", "status.event", "resolve.event"} {
		if !recorded[want] {
			t.Errorf("audit trail missing %s for event %s", want, eventID)
		}
	}
}

func TestAdminCreateEventValidation(t *testing.T) {
	t.Parallel()

	server := newEventsTestServer(t)
	token := server.login(t, "boss")

	response := server.request(
		t,
		http.MethodPost,
		"/api/v1/admin/events",
		[]byte(`{"title":"Missing times","category":"sports"}`),
		token,
	)
	if response.Code != http.StatusBadRequest {
		t.Errorf("invalid create status = %d, want 400; body = %s", response.Code, response.Body.String())
	}

	duplicate := `{
		"source_type":"custom",
		"source_id":"events-dup-1",
		"title":"Duplicate source",
		"category":"crypto",
		"end_time":"2026-08-10T12:00:00Z",
		"resolution_time":"2026-08-10T13:00:00Z"
	}`
	response = server.request(t, http.MethodPost, "/api/v1/admin/events", []byte(duplicate), token)
	if response.Code != http.StatusCreated {
		t.Fatalf("first create status = %d, body = %s", response.Code, response.Body.String())
	}
	response = server.request(t, http.MethodPost, "/api/v1/admin/events", []byte(duplicate), token)
	if response.Code != http.StatusConflict {
		t.Errorf("duplicate create status = %d, want 409; body = %s", response.Code, response.Body.String())
	}
}

func TestAdminGetEventNotFound(t *testing.T) {
	t.Parallel()

	server := newEventsTestServer(t)
	token := server.login(t, "boss")

	response := server.request(t, http.MethodGet, "/api/v1/admin/events/does-not-exist", nil, token)
	if response.Code != http.StatusNotFound {
		t.Errorf("missing event status = %d, want 404; body = %s", response.Code, response.Body.String())
	}
}

func TestAdminGetEventIncludesMarkets(t *testing.T) {
	t.Parallel()

	server := newEventsTestServer(t)
	token := server.login(t, "boss")
	eventID := server.createEvent(t, token, eventsValidEventBody)

	credentials := registerMerchant(t, server.handler, "Events Market", "events-market@example.test")
	createdMarket, err := server.markets.Create(context.Background(), &market.CreateRequest{
		MerchantID: credentials.Data.MerchantID,
		EventID:    eventID,
		Type:       "binary",
		Question:   "Will it happen?",
		Options:    []string{"Yes", "No"},
	})
	if err != nil {
		t.Fatalf("create market: %v", err)
	}

	response := server.request(t, http.MethodGet, "/api/v1/admin/events/"+eventID, nil, token)
	if response.Code != http.StatusOK {
		t.Fatalf("get event status = %d, body = %s", response.Code, response.Body.String())
	}
	var payload struct {
		Data struct {
			Markets []struct {
				ID          string  `json:"id"`
				Question    string  `json:"question"`
				Status      string  `json:"status"`
				TotalVolume float64 `json:"total_volume"`
			} `json:"markets"`
		} `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode event detail: %v", err)
	}
	if len(payload.Data.Markets) != 1 {
		t.Fatalf("event markets = %#v, want 1", payload.Data.Markets)
	}
	item := payload.Data.Markets[0]
	if item.ID != createdMarket.ID || item.Question != "Will it happen?" ||
		item.Status != "active" || item.TotalVolume != 0 {
		t.Errorf("event market = %#v", item)
	}
}

func TestAdminEventsRequireSession(t *testing.T) {
	t.Parallel()

	server := newEventsTestServer(t)

	response := server.request(t, http.MethodGet, "/api/v1/admin/events", nil, "")
	if response.Code != http.StatusUnauthorized {
		t.Errorf("unauthenticated list status = %d, want 401", response.Code)
	}
}

func TestAdminResolveEventRequiresSuperAdmin(t *testing.T) {
	t.Parallel()

	server := newEventsTestServer(t,
		adminauth.Account{Username: "boss", Role: adminauth.RoleSuperAdmin, Status: adminauth.StatusActive},
		adminauth.Account{Username: "operator", Role: adminauth.RoleOperator, Status: adminauth.StatusActive},
	)
	eventID := server.createEvent(t, server.login(t, "boss"), eventsValidEventBody)
	operatorToken := server.login(t, "operator")

	response := server.request(
		t,
		http.MethodPost,
		"/api/v1/admin/events/"+eventID+"/resolve",
		[]byte(`{"outcome":"Yes","confirm":"resolve"}`),
		operatorToken,
	)
	if response.Code != http.StatusForbidden {
		t.Errorf("operator resolve status = %d, want 403; body = %s", response.Code, response.Body.String())
	}
}

// TestAdminListEventsWithoutQueryDatabase locks in the list route wiring:
// with no admin query database configured the handler degrades to 500 rather
// than panicking. Query-backed behavior is covered by integration tests.
func TestAdminListEventsWithoutQueryDatabase(t *testing.T) {
	t.Parallel()

	server := newEventsTestServer(t)
	token := server.login(t, "boss")

	response := server.request(t, http.MethodGet, "/api/v1/admin/events?q=sports&status=pending", nil, token)
	if response.Code != http.StatusInternalServerError {
		t.Errorf("list without query db status = %d, want 500; body = %s", response.Code, response.Body.String())
	}
}

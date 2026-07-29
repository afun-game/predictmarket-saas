package httpapi

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/afun-game/predictmarket-saas/internal/currency"
	"github.com/afun-game/predictmarket-saas/internal/event"
	"github.com/afun-game/predictmarket-saas/internal/market"
	"github.com/afun-game/predictmarket-saas/internal/merchant"
	"github.com/afun-game/predictmarket-saas/internal/order"
	"github.com/afun-game/predictmarket-saas/internal/wallet"
)

type eventResponse struct {
	Data struct {
		ID      string `json:"id"`
		Title   string `json:"title"`
		Status  string `json:"status"`
		Outcome string `json:"outcome"`
	} `json:"data"`
}

func TestEventHTTPFlow(t *testing.T) {
	t.Parallel()

	merchantService := merchant.NewService()
	handler := NewHandler(
		merchantService,
		event.NewService(),
		market.NewService(),
		wallet.NewService(),
		order.NewService(),
		currency.NewService(),
		"admin-secret",
	)
	credentials := registerMerchant(t, handler, "Event Reader", "events@example.test")

	response := performRequest(
		t,
		handler,
		http.MethodPost,
		"/api/v1/events",
		[]byte(`{
			"source_type":"custom",
			"source_id":"custom-event-1",
			"title":"Will the launch succeed?",
			"description":"A custom event.",
			"category":"technology",
			"end_time":"2026-08-10T12:00:00Z",
			"resolution_time":"2026-08-10T13:00:00Z"
		}`),
		"Bearer admin-secret",
	)
	if response.Code != http.StatusCreated {
		t.Fatalf("create event status = %d, body = %s", response.Code, response.Body.String())
	}
	var created eventResponse
	if err := json.Unmarshal(response.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode created event: %v", err)
	}
	if created.Data.ID == "" || created.Data.Status != "pending" {
		t.Fatalf("created event = %#v", created.Data)
	}

	authorization := "Bearer " + credentials.Data.APIKey
	response = performRequest(
		t,
		handler,
		http.MethodGet,
		"/api/v1/events?category=technology&status=pending&page=1&limit=10",
		nil,
		authorization,
	)
	if response.Code != http.StatusOK {
		t.Fatalf("list events status = %d, body = %s", response.Code, response.Body.String())
	}
	assertEventListResponse(t, response.Body.Bytes(), created.Data.ID)

	response = performRequest(
		t,
		handler,
		http.MethodGet,
		"/api/v1/events/"+created.Data.ID,
		nil,
		authorization,
	)
	if response.Code != http.StatusOK {
		t.Fatalf("get event status = %d, body = %s", response.Code, response.Body.String())
	}

	for _, status := range []string{"active", "closed"} {
		response = performRequest(
			t,
			handler,
			http.MethodPatch,
			"/api/v1/events/"+created.Data.ID+"/status",
			[]byte(`{"status":"`+status+`"}`),
			"Bearer admin-secret",
		)
		if response.Code != http.StatusNoContent {
			t.Fatalf("update event to %s status = %d, body = %s", status, response.Code, response.Body.String())
		}
	}

	response = performRequest(
		t,
		handler,
		http.MethodPost,
		"/api/v1/events/"+created.Data.ID+"/resolve",
		[]byte(`{"outcome":"Yes"}`),
		"Bearer admin-secret",
	)
	if response.Code != http.StatusNoContent {
		t.Fatalf("resolve event status = %d, body = %s", response.Code, response.Body.String())
	}

	response = performRequest(
		t,
		handler,
		http.MethodGet,
		"/api/v1/events/"+created.Data.ID,
		nil,
		authorization,
	)
	if response.Code != http.StatusOK {
		t.Fatalf("get resolved event status = %d, body = %s", response.Code, response.Body.String())
	}
	var resolved eventResponse
	if err := json.Unmarshal(response.Body.Bytes(), &resolved); err != nil {
		t.Fatalf("decode resolved event: %v", err)
	}
	if resolved.Data.Status != "resolved" || resolved.Data.Outcome != "Yes" {
		t.Errorf("resolved event = %#v", resolved.Data)
	}
}

func TestEventHTTPAuthorizationAndErrors(t *testing.T) {
	t.Parallel()

	handler := NewHandler(
		merchant.NewService(),
		event.NewService(),
		market.NewService(),
		wallet.NewService(),
		order.NewService(),
		currency.NewService(),
		"admin-secret",
	)
	response := performRequest(t, handler, http.MethodGet, "/api/v1/events", nil, "")
	if response.Code != http.StatusUnauthorized {
		t.Errorf("unauthenticated list status = %d, want %d", response.Code, http.StatusUnauthorized)
	}

	response = performRequest(
		t,
		handler,
		http.MethodPost,
		"/api/v1/events",
		[]byte(`{"source_type":"custom"}`),
		"Bearer wrong-key",
	)
	if response.Code != http.StatusUnauthorized {
		t.Errorf("non-admin create status = %d, want %d", response.Code, http.StatusUnauthorized)
	}

	credentials := registerMerchant(t, handler, "Reader", "reader@example.test")
	response = performRequest(
		t,
		handler,
		http.MethodGet,
		"/api/v1/events/missing",
		nil,
		"Bearer "+credentials.Data.APIKey,
	)
	if response.Code != http.StatusNotFound {
		t.Errorf("missing event status = %d, want %d", response.Code, http.StatusNotFound)
	}

	eventBody := []byte(`{
		"source_type":"custom",
		"source_id":"duplicate-source",
		"title":"Duplicate test",
		"category":"test",
		"end_time":"2026-08-10T12:00:00Z",
		"resolution_time":"2026-08-10T13:00:00Z"
	}`)
	response = performRequest(
		t,
		handler,
		http.MethodPost,
		"/api/v1/events",
		eventBody,
		"Bearer admin-secret",
	)
	if response.Code != http.StatusCreated {
		t.Fatalf("first duplicate-test create status = %d", response.Code)
	}
	var created eventResponse
	if err := json.Unmarshal(response.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode duplicate-test event: %v", err)
	}
	response = performRequest(
		t,
		handler,
		http.MethodPost,
		"/api/v1/events",
		eventBody,
		"Bearer admin-secret",
	)
	if response.Code != http.StatusConflict {
		t.Errorf("duplicate event status = %d, want %d", response.Code, http.StatusConflict)
	}
	response = performRequest(
		t,
		handler,
		http.MethodPost,
		"/api/v1/events/"+created.Data.ID+"/resolve",
		[]byte(`{"outcome":"Yes"}`),
		"Bearer admin-secret",
	)
	if response.Code != http.StatusConflict {
		t.Errorf("premature resolve status = %d, want %d", response.Code, http.StatusConflict)
	}
}

func assertEventListResponse(t *testing.T, body []byte, eventID string) {
	t.Helper()
	var response struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
		Meta struct {
			Pagination pagination `json:"pagination"`
		} `json:"meta"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		t.Fatalf("decode event list: %v", err)
	}
	if len(response.Data) != 1 || response.Data[0].ID != eventID {
		t.Errorf("event list = %#v", response.Data)
	}
	page := response.Meta.Pagination
	validRequestPage := page.Page == 1 && page.Limit == 10
	validResultPage := page.Total == 1 && page.Pages == 1
	if !validRequestPage || !validResultPage {
		t.Errorf("pagination = %#v", page)
	}
}

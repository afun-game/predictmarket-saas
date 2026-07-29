package polymarket

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestListEventsBuildsQueryAndDecodesGammaFields(t *testing.T) {
	t.Parallel()

	active := true
	closed := false
	ascending := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/events" {
			t.Errorf("path = %q, want %q", r.URL.Path, "/events")
		}
		query := r.URL.Query()
		assertQueryValue(t, query.Get("tag_slug"), "world politics")
		assertQueryValue(t, query.Get("series_id"), "league-42")
		assertQueryValue(t, query.Get("active"), "true")
		assertQueryValue(t, query.Get("closed"), "false")
		assertQueryValue(t, query.Get("limit"), "25")
		assertQueryValue(t, query.Get("offset"), "50")
		assertQueryValue(t, query.Get("order"), "volume24hr")
		assertQueryValue(t, query.Get("ascending"), "false")
		if r.Header.Get("Accept") != "application/json" {
			t.Errorf("Accept header = %q", r.Header.Get("Accept"))
		}
		_, _ = fmt.Fprint(w, `[{
			"id":"event-1",
			"slug":"election",
			"title":"Election winner",
			"endDate":"2026-11-03T00:00:00Z",
			"active":true,
			"closed":false,
			"markets":[{
				"id":"market-1",
				"question":"Will A win?",
				"outcomes":"[\"Yes\",\"No\"]",
				"outcomePrices":"[\"0.62\",\"0.38\"]",
				"volume":"1234.5",
				"active":true
			}]
		}]`)
	}))
	defer server.Close()

	client := NewClient(WithBaseURL(server.URL), WithRetry(0, 0))
	events, err := client.ListEvents(context.Background(), ListEventsOptions{
		TagSlug:   "world politics",
		SeriesID:  "league-42",
		Active:    &active,
		Closed:    &closed,
		Order:     "volume24hr",
		Ascending: &ascending,
		Limit:     25,
		Offset:    50,
	})
	if err != nil {
		t.Fatalf("ListEvents() error = %v", err)
	}
	if len(events) != 1 || events[0].ID != "event-1" {
		t.Fatalf("ListEvents() = %#v", events)
	}
	if got := events[0].EndDate.Format(time.RFC3339); got != "2026-11-03T00:00:00Z" {
		t.Errorf("event end date = %q", got)
	}
	market := events[0].Markets[0]
	if strings.Join(market.Outcomes, ",") != "Yes,No" {
		t.Errorf("outcomes = %#v", market.Outcomes)
	}
	if len(market.OutcomePrices) != 2 || market.OutcomePrices[0] != 0.62 {
		t.Errorf("outcome prices = %#v", market.OutcomePrices)
	}
	if market.Volume != 1234.5 {
		t.Errorf("volume = %v", market.Volume)
	}
}

func TestListSports(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/sports" {
			t.Errorf("path = %q, want /sports", r.URL.Path)
		}
		_, _ = w.Write([]byte(`[{"id":10,"sport":"nfl","ordering":"away","series":"10187"}]`))
	}))
	defer server.Close()
	values, err := NewClient(WithBaseURL(server.URL), WithRetry(0, 0)).ListSports(context.Background())
	if err != nil {
		t.Fatalf("ListSports() error = %v", err)
	}
	if len(values) != 1 || values[0].Sport != "nfl" || values[0].SeriesID != "10187" {
		t.Errorf("ListSports() = %#v", values)
	}
}

func TestGetEventsUsesTagSlugAndLiveStatus(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query()
		assertQueryValue(t, query.Get("tag_slug"), "sports")
		assertQueryValue(t, query.Get("active"), "true")
		assertQueryValue(t, query.Get("closed"), "false")
		assertQueryValue(t, query.Get("limit"), "100")
		_, _ = w.Write([]byte("[]"))
	}))
	defer server.Close()

	client := NewClient(WithBaseURL(server.URL), WithRetry(0, 0))
	if _, err := client.GetEvents(context.Background(), "sports", true); err != nil {
		t.Fatalf("GetEvents() error = %v", err)
	}
}

func TestGetEvent(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/events/123" {
			t.Errorf("path = %q, want %q", r.URL.Path, "/events/123")
		}
		_, _ = w.Write([]byte(`{"id":"123","markets":[]}`))
	}))
	defer server.Close()

	client := NewClient(WithBaseURL(server.URL), WithRetry(0, 0))
	event, err := client.GetEvent(context.Background(), " 123 ")
	if err != nil {
		t.Fatalf("GetEvent() error = %v", err)
	}
	if event.ID != "123" {
		t.Errorf("event ID = %q, want %q", event.ID, "123")
	}
	if event.Markets == nil {
		t.Error("event markets is nil, want an empty slice")
	}
	if _, err := client.GetEvent(context.Background(), " "); err == nil {
		t.Fatal("GetEvent() with empty ID returned no error")
	}
}

func TestClientTruncatesHTTPErrorBody(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte(strings.Repeat("x", maxErrorBodyBytes+100)))
	}))
	defer server.Close()

	client := NewClient(WithBaseURL(server.URL), WithRetry(0, 0))
	_, err := client.GetEvent(context.Background(), "123")
	var httpErr *HTTPError
	if !errors.As(err, &httpErr) {
		t.Fatalf("GetEvent() error = %v, want *HTTPError", err)
	}
	if len(httpErr.Body) != maxErrorBodyBytes+3 {
		t.Errorf("error body length = %d, want %d", len(httpErr.Body), maxErrorBodyBytes+3)
	}
}

func TestClientReturnsHTTPError(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "event not found", http.StatusNotFound)
	}))
	defer server.Close()

	client := NewClient(WithBaseURL(server.URL), WithRetry(0, 0))
	_, err := client.GetEvent(context.Background(), "404")
	var httpErr *HTTPError
	if !errors.As(err, &httpErr) {
		t.Fatalf("GetEvent() error = %v, want *HTTPError", err)
	}
	if httpErr.StatusCode != http.StatusNotFound || httpErr.Body != "event not found" {
		t.Errorf("HTTPError = %#v", httpErr)
	}
}

func TestClientRetriesRateLimit(t *testing.T) {
	t.Parallel()

	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if requests.Add(1) == 1 {
			http.Error(w, "rate limited", http.StatusTooManyRequests)
			return
		}
		_, _ = w.Write([]byte(`{"id":"123","markets":[]}`))
	}))
	defer server.Close()

	client := NewClient(WithBaseURL(server.URL), WithRetry(1, 0))
	if _, err := client.GetEvent(context.Background(), "123"); err != nil {
		t.Fatalf("GetEvent() error = %v", err)
	}
	if got := requests.Load(); got != 2 {
		t.Errorf("requests = %d, want 2", got)
	}
}

func TestListEventsValidatesPagination(t *testing.T) {
	t.Parallel()

	client := NewClient()
	if _, err := client.ListEvents(
		context.Background(),
		ListEventsOptions{Limit: maxEventLimit + 1},
	); err == nil {
		t.Fatal("ListEvents() with excessive limit returned no error")
	}
	if _, err := client.ListEvents(
		context.Background(),
		ListEventsOptions{Offset: -1},
	); err == nil {
		t.Fatal("ListEvents() with negative offset returned no error")
	}
}

func assertQueryValue(t *testing.T, got, want string) {
	t.Helper()
	if got != want {
		t.Errorf("query value = %q, want %q", got, want)
	}
}

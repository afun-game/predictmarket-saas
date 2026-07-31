package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

func TestAcceleratorResolvesDueEvents(t *testing.T) {
	var mu sync.Mutex
	resolved := map[string]string{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/v1/events" && r.Method == http.MethodGet:
			if r.Header.Get("Authorization") != "Bearer merchant-key" {
				t.Errorf("list events auth = %q", r.Header.Get("Authorization"))
			}
			payload := map[string]any{
				"data": []map[string]any{
					{"id": "due-1", "status": "active", "resolution_time": time.Now().UTC().Add(-time.Minute).Format(time.RFC3339)},
					{"id": "future-1", "status": "active", "resolution_time": time.Now().UTC().Add(time.Hour).Format(time.RFC3339)},
				},
				"meta": map[string]any{"pagination": map[string]any{"page": 1, "has_next": false}},
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(payload)
		case r.URL.Path == "/api/v1/events/due-1/resolve" && r.Method == http.MethodPost:
			if r.Header.Get("Authorization") != "Bearer admin-key" {
				t.Errorf("resolve auth = %q", r.Header.Get("Authorization"))
			}
			var body map[string]string
			_ = json.NewDecoder(r.Body).Decode(&body)
			mu.Lock()
			resolved["due-1"] = body["outcome"]
			mu.Unlock()
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	accelerator := &accelerator{
		baseURL:     server.URL,
		merchantKey: "merchant-key",
		adminKey:    "admin-key",
		outcome:     "Yes",
		limit:       500,
		client:      &http.Client{Timeout: 5 * time.Second},
	}
	accelerator.poll(context.Background())

	mu.Lock()
	defer mu.Unlock()
	if resolved["due-1"] != "Yes" {
		t.Fatalf("resolved = %#v, want due-1:Yes", resolved)
	}
}

func TestAcceleratorDryRunDoesNotResolve(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		payload := map[string]any{
			"data": []map[string]any{
				{"id": "due-1", "status": "active", "resolution_time": time.Now().UTC().Add(-time.Minute).Format(time.RFC3339)},
			},
			"meta": map[string]any{"pagination": map[string]any{"page": 1, "has_next": false}},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(payload)
	}))
	defer server.Close()

	accelerator := &accelerator{
		baseURL:     server.URL,
		merchantKey: "merchant-key",
		adminKey:    "admin-key",
		outcome:     "Yes",
		limit:       500,
		dryRun:      true,
		client:      &http.Client{Timeout: 5 * time.Second},
	}
	accelerator.poll(context.Background())
}

package currency

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestHTTPRateProvider(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
            "base":"USD",
            "time_last_updated":1785232800,
            "rates":{"EUR":0.8,"CNY":7.2,"JPY":150,"GBP":0.7}
        }`))
	}))
	defer server.Close()
	provider := newHTTPRateProvider(server.URL)
	snapshot, err := provider.Fetch(context.Background())
	if err != nil {
		t.Fatalf("Fetch() error = %v", err)
	}
	if snapshot.Base != "USD" || snapshot.Rates["JPY"] != "150.00000000" {
		t.Errorf("snapshot = %#v", snapshot)
	}
	if want := time.Unix(1785232800, 0).UTC(); !snapshot.Timestamp.Equal(want) {
		t.Errorf("timestamp = %v, want %v", snapshot.Timestamp, want)
	}
}

func TestHTTPRateProviderRejectsIncompleteResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"base":"USD","rates":{"EUR":0.8}}`))
	}))
	defer server.Close()
	if _, err := newHTTPRateProvider(server.URL).Fetch(context.Background()); err == nil {
		t.Error("Fetch(incomplete response) error = nil")
	}
}

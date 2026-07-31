package callback

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestDeliverCallbackSuccessAndSignature(t *testing.T) {
	t.Parallel()

	var seenSignature string
	var seenBody []byte
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenSignature = r.Header.Get("X-PM-Signature")
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read body: %v", err)
		}
		seenBody = body
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok", "balance": "70.00"})
	}))
	defer server.Close()

	client := NewClient(time.Second)
	client.httpClient = server.Client()
	client.allowPrivateURLs = true
	client.now = func() time.Time { return time.Unix(1_700_000_000, 0).UTC() }

	response, err := client.DeliverCallback(context.Background(), server.URL, "merchant-1", "callback-secret", CallbackRequest{
		CallbackID:    "cb-1",
		Type:          "debit",
		TransactionID: "tx-1",
		UserID:        "user-1",
		Currency:      "USD",
		Amount:        "30.00",
		Reason:        "bet",
		CreatedAt:     time.Unix(1_700_000_000, 0).UTC(),
	})
	if err != nil {
		t.Fatalf("DeliverCallback() error = %v", err)
	}
	if response.Status != StatusOK || response.Balance != "70.00" {
		t.Fatalf("response = %#v", response)
	}
	expected := signPayload("callback-secret", "1700000000", seenBody)
	if seenSignature != expected {
		t.Fatalf("signature = %s, want %s", seenSignature, expected)
	}
}

func TestDeliverCallbackRejectsPrivateURL(t *testing.T) {
	t.Parallel()

	client := NewClient(time.Second)
	_, err := client.DeliverCallback(context.Background(), "https://127.0.0.1/callback", "merchant-1", "secret", CallbackRequest{
		CallbackID:    "cb-1",
		Type:          "debit",
		TransactionID: "tx-1",
		UserID:        "user-1",
		Currency:      "USD",
		Amount:        "1.00",
		Reason:        "bet",
		CreatedAt:     time.Now().UTC(),
	})
	if err == nil {
		t.Fatal("expected private URL rejection")
	}
}

func TestDeliverCallbackInsufficientFundsIsPermanent(t *testing.T) {
	t.Parallel()

	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "insufficient_funds"})
	}))
	defer server.Close()

	client := NewClient(time.Second)
	client.httpClient = server.Client()
	client.allowPrivateURLs = true
	_, err := client.DeliverCallback(context.Background(), server.URL, "merchant-1", "secret", CallbackRequest{
		CallbackID:    "cb-1",
		Type:          "debit",
		TransactionID: "tx-1",
		UserID:        "user-1",
		Currency:      "USD",
		Amount:        "1.00",
		Reason:        "bet",
		CreatedAt:     time.Now().UTC(),
	})
	if !errors.Is(err, ErrPermanent) {
		t.Fatalf("error = %v, want ErrPermanent", err)
	}
}

func TestDeliverVerificationEchoesChallenge(t *testing.T) {
	t.Parallel()
	var received map[string]string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-PM-Signature") == "" || r.Header.Get("X-PM-Timestamp") == "" || r.Header.Get("X-PM-Merchant-Id") == "" {
			t.Errorf("verification request missing signature headers")
		}
		if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
			t.Errorf("decode verification body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok", "challenge": received["challenge"]})
	}))
	defer server.Close()

	client := newClient(time.Second, true)
	echoed, err := client.DeliverVerification(context.Background(), server.URL, "merchant-1", "secret", "abc123")
	if err != nil {
		t.Fatalf("DeliverVerification() error = %v", err)
	}
	if echoed != "abc123" {
		t.Fatalf("echoed = %q, want abc123", echoed)
	}
	if received["type"] != "callback.verify" || received["challenge"] != "abc123" {
		t.Fatalf("received = %#v", received)
	}
}

func TestDeliverVerificationRejectsWrongEcho(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok", "challenge": "wrong"})
	}))
	defer server.Close()

	client := newClient(time.Second, true)
	if _, err := client.DeliverVerification(context.Background(), server.URL, "merchant-1", "secret", "abc123"); err == nil {
		t.Fatal("DeliverVerification() accepted a wrong challenge echo")
	}
}

func TestDeliverVerificationRejectsMissingEcho(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	}))
	defer server.Close()

	client := newClient(time.Second, true)
	if _, err := client.DeliverVerification(context.Background(), server.URL, "merchant-1", "secret", "abc123"); err == nil {
		t.Fatal("DeliverVerification() accepted a response without a challenge echo")
	}
}

func TestDeliverBalanceQueryReturnsMerchantBalance(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request BalanceRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("decode balance query: %v", err)
		}
		if request.Type != "balance" || request.UserID != "user-1" || request.Currency != "USD" {
			t.Errorf("balance query = %#v", request)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok", "balance": "70.00"})
	}))
	defer server.Close()

	client := newClient(time.Second, true)
	balance, err := client.DeliverBalanceQuery(context.Background(), server.URL, "merchant-1", "secret", "user-1", "USD")
	if err != nil {
		t.Fatalf("DeliverBalanceQuery() error = %v", err)
	}
	if balance != "70.00" {
		t.Fatalf("balance = %q, want 70.00", balance)
	}
}

func TestDeliverBalanceQueryMapsUserNotFound(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "user_not_found"})
	}))
	defer server.Close()

	client := newClient(time.Second, true)
	if _, err := client.DeliverBalanceQuery(context.Background(), server.URL, "merchant-1", "secret", "ghost", "USD"); !errors.Is(err, ErrPermanent) {
		t.Fatalf("DeliverBalanceQuery() error = %v, want ErrPermanent", err)
	}
}

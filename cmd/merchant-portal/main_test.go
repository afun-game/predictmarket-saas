package main

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// fakePlatform implements the minimal platform surface the portal proxies.
func fakePlatform(t *testing.T, secret string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})
	mux.HandleFunc("POST /api/v1/merchants/register", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusCreated, map[string]any{"data": map[string]any{"merchant_id": "m-1"}})
	})
	mux.HandleFunc("POST /api/v1/events", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusCreated, map[string]any{"data": map[string]any{"id": "event-1"}})
	})
	mux.HandleFunc("PATCH /api/v1/events/event-1/status", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("POST /api/v1/markets", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusCreated, map[string]any{"data": map[string]any{"id": "market-1"}})
	})
	mux.HandleFunc("POST /api/v2/sessions", func(w http.ResponseWriter, r *http.Request) {
		if secret != "" {
			body, _ := io.ReadAll(r.Body)
			timestamp := r.Header.Get("X-PM-Timestamp")
			signature := r.Header.Get("X-PM-Signature")
			mac := hmac.New(sha256.New, []byte(secret))
			_, _ = mac.Write([]byte(timestamp + "."))
			_, _ = mac.Write(body)
			if !hmac.Equal([]byte(signature), []byte(hex.EncodeToString(mac.Sum(nil)))) {
				writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "bad signature"})
				return
			}
			if r.Header.Get("Idempotency-Key") == "" {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing idempotency key"})
				return
			}
		}
		writeJSON(w, http.StatusCreated, map[string]any{"data": map[string]any{"launch_url": "https://play.example/launch?token=lt_1"}})
	})
	return httptest.NewServer(mux)
}

func newPortal(t *testing.T, target string) *portal {
	t.Helper()
	return &portal{baseURL: target}
}

func portalCall(t *testing.T, handler http.Handler, path string, body any) (int, map[string]any) {
	t.Helper()
	encoded, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	request := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(encoded))
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	var payload map[string]any
	_ = json.Unmarshal(recorder.Body.Bytes(), &payload)
	return recorder.Code, payload
}

func TestPortalReadyz(t *testing.T) {
	target := fakePlatform(t, "")
	defer target.Close()
	handler := http.HandlerFunc(newPortal(t, target.URL).readyz)
	status, payload := portalCall(t, handler, "/api/readyz", nil)
	if status != http.StatusOK || payload["ok"] != true {
		t.Fatalf("readyz = (%d, %#v)", status, payload)
	}
}

func TestPortalRegisterAndDemoSetup(t *testing.T) {
	target := fakePlatform(t, "")
	defer target.Close()
	portal := newPortal(t, target.URL)

	status, payload := portalCall(t, http.HandlerFunc(portal.register), "/api/register", map[string]string{"name": "M", "email": "m@example.com"})
	if status != http.StatusCreated || payload["data"] == nil {
		t.Fatalf("register = (%d, %#v)", status, payload)
	}

	status, payload = portalCall(t, http.HandlerFunc(portal.demoSetup), "/api/demo-setup", map[string]any{
		"admin_key": "admin", "merchant_id": "m-1",
	})
	if status != http.StatusOK || payload["market_id"] != "market-1" || payload["event_id"] != "event-1" {
		t.Fatalf("demo-setup = (%d, %#v)", status, payload)
	}
}

func TestPortalLaunchSignsRequest(t *testing.T) {
	const secret = "portal-test-secret"
	target := fakePlatform(t, secret)
	defer target.Close()
	portal := newPortal(t, target.URL)

	status, payload := portalCall(t, http.HandlerFunc(portal.launch), "/api/launch", map[string]any{
		"api_key": "key", "api_secret": secret, "user_id": "u-1",
	})
	if status != http.StatusOK {
		t.Fatalf("launch = (%d, %#v)", status, payload)
	}
	if !strings.Contains(payload["launch_url"].(string), "lt_1") {
		t.Fatalf("launch_url = %v", payload["launch_url"])
	}
}

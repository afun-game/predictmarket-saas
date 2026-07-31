// Command merchant-portal is a local merchant-site simulator for driving the
// V3 hosted flow against any environment (typically the AWS dev deployment).
// It proxies the public merchant/admin API server-side, so the browser never
// faces CORS, and it computes the V2 HMAC signature itself.
//
// Usage:
//
//	go run ./cmd/merchant-portal -addr :8091 -api https://<dev-host>
//
// Then open http://localhost:8091, register a test merchant, create a demo
// market (admin key required), and launch the hosted page.
package main

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

func main() {
	addr := flag.String("addr", ":8091", "portal listen address")
	api := flag.String("api", "http://localhost:8080", "target platform base URL")
	flag.Parse()

	portal := &portal{baseURL: strings.TrimRight(*api, "/")}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /", portal.page)
	mux.HandleFunc("POST /api/readyz", portal.readyz)
	mux.HandleFunc("POST /api/register", portal.register)
	mux.HandleFunc("POST /api/demo-setup", portal.demoSetup)
	mux.HandleFunc("POST /api/launch", portal.launch)
	mux.HandleFunc("POST /api/events", portal.events)

	slog.Info("merchant portal listening", "addr", *addr, "api", portal.baseURL)
	if err := http.ListenAndServe(*addr, mux); err != nil {
		slog.Error("merchant portal", "error", err)
		os.Exit(1)
	}
}

type portal struct {
	baseURL string
}

type launchRequest struct {
	APIKey    string `json:"api_key"`
	APISecret string `json:"api_secret"`
	UserID    string `json:"user_id"`
	Currency  string `json:"currency"`
	Locale    string `json:"locale"`
}

type demoSetupRequest struct {
	AdminKey   string   `json:"admin_key"`
	MerchantID string   `json:"merchant_id"`
	SourceID   string   `json:"source_id"`
	Question   string   `json:"question"`
	MarketType string   `json:"market_type"`
	Options    []string `json:"options"`
}

//go:embed index.html
var pageHTML string

func (p *portal) page(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(pageHTML))
}

func (p *portal) readyz(w http.ResponseWriter, r *http.Request) {
	status, body, err := p.proxy(r.Context(), http.MethodGet, "/readyz", "", nil)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":     status == http.StatusOK,
		"status": status,
		"body":   strings.TrimSpace(string(body)),
	})
}

func (p *portal) register(w http.ResponseWriter, r *http.Request) {
	var request map[string]any
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&request); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid JSON"})
		return
	}
	status, body, err := p.proxy(r.Context(), http.MethodPost, "/api/v1/merchants/register", "", mustJSON(request))
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, status, json.RawMessage(body))
}

func (p *portal) demoSetup(w http.ResponseWriter, r *http.Request) {
	var request demoSetupRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&request); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid JSON"})
		return
	}
	if request.AdminKey == "" || request.MerchantID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "admin_key and merchant_id are required"})
		return
	}
	now := time.Now().UTC()
	endTime := now.Add(time.Hour)
	sourceID := request.SourceID
	if sourceID == "" {
		sourceID = "portal-" + strconv.FormatInt(now.UnixNano(), 10)
	}
	eventPayload := map[string]any{
		"source_type":     "custom",
		"source_id":       sourceID,
		"title":           "Portal demo event",
		"description":     "created by merchant-portal",
		"category":        "sports",
		"end_time":        endTime.Format(time.RFC3339),
		"resolution_time": endTime.Add(time.Minute).Format(time.RFC3339),
	}
	status, body, err := p.proxy(r.Context(), http.MethodPost, "/api/v1/events", request.AdminKey, mustJSON(eventPayload))
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": "create event: " + err.Error()})
		return
	}
	if status != http.StatusCreated {
		writeJSON(w, status, json.RawMessage(body))
		return
	}
	var createdEvent struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	_ = json.Unmarshal(body, &createdEvent)
	eventID := createdEvent.Data.ID

	if status, _, err = p.proxy(r.Context(), http.MethodPatch, "/api/v1/events/"+eventID+"/status", request.AdminKey, mustJSON(map[string]string{"status": "active"})); err != nil || status != http.StatusNoContent {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": "activate event"})
		return
	}
	options := request.Options
	if len(options) == 0 {
		options = []string{"Yes", "No"}
	}
	marketType := request.MarketType
	if marketType == "" {
		marketType = "binary"
	}
	question := request.Question
	if question == "" {
		question = "Will the portal demo settle?"
	}
	marketPayload := map[string]any{
		"merchant_id":    request.MerchantID,
		"event_id":       eventID,
		"type":           marketType,
		"question":       question,
		"options":        options,
		"liquidity_pool": 1000,
	}
	status, body, err = p.proxy(r.Context(), http.MethodPost, "/api/v1/markets", request.AdminKey, mustJSON(marketPayload))
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": "create market: " + err.Error()})
		return
	}
	if status != http.StatusCreated {
		writeJSON(w, status, json.RawMessage(body))
		return
	}
	var createdMarket struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	_ = json.Unmarshal(body, &createdMarket)
	writeJSON(w, http.StatusOK, map[string]any{
		"event_id":  eventID,
		"market_id": createdMarket.Data.ID,
	})
}

func (p *portal) launch(w http.ResponseWriter, r *http.Request) {
	var request launchRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&request); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid JSON"})
		return
	}
	userID := strings.TrimSpace(request.UserID)
	if userID == "" {
		userID = "portal-user"
	}
	currency := strings.TrimSpace(request.Currency)
	if currency == "" {
		currency = "USD"
	}
	locale := strings.TrimSpace(request.Locale)
	if locale == "" {
		locale = "zh-CN"
	}
	payload := mustJSON(map[string]string{
		"user_id":  userID,
		"currency": currency,
		"locale":   locale,
	})
	status, body, err := p.signedRequest(r.Context(), request.APIKey, request.APISecret, payload)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": err.Error()})
		return
	}
	if status != http.StatusCreated {
		writeJSON(w, status, json.RawMessage(body))
		return
	}
	var response struct {
		Data struct {
			LaunchURL string `json:"launch_url"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &response); err != nil || response.Data.LaunchURL == "" {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": "launch response has no launch_url", "body": string(body)})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"launch_url": response.Data.LaunchURL})
}

// events proxies the merchant-scoped event listing so the portal page can show
// whether the V3 user API is reachable.
func (p *portal) events(w http.ResponseWriter, r *http.Request) {
	var request struct {
		APIKey string `json:"api_key"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&request); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid JSON"})
		return
	}
	status, body, err := p.proxy(r.Context(), http.MethodGet, "/api/v1/events?status=active&limit=20", request.APIKey, nil)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, status, json.RawMessage(body))
}

// signedRequest performs a V2 HMAC-signed POST to /api/v2/sessions.
func (p *portal) signedRequest(ctx context.Context, apiKey, apiSecret string, body []byte) (int, []byte, error) {
	timestamp := fmt.Sprintf("%d", time.Now().Unix())
	mac := hmac.New(sha256.New, []byte(apiSecret))
	_, _ = mac.Write([]byte(timestamp + "."))
	_, _ = mac.Write(body)
	signature := hex.EncodeToString(mac.Sum(nil))

	request, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+"/api/v2/sessions", bytes.NewReader(body))
	if err != nil {
		return 0, nil, err
	}
	request.Header.Set("Authorization", "Bearer "+apiKey)
	request.Header.Set("X-PM-Timestamp", timestamp)
	request.Header.Set("X-PM-Signature", signature)
	request.Header.Set("Idempotency-Key", randomID())
	request.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return 0, nil, err
	}
	defer func() { _ = response.Body.Close() }()
	encoded, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	return response.StatusCode, encoded, err
}

func (p *portal) proxy(ctx context.Context, method, path, bearer string, body []byte) (int, []byte, error) {
	request, err := http.NewRequestWithContext(ctx, method, p.baseURL+path, bytes.NewReader(body))
	if err != nil {
		return 0, nil, err
	}
	if bearer != "" {
		request.Header.Set("Authorization", "Bearer "+bearer)
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return 0, nil, err
	}
	defer func() { _ = response.Body.Close() }()
	encoded, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	return response.StatusCode, encoded, err
}

func mustJSON(value any) []byte {
	encoded, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return encoded
}

func randomID() string {
	buffer := make([]byte, 16)
	_, _ = rand.Read(buffer)
	return fmt.Sprintf("%x-%x-%x-%x-%x", buffer[0:4], buffer[4:6], buffer[6:8], buffer[8:10], buffer[10:16])
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

// Command merchant-sim is a local counterpart for the V3 seamless-wallet
// callback and settlement-webhook contracts. It is intentionally stateful so
// acceptance tests can exercise retries, duplicate delivery, and rollback
// arriving before the original debit.
package main

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

type callbackRequest struct {
	CallbackID    string         `json:"callback_id"`
	Type          string         `json:"type"`
	TransactionID string         `json:"transaction_id"`
	UserID        string         `json:"user_id"`
	Currency      string         `json:"currency"`
	Amount        string         `json:"amount"`
	Reason        string         `json:"reason"`
	Ref           map[string]any `json:"ref,omitempty"`
}

type callbackResponse struct {
	Status  string `json:"status"`
	Balance string `json:"balance,omitempty"`
}

type transaction struct {
	typ         string
	amountCents int64
	rolledBack  bool
}

type simulator struct {
	mu             sync.Mutex
	secret         string
	merchantID     string
	balanceCts     map[string]int64
	transactions   map[string]transaction
	failStatus     string
	failHTTPStatus int
	delay          time.Duration
}

func main() {
	addr := flag.String("addr", ":8090", "HTTP listen address")
	secret := flag.String("secret", "", "callback signing secret; empty disables signature verification")
	merchantID := flag.String("merchant-id", "", "expected X-PM-Merchant-Id; empty accepts any merchant")
	initialBalance := flag.String("initial-balance", "100.00", "initial balance for every user")
	failStatus := flag.String("fail-status", "", "return this callback status for every request (test fault injection)")
	failHTTPStatus := flag.Int("fail-http-status", 0, "return this HTTP status for every callback/webhook request (test 5xx injection)")
	delay := flag.Duration("delay", 0, "delay callback responses (test timeout injection)")
	flag.Parse()

	initialCents, err := parseCents(*initialBalance)
	if err != nil || initialCents < 0 {
		fatal("invalid -initial-balance: %q", *initialBalance)
	}
	sim := &simulator{
		secret:         strings.TrimSpace(*secret),
		merchantID:     strings.TrimSpace(*merchantID),
		balanceCts:     map[string]int64{},
		transactions:   map[string]transaction{},
		failStatus:     strings.ToLower(strings.TrimSpace(*failStatus)),
		failHTTPStatus: *failHTTPStatus,
		delay:          *delay,
	}
	if initialCents != 0 {
		sim.balanceCts["*"] = initialCents
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", sim.health)
	mux.HandleFunc("GET /balance", sim.balance)
	mux.HandleFunc("POST /callback", sim.callback)
	mux.HandleFunc("POST /webhook", sim.webhook)
	mux.HandleFunc("POST /verify", sim.verifyOwnership)
	server := &http.Server{Addr: *addr, Handler: logging(mux)}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	}()

	slog.Info("merchant simulator listening", "addr", *addr, "callback", "/callback", "webhook", "/webhook")
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		fatal("merchant simulator: %v", err)
	}
}

func (s *simulator) health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// verifyOwnership proves callback URL ownership by echoing the platform challenge.
func (s *simulator) verifyOwnership(w http.ResponseWriter, r *http.Request) {
	if s.failHTTPStatus != 0 {
		w.WriteHeader(s.failHTTPStatus)
		return
	}
	var request struct {
		Type      string `json:"type"`
		Challenge string `json:"challenge"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&request); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"status": "invalid_request"})
		return
	}
	if request.Type != "callback.verify" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"status": "unsupported_type"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{
		"status":    "ok",
		"challenge": request.Challenge,
	})
}

func (s *simulator) balance(w http.ResponseWriter, r *http.Request) {
	userID := strings.TrimSpace(r.URL.Query().Get("user_id"))
	s.mu.Lock()
	value := s.balanceCts[userID]
	if value == 0 {
		value = s.balanceCts["*"]
	}
	s.mu.Unlock()
	writeJSON(w, http.StatusOK, callbackResponse{Status: "ok", Balance: formatCents(value)})
}

func (s *simulator) callback(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "read request body"})
		return
	}
	if err := s.verify(r, body); err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": err.Error()})
		return
	}
	if s.delay > 0 {
		time.Sleep(s.delay)
	}
	if s.failHTTPStatus > 0 {
		writeJSON(w, s.failHTTPStatus, map[string]string{"error": "injected HTTP failure"})
		return
	}
	request := callbackRequest{}
	if err := json.Unmarshal(body, &request); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid callback JSON"})
		return
	}
	if s.failStatus != "" {
		writeJSON(w, http.StatusOK, callbackResponse{Status: s.failStatus})
		return
	}
	if request.Type == "balance" {
		s.mu.Lock()
		balance := s.balanceCts[request.UserID]
		if balance == 0 {
			balance = s.balanceCts["*"]
		}
		s.mu.Unlock()
		writeJSON(w, http.StatusOK, callbackResponse{Status: "ok", Balance: formatCents(balance)})
		return
	}
	amount, err := parseCents(request.Amount)
	if err != nil || amount <= 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "amount must be positive cents"})
		return
	}
	status, balance := s.apply(request, amount)
	writeJSON(w, http.StatusOK, callbackResponse{Status: status, Balance: formatCents(balance)})
}

func (s *simulator) webhook(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "read request body"})
		return
	}
	if err := s.verify(r, body); err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": err.Error()})
		return
	}
	if s.failHTTPStatus > 0 {
		writeJSON(w, s.failHTTPStatus, map[string]string{"error": "injected HTTP failure"})
		return
	}
	slog.Info("merchant simulator received webhook", "bytes", len(body), "type", r.Header.Get("X-PM-Event-Type"))
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *simulator) apply(request callbackRequest, amount int64) (string, int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	userID := request.UserID
	if _, exists := s.balanceCts[userID]; !exists {
		s.balanceCts[userID] = s.balanceCts["*"]
	}
	balance := s.balanceCts[userID]
	if previous, exists := s.transactions[request.TransactionID]; exists {
		if request.Type != "rollback" || previous.rolledBack {
			return "duplicate", balance
		}
	}
	switch request.Type {
	case "debit":
		if balance < amount {
			return "insufficient_funds", balance
		}
		balance -= amount
		s.transactions[request.TransactionID] = transaction{typ: request.Type, amountCents: amount}
	case "credit":
		balance += amount
		s.transactions[request.TransactionID] = transaction{typ: request.Type, amountCents: amount}
	case "rollback":
		originalID := request.TransactionID
		if request.Ref != nil {
			if value, ok := request.Ref["original_transaction_id"].(string); ok && value != "" {
				originalID = value
			}
		}
		if original, exists := s.transactions[originalID]; exists && original.typ == "debit" && !original.rolledBack {
			balance += original.amountCents
			original.rolledBack = true
			s.transactions[originalID] = original
		} else if !exists {
			// Rollback-before-bet is recorded so a delayed debit is a duplicate.
			balance += amount
			s.transactions[originalID] = transaction{typ: "debit", amountCents: amount, rolledBack: true}
		} else {
			return "duplicate", balance
		}
	default:
		return "user_blocked", balance
	}
	s.balanceCts[userID] = balance
	return "ok", balance
}

func (s *simulator) verify(r *http.Request, body []byte) error {
	if s.merchantID != "" && r.Header.Get("X-PM-Merchant-Id") != s.merchantID {
		return fmt.Errorf("merchant ID mismatch")
	}
	if s.secret == "" {
		return nil
	}
	timestamp := strings.TrimSpace(r.Header.Get("X-PM-Timestamp"))
	seconds, err := strconv.ParseInt(timestamp, 10, 64)
	if err != nil || time.Since(time.Unix(seconds, 0)) > 5*time.Minute || time.Since(time.Unix(seconds, 0)) < -5*time.Minute {
		return fmt.Errorf("invalid callback timestamp")
	}
	mac := hmac.New(sha256.New, []byte(s.secret))
	_, _ = mac.Write([]byte(timestamp + "."))
	_, _ = mac.Write(body)
	expected := hex.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(strings.ToLower(r.Header.Get("X-PM-Signature"))), []byte(expected)) {
		return fmt.Errorf("invalid callback signature")
	}
	return nil
}

func parseCents(value string) (int64, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, fmt.Errorf("empty amount")
	}
	parts := strings.Split(value, ".")
	if len(parts) > 2 || parts[0] == "" {
		return 0, fmt.Errorf("invalid amount")
	}
	if len(parts) == 1 {
		parts = append(parts, "")
	}
	if len(parts[1]) > 2 {
		return 0, fmt.Errorf("amount has more than two decimals")
	}
	for len(parts[1]) < 2 {
		parts[1] += "0"
	}
	whole, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || whole < 0 || whole > (1<<62)/100 {
		return 0, fmt.Errorf("invalid amount")
	}
	minor, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		return 0, err
	}
	return whole*100 + minor, nil
}

func formatCents(value int64) string { return fmt.Sprintf("%d.%02d", value/100, value%100) }

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func logging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		slog.Info("merchant simulator request", "method", r.Method, "path", r.URL.Path)
		next.ServeHTTP(w, r)
	})
}

func fatal(format string, args ...any) {
	slog.Error(fmt.Sprintf(format, args...))
	os.Exit(1)
}

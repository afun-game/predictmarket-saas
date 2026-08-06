// Package merchantsim implements the stateful merchant counterpart for the
// V3 seamless-wallet callback and settlement-webhook contracts. The same
// simulator powers cmd/merchant-sim and the platform-side chaos integration
// tests, so acceptance fault injection exercises exactly the production
// counterpart behavior.
package merchantsim

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// CallbackRequest is the platform callback body.
type CallbackRequest struct {
	CallbackID    string         `json:"callback_id"`
	Type          string         `json:"type"`
	TransactionID string         `json:"transaction_id"`
	UserID        string         `json:"user_id"`
	Currency      string         `json:"currency"`
	Amount        string         `json:"amount"`
	Reason        string         `json:"reason"`
	Ref           map[string]any `json:"ref,omitempty"`
}

// CallbackResponse is the merchant HTTP 200 body.
type CallbackResponse struct {
	Status  string `json:"status"`
	Balance string `json:"balance,omitempty"`
}

// Options configure a Simulator.
type Options struct {
	Secret         string        // callback signing secret; empty disables signature verification
	MerchantID     string        // expected X-PM-Merchant-Id; empty accepts any merchant
	InitialBalance string        // initial balance for every user
	FailStatus     string        // return this callback status for every request
	FailHTTPStatus int           // return this HTTP status for every request
	FailCount      int           // fail only the first N requests, then behave normally
	Delay          time.Duration // delay callback responses (timeout injection)
	DelayCount     int           // delay only the first N requests, then respond immediately
}

type transaction struct {
	typ         string
	amountCents int64
	rolledBack  bool
}

// Simulator is a stateful merchant callback/webhook counterpart.
type Simulator struct {
	mu             sync.Mutex
	secret         string
	merchantID     string
	balanceCents   map[string]int64
	transactions   map[string]transaction
	failStatus     string
	failHTTPStatus int
	failCount      int
	failuresSoFar  int
	delay          time.Duration
	delayCount     int
	delaysSoFar    int
	requestCount   int
	webhookCount   int
	verifyCount    int
}

// New creates a simulator with the given options.
func New(options Options) (*Simulator, error) {
	if strings.TrimSpace(options.InitialBalance) == "" {
		options.InitialBalance = "100.00"
	}
	initialCents, err := ParseCents(options.InitialBalance)
	if err != nil || initialCents < 0 {
		return nil, fmt.Errorf("invalid initial balance %q", options.InitialBalance)
	}
	simulator := &Simulator{
		secret:         strings.TrimSpace(options.Secret),
		merchantID:     strings.TrimSpace(options.MerchantID),
		balanceCents:   map[string]int64{},
		transactions:   map[string]transaction{},
		failStatus:     strings.ToLower(strings.TrimSpace(options.FailStatus)),
		failHTTPStatus: options.FailHTTPStatus,
		failCount:      options.FailCount,
		delay:          options.Delay,
		delayCount:     options.DelayCount,
	}
	if initialCents != 0 {
		simulator.balanceCents["*"] = initialCents
	}
	return simulator, nil
}

// Handler returns the HTTP handler exposing the counterpart endpoints.
func (s *Simulator) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.health)
	mux.HandleFunc("GET /balance", s.balance)
	mux.HandleFunc("POST /callback", s.callback)
	mux.HandleFunc("POST /webhook", s.webhook)
	mux.HandleFunc("POST /verify", s.verifyOwnership)
	return logging(mux)
}

// Snapshot is a thread-safe view of the simulator ledger state.
type Snapshot struct {
	Balance      int64
	Requests     int
	Webhooks     int
	VerifyCalls  int
	Transactions map[string]TransactionState
}

// TransactionState is the per-ID ledger state.
type TransactionState struct {
	Type        string
	AmountCents int64
	RolledBack  bool
}

// Snapshot returns the current ledger state.
func (s *Simulator) Snapshot() Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	transactions := make(map[string]TransactionState, len(s.transactions))
	for id, value := range s.transactions {
		transactions[id] = TransactionState{Type: value.typ, AmountCents: value.amountCents, RolledBack: value.rolledBack}
	}
	return Snapshot{
		Balance:      s.balanceCents["*"],
		Requests:     s.requestCount,
		Webhooks:     s.webhookCount,
		VerifyCalls:  s.verifyCount,
		Transactions: transactions,
	}
}

// BalanceFor returns the current balance for a user (or the wildcard balance).
func (s *Simulator) BalanceFor(userID string) int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	if value, exists := s.balanceCents[userID]; exists {
		return value
	}
	return s.balanceCents["*"]
}

func (s *Simulator) health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// verifyOwnership proves callback URL ownership by echoing the platform challenge.
func (s *Simulator) verifyOwnership(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	s.verifyCount++
	s.mu.Unlock()
	if s.injectFailure(w) {
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

func (s *Simulator) balance(w http.ResponseWriter, r *http.Request) {
	userID := strings.TrimSpace(r.URL.Query().Get("user_id"))
	writeJSON(w, http.StatusOK, CallbackResponse{Status: "ok", Balance: FormatCents(s.BalanceFor(userID))})
}

func (s *Simulator) callback(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "read request body"})
		return
	}
	if err := s.Verify(r, body); err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": err.Error()})
		return
	}
	s.mu.Lock()
	s.requestCount++
	delayNow := s.delay > 0 && (s.delayCount == 0 || s.delaysSoFar < s.delayCount)
	if delayNow {
		s.delaysSoFar++
	}
	s.mu.Unlock()
	if delayNow {
		time.Sleep(s.delay)
	}
	if s.injectFailure(w) {
		return
	}
	request := CallbackRequest{}
	if err := json.Unmarshal(body, &request); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid callback JSON"})
		return
	}
	if s.failStatus != "" {
		writeJSON(w, http.StatusOK, CallbackResponse{Status: s.failStatus})
		return
	}
	if request.Type == "balance" {
		writeJSON(w, http.StatusOK, CallbackResponse{Status: "ok", Balance: FormatCents(s.BalanceFor(request.UserID))})
		return
	}
	amount, err := ParseCents(request.Amount)
	if err != nil || amount <= 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "amount must be positive cents"})
		return
	}
	status, balance := s.Apply(request, amount)
	if status == "invalid_request" {
		writeJSON(w, http.StatusBadRequest, CallbackResponse{Status: status})
		return
	}
	writeJSON(w, http.StatusOK, CallbackResponse{Status: status, Balance: FormatCents(balance)})
}

func (s *Simulator) webhook(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "read request body"})
		return
	}
	if err := s.Verify(r, body); err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": err.Error()})
		return
	}
	s.mu.Lock()
	s.webhookCount++
	s.mu.Unlock()
	if s.injectFailure(w) {
		return
	}
	slog.Info("merchant simulator received webhook", "bytes", len(body), "type", r.Header.Get("X-PM-Event-Type"))
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// ClearFaultInjection removes any injected failures so subsequent deliveries
// behave normally (used by chaos tests after a dead-letter replay).
func (s *Simulator) ClearFaultInjection() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failStatus = ""
	s.failHTTPStatus = 0
	s.failCount = 0
	s.failuresSoFar = 0
	s.delay = 0
	s.delayCount = 0
	s.delaysSoFar = 0
}

// injectFailure applies fail-count or fail-http-status fault injection. It
// returns true when the response has already been written.
func (s *Simulator) injectFailure(w http.ResponseWriter) bool {
	s.mu.Lock()
	failNow := s.failHTTPStatus > 0 && (s.failCount == 0 || s.failuresSoFar < s.failCount)
	if failNow {
		s.failuresSoFar++
	}
	s.mu.Unlock()
	if failNow {
		writeJSON(w, s.failHTTPStatus, map[string]string{"error": "injected HTTP failure"})
		return true
	}
	return false
}

// Apply applies one callback to the ledger. It is exported so tests and the
// CLI share exactly the same merchant-side semantics.
func (s *Simulator) Apply(request CallbackRequest, amount int64) (string, int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	userID := request.UserID
	if _, exists := s.balanceCents[userID]; !exists {
		s.balanceCents[userID] = s.balanceCents["*"]
	}
	balance := s.balanceCents[userID]
	if previous, exists := s.transactions[request.TransactionID]; exists {
		if request.Type != "rollback" || previous.rolledBack {
			return "duplicate", balance
		}
	}
	// Real merchant wallets require a well-formed order_id (UUID) in the
	// callback ref; missing or empty references are rejected with
	// invalid_request. The simulator mirrors that contract so platform tests
	// catch payload regressions.
	if request.Ref == nil {
		return "invalid_request", balance
	}
	orderID, _ := request.Ref["order_id"].(string)
	if strings.TrimSpace(orderID) == "" {
		return "invalid_request", balance
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
	s.balanceCents[userID] = balance
	return "ok", balance
}

// Verify validates the platform callback signature.
func (s *Simulator) Verify(r *http.Request, body []byte) error {
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

// ParseCents parses a two-decimal money string into cents.
func ParseCents(value string) (int64, error) {
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

// FormatCents formats cents as a two-decimal string.
func FormatCents(value int64) string { return fmt.Sprintf("%d.%02d", value/100, value%100) }

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

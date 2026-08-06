package merchantsim

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestRollbackBeforeBet(t *testing.T) {
	simulator, err := New(Options{InitialBalance: "100.00"})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	rollbackStatus, rollbackBalance := simulator.Apply(CallbackRequest{
		Type: "rollback", TransactionID: "tx-1", UserID: "user-1",
		Ref: map[string]any{"order_id": "order-1", "original_transaction_id": "tx-1"},
	}, 3000)
	if rollbackStatus != "ok" || rollbackBalance != 13000 {
		t.Fatalf("rollback-before-bet = (%s, %d), want (ok, 13000)", rollbackStatus, rollbackBalance)
	}
	debitStatus, debitBalance := simulator.Apply(CallbackRequest{
		Type: "debit", TransactionID: "tx-1", UserID: "user-1",
		Ref: map[string]any{"order_id": "order-1"},
	}, 3000)
	if debitStatus != "duplicate" || debitBalance != 13000 {
		t.Fatalf("delayed debit = (%s, %d), want (duplicate, 13000)", debitStatus, debitBalance)
	}
}

func TestDuplicateDebitIsIdempotent(t *testing.T) {
	simulator, err := New(Options{InitialBalance: "100.00"})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	status, balance := simulator.Apply(CallbackRequest{
		Type: "debit", TransactionID: "tx-1", UserID: "user-1",
		Ref: map[string]any{"order_id": "order-1"},
	}, 3000)
	if status != "ok" || balance != 7000 {
		t.Fatalf("first debit = (%s, %d), want (ok, 7000)", status, balance)
	}
	status, balance = simulator.Apply(CallbackRequest{
		Type: "debit", TransactionID: "tx-1", UserID: "user-1",
		Ref: map[string]any{"order_id": "order-1"},
	}, 3000)
	if status != "duplicate" || balance != 7000 {
		t.Fatalf("duplicate debit = (%s, %d), want (duplicate, 7000)", status, balance)
	}
}

func TestInjectedHTTPFailure(t *testing.T) {
	simulator, err := New(Options{InitialBalance: "100.00", FailHTTPStatus: 503})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	request := httptest.NewRequest(http.MethodPost, "/callback", strings.NewReader(`{"type":"debit","transaction_id":"tx-1","user_id":"user-1","amount":"30.00"}`))
	request.Header.Set("X-PM-Timestamp", "1785398400")
	request.Header.Set("X-PM-Signature", "not-checked")
	recorder := httptest.NewRecorder()
	simulator.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("injected callback status = %d", recorder.Code)
	}
}

func TestTransientHTTPFailureRecovers(t *testing.T) {
	simulator, err := New(Options{InitialBalance: "100.00", FailHTTPStatus: 503, FailCount: 1})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	handler := simulator.Handler()
	first := httptest.NewRecorder()
	handler.ServeHTTP(first, httptest.NewRequest(http.MethodPost, "/webhook", strings.NewReader(`{}`)))
	if first.Code != http.StatusServiceUnavailable {
		t.Fatalf("first webhook status = %d, want 503", first.Code)
	}
	second := httptest.NewRecorder()
	handler.ServeHTTP(second, httptest.NewRequest(http.MethodPost, "/webhook", strings.NewReader(`{}`)))
	if second.Code != http.StatusOK {
		t.Fatalf("second webhook status = %d, want 200", second.Code)
	}
}

func TestVerifyOwnershipEchoesChallenge(t *testing.T) {
	simulator, err := New(Options{})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	request := httptest.NewRequest(http.MethodPost, "/verify", strings.NewReader(`{"type":"callback.verify","challenge":"echo-me"}`))
	recorder := httptest.NewRecorder()
	simulator.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), "echo-me") {
		t.Fatalf("verifyOwnership = %d %s", recorder.Code, recorder.Body.String())
	}
}

func TestBalanceQuery(t *testing.T) {
	simulator, err := New(Options{InitialBalance: "70.00"})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	request := httptest.NewRequest(http.MethodPost, "/callback", strings.NewReader(`{"callback_id":"balance-1","type":"balance","user_id":"user-1","currency":"USD"}`))
	request.Header.Set("X-PM-Timestamp", "1785398400")
	request.Header.Set("X-PM-Signature", "not-checked")
	recorder := httptest.NewRecorder()
	simulator.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), "70.00") {
		t.Fatalf("balance callback = %d %s", recorder.Code, recorder.Body.String())
	}
}

func TestSignatureVerification(t *testing.T) {
	simulator, err := New(Options{InitialBalance: "100.00", Secret: "test-secret"})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	request := httptest.NewRequest(http.MethodPost, "/callback", strings.NewReader(`{"type":"debit","transaction_id":"tx-1","user_id":"user-1","amount":"1.00"}`))
	request.Header.Set("X-PM-Timestamp", "1785398400")
	request.Header.Set("X-PM-Signature", "deadbeef")
	recorder := httptest.NewRecorder()
	simulator.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("bad signature status = %d, want 401", recorder.Code)
	}
}

func TestSnapshotReportsLedger(t *testing.T) {
	simulator, err := New(Options{InitialBalance: "100.00"})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	_, _ = simulator.Apply(CallbackRequest{
		Type: "debit", TransactionID: "tx-1", UserID: "user-1",
		Ref: map[string]any{"order_id": "order-1"},
	}, 500)
	snapshot := simulator.Snapshot()
	if simulator.BalanceFor("user-1") != 9500 || snapshot.Requests != 0 {
		t.Fatalf("snapshot = %#v", snapshot)
	}
	if state, exists := snapshot.Transactions["tx-1"]; !exists || state.Type != "debit" || state.AmountCents != 500 {
		t.Fatalf("snapshot transactions = %#v", snapshot.Transactions)
	}
}

func TestApplyRejectsMissingOrderID(t *testing.T) {
	simulator, err := New(Options{InitialBalance: "100.00"})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	status, balance := simulator.Apply(CallbackRequest{
		Type: "debit", TransactionID: "tx-1", UserID: "user-1",
		Ref: map[string]any{"market_id": "market-1"},
	}, 3000)
	if status != "invalid_request" || balance != 10000 {
		t.Fatalf("debit without order_id = (%s, %d), want (invalid_request, 10000)", status, balance)
	}
}

func TestCallbackHTTPRejectsMissingOrderID(t *testing.T) {
	simulator, err := New(Options{InitialBalance: "100.00"})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	request := httptest.NewRequest(http.MethodPost, "/callback", strings.NewReader(
		`{"type":"debit","transaction_id":"tx-1","user_id":"user-1","amount":"30.00","ref":{"market_id":"market-1"}}`))
	request.Header.Set("X-PM-Timestamp", "1785398400")
	request.Header.Set("X-PM-Signature", "not-checked")
	recorder := httptest.NewRecorder()
	simulator.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("callback status = %d, want 400", recorder.Code)
	}
}

func TestFormatAndParseCents(t *testing.T) {
	for _, value := range []int64{0, 1, 99, 100, 123456789} {
		parsed, err := ParseCents(FormatCents(value))
		if err != nil || parsed != value {
			t.Fatalf("round trip %d = (%d, %v)", value, parsed, err)
		}
	}
	if _, err := ParseCents("1.234"); err == nil {
		t.Fatal("ParseCents accepted three decimals")
	}
}

var _ = json.Marshal
var _ = time.Second

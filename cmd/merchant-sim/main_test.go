package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSimulatorRollbackBeforeBet(t *testing.T) {
	sim := &simulator{
		balanceCts:   map[string]int64{"user-1": 10000},
		transactions: map[string]transaction{},
	}
	rollbackStatus, rollbackBalance := sim.apply(callbackRequest{
		Type:          "rollback",
		TransactionID: "tx-1",
		UserID:        "user-1",
	}, 3000)
	if rollbackStatus != "ok" || rollbackBalance != 13000 {
		t.Fatalf("rollback-before-bet = (%s, %d), want (ok, 13000)", rollbackStatus, rollbackBalance)
	}
	debitStatus, debitBalance := sim.apply(callbackRequest{
		Type:          "debit",
		TransactionID: "tx-1",
		UserID:        "user-1",
	}, 3000)
	if debitStatus != "duplicate" || debitBalance != 13000 {
		t.Fatalf("delayed debit = (%s, %d), want (duplicate, 13000)", debitStatus, debitBalance)
	}
}

func TestSimulatorDuplicateDebitIsIdempotent(t *testing.T) {
	sim := &simulator{
		balanceCts:   map[string]int64{"user-1": 10000},
		transactions: map[string]transaction{},
	}
	firstStatus, firstBalance := sim.apply(callbackRequest{
		Type:          "debit",
		TransactionID: "tx-1",
		UserID:        "user-1",
	}, 3000)
	secondStatus, secondBalance := sim.apply(callbackRequest{
		Type:          "debit",
		TransactionID: "tx-1",
		UserID:        "user-1",
	}, 3000)
	if firstStatus != "ok" || firstBalance != 7000 || secondStatus != "duplicate" || secondBalance != 7000 {
		t.Fatalf("duplicate debit = (%s, %d), (%s, %d)", firstStatus, firstBalance, secondStatus, secondBalance)
	}
}

func TestSimulatorInjectedHTTPFailure(t *testing.T) {
	sim := &simulator{
		balanceCts:     map[string]int64{"user-1": 10000},
		transactions:   map[string]transaction{},
		failHTTPStatus: http.StatusBadGateway,
	}
	req := httptest.NewRequest(http.MethodPost, "/callback", strings.NewReader(`{"type":"debit","transaction_id":"tx-1","user_id":"user-1","amount":"30.00"}`))
	rr := httptest.NewRecorder()

	sim.callback(rr, req)

	if rr.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusBadGateway)
	}
	if got := sim.balanceCts["user-1"]; got != 10000 {
		t.Fatalf("balance = %d, want 10000", got)
	}
	if _, exists := sim.transactions["tx-1"]; exists {
		t.Fatalf("transaction was recorded despite injected HTTP failure")
	}
	if body := rr.Body.String(); !strings.Contains(body, "injected HTTP failure") {
		t.Fatalf("body = %q, want injected failure marker", body)
	}
}

func TestSimulatorWebhookInjectedHTTPFailure(t *testing.T) {
	sim := &simulator{failHTTPStatus: http.StatusServiceUnavailable}
	req := httptest.NewRequest(http.MethodPost, "/webhook", strings.NewReader(`{"event":"settled"}`))
	rr := httptest.NewRecorder()

	sim.webhook(rr, req)

	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusServiceUnavailable)
	}
	if body := rr.Body.String(); !strings.Contains(body, "injected HTTP failure") {
		t.Fatalf("body = %q, want injected failure marker", body)
	}
}

func TestSimulatorVerifyOwnershipEchoesChallenge(t *testing.T) {
	sim := &simulator{balanceCts: map[string]int64{}, transactions: map[string]transaction{}}
	body := `{"type":"callback.verify","challenge":"echo-me"}`
	request := httptest.NewRequest(http.MethodPost, "/verify", strings.NewReader(body))
	recorder := httptest.NewRecorder()
	sim.verifyOwnership(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("verifyOwnership status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), "echo-me") {
		t.Fatalf("verifyOwnership body = %s, want echoed challenge", recorder.Body.String())
	}
}

func TestSimulatorVerifyOwnershipHonorsFaultInjection(t *testing.T) {
	sim := &simulator{
		balanceCts:     map[string]int64{},
		transactions:   map[string]transaction{},
		failHTTPStatus: 503,
	}
	request := httptest.NewRequest(http.MethodPost, "/verify", strings.NewReader(`{"type":"callback.verify","challenge":"x"}`))
	recorder := httptest.NewRecorder()
	sim.verifyOwnership(recorder, request)
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("verifyOwnership with injected failure status = %d", recorder.Code)
	}
}

func TestSimulatorBalanceQuery(t *testing.T) {
	sim := &simulator{
		balanceCts:   map[string]int64{"user-1": 7000, "*": 10000},
		transactions: map[string]transaction{},
	}
	body := `{"callback_id":"balance-1","type":"balance","user_id":"user-1","currency":"USD"}`
	request := httptest.NewRequest(http.MethodPost, "/callback", strings.NewReader(body))
	request.Header.Set("X-PM-Timestamp", "1785398400")
	request.Header.Set("X-PM-Signature", "not-checked")
	recorder := httptest.NewRecorder()
	sim.callback(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("balance callback status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), "70.00") {
		t.Fatalf("balance callback body = %s, want 70.00", recorder.Body.String())
	}
}

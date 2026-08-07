package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/afun-game/predictmarket-saas/internal/currency"
	"github.com/afun-game/predictmarket-saas/internal/event"
	"github.com/afun-game/predictmarket-saas/internal/market"
	"github.com/afun-game/predictmarket-saas/internal/merchant"
	"github.com/afun-game/predictmarket-saas/internal/order"
	"github.com/afun-game/predictmarket-saas/internal/sportsingest"
	"github.com/afun-game/predictmarket-saas/internal/wallet"
)

type stubBoxRecIntaker struct {
	result sportsingest.Result
	err    error
	data   []byte
}

func (s *stubBoxRecIntaker) IngestBoxRec(ctx context.Context, data []byte) (sportsingest.Result, error) {
	s.data = data
	return s.result, s.err
}

func TestBoxRecIntakeProjectsPayload(t *testing.T) {
	service := &stubBoxRecIntaker{result: sportsingest.Result{Fetched: 306, Synced: 300, Skipped: 6}}
	handler := NewHandler(
		merchant.NewService(), event.NewService(), market.NewService(), wallet.NewService(),
		order.NewService(), currency.NewService(), "admin-secret", service,
	)
	payload := []byte(`{"provider":"boxrec","fetchedAt":"x","events":[]}`)

	response := performRequest(t, handler, http.MethodPost, "/api/v1/ingest/boxrec", payload, "Bearer admin-secret")
	if response.Code != http.StatusOK {
		t.Fatalf("ingest status = %d, body = %s", response.Code, response.Body.String())
	}
	if !bytes.Equal(service.data, payload) {
		t.Errorf("service received body = %s", service.data)
	}
	var body struct {
		Data struct {
			Fetched int `json:"fetched"`
			Synced  int `json:"synced"`
			Skipped int `json:"skipped"`
		} `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Data.Fetched != 306 || body.Data.Synced != 300 || body.Data.Skipped != 6 {
		t.Errorf("response data = %#v", body.Data)
	}
}

func TestBoxRecIntakeRequiresAdmin(t *testing.T) {
	service := &stubBoxRecIntaker{}
	handler := NewHandler(
		merchant.NewService(), event.NewService(), market.NewService(), wallet.NewService(),
		order.NewService(), currency.NewService(), "admin-secret", service,
	)
	response := performRequest(t, handler, http.MethodPost, "/api/v1/ingest/boxrec", []byte("{}"), "")
	if response.Code != http.StatusUnauthorized {
		t.Errorf("unauthenticated status = %d", response.Code)
	}
	if service.data != nil {
		t.Error("service called without auth")
	}
}

func TestBoxRecIntakeRejectsInvalidJSON(t *testing.T) {
	service := &stubBoxRecIntaker{err: sportsingest.ErrInvalidBoxRecJSON}
	handler := NewHandler(
		merchant.NewService(), event.NewService(), market.NewService(), wallet.NewService(),
		order.NewService(), currency.NewService(), "admin-secret", service,
	)
	response := performRequest(t, handler, http.MethodPost, "/api/v1/ingest/boxrec", []byte("{bad"), "Bearer admin-secret")
	if response.Code != http.StatusBadRequest {
		t.Errorf("invalid json status = %d, body = %s", response.Code, response.Body.String())
	}
}

func TestBoxRecIntakeReportsServiceFailure(t *testing.T) {
	service := &stubBoxRecIntaker{err: errors.New("database down")}
	handler := NewHandler(
		merchant.NewService(), event.NewService(), market.NewService(), wallet.NewService(),
		order.NewService(), currency.NewService(), "admin-secret", service,
	)
	response := performRequest(t, handler, http.MethodPost, "/api/v1/ingest/boxrec", []byte("{}"), "Bearer admin-secret")
	if response.Code != http.StatusInternalServerError {
		t.Errorf("service failure status = %d, body = %s", response.Code, response.Body.String())
	}
}

package httpapi

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync/atomic"
	"testing"

	"github.com/afun-game/predictmarket-saas/internal/currency"
	"github.com/afun-game/predictmarket-saas/internal/event"
	"github.com/afun-game/predictmarket-saas/internal/market"
	"github.com/afun-game/predictmarket-saas/internal/merchant"
	"github.com/afun-game/predictmarket-saas/internal/order"
	"github.com/afun-game/predictmarket-saas/internal/wallet"
)

type registrationCredentials struct {
	Data struct {
		MerchantID string `json:"merchant_id"`
		APIKey     string `json:"api_key"`
		APISecret  string `json:"api_secret"`
	} `json:"data"`
}

var httpRequestSequence atomic.Uint64

func TestMerchantHTTPFlow(t *testing.T) {
	t.Parallel()

	handler := NewHandler(
		merchant.NewService(),
		event.NewService(),
		market.NewService(),
		wallet.NewService(),
		order.NewService(),
		currency.NewService(),
		"admin-secret",
	)
	first := registerMerchant(t, handler, "Acme", "admin@acme.test")
	second := registerMerchant(t, handler, "Other", "admin@other.test")

	response := performRequest(
		t,
		handler,
		http.MethodGet,
		"/api/v1/merchants/"+first.Data.MerchantID+"/config",
		nil,
		"Bearer "+first.Data.APIKey,
	)
	if response.Code != http.StatusOK {
		t.Fatalf("get config status = %d, body = %s", response.Code, response.Body.String())
	}

	response = performRequest(
		t,
		handler,
		http.MethodGet,
		"/api/v1/merchants/"+first.Data.MerchantID+"/config",
		nil,
		"Bearer "+second.Data.APIKey,
	)
	if response.Code != http.StatusForbidden {
		t.Errorf("cross-merchant get status = %d, want %d", response.Code, http.StatusForbidden)
	}

	response = performRequest(
		t,
		handler,
		http.MethodPatch,
		"/api/v1/merchants/"+first.Data.MerchantID+"/config",
		[]byte(`{"currency":"EUR","timezone":"Europe/Paris"}`),
		"Bearer "+first.Data.APIKey,
	)
	if response.Code != http.StatusOK {
		t.Fatalf("update config status = %d, body = %s", response.Code, response.Body.String())
	}

	response = performRequest(
		t,
		handler,
		http.MethodGet,
		"/api/v1/merchants?page=1&limit=10",
		nil,
		"Bearer admin-secret",
	)
	if response.Code != http.StatusOK {
		t.Fatalf("admin list status = %d, body = %s", response.Code, response.Body.String())
	}

	for _, body := range [][]byte{
		[]byte(`{"fee_rate":0.02}`),
		[]byte(`{"status":"inactive"}`),
	} {
		response = performRequest(
			t,
			handler,
			http.MethodPatch,
			"/api/v1/merchants/"+first.Data.MerchantID+"/config",
			body,
			"Bearer "+first.Data.APIKey,
		)
		if response.Code != http.StatusBadRequest {
			t.Errorf(
				"restricted configuration update status = %d, want %d; body = %s",
				response.Code,
				http.StatusBadRequest,
				response.Body.String(),
			)
		}
	}
}

func TestMerchantHTTPRejectsInvalidRequests(t *testing.T) {
	t.Parallel()

	handler := NewHandler(
		merchant.NewService(),
		event.NewService(),
		market.NewService(),
		wallet.NewService(),
		order.NewService(),
		currency.NewService(),
		"admin-secret",
	)
	response := performRequest(
		t,
		handler,
		http.MethodPost,
		"/api/v1/merchants/register",
		[]byte(`{"name":"Acme","unknown":true}`),
		"",
	)
	if response.Code != http.StatusBadRequest {
		t.Errorf("unknown field status = %d, want %d", response.Code, http.StatusBadRequest)
	}

	response = performRequest(
		t,
		handler,
		http.MethodGet,
		"/api/v1/merchants?page=bad",
		nil,
		"Bearer admin-secret",
	)
	if response.Code != http.StatusBadRequest {
		t.Errorf("invalid page status = %d, want %d", response.Code, http.StatusBadRequest)
	}
}

func registerMerchant(
	t *testing.T,
	handler http.Handler,
	name string,
	email string,
) registrationCredentials {
	t.Helper()
	body := []byte(fmt.Sprintf(
		`{"name":%q,"email":%q,"currency":"USD","timezone":"UTC"}`,
		name,
		email,
	))
	response := performRequest(
		t,
		handler,
		http.MethodPost,
		"/api/v1/merchants/register",
		body,
		"",
	)
	if response.Code != http.StatusCreated {
		t.Fatalf("register status = %d, body = %s", response.Code, response.Body.String())
	}

	var credentials registrationCredentials
	if err := json.Unmarshal(response.Body.Bytes(), &credentials); err != nil {
		t.Fatalf("decode registration response: %v", err)
	}
	if credentials.Data.MerchantID == "" || credentials.Data.APISecret == "" {
		t.Fatalf("registration credentials = %#v", credentials)
	}
	return credentials
}

func performRequest(
	t *testing.T,
	handler http.Handler,
	method string,
	path string,
	body []byte,
	authorization string,
) *httptest.ResponseRecorder {
	return performRequestWithIdempotency(
		t,
		handler,
		method,
		path,
		body,
		authorization,
		"test-"+strconv.FormatUint(httpRequestSequence.Add(1), 10),
	)
}

func performRequestWithIdempotency(
	t *testing.T,
	handler http.Handler,
	method string,
	path string,
	body []byte,
	authorization string,
	idempotencyKey string,
) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, path, bytes.NewReader(body))
	if authorization != "" {
		request.Header.Set("Authorization", authorization)
	}
	if idempotencyKey != "" {
		request.Header.Set("Idempotency-Key", idempotencyKey)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

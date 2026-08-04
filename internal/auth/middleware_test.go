package auth

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/afun-game/predictmarket-saas/pkg/types"
)

type validatorStub struct{}

type signedValidatorStub struct{}

type userSessionValidatorStub struct{}

func (validatorStub) ValidateAPIKey(
	_ context.Context,
	apiKey string,
) (*types.Merchant, error) {
	if apiKey != "pk_live_valid" {
		return nil, errors.New("invalid key")
	}
	return &types.Merchant{ID: "merchant-1"}, nil
}

func (signedValidatorStub) ValidateSignedRequest(
	_ context.Context,
	_ string,
	_ string,
	_ string,
	_ []byte,
) (*types.Merchant, error) {
	return &types.Merchant{ID: "merchant-1"}, nil
}

func (userSessionValidatorStub) ValidateUserSession(
	_ context.Context,
	token string,
) (*UserSession, error) {
	if token != "valid-session" {
		return nil, errors.New("invalid session")
	}
	return &UserSession{ID: "session-1", UserID: "user-1"}, nil
}

func TestRequireUserSessionErrorResponses(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		authorization string
		wantCode      string
		wantChallenge string
	}{
		{name: "missing bearer", wantCode: "unauthorized", wantChallenge: "Bearer"},
		{
			name:          "invalid bearer",
			authorization: "Bearer invalid",
			wantCode:      "invalid_token",
			wantChallenge: `Bearer error="invalid_token"`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			request := httptest.NewRequest(http.MethodGet, "/", nil)
			request.Header.Set("Authorization", test.authorization)
			recorder := httptest.NewRecorder()
			RequireUserSession(userSessionValidatorStub{}, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusNoContent)
			})).ServeHTTP(recorder, request)

			if recorder.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want %d", recorder.Code, http.StatusUnauthorized)
			}
			if challenge := recorder.Header().Get("WWW-Authenticate"); challenge != test.wantChallenge {
				t.Errorf("WWW-Authenticate = %q, want %q", challenge, test.wantChallenge)
			}
			var response struct {
				Error struct {
					Code string `json:"code"`
				} `json:"error"`
			}
			if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			if response.Error.Code != test.wantCode {
				t.Errorf("error code = %q, want %q", response.Error.Code, test.wantCode)
			}
		})
	}
}

func TestRequireMerchant(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		header     string
		wantStatus int
	}{
		{name: "missing header", wantStatus: http.StatusUnauthorized},
		{name: "wrong scheme", header: "Basic pk_live_valid", wantStatus: http.StatusUnauthorized},
		{name: "invalid key", header: "Bearer invalid", wantStatus: http.StatusUnauthorized},
		{name: "valid key", header: "Bearer pk_live_valid", wantStatus: http.StatusNoContent},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				merchant, ok := MerchantFromContext(r.Context())
				if !ok || merchant.ID != "merchant-1" {
					t.Error("authenticated merchant missing from request context")
				}
				w.WriteHeader(http.StatusNoContent)
			})
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodGet, "/", nil)
			request.Header.Set("Authorization", test.header)

			RequireMerchant(validatorStub{}, next).ServeHTTP(recorder, request)
			if recorder.Code != test.wantStatus {
				t.Errorf("status = %d, want %d", recorder.Code, test.wantStatus)
			}
		})
	}
}

func TestRequireAdmin(t *testing.T) {
	t.Parallel()

	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})

	for _, test := range []struct {
		name       string
		configured string
		header     string
		wantStatus int
	}{
		{name: "empty configuration", header: "Bearer secret", wantStatus: http.StatusUnauthorized},
		{name: "wrong key", configured: "secret", header: "Bearer wrong", wantStatus: http.StatusUnauthorized},
		{name: "valid key", configured: "secret", header: "Bearer secret", wantStatus: http.StatusNoContent},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodGet, "/", nil)
			request.Header.Set("Authorization", test.header)
			RequireAdmin(test.configured, next).ServeHTTP(recorder, request)
			if recorder.Code != test.wantStatus {
				t.Errorf("status = %d, want %d", recorder.Code, test.wantStatus)
			}
		})
	}
}

func TestRequireSignedMerchantRejectsMissingReplayGuard(t *testing.T) {
	t.Parallel()

	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	request := httptest.NewRequest(http.MethodPost, "/", nil)
	request.Header.Set("Authorization", "Bearer pk_live_valid")
	request.Header.Set(signatureTimestampHeader, strconv.FormatInt(time.Now().Unix(), 10))
	request.Header.Set(signatureHeader, "not-checked")
	request.Header.Set(idempotencyKeyHeader, "request-1")
	recorder := httptest.NewRecorder()

	RequireSignedMerchant(signedValidatorStub{}, nil, next).ServeHTTP(recorder, request)
	if recorder.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", recorder.Code, http.StatusUnauthorized)
	}
}

func TestRequireSignedMerchantWithoutReplayAllowsDatabaseIdempotentRetry(t *testing.T) {
	t.Parallel()

	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	request := httptest.NewRequest(http.MethodPost, "/", nil)
	request.Header.Set("Authorization", "Bearer pk_live_valid")
	request.Header.Set(signatureTimestampHeader, strconv.FormatInt(time.Now().Unix(), 10))
	request.Header.Set(signatureHeader, "not-checked")
	request.Header.Set(idempotencyKeyHeader, "transfer-retry")

	recorder := httptest.NewRecorder()
	RequireSignedMerchantWithoutReplay(signedValidatorStub{}, next).ServeHTTP(recorder, request)
	if recorder.Code != http.StatusNoContent {
		t.Errorf("status = %d, want %d", recorder.Code, http.StatusNoContent)
	}
}

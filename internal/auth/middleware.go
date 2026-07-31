// Package auth provides HTTP authentication for merchant and admin API keys.
package auth

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/afun-game/predictmarket-saas/pkg/types"
)

type merchantContextKey struct{}
type userSessionContextKey struct{}

const (
	signatureTimestampHeader = "X-PM-Timestamp"
	signatureHeader          = "X-PM-Signature"
	idempotencyKeyHeader     = "Idempotency-Key"
	signedRequestMaxAge      = 5 * time.Minute
	maxSignedRequestBody     = 1 << 20
)

// MerchantValidator resolves an active merchant from an API key.
type MerchantValidator interface {
	ValidateAPIKey(ctx context.Context, apiKey string) (*types.Merchant, error)
}

// SignedMerchantValidator verifies a V3 HMAC request after its timestamp has
// been parsed and its raw body preserved.
type SignedMerchantValidator interface {
	ValidateSignedRequest(
		ctx context.Context,
		apiKey string,
		timestamp string,
		signature string,
		body []byte,
	) (*types.Merchant, error)
}

// ReplayGuard records state-changing V3 request keys for the signature window.
type ReplayGuard interface {
	ReserveNonce(ctx context.Context, merchantID, nonce string) error
}

// UserSession is the tenant-scoped browser identity carried by a Launch JWT.
type UserSession struct {
	ID         string
	MerchantID string
	UserID     string
	Currency   string
	WalletMode string
	Locale     string
}

// UserSessionValidator verifies a browser credential and checks server-side revocation.
type UserSessionValidator interface {
	ValidateUserSession(ctx context.Context, token string) (*UserSession, error)
}

// RequireMerchant authenticates a merchant Bearer token.
func RequireMerchant(validator MerchantValidator, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		apiKey, ok := bearerToken(r.Header.Get("Authorization"))
		if !ok {
			writeUnauthorized(w)
			return
		}

		merchant, err := validator.ValidateAPIKey(r.Context(), apiKey)
		if err != nil {
			writeUnauthorized(w)
			return
		}
		ctx := context.WithValue(r.Context(), merchantContextKey{}, merchant)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// RequireSignedMerchant authenticates a V3 server request with Bearer API key,
// timestamped HMAC, and replay prevention for state-changing methods.
func RequireSignedMerchant(
	validator SignedMerchantValidator,
	replayGuard ReplayGuard,
	next http.Handler,
) http.Handler {
	return requireSignedMerchant(validator, replayGuard, true, next)
}

// RequireSignedMerchantWithoutReplay authenticates an HMAC-signed merchant
// request without reserving its Idempotency-Key in Redis. It is only for
// operations that are independently idempotent in the primary database.
func RequireSignedMerchantWithoutReplay(
	validator SignedMerchantValidator,
	next http.Handler,
) http.Handler {
	return requireSignedMerchant(validator, nil, false, next)
}

func requireSignedMerchant(
	validator SignedMerchantValidator,
	replayGuard ReplayGuard,
	reserveReplayNonce bool,
	next http.Handler,
) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requiresReplay := reserveReplayNonce && requiresReplayProtection(r.Method)
		if validator == nil || (requiresReplay && replayGuard == nil) {
			writeUnauthorized(w)
			return
		}
		apiKey, ok := bearerToken(r.Header.Get("Authorization"))
		if !ok || !validTimestamp(r.Header.Get(signatureTimestampHeader)) {
			writeUnauthorized(w)
			return
		}
		body, err := readAndRestoreBody(w, r)
		if err != nil {
			writeUnauthorized(w)
			return
		}
		merchant, err := validator.ValidateSignedRequest(
			r.Context(),
			apiKey,
			r.Header.Get(signatureTimestampHeader),
			r.Header.Get(signatureHeader),
			body,
		)
		if err != nil {
			writeUnauthorized(w)
			return
		}
		if requiresReplay {
			nonce := strings.TrimSpace(r.Header.Get(idempotencyKeyHeader))
			if nonce == "" || replayGuard.ReserveNonce(r.Context(), merchant.ID, nonce) != nil {
				writeUnauthorized(w)
				return
			}
		}
		ctx := context.WithValue(r.Context(), merchantContextKey{}, merchant)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// RequireUserSession authenticates browser calls made after a Launch exchange.
func RequireUserSession(validator UserSessionValidator, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token, ok := bearerToken(r.Header.Get("Authorization"))
		if !ok {
			writeUnauthorized(w)
			return
		}
		session, err := validator.ValidateUserSession(r.Context(), token)
		if err != nil {
			writeUnauthorized(w)
			return
		}
		ctx := context.WithValue(r.Context(), userSessionContextKey{}, session)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// RequireAdmin authenticates an administrator Bearer token.
func RequireAdmin(adminAPIKey string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		apiKey, ok := bearerToken(r.Header.Get("Authorization"))
		if !ok || !sameAPIKey(apiKey, adminAPIKey) {
			writeUnauthorized(w)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// MerchantFromContext returns the authenticated merchant attached by RequireMerchant.
func MerchantFromContext(ctx context.Context) (*types.Merchant, bool) {
	merchant, ok := ctx.Value(merchantContextKey{}).(*types.Merchant)
	return merchant, ok
}

// UserSessionFromContext returns the browser identity set by RequireUserSession.
func UserSessionFromContext(ctx context.Context) (*UserSession, bool) {
	session, ok := ctx.Value(userSessionContextKey{}).(*UserSession)
	return session, ok
}

func bearerToken(header string) (string, bool) {
	scheme, token, found := strings.Cut(strings.TrimSpace(header), " ")
	token = strings.TrimSpace(token)
	if !found || !strings.EqualFold(scheme, "Bearer") || token == "" {
		return "", false
	}
	return token, true
}

func sameAPIKey(provided, expected string) bool {
	if expected == "" || len(provided) != len(expected) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(provided), []byte(expected)) == 1
}

func validTimestamp(value string) bool {
	seconds, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
	if err != nil {
		return false
	}
	delta := time.Since(time.Unix(seconds, 0))
	return delta >= -signedRequestMaxAge && delta <= signedRequestMaxAge
}

func readAndRestoreBody(w http.ResponseWriter, r *http.Request) ([]byte, error) {
	if r.Body == nil {
		return []byte{}, nil
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxSignedRequestBody))
	if err != nil {
		return nil, err
	}
	if err := r.Body.Close(); err != nil {
		return nil, err
	}
	r.Body = io.NopCloser(bytes.NewReader(body))
	return body, nil
}

func requiresReplayProtection(method string) bool {
	switch method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	default:
		return false
	}
}

func writeUnauthorized(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnauthorized)
	if err := json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]string{
			"code":    "unauthorized",
			"message": "a valid API key is required",
		},
	}); err != nil {
		return
	}
}

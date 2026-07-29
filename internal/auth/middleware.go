// Package auth provides HTTP authentication for merchant and admin API keys.
package auth

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/afun-game/predictmarket-saas/pkg/types"
)

type merchantContextKey struct{}

// MerchantValidator resolves an active merchant from an API key.
type MerchantValidator interface {
	ValidateAPIKey(ctx context.Context, apiKey string) (*types.Merchant, error)
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

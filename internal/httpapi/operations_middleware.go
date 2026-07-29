package httpapi

import (
	"log/slog"
	"net/http"
	"runtime/debug"
)

// RecoverPanic prevents a handler fault from terminating the API process.
func RecoverPanic(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if recovered := recover(); recovered != nil {
				slog.ErrorContext(
					r.Context(),
					"HTTP handler panic recovered",
					"panic", recovered,
					"stack", string(debug.Stack()),
				)
				writeError(w, http.StatusInternalServerError, "internal_error", "an internal error occurred")
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// LimitRequestBody applies the same body ceiling to every API route, including
// handlers that do not decode JSON themselves.
func LimitRequestBody(maxBytes int64) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.ContentLength > maxBytes {
				writeError(w, http.StatusRequestEntityTooLarge, "request_too_large", "request body exceeds the maximum size")
				return
			}
			if r.Body != nil {
				r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
			}
			next.ServeHTTP(w, r)
		})
	}
}

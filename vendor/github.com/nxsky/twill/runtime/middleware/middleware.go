// Copyright 2026 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Package middleware provides conservative net/http middleware primitives for
// Twill endpoint transports.
package middleware

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

const (
	// RequestIDHeader is the HTTP header used for request IDs.
	RequestIDHeader = "X-Request-Id"
	// IdempotencyKeyHeader is the HTTP header used to mark retryable unsafe requests.
	IdempotencyKeyHeader = "Idempotency-Key"
)

// Middleware wraps an HTTP handler.
type Middleware func(http.Handler) http.Handler

// Chain returns a middleware that applies middlewares in order.
func Chain(middlewares ...Middleware) Middleware {
	return func(next http.Handler) http.Handler {
		for i := len(middlewares) - 1; i >= 0; i-- {
			if middlewares[i] == nil {
				continue
			}
			next = middlewares[i](next)
		}
		return next
	}
}

// Timeout returns middleware that bounds request execution time.
func Timeout(timeout time.Duration) Middleware {
	if timeout <= 0 {
		return func(next http.Handler) http.Handler {
			return next
		}
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx, cancel := context.WithTimeout(r.Context(), timeout)
			defer cancel()

			tw := &timeoutResponseWriter{
				header: make(http.Header),
				status: http.StatusOK,
			}
			done := make(chan any, 1)
			go func() {
				defer func() {
					done <- recover()
				}()
				next.ServeHTTP(tw, r.WithContext(ctx))
			}()

			select {
			case panicValue := <-done:
				if panicValue != nil {
					panic(panicValue)
				}
				if errors.Is(ctx.Err(), context.DeadlineExceeded) {
					tw.timeout()
					WriteError(w, r.WithContext(ctx), &StatusError{
						Status:  http.StatusServiceUnavailable,
						Code:    "timeout",
						Message: "request timed out",
						Err:     ctx.Err(),
					})
					return
				}
				tw.flush(w)
			case <-ctx.Done():
				tw.timeout()
				WriteError(w, r.WithContext(ctx), &StatusError{
					Status:  http.StatusServiceUnavailable,
					Code:    "timeout",
					Message: "request timed out",
					Err:     ctx.Err(),
				})
			}
		})
	}
}

type timeoutResponseWriter struct {
	mu          sync.Mutex
	header      http.Header
	body        bytes.Buffer
	status      int
	wroteHeader bool
	timedOut    bool
}

func (w *timeoutResponseWriter) Header() http.Header {
	return w.header
}

func (w *timeoutResponseWriter) Write(data []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.timedOut {
		return 0, http.ErrHandlerTimeout
	}
	if !w.wroteHeader {
		w.status = http.StatusOK
		w.wroteHeader = true
	}
	return w.body.Write(data)
}

func (w *timeoutResponseWriter) WriteHeader(status int) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.timedOut || w.wroteHeader {
		return
	}
	w.status = status
	w.wroteHeader = true
}

func (w *timeoutResponseWriter) timeout() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.timedOut = true
}

func (w *timeoutResponseWriter) flush(dst http.ResponseWriter) {
	w.mu.Lock()
	defer w.mu.Unlock()
	for key, values := range w.header {
		for _, value := range values {
			dst.Header().Add(key, value)
		}
	}
	dst.WriteHeader(w.status)
	_, _ = dst.Write(w.body.Bytes())
}

type requestIDContextKey struct{}

// RequestIDGenerator generates request IDs.
type RequestIDGenerator func() string

// RequestID returns middleware that propagates or generates a request ID.
func RequestID() Middleware {
	return RequestIDWithGenerator(uuid.NewString)
}

// RequestIDWithGenerator returns request ID middleware with a custom generator.
func RequestIDWithGenerator(generate RequestIDGenerator) Middleware {
	if generate == nil {
		generate = uuid.NewString
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			id := strings.TrimSpace(r.Header.Get(RequestIDHeader))
			if id == "" {
				id = generate()
			}
			w.Header().Set(RequestIDHeader, id)
			ctx := context.WithValue(r.Context(), requestIDContextKey{}, id)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// RequestIDFromContext returns the request ID stored in ctx.
func RequestIDFromContext(ctx context.Context) (string, bool) {
	id, ok := ctx.Value(requestIDContextKey{}).(string)
	return id, ok && id != ""
}

// AuthFunc validates an HTTP request before it reaches a handler.
type AuthFunc func(*http.Request) error

// AuthHook returns middleware that rejects requests that fail auth.
func AuthHook(auth AuthFunc) Middleware {
	if auth == nil {
		return func(next http.Handler) http.Handler {
			return next
		}
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if err := auth(r); err != nil {
				WriteError(w, r, &StatusError{
					Status:  http.StatusUnauthorized,
					Code:    "unauthorized",
					Message: "unauthorized",
					Err:     err,
				})
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// ErrorHandlerFunc is an HTTP handler that can return an error.
type ErrorHandlerFunc func(http.ResponseWriter, *http.Request) error

// StatusError maps an application error to an HTTP response.
type StatusError struct {
	Status  int
	Code    string
	Message string
	Err     error
}

func (e *StatusError) Error() string {
	if e == nil {
		return ""
	}
	if e.Err != nil {
		return e.Err.Error()
	}
	if e.Message != "" {
		return e.Message
	}
	return http.StatusText(e.Status)
}

// Unwrap returns the wrapped error.
func (e *StatusError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

// ErrorResponse is the JSON response body emitted by WriteError.
type ErrorResponse struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	RequestID string `json:"request_id,omitempty"`
}

// HandleErrors adapts an ErrorHandlerFunc into an HTTP handler with structured errors.
func HandleErrors(handler ErrorHandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := handler(w, r); err != nil {
			WriteError(w, r, err)
		}
	})
}

// WriteError writes err as a structured JSON error response.
func WriteError(w http.ResponseWriter, r *http.Request, err error) {
	status, code, message := mapError(err)
	response := ErrorResponse{
		Code:    code,
		Message: message,
	}
	if id, ok := RequestIDFromContext(r.Context()); ok {
		response.RequestID = id
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(response)
}

func mapError(err error) (int, string, string) {
	var statusErr *StatusError
	if errors.As(err, &statusErr) {
		status := statusErr.Status
		if status < 100 {
			status = http.StatusInternalServerError
		}
		code := statusErr.Code
		if code == "" {
			code = "http_" + strconv.Itoa(status)
		}
		message := statusErr.Message
		if message == "" {
			message = http.StatusText(status)
		}
		return status, code, message
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return http.StatusGatewayTimeout, "timeout", "request timed out"
	}
	if errors.Is(err, context.Canceled) {
		return http.StatusServiceUnavailable, "canceled", "request canceled"
	}
	return http.StatusInternalServerError, "internal_error", "internal server error"
}

// RetryAllowed reports whether retrying r is safe by default.
func RetryAllowed(r *http.Request) bool {
	switch r.Method {
	case http.MethodGet, http.MethodHead, http.MethodOptions, http.MethodTrace:
		return true
	case http.MethodPut, http.MethodDelete:
		return true
	default:
		return strings.TrimSpace(r.Header.Get(IdempotencyKeyHeader)) != ""
	}
}

// RequireIdempotencyKey rejects unsafe methods unless they include an idempotency key.
func RequireIdempotencyKey(methods ...string) Middleware {
	required := map[string]struct{}{}
	if len(methods) == 0 {
		required[http.MethodPost] = struct{}{}
		required[http.MethodPatch] = struct{}{}
	}
	for _, method := range methods {
		method = strings.ToUpper(strings.TrimSpace(method))
		if method != "" {
			required[method] = struct{}{}
		}
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if _, ok := required[r.Method]; ok && strings.TrimSpace(r.Header.Get(IdempotencyKeyHeader)) == "" {
				WriteError(w, r, &StatusError{
					Status:  http.StatusPreconditionRequired,
					Code:    "idempotency_key_required",
					Message: "idempotency key required",
				})
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// RateLimit returns a process-local fixed-window rate limiter.
func RateLimit(limit int, window time.Duration) Middleware {
	if limit <= 0 || window <= 0 {
		return func(next http.Handler) http.Handler {
			return next
		}
	}
	limiter := &fixedWindowLimiter{
		limit:  limit,
		window: window,
		reset:  time.Now().Add(window),
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if retryAfter, ok := limiter.allow(time.Now()); !ok {
				w.Header().Set("Retry-After", strconv.Itoa(int(retryAfter.Seconds())+1))
				WriteError(w, r, &StatusError{
					Status:  http.StatusTooManyRequests,
					Code:    "rate_limited",
					Message: "rate limit exceeded",
				})
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

type fixedWindowLimiter struct {
	mu     sync.Mutex
	limit  int
	window time.Duration
	reset  time.Time
	count  int
}

func (l *fixedWindowLimiter) allow(now time.Time) (time.Duration, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !now.Before(l.reset) {
		l.reset = now.Add(l.window)
		l.count = 0
	}
	if l.count >= l.limit {
		return time.Until(l.reset), false
	}
	l.count++
	return 0, true
}

// CircuitBreakerOptions configures CircuitBreaker.
type CircuitBreakerOptions struct {
	FailureThreshold int
	OpenDuration     time.Duration
}

// CircuitBreaker returns process-local circuit-breaker middleware.
func CircuitBreaker(options CircuitBreakerOptions) Middleware {
	if options.FailureThreshold <= 0 {
		options.FailureThreshold = 5
	}
	if options.OpenDuration <= 0 {
		options.OpenDuration = time.Second
	}
	breaker := &circuitBreaker{
		failureThreshold: options.FailureThreshold,
		openDuration:     options.OpenDuration,
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !breaker.allow(time.Now()) {
				WriteError(w, r, &StatusError{
					Status:  http.StatusServiceUnavailable,
					Code:    "circuit_open",
					Message: "circuit breaker open",
				})
				return
			}
			recorder := &statusRecorder{
				ResponseWriter: w,
				status:         http.StatusOK,
			}
			next.ServeHTTP(recorder, r)
			breaker.observe(time.Now(), recorder.status)
		})
	}
}

type circuitBreaker struct {
	mu               sync.Mutex
	failureThreshold int
	openDuration     time.Duration
	failures         int
	openUntil        time.Time
}

func (b *circuitBreaker) allow(now time.Time) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.openUntil.IsZero() || !now.Before(b.openUntil)
}

func (b *circuitBreaker) observe(now time.Time, status int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if status < http.StatusInternalServerError {
		b.failures = 0
		b.openUntil = time.Time{}
		return
	}
	b.failures++
	if b.failures >= b.failureThreshold {
		b.openUntil = now.Add(b.openDuration)
	}
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(status int) {
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}

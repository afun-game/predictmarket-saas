// Package observability exposes lightweight Prometheus-compatible telemetry.
package observability

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

type requestKey struct {
	method string
	path   string
	status int
}

// Metrics records request-path health and derives safety gauges from PostgreSQL
// when Prometheus scrapes /metrics.
type Metrics struct {
	database *sql.DB

	mu       sync.Mutex
	requests map[requestKey]uint64
	errors   map[requestKey]uint64

	orders        atomic.Uint64
	durationNanos atomic.Int64
	durationCount atomic.Uint64
}

// NewMetrics opens the read-only connection used by scrape-time gauges.
func NewMetrics(databaseURL string) (*Metrics, error) {
	database, err := sql.Open("pgx", databaseURL)
	if err != nil {
		return nil, fmt.Errorf("open metrics database: %w", err)
	}
	return &Metrics{
		database: database,
		requests: map[requestKey]uint64{},
		errors:   map[requestKey]uint64{},
	}, nil
}

// Close releases the scrape-time database connection.
func (m *Metrics) Close() error {
	if m == nil || m.database == nil {
		return nil
	}
	return m.database.Close()
}

// Middleware records HTTP request count, error rate, latency, and successful
// order creation without coupling handlers to metrics code.
func (m *Metrics) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		response := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(response, r)
		m.record(r, response.status, time.Since(started))
	})
}

func (m *Metrics) record(r *http.Request, status int, elapsed time.Duration) {
	if m == nil {
		return
	}
	path := r.Pattern
	if path == "" {
		path = "unmatched"
	}
	key := requestKey{method: r.Method, path: path, status: status}
	m.mu.Lock()
	m.requests[key]++
	if status >= http.StatusInternalServerError {
		m.errors[key]++
	}
	m.mu.Unlock()
	if r.Method == http.MethodPost && path == "/api/v1/orders" && status == http.StatusCreated {
		m.orders.Add(1)
	}
	m.durationCount.Add(1)
	m.durationNanos.Add(elapsed.Nanoseconds())
}

// Handler writes Prometheus text exposition format. A failed optional gauge
// query does not make scraping fail; request telemetry remains available.
func (m *Metrics) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		m.write(w, r.Context())
	})
}

func (m *Metrics) write(w http.ResponseWriter, ctx context.Context) {
	fmt.Fprintln(w, "# HELP predictmarket_http_requests_total HTTP requests handled by the API.")
	fmt.Fprintln(w, "# TYPE predictmarket_http_requests_total counter")
	fmt.Fprintln(w, "# HELP predictmarket_http_errors_total HTTP requests that returned a 5xx response.")
	fmt.Fprintln(w, "# TYPE predictmarket_http_errors_total counter")
	m.mu.Lock()
	requestKeys := make([]requestKey, 0, len(m.requests))
	for key := range m.requests {
		requestKeys = append(requestKeys, key)
	}
	sort.Slice(requestKeys, func(i, j int) bool {
		return formatKey(requestKeys[i]) < formatKey(requestKeys[j])
	})
	for _, key := range requestKeys {
		fmt.Fprintf(w, "predictmarket_http_requests_total{%s} %d\n", formatKey(key), m.requests[key])
		if errors := m.errors[key]; errors > 0 {
			fmt.Fprintf(w, "predictmarket_http_errors_total{%s} %d\n", formatKey(key), errors)
		}
	}
	m.mu.Unlock()

	fmt.Fprintln(w, "# HELP predictmarket_http_request_duration_seconds Total API request latency.")
	fmt.Fprintln(w, "# TYPE predictmarket_http_request_duration_seconds summary")
	fmt.Fprintf(w, "predictmarket_http_request_duration_seconds_count %d\n", m.durationCount.Load())
	fmt.Fprintf(w, "predictmarket_http_request_duration_seconds_sum %.9f\n", float64(m.durationNanos.Load())/float64(time.Second))
	fmt.Fprintln(w, "# HELP predictmarket_orders_total Successfully created orders.")
	fmt.Fprintln(w, "# TYPE predictmarket_orders_total counter")
	fmt.Fprintf(w, "predictmarket_orders_total %d\n", m.orders.Load())

	lag, stranded, err := m.safetyGauges(ctx)
	if err != nil {
		fmt.Fprintf(w, "# safety gauge query failed: %s\n", strings.ReplaceAll(err.Error(), "\n", " "))
	}
	fmt.Fprintln(w, "# HELP predictmarket_settlement_lag_seconds Age of the oldest overdue unsettled event.")
	fmt.Fprintln(w, "# TYPE predictmarket_settlement_lag_seconds gauge")
	fmt.Fprintf(w, "predictmarket_settlement_lag_seconds %.3f\n", lag)
	fmt.Fprintln(w, "# HELP predictmarket_stranded_collateral_amount Collateral locked without an open order.")
	fmt.Fprintln(w, "# TYPE predictmarket_stranded_collateral_amount gauge")
	fmt.Fprintf(w, "predictmarket_stranded_collateral_amount %.2f\n", stranded)
}

func (m *Metrics) safetyGauges(ctx context.Context) (float64, float64, error) {
	if m == nil || m.database == nil {
		return 0, 0, fmt.Errorf("metrics database is not configured")
	}
	queryCtx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	const lagQuery = `
SELECT COALESCE(EXTRACT(EPOCH FROM (NOW() - MIN(e.resolution_time))), 0)
FROM events AS e
WHERE e.status <> 'resolved'
  AND e.resolution_time < NOW()
  AND EXISTS (SELECT 1 FROM markets AS m WHERE m.event_id = e.id)`
	var lag float64
	if err := m.database.QueryRowContext(queryCtx, lagQuery).Scan(&lag); err != nil {
		return 0, 0, fmt.Errorf("query settlement lag: %w", err)
	}
	const strandedQuery = `
SELECT COALESCE(SUM(w.locked_balance), 0)
FROM wallets AS w
WHERE w.locked_balance > 0
  AND NOT EXISTS (
      SELECT 1 FROM orders AS o
      WHERE o.merchant_id = w.merchant_id
        AND o.user_id = w.user_id
        AND o.currency = w.currency
        AND o.status IN ('pending', 'partial', 'filled')
  )`
	var stranded float64
	if err := m.database.QueryRowContext(queryCtx, strandedQuery).Scan(&stranded); err != nil {
		return 0, 0, fmt.Errorf("query stranded collateral: %w", err)
	}
	return lag, stranded, nil
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(status int) {
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

func (w *statusWriter) Write(data []byte) (int, error) {
	return w.ResponseWriter.Write(data)
}

func formatKey(key requestKey) string {
	return "method=\"" + escapeLabel(key.method) + "\",path=\"" + escapeLabel(key.path) + "\",status=\"" + strconv.Itoa(key.status) + "\""
}

func escapeLabel(value string) string {
	value = strings.ReplaceAll(value, "\\", "\\\\")
	value = strings.ReplaceAll(value, "\n", "\\n")
	return strings.ReplaceAll(value, "\"", "\\\"")
}

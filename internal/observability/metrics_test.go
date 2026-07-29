package observability

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMetricsMiddlewareRecordsOrderAndRequest(t *testing.T) {
	t.Parallel()

	metrics := &Metrics{requests: map[requestKey]uint64{}, errors: map[requestKey]uint64{}}
	handler := metrics.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Pattern = "/api/v1/orders"
		w.WriteHeader(http.StatusCreated)
	}))
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/api/v1/orders", nil))
	if metrics.orders.Load() != 1 || metrics.durationCount.Load() != 1 {
		t.Errorf("metrics orders/count = %d/%d", metrics.orders.Load(), metrics.durationCount.Load())
	}
	if got := metrics.requests[requestKey{method: http.MethodPost, path: "/api/v1/orders", status: http.StatusCreated}]; got != 1 {
		t.Errorf("request metric = %d, want 1", got)
	}
}

func TestEscapeLabel(t *testing.T) {
	t.Parallel()
	if got := escapeLabel("a\\\"\nb"); !strings.Contains(got, "\\\\") || !strings.Contains(got, "\\\"") || !strings.Contains(got, "\\n") {
		t.Errorf("escapeLabel() = %q", got)
	}
}

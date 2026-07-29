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

package observability

import (
	"bytes"
	"net/http"
	"os"
	"strings"

	"github.com/nxsky/twill/runtime/metrics"
	"github.com/nxsky/twill/runtime/prometheus"
)

// DefaultMetricsPath is the HTTP path used when RegisterMetricsHandler is
// called without an explicit path. Aligns with common Prometheus scrape
// conventions and the TWILL_METRICS_PATH override.
const DefaultMetricsPath = "/metrics"

// MetricsPath returns the configured metrics scrape path. Override with
// TWILL_METRICS_PATH; empty or unset yields DefaultMetricsPath.
func MetricsPath() string {
	if v := strings.TrimSpace(os.Getenv("TWILL_METRICS_PATH")); v != "" {
		if !strings.HasPrefix(v, "/") {
			v = "/" + v
		}
		return v
	}
	return DefaultMetricsPath
}

// MetricsEnabled reports whether the Prometheus metrics endpoint should be
// exposed. Disabled when TWILL_METRICS_ENABLED is "false", "0", "off", or
// "disabled". Defaults to enabled.
func MetricsEnabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("TWILL_METRICS_ENABLED"))) {
	case "false", "0", "off", "disabled", "no":
		return false
	default:
		return true
	}
}

// MetricsHandler returns an http.Handler that serves Twill runtime metrics in
// Prometheus text exposition format. Safe to mount on any ServeMux.
func MetricsHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		snapshots := metrics.Snapshot()
		var buf bytes.Buffer
		path := r.URL.Path
		if path == "" {
			path = MetricsPath()
		}
		prometheus.TranslateMetricsToPrometheusTextFormat(&buf, snapshots, r.Host, path)
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		if r.Method == http.MethodHead {
			return
		}
		_, _ = w.Write(buf.Bytes())
	})
}

// RegisterMetricsHandler mounts MetricsHandler on mux at MetricsPath when
// metrics are enabled. Returns the path that was registered, or "" when
// disabled.
func RegisterMetricsHandler(mux *http.ServeMux) string {
	if mux == nil || !MetricsEnabled() {
		return ""
	}
	path := MetricsPath()
	mux.Handle(path, MetricsHandler())
	return path
}

// GenerateConfigReport metrics section helpers: keep MetricsConfig in sync with
// the env-driven defaults above.
func metricsConfigFromEnv() MetricsConfig {
	exporter := "prometheus"
	if !MetricsEnabled() {
		return MetricsConfig{Enabled: false, Exporter: "none"}
	}
	return MetricsConfig{
		Enabled:  true,
		Exporter: exporter,
		Endpoint: MetricsPath(),
	}
}

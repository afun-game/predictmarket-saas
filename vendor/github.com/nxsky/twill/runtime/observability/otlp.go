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
	"context"
	"fmt"
	"os"
	"strings"

	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// otlpHTTPProvider is the built-in OTLP/HTTP span exporter. Configuration is
// read from the standard OpenTelemetry environment variables:
//
//   - OTEL_EXPORTER_OTLP_ENDPOINT or OTEL_EXPORTER_OTLP_TRACES_ENDPOINT
//   - OTEL_EXPORTER_OTLP_HEADERS (comma-separated key=value)
//   - OTEL_EXPORTER_OTLP_INSECURE ("true" for plain HTTP)
//
// Set TWILL_TRACE_EXPORTER=otlp (or leave it unset when
// OTEL_EXPORTER_OTLP_ENDPOINT is present) to enable this exporter.
type otlpHTTPProvider struct{}

func init() {
	RegisterTraceExporter("otlp", otlpHTTPProvider{})
	RegisterTraceExporter("otlphttp", otlpHTTPProvider{})
	RegisterTraceExporter("otlp-http", otlpHTTPProvider{})
}

// New implements TraceExporterProvider.
func (otlpHTTPProvider) New(ctx context.Context) (sdktrace.SpanExporter, error) {
	opts, err := otlpHTTPOptionsFromEnv()
	if err != nil {
		return nil, err
	}
	exp, err := otlptracehttp.New(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("otlp http exporter: %w", err)
	}
	return exp, nil
}

func otlpHTTPOptionsFromEnv() ([]otlptracehttp.Option, error) {
	var opts []otlptracehttp.Option

	endpoint := firstNonEmpty(
		os.Getenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT"),
		os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"),
	)
	if endpoint != "" {
		// otlptracehttp accepts either WithEndpointURL (full URL) or
		// WithEndpoint (host:port). Prefer full URL when a scheme is present.
		if strings.Contains(endpoint, "://") {
			opts = append(opts, otlptracehttp.WithEndpointURL(endpoint))
		} else {
			opts = append(opts, otlptracehttp.WithEndpoint(endpoint))
		}
	}

	if insecureOTLP() {
		opts = append(opts, otlptracehttp.WithInsecure())
	}

	if headers := parseOTLPHeaders(os.Getenv("OTEL_EXPORTER_OTLP_HEADERS")); len(headers) > 0 {
		opts = append(opts, otlptracehttp.WithHeaders(headers))
	}

	return opts, nil
}

func insecureOTLP() bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv("OTEL_EXPORTER_OTLP_INSECURE")))
	if v == "true" || v == "1" || v == "yes" {
		return true
	}
	// When only an http:// endpoint is configured, default to insecure.
	endpoint := firstNonEmpty(
		os.Getenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT"),
		os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"),
	)
	return strings.HasPrefix(strings.ToLower(endpoint), "http://")
}

func parseOTLPHeaders(raw string) map[string]string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	out := map[string]string{}
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		key, val, ok := strings.Cut(part, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		val = strings.TrimSpace(val)
		if key != "" {
			out[key] = val
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

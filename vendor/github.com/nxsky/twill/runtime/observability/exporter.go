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
	"io"
	"log/slog"
	"os"
	"strings"
	"time"

	"go.opentelemetry.io/otel/exporters/stdout/stdouttrace"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// ExporterType identifies a trace exporter kind.
type ExporterType string

const (
	// ExporterStdout writes traces to stdout (or a configured writer).
	ExporterStdout ExporterType = "stdout"
	// ExporterNone disables trace export.
	ExporterNone ExporterType = "none"
	// ExporterOTLP uses the built-in OTLP/HTTP exporter (registered as "otlp").
	ExporterOTLP ExporterType = "otlp"
	// ExporterCustom uses a registered TraceExporterProvider.
	ExporterCustom ExporterType = "custom"
)

// TraceExporterProvider creates a trace.SpanExporter for production use
// (e.g., OTLP to Jaeger/Tempo, Datadog, etc.). Users register a provider
// via RegisterTraceExporter to replace the default stdout exporter.
//
// Example with OTLP HTTP:
//
//	type otlpProvider struct{}
//	func (otlpProvider) New(ctx context.Context) (sdktrace.SpanExporter, error) {
//	    return otlptracehttp.New(ctx,
//	        otlptracehttp.WithEndpointURL(os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT")),
//	    )
//	}
//	func init() {
//	    observability.RegisterTraceExporter("otlp", otlpProvider{})
//	}
type TraceExporterProvider interface {
	// New creates a SpanExporter. The provider should read its configuration
	// from environment variables or other sources.
	New(ctx context.Context) (sdktrace.SpanExporter, error)
}

var exporterProviders = map[string]TraceExporterProvider{}

// RegisterTraceExporter registers a named trace exporter provider. If
// TWILL_TRACE_EXPORTER matches name, the provider is used instead of the
// default stdout exporter. This should be called once at program startup.
func RegisterTraceExporter(name string, provider TraceExporterProvider) {
	exporterProviders[strings.ToLower(name)] = provider
}

// ExporterConfig holds the resolved trace exporter configuration.
type ExporterConfig struct {
	Type       ExporterType `json:"type"`
	Provider   string       `json:"provider,omitempty"`
	Endpoint   string       `json:"endpoint,omitempty"`
	Sampler    string       `json:"sampler"`
	SampleRate float64      `json:"sample_rate"`
}

// ResolveExporterConfig reads environment variables and returns the
// effective trace exporter configuration. It checks:
//
//   - TWILL_TRACE_EXPORTER: "stdout", "none", or a registered provider name.
//     Defaults to "stdout".
//   - OTEL_EXPORTER_OTLP_ENDPOINT: OTLP endpoint URL (informational).
//   - OTEL_TRACES_SAMPLER: "always_on", "always_off", "traceidratio".
//     Defaults to "always_on" for local, "traceidratio" for remote.
//   - OTEL_TRACES_SAMPLER_ARG: sample rate for ratio sampler (0.0-1.0).
func ResolveExporterConfig() ExporterConfig {
	cfg := ExporterConfig{
		Type:       ExporterStdout,
		Sampler:    "always_on",
		SampleRate: 1.0,
	}

	if v := os.Getenv("TWILL_TRACE_EXPORTER"); v != "" {
		v = strings.ToLower(v)
		switch v {
		case "stdout":
			cfg.Type = ExporterStdout
		case "none", "off", "disabled":
			cfg.Type = ExporterNone
		case "otlp", "otlphttp", "otlp-http":
			cfg.Type = ExporterOTLP
			cfg.Provider = "otlp"
		default:
			if _, ok := exporterProviders[v]; ok {
				cfg.Type = ExporterCustom
				cfg.Provider = v
			} else {
				// Unknown exporter, fall back to stdout.
				cfg.Type = ExporterStdout
			}
		}
	}

	if v := os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"); v != "" {
		cfg.Endpoint = v
		// If no explicit TWILL_TRACE_EXPORTER was set but OTLP endpoint
		// is configured, prefer the built-in OTLP exporter.
		if os.Getenv("TWILL_TRACE_EXPORTER") == "" {
			if _, ok := exporterProviders["otlp"]; ok {
				cfg.Type = ExporterOTLP
				cfg.Provider = "otlp"
			}
		}
	}

	if v := os.Getenv("OTEL_TRACES_SAMPLER"); v != "" {
		cfg.Sampler = strings.ToLower(v)
	}
	if v := os.Getenv("OTEL_TRACES_SAMPLER_ARG"); v != "" {
		var rate float64
		fmt.Sscanf(v, "%f", &rate)
		if rate > 0 && rate <= 1.0 {
			cfg.SampleRate = rate
		}
	}

	return cfg
}

// newExporter creates a SpanExporter from the resolved configuration.
func newExporter(ctx context.Context, cfg ExporterConfig, writer io.Writer) (sdktrace.SpanExporter, error) {
	switch cfg.Type {
	case ExporterNone:
		return nil, nil
	case ExporterOTLP, ExporterCustom:
		name := cfg.Provider
		if name == "" {
			name = "otlp"
		}
		provider, ok := exporterProviders[name]
		if !ok {
			return nil, fmt.Errorf("trace exporter provider %q not registered", name)
		}
		return provider.New(ctx)
	case ExporterStdout:
		fallthrough
	default:
		w := writer
		if w == nil {
			w = os.Stdout
		}
		return stdouttrace.New(
			stdouttrace.WithWriter(w),
			stdouttrace.WithoutTimestamps(),
		)
	}
}

// samplerFromConfig returns a sampler based on the configuration.
func samplerFromConfig(cfg ExporterConfig) sdktrace.Sampler {
	switch cfg.Sampler {
	case "always_off":
		return sdktrace.NeverSample()
	case "traceidratio":
		return sdktrace.TraceIDRatioBased(cfg.SampleRate)
	case "always_on":
		fallthrough
	default:
		return sdktrace.AlwaysSample()
	}
}

// ConfigReport is a human-readable observability configuration report
// suitable for display by the CLI.
type ConfigReport struct {
	Traces              ExporterConfig `json:"traces"`
	Metrics             MetricsConfig  `json:"metrics"`
	Logs                LogsConfig     `json:"logs"`
	RegisteredExporters []string       `json:"registered_exporters,omitempty"`
	EnvVars             []EnvVarHint   `json:"env_vars"`
}

// MetricsConfig describes the metrics export configuration.
type MetricsConfig struct {
	Enabled  bool   `json:"enabled"`
	Exporter string `json:"exporter"`
	Endpoint string `json:"endpoint,omitempty"`
}

// LogsConfig describes the log export configuration.
type LogsConfig struct {
	Level     string `json:"level"`
	Format    string `json:"format"`
	Redaction bool   `json:"redaction"`
}

// EnvVarHint documents an environment variable and its effect.
type EnvVarHint struct {
	Name    string `json:"name"`
	Value   string `json:"value,omitempty"`
	Default string `json:"default"`
	Effect  string `json:"effect"`
}

// GenerateConfigReport produces a configuration report from the current
// environment variables and registered providers.
func GenerateConfigReport() ConfigReport {
	traceCfg := ResolveExporterConfig()

	report := ConfigReport{
		Traces:  traceCfg,
		Metrics: metricsConfigFromEnv(),
		Logs: LogsConfig{
			Level:     LogLevelFromEnv().String(),
			Format:    LogFormatFromEnv(),
			Redaction: true,
		},
		EnvVars: []EnvVarHint{},
	}

	for name := range exporterProviders {
		report.RegisteredExporters = append(report.RegisteredExporters, name)
	}

	// Document relevant env vars.
	envVars := []struct {
		name, def, effect string
	}{
		{"TWILL_TRACE_EXPORTER", "stdout", "Trace exporter: stdout, none, otlp, or registered provider name"},
		{"OTEL_EXPORTER_OTLP_ENDPOINT", "", "OTLP endpoint URL (e.g., http://jaeger:4318); enables otlp when TWILL_TRACE_EXPORTER is unset"},
		{"OTEL_EXPORTER_OTLP_INSECURE", "", "Use plain HTTP for OTLP (true/false); default true for http:// endpoints"},
		{"OTEL_EXPORTER_OTLP_HEADERS", "", "Comma-separated OTLP headers as key=value pairs"},
		{"OTEL_TRACES_SAMPLER", "always_on", "Trace sampler: always_on, always_off, traceidratio"},
		{"OTEL_TRACES_SAMPLER_ARG", "1.0", "Sample rate for ratio sampler (0.0-1.0)"},
		{"TWILL_METRICS_ENABLED", "true", "Expose Prometheus /metrics endpoint (true/false)"},
		{"TWILL_METRICS_PATH", "/metrics", "HTTP path for Prometheus metrics scrape"},
		{"TWILL_CONFIG_DIR", "", "Directory for file-based config (K8s ConfigMap mount)"},
		{"TWILL_SECRET_DIR", "", "Directory for file-based secrets (K8s Secret mount)"},
		{"TWILL_LOG_LEVEL", "info", "Log level: debug, info, warn, error"},
		{"TWILL_LOG_FORMAT", "json", "Log format: json, text"},
	}
	for _, ev := range envVars {
		val := os.Getenv(ev.name)
		hint := EnvVarHint{
			Name:    ev.name,
			Value:   val,
			Default: ev.def,
			Effect:  ev.effect,
		}
		if val == "" {
			hint.Value = "(not set)"
		}
		report.EnvVars = append(report.EnvVars, hint)
	}

	return report
}

// LogLevelFromEnv returns the configured log level, defaulting to LevelInfo.
func LogLevelFromEnv() slog.Level {
	switch strings.ToLower(os.Getenv("TWILL_LOG_LEVEL")) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// LogFormatFromEnv returns "json" or "text" based on TWILL_LOG_FORMAT.
func LogFormatFromEnv() string {
	if v := strings.ToLower(os.Getenv("TWILL_LOG_FORMAT")); v == "text" {
		return "text"
	}
	return "json"
}

// startTimeout is the maximum time to wait for exporter initialization.
const startTimeout = 5 * time.Second

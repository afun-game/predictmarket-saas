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

// Package observability provides opt-in local observability defaults for Twill
// applications.
package observability

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/nxsky/twill/runtime/metrics"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"
)

// Options configures local observability defaults.
type Options struct {
	ServiceName   string
	TraceWriter   io.Writer
	LogWriter     io.Writer
	Disabled      bool
	InstallGlobal bool
}

// Defaults contains local observability primitives.
type Defaults struct {
	ServiceName string
	Tracer      trace.Tracer
	Logger      *slog.Logger
	Enabled     bool

	tracerProvider trace.TracerProvider
	shutdown       func(context.Context) error
}

// Start initializes local observability defaults.
func Start(ctx context.Context, opts Options) (*Defaults, error) {
	if opts.ServiceName == "" {
		opts.ServiceName = "twill"
	}
	if opts.Disabled {
		logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
		return &Defaults{
			ServiceName:    opts.ServiceName,
			Tracer:         noop.NewTracerProvider().Tracer(opts.ServiceName),
			Logger:         logger,
			Enabled:        false,
			tracerProvider: noop.NewTracerProvider(),
			shutdown:       func(context.Context) error { return nil },
		}, nil
	}

	// Resolve exporter configuration from environment variables.
	expCfg := ResolveExporterConfig()

	traceWriter := opts.TraceWriter
	if traceWriter == nil {
		traceWriter = os.Stdout
	}
	logWriter := opts.LogWriter
	if logWriter == nil {
		logWriter = os.Stdout
	}

	exporter, err := newExporter(ctx, expCfg, traceWriter)
	if err != nil {
		return nil, fmt.Errorf("trace exporter: %w", err)
	}

	var tracerProvider *sdktrace.TracerProvider
	if exporter == nil {
		// ExporterNone: use noop tracer.
		noopProvider := noop.NewTracerProvider()
		if opts.InstallGlobal {
			otel.SetTracerProvider(noopProvider)
		}
		tracerProvider = nil
		defaults := &Defaults{
			ServiceName:    opts.ServiceName,
			Tracer:         noopProvider.Tracer(opts.ServiceName),
			Logger:         slog.New(newRedactingHandler(logWriter)),
			Enabled:        true,
			tracerProvider: noopProvider,
			shutdown:       func(context.Context) error { return nil },
		}
		return defaults, nil
	}

	tracerProvider = sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithSampler(samplerFromConfig(expCfg)),
	)
	if opts.InstallGlobal {
		otel.SetTracerProvider(tracerProvider)
	}
	defaults := &Defaults{
		ServiceName:    opts.ServiceName,
		Tracer:         tracerProvider.Tracer(opts.ServiceName),
		Logger:         slog.New(newRedactingHandler(logWriter)),
		Enabled:        true,
		tracerProvider: tracerProvider,
		shutdown: func(ctx context.Context) error {
			return tracerProvider.Shutdown(ctx)
		},
	}
	if err := ctx.Err(); err != nil {
		_ = defaults.Shutdown(context.Background())
		return nil, err
	}
	return defaults, nil
}

// Shutdown flushes and stops any local exporters.
func (d *Defaults) Shutdown(ctx context.Context) error {
	if d == nil || d.shutdown == nil {
		return nil
	}
	return d.shutdown(ctx)
}

// InstrumentHandler wraps an HTTP handler with OpenTelemetry tracing.
func (d *Defaults) InstrumentHandler(name string, handler http.Handler) http.Handler {
	if d == nil || !d.Enabled {
		return handler
	}
	if name == "" {
		name = "http"
	}
	return otelhttp.NewHandler(
		handler,
		name,
		otelhttp.WithTracerProvider(d.tracerProvider),
		otelhttp.WithSpanNameFormatter(func(string, *http.Request) string {
			return name
		}),
	)
}

// SnapshotMetrics returns a point-in-time snapshot of Twill runtime metrics.
func (d *Defaults) SnapshotMetrics() []*metrics.MetricSnapshot {
	if d == nil || !d.Enabled {
		return []*metrics.MetricSnapshot{}
	}
	return metrics.Snapshot()
}

func newRedactingHandler(w io.Writer) slog.Handler {
	return &redactingHandler{
		next: slog.NewJSONHandler(w, &slog.HandlerOptions{}),
	}
}

type redactingHandler struct {
	next slog.Handler
}

func (h *redactingHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.next.Enabled(ctx, level)
}

func (h *redactingHandler) Handle(ctx context.Context, record slog.Record) error {
	redacted := slog.NewRecord(record.Time, record.Level, redactString(record.Message), record.PC)
	record.Attrs(func(attr slog.Attr) bool {
		redacted.AddAttrs(redactAttr(attr))
		return true
	})
	return h.next.Handle(ctx, redacted)
}

func (h *redactingHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	redacted := make([]slog.Attr, 0, len(attrs))
	for _, attr := range attrs {
		redacted = append(redacted, redactAttr(attr))
	}
	return &redactingHandler{next: h.next.WithAttrs(redacted)}
}

func (h *redactingHandler) WithGroup(name string) slog.Handler {
	return &redactingHandler{next: h.next.WithGroup(name)}
}

func redactAttr(attr slog.Attr) slog.Attr {
	if attr.Value.Kind() == slog.KindGroup {
		children := attr.Value.Group()
		redacted := make([]slog.Attr, 0, len(children))
		for _, child := range children {
			redacted = append(redacted, redactAttr(child))
		}
		return slog.Group(attr.Key, attrsToAny(redacted)...)
	}
	if sensitiveKey(attr.Key) {
		attr.Value = slog.StringValue("<redacted>")
		return attr
	}
	switch attr.Value.Kind() {
	case slog.KindString:
		attr.Value = slog.StringValue(redactString(attr.Value.String()))
	case slog.KindDuration:
		attr.Value = slog.DurationValue(attr.Value.Duration().Round(time.Microsecond))
	}
	return attr
}

func attrsToAny(attrs []slog.Attr) []any {
	values := make([]any, 0, len(attrs))
	for _, attr := range attrs {
		values = append(values, attr)
	}
	return values
}

func sensitiveKey(key string) bool {
	key = strings.ToLower(key)
	for _, marker := range []string{
		"authorization",
		"api_key",
		"apikey",
		"password",
		"secret",
		"token",
		"credential",
		"session",
		"cookie",
	} {
		if strings.Contains(key, marker) {
			return true
		}
	}
	return false
}

func redactString(value string) string {
	fields := strings.Fields(value)
	for i, field := range fields {
		key, val, ok := strings.Cut(field, "=")
		if !ok || !sensitiveKey(key) || val == "" {
			continue
		}
		fields[i] = key + "=<redacted>"
	}
	value = strings.Join(fields, " ")
	if strings.Contains(strings.ToLower(value), "bearer ") {
		return redactBearer(value)
	}
	return value
}

func redactBearer(value string) string {
	parts := strings.Fields(value)
	for i := 0; i+1 < len(parts); i++ {
		if strings.EqualFold(parts[i], "bearer") {
			parts[i+1] = "<redacted>"
		}
	}
	return strings.Join(parts, " ")
}

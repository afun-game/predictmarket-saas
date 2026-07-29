package observability

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"strings"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	"go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.41.0"
)

// SetupTracing installs W3C trace propagation and an OTLP/HTTP exporter when
// OTEL_EXPORTER_OTLP_ENDPOINT is configured. Without an endpoint it keeps
// in-process spans enabled for local correlation without attempting export.
func SetupTracing(ctx context.Context) (func(context.Context) error, error) {
	serviceName := strings.TrimSpace(os.Getenv("OTEL_SERVICE_NAME"))
	if serviceName == "" {
		serviceName = "predictmarket-saas"
	}
	options := []trace.TracerProviderOption{
		trace.WithResource(resource.NewWithAttributes("", semconv.ServiceName(serviceName))),
		trace.WithSampler(trace.ParentBased(trace.AlwaysSample())),
	}
	endpoint := strings.TrimSpace(os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"))
	if endpoint != "" {
		exporterOptions, err := otlpOptions(endpoint)
		if err != nil {
			return nil, err
		}
		exporter, err := otlptracehttp.New(ctx, exporterOptions...)
		if err != nil {
			return nil, fmt.Errorf("create OTLP trace exporter: %w", err)
		}
		options = append(options, trace.WithBatcher(exporter))
	}
	provider := trace.NewTracerProvider(options...)
	otel.SetTracerProvider(provider)
	otel.SetTextMapPropagator(propagation.TraceContext{})
	return provider.Shutdown, nil
}

func otlpOptions(endpoint string) ([]otlptracehttp.Option, error) {
	if strings.Contains(endpoint, "://") {
		parsed, err := url.Parse(endpoint)
		if err != nil || parsed.Scheme == "" || parsed.Host == "" {
			return nil, fmt.Errorf("invalid OTEL_EXPORTER_OTLP_ENDPOINT %q", endpoint)
		}
		options := []otlptracehttp.Option{otlptracehttp.WithEndpointURL(endpoint)}
		if parsed.Scheme == "http" {
			options = append(options, otlptracehttp.WithInsecure())
		}
		return options, nil
	}
	return []otlptracehttp.Option{otlptracehttp.WithEndpoint(endpoint), otlptracehttp.WithInsecure()}, nil
}

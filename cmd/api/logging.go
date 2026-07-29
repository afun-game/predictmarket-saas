package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"strings"

	"github.com/nxsky/twill/runtime/middleware"
	"go.opentelemetry.io/otel/trace"
)

func configureLogging(output io.Writer) error {
	if output == nil {
		return errors.New("log output is required")
	}
	level, err := parseLogLevel(os.Getenv("LOG_LEVEL"))
	if err != nil {
		return err
	}
	options := &slog.HandlerOptions{Level: level}
	format := strings.ToLower(strings.TrimSpace(os.Getenv("LOG_FORMAT")))
	if format == "" {
		format = "json"
	}
	var handler slog.Handler
	switch format {
	case "json":
		handler = slog.NewJSONHandler(output, options)
	case "text":
		handler = slog.NewTextHandler(output, options)
	default:
		return errors.New("LOG_FORMAT must be json or text")
	}
	environment := strings.TrimSpace(os.Getenv("APP_ENV"))
	if environment == "" {
		environment = "development"
	}
	contextualHandler := requestContextHandler{Handler: handler}
	slog.SetDefault(slog.New(contextualHandler).With(
		"service",
		"predictmarket-saas",
		"environment",
		environment,
	))
	return nil
}

type requestContextHandler struct {
	slog.Handler
}

func (h requestContextHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.Handler.Enabled(ctx, level)
}

func (h requestContextHandler) Handle(ctx context.Context, record slog.Record) error {
	if requestID, ok := middleware.RequestIDFromContext(ctx); ok {
		record.AddAttrs(slog.String("request_id", requestID))
	}
	if span := trace.SpanContextFromContext(ctx); span.IsValid() {
		record.AddAttrs(slog.String("trace_id", span.TraceID().String()))
	}
	return h.Handler.Handle(ctx, record)
}

func (h requestContextHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return requestContextHandler{Handler: h.Handler.WithAttrs(attrs)}
}

func (h requestContextHandler) WithGroup(name string) slog.Handler {
	return requestContextHandler{Handler: h.Handler.WithGroup(name)}
}

func parseLogLevel(value string) (slog.Level, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "info":
		return slog.LevelInfo, nil
	case "debug":
		return slog.LevelDebug, nil
	case "warn", "warning":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	default:
		return slog.LevelInfo, errors.New("LOG_LEVEL must be debug, info, warn, or error")
	}
}

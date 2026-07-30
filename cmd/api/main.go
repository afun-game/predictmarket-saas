package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/afun-game/predictmarket-saas/internal/analytics"
	"github.com/afun-game/predictmarket-saas/internal/currency"
	"github.com/afun-game/predictmarket-saas/internal/event"
	"github.com/afun-game/predictmarket-saas/internal/eventsync"
	"github.com/afun-game/predictmarket-saas/internal/health"
	"github.com/afun-game/predictmarket-saas/internal/httpapi"
	"github.com/afun-game/predictmarket-saas/internal/infra"
	"github.com/afun-game/predictmarket-saas/internal/market"
	"github.com/afun-game/predictmarket-saas/internal/merchant"
	"github.com/afun-game/predictmarket-saas/internal/observability"
	"github.com/afun-game/predictmarket-saas/internal/order"
	"github.com/afun-game/predictmarket-saas/internal/reconciliation"
	"github.com/afun-game/predictmarket-saas/internal/settlement"
	"github.com/afun-game/predictmarket-saas/internal/settlementmonitor"
	"github.com/afun-game/predictmarket-saas/internal/settlementworker"
	"github.com/afun-game/predictmarket-saas/internal/sports"
	"github.com/afun-game/predictmarket-saas/internal/wallet"
	"github.com/nxsky/twill"
	"github.com/nxsky/twill/runtime/middleware"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"

	_ "github.com/jackc/pgx/v5/stdlib" // Register PostgreSQL for Twill database resources.
)

const defaultDatabaseURL = "postgres://predictmarket:password@localhost:5432/predictmarket?sslmode=disable"
const defaultRedisURL = "redis://localhost:6379/0"

const (
	serverReadHeaderTimeout = 5 * time.Second
	serverReadTimeout       = 15 * time.Second
	serverWriteTimeout      = 30 * time.Second
	serverIdleTimeout       = 60 * time.Second
	shutdownTimeout         = 30 * time.Second
)

type application struct {
	twill.Implements[twill.Main]

	merchant          twill.Ref[merchant.Service]
	event             twill.Ref[event.Service]
	eventsync         twill.Ref[eventsync.Service]
	market            twill.Ref[market.Service]
	order             twill.Ref[order.Service]
	reconciliation    twill.Ref[reconciliation.Service]
	settlementMonitor twill.Ref[settlementmonitor.Service]
	settlement        twill.Ref[settlement.Service]
	settlementWorker  twill.Ref[settlementworker.Service]
	wallet            twill.Ref[wallet.Service]
	currency          twill.Ref[currency.Service]
	sports            twill.Ref[sports.Service]
	analytics         twill.Ref[analytics.Service]
	public            twill.Listener `twill:"public"`
}

func main() {
	if err := configureLogging(os.Stdout); err != nil {
		slog.Error("configure logging", "error", err)
		os.Exit(1)
	}
	infra.RegisterRedisCacheProvider()
	infra.RegisterNATSPubSubProvider()
	infra.RegisterLockedCronProvider()
	resources, err := configureResources()
	if err != nil {
		slog.Error("configure resources", "error", err)
		os.Exit(1)
	}
	checker, err := health.New(resources.databaseURL, resources.redisURL, resources.natsURL)
	if err != nil {
		slog.Error("configure readiness checks", "error", err)
		os.Exit(1)
	}
	defer func() {
		if err := checker.Close(); err != nil {
			slog.Error("close readiness checks", "error", err)
		}
	}()
	metrics, err := observability.NewMetrics(resources.databaseURL)
	if err != nil {
		slog.Error("configure metrics", "error", err)
		os.Exit(1)
	}
	defer func() {
		if err := metrics.Close(); err != nil {
			slog.Error("close metrics", "error", err)
		}
	}()
	traceShutdown, err := observability.SetupTracing(context.Background())
	if err != nil {
		slog.Error("configure tracing", "error", err)
		os.Exit(1)
	}
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()
		if err := traceShutdown(shutdownCtx); err != nil {
			slog.Error("shutdown tracing", "error", err)
		}
	}()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := twill.Run(ctx, func(runCtx context.Context, app *application) error {
		return run(runCtx, app, checker, metrics)
	}); err != nil {
		slog.Error("run application", "error", err)
		os.Exit(1)
	}
}

func run(
	ctx context.Context,
	app *application,
	checker *health.Checker,
	metrics *observability.Metrics,
) error {
	mux := http.NewServeMux()
	mux.HandleFunc("GET "+twill.HealthzURL, checker.Liveness)
	mux.HandleFunc("GET /healthz", checker.Liveness)
	mux.HandleFunc("GET /readyz", checker.Readiness)
	mux.Handle("GET /metrics", metrics.Handler())
	api := httpapi.NewHandler(
		app.merchant.Get(),
		app.event.Get(),
		app.market.Get(),
		app.wallet.Get(),
		app.order.Get(),
		app.currency.Get(),
		os.Getenv("ADMIN_API_KEY"),
		app.sports.Get(),
		app.analytics.Get(),
	)
	api = httpapi.LimitRequestBody(httpapi.MaxRequestBodyBytes)(api)
	api = metrics.Middleware(api)
	api = httpapi.RecoverPanic(api)
	api = otelhttp.NewHandler(api, "predictmarket-api")
	api = middleware.RequestID()(api)
	mux.Handle("/", api)
	return serveHTTP(ctx, app.public, mux)
}

type resourceEndpoints struct {
	databaseURL string
	redisURL    string
	natsURL     string
}

func configureResources() (resourceEndpoints, error) {
	databaseURL := firstEnvironmentValue(
		"TWILL_TWILL_RESOURCES_PRIMARY_DB_DSN",
		"DATABASE_URL",
		defaultDatabaseURL,
	)
	if err := setResourceDSN("TWILL_TWILL_RESOURCES_PRIMARY_DB_DSN", databaseURL); err != nil {
		return resourceEndpoints{}, fmt.Errorf("configure primary database: %w", err)
	}
	redisURL := firstEnvironmentValue("REDIS_URL", "", defaultRedisURL)
	if err := setResourceDSN("TWILL_TWILL_RESOURCES_EVENT_CACHE_DSN", redisURL); err != nil {
		return resourceEndpoints{}, fmt.Errorf("configure event cache: %w", err)
	}
	if err := setResourceDSN("TWILL_TWILL_RESOURCES_CURRENCY_CACHE_DSN", redisURL); err != nil {
		return resourceEndpoints{}, fmt.Errorf("configure currency cache: %w", err)
	}
	if err := setResourceDSN("TWILL_TWILL_RESOURCES_SPORTS_CACHE_DSN", redisURL); err != nil {
		return resourceEndpoints{}, fmt.Errorf("configure sports cache: %w", err)
	}
	if err := setResourceDSN("TWILL_TWILL_RESOURCES_ANALYTICS_CACHE_DSN", redisURL); err != nil {
		return resourceEndpoints{}, fmt.Errorf("configure analytics cache: %w", err)
	}
	natsURL := firstEnvironmentValue("TWILL_TWILL_RESOURCES_EVENT_STREAM_DSN", "NATS_URL", "nats://localhost:4222")
	if err := setResourceDSN("TWILL_TWILL_RESOURCES_EVENT_STREAM_DSN", natsURL); err != nil {
		return resourceEndpoints{}, fmt.Errorf("configure event stream: %w", err)
	}
	return resourceEndpoints{databaseURL: databaseURL, redisURL: redisURL, natsURL: natsURL}, nil
}

func setResourceDSN(name, value string) error {
	if os.Getenv(name) != "" {
		return nil
	}
	return os.Setenv(name, value)
}

func firstEnvironmentValue(primary, secondary, fallback string) string {
	if value := os.Getenv(primary); value != "" {
		return value
	}
	if secondary != "" {
		if value := os.Getenv(secondary); value != "" {
			return value
		}
	}
	return fallback
}

func serveHTTP(ctx context.Context, listener twill.Listener, handler http.Handler) error {
	if middlewares := listener.MiddlewareChain(); len(middlewares) > 0 {
		handler = middleware.Chain(middlewares...)(handler)
	}
	server := &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: serverReadHeaderTimeout,
		ReadTimeout:       serverReadTimeout,
		WriteTimeout:      serverWriteTimeout,
		IdleTimeout:       serverIdleTimeout,
	}
	errorsCh := make(chan error, 1)
	go func() {
		errorsCh <- server.Serve(listener.Listener)
	}()
	select {
	case err := <-errorsCh:
		if errors.Is(err, http.ErrServerClosed) || errors.Is(err, net.ErrClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		slog.Info("shutdown signal received; draining HTTP requests")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("graceful HTTP shutdown: %w", err)
		}
		err := <-errorsCh
		if errors.Is(err, http.ErrServerClosed) || errors.Is(err, net.ErrClosed) {
			return nil
		}
		return err
	}
}

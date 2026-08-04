package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/afun-game/predictmarket-saas/internal/adminauth"
	"github.com/afun-game/predictmarket-saas/internal/adminquery"
	"github.com/afun-game/predictmarket-saas/internal/analytics"
	"github.com/afun-game/predictmarket-saas/internal/audit"
	"github.com/afun-game/predictmarket-saas/internal/callback"
	"github.com/afun-game/predictmarket-saas/internal/credentials"
	"github.com/afun-game/predictmarket-saas/internal/currency"
	"github.com/afun-game/predictmarket-saas/internal/event"
	"github.com/afun-game/predictmarket-saas/internal/eventsync"
	"github.com/afun-game/predictmarket-saas/internal/health"
	"github.com/afun-game/predictmarket-saas/internal/httpapi"
	"github.com/afun-game/predictmarket-saas/internal/infra"
	"github.com/afun-game/predictmarket-saas/internal/market"
	"github.com/afun-game/predictmarket-saas/internal/marketmaker"
	"github.com/afun-game/predictmarket-saas/internal/merchant"
	"github.com/afun-game/predictmarket-saas/internal/observability"
	"github.com/afun-game/predictmarket-saas/internal/order"
	"github.com/afun-game/predictmarket-saas/internal/parimutuel"
	"github.com/afun-game/predictmarket-saas/internal/platformuser"
	"github.com/afun-game/predictmarket-saas/internal/ratelimit"
	"github.com/afun-game/predictmarket-saas/internal/reconciliation"
	"github.com/afun-game/predictmarket-saas/internal/session"
	"github.com/afun-game/predictmarket-saas/internal/settlement"
	"github.com/afun-game/predictmarket-saas/internal/settlementmonitor"
	"github.com/afun-game/predictmarket-saas/internal/settlementworker"
	"github.com/afun-game/predictmarket-saas/internal/sports"
	"github.com/afun-game/predictmarket-saas/internal/v2auth"
	"github.com/afun-game/predictmarket-saas/internal/v2query"
	"github.com/afun-game/predictmarket-saas/internal/wallet"
	"github.com/afun-game/predictmarket-saas/web/admin"
	"github.com/afun-game/predictmarket-saas/web/hosted"
	"github.com/nxsky/twill"
	"github.com/nxsky/twill/runtime/middleware"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"

	_ "github.com/jackc/pgx/v5/stdlib" // Register PostgreSQL for Twill database resources.
)

const defaultDatabaseURL = "postgres://predictmarket:password@localhost:5432/predictmarket?sslmode=disable"
const defaultRedisURL = "redis://localhost:6379/0"

const (
	v3OrderRateLimit = 120
	v3QueryRateLimit = 600
	v3UserRateLimit  = 300
	rateLimitWindow  = time.Minute

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
	marketMaker       twill.Ref[marketmaker.Service]
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
		return run(runCtx, app, checker, metrics, resources)
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
	resources resourceEndpoints,
) error {
	mux := http.NewServeMux()
	mux.HandleFunc("GET "+twill.HealthzURL, checker.Liveness)
	mux.HandleFunc("GET /healthz", checker.Liveness)
	mux.HandleFunc("GET /readyz", checker.Readiness)
	mux.Handle("GET /metrics", metrics.Handler())
	optionalServices := []any{app.sports.Get(), app.analytics.Get(), app.settlement.Get()}
	v3Config, closeV3, enabled, err := configuredV3(resources)
	if err != nil {
		slog.Warn("V3 hosted API is disabled", "error", err)
	}
	if enabled {
		defer closeV3()
		optionalServices = append(optionalServices, v3Config)
		// Serve the embedded hosted UI at /launch so a single deployment can
		// host both the API and the sandbox trading page. The page references
		// ./styles.css and ./app.js, which resolve to the root path when opened
		// from /launch (no trailing slash), so those are registered as well.
		mux.Handle("GET /launch", hostedui.Handler())
		mux.Handle("GET /launch/", hostedui.Handler())
		mux.Handle("GET /app.js", hostedui.Handler())
		mux.Handle("GET /styles.css", hostedui.Handler())
		// Serve the embedded admin console at /admin. It shares the
		// SESSION_JWT_SECRET so it is enabled together with the V3 stack.
		adminConfig, closeAdmin, err := configuredAdmin(
			resources,
			app.settlement.Get(),
			v3Config.Sessions,
			v3Config.HostedLaunchURL,
		)
		if err != nil {
			slog.Warn("Admin console is disabled", "error", err)
		} else {
			defer closeAdmin()
			optionalServices = append(optionalServices, adminConfig)
			mux.Handle("GET /admin", adminui.Handler())
			mux.Handle("GET /admin/", adminui.Handler())
			mux.Handle("GET /admin/app.js", adminui.Handler())
			mux.Handle("GET /admin/styles.css", adminui.Handler())
			slog.Info("Admin console is enabled at /admin")
		}
		slog.Info("V3 hosted API is enabled")
	}
	api := httpapi.NewHandler(
		app.merchant.Get(),
		app.event.Get(),
		app.market.Get(),
		app.wallet.Get(),
		app.order.Get(),
		app.currency.Get(),
		os.Getenv("ADMIN_API_KEY"),
		optionalServices...,
	)
	api = httpapi.LimitRequestBody(httpapi.MaxRequestBodyBytes)(api)
	api = metrics.Middleware(api)
	api = httpapi.RecoverPanic(api)
	api = otelhttp.NewHandler(api, "predictmarket-api")
	api = middleware.RequestID()(api)
	mux.Handle("/", api)
	return serveHTTP(ctx, app.public, mux)
}

// configuredAdmin builds the admin console backend over the session JWT
// secret. It shares SESSION_JWT_SECRET with the V3 stack, so the console is
// enabled together with V3. ADMIN_USERNAME/ADMIN_PASSWORD bootstrap the first
// super-admin account when no admin account exists yet.
func configuredAdmin(
	resources resourceEndpoints,
	settlementService settlement.Service,
	sessions *session.Manager,
	hostedLaunchURL string,
) (httpapi.AdminConfig, func(), error) {
	sessionSecret := strings.TrimSpace(os.Getenv("SESSION_JWT_SECRET"))
	if sessionSecret == "" {
		return httpapi.AdminConfig{}, func() {}, errors.New("SESSION_JWT_SECRET is required for the admin console")
	}
	database, err := sql.Open("pgx", resources.databaseURL)
	if err != nil {
		return httpapi.AdminConfig{}, func() {}, fmt.Errorf("open admin database: %w", err)
	}
	manager, err := adminauth.NewManagerFromEncodedSecret(
		adminauth.NewPostgresRepository(database),
		adminauth.NewPostgresActionLog(database),
		sessionSecret,
	)
	if err != nil {
		_ = database.Close()
		return httpapi.AdminConfig{}, func() {}, err
	}
	if username := strings.TrimSpace(os.Getenv("ADMIN_USERNAME")); username != "" {
		if err := manager.EnsureBootstrap(context.Background(), username, os.Getenv("ADMIN_PASSWORD")); err != nil {
			slog.Warn("admin bootstrap account was not created", "error", err)
		}
	}
	config := httpapi.AdminConfig{
		Accounts:        manager,
		Queries:         adminquery.New(database),
		PlatformUsers:   platformuser.NewPostgresRepository(database),
		Settlement:      settlementService,
		Parimutuel:      parimutuel.NewServiceWithRepository(parimutuel.NewPostgresRepository(database)),
		Sessions:        sessions,
		HostedLaunchURL: hostedLaunchURL,
	}
	return config, func() { _ = database.Close() }, nil
}

func configuredV3(resources resourceEndpoints) (httpapi.V3Config, func(), bool, error) {
	encryptionKey := strings.TrimSpace(os.Getenv("MERCHANT_SECRET_ENCRYPTION_KEY"))
	sessionSecret := strings.TrimSpace(os.Getenv("SESSION_JWT_SECRET"))
	hostedLaunchURL := strings.TrimSpace(os.Getenv("HOSTED_UI_URL"))
	if encryptionKey == "" && sessionSecret == "" && hostedLaunchURL == "" {
		return httpapi.V3Config{}, func() {}, false, nil
	}
	if encryptionKey == "" || sessionSecret == "" || hostedLaunchURL == "" {
		return httpapi.V3Config{}, func() {}, false, errors.New("MERCHANT_SECRET_ENCRYPTION_KEY, SESSION_JWT_SECRET, and HOSTED_UI_URL must be configured together")
	}
	database, err := sql.Open("pgx", resources.databaseURL)
	if err != nil {
		return httpapi.V3Config{}, func() {}, false, fmt.Errorf("open V3 database: %w", err)
	}
	redisStore, err := session.NewRedisStore(resources.redisURL)
	if err != nil {
		_ = database.Close()
		return httpapi.V3Config{}, func() {}, false, fmt.Errorf("open V3 Redis store: %w", err)
	}
	manager, err := session.NewManagerFromEncodedSecret(redisStore, sessionSecret)
	if err != nil {
		_ = redisStore.Close()
		_ = database.Close()
		return httpapi.V3Config{}, func() {}, false, err
	}
	authenticator, err := v2auth.NewAuthenticator(database, encryptionKey)
	if err != nil {
		_ = redisStore.Close()
		_ = database.Close()
		return httpapi.V3Config{}, func() {}, false, err
	}
	allowPrivateCallbackURLs := boolEnvironmentValue("V3_ALLOW_PRIVATE_CALLBACK_URLS")
	callbackService, err := callback.NewWithDB(database, encryptionKey, allowPrivateCallbackURLs)
	if err != nil {
		_ = redisStore.Close()
		_ = database.Close()
		return httpapi.V3Config{}, func() {}, false, err
	}
	if err := callbackService.Init(context.Background()); err != nil {
		_ = redisStore.Close()
		_ = database.Close()
		return httpapi.V3Config{}, func() {}, false, fmt.Errorf("start callback worker: %w", err)
	}
	protector, err := credentials.NewProtector(encryptionKey)
	if err != nil {
		_ = redisStore.Close()
		_ = database.Close()
		return httpapi.V3Config{}, func() {}, false, err
	}
	seamless, err := callback.NewSeamlessCoordinator(database, protector, callbackService, allowPrivateCallbackURLs)
	if err != nil {
		_ = redisStore.Close()
		_ = database.Close()
		return httpapi.V3Config{}, func() {}, false, err
	}
	orderLimiter, err := ratelimit.NewRedisLimiterFromURL(resources.redisURL, v3RateLimitFromEnvironment("V3_ORDER_RATE_LIMIT", v3OrderRateLimit), rateLimitWindow)
	if err != nil {
		_ = redisStore.Close()
		_ = database.Close()
		return httpapi.V3Config{}, func() {}, false, err
	}
	queryLimiter, err := ratelimit.NewRedisLimiterFromURL(resources.redisURL, v3RateLimitFromEnvironment("V3_QUERY_RATE_LIMIT", v3QueryRateLimit), rateLimitWindow)
	if err != nil {
		_ = orderLimiter.Close()
		_ = redisStore.Close()
		_ = database.Close()
		return httpapi.V3Config{}, func() {}, false, err
	}
	userLimiter, err := ratelimit.NewRedisLimiterFromURL(resources.redisURL, v3RateLimitFromEnvironment("V3_USER_RATE_LIMIT", v3UserRateLimit), rateLimitWindow)
	if err != nil {
		_ = queryLimiter.Close()
		_ = orderLimiter.Close()
		_ = redisStore.Close()
		_ = database.Close()
		return httpapi.V3Config{}, func() {}, false, err
	}
	config := httpapi.V3Config{
		Authenticator:        authenticator,
		Sessions:             manager,
		PlatformUsers:        platformuser.NewPostgresRepository(database),
		Queries:              v2query.New(database),
		Callbacks:            callbackService,
		Seamless:             seamless,
		HostedLaunchURL:      hostedLaunchURL,
		Audit:                audit.NewPostgresStore(database),
		MerchantOrderLimiter: orderLimiter,
		MerchantQueryLimiter: queryLimiter,
		UserSessionLimiter:   userLimiter,
	}
	closeResources := func() {
		if err := userLimiter.Close(); err != nil {
			slog.Error("close V3 user rate limiter", "error", err)
		}
		if err := queryLimiter.Close(); err != nil {
			slog.Error("close V3 query rate limiter", "error", err)
		}
		if err := orderLimiter.Close(); err != nil {
			slog.Error("close V3 order rate limiter", "error", err)
		}
		if err := redisStore.Close(); err != nil {
			slog.Error("close V3 Redis store", "error", err)
		}
		if err := database.Close(); err != nil {
			slog.Error("close V3 database", "error", err)
		}
	}
	return config, closeResources, true, nil
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

func boolEnvironmentValue(name string) bool {
	value := strings.ToLower(strings.TrimSpace(os.Getenv(name)))
	return value == "1" || value == "true" || value == "yes"
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

// v3RateLimitFromEnvironment reads a positive integer rate limit override.
func v3RateLimitFromEnvironment(name string, fallback int) int {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value <= 0 {
		slog.Warn("invalid V3 rate limit override, using default", "variable", name, "value", raw, "fallback", fallback)
		return fallback
	}
	return value
}

package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"go.opentelemetry.io/otel/trace"

	"api-manager/internal/api"
	"api-manager/internal/catalog"
	"api-manager/internal/config"
	"api-manager/internal/gateway"
	"api-manager/internal/httpx"
	"api-manager/internal/observability"
	"api-manager/internal/plugin"
	"api-manager/internal/publicweb"
	"api-manager/internal/ratelimit"
	"api-manager/internal/stack"
	"api-manager/internal/store"
	"api-manager/internal/upstream"
	"api-manager/internal/user"
	"api-manager/internal/web"
)

func main() {
	if len(os.Args) == 2 && os.Args[1] == "--healthcheck" {
		client := &http.Client{Timeout: 2 * time.Second}
		address := os.Getenv("HTTP_ADDR")
		if address == "" {
			address = ":8080"
		}
		_, port, err := net.SplitHostPort(address)
		if err != nil || port == "" || strings.ContainsAny(port, "/ ") {
			os.Exit(1)
		}
		// #nosec G704 -- the URL is fixed to loopback and the port came from SplitHostPort.
		response, err := client.Get("http://127.0.0.1:" + port + "/health/live")
		if err != nil {
			os.Exit(1)
		}
		_ = response.Body.Close()
		if response.StatusCode != http.StatusOK {
			os.Exit(1)
		}
		return
	}
	cfg, err := config.Load()
	if err != nil {
		slog.Error("load configuration failed", "error", err)
		os.Exit(1)
	}
	if cfg.ObservabilityStackEnabled && cfg.OTLPEndpoint == "" {
		cfg.OTELEnabled = true
		cfg.OTLPEndpoint = "127.0.0.1:4317"
		cfg.OTLPInsecure = true // OTLP is bound to loopback in the bundled Tempo process.
	}
	bootstrapLogger := newLogger(cfg.LogLevel, nil)
	if err := cfg.Validate(); err != nil {
		bootstrapLogger.Error("invalid security configuration", "error", err)
		os.Exit(1)
	}
	observabilityHub, err := observability.NewHub(observability.HubOptions{Directory: cfg.ObservabilityDir, MaxLogs: cfg.ObservabilityMaxLogs, MaxTraces: cfg.ObservabilityMaxTraces, MaxFileBytes: cfg.ObservabilityFileBytes})
	if err != nil {
		bootstrapLogger.Error("initialize embedded observability failed", "error", err)
		os.Exit(1)
	}
	defer func() { _ = observabilityHub.Close() }()
	logger := newLogger(cfg.LogLevel, observabilityHub)
	upstreamCredentials, err := upstream.Parse(cfg.UpstreamCredentials)
	if err != nil {
		logger.Error("invalid upstream configuration", "error", err)
		os.Exit(1)
	}
	rootCtx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	var bundledStack *stack.Stack
	if cfg.ObservabilityStackEnabled {
		_, port, splitErr := net.SplitHostPort(cfg.HTTPAddr)
		if splitErr != nil {
			logger.Error("full observability stack requires HTTP_ADDR with a numeric port", "error", splitErr)
			os.Exit(1)
		}
		bundledStack, err = stack.Start(rootCtx, stack.Options{Directory: cfg.ObservabilityDir, APIPort: port, MetricsToken: cfg.MetricsToken})
		if err != nil {
			logger.Error("start bundled observability stack failed", "error", err)
			os.Exit(1)
		}
		defer bundledStack.Close()
	}
	shutdownTracing, err := observability.InitTracing(rootCtx, observability.TraceConfig{Enabled: cfg.OTELEnabled, ServiceName: cfg.OTELServiceName, Endpoint: cfg.OTLPEndpoint, Insecure: cfg.OTLPInsecure}, observabilityHub)
	if err != nil {
		logger.Error("initialize OpenTelemetry failed", "error", err)
		os.Exit(1)
	}
	defer func() { _ = shutdownTracing(context.Background()) }()
	metrics := observability.NewMetrics()

	activeStore, closeStore := setupStore(rootCtx, cfg, logger)
	defer closeStore()
	limiter, closeLimiter := setupLimiter(rootCtx, cfg, logger)
	defer closeLimiter()

	plugins := plugin.NewRegistry()
	defer func() { _ = plugins.Close(context.Background()) }()
	pluginManager := plugin.NewManagerWithOptions(activeStore, plugins, cfg.PluginDir, cfg.PluginMaxBytes, logger, plugin.Options{DatabaseWritesEnabled: cfg.PluginDatabaseWrites})
	if err := pluginManager.LoadEnabled(rootCtx); err != nil {
		logger.Error("some managed WASM plugins failed to load", "error", err)
	}
	userService := user.NewService(activeStore)
	userService.SetSessionTTL(cfg.UserSessionTTL)
	if err := userService.EnsureDefaults(); err != nil {
		logger.Error("initialize RBAC defaults failed", "error", err)
		os.Exit(1)
	}
	if err := userService.EnsureInitialAdmin(cfg.AdminUsername, cfg.AdminPassword); err != nil {
		logger.Error("initialize administrator failed", "error", err)
		os.Exit(1)
	}
	admin := api.NewAdminWithUserManagementAndPluginManager(activeStore, plugins, userService, pluginManager, logger)
	admin.SetCredentialEncryptionKey(cfg.CredentialEncryptionKey)
	admin.SetProductionMode(cfg.ProductionMode)
	admin.SetPluginLibrary(plugin.NewLibrary(cfg.PluginLibraryDir, pluginManager))
	admin.SetObservability(observabilityHub, metrics)
	authHandler := user.NewHTTP(userService)
	gatewayHandler := gateway.NewWithMetrics(activeStore, plugins, limiter, logger, metrics)
	gatewayHandler.SetUpstreamCredentials(upstreamCredentials)
	gatewayHandler.SetProductionMode(cfg.ProductionMode)

	ready := func(ctx context.Context) error {
		if health, ok := activeStore.(store.HealthStore); ok {
			if err := health.Ping(ctx); err != nil {
				return err
			}
		}
		if health, ok := limiter.(ratelimit.Health); ok {
			if err := health.Ping(ctx); err != nil {
				return err
			}
		}
		return nil
	}

	mux := http.NewServeMux()
	web.MountAdministration(mux, cfg.AdminPath, admin, authHandler)
	mux.Handle("/api/", gatewayHandler)
	publicExport := catalog.New(activeStore, cfg.PublicAPIBaseURL)
	mux.Handle("/public/v1/catalog", publicExport)
	publicHandler, err := publicweb.New(cfg.PublicUIDir, publicExport)
	if err != nil {
		logger.Error("load public documentation failed", "error", err)
		os.Exit(1)
	}
	mux.Handle("/", publicHandler)
	mux.HandleFunc("/health/live", func(w http.ResponseWriter, _ *http.Request) {
		writeHealth(w, http.StatusOK, `{"status":"ok"}`)
	})
	mux.HandleFunc("/health/ready", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if err := ready(ctx); err != nil {
			logger.Error("readiness check failed", "error", err)
			writeHealth(w, http.StatusServiceUnavailable, `{"status":"not_ready"}`)
			return
		}
		writeHealth(w, http.StatusOK, `{"status":"ready"}`)
	})
	mux.Handle("/metrics", httpx.ProtectMetrics(metrics, cfg.MetricsToken))

	handler := httpx.LimitRequestBody(cfg.MaxBodyBytes, cfg.PluginMaxBytes+(64<<10), mux)
	handler = httpx.ThrottleAdmin(limiter, handler)
	handler = loggingMiddleware(logger, handler)
	handler = metrics.Middleware(handler)
	handler = httpx.CORS(cfg.CORSOrigins, handler)
	handler = httpx.SecurityHeaders(handler)
	handler = httpx.Recover(logger, handler)
	handler = httpx.RequestID(handler)
	handler = observability.Middleware(handler)

	server := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    64 << 10,
	}

	serverErr := make(chan error, 1)

	go func() {
		logger.Info("server starting", "addr", cfg.HTTPAddr, "store", storeName(activeStore), "limiter", limiterName(limiter))
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErr <- err
			stop()
		}
	}()

	var stackErrors <-chan error
	if bundledStack != nil {
		stackErrors = bundledStack.Errors()
	}
	select {
	case <-rootCtx.Done():
	case err := <-stackErrors:
		logger.Error("bundled observability component stopped", "error", err)
		stop()
	case err := <-serverErr:
		logger.Error("server stopped unexpectedly", "error", err)
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancel()

	if err := server.Shutdown(shutdownCtx); err != nil {
		logger.Error("graceful shutdown failed", "error", err)
		os.Exit(1)
	}
	logger.Info("server stopped")
}

func setupStore(ctx context.Context, cfg config.Config, logger *slog.Logger) (store.Store, func()) {
	if cfg.PostgresDSN == "" {
		memory := store.NewMemory()
		return memory, memory.Close
	}
	for attempt := 1; attempt <= 15; attempt++ {
		postgres, err := store.NewPostgres(ctx, cfg.PostgresDSN)
		if err == nil {
			return postgres, postgres.Close
		}
		logger.Warn("postgres not ready; retrying", "attempt", attempt, "error", err)
		if !sleepOrStop(ctx, 2*time.Second) {
			break
		}
	}
	logger.Error("postgres initialization failed")
	os.Exit(1)
	return nil, func() {}
}

func setupLimiter(ctx context.Context, cfg config.Config, logger *slog.Logger) (ratelimit.Limiter, func()) {
	if !cfg.UseRedis {
		memory := ratelimit.NewMemory()
		return memory, func() { _ = memory.Close() }
	}
	for attempt := 1; attempt <= 15; attempt++ {
		redisLimiter, err := ratelimit.NewRedisWithTLS(ctx, cfg.RedisAddr, cfg.RedisUsername, cfg.RedisPassword, cfg.RedisDB, cfg.RedisTLS)
		if err == nil {
			return redisLimiter, func() { _ = redisLimiter.Close() }
		}
		logger.Warn("redis not ready; retrying", "attempt", attempt, "error", err)
		if !sleepOrStop(ctx, 2*time.Second) {
			break
		}
	}
	logger.Error("redis initialization failed")
	os.Exit(1)
	return nil, func() {}
}

func sleepOrStop(ctx context.Context, duration time.Duration) bool {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func storeName(s store.Store) string {
	if _, ok := s.(*store.Postgres); ok {
		return "postgres"
	}
	return "memory"
}

func limiterName(l ratelimit.Limiter) string {
	if _, ok := l.(*ratelimit.Redis); ok {
		return "redis"
	}
	return "memory"
}

func writeHealth(w http.ResponseWriter, status int, body string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(body))
}

func newLogger(level string, sink io.Writer) *slog.Logger {
	var slogLevel slog.Level
	switch level {
	case "debug":
		slogLevel = slog.LevelDebug
	case "warn":
		slogLevel = slog.LevelWarn
	case "error":
		slogLevel = slog.LevelError
	default:
		slogLevel = slog.LevelInfo
	}
	output := io.Writer(os.Stdout)
	if sink != nil {
		output = io.MultiWriter(os.Stdout, sink)
	}
	return slog.New(slog.NewJSONHandler(output, &slog.HandlerOptions{Level: slogLevel}))
}

func loggingMiddleware(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		next.ServeHTTP(w, r)
		spanContext := trace.SpanContextFromContext(r.Context())
		logger.Info("http request", "request_id", httpx.RequestIDFromContext(r.Context()), "trace_id", spanContext.TraceID().String(), "span_id", spanContext.SpanID().String(), "method", r.Method, "path", r.URL.Path, "duration_ms", time.Since(started).Milliseconds())
	})
}

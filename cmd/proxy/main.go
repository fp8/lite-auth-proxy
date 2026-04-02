package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/fp8/lite-auth-proxy/internal/config"
	"github.com/fp8/lite-auth-proxy/internal/logging"
	"github.com/fp8/lite-auth-proxy/internal/proxy"
	"github.com/fp8/lite-auth-proxy/internal/startup"
)

var (
	// Version is set via ldflags during build
	Version = "1.1.1"
)

const (
	defaultConfigPath = "/config/config.toml"
)

func main() {
	configPath, healthcheckOnly, err := parseFlags(os.Args[1:], os.Stderr)
	if err != nil {
		os.Exit(2)
	}

	// Initialize logger
	logMode := logging.GetLogModeFromEnv()
	logLevel := logging.GetLogLevelFromEnv()
	logger := logging.InitLogger(logMode, logLevel)

	logger.Info("Starting lite-auth-proxy",
		"version", Version,
		"log_mode", logMode,
	)

	// Load configuration
	cfg, err := config.Load(configPath)
	if err != nil {
		logger.Error("Failed to load configuration",
			"error", err,
			"config_path", configPath,
		)
		os.Exit(1)
	}

	logger.Info("Configuration loaded successfully", "config_path", configPath)

	if healthcheckOnly {
		if err := runHealthCheck(cfg); err != nil {
			logger.Error("Health check failed", "error", err)
			os.Exit(1)
		}
		logger.Info("Health check OK")
		return
	}

	// Log configuration summary
	logConfigSummary(logger, cfg)

	handler, deps, err := proxy.NewHandlerWithDeps(cfg, logger)
	if err != nil {
		logger.Error("Failed to initialize proxy handler", "error", err)
		os.Exit(1)
	}

	// Step 05: pre-load throttle rules from env var before serving traffic.
	if cfg.Admin.Enabled && deps != nil && deps.RuleStore != nil {
		loader := startup.NewRuleLoader(deps.RuleStore, deps.RateLimiters, logger)
		if err := loader.Load(); err != nil {
			// Non-fatal: proxy starts normally; ShockGuard re-applies rules on next cycle.
			logger.Warn("startup rule load failed", "error", err)
		}
	}

	server := &http.Server{
		Addr:              fmt.Sprintf(":%d", cfg.Server.Port),
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	shutdownErrors := make(chan error, 1)
	go func() {
		shutdownErrors <- runServer(server, logger)
	}()

	awaitShutdown(server, cfg.Server.ShutdownTimeoutSecs, logger)

	if err := <-shutdownErrors; err != nil {
		logger.Error("Server exited with error", "error", err)
	}

	// Stop background goroutines (rule store cleanup, RPM reset).
	if deps != nil && deps.StopFn != nil {
		deps.StopFn()
	}
}

func parseFlags(args []string, output io.Writer) (string, bool, error) {
	fs := flag.NewFlagSet("lite-auth-proxy", flag.ContinueOnError)
	fs.SetOutput(output)
	configPath := fs.String("config", defaultConfigPath, "Path to configuration file")
	healthcheckOnly := fs.Bool("healthcheck", false, "Run configured health check and exit")
	if err := fs.Parse(args); err != nil {
		return "", false, err
	}
	return *configPath, *healthcheckOnly, nil
}

func runHealthCheck(cfg *config.Config) error {
	if cfg.Server.HealthCheck.Target == "" {
		return nil
	}

	client := &http.Client{Timeout: 5 * time.Second}
	req, err := http.NewRequest(http.MethodGet, cfg.Server.HealthCheck.Target, nil)
	if err != nil {
		return fmt.Errorf("invalid health check target: %w", err)
	}

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("health check request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, resp.Body)

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusBadRequest {
		return fmt.Errorf("health check returned status %d", resp.StatusCode)
	}

	return nil
}

// logConfigSummary logs a summary of the loaded configuration
func logConfigSummary(logger *slog.Logger, cfg *config.Config) {
	logger.Info("Server configuration",
		"port", cfg.Server.Port,
		"target_url", cfg.Server.TargetURL,
		"strip_prefix", cfg.Server.StripPrefix,
		"health_check_path", cfg.Server.HealthCheck.Path,
		"health_check_target", cfg.Server.HealthCheck.Target,
		"shutdown_timeout_secs", cfg.Server.ShutdownTimeoutSecs,
	)

	logger.Info("Security configuration",
		"rate_limit_enabled", cfg.Security.RateLimit.Enabled,
		"requests_per_min", cfg.Security.RateLimit.RequestsPerMin,
		"ban_for_min", cfg.Security.RateLimit.BanForMin,
	)

	// Auth modes
	authModes := []string{}
	if cfg.Auth.JWT.Enabled {
		authModes = append(authModes, "JWT")
	}
	if cfg.Auth.APIKey.Enabled {
		authModes = append(authModes, "API-Key")
	}

	logger.Info("Authentication configuration",
		"enabled_methods", fmt.Sprintf("%v", authModes),
		"header_prefix", cfg.Auth.HeaderPrefix,
	)

	if cfg.Auth.JWT.Enabled {
		logger.Info("JWT authentication enabled",
			"issuer", cfg.Auth.JWT.Issuer,
			"audience", cfg.Auth.JWT.Audience,
			"tolerance_secs", cfg.Auth.JWT.ToleranceSecs,
			"cache_ttl_mins", cfg.Auth.JWT.CacheTTLMins,
			"filters_count", len(cfg.Auth.JWT.Filters),
			"mappings_count", len(cfg.Auth.JWT.Mappings),
		)
	}

	if cfg.Auth.APIKey.Enabled {
		logger.Info("API-Key authentication enabled",
			"header_name", cfg.Auth.APIKey.Name,
			"has_value", cfg.Auth.APIKey.Value != "",
			"payload_count", len(cfg.Auth.APIKey.Payload),
		)
	}

	logger.Info("Path filtering",
		"include_paths", cfg.Server.IncludePaths,
		"exclude_paths", cfg.Server.ExcludePaths,
	)
}

func runServer(server *http.Server, logger *slog.Logger) error {
	logger.Info("Proxy server listening", "addr", server.Addr)
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}

func awaitShutdown(server *http.Server, timeoutSeconds int, logger *slog.Logger) {
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)

	<-signals
	logger.Info("Shutdown signal received")

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeoutSeconds)*time.Second)
	defer cancel()

	if err := server.Shutdown(ctx); err != nil {
		logger.Error("Graceful shutdown failed", "error", err)
	}
}

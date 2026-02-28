package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/fp8/lite-auth-proxy/internal/config"
	"github.com/fp8/lite-auth-proxy/internal/logging"
	"github.com/fp8/lite-auth-proxy/internal/proxy"
)

var (
	// Version is set via ldflags during build
	Version = "1.0.2"
)

const (
	defaultConfigPath = "configs/config.toml"
)

func main() {
	// Parse command-line flags
	configPath := flag.String("config", defaultConfigPath, "Path to configuration file")
	flag.Parse()

	// Initialize logger
	logMode := logging.GetLogModeFromEnv()
	logLevel := logging.GetLogLevelFromEnv()
	logger := logging.InitLogger(logMode, logLevel)

	logger.Info("Starting lite-auth-proxy",
		"version", Version,
		"log_mode", logMode,
	)

	// Load configuration
	cfg, err := config.Load(*configPath)
	if err != nil {
		logger.Error("Failed to load configuration",
			"error", err,
			"config_path", *configPath,
		)
		os.Exit(1)
	}

	logger.Info("Configuration loaded successfully", "config_path", *configPath)

	// Log configuration summary
	logConfigSummary(logger, cfg)

	handler, err := proxy.NewHandler(cfg, logger)
	if err != nil {
		logger.Error("Failed to initialize proxy handler", "error", err)
		os.Exit(1)
	}

	server := &http.Server{
		Addr:              fmt.Sprintf(":%d", cfg.Server.Port),
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
	}

	shutdownErrors := make(chan error, 1)
	go func() {
		shutdownErrors <- runServer(server, logger)
	}()

	awaitShutdown(server, cfg.Server.ShutdownTimeoutSecs, logger)

	if err := <-shutdownErrors; err != nil {
		logger.Error("Server exited with error", "error", err)
	}
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

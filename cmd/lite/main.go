package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/fp8/lite-auth-proxy/internal/config"
	"github.com/fp8/lite-auth-proxy/internal/logging"
	"github.com/fp8/lite-auth-proxy/internal/plugin"
	"github.com/fp8/lite-auth-proxy/internal/proxy"
)

var (
	// Version is set via ldflags during build
	Version = "1.2.0"
)

const (
	defaultConfigPath = "/config/config.toml"
)

func main() {
	configPath, healthcheckOnly, err := parseFlags(os.Args[1:], os.Stderr)
	if err != nil {
		os.Exit(2)
	}

	logMode := logging.GetLogModeFromEnv()
	logLevel := logging.GetLogLevelFromEnv()
	logger := logging.InitLogger(logMode, logLevel)

	logger.Info("Starting lite-auth-proxy",
		"version", Version,
		"build", buildVariant(),
		"log_mode", logMode,
	)

	cfg, err := config.Load(configPath)
	if err != nil {
		logger.Error("Failed to load configuration", "error", err, "config_path", configPath)
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

	handler, deps, err := proxy.NewHandlerWithDeps(cfg, logger)
	if err != nil {
		logger.Error("Failed to initialize proxy handler", "error", err)
		os.Exit(1)
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
		logger.Info("Proxy server listening", "addr", server.Addr)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			shutdownErrors <- err
		} else {
			shutdownErrors <- nil
		}
	}()

	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)
	<-signals
	logger.Info("Shutdown signal received")

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(cfg.Server.ShutdownTimeoutSecs)*time.Second)
	defer cancel()
	if err := server.Shutdown(ctx); err != nil {
		logger.Error("Graceful shutdown failed", "error", err)
	}

	if err := <-shutdownErrors; err != nil {
		logger.Error("Server exited with error", "error", err)
	}

	if deps != nil && deps.StopFn != nil {
		deps.StopFn()
	}
}

func buildVariant() string {
	if len(plugin.All()) == 0 {
		return "lite"
	}
	return "custom"
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

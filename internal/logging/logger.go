package logging

import (
	"log/slog"
	"os"
	"strings"
)

// LogMode represents the logging mode (development or production)
type LogMode string

const (
	ModeDevelopment LogMode = "development"
	ModeProduction  LogMode = "production"
)

// InitLogger initializes the structured logger with the specified mode and level
func InitLogger(mode LogMode, level slog.Level) *slog.Logger {
	var handler slog.Handler

	opts := &slog.HandlerOptions{
		Level: level,
	}

	switch mode {
	case ModeProduction:
		// JSON handler for production (Google Cloud Logging compatible)
		handler = slog.NewJSONHandler(os.Stdout, opts)
	case ModeDevelopment:
		fallthrough
	default:
		// Text handler for development (human-readable)
		handler = slog.NewTextHandler(os.Stdout, opts)
	}

	logger := slog.New(handler)
	slog.SetDefault(logger)

	return logger
}

// GetLogModeFromEnv determines the log mode from environment variables
func GetLogModeFromEnv() LogMode {
	env := strings.ToLower(os.Getenv("PROXY_LOG_MODE"))
	switch env {
	case "production", "prod":
		return ModeProduction
	case "development", "dev":
		return ModeDevelopment
	default:
		// Check for common production environment indicators
		if os.Getenv("K_SERVICE") != "" || os.Getenv("GOOGLE_CLOUD_PROJECT") != "" {
			return ModeProduction
		}
		return ModeDevelopment
	}
}

// GetLogLevelFromEnv determines the log level from environment variables
func GetLogLevelFromEnv() slog.Level {
	level := strings.ToUpper(os.Getenv("PROXY_LOG_LEVEL"))
	switch level {
	case "DEBUG":
		return slog.LevelDebug
	case "INFO":
		return slog.LevelInfo
	case "WARN", "WARNING":
		return slog.LevelWarn
	case "ERROR":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// WithComponent returns a logger with a component field
func WithComponent(logger *slog.Logger, component string) *slog.Logger {
	return logger.With("component", component)
}

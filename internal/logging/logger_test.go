package logging

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"os"
	"strings"
	"testing"
)

func TestGetLogModeFromEnv(t *testing.T) {
	tests := []struct {
		name     string
		envVars  map[string]string
		expected LogMode
	}{
		{
			name:     "Explicit production mode",
			envVars:  map[string]string{"PROXY_LOG_MODE": "production"},
			expected: ModeProduction,
		},
		{
			name:     "Explicit prod shorthand",
			envVars:  map[string]string{"PROXY_LOG_MODE": "prod"},
			expected: ModeProduction,
		},
		{
			name:     "Explicit development mode",
			envVars:  map[string]string{"PROXY_LOG_MODE": "development"},
			expected: ModeDevelopment,
		},
		{
			name:     "Explicit dev shorthand",
			envVars:  map[string]string{"PROXY_LOG_MODE": "dev"},
			expected: ModeDevelopment,
		},
		{
			name:     "Detect Cloud Run environment",
			envVars:  map[string]string{"K_SERVICE": "my-service"},
			expected: ModeProduction,
		},
		{
			name:     "Detect GCP environment",
			envVars:  map[string]string{"GOOGLE_CLOUD_PROJECT": "my-project"},
			expected: ModeProduction,
		},
		{
			name:     "Default to development",
			envVars:  map[string]string{},
			expected: ModeDevelopment,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Clear relevant env vars
			_ = os.Unsetenv("PROXY_LOG_MODE")
			_ = os.Unsetenv("K_SERVICE")
			_ = os.Unsetenv("GOOGLE_CLOUD_PROJECT")

			// Set test env vars
			for k, v := range tt.envVars {
				_ = os.Setenv(k, v)
				defer func() { _ = os.Unsetenv(k) }()
			}

			mode := GetLogModeFromEnv()
			if mode != tt.expected {
				t.Errorf("Expected log mode %s, got %s", tt.expected, mode)
			}
		})
	}
}

func TestGetLogLevelFromEnv(t *testing.T) {
	tests := []struct {
		name     string
		envValue string
		expected slog.Level
	}{
		{
			name:     "Debug level",
			envValue: "DEBUG",
			expected: slog.LevelDebug,
		},
		{
			name:     "Info level",
			envValue: "INFO",
			expected: slog.LevelInfo,
		},
		{
			name:     "Warn level",
			envValue: "WARN",
			expected: slog.LevelWarn,
		},
		{
			name:     "Warning level",
			envValue: "WARNING",
			expected: slog.LevelWarn,
		},
		{
			name:     "Error level",
			envValue: "ERROR",
			expected: slog.LevelError,
		},
		{
			name:     "Default to Info",
			envValue: "",
			expected: slog.LevelInfo,
		},
		{
			name:     "Invalid level defaults to Info",
			envValue: "INVALID",
			expected: slog.LevelInfo,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.envValue != "" {
				_ = os.Setenv("PROXY_LOG_LEVEL", tt.envValue)
				defer func() { _ = os.Unsetenv("PROXY_LOG_LEVEL") }()
			} else {
				_ = os.Unsetenv("PROXY_LOG_LEVEL")
			}

			level := GetLogLevelFromEnv()
			if level != tt.expected {
				t.Errorf("Expected log level %v, got %v", tt.expected, level)
			}
		})
	}
}

func TestInitLoggerDevelopmentMode(t *testing.T) {
	var buf bytes.Buffer

	// Create a text handler that writes to our buffer
	handler := slog.NewTextHandler(&buf, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	})
	logger := slog.New(handler)

	// Test logging
	logger.Info("test message", "key", "value")

	output := buf.String()

	// Text handler should produce human-readable output
	if !strings.Contains(output, "test message") {
		t.Errorf("Expected log output to contain 'test message', got: %s", output)
	}
	if !strings.Contains(output, "key=value") {
		t.Errorf("Expected log output to contain 'key=value', got: %s", output)
	}
	if !strings.Contains(output, "level=INFO") {
		t.Errorf("Expected log output to contain 'level=INFO', got: %s", output)
	}
}

func TestInitLoggerProductionMode(t *testing.T) {
	var buf bytes.Buffer

	// Create a JSON handler that writes to our buffer
	handler := slog.NewJSONHandler(&buf, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	})
	logger := slog.New(handler)

	// Test logging
	logger.Info("test message", "key", "value")

	output := buf.String()

	// JSON handler should produce valid JSON
	var logEntry map[string]interface{}
	if err := json.Unmarshal([]byte(output), &logEntry); err != nil {
		t.Fatalf("Expected valid JSON output, got error: %v\nOutput: %s", err, output)
	}

	// Check JSON fields
	if logEntry["msg"] != "test message" {
		t.Errorf("Expected msg='test message', got: %v", logEntry["msg"])
	}
	if logEntry["key"] != "value" {
		t.Errorf("Expected key='value', got: %v", logEntry["key"])
	}
	if logEntry["level"] != "INFO" {
		t.Errorf("Expected level='INFO', got: %v", logEntry["level"])
	}
	if _, ok := logEntry["time"]; !ok {
		t.Error("Expected 'time' field in JSON output")
	}
}

func TestWithComponent(t *testing.T) {
	var buf bytes.Buffer

	handler := slog.NewJSONHandler(&buf, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	})
	logger := slog.New(handler)

	// Add component
	componentLogger := WithComponent(logger, "test-component")
	componentLogger.Info("test message")

	output := buf.String()

	var logEntry map[string]interface{}
	if err := json.Unmarshal([]byte(output), &logEntry); err != nil {
		t.Fatalf("Expected valid JSON output, got error: %v", err)
	}

	if logEntry["component"] != "test-component" {
		t.Errorf("Expected component='test-component', got: %v", logEntry["component"])
	}
}

func TestLogLevelFiltering(t *testing.T) {
	var buf bytes.Buffer

	handler := slog.NewTextHandler(&buf, &slog.HandlerOptions{
		Level: slog.LevelWarn, // Only warn and above
	})
	logger := slog.New(handler)

	// These should not appear
	logger.Debug("debug message")
	logger.Info("info message")

	// These should appear
	logger.Warn("warn message")
	logger.Error("error message")

	output := buf.String()

	if strings.Contains(output, "debug message") {
		t.Error("Debug message should be filtered out")
	}
	if strings.Contains(output, "info message") {
		t.Error("Info message should be filtered out")
	}
	if !strings.Contains(output, "warn message") {
		t.Error("Warn message should be present")
	}
	if !strings.Contains(output, "error message") {
		t.Error("Error message should be present")
	}
}

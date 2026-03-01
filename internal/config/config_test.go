package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Helper function to create a temporary config file for testing
func createTempConfig(t *testing.T, content string) string {
	t.Helper()
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.toml")
	if err := os.WriteFile(configPath, []byte(content), 0644); err != nil {
		t.Fatalf("Failed to create temp config: %v", err)
	}
	return configPath
}

func TestConfigParsing(t *testing.T) {
	configContent := `
[server]
port = 9090
target_url = "http://example.com"
strip_prefix = "/api"
include_paths = ["/app/*"]
exclude_paths = ["/healthz", "/metrics"]
shutdown_timeout_secs = 15

[server.health_check]
path = "/health"
target = "http://localhost:8080/status"

[security.rate_limit]
enabled = true
requests_per_min = 120
ban_for_min = 10

[auth]
header_prefix = "X-MY-AUTH-"

[auth.jwt]
enabled = true
issuer = "https://accounts.google.com"
audience = "my-app"
tolerance_secs = 60
cache_ttl_mins = 720

[auth.jwt.filters]
email_verified = "true"

[auth.jwt.mappings]
email = "USER-EMAIL"
sub = "USER-ID"

[auth.api_key]
enabled = false
name = "X-SECRET"
value = "secret123"
`

	configPath := createTempConfig(t, configContent)
	cfg, err := Load(configPath)
	if err != nil {
		t.Fatalf("Failed to load config: %v", err)
	}

	// Server config
	if cfg.Server.Port != 9090 {
		t.Errorf("Expected port 9090, got %d", cfg.Server.Port)
	}
	if cfg.Server.TargetURL != "http://example.com" {
		t.Errorf("Expected target_url http://example.com, got %s", cfg.Server.TargetURL)
	}
	if cfg.Server.StripPrefix != "/api" {
		t.Errorf("Expected strip_prefix /api, got %s", cfg.Server.StripPrefix)
	}
	if len(cfg.Server.IncludePaths) != 1 || cfg.Server.IncludePaths[0] != "/app/*" {
		t.Errorf("Unexpected include_paths: %v", cfg.Server.IncludePaths)
	}
	if len(cfg.Server.ExcludePaths) != 2 {
		t.Errorf("Expected 2 exclude paths, got %d", len(cfg.Server.ExcludePaths))
	}
	if cfg.Server.ShutdownTimeoutSecs != 15 {
		t.Errorf("Expected shutdown timeout 15, got %d", cfg.Server.ShutdownTimeoutSecs)
	}

	// Health check config
	if cfg.Server.HealthCheck.Path != "/health" {
		t.Errorf("Expected health check path /health, got %s", cfg.Server.HealthCheck.Path)
	}
	if cfg.Server.HealthCheck.Target != "http://localhost:8080/status" {
		t.Errorf("Expected health check target http://localhost:8080/status, got %s", cfg.Server.HealthCheck.Target)
	}

	// Rate limit config
	if !cfg.Security.RateLimit.Enabled {
		t.Error("Expected rate limit to be enabled")
	}
	if cfg.Security.RateLimit.RequestsPerMin != 120 {
		t.Errorf("Expected 120 requests per minute, got %d", cfg.Security.RateLimit.RequestsPerMin)
	}
	if cfg.Security.RateLimit.BanForMin != 10 {
		t.Errorf("Expected ban for 10 minutes, got %d", cfg.Security.RateLimit.BanForMin)
	}

	// Auth config
	if cfg.Auth.HeaderPrefix != "X-MY-AUTH-" {
		t.Errorf("Expected header prefix X-MY-AUTH-, got %s", cfg.Auth.HeaderPrefix)
	}

	// JWT config
	if !cfg.Auth.JWT.Enabled {
		t.Error("Expected JWT to be enabled")
	}
	if cfg.Auth.JWT.Issuer != "https://accounts.google.com" {
		t.Errorf("Expected issuer https://accounts.google.com, got %s", cfg.Auth.JWT.Issuer)
	}
	if cfg.Auth.JWT.Audience != "my-app" {
		t.Errorf("Expected audience my-app, got %s", cfg.Auth.JWT.Audience)
	}
	if cfg.Auth.JWT.ToleranceSecs != 60 {
		t.Errorf("Expected clock tolerance 60, got %d", cfg.Auth.JWT.ToleranceSecs)
	}
	if cfg.Auth.JWT.CacheTTLMins != 720 {
		t.Errorf("Expected JWKS cache TTL 720, got %d", cfg.Auth.JWT.CacheTTLMins)
	}

	// API Key config
	if cfg.Auth.APIKey.Enabled {
		t.Error("Expected API key to be disabled")
	}
}

func TestEnvVarSubstitution(t *testing.T) {
	_ = os.Setenv("TEST_PROJECT_ID", "my-project-123")
	_ = os.Setenv("TEST_API_SECRET", "super-secret-key")
	defer func() { _ = os.Unsetenv("TEST_PROJECT_ID") }()
	defer func() { _ = os.Unsetenv("TEST_API_SECRET") }()

	configContent := `
[server]
port = 8888
target_url = "http://localhost:8080"

[auth.jwt]
enabled = true
issuer = "https://securetoken.google.com/{{ENV.TEST_PROJECT_ID}}"
audience = "{{ENV.TEST_PROJECT_ID}}"

[auth.api_key]
enabled = true
name = "X-API-KEY"
value = "{{ENV.TEST_API_SECRET}}"
`

	configPath := createTempConfig(t, configContent)
	cfg, err := Load(configPath)
	if err != nil {
		t.Fatalf("Failed to load config: %v", err)
	}

	if cfg.Auth.JWT.Issuer != "https://securetoken.google.com/my-project-123" {
		t.Errorf("Env substitution failed for issuer: %s", cfg.Auth.JWT.Issuer)
	}
	if cfg.Auth.JWT.Audience != "my-project-123" {
		t.Errorf("Env substitution failed for audience: %s", cfg.Auth.JWT.Audience)
	}
	if cfg.Auth.APIKey.Value != "super-secret-key" {
		t.Errorf("Env substitution failed for API key: %s", cfg.Auth.APIKey.Value)
	}
}

func TestEnvVarOverrides(t *testing.T) {
	_ = os.Setenv("PROXY_SERVER_PORT", "7777")
	_ = os.Setenv("PROXY_SERVER_TARGET_URL", "http://overridden.com")
	_ = os.Setenv("PROXY_SERVER_INCLUDE_PATHS", "/api/*, /admin/*")
	_ = os.Setenv("PROXY_SERVER_EXCLUDE_PATHS", "/healthz, /metrics")
	_ = os.Setenv("PROXY_SECURITY_RATE_LIMIT_ENABLED", "false")
	_ = os.Setenv("PROXY_SECURITY_RATE_LIMIT_BAN_FOR_MIN", "15")
	_ = os.Setenv("PROXY_AUTH_JWT_FILTERS_HD", "farport.co")
	_ = os.Setenv("PROXY_AUTH_JWT_TOLERANCE_SECS", "45")
	_ = os.Setenv("PROXY_AUTH_JWT_CACHE_TTL_MINS", "30")
	_ = os.Setenv("PROXY_AUTH_API_KEY_PAYLOAD_SERVICE", "internal")
	defer func() { _ = os.Unsetenv("PROXY_SERVER_PORT") }()
	defer func() { _ = os.Unsetenv("PROXY_SERVER_TARGET_URL") }()
	defer func() { _ = os.Unsetenv("PROXY_SERVER_INCLUDE_PATHS") }()
	defer func() { _ = os.Unsetenv("PROXY_SERVER_EXCLUDE_PATHS") }()
	defer func() { _ = os.Unsetenv("PROXY_SECURITY_RATE_LIMIT_ENABLED") }()
	defer func() { _ = os.Unsetenv("PROXY_SECURITY_RATE_LIMIT_BAN_FOR_MIN") }()
	defer func() { _ = os.Unsetenv("PROXY_AUTH_JWT_FILTERS_HD") }()
	defer func() { _ = os.Unsetenv("PROXY_AUTH_JWT_TOLERANCE_SECS") }()
	defer func() { _ = os.Unsetenv("PROXY_AUTH_JWT_CACHE_TTL_MINS") }()
	defer func() { _ = os.Unsetenv("PROXY_AUTH_API_KEY_PAYLOAD_SERVICE") }()

	configContent := `
[server]
port = 8888
target_url = "http://localhost:8080"

[security.rate_limit]
enabled = true
ban_for_min = 5

[auth.jwt]
enabled = true
issuer = "https://example.com"
audience = "test"

[auth.jwt.filters]
hd = "trybuyme.com"

[auth.api_key]
enabled = true
name = "X-API-KEY"
value = "secret"
`

	configPath := createTempConfig(t, configContent)
	cfg, err := Load(configPath)
	if err != nil {
		t.Fatalf("Failed to load config: %v", err)
	}

	if cfg.Server.Port != 7777 {
		t.Errorf("Expected port override to 7777, got %d", cfg.Server.Port)
	}
	if cfg.Server.TargetURL != "http://overridden.com" {
		t.Errorf("Expected target_url override, got %s", cfg.Server.TargetURL)
	}
	if len(cfg.Server.IncludePaths) != 2 || cfg.Server.IncludePaths[0] != "/api/*" || cfg.Server.IncludePaths[1] != "/admin/*" {
		t.Errorf("Expected include_paths override, got %v", cfg.Server.IncludePaths)
	}
	if len(cfg.Server.ExcludePaths) != 2 || cfg.Server.ExcludePaths[0] != "/healthz" || cfg.Server.ExcludePaths[1] != "/metrics" {
		t.Errorf("Expected exclude_paths override, got %v", cfg.Server.ExcludePaths)
	}
	if cfg.Security.RateLimit.Enabled {
		t.Error("Expected rate limit to be disabled via env override")
	}
	if cfg.Security.RateLimit.BanForMin != 15 {
		t.Errorf("Expected ban_for_min override to 15, got %d", cfg.Security.RateLimit.BanForMin)
	}
	if cfg.Auth.JWT.Filters["hd"] != "farport.co" {
		t.Errorf("Expected JWT filter override for hd to be farport.co, got %s", cfg.Auth.JWT.Filters["hd"])
	}
	if cfg.Auth.JWT.ToleranceSecs != 45 {
		t.Errorf("Expected JWT tolerance override to 45, got %d", cfg.Auth.JWT.ToleranceSecs)
	}
	if cfg.Auth.JWT.CacheTTLMins != 30 {
		t.Errorf("Expected JWT cache TTL override to 30, got %d", cfg.Auth.JWT.CacheTTLMins)
	}
	if cfg.Auth.APIKey.Payload["service"] != "internal" {
		t.Errorf("Expected API key payload override for service to be internal, got %s", cfg.Auth.APIKey.Payload["service"])
	}
}

func TestDefaultValues(t *testing.T) {
	configContent := `
[server]
target_url = "http://localhost:8080"

[auth.jwt]
enabled = true
issuer = "https://example.com"
audience = "test"
`

	configPath := createTempConfig(t, configContent)
	cfg, err := Load(configPath)
	if err != nil {
		t.Fatalf("Failed to load config: %v", err)
	}

	// Check defaults
	if cfg.Server.Port != 8888 {
		t.Errorf("Expected default port 8888, got %d", cfg.Server.Port)
	}
	if cfg.Server.ShutdownTimeoutSecs != 10 {
		t.Errorf("Expected default shutdown timeout 10, got %d", cfg.Server.ShutdownTimeoutSecs)
	}
	if cfg.Server.HealthCheck.Path != "/healthz" {
		t.Errorf("Expected default health check path /healthz, got %s", cfg.Server.HealthCheck.Path)
	}
	if len(cfg.Server.IncludePaths) != 1 || cfg.Server.IncludePaths[0] != "/*" {
		t.Errorf("Expected default include paths [/*], got %v", cfg.Server.IncludePaths)
	}
	if cfg.Security.RateLimit.RequestsPerMin != 60 {
		t.Errorf("Expected default RPM 60, got %d", cfg.Security.RateLimit.RequestsPerMin)
	}
	if cfg.Security.RateLimit.BanForMin != 5 {
		t.Errorf("Expected default ban duration 5, got %d", cfg.Security.RateLimit.BanForMin)
	}
	if cfg.Auth.HeaderPrefix != "X-AUTH-" {
		t.Errorf("Expected default header prefix X-AUTH-, got %s", cfg.Auth.HeaderPrefix)
	}
	if cfg.Auth.JWT.ToleranceSecs != 30 {
		t.Errorf("Expected default clock tolerance 30, got %d", cfg.Auth.JWT.ToleranceSecs)
	}
	if cfg.Auth.JWT.CacheTTLMins != 1440 {
		t.Errorf("Expected default JWKS cache TTL 1440, got %d", cfg.Auth.JWT.CacheTTLMins)
	}
}

func TestBothAuthMethodsDisabledError(t *testing.T) {
	configContent := `
[server]
target_url = "http://localhost:8080"

[auth.jwt]
enabled = false

[auth.api_key]
enabled = false
`

	configPath := createTempConfig(t, configContent)
	_, err := Load(configPath)
	if err == nil {
		t.Error("Expected error when both auth methods are disabled")
	}
	expectedErr := "at least one authentication method (JWT or API-Key) must be enabled"
	if err != nil && !strings.Contains(err.Error(), expectedErr) {
		t.Errorf("Expected error containing %q, got %q", expectedErr, err.Error())
	}
}

func TestJWTRequiredFieldsValidation(t *testing.T) {
	tests := []struct {
		name        string
		config      string
		expectedErr string
	}{
		{
			name: "Missing JWT issuer",
			config: `
[server]
target_url = "http://localhost:8080"

[auth.jwt]
enabled = true
audience = "test"
`,
			expectedErr: "JWT issuer is required when JWT auth is enabled",
		},
		{
			name: "Missing JWT audience",
			config: `
[server]
target_url = "http://localhost:8080"

[auth.jwt]
enabled = true
issuer = "https://example.com"
`,
			expectedErr: "JWT audience is required when JWT auth is enabled",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			configPath := createTempConfig(t, tt.config)
			_, err := Load(configPath)
			if err == nil {
				t.Error("Expected validation error but got none")
			}
			if err != nil && !strings.Contains(err.Error(), tt.expectedErr) {
				t.Errorf("Expected error containing %q, got %q", tt.expectedErr, err.Error())
			}
		})
	}
}

func TestAPIKeyRequiredFieldsValidation(t *testing.T) {
	configContent := `
[server]
target_url = "http://localhost:8080"

[auth.api_key]
enabled = true
name = "X-API-KEY"
`

	configPath := createTempConfig(t, configContent)
	_, err := Load(configPath)
	if err == nil {
		t.Error("Expected validation error for missing API key value")
	}
	if err != nil && !strings.Contains(err.Error(), "API key value is required") {
		t.Errorf("Unexpected error message: %v", err)
	}
}

func TestInvalidPortValidation(t *testing.T) {
	_ = os.Setenv("PROXY_SERVER_PORT", "99999")
	defer func() { _ = os.Unsetenv("PROXY_SERVER_PORT") }()

	configContent := `
[server]
target_url = "http://localhost:8080"

[auth.jwt]
enabled = true
issuer = "https://example.com"
audience = "test"
`

	configPath := createTempConfig(t, configContent)
	_, err := Load(configPath)
	if err == nil {
		t.Error("Expected validation error for invalid port")
	}
	if err != nil && !strings.Contains(err.Error(), "server port must be between 1 and 65535") {
		t.Errorf("Unexpected error message: %v", err)
	}
}

func TestHealthCheckConfiguration(t *testing.T) {
	tests := []struct {
		name           string
		config         string
		expectedPath   string
		expectedTarget string
	}{
		{
			name: "With proxy target",
			config: `
[server]
target_url = "http://localhost:8080"

[server.health_check]
path = "/custom-health"
target = "http://localhost:8080/status"

[auth.jwt]
enabled = true
issuer = "https://example.com"
audience = "test"
`,
			expectedPath:   "/custom-health",
			expectedTarget: "http://localhost:8080/status",
		},
		{
			name: "Without proxy target (local mode)",
			config: `
[server]
target_url = "http://localhost:8080"

[server.health_check]
path = "/healthz"

[auth.jwt]
enabled = true
issuer = "https://example.com"
audience = "test"
`,
			expectedPath:   "/healthz",
			expectedTarget: "",
		},
		{
			name: "Default health check",
			config: `
[server]
target_url = "http://localhost:8080"

[auth.jwt]
enabled = true
issuer = "https://example.com"
audience = "test"
`,
			expectedPath:   "/healthz",
			expectedTarget: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			configPath := createTempConfig(t, tt.config)
			cfg, err := Load(configPath)
			if err != nil {
				t.Fatalf("Failed to load config: %v", err)
			}

			if cfg.Server.HealthCheck.Path != tt.expectedPath {
				t.Errorf("Expected health check path %s, got %s", tt.expectedPath, cfg.Server.HealthCheck.Path)
			}
			if cfg.Server.HealthCheck.Target != tt.expectedTarget {
				t.Errorf("Expected health check target %s, got %s", tt.expectedTarget, cfg.Server.HealthCheck.Target)
			}
		})
	}
}

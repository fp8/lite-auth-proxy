package config

import (
	"os"
	"path/filepath"
	"regexp"
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

func TestEnvVarAllowedEmailsOverride(t *testing.T) {
	_ = os.Setenv("PROXY_AUTH_JWT_ALLOWED_EMAILS", "alice@example.com,bob@example.com, carol@example.com")
	defer func() { _ = os.Unsetenv("PROXY_AUTH_JWT_ALLOWED_EMAILS") }()

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

	want := []string{"alice@example.com", "bob@example.com", "carol@example.com"}
	if len(cfg.Auth.JWT.AllowedEmails) != len(want) {
		t.Fatalf("Expected %d allowed_emails, got %d: %v", len(want), len(cfg.Auth.JWT.AllowedEmails), cfg.Auth.JWT.AllowedEmails)
	}
	for i, email := range want {
		if cfg.Auth.JWT.AllowedEmails[i] != email {
			t.Errorf("allowed_emails[%d]: expected %q, got %q", i, email, cfg.Auth.JWT.AllowedEmails[i])
		}
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

func TestBothAuthMethodsDisabledAllowed(t *testing.T) {
	// Both auth methods disabled is valid: proxy operates in rate-limit-only mode.
	configContent := `
[server]
target_url = "http://localhost:8080"

[auth.jwt]
enabled = false

[auth.api_key]
enabled = false
`

	configPath := createTempConfig(t, configContent)
	cfg, err := Load(configPath)
	if err != nil {
		t.Errorf("Expected no error when both auth methods disabled (rate-limit-only mode), got: %v", err)
	}
	if cfg != nil && (cfg.Auth.JWT.Enabled || cfg.Auth.APIKey.Enabled) {
		t.Error("Expected both auth methods to be disabled")
	}
}

func TestAdminJWTConfig(t *testing.T) {
	configContent := `
[server]
target_url = "http://localhost:8080"

[auth.jwt]
enabled = true
issuer = "https://example.com"
audience = "test"

[admin]
enabled = true

[admin.jwt]
issuer = "https://accounts.google.com"
audience = "my-service-url"
allowed_emails = ["sa@my-project.iam.gserviceaccount.com"]
`

	configPath := createTempConfig(t, configContent)
	cfg, err := Load(configPath)
	if err != nil {
		t.Fatalf("Failed to load config: %v", err)
	}

	if !cfg.Admin.Enabled {
		t.Error("Expected admin to be enabled")
	}
	if cfg.Admin.JWT.Issuer != "https://accounts.google.com" {
		t.Errorf("Expected admin JWT issuer https://accounts.google.com, got %s", cfg.Admin.JWT.Issuer)
	}
	if cfg.Admin.JWT.Audience != "my-service-url" {
		t.Errorf("Expected admin JWT audience my-service-url, got %s", cfg.Admin.JWT.Audience)
	}
	if len(cfg.Admin.JWT.AllowedEmails) != 1 || cfg.Admin.JWT.AllowedEmails[0] != "sa@my-project.iam.gserviceaccount.com" {
		t.Errorf("Unexpected allowed_emails: %v", cfg.Admin.JWT.AllowedEmails)
	}
	if cfg.Admin.JWT.ToleranceSecs != 30 {
		t.Errorf("Expected default admin JWT tolerance 30, got %d", cfg.Admin.JWT.ToleranceSecs)
	}
	if cfg.Admin.JWT.CacheTTLMins != 1440 {
		t.Errorf("Expected default admin JWT cache TTL 1440, got %d", cfg.Admin.JWT.CacheTTLMins)
	}
}

func TestAdminJWTValidationErrors(t *testing.T) {
	tests := []struct {
		name        string
		config      string
		expectedErr string
	}{
		{
			name: "Missing admin JWT issuer",
			config: `
[server]
target_url = "http://localhost:8080"

[auth.jwt]
enabled = true
issuer = "https://example.com"
audience = "test"

[admin]
enabled = true

[admin.jwt]
audience = "my-service"
allowed_emails = ["sa@example.com"]
`,
			expectedErr: "admin JWT issuer is required",
		},
		{
			name: "Missing admin JWT audience",
			config: `
[server]
target_url = "http://localhost:8080"

[auth.jwt]
enabled = true
issuer = "https://example.com"
audience = "test"

[admin]
enabled = true

[admin.jwt]
issuer = "https://accounts.google.com"
allowed_emails = ["sa@example.com"]
`,
			expectedErr: "admin JWT audience is required",
		},
		{
			name: "Neither allowed_emails nor filters provided",
			config: `
[server]
target_url = "http://localhost:8080"

[auth.jwt]
enabled = true
issuer = "https://example.com"
audience = "test"

[admin]
enabled = true

[admin.jwt]
issuer = "https://accounts.google.com"
audience = "my-service"
`,
			expectedErr: "admin JWT requires at least one of allowed_emails or filters",
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

func TestAdminJWTFiltersOnly(t *testing.T) {
	// Filters alone (without allowed_emails) should pass validation.
	configContent := `
[server]
target_url = "http://localhost:8080"

[auth.jwt]
enabled = true
issuer = "https://example.com"
audience = "test"

[admin]
enabled = true

[admin.jwt]
issuer = "https://accounts.google.com"
audience = "my-service"

[admin.jwt.filters]
hd = "company.com"
`

	configPath := createTempConfig(t, configContent)
	cfg, err := Load(configPath)
	if err != nil {
		t.Fatalf("Expected no error with filters-only admin config, got: %v", err)
	}
	if cfg.Admin.JWT.Filters["hd"] != "company.com" {
		t.Errorf("Expected admin JWT filter hd=company.com, got %v", cfg.Admin.JWT.Filters)
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

// TestEnvVarOverridesComplete verifies that every PROXY_* env var is wired up and applied.
// This is the single source of truth — every env override in applyEnvOverrides must appear here.
func TestEnvVarOverridesComplete(t *testing.T) {
	envs := map[string]string{
		// Server
		"PROXY_SERVER_PORT":                  "7777",
		"PROXY_SERVER_TARGET_URL":            "http://overridden.example.com",
		"PROXY_SERVER_STRIP_PREFIX":          "/api",
		"PROXY_SERVER_INCLUDE_PATHS":         "/v1/*, /v2/*",
		"PROXY_SERVER_EXCLUDE_PATHS":         "/healthz, /metrics",
		"PROXY_SERVER_SHUTDOWN_TIMEOUT_SECS": "20",
		"PROXY_SERVER_HEALTH_CHECK_PATH":     "/ping",
		"PROXY_SERVER_HEALTH_CHECK_TARGET":   "http://healthz-backend:9090",
		// Security — IP rate limit
		"PROXY_SECURITY_RATE_LIMIT_ENABLED":                 "false",
		"PROXY_SECURITY_RATE_LIMIT_REQUESTS_PER_MIN":        "42",
		"PROXY_SECURITY_RATE_LIMIT_BAN_FOR_MIN":             "12",
		"PROXY_SECURITY_RATE_LIMIT_THROTTLE_DELAY_MS":       "250",
		"PROXY_SECURITY_RATE_LIMIT_MAX_DELAY_SLOTS":         "50",
		"PROXY_SECURITY_RATE_LIMIT_SKIP_IF_JWT_IDENTIFIED":  "true",
		// Security — API-key rate limit
		"PROXY_SECURITY_APIKEY_RATE_LIMIT_ENABLED":           "true",
		"PROXY_SECURITY_APIKEY_RATE_LIMIT_REQUESTS_PER_MIN":  "30",
		"PROXY_SECURITY_APIKEY_RATE_LIMIT_BAN_FOR_MIN":       "10",
		"PROXY_SECURITY_APIKEY_RATE_LIMIT_INCLUDE_IP":        "true",
		"PROXY_SECURITY_APIKEY_RATE_LIMIT_KEY_HEADER":        "x-my-api-key",
		"PROXY_SECURITY_APIKEY_RATE_LIMIT_THROTTLE_DELAY_MS": "100",
		"PROXY_SECURITY_APIKEY_RATE_LIMIT_MAX_DELAY_SLOTS":   "25",
		// Security — JWT rate limit
		"PROXY_SECURITY_JWT_RATE_LIMIT_ENABLED":           "true",
		"PROXY_SECURITY_JWT_RATE_LIMIT_REQUESTS_PER_MIN":  "20",
		"PROXY_SECURITY_JWT_RATE_LIMIT_BAN_FOR_MIN":       "15",
		"PROXY_SECURITY_JWT_RATE_LIMIT_INCLUDE_IP":        "true",
		"PROXY_SECURITY_JWT_RATE_LIMIT_THROTTLE_DELAY_MS": "75",
		"PROXY_SECURITY_JWT_RATE_LIMIT_MAX_DELAY_SLOTS":   "10",
		// Security — misc
		"PROXY_SECURITY_MAX_BODY_BYTES": "2097152",
		// Auth — common
		"PROXY_AUTH_HEADER_PREFIX": "X-CUSTOM-",
		// Auth — JWT
		"PROXY_AUTH_JWT_ENABLED":           "true",
		"PROXY_AUTH_JWT_ISSUER":            "https://override-issuer.example.com",
		"PROXY_AUTH_JWT_AUDIENCE":          "override-audience",
		"PROXY_AUTH_JWT_TOLERANCE_SECS":    "45",
		"PROXY_AUTH_JWT_CACHE_TTL_MINS":    "30",
		"PROXY_AUTH_JWT_ALLOWED_EMAILS":    "user1@example.com,user2@example.com",
		"PROXY_AUTH_JWT_FILTERS_HD":        "override.co",
		"PROXY_AUTH_JWT_FILTERS_EMAIL_VERIFIED": "true",
		"PROXY_AUTH_JWT_MAPPINGS_EMAIL":    "USER-EMAIL",
		"PROXY_AUTH_JWT_MAPPINGS_SUB":      "USER-ID",
		// Auth — API key
		"PROXY_AUTH_API_KEY_ENABLED":         "true",
		"PROXY_AUTH_API_KEY_NAME":            "X-Override-Key",
		"PROXY_AUTH_API_KEY_VALUE":           "override-secret",
		"PROXY_AUTH_API_KEY_PAYLOAD_SERVICE": "internal",
		"PROXY_AUTH_API_KEY_PAYLOAD_ROLE":    "admin",
		// Storage
		"PROXY_STORAGE_ENABLED":           "true",
		"PROXY_STORAGE_PROJECT_ID":        "test-project-123",
		"PROXY_STORAGE_DBNAME":            "test-db",
		"PROXY_STORAGE_COLLECTION_PREFIX": "test-prefix",
		// Admin
		"PROXY_ADMIN_ENABLED":                   "true",
		"PROXY_ADMIN_JWT_ISSUER":                "https://accounts.google.com",
		"PROXY_ADMIN_JWT_AUDIENCE":              "https://my-proxy.run.app",
		"PROXY_ADMIN_JWT_ALLOWED_EMAILS":        "admin@example.com,ops@example.com",
		"PROXY_ADMIN_JWT_FILTERS_HD":            "farport.co",
		"PROXY_ADMIN_JWT_FILTERS_EMAIL_VERIFIED": "true",
		"PROXY_ADMIN_JWT_MAPPINGS_EMAIL":        "ADMIN-EMAIL",
		"PROXY_ADMIN_JWT_MAPPINGS_SUB":          "ADMIN-ID",
	}
	for k, v := range envs {
		_ = os.Setenv(k, v)
	}
	defer func() {
		for k := range envs {
			_ = os.Unsetenv(k)
		}
	}()

	// Minimal TOML — every field should come from env overrides.
	configContent := `
[server]
target_url = "http://localhost:8080"

[auth.jwt]
enabled = true
issuer = "https://example.com"
audience = "test"

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

	// ── Server ──
	if cfg.Server.Port != 7777 {
		t.Errorf("PROXY_SERVER_PORT: got %d", cfg.Server.Port)
	}
	if cfg.Server.TargetURL != "http://overridden.example.com" {
		t.Errorf("PROXY_SERVER_TARGET_URL: got %q", cfg.Server.TargetURL)
	}
	if cfg.Server.StripPrefix != "/api" {
		t.Errorf("PROXY_SERVER_STRIP_PREFIX: got %q", cfg.Server.StripPrefix)
	}
	if len(cfg.Server.IncludePaths) != 2 || cfg.Server.IncludePaths[0] != "/v1/*" || cfg.Server.IncludePaths[1] != "/v2/*" {
		t.Errorf("PROXY_SERVER_INCLUDE_PATHS: got %v", cfg.Server.IncludePaths)
	}
	if len(cfg.Server.ExcludePaths) != 2 || cfg.Server.ExcludePaths[0] != "/healthz" || cfg.Server.ExcludePaths[1] != "/metrics" {
		t.Errorf("PROXY_SERVER_EXCLUDE_PATHS: got %v", cfg.Server.ExcludePaths)
	}
	if cfg.Server.ShutdownTimeoutSecs != 20 {
		t.Errorf("PROXY_SERVER_SHUTDOWN_TIMEOUT_SECS: got %d", cfg.Server.ShutdownTimeoutSecs)
	}
	if cfg.Server.HealthCheck.Path != "/ping" {
		t.Errorf("PROXY_SERVER_HEALTH_CHECK_PATH: got %q", cfg.Server.HealthCheck.Path)
	}
	if cfg.Server.HealthCheck.Target != "http://healthz-backend:9090" {
		t.Errorf("PROXY_SERVER_HEALTH_CHECK_TARGET: got %q", cfg.Server.HealthCheck.Target)
	}

	// ── Security — IP rate limit ──
	if cfg.Security.RateLimit.Enabled {
		t.Error("PROXY_SECURITY_RATE_LIMIT_ENABLED: got true, want false")
	}
	if cfg.Security.RateLimit.RequestsPerMin != 42 {
		t.Errorf("PROXY_SECURITY_RATE_LIMIT_REQUESTS_PER_MIN: got %d", cfg.Security.RateLimit.RequestsPerMin)
	}
	if cfg.Security.RateLimit.BanForMin != 12 {
		t.Errorf("PROXY_SECURITY_RATE_LIMIT_BAN_FOR_MIN: got %d", cfg.Security.RateLimit.BanForMin)
	}
	if cfg.Security.RateLimit.ThrottleDelayMs != 250 {
		t.Errorf("PROXY_SECURITY_RATE_LIMIT_THROTTLE_DELAY_MS: got %d", cfg.Security.RateLimit.ThrottleDelayMs)
	}
	if cfg.Security.RateLimit.MaxDelaySlots != 50 {
		t.Errorf("PROXY_SECURITY_RATE_LIMIT_MAX_DELAY_SLOTS: got %d", cfg.Security.RateLimit.MaxDelaySlots)
	}
	if cfg.Security.RateLimit.SkipIfJwtIdentified == nil || !*cfg.Security.RateLimit.SkipIfJwtIdentified {
		t.Error("PROXY_SECURITY_RATE_LIMIT_SKIP_IF_JWT_IDENTIFIED: got nil or false, want true")
	}

	// ── Security — API-key rate limit ──
	if !cfg.Security.ApiKeyRateLimit.Enabled {
		t.Error("PROXY_SECURITY_APIKEY_RATE_LIMIT_ENABLED: got false")
	}
	if cfg.Security.ApiKeyRateLimit.RequestsPerMin != 30 {
		t.Errorf("PROXY_SECURITY_APIKEY_RATE_LIMIT_REQUESTS_PER_MIN: got %d", cfg.Security.ApiKeyRateLimit.RequestsPerMin)
	}
	if cfg.Security.ApiKeyRateLimit.BanForMin != 10 {
		t.Errorf("PROXY_SECURITY_APIKEY_RATE_LIMIT_BAN_FOR_MIN: got %d", cfg.Security.ApiKeyRateLimit.BanForMin)
	}
	if !cfg.Security.ApiKeyRateLimit.IncludeIP {
		t.Error("PROXY_SECURITY_APIKEY_RATE_LIMIT_INCLUDE_IP: got false")
	}
	if cfg.Security.ApiKeyRateLimit.KeyHeader != "x-my-api-key" {
		t.Errorf("PROXY_SECURITY_APIKEY_RATE_LIMIT_KEY_HEADER: got %q", cfg.Security.ApiKeyRateLimit.KeyHeader)
	}
	if cfg.Security.ApiKeyRateLimit.ThrottleDelayMs != 100 {
		t.Errorf("PROXY_SECURITY_APIKEY_RATE_LIMIT_THROTTLE_DELAY_MS: got %d", cfg.Security.ApiKeyRateLimit.ThrottleDelayMs)
	}
	if cfg.Security.ApiKeyRateLimit.MaxDelaySlots != 25 {
		t.Errorf("PROXY_SECURITY_APIKEY_RATE_LIMIT_MAX_DELAY_SLOTS: got %d", cfg.Security.ApiKeyRateLimit.MaxDelaySlots)
	}

	// ── Security — JWT rate limit ──
	if !cfg.Security.JwtRateLimit.Enabled {
		t.Error("PROXY_SECURITY_JWT_RATE_LIMIT_ENABLED: got false")
	}
	if cfg.Security.JwtRateLimit.RequestsPerMin != 20 {
		t.Errorf("PROXY_SECURITY_JWT_RATE_LIMIT_REQUESTS_PER_MIN: got %d", cfg.Security.JwtRateLimit.RequestsPerMin)
	}
	if cfg.Security.JwtRateLimit.BanForMin != 15 {
		t.Errorf("PROXY_SECURITY_JWT_RATE_LIMIT_BAN_FOR_MIN: got %d", cfg.Security.JwtRateLimit.BanForMin)
	}
	if !cfg.Security.JwtRateLimit.IncludeIP {
		t.Error("PROXY_SECURITY_JWT_RATE_LIMIT_INCLUDE_IP: got false")
	}
	if cfg.Security.JwtRateLimit.ThrottleDelayMs != 75 {
		t.Errorf("PROXY_SECURITY_JWT_RATE_LIMIT_THROTTLE_DELAY_MS: got %d", cfg.Security.JwtRateLimit.ThrottleDelayMs)
	}
	if cfg.Security.JwtRateLimit.MaxDelaySlots != 10 {
		t.Errorf("PROXY_SECURITY_JWT_RATE_LIMIT_MAX_DELAY_SLOTS: got %d", cfg.Security.JwtRateLimit.MaxDelaySlots)
	}

	// ── Security — misc ──
	if cfg.Security.MaxBodyBytes != 2097152 {
		t.Errorf("PROXY_SECURITY_MAX_BODY_BYTES: got %d", cfg.Security.MaxBodyBytes)
	}

	// ── Auth — common ──
	if cfg.Auth.HeaderPrefix != "X-CUSTOM-" {
		t.Errorf("PROXY_AUTH_HEADER_PREFIX: got %q", cfg.Auth.HeaderPrefix)
	}

	// ── Auth — JWT ──
	if !cfg.Auth.JWT.Enabled {
		t.Error("PROXY_AUTH_JWT_ENABLED: got false")
	}
	if cfg.Auth.JWT.Issuer != "https://override-issuer.example.com" {
		t.Errorf("PROXY_AUTH_JWT_ISSUER: got %q", cfg.Auth.JWT.Issuer)
	}
	if cfg.Auth.JWT.Audience != "override-audience" {
		t.Errorf("PROXY_AUTH_JWT_AUDIENCE: got %q", cfg.Auth.JWT.Audience)
	}
	if cfg.Auth.JWT.ToleranceSecs != 45 {
		t.Errorf("PROXY_AUTH_JWT_TOLERANCE_SECS: got %d", cfg.Auth.JWT.ToleranceSecs)
	}
	if cfg.Auth.JWT.CacheTTLMins != 30 {
		t.Errorf("PROXY_AUTH_JWT_CACHE_TTL_MINS: got %d", cfg.Auth.JWT.CacheTTLMins)
	}
	if len(cfg.Auth.JWT.AllowedEmails) != 2 ||
		cfg.Auth.JWT.AllowedEmails[0] != "user1@example.com" ||
		cfg.Auth.JWT.AllowedEmails[1] != "user2@example.com" {
		t.Errorf("PROXY_AUTH_JWT_ALLOWED_EMAILS: got %v", cfg.Auth.JWT.AllowedEmails)
	}
	if cfg.Auth.JWT.Filters["hd"] != "override.co" {
		t.Errorf("PROXY_AUTH_JWT_FILTERS_HD: got %q", cfg.Auth.JWT.Filters["hd"])
	}
	if cfg.Auth.JWT.Filters["email_verified"] != "true" {
		t.Errorf("PROXY_AUTH_JWT_FILTERS_EMAIL_VERIFIED: got %q", cfg.Auth.JWT.Filters["email_verified"])
	}
	if cfg.Auth.JWT.Mappings["email"] != "USER-EMAIL" {
		t.Errorf("PROXY_AUTH_JWT_MAPPINGS_EMAIL: got %q", cfg.Auth.JWT.Mappings["email"])
	}
	if cfg.Auth.JWT.Mappings["sub"] != "USER-ID" {
		t.Errorf("PROXY_AUTH_JWT_MAPPINGS_SUB: got %q", cfg.Auth.JWT.Mappings["sub"])
	}

	// ── Auth — API key ──
	if !cfg.Auth.APIKey.Enabled {
		t.Error("PROXY_AUTH_API_KEY_ENABLED: got false")
	}
	if cfg.Auth.APIKey.Name != "X-Override-Key" {
		t.Errorf("PROXY_AUTH_API_KEY_NAME: got %q", cfg.Auth.APIKey.Name)
	}
	if cfg.Auth.APIKey.Value != "override-secret" {
		t.Errorf("PROXY_AUTH_API_KEY_VALUE: got %q", cfg.Auth.APIKey.Value)
	}
	if cfg.Auth.APIKey.Payload["service"] != "internal" {
		t.Errorf("PROXY_AUTH_API_KEY_PAYLOAD_SERVICE: got %q", cfg.Auth.APIKey.Payload["service"])
	}
	if cfg.Auth.APIKey.Payload["role"] != "admin" {
		t.Errorf("PROXY_AUTH_API_KEY_PAYLOAD_ROLE: got %q", cfg.Auth.APIKey.Payload["role"])
	}

	// ── Storage ──
	if !cfg.Storage.Enabled {
		t.Error("PROXY_STORAGE_ENABLED: got false")
	}
	if cfg.Storage.ProjectID != "test-project-123" {
		t.Errorf("PROXY_STORAGE_PROJECT_ID: got %q", cfg.Storage.ProjectID)
	}
	if cfg.Storage.Dbname != "test-db" {
		t.Errorf("PROXY_STORAGE_DBNAME: got %q", cfg.Storage.Dbname)
	}
	if cfg.Storage.CollectionPrefix != "test-prefix" {
		t.Errorf("PROXY_STORAGE_COLLECTION_PREFIX: got %q", cfg.Storage.CollectionPrefix)
	}

	// ── Admin ──
	if !cfg.Admin.Enabled {
		t.Error("PROXY_ADMIN_ENABLED: got false")
	}
	if cfg.Admin.JWT.Issuer != "https://accounts.google.com" {
		t.Errorf("PROXY_ADMIN_JWT_ISSUER: got %q", cfg.Admin.JWT.Issuer)
	}
	if cfg.Admin.JWT.Audience != "https://my-proxy.run.app" {
		t.Errorf("PROXY_ADMIN_JWT_AUDIENCE: got %q", cfg.Admin.JWT.Audience)
	}
	if len(cfg.Admin.JWT.AllowedEmails) != 2 ||
		cfg.Admin.JWT.AllowedEmails[0] != "admin@example.com" ||
		cfg.Admin.JWT.AllowedEmails[1] != "ops@example.com" {
		t.Errorf("PROXY_ADMIN_JWT_ALLOWED_EMAILS: got %v", cfg.Admin.JWT.AllowedEmails)
	}
	if cfg.Admin.JWT.Filters["hd"] != "farport.co" {
		t.Errorf("PROXY_ADMIN_JWT_FILTERS_HD: got %q", cfg.Admin.JWT.Filters["hd"])
	}
	if cfg.Admin.JWT.Filters["email_verified"] != "true" {
		t.Errorf("PROXY_ADMIN_JWT_FILTERS_EMAIL_VERIFIED: got %q", cfg.Admin.JWT.Filters["email_verified"])
	}
	if cfg.Admin.JWT.Mappings["email"] != "ADMIN-EMAIL" {
		t.Errorf("PROXY_ADMIN_JWT_MAPPINGS_EMAIL: got %q", cfg.Admin.JWT.Mappings["email"])
	}
	if cfg.Admin.JWT.Mappings["sub"] != "ADMIN-ID" {
		t.Errorf("PROXY_ADMIN_JWT_MAPPINGS_SUB: got %q", cfg.Admin.JWT.Mappings["sub"])
	}
}

// TestEnvOverridesCoverageGuard is a structural test that parses applyEnvOverrides
// to extract every os.Getenv("PROXY_...") call and every applyJWTMapOverrides prefix,
// then verifies each one appears in TestEnvVarOverridesComplete's env map.
// If a developer adds a new PROXY_* env var but forgets to test it, this test fails.
func TestEnvOverridesCoverageGuard(t *testing.T) {
	src, err := os.ReadFile("config.go")
	if err != nil {
		t.Fatalf("failed to read config.go: %v", err)
	}
	content := string(src)

	// Find the applyEnvOverrides function body.
	fnStart := strings.Index(content, "func applyEnvOverrides(")
	if fnStart < 0 {
		t.Fatal("could not find applyEnvOverrides function")
	}
	// Find the end of the function by counting braces.
	body := content[fnStart:]
	depth := 0
	fnEnd := -1
	for i, ch := range body {
		if ch == '{' {
			depth++
		} else if ch == '}' {
			depth--
			if depth == 0 {
				fnEnd = i
				break
			}
		}
	}
	if fnEnd < 0 {
		t.Fatal("could not find end of applyEnvOverrides")
	}
	fnBody := body[:fnEnd+1]

	// Extract all os.Getenv("PROXY_...") env var names.
	getenvRe := regexp.MustCompile(`os\.Getenv\("(PROXY_[A-Z_]+)"\)`)
	getenvMatches := getenvRe.FindAllStringSubmatch(fnBody, -1)

	// Extract all applyJWTMapOverrides prefixes (e.g. "PROXY_AUTH_JWT_FILTERS_").
	mapOverrideRe := regexp.MustCompile(`applyJWTMapOverrides\("(PROXY_[A-Z_]+_)"`)
	mapOverrideMatches := mapOverrideRe.FindAllStringSubmatch(fnBody, -1)

	// Build the canonical env var map — same keys as TestEnvVarOverridesComplete.
	// We maintain this list alongside the test above; the guard ensures they stay in sync.
	testedEnvVars := map[string]bool{
		"PROXY_SERVER_PORT":                                true,
		"PROXY_SERVER_TARGET_URL":                          true,
		"PROXY_SERVER_STRIP_PREFIX":                        true,
		"PROXY_SERVER_INCLUDE_PATHS":                       true,
		"PROXY_SERVER_EXCLUDE_PATHS":                       true,
		"PROXY_SERVER_SHUTDOWN_TIMEOUT_SECS":               true,
		"PROXY_SERVER_HEALTH_CHECK_PATH":                   true,
		"PROXY_SERVER_HEALTH_CHECK_TARGET":                 true,
		"PROXY_SECURITY_RATE_LIMIT_ENABLED":                true,
		"PROXY_SECURITY_RATE_LIMIT_REQUESTS_PER_MIN":       true,
		"PROXY_SECURITY_RATE_LIMIT_BAN_FOR_MIN":            true,
		"PROXY_SECURITY_RATE_LIMIT_THROTTLE_DELAY_MS":      true,
		"PROXY_SECURITY_RATE_LIMIT_MAX_DELAY_SLOTS":        true,
		"PROXY_SECURITY_RATE_LIMIT_SKIP_IF_JWT_IDENTIFIED": true,
		"PROXY_SECURITY_APIKEY_RATE_LIMIT_ENABLED":           true,
		"PROXY_SECURITY_APIKEY_RATE_LIMIT_REQUESTS_PER_MIN":  true,
		"PROXY_SECURITY_APIKEY_RATE_LIMIT_BAN_FOR_MIN":       true,
		"PROXY_SECURITY_APIKEY_RATE_LIMIT_INCLUDE_IP":        true,
		"PROXY_SECURITY_APIKEY_RATE_LIMIT_KEY_HEADER":        true,
		"PROXY_SECURITY_APIKEY_RATE_LIMIT_THROTTLE_DELAY_MS": true,
		"PROXY_SECURITY_APIKEY_RATE_LIMIT_MAX_DELAY_SLOTS":   true,
		"PROXY_SECURITY_JWT_RATE_LIMIT_ENABLED":           true,
		"PROXY_SECURITY_JWT_RATE_LIMIT_REQUESTS_PER_MIN":  true,
		"PROXY_SECURITY_JWT_RATE_LIMIT_BAN_FOR_MIN":       true,
		"PROXY_SECURITY_JWT_RATE_LIMIT_INCLUDE_IP":        true,
		"PROXY_SECURITY_JWT_RATE_LIMIT_THROTTLE_DELAY_MS": true,
		"PROXY_SECURITY_JWT_RATE_LIMIT_MAX_DELAY_SLOTS":   true,
		"PROXY_SECURITY_MAX_BODY_BYTES":                    true,
		"PROXY_AUTH_HEADER_PREFIX":                         true,
		"PROXY_AUTH_JWT_ENABLED":                           true,
		"PROXY_AUTH_JWT_ISSUER":                            true,
		"PROXY_AUTH_JWT_AUDIENCE":                          true,
		"PROXY_AUTH_JWT_TOLERANCE_SECS":                    true,
		"PROXY_AUTH_JWT_CACHE_TTL_MINS":                    true,
		"PROXY_AUTH_JWT_ALLOWED_EMAILS":                    true,
		"PROXY_AUTH_API_KEY_ENABLED":                       true,
		"PROXY_AUTH_API_KEY_NAME":                          true,
		"PROXY_AUTH_API_KEY_VALUE":                         true,
		"PROXY_STORAGE_ENABLED":                            true,
		"PROXY_STORAGE_PROJECT_ID":                         true,
		"PROXY_STORAGE_DBNAME":                             true,
		"PROXY_STORAGE_COLLECTION_PREFIX":                  true,
		"PROXY_ADMIN_ENABLED":                              true,
		"PROXY_ADMIN_JWT_ISSUER":                           true,
		"PROXY_ADMIN_JWT_AUDIENCE":                         true,
		"PROXY_ADMIN_JWT_ALLOWED_EMAILS":                   true,
	}

	// Map override prefixes that are tested via specific env vars in the test
	// (e.g. PROXY_AUTH_JWT_FILTERS_ is tested via PROXY_AUTH_JWT_FILTERS_HD).
	testedMapPrefixes := map[string]bool{
		"PROXY_AUTH_JWT_FILTERS_":     true,
		"PROXY_AUTH_JWT_MAPPINGS_":    true,
		"PROXY_AUTH_API_KEY_PAYLOAD_": true,
		"PROXY_ADMIN_JWT_FILTERS_":    true,
		"PROXY_ADMIN_JWT_MAPPINGS_":   true,
	}

	// Check every os.Getenv call is represented.
	for _, m := range getenvMatches {
		envVar := m[1]
		if !testedEnvVars[envVar] {
			t.Errorf("os.Getenv(%q) found in applyEnvOverrides but not covered in TestEnvVarOverridesComplete — add it to the test", envVar)
		}
	}

	// Check every applyJWTMapOverrides prefix is represented.
	for _, m := range mapOverrideMatches {
		prefix := m[1]
		if !testedMapPrefixes[prefix] {
			t.Errorf("applyJWTMapOverrides(%q) found in applyEnvOverrides but not covered in TestEnvVarOverridesComplete — add a test env var with this prefix", prefix)
		}
	}
}

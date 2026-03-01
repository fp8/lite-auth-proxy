package config

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"

	"github.com/BurntSushi/toml"
)

// Config represents the complete application configuration
type Config struct {
	Server   ServerConfig   `toml:"server"`
	Security SecurityConfig `toml:"security"`
	Auth     AuthConfig     `toml:"auth"`
}

// ServerConfig contains HTTP server and proxy settings
type ServerConfig struct {
	Port                int         `toml:"port"`
	TargetURL           string      `toml:"target_url"`
	StripPrefix         string      `toml:"strip_prefix"`
	IncludePaths        []string    `toml:"include_paths"`
	ExcludePaths        []string    `toml:"exclude_paths"`
	ShutdownTimeoutSecs int         `toml:"shutdown_timeout_secs"`
	HealthCheck         HealthCheck `toml:"health_check"`
}

// HealthCheck configures the health check endpoint
type HealthCheck struct {
	Path   string `toml:"path"`
	Target string `toml:"target"`
}

// SecurityConfig contains security-related settings
type SecurityConfig struct {
	RateLimit RateLimitConfig `toml:"rate_limit"`
}

// RateLimitConfig contains rate limiting settings
type RateLimitConfig struct {
	Enabled        bool `toml:"enabled"`
	RequestsPerMin int  `toml:"requests_per_min"`
	BanForMin      int  `toml:"ban_for_min"`
}

// AuthConfig contains authentication settings
type AuthConfig struct {
	HeaderPrefix string       `toml:"header_prefix"`
	JWT          JWTConfig    `toml:"jwt"`
	APIKey       APIKeyConfig `toml:"api_key"`
}

// JWTConfig contains JWT authentication settings
type JWTConfig struct {
	Enabled       bool              `toml:"enabled"`
	Issuer        string            `toml:"issuer"`
	Audience      string            `toml:"audience"`
	ToleranceSecs int               `toml:"tolerance_secs"`
	CacheTTLMins  int               `toml:"cache_ttl_mins"`
	Filters       map[string]string `toml:"filters"`
	Mappings      map[string]string `toml:"mappings"`
}

// APIKeyConfig contains API key authentication settings
type APIKeyConfig struct {
	Enabled bool              `toml:"enabled"`
	Name    string            `toml:"name"`
	Value   string            `toml:"value"`
	Payload map[string]string `toml:"payload"`
}

// Load reads and parses the configuration file with environment variable substitution
func Load(configPath string) (*Config, error) {
	// Read config file
	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	// Substitute environment variables in the config
	configContent := substituteEnvVars(string(data))

	// Parse TOML
	var config Config
	if err := toml.Unmarshal([]byte(configContent), &config); err != nil {
		return nil, fmt.Errorf("failed to parse TOML config: %w", err)
	}

	// Apply environment variable overrides
	if err := applyEnvOverrides(&config); err != nil {
		return nil, fmt.Errorf("failed to apply env overrides: %w", err)
	}

	// Set defaults
	setDefaults(&config)

	// Validate configuration
	if err := validate(&config); err != nil {
		return nil, fmt.Errorf("configuration validation failed: %w", err)
	}

	return &config, nil
}

// substituteEnvVars replaces {{ENV.VARIABLE_NAME}} patterns with environment variable values
func substituteEnvVars(content string) string {
	re := regexp.MustCompile(`\{\{ENV\.([A-Z_][A-Z0-9_]*)\}\}`)
	return re.ReplaceAllStringFunc(content, func(match string) string {
		// Extract variable name from {{ENV.VAR_NAME}}
		varName := re.FindStringSubmatch(match)[1]
		if value := os.Getenv(varName); value != "" {
			return value
		}
		return match // Keep original if env var not found
	})
}

// applyEnvOverrides applies environment variable overrides with PROXY_ prefix
func applyEnvOverrides(config *Config) error {
	// Server overrides
	if val := os.Getenv("PROXY_SERVER_PORT"); val != "" {
		port, err := strconv.Atoi(val)
		if err != nil {
			return fmt.Errorf("invalid PROXY_SERVER_PORT: %w", err)
		}
		config.Server.Port = port
	}
	if val := os.Getenv("PROXY_SERVER_TARGET_URL"); val != "" {
		config.Server.TargetURL = val
	}
	if val := os.Getenv("PROXY_SERVER_STRIP_PREFIX"); val != "" {
		config.Server.StripPrefix = val
	}
	if val := os.Getenv("PROXY_SERVER_INCLUDE_PATHS"); val != "" {
		config.Server.IncludePaths = splitCSV(val)
	}
	if val := os.Getenv("PROXY_SERVER_EXCLUDE_PATHS"); val != "" {
		config.Server.ExcludePaths = splitCSV(val)
	}
	if val := os.Getenv("PROXY_SERVER_SHUTDOWN_TIMEOUT_SECS"); val != "" {
		timeout, err := strconv.Atoi(val)
		if err != nil {
			return fmt.Errorf("invalid PROXY_SERVER_SHUTDOWN_TIMEOUT_SECS: %w", err)
		}
		config.Server.ShutdownTimeoutSecs = timeout
	}

	// Health check overrides
	if val := os.Getenv("PROXY_SERVER_HEALTH_CHECK_PATH"); val != "" {
		config.Server.HealthCheck.Path = val
	}
	if val := os.Getenv("PROXY_SERVER_HEALTH_CHECK_TARGET"); val != "" {
		config.Server.HealthCheck.Target = val
	}

	// Security overrides
	if val := os.Getenv("PROXY_SECURITY_RATE_LIMIT_ENABLED"); val != "" {
		enabled, err := strconv.ParseBool(val)
		if err != nil {
			return fmt.Errorf("invalid PROXY_SECURITY_RATE_LIMIT_ENABLED: %w", err)
		}
		config.Security.RateLimit.Enabled = enabled
	}
	if val := os.Getenv("PROXY_SECURITY_RATE_LIMIT_REQUESTS_PER_MIN"); val != "" {
		rpm, err := strconv.Atoi(val)
		if err != nil {
			return fmt.Errorf("invalid PROXY_SECURITY_RATE_LIMIT_REQUESTS_PER_MIN: %w", err)
		}
		config.Security.RateLimit.RequestsPerMin = rpm
	}
	if val := os.Getenv("PROXY_SECURITY_RATE_LIMIT_BAN_FOR_MIN"); val != "" {
		ban, err := strconv.Atoi(val)
		if err != nil {
			return fmt.Errorf("invalid PROXY_SECURITY_RATE_LIMIT_BAN_FOR_MIN: %w", err)
		}
		config.Security.RateLimit.BanForMin = ban
	}

	// Auth overrides
	if val := os.Getenv("PROXY_AUTH_HEADER_PREFIX"); val != "" {
		config.Auth.HeaderPrefix = val
	}

	// JWT overrides
	if val := os.Getenv("PROXY_AUTH_JWT_ENABLED"); val != "" {
		enabled, err := strconv.ParseBool(val)
		if err != nil {
			return fmt.Errorf("invalid PROXY_AUTH_JWT_ENABLED: %w", err)
		}
		config.Auth.JWT.Enabled = enabled
	}
	if val := os.Getenv("PROXY_AUTH_JWT_ISSUER"); val != "" {
		config.Auth.JWT.Issuer = substituteEnvVars(val)
	}
	if val := os.Getenv("PROXY_AUTH_JWT_AUDIENCE"); val != "" {
		config.Auth.JWT.Audience = substituteEnvVars(val)
	}
	if val := os.Getenv("PROXY_AUTH_JWT_TOLERANCE_SECS"); val != "" {
		tolerance, err := strconv.Atoi(val)
		if err != nil {
			return fmt.Errorf("invalid PROXY_AUTH_JWT_TOLERANCE_SECS: %w", err)
		}
		config.Auth.JWT.ToleranceSecs = tolerance
	}
	if val := os.Getenv("PROXY_AUTH_JWT_CACHE_TTL_MINS"); val != "" {
		cacheTTL, err := strconv.Atoi(val)
		if err != nil {
			return fmt.Errorf("invalid PROXY_AUTH_JWT_CACHE_TTL_MINS: %w", err)
		}
		config.Auth.JWT.CacheTTLMins = cacheTTL
	}
	config.Auth.JWT.Filters = applyJWTMapOverrides("PROXY_AUTH_JWT_FILTERS_", config.Auth.JWT.Filters)
	config.Auth.JWT.Mappings = applyJWTMapOverrides("PROXY_AUTH_JWT_MAPPINGS_", config.Auth.JWT.Mappings)

	// API Key overrides
	if val := os.Getenv("PROXY_AUTH_API_KEY_ENABLED"); val != "" {
		enabled, err := strconv.ParseBool(val)
		if err != nil {
			return fmt.Errorf("invalid PROXY_AUTH_API_KEY_ENABLED: %w", err)
		}
		config.Auth.APIKey.Enabled = enabled
	}
	if val := os.Getenv("PROXY_AUTH_API_KEY_NAME"); val != "" {
		config.Auth.APIKey.Name = val
	}
	if val := os.Getenv("PROXY_AUTH_API_KEY_VALUE"); val != "" {
		config.Auth.APIKey.Value = substituteEnvVars(val)
	}
	config.Auth.APIKey.Payload = applyJWTMapOverrides("PROXY_AUTH_API_KEY_PAYLOAD_", config.Auth.APIKey.Payload)

	return nil
}

func splitCSV(value string) []string {
	parts := strings.Split(value, ",")
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		item := strings.TrimSpace(part)
		if item == "" {
			continue
		}
		result = append(result, item)
	}
	return result
}

func applyJWTMapOverrides(prefix string, target map[string]string) map[string]string {
	if target == nil {
		target = map[string]string{}
	}

	for _, envVar := range os.Environ() {
		parts := strings.SplitN(envVar, "=", 2)
		if len(parts) != 2 {
			continue
		}
		key := parts[0]
		value := parts[1]
		if !strings.HasPrefix(key, prefix) {
			continue
		}
		claimKey := strings.ToLower(strings.TrimPrefix(key, prefix))
		if claimKey == "" {
			continue
		}
		target[claimKey] = substituteEnvVars(value)
	}

	return target
}

// setDefaults sets default values for optional configuration fields
func setDefaults(config *Config) {
	// Server defaults
	if config.Server.Port == 0 {
		config.Server.Port = 8888
	}
	if config.Server.ShutdownTimeoutSecs == 0 {
		config.Server.ShutdownTimeoutSecs = 10
	}
	if config.Server.HealthCheck.Path == "" {
		config.Server.HealthCheck.Path = "/healthz"
	}
	if len(config.Server.IncludePaths) == 0 {
		config.Server.IncludePaths = []string{"/*"}
	}

	// Security defaults
	if config.Security.RateLimit.RequestsPerMin == 0 {
		config.Security.RateLimit.RequestsPerMin = 60
	}
	if config.Security.RateLimit.BanForMin == 0 {
		config.Security.RateLimit.BanForMin = 5
	}

	// Auth defaults
	if config.Auth.HeaderPrefix == "" {
		config.Auth.HeaderPrefix = "X-AUTH-"
	}

	// JWT defaults
	if config.Auth.JWT.ToleranceSecs == 0 {
		config.Auth.JWT.ToleranceSecs = 30
	}
	if config.Auth.JWT.CacheTTLMins == 0 {
		config.Auth.JWT.CacheTTLMins = 1440 // 24 hours
	}

	// API Key defaults
	if config.Auth.APIKey.Name == "" {
		config.Auth.APIKey.Name = "X-API-KEY"
	}

	// Resolve GOOGLE_CLOUD_PROJECT if needed
	resolveGCPProjectID(config)
}

// resolveGCPProjectID attempts to resolve GOOGLE_CLOUD_PROJECT from env or metadata server
func resolveGCPProjectID(config *Config) {
	projectID := os.Getenv("GOOGLE_CLOUD_PROJECT")
	if projectID == "" {
		// Try to fetch from GCP metadata server
		projectID = fetchGCPProjectID()
	}

	// Replace placeholder in issuer and audience
	if projectID != "" {
		config.Auth.JWT.Issuer = strings.ReplaceAll(config.Auth.JWT.Issuer, "{{ENV.GOOGLE_CLOUD_PROJECT}}", projectID)
		config.Auth.JWT.Audience = strings.ReplaceAll(config.Auth.JWT.Audience, "{{ENV.GOOGLE_CLOUD_PROJECT}}", projectID)
	}
}

// fetchGCPProjectID fetches GOOGLE_CLOUD_PROJECT from the metadata server
func fetchGCPProjectID() string {
	client := &http.Client{
		Timeout: 1 * 1e9, // 1 second timeout
	}

	req, err := http.NewRequest("GET", "http://metadata.google.internal/computeMetadata/v1/project/project-id", nil)
	if err != nil {
		return ""
	}
	req.Header.Set("Metadata-Flavor", "Google")

	resp, err := client.Do(req)
	if err != nil {
		return ""
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return ""
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return ""
	}

	return strings.TrimSpace(string(body))
}

// validate checks that the configuration is valid
func validate(config *Config) error {
	// At least one auth method must be enabled
	if !config.Auth.JWT.Enabled && !config.Auth.APIKey.Enabled {
		return fmt.Errorf("at least one authentication method (JWT or API-Key) must be enabled")
	}

	// JWT validation
	if config.Auth.JWT.Enabled {
		if config.Auth.JWT.Issuer == "" {
			return fmt.Errorf("JWT issuer is required when JWT auth is enabled")
		}
		if config.Auth.JWT.Audience == "" {
			return fmt.Errorf("JWT audience is required when JWT auth is enabled")
		}
		// Check if placeholders are still present (couldn't resolve GOOGLE_CLOUD_PROJECT)
		if strings.Contains(config.Auth.JWT.Issuer, "{{ENV.GOOGLE_CLOUD_PROJECT}}") {
			return fmt.Errorf("could not resolve GOOGLE_CLOUD_PROJECT in JWT issuer")
		}
		if strings.Contains(config.Auth.JWT.Audience, "{{ENV.GOOGLE_CLOUD_PROJECT}}") {
			return fmt.Errorf("could not resolve GOOGLE_CLOUD_PROJECT in JWT audience")
		}
	}

	// API Key validation
	if config.Auth.APIKey.Enabled {
		if config.Auth.APIKey.Value == "" {
			return fmt.Errorf("API key value is required when API-Key auth is enabled")
		}
		// Check if placeholders are still present
		if strings.Contains(config.Auth.APIKey.Value, "{{ENV.") {
			return fmt.Errorf("unresolved environment variable in API key value")
		}
	}

	// Server validation
	if config.Server.Port < 1 || config.Server.Port > 65535 {
		return fmt.Errorf("server port must be between 1 and 65535, got: %d", config.Server.Port)
	}

	return nil
}

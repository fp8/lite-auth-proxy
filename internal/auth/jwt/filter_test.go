package jwt

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fp8/lite-auth-proxy/internal/config"
)

func TestEvaluateFiltersExactMatchPass(t *testing.T) {
	claims := Claims{"role": "admin"}
	filters := map[string]string{"role": "admin"}

	if err := EvaluateFilters(claims, filters); err != nil {
		t.Fatalf("expected filter to pass, got error: %v", err)
	}
}

func TestEvaluateFiltersExactMatchFail(t *testing.T) {
	claims := Claims{"role": "user"}
	filters := map[string]string{"role": "admin"}

	err := EvaluateFilters(claims, filters)
	if err == nil {
		t.Fatal("expected filter mismatch error, got nil")
	}

	if !strings.Contains(err.Error(), "role") {
		t.Fatalf("expected error to mention claim name, got: %v", err)
	}
}

func TestEvaluateFiltersRegexPass(t *testing.T) {
	claims := Claims{"email": "alice@company.com"}
	filters := map[string]string{"email": `/@company\.com$/`}

	if err := EvaluateFilters(claims, filters); err != nil {
		t.Fatalf("expected regex filter to pass, got error: %v", err)
	}
}

func TestEvaluateFiltersRegexFail(t *testing.T) {
	claims := Claims{"email": "alice@example.com"}
	filters := map[string]string{"email": `/@company\.com$/`}

	err := EvaluateFilters(claims, filters)
	if err == nil {
		t.Fatal("expected regex filter mismatch error, got nil")
	}
}

func TestEvaluateFiltersArrayORPass(t *testing.T) {
	claims := Claims{"roles": []interface{}{"viewer", "admin", "editor"}}
	filters := map[string]string{"roles": "admin"}

	if err := EvaluateFilters(claims, filters); err != nil {
		t.Fatalf("expected array OR filter to pass, got error: %v", err)
	}
}

func TestEvaluateFiltersArrayORFail(t *testing.T) {
	claims := Claims{"roles": []interface{}{"viewer", "editor"}}
	filters := map[string]string{"roles": "admin"}

	err := EvaluateFilters(claims, filters)
	if err == nil {
		t.Fatal("expected array OR filter mismatch error, got nil")
	}
}

func TestEvaluateFiltersMissingClaim(t *testing.T) {
	claims := Claims{"sub": "user-1"}
	filters := map[string]string{"email": `/@company\.com$/`}

	err := EvaluateFilters(claims, filters)
	if err == nil {
		t.Fatal("expected missing claim error, got nil")
	}

	if !strings.Contains(err.Error(), "missing") {
		t.Fatalf("expected missing claim error message, got: %v", err)
	}
}

func TestEnvOverrideFilterRejectsClaim(t *testing.T) {
	_ = os.Setenv("PROXY_AUTH_JWT_FILTERS_HD", "farport.co")
	defer func() { _ = os.Unsetenv("PROXY_AUTH_JWT_FILTERS_HD") }()

	configContent := `
[server]
target_url = "http://localhost:8080"

[auth.jwt]
enabled = true
issuer = "https://example.com"
audience = "test"

[auth.jwt.filters]
hd = "trybuyme.com"
`

	configPath := writeTempConfig(t, configContent)
	cfg, err := config.Load(configPath)
	if err != nil {
		t.Fatalf("Failed to load config: %v", err)
	}

	claims := Claims{"hd": "trybuyme.com"}
	if err := EvaluateFilters(claims, cfg.Auth.JWT.Filters); err == nil {
		t.Fatal("expected filter mismatch error due to env override, got nil")
	}
}

func writeTempConfig(t *testing.T, content string) string {
	t.Helper()
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.toml")
	if err := os.WriteFile(configPath, []byte(content), 0644); err != nil {
		t.Fatalf("Failed to create temp config: %v", err)
	}
	return configPath
}

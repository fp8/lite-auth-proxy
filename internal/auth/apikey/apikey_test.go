package apikey

import (
	"net/http/httptest"
	"testing"

	"github.com/fp8/lite-auth-proxy/internal/config"
)

func TestValidateAPIKeyCorrectKey(t *testing.T) {
	req := httptest.NewRequest("GET", "http://example.com", nil)
	req.Header.Set("X-API-KEY", "secret-123")

	authCfg := &config.AuthConfig{
		HeaderPrefix: "X-AUTH-",
		APIKey: config.APIKeyConfig{
			Enabled: true,
			Name:    "X-API-KEY",
			Value:   "secret-123",
			Payload: map[string]string{"service": "internal"},
		},
	}

	headers, err := ValidateAPIKey(req, authCfg)
	if err != nil {
		t.Fatalf("expected success, got error: %v", err)
	}

	if headers["X-AUTH-SERVICE"] != "internal" {
		t.Fatalf("expected injected header X-AUTH-SERVICE=internal, got: %v", headers)
	}
}

func TestValidateAPIKeyWrongKey(t *testing.T) {
	req := httptest.NewRequest("GET", "http://example.com", nil)
	req.Header.Set("X-API-KEY", "wrong")

	authCfg := &config.AuthConfig{
		HeaderPrefix: "X-AUTH-",
		APIKey: config.APIKeyConfig{
			Enabled: true,
			Name:    "X-API-KEY",
			Value:   "secret-123",
		},
	}

	_, err := ValidateAPIKey(req, authCfg)
	if err == nil {
		t.Fatal("expected invalid api key error, got nil")
	}
}

func TestValidateAPIKeyMissingHeader(t *testing.T) {
	req := httptest.NewRequest("GET", "http://example.com", nil)

	authCfg := &config.AuthConfig{
		HeaderPrefix: "X-AUTH-",
		APIKey: config.APIKeyConfig{
			Enabled: true,
			Name:    "X-API-KEY",
			Value:   "secret-123",
		},
	}

	_, err := ValidateAPIKey(req, authCfg)
	if err == nil {
		t.Fatal("expected missing api key error, got nil")
	}
}

func TestValidateAPIKeyPayloadInjectionPrefixAndUppercase(t *testing.T) {
	req := httptest.NewRequest("GET", "http://example.com", nil)
	req.Header.Set("X-API-KEY", "secret-123")

	authCfg := &config.AuthConfig{
		HeaderPrefix: "X-AUTH-",
		APIKey: config.APIKeyConfig{
			Enabled: true,
			Name:    "X-API-KEY",
			Value:   "secret-123",
			Payload: map[string]string{
				"service": "candidates",
				"env":     "dev",
			},
		},
	}

	headers, err := ValidateAPIKey(req, authCfg)
	if err != nil {
		t.Fatalf("expected success, got error: %v", err)
	}

	if headers["X-AUTH-SERVICE"] != "candidates" {
		t.Fatalf("expected X-AUTH-SERVICE=candidates, got: %v", headers["X-AUTH-SERVICE"])
	}
	if headers["X-AUTH-ENV"] != "dev" {
		t.Fatalf("expected X-AUTH-ENV=dev, got: %v", headers["X-AUTH-ENV"])
	}
}

func TestValidateAPIKeyDisabledSkipsValidation(t *testing.T) {
	req := httptest.NewRequest("GET", "http://example.com", nil)

	authCfg := &config.AuthConfig{
		HeaderPrefix: "X-AUTH-",
		APIKey: config.APIKeyConfig{
			Enabled: false,
			Name:    "X-API-KEY",
			Value:   "secret-123",
		},
	}

	headers, err := ValidateAPIKey(req, authCfg)
	if err != nil {
		t.Fatalf("expected disabled API key validation to be skipped, got error: %v", err)
	}
	if headers != nil {
		t.Fatalf("expected nil headers when validation skipped, got: %v", headers)
	}
}

func TestValidateAPIKeySoleEnabledAuthMethod(t *testing.T) {
	req := httptest.NewRequest("GET", "http://example.com", nil)
	req.Header.Set("X-API-KEY", "secret-123")

	authCfg := &config.AuthConfig{
		HeaderPrefix: "X-AUTH-",
		JWT: config.JWTConfig{
			Enabled: false,
		},
		APIKey: config.APIKeyConfig{
			Enabled: true,
			Name:    "X-API-KEY",
			Value:   "secret-123",
			Payload: map[string]string{"service": "internal"},
		},
	}

	headers, err := ValidateAPIKey(req, authCfg)
	if err != nil {
		t.Fatalf("expected API-key-only mode to succeed, got error: %v", err)
	}

	if headers["X-AUTH-SERVICE"] != "internal" {
		t.Fatalf("expected API-key-only mode payload injection, got headers: %v", headers)
	}
}

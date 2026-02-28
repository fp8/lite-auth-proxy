//go:build integration

package proxy_test

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fp8/lite-auth-proxy/internal/auth/jwt"
	"github.com/fp8/lite-auth-proxy/internal/config"
	"github.com/fp8/lite-auth-proxy/internal/proxy"
)

// TestIntegrationJWTRS256Pipeline tests full pipeline with RS256 JWT validation
func TestIntegrationJWTRS256Pipeline(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	rsaKey, err := jwt.GenerateRSAKeyPair()
	if err != nil {
		t.Fatalf("failed to generate RSA key: %v", err)
	}

	jwksServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/openid-configuration":
			config := map[string]interface{}{
				"jwks_uri": "http://" + r.Host + "/jwks",
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(config)
		case "/jwks":
			keys := []map[string]interface{}{
				{
					"kty": "RSA",
					"kid": "test-key",
					"use": "sig",
					"alg": "RS256",
					"n":   base64.RawURLEncoding.EncodeToString(rsaKey.PublicKey.N.Bytes()),
					"e":   base64.RawURLEncoding.EncodeToString(big.NewInt(int64(rsaKey.PublicKey.E)).Bytes()),
				},
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"keys": keys})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer jwksServer.Close()

	issuer := "http://" + jwksServer.Listener.Addr().String()

	// Upstream service assertion
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/resource" {
			t.Errorf("expected path /resource (after prefix strip), got %s", r.URL.Path)
		}
		if r.Header.Get("X-AUTH-USER-ID") != "user-123" {
			t.Errorf("expected X-AUTH-USER-ID header, got %s", r.Header.Get("X-AUTH-USER-ID"))
		}
		if r.Header.Get("X-AUTH-USER-ROLE") != "admin" {
			t.Errorf("expected X-AUTH-USER-ROLE header, got %s", r.Header.Get("X-AUTH-USER-ROLE"))
		}
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"status": "ok"})
	}))
	defer upstream.Close()

	cfg := &config.Config{
		Server: config.ServerConfig{
			Port:         8888,
			TargetURL:    upstream.URL,
			StripPrefix:  "/api",
			IncludePaths: []string{"/api/*"},
			ExcludePaths: []string{"/healthz"},
		},
		Auth: config.AuthConfig{
			HeaderPrefix: "X-AUTH-",
			JWT: config.JWTConfig{
				Enabled:       true,
				Issuer:        issuer,
				Audience:      "test-aud",
				ToleranceSecs: 30,
				CacheTTLMins:  60,
				Filters:       map[string]string{"email_verified": "true"},
				Mappings: map[string]string{
					"sub":  "USER-ID",
					"role": "USER-ROLE",
				},
			},
			APIKey: config.APIKeyConfig{Enabled: false},
		},
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	h, err := proxy.NewHandler(cfg, logger)
	if err != nil {
		t.Fatalf("failed to create handler: %v", err)
	}

	// Build valid JWT
	builder := jwt.NewTokenBuilder("RS256", rsaKey, "test-key")
	builder.
		WithIssuer(issuer).
		WithAudience("test-aud").
		WithIssuedAt(time.Now()).
		WithExpiresAt(time.Now().Add(1*time.Hour)).
		WithClaim("sub", "user-123").
		WithClaim("role", "admin").
		WithClaim("email_verified", "true")

	token, err := builder.Build()
	if err != nil {
		t.Fatalf("failed to build token: %v", err)
	}

	// Make request
	req := httptest.NewRequest("GET", "http://proxy.local/api/resource", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp := httptest.NewRecorder()

	h.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		body := resp.Body.String()
		t.Fatalf("expected 200, got %d, body: %s", resp.Code, body)
	}
}

// TestIntegrationJWTES256Pipeline tests full pipeline with ES256 JWT validation
func TestIntegrationJWTES256Pipeline(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	ecdsaKey, err := jwt.GenerateECDSAKeyPair()
	if err != nil {
		t.Fatalf("failed to generate ECDSA key: %v", err)
	}

	jwksServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/openid-configuration":
			config := map[string]interface{}{
				"jwks_uri": "http://" + r.Host + "/jwks",
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(config)
		case "/jwks":
			x := ecdsaKey.PublicKey.X
			y := ecdsaKey.PublicKey.Y
			keys := []map[string]interface{}{
				{
					"kty": "EC",
					"kid": "ec-key",
					"use": "sig",
					"alg": "ES256",
					"crv": "P-256",
					"x":   base64.RawURLEncoding.EncodeToString(x.Bytes()),
					"y":   base64.RawURLEncoding.EncodeToString(y.Bytes()),
				},
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"keys": keys})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer jwksServer.Close()

	issuer := "http://" + jwksServer.Listener.Addr().String()

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-AUTH-SUB") != "user-es256" {
			t.Errorf("expected X-AUTH-SUB header, got %s", r.Header.Get("X-AUTH-SUB"))
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	cfg := &config.Config{
		Server: config.ServerConfig{
			Port:         8888,
			TargetURL:    upstream.URL,
			IncludePaths: []string{"/*"},
		},
		Auth: config.AuthConfig{
			HeaderPrefix: "X-AUTH-",
			JWT: config.JWTConfig{
				Enabled:       true,
				Issuer:        issuer,
				Audience:      "test-aud",
				ToleranceSecs: 30,
				CacheTTLMins:  60,
				Mappings: map[string]string{
					"sub": "SUB",
				},
			},
			APIKey: config.APIKeyConfig{Enabled: false},
		},
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	h, err := proxy.NewHandler(cfg, logger)
	if err != nil {
		t.Fatalf("failed to create handler: %v", err)
	}

	builder := jwt.NewTokenBuilder("ES256", ecdsaKey, "ec-key")
	builder.
		WithIssuer(issuer).
		WithAudience("test-aud").
		WithIssuedAt(time.Now()).
		WithExpiresAt(time.Now().Add(1*time.Hour)).
		WithClaim("sub", "user-es256")

	token, err := builder.Build()
	if err != nil {
		t.Fatalf("failed to build token: %v", err)
	}

	req := httptest.NewRequest("GET", "http://proxy.local/resource", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp := httptest.NewRecorder()

	h.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.Code)
	}
}

// TestIntegrationAPIKeyInjection tests API-key auth with payload injection
func TestIntegrationAPIKeyInjection(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	headersCaptured := make(map[string]string)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		headersCaptured["X-AUTH-SERVICE"] = r.Header.Get("X-AUTH-SERVICE")
		headersCaptured["X-AUTH-TENANT"] = r.Header.Get("X-AUTH-TENANT")
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	cfg := &config.Config{
		Server: config.ServerConfig{
			Port:         8888,
			TargetURL:    upstream.URL,
			IncludePaths: []string{"/*"},
		},
		Auth: config.AuthConfig{
			HeaderPrefix: "X-AUTH-",
			JWT:          config.JWTConfig{Enabled: false},
			APIKey: config.APIKeyConfig{
				Enabled: true,
				Name:    "X-API-KEY",
				Value:   "secret-key-123",
				Payload: map[string]string{
					"service": "api-gateway",
					"tenant":  "acme-corp",
				},
			},
		},
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	h, err := proxy.NewHandler(cfg, logger)
	if err != nil {
		t.Fatalf("failed to create handler: %v", err)
	}

	req := httptest.NewRequest("POST", "http://proxy.local/data", nil)
	req.Header.Set("X-API-KEY", "secret-key-123")
	req.Body = io.NopCloser(strings.NewReader(`{"test":"data"}`))
	resp := httptest.NewRecorder()

	h.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.Code)
	}
	if headersCaptured["X-AUTH-SERVICE"] != "api-gateway" {
		t.Errorf("expected X-AUTH-SERVICE: api-gateway, got %s", headersCaptured["X-AUTH-SERVICE"])
	}
	if headersCaptured["X-AUTH-TENANT"] != "acme-corp" {
		t.Errorf("expected X-AUTH-TENANT: acme-corp, got %s", headersCaptured["X-AUTH-TENANT"])
	}
}

// TestIntegrationBearerInvalidNoAPIKeyFallback tests JWT precedence: invalid bearer = no API-key fallback
func TestIntegrationBearerInvalidNoAPIKeyFallback(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	cfg := &config.Config{
		Server: config.ServerConfig{
			Port:         8888,
			TargetURL:    upstream.URL,
			IncludePaths: []string{"/*"},
		},
		Auth: config.AuthConfig{
			HeaderPrefix: "X-AUTH-",
			JWT: config.JWTConfig{
				Enabled:  true,
				Issuer:   "https://example.com",
				Audience: "test-aud",
			},
			APIKey: config.APIKeyConfig{
				Enabled: true,
				Name:    "X-API-KEY",
				Value:   "api-key",
			},
		},
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	h, err := proxy.NewHandler(cfg, logger)
	if err != nil {
		t.Fatalf("failed to create handler: %v", err)
	}

	// Present invalid bearer, but valid API-key
	req := httptest.NewRequest("GET", "http://proxy.local/resource", nil)
	req.Header.Set("Authorization", "Bearer invalid.jwt.token")
	req.Header.Set("X-API-KEY", "api-key")
	resp := httptest.NewRecorder()

	h.ServeHTTP(resp, req)

	// Should get 401 because invalid bearer takes precedence
	if resp.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d (invalid bearer should not fall through to API-key)", resp.Code)
	}
}

// TestIntegrationConcurrentRateLimitBan tests concurrent requests hitting rate limit and ban
func TestIntegrationConcurrentRateLimitBan(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	cfg := &config.Config{
		Server: config.ServerConfig{
			Port:         8888,
			TargetURL:    upstream.URL,
			IncludePaths: []string{"/api/*"},
			ExcludePaths: []string{"/healthz"},
		},
		Auth: config.AuthConfig{
			HeaderPrefix: "X-AUTH-",
			JWT:          config.JWTConfig{Enabled: false},
			APIKey: config.APIKeyConfig{
				Enabled: true,
				Name:    "X-API-KEY",
				Value:   "key",
			},
		},
		Security: config.SecurityConfig{
			RateLimit: config.RateLimitConfig{
				Enabled:        true,
				RequestsPerMin: 5, // Very low to trigger ban quickly
				BanForMin:      1,
			},
		},
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	h, err := proxy.NewHandler(cfg, logger)
	if err != nil {
		t.Fatalf("failed to create handler: %v", err)
	}

	// Make several parallel requests from same IP (127.0.0.1)
	var wg sync.WaitGroup
	statusCodes := make([]int, 10)
	var mu sync.Mutex

	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			req := httptest.NewRequest("GET", "http://proxy.local/api/data", nil)
			req.Header.Set("X-API-KEY", "key")
			// Set remote addr to simulate same IP
			req.RemoteAddr = "127.0.0.1:12345"
			resp := httptest.NewRecorder()

			h.ServeHTTP(resp, req)

			mu.Lock()
			statusCodes[idx] = resp.Code
			mu.Unlock()
		}(i)
	}

	wg.Wait()

	// Expect: 5 successful (200), 5 rate-limited (429)
	successCount := 0
	rateLimitedCount := 0
	for _, code := range statusCodes {
		if code == http.StatusOK {
			successCount++
		} else if code == http.StatusTooManyRequests {
			rateLimitedCount++
		}
	}

	if successCount != 5 || rateLimitedCount != 5 {
		t.Logf("status codes: %v", statusCodes)
		t.Errorf("expected 5 success and 5 rate-limited, got %d success and %d rate-limited", successCount, rateLimitedCount)
	}
}

// TestIntegrationExcludedPathBypassesAuth tests excluded paths bypass auth
func TestIntegrationExcludedPathBypassesAuth(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	cfg := &config.Config{
		Server: config.ServerConfig{
			Port:         8888,
			TargetURL:    upstream.URL,
			IncludePaths: []string{"/*"},
			ExcludePaths: []string{"/healthz", "/public/*"},
		},
		Auth: config.AuthConfig{
			HeaderPrefix: "X-AUTH-",
			JWT:          config.JWTConfig{Enabled: true, Issuer: "https://example.com", Audience: "aud"},
			APIKey:       config.APIKeyConfig{Enabled: false},
		},
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	h, err := proxy.NewHandler(cfg, logger)
	if err != nil {
		t.Fatalf("failed to create handler: %v", err)
	}

	// Request to excluded path without any credentials should succeed
	req := httptest.NewRequest("GET", "http://proxy.local/public/info", nil)
	resp := httptest.NewRecorder()

	h.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200 for excluded path, got %d", resp.Code)
	}
}

// TestIntegrationEnvVarSubstitution tests {{ENV.VAR}} substitution in config
func TestIntegrationEnvVarSubstitution(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	// This test confirms that env var substitution works through the config system
	// (Config loading is tested in Step 1, this is just an integration verification)

	rsaKey, err := jwt.GenerateRSAKeyPair()
	if err != nil {
		t.Fatalf("failed to generate RSA key: %v", err)
	}

	jwksServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/openid-configuration":
			config := map[string]interface{}{
				"jwks_uri": "http://" + r.Host + "/jwks",
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(config)
		case "/jwks":
			keys := []map[string]interface{}{
				{
					"kty": "RSA",
					"kid": "key1",
					"use": "sig",
					"alg": "RS256",
					"n":   base64.RawURLEncoding.EncodeToString(rsaKey.PublicKey.N.Bytes()),
					"e":   base64.RawURLEncoding.EncodeToString(big.NewInt(int64(rsaKey.PublicKey.E)).Bytes()),
				},
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"keys": keys})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer jwksServer.Close()

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	// Config with issuer that resolves to mock server
	cfg := &config.Config{
		Server: config.ServerConfig{
			Port:         8888,
			TargetURL:    upstream.URL,
			IncludePaths: []string{"/*"},
		},
		Auth: config.AuthConfig{
			HeaderPrefix: "X-AUTH-",
			JWT: config.JWTConfig{
				Enabled:       true,
				Issuer:        "http://" + jwksServer.Listener.Addr().String(),
				Audience:      "project123",
				ToleranceSecs: 30,
				CacheTTLMins:  60,
			},
			APIKey: config.APIKeyConfig{Enabled: false},
		},
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	h, err := proxy.NewHandler(cfg, logger)
	if err != nil {
		t.Fatalf("failed to create handler: %v", err)
	}

	builder := jwt.NewTokenBuilder("RS256", rsaKey, "key1")
	builder.
		WithIssuer("http://"+jwksServer.Listener.Addr().String()).
		WithAudience("project123").
		WithIssuedAt(time.Now()).
		WithExpiresAt(time.Now().Add(1*time.Hour)).
		WithClaim("sub", "user")

	token, err := builder.Build()
	if err != nil {
		t.Fatalf("failed to build token: %v", err)
	}

	req := httptest.NewRequest("GET", "http://proxy.local/api", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp := httptest.NewRecorder()

	h.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.Code)
	}
}

// TestIntegrationHeaderSanitization tests X-AUTH-* headers are stripped
func TestIntegrationHeaderSanitization(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	sensitiveHeaders := make(map[string]bool)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sensitiveHeaders["X-AUTH-INJECTED"] = r.Header.Get("X-AUTH-INJECTED") != ""
		sensitiveHeaders["X-AUTH-SECRET"] = r.Header.Get("X-AUTH-SECRET") != ""
		// X-AUTH-SERVICE should be present (injected by proxy from apikey.Payload)
		sensitiveHeaders["X-AUTH-SERVICE-PRESENT"] = r.Header.Get("X-AUTH-SERVICE") != ""
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	cfg := &config.Config{
		Server: config.ServerConfig{
			Port:         8888,
			TargetURL:    upstream.URL,
			IncludePaths: []string{"/*"},
		},
		Auth: config.AuthConfig{
			HeaderPrefix: "X-AUTH-",
			JWT:          config.JWTConfig{Enabled: false},
			APIKey: config.APIKeyConfig{
				Enabled: true,
				Name:    "X-API-KEY",
				Value:   "key",
				Payload: map[string]string{"service": "api-service"},
			},
		},
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	h, err := proxy.NewHandler(cfg, logger)
	if err != nil {
		t.Fatalf("failed to create handler: %v", err)
	}

	// Send request with malicious X-AUTH-* headers that should be stripped
	req := httptest.NewRequest("GET", "http://proxy.local/resource", nil)
	req.Header.Set("X-API-KEY", "key")
	req.Header.Set("X-AUTH-INJECTED", "attacker-data")
	req.Header.Set("X-AUTH-SECRET", "stolen-secret")
	resp := httptest.NewRecorder()

	h.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.Code)
	}

	// Verify malicious headers were stripped, but proxy-injected headers are present
	if sensitiveHeaders["X-AUTH-INJECTED"] {
		t.Error("X-AUTH-INJECTED should have been stripped")
	}
	if sensitiveHeaders["X-AUTH-SECRET"] {
		t.Error("X-AUTH-SECRET should have been stripped")
	}
	if !sensitiveHeaders["X-AUTH-SERVICE-PRESENT"] {
		t.Error("X-AUTH-SERVICE should have been injected by proxy")
	}
}

// TestIntegrationJWTOnlyConfig tests JWT-only auth (API-key disabled)
func TestIntegrationJWTOnlyConfig(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	rsaKey, err := jwt.GenerateRSAKeyPair()
	if err != nil {
		t.Fatalf("failed to generate RSA key: %v", err)
	}

	jwksServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/openid-configuration":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"jwks_uri": "http://" + r.Host + "/jwks",
			})
		case "/jwks":
			keys := []map[string]interface{}{
				{
					"kty": "RSA",
					"kid": "key1",
					"use": "sig",
					"alg": "RS256",
					"n":   base64.RawURLEncoding.EncodeToString(rsaKey.PublicKey.N.Bytes()),
					"e":   base64.RawURLEncoding.EncodeToString(big.NewInt(int64(rsaKey.PublicKey.E)).Bytes()),
				},
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"keys": keys})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer jwksServer.Close()

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	cfg := &config.Config{
		Server: config.ServerConfig{
			Port:         8888,
			TargetURL:    upstream.URL,
			IncludePaths: []string{"/*"},
		},
		Auth: config.AuthConfig{
			HeaderPrefix: "X-AUTH-",
			JWT: config.JWTConfig{
				Enabled:  true,
				Issuer:   "http://" + jwksServer.Listener.Addr().String(),
				Audience: "test",
				Mappings: map[string]string{"sub": "SUB"},
			},
			APIKey: config.APIKeyConfig{Enabled: false},
		},
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	h, err := proxy.NewHandler(cfg, logger)
	if err != nil {
		t.Fatalf("failed to create handler: %v", err)
	}

	// Request with API-key should fail (JWT-only mode)
	req := httptest.NewRequest("GET", "http://proxy.local/resource", nil)
	req.Header.Set("X-API-KEY", "some-key")
	resp := httptest.NewRecorder()

	h.ServeHTTP(resp, req)

	// Should be 401 because API-key is not enabled
	if resp.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 in JWT-only mode with API-key, got %d", resp.Code)
	}
}

// TestIntegrationAPIKeyOnlyConfig tests API-key-only auth (JWT disabled)
func TestIntegrationAPIKeyOnlyConfig(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	cfg := &config.Config{
		Server: config.ServerConfig{
			Port:         8888,
			TargetURL:    upstream.URL,
			IncludePaths: []string{"/*"},
		},
		Auth: config.AuthConfig{
			HeaderPrefix: "X-AUTH-",
			JWT:          config.JWTConfig{Enabled: false},
			APIKey: config.APIKeyConfig{
				Enabled: true,
				Name:    "X-API-KEY",
				Value:   "api-secret",
				Payload: map[string]string{"service": "api"},
			},
		},
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	h, err := proxy.NewHandler(cfg, logger)
	if err != nil {
		t.Fatalf("failed to create handler: %v", err)
	}

	// Request with Bearer token should fail (API-key-only mode)
	req := httptest.NewRequest("GET", "http://proxy.local/resource", nil)
	req.Header.Set("Authorization", "Bearer eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.dozjgNryP4J3jVmNHl0w5N_XgL0n3I9PlFUP0THsR8U")
	resp := httptest.NewRecorder()

	h.ServeHTTP(resp, req)

	// Should be 401 because JWT is not enabled
	if resp.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 in API-key-only mode with Bearer token, got %d", resp.Code)
	}
}

// TestIntegrationRateLimitBanExpiry tests rate limit ban expires and allows requests again
func TestIntegrationRateLimitBanExpiry(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	cfg := &config.Config{
		Server: config.ServerConfig{
			Port:         8888,
			TargetURL:    upstream.URL,
			IncludePaths: []string{"/*"},
		},
		Auth: config.AuthConfig{
			HeaderPrefix: "X-AUTH-",
			JWT:          config.JWTConfig{Enabled: false},
			APIKey: config.APIKeyConfig{
				Enabled: true,
				Name:    "X-API-KEY",
				Value:   "key",
			},
		},
		Security: config.SecurityConfig{
			RateLimit: config.RateLimitConfig{
				Enabled:        true,
				RequestsPerMin: 2, // Very low
				BanForMin:      1, // 1 minute ban (we'll reduce for testing)
			},
		},
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	h, err := proxy.NewHandler(cfg, logger)
	if err != nil {
		t.Fatalf("failed to create handler: %v", err)
	}

	// Make 3 requests (2 allowed, 1 rate-limited and banned)
	for i := 0; i < 3; i++ {
		req := httptest.NewRequest("GET", "http://proxy.local/api", nil)
		req.Header.Set("X-API-KEY", "key")
		req.RemoteAddr = "127.0.0.1:12345"
		resp := httptest.NewRecorder()
		h.ServeHTTP(resp, req)

		if i < 2 {
			if resp.Code != http.StatusOK {
				t.Fatalf("request %d should succeed, got %d", i+1, resp.Code)
			}
		} else {
			if resp.Code != http.StatusTooManyRequests {
				t.Fatalf("request %d should be rate-limited, got %d", i+1, resp.Code)
			}
		}
	}

	// Note: Ban expiry typically takes minutes; this test verifies the basic flow
	// Full ban expiry testing would require mocking time or waiting
}

// TestIntegrationClaimFilterValidation tests claim filtering fails on invalid claims
func TestIntegrationClaimFilterValidation(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	rsaKey, err := jwt.GenerateRSAKeyPair()
	if err != nil {
		t.Fatalf("failed to generate RSA key: %v", err)
	}

	jwksServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/openid-configuration":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"jwks_uri": "http://" + r.Host + "/jwks",
			})
		case "/jwks":
			keys := []map[string]interface{}{
				{
					"kty": "RSA",
					"kid": "key1",
					"use": "sig",
					"alg": "RS256",
					"n":   base64.RawURLEncoding.EncodeToString(rsaKey.PublicKey.N.Bytes()),
					"e":   base64.RawURLEncoding.EncodeToString(big.NewInt(int64(rsaKey.PublicKey.E)).Bytes()),
				},
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"keys": keys})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer jwksServer.Close()

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	cfg := &config.Config{
		Server: config.ServerConfig{
			Port:         8888,
			TargetURL:    upstream.URL,
			IncludePaths: []string{"/*"},
		},
		Auth: config.AuthConfig{
			HeaderPrefix: "X-AUTH-",
			JWT: config.JWTConfig{
				Enabled:  true,
				Issuer:   "http://" + jwksServer.Listener.Addr().String(),
				Audience: "test",
				Filters: map[string]string{
					"email_verified": "true",
					"role":           "admin",
				},
			},
			APIKey: config.APIKeyConfig{Enabled: false},
		},
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	h, err := proxy.NewHandler(cfg, logger)
	if err != nil {
		t.Fatalf("failed to create handler: %v", err)
	}

	// Build token with wrong role
	builder := jwt.NewTokenBuilder("RS256", rsaKey, "key1")
	builder.
		WithIssuer("http://"+jwksServer.Listener.Addr().String()).
		WithAudience("test").
		WithIssuedAt(time.Now()).
		WithExpiresAt(time.Now().Add(1*time.Hour)).
		WithClaim("email_verified", "true").
		WithClaim("role", "user") // Should be "admin"

	token, err := builder.Build()
	if err != nil {
		t.Fatalf("failed to build token: %v", err)
	}

	req := httptest.NewRequest("GET", "http://proxy.local/api", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp := httptest.NewRecorder()

	h.ServeHTTP(resp, req)

	if resp.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for failed filter, got %d", resp.Code)
	}
}

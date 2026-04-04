//go:build integration

package proxy_test

import (
	"bytes"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fp8/lite-auth-proxy/internal/auth/jwt"
	"github.com/fp8/lite-auth-proxy/internal/config"
	"github.com/fp8/lite-auth-proxy/internal/proxy"
)

// ---------------------------------------------------------------------------
// Test helpers
// ---------------------------------------------------------------------------

var discardLogger = slog.New(slog.NewTextHandler(io.Discard, nil))

// setupRSAJWKS creates a mock JWKS + OIDC discovery server for RSA keys.
func setupRSAJWKS(t *testing.T, rsaKey *rsa.PrivateKey, kid string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
					"kid": kid,
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
}

// setupUpstream creates a mock upstream that records request details.
type upstreamRecord struct {
	Method  string
	Path    string
	Headers http.Header
}

func setupUpstream(t *testing.T, records *[]upstreamRecord, mu *sync.Mutex) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if records != nil {
			rec := upstreamRecord{
				Method:  r.Method,
				Path:    r.URL.Path,
				Headers: r.Header.Clone(),
			}
			mu.Lock()
			*records = append(*records, rec)
			mu.Unlock()
		}
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	}))
}

// buildJWT builds a signed RS256 JWT token with the given claims.
func buildJWT(t *testing.T, rsaKey *rsa.PrivateKey, kid, issuer, audience string, extraClaims map[string]interface{}) string {
	t.Helper()
	builder := jwt.NewTokenBuilder("RS256", rsaKey, kid)
	builder.
		WithIssuer(issuer).
		WithAudience(audience).
		WithIssuedAt(time.Now()).
		WithExpiresAt(time.Now().Add(1 * time.Hour))
	for k, v := range extraClaims {
		builder.WithClaim(k, v)
	}
	token, err := builder.Build()
	if err != nil {
		t.Fatalf("failed to build JWT: %v", err)
	}
	return token
}

// writeTempConfig writes a TOML string to a temp file and returns the path.
func writeTempConfig(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(p, []byte(content), 0644); err != nil {
		t.Fatalf("failed to write temp config: %v", err)
	}
	return p
}

// setEnvVars sets multiple env vars and returns a cleanup function.
func setEnvVars(t *testing.T, vars map[string]string) {
	t.Helper()
	for k, v := range vars {
		t.Setenv(k, v)
	}
}

// ---------------------------------------------------------------------------
// Section 1: Individual feature tests
// ---------------------------------------------------------------------------

// TestFeature_ProxyOnly tests the proxy with no auth and no rate limiting.
// All requests should pass through to the upstream.
func TestFeature_ProxyOnly(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	var records []upstreamRecord
	var mu sync.Mutex
	upstream := setupUpstream(t, &records, &mu)
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
			APIKey:       config.APIKeyConfig{Enabled: false},
		},
		Security: config.SecurityConfig{
			RateLimit: config.RateLimitConfig{Enabled: false},
		},
	}

	h, err := proxy.NewHandler(cfg, discardLogger)
	if err != nil {
		t.Fatalf("failed to create handler: %v", err)
	}

	// Request with no credentials should succeed (rate-limit-only mode with no rate limit)
	req := httptest.NewRequest("GET", "http://proxy.local/some/path", nil)
	resp := httptest.NewRecorder()
	h.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.Code, resp.Body.String())
	}
	mu.Lock()
	if len(records) != 1 {
		t.Fatalf("expected 1 upstream call, got %d", len(records))
	}
	if records[0].Path != "/some/path" {
		t.Errorf("expected upstream path /some/path, got %s", records[0].Path)
	}
	mu.Unlock()
}

// TestFeature_RateLimitOnly tests rate limiting with no auth enabled.
// Requests should pass until the rate limit is exceeded.
func TestFeature_RateLimitOnly(t *testing.T) {
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
			APIKey:       config.APIKeyConfig{Enabled: false},
		},
		Security: config.SecurityConfig{
			RateLimit: config.RateLimitConfig{
				Enabled:        true,
				RequestsPerMin: 3,
				BanForMin:      1,
			},
		},
	}

	h, err := proxy.NewHandler(cfg, discardLogger)
	if err != nil {
		t.Fatalf("failed to create handler: %v", err)
	}

	// First 3 requests should succeed, 4th should be rate-limited
	for i := 0; i < 4; i++ {
		req := httptest.NewRequest("GET", "http://proxy.local/api/data", nil)
		req.RemoteAddr = "10.0.0.1:12345"
		resp := httptest.NewRecorder()
		h.ServeHTTP(resp, req)

		if i < 3 {
			if resp.Code != http.StatusOK {
				t.Fatalf("request %d: expected 200, got %d", i+1, resp.Code)
			}
		} else {
			if resp.Code != http.StatusTooManyRequests {
				t.Fatalf("request %d: expected 429, got %d", i+1, resp.Code)
			}
		}
	}
}

// TestFeature_JWTAuthOnly tests JWT authentication with no rate limiting.
func TestFeature_JWTAuthOnly(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	rsaKey, err := jwt.GenerateRSAKeyPair()
	if err != nil {
		t.Fatalf("failed to generate RSA key: %v", err)
	}

	jwksServer := setupRSAJWKS(t, rsaKey, "jwt-only-key")
	defer jwksServer.Close()
	issuer := "http://" + jwksServer.Listener.Addr().String()

	var records []upstreamRecord
	var mu sync.Mutex
	upstream := setupUpstream(t, &records, &mu)
	defer upstream.Close()

	cfg := &config.Config{
		Server: config.ServerConfig{
			Port:         8888,
			TargetURL:    upstream.URL,
			IncludePaths: []string{"/*"},
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
				Mappings:      map[string]string{"sub": "USER-ID", "email": "USER-EMAIL"},
			},
			APIKey: config.APIKeyConfig{Enabled: false},
		},
		Security: config.SecurityConfig{
			RateLimit: config.RateLimitConfig{Enabled: false},
		},
	}

	h, err := proxy.NewHandler(cfg, discardLogger)
	if err != nil {
		t.Fatalf("failed to create handler: %v", err)
	}

	t.Run("valid JWT succeeds", func(t *testing.T) {
		token := buildJWT(t, rsaKey, "jwt-only-key", issuer, "test-aud", map[string]interface{}{
			"sub":   "user-42",
			"email": "user@test.com",
		})
		req := httptest.NewRequest("GET", "http://proxy.local/resource", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		resp := httptest.NewRecorder()
		h.ServeHTTP(resp, req)

		if resp.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", resp.Code, resp.Body.String())
		}

		mu.Lock()
		defer mu.Unlock()
		last := records[len(records)-1]
		if last.Headers.Get("X-Auth-User-Id") != "user-42" {
			t.Errorf("expected X-AUTH-USER-ID=user-42, got %s", last.Headers.Get("X-Auth-User-Id"))
		}
		if last.Headers.Get("X-Auth-User-Email") != "user@test.com" {
			t.Errorf("expected X-AUTH-USER-EMAIL=user@test.com, got %s", last.Headers.Get("X-Auth-User-Email"))
		}
	})

	t.Run("no credentials returns 401", func(t *testing.T) {
		req := httptest.NewRequest("GET", "http://proxy.local/resource", nil)
		resp := httptest.NewRecorder()
		h.ServeHTTP(resp, req)

		if resp.Code != http.StatusUnauthorized {
			t.Fatalf("expected 401, got %d", resp.Code)
		}
	})

	t.Run("invalid JWT returns 401", func(t *testing.T) {
		req := httptest.NewRequest("GET", "http://proxy.local/resource", nil)
		req.Header.Set("Authorization", "Bearer invalid.token.here")
		resp := httptest.NewRecorder()
		h.ServeHTTP(resp, req)

		if resp.Code != http.StatusUnauthorized {
			t.Fatalf("expected 401, got %d", resp.Code)
		}
	})

	t.Run("API key rejected when JWT-only", func(t *testing.T) {
		req := httptest.NewRequest("GET", "http://proxy.local/resource", nil)
		req.Header.Set("X-API-KEY", "some-key")
		resp := httptest.NewRecorder()
		h.ServeHTTP(resp, req)

		if resp.Code != http.StatusUnauthorized {
			t.Fatalf("expected 401, got %d", resp.Code)
		}
	})
}

// TestFeature_APIKeyAuthOnly tests API key authentication with no rate limiting.
func TestFeature_APIKeyAuthOnly(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	var records []upstreamRecord
	var mu sync.Mutex
	upstream := setupUpstream(t, &records, &mu)
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
				Value:   "my-secret-key",
				Payload: map[string]string{
					"service": "test-service",
					"role":    "internal",
				},
			},
		},
		Security: config.SecurityConfig{
			RateLimit: config.RateLimitConfig{Enabled: false},
		},
	}

	h, err := proxy.NewHandler(cfg, discardLogger)
	if err != nil {
		t.Fatalf("failed to create handler: %v", err)
	}

	t.Run("valid API key succeeds with payload injection", func(t *testing.T) {
		req := httptest.NewRequest("POST", "http://proxy.local/data", nil)
		req.Header.Set("X-API-KEY", "my-secret-key")
		resp := httptest.NewRecorder()
		h.ServeHTTP(resp, req)

		if resp.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", resp.Code, resp.Body.String())
		}

		mu.Lock()
		defer mu.Unlock()
		last := records[len(records)-1]
		if last.Headers.Get("X-Auth-Service") != "test-service" {
			t.Errorf("expected X-AUTH-SERVICE=test-service, got %s", last.Headers.Get("X-Auth-Service"))
		}
		if last.Headers.Get("X-Auth-Role") != "internal" {
			t.Errorf("expected X-AUTH-ROLE=internal, got %s", last.Headers.Get("X-Auth-Role"))
		}
	})

	t.Run("wrong API key returns 401", func(t *testing.T) {
		req := httptest.NewRequest("GET", "http://proxy.local/data", nil)
		req.Header.Set("X-API-KEY", "wrong-key")
		resp := httptest.NewRecorder()
		h.ServeHTTP(resp, req)

		if resp.Code != http.StatusUnauthorized {
			t.Fatalf("expected 401, got %d", resp.Code)
		}
	})

	t.Run("no credentials returns 401", func(t *testing.T) {
		req := httptest.NewRequest("GET", "http://proxy.local/data", nil)
		resp := httptest.NewRecorder()
		h.ServeHTTP(resp, req)

		if resp.Code != http.StatusUnauthorized {
			t.Fatalf("expected 401, got %d", resp.Code)
		}
	})
}

// TestFeature_AdminControlPlane tests the admin API for setting and enforcing dynamic rules.
func TestFeature_AdminControlPlane(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	rsaKey, err := jwt.GenerateRSAKeyPair()
	if err != nil {
		t.Fatalf("failed to generate RSA key: %v", err)
	}

	// JWKS server shared by both auth JWT and admin JWT
	jwksServer := setupRSAJWKS(t, rsaKey, "admin-key")
	defer jwksServer.Close()
	issuer := "http://" + jwksServer.Listener.Addr().String()

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
				Enabled:       true,
				Issuer:        issuer,
				Audience:      "test-aud",
				ToleranceSecs: 30,
				CacheTTLMins:  60,
			},
			APIKey: config.APIKeyConfig{Enabled: false},
		},
		Admin: config.AdminConfig{
			Enabled: true,
			JWT: config.JWTConfig{
				Issuer:        issuer,
				Audience:      "test-aud",
				ToleranceSecs: 30,
				CacheTTLMins:  60,
				AllowedEmails: []string{"admin@example.com"},
			},
		},
	}

	mux, deps, err := proxy.NewHandlerWithDeps(cfg, discardLogger)
	if err != nil {
		t.Fatalf("failed to create handler: %v", err)
	}
	defer deps.StopFn()

	adminToken := buildJWT(t, rsaKey, "admin-key", issuer, "test-aud", map[string]interface{}{
		"sub":   "admin-sa",
		"email": "admin@example.com",
	})

	t.Run("set block rule and verify enforcement", func(t *testing.T) {
		// Set a block rule for a specific host
		ruleBody := `{
			"command": "set-rule",
			"rule": {
				"ruleId": "block-test-host",
				"targetHost": "blocked.example.com",
				"action": "block",
				"durationSeconds": 300
			}
		}`
		req := httptest.NewRequest("POST", "http://proxy.local/admin/control", strings.NewReader(ruleBody))
		req.Header.Set("Authorization", "Bearer "+adminToken)
		req.Header.Set("Content-Type", "application/json")
		resp := httptest.NewRecorder()
		mux.ServeHTTP(resp, req)

		if resp.Code != http.StatusOK {
			t.Fatalf("set-rule: expected 200, got %d: %s", resp.Code, resp.Body.String())
		}

		// Verify request to blocked host is denied (429 from DynamicRuleCheck)
		userToken := buildJWT(t, rsaKey, "admin-key", issuer, "test-aud", map[string]interface{}{
			"sub": "user-1",
		})
		req = httptest.NewRequest("GET", "http://blocked.example.com/api", nil)
		req.Header.Set("Authorization", "Bearer "+userToken)
		resp = httptest.NewRecorder()
		mux.ServeHTTP(resp, req)

		if resp.Code != http.StatusTooManyRequests {
			t.Fatalf("blocked host: expected 429, got %d: %s", resp.Code, resp.Body.String())
		}
	})

	t.Run("admin status endpoint returns active rules", func(t *testing.T) {
		req := httptest.NewRequest("GET", "http://proxy.local/admin/status", nil)
		req.Header.Set("Authorization", "Bearer "+adminToken)
		resp := httptest.NewRecorder()
		mux.ServeHTTP(resp, req)

		if resp.Code != http.StatusOK {
			t.Fatalf("status: expected 200, got %d: %s", resp.Code, resp.Body.String())
		}

		var status struct {
			Rules []struct {
				RuleID string `json:"ruleId"`
				Status string `json:"status"`
			} `json:"rules"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&status); err != nil {
			t.Fatalf("failed to decode status: %v", err)
		}
		found := false
		for _, r := range status.Rules {
			if r.RuleID == "block-test-host" && r.Status == "active" {
				found = true
			}
		}
		if !found {
			t.Errorf("expected active rule 'block-test-host' in status, got %+v", status.Rules)
		}
	})

	t.Run("remove rule and verify unblocked", func(t *testing.T) {
		removeBody := `{"command": "remove-rule", "ruleId": "block-test-host"}`
		req := httptest.NewRequest("POST", "http://proxy.local/admin/control", strings.NewReader(removeBody))
		req.Header.Set("Authorization", "Bearer "+adminToken)
		req.Header.Set("Content-Type", "application/json")
		resp := httptest.NewRecorder()
		mux.ServeHTTP(resp, req)

		if resp.Code != http.StatusOK {
			t.Fatalf("remove-rule: expected 200, got %d: %s", resp.Code, resp.Body.String())
		}

		// Request to previously blocked host should now succeed
		userToken := buildJWT(t, rsaKey, "admin-key", issuer, "test-aud", map[string]interface{}{
			"sub": "user-1",
		})
		req = httptest.NewRequest("GET", "http://blocked.example.com/api", nil)
		req.Header.Set("Authorization", "Bearer "+userToken)
		resp = httptest.NewRecorder()
		mux.ServeHTTP(resp, req)

		if resp.Code != http.StatusOK {
			t.Fatalf("after remove: expected 200, got %d: %s", resp.Code, resp.Body.String())
		}
	})

	t.Run("admin without valid token returns 401", func(t *testing.T) {
		req := httptest.NewRequest("GET", "http://proxy.local/admin/status", nil)
		resp := httptest.NewRecorder()
		mux.ServeHTTP(resp, req)

		if resp.Code != http.StatusUnauthorized {
			t.Fatalf("expected 401 without auth, got %d", resp.Code)
		}
	})
}

// ---------------------------------------------------------------------------
// Section 2: Feature combination tests
// ---------------------------------------------------------------------------

// TestCombo_RateLimit_JWTAuth tests rate limiting combined with JWT auth.
// Valid JWT requests should be rate limited; unauthenticated requests should
// still be rate limited (IP rate limit runs before auth).
func TestCombo_RateLimit_JWTAuth(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	rsaKey, err := jwt.GenerateRSAKeyPair()
	if err != nil {
		t.Fatalf("failed to generate RSA key: %v", err)
	}

	jwksServer := setupRSAJWKS(t, rsaKey, "combo-key")
	defer jwksServer.Close()
	issuer := "http://" + jwksServer.Listener.Addr().String()

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
				Enabled:       true,
				Issuer:        issuer,
				Audience:      "test-aud",
				ToleranceSecs: 30,
				CacheTTLMins:  60,
			},
			APIKey: config.APIKeyConfig{Enabled: false},
		},
		Security: config.SecurityConfig{
			RateLimit: config.RateLimitConfig{
				Enabled:        true,
				RequestsPerMin: 3,
				BanForMin:      1,
			},
		},
	}

	h, err := proxy.NewHandler(cfg, discardLogger)
	if err != nil {
		t.Fatalf("failed to create handler: %v", err)
	}

	token := buildJWT(t, rsaKey, "combo-key", issuer, "test-aud", map[string]interface{}{
		"sub": "user-combo",
	})

	// First 3 requests with valid JWT should succeed
	for i := 0; i < 3; i++ {
		req := httptest.NewRequest("GET", "http://proxy.local/api", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		req.RemoteAddr = "10.0.0.2:12345"
		resp := httptest.NewRecorder()
		h.ServeHTTP(resp, req)

		if resp.Code != http.StatusOK {
			t.Fatalf("request %d: expected 200, got %d: %s", i+1, resp.Code, resp.Body.String())
		}
	}

	// 4th request should be rate-limited (429), not auth error
	req := httptest.NewRequest("GET", "http://proxy.local/api", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	req.RemoteAddr = "10.0.0.2:12345"
	resp := httptest.NewRecorder()
	h.ServeHTTP(resp, req)

	if resp.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429 (rate limited), got %d: %s", resp.Code, resp.Body.String())
	}
}

// TestCombo_RateLimit_APIKeyAuth tests rate limiting combined with API key auth.
func TestCombo_RateLimit_APIKeyAuth(t *testing.T) {
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
				Value:   "test-key",
			},
		},
		Security: config.SecurityConfig{
			RateLimit: config.RateLimitConfig{
				Enabled:        true,
				RequestsPerMin: 2,
				BanForMin:      1,
			},
		},
	}

	h, err := proxy.NewHandler(cfg, discardLogger)
	if err != nil {
		t.Fatalf("failed to create handler: %v", err)
	}

	// 2 requests with valid API key → OK, 3rd → rate limited
	for i := 0; i < 3; i++ {
		req := httptest.NewRequest("GET", "http://proxy.local/data", nil)
		req.Header.Set("X-API-KEY", "test-key")
		req.RemoteAddr = "10.0.0.3:12345"
		resp := httptest.NewRecorder()
		h.ServeHTTP(resp, req)

		if i < 2 {
			if resp.Code != http.StatusOK {
				t.Fatalf("request %d: expected 200, got %d", i+1, resp.Code)
			}
		} else {
			if resp.Code != http.StatusTooManyRequests {
				t.Fatalf("request %d: expected 429, got %d", i+1, resp.Code)
			}
		}
	}

	// Wrong API key from different IP should still get 401 (not rate limited)
	req := httptest.NewRequest("GET", "http://proxy.local/data", nil)
	req.Header.Set("X-API-KEY", "wrong-key")
	req.RemoteAddr = "10.0.0.99:12345"
	resp := httptest.NewRecorder()
	h.ServeHTTP(resp, req)

	if resp.Code != http.StatusUnauthorized {
		t.Fatalf("wrong API key: expected 401, got %d", resp.Code)
	}
}

// TestCombo_JWTAuth_APIKeyAuth tests both auth methods enabled simultaneously.
// JWT with Bearer takes precedence; API key is used when no Bearer is present.
func TestCombo_JWTAuth_APIKeyAuth(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	rsaKey, err := jwt.GenerateRSAKeyPair()
	if err != nil {
		t.Fatalf("failed to generate RSA key: %v", err)
	}

	jwksServer := setupRSAJWKS(t, rsaKey, "dual-auth-key")
	defer jwksServer.Close()
	issuer := "http://" + jwksServer.Listener.Addr().String()

	var records []upstreamRecord
	var mu sync.Mutex
	upstream := setupUpstream(t, &records, &mu)
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
				Mappings:      map[string]string{"sub": "USER-ID"},
			},
			APIKey: config.APIKeyConfig{
				Enabled: true,
				Name:    "X-API-KEY",
				Value:   "api-secret",
				Payload: map[string]string{"source": "api-key"},
			},
		},
	}

	h, err := proxy.NewHandler(cfg, discardLogger)
	if err != nil {
		t.Fatalf("failed to create handler: %v", err)
	}

	t.Run("JWT Bearer succeeds", func(t *testing.T) {
		token := buildJWT(t, rsaKey, "dual-auth-key", issuer, "test-aud", map[string]interface{}{
			"sub": "jwt-user",
		})
		req := httptest.NewRequest("GET", "http://proxy.local/resource", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		resp := httptest.NewRecorder()
		h.ServeHTTP(resp, req)

		if resp.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", resp.Code, resp.Body.String())
		}

		mu.Lock()
		last := records[len(records)-1]
		mu.Unlock()
		if last.Headers.Get("X-Auth-User-Id") != "jwt-user" {
			t.Errorf("expected JWT-mapped header USER-ID=jwt-user, got %s", last.Headers.Get("X-Auth-User-Id"))
		}
	})

	t.Run("API key succeeds when no Bearer", func(t *testing.T) {
		req := httptest.NewRequest("GET", "http://proxy.local/resource", nil)
		req.Header.Set("X-API-KEY", "api-secret")
		resp := httptest.NewRecorder()
		h.ServeHTTP(resp, req)

		if resp.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", resp.Code, resp.Body.String())
		}

		mu.Lock()
		last := records[len(records)-1]
		mu.Unlock()
		if last.Headers.Get("X-Auth-Source") != "api-key" {
			t.Errorf("expected API-key payload SOURCE=api-key, got %s", last.Headers.Get("X-Auth-Source"))
		}
	})

	t.Run("invalid Bearer with valid API key returns 401", func(t *testing.T) {
		// Bearer takes precedence — if invalid, API key is not tried
		req := httptest.NewRequest("GET", "http://proxy.local/resource", nil)
		req.Header.Set("Authorization", "Bearer bad.token.value")
		req.Header.Set("X-API-KEY", "api-secret")
		resp := httptest.NewRecorder()
		h.ServeHTTP(resp, req)

		if resp.Code != http.StatusUnauthorized {
			t.Fatalf("expected 401 (invalid Bearer takes precedence), got %d", resp.Code)
		}
	})

	t.Run("no credentials returns 401", func(t *testing.T) {
		req := httptest.NewRequest("GET", "http://proxy.local/resource", nil)
		resp := httptest.NewRecorder()
		h.ServeHTTP(resp, req)

		if resp.Code != http.StatusUnauthorized {
			t.Fatalf("expected 401, got %d", resp.Code)
		}
	})
}

// TestCombo_AllFeatures tests rate limiting + JWT + API key + admin all enabled.
func TestCombo_AllFeatures(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	rsaKey, err := jwt.GenerateRSAKeyPair()
	if err != nil {
		t.Fatalf("failed to generate RSA key: %v", err)
	}

	jwksServer := setupRSAJWKS(t, rsaKey, "all-key")
	defer jwksServer.Close()
	issuer := "http://" + jwksServer.Listener.Addr().String()

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	cfg := &config.Config{
		Server: config.ServerConfig{
			Port:         8888,
			TargetURL:    upstream.URL,
			IncludePaths: []string{"/*"},
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
				Mappings:      map[string]string{"sub": "USER-ID"},
			},
			APIKey: config.APIKeyConfig{
				Enabled: true,
				Name:    "X-API-KEY",
				Value:   "all-features-key",
				Payload: map[string]string{"mode": "api-key"},
			},
		},
		Security: config.SecurityConfig{
			RateLimit: config.RateLimitConfig{
				Enabled:        true,
				RequestsPerMin: 10,
				BanForMin:      1,
			},
		},
		Admin: config.AdminConfig{
			Enabled: true,
			JWT: config.JWTConfig{
				Issuer:        issuer,
				Audience:      "test-aud",
				ToleranceSecs: 30,
				CacheTTLMins:  60,
				AllowedEmails: []string{"admin@all.com"},
			},
		},
	}

	mux, deps, err := proxy.NewHandlerWithDeps(cfg, discardLogger)
	if err != nil {
		t.Fatalf("failed to create handler: %v", err)
	}
	defer deps.StopFn()

	t.Run("JWT auth works with all features enabled", func(t *testing.T) {
		token := buildJWT(t, rsaKey, "all-key", issuer, "test-aud", map[string]interface{}{
			"sub": "user-all",
		})
		req := httptest.NewRequest("GET", "http://proxy.local/api", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		req.RemoteAddr = "10.0.1.1:12345"
		resp := httptest.NewRecorder()
		mux.ServeHTTP(resp, req)

		if resp.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", resp.Code, resp.Body.String())
		}
	})

	t.Run("API key auth works with all features enabled", func(t *testing.T) {
		req := httptest.NewRequest("GET", "http://proxy.local/api", nil)
		req.Header.Set("X-API-KEY", "all-features-key")
		req.RemoteAddr = "10.0.1.2:12345"
		resp := httptest.NewRecorder()
		mux.ServeHTTP(resp, req)

		if resp.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", resp.Code, resp.Body.String())
		}
	})

	t.Run("admin can set block rule while all features active", func(t *testing.T) {
		adminToken := buildJWT(t, rsaKey, "all-key", issuer, "test-aud", map[string]interface{}{
			"sub":   "admin-sa",
			"email": "admin@all.com",
		})

		ruleBody := `{
			"command": "set-rule",
			"rule": {
				"ruleId": "block-all-test",
				"targetHost": "all.blocked.com",
				"action": "block",
				"durationSeconds": 60
			}
		}`
		req := httptest.NewRequest("POST", "http://proxy.local/admin/control", strings.NewReader(ruleBody))
		req.Header.Set("Authorization", "Bearer "+adminToken)
		req.Header.Set("Content-Type", "application/json")
		resp := httptest.NewRecorder()
		mux.ServeHTTP(resp, req)

		if resp.Code != http.StatusOK {
			t.Fatalf("admin set-rule: expected 200, got %d: %s", resp.Code, resp.Body.String())
		}

		// Verify the blocked host is enforced
		userToken := buildJWT(t, rsaKey, "all-key", issuer, "test-aud", map[string]interface{}{
			"sub": "user-blocked",
		})
		req = httptest.NewRequest("GET", "http://all.blocked.com/api", nil)
		req.Header.Set("Authorization", "Bearer "+userToken)
		req.RemoteAddr = "10.0.1.3:12345"
		resp = httptest.NewRecorder()
		mux.ServeHTTP(resp, req)

		if resp.Code != http.StatusTooManyRequests {
			t.Fatalf("blocked host: expected 429, got %d: %s", resp.Code, resp.Body.String())
		}
	})

	t.Run("excluded path bypasses auth with all features", func(t *testing.T) {
		req := httptest.NewRequest("GET", "http://proxy.local/healthz", nil)
		req.RemoteAddr = "10.0.1.4:12345"
		resp := httptest.NewRecorder()
		mux.ServeHTTP(resp, req)

		if resp.Code != http.StatusOK {
			t.Fatalf("healthz: expected 200, got %d: %s", resp.Code, resp.Body.String())
		}
	})
}

// ---------------------------------------------------------------------------
// Section 3: Config file loading integration tests
// ---------------------------------------------------------------------------

// TestConfigFile_RateLimitOnlyFromTOML loads a TOML config with only rate limiting
// (no auth) and verifies the proxy enforces it.
func TestConfigFile_RateLimitOnlyFromTOML(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	toml := `
[server]
port = 8888
target_url = "` + upstream.URL + `"
include_paths = ["/*"]

[security.rate_limit]
enabled = true
requests_per_min = 2
ban_for_min = 1

[auth.jwt]
enabled = false

[auth.api_key]
enabled = false
`
	cfgPath := writeTempConfig(t, toml)
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("failed to load config: %v", err)
	}

	h, err := proxy.NewHandler(cfg, discardLogger)
	if err != nil {
		t.Fatalf("failed to create handler: %v", err)
	}

	// 2 requests pass, 3rd is rate-limited
	for i := 0; i < 3; i++ {
		req := httptest.NewRequest("GET", "http://proxy.local/test", nil)
		req.RemoteAddr = "10.0.2.1:12345"
		resp := httptest.NewRecorder()
		h.ServeHTTP(resp, req)

		if i < 2 && resp.Code != http.StatusOK {
			t.Fatalf("request %d: expected 200, got %d", i+1, resp.Code)
		}
		if i == 2 && resp.Code != http.StatusTooManyRequests {
			t.Fatalf("request %d: expected 429, got %d", i+1, resp.Code)
		}
	}
}

// TestConfigFile_JWTFromTOML loads a TOML config with JWT auth and verifies authentication.
func TestConfigFile_JWTFromTOML(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	rsaKey, err := jwt.GenerateRSAKeyPair()
	if err != nil {
		t.Fatalf("failed to generate RSA key: %v", err)
	}

	jwksServer := setupRSAJWKS(t, rsaKey, "toml-key")
	defer jwksServer.Close()
	issuer := "http://" + jwksServer.Listener.Addr().String()

	var records []upstreamRecord
	var mu sync.Mutex
	upstream := setupUpstream(t, &records, &mu)
	defer upstream.Close()

	toml := `
[server]
port = 8888
target_url = "` + upstream.URL + `"
include_paths = ["/*"]

[security.rate_limit]
enabled = false

[auth]
header_prefix = "X-AUTH-"

[auth.jwt]
enabled = true
issuer = "` + issuer + `"
audience = "toml-aud"
tolerance_secs = 30
cache_ttl_mins = 60

[auth.jwt.mappings]
sub = "USER-ID"
email = "USER-EMAIL"

[auth.api_key]
enabled = false
`
	cfgPath := writeTempConfig(t, toml)
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("failed to load config: %v", err)
	}

	h, err := proxy.NewHandler(cfg, discardLogger)
	if err != nil {
		t.Fatalf("failed to create handler: %v", err)
	}

	token := buildJWT(t, rsaKey, "toml-key", issuer, "toml-aud", map[string]interface{}{
		"sub":   "toml-user",
		"email": "toml@test.com",
	})

	req := httptest.NewRequest("GET", "http://proxy.local/resource", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp := httptest.NewRecorder()
	h.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.Code, resp.Body.String())
	}

	mu.Lock()
	defer mu.Unlock()
	last := records[len(records)-1]
	if last.Headers.Get("X-Auth-User-Id") != "toml-user" {
		t.Errorf("expected X-AUTH-USER-ID=toml-user, got %s", last.Headers.Get("X-Auth-User-Id"))
	}
	if last.Headers.Get("X-Auth-User-Email") != "toml@test.com" {
		t.Errorf("expected X-AUTH-USER-EMAIL=toml@test.com, got %s", last.Headers.Get("X-Auth-User-Email"))
	}
}

// TestConfigFile_APIKeyFromTOML loads a TOML config with API key auth and verifies it.
func TestConfigFile_APIKeyFromTOML(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	toml := `
[server]
port = 8888
target_url = "` + upstream.URL + `"
include_paths = ["/*"]

[security.rate_limit]
enabled = false

[auth]
header_prefix = "X-AUTH-"

[auth.jwt]
enabled = false

[auth.api_key]
enabled = true
name = "X-API-KEY"
value = "toml-secret"

[auth.api_key.payload]
service = "toml-service"
`
	cfgPath := writeTempConfig(t, toml)
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("failed to load config: %v", err)
	}

	h, err := proxy.NewHandler(cfg, discardLogger)
	if err != nil {
		t.Fatalf("failed to create handler: %v", err)
	}

	// Valid API key
	req := httptest.NewRequest("GET", "http://proxy.local/data", nil)
	req.Header.Set("X-API-KEY", "toml-secret")
	resp := httptest.NewRecorder()
	h.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.Code, resp.Body.String())
	}

	// Invalid API key
	req = httptest.NewRequest("GET", "http://proxy.local/data", nil)
	req.Header.Set("X-API-KEY", "wrong")
	resp = httptest.NewRecorder()
	h.ServeHTTP(resp, req)

	if resp.Code != http.StatusUnauthorized {
		t.Fatalf("wrong key: expected 401, got %d", resp.Code)
	}
}

// TestConfigFile_EnvVarOverrideChangesFeature verifies that env vars can toggle
// features on/off, overriding the TOML file.
func TestConfigFile_EnvVarOverrideChangesFeature(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	// TOML has JWT enabled, rate limit disabled, API key disabled
	tomlContent := `
[server]
port = 8888
target_url = "` + upstream.URL + `"
include_paths = ["/*"]

[security.rate_limit]
enabled = false

[auth.jwt]
enabled = true
issuer = "https://not-used.example.com"
audience = "not-used"

[auth.api_key]
enabled = false
name = "X-API-KEY"
value = "not-set"
`

	t.Run("disable JWT and enable API key via env vars", func(t *testing.T) {
		// Override: disable JWT, enable API key with value
		setEnvVars(t, map[string]string{
			"PROXY_AUTH_JWT_ENABLED":     "false",
			"PROXY_AUTH_API_KEY_ENABLED": "true",
			"PROXY_AUTH_API_KEY_VALUE":   "env-secret",
		})

		cfgPath := writeTempConfig(t, tomlContent)
		cfg, err := config.Load(cfgPath)
		if err != nil {
			t.Fatalf("failed to load config: %v", err)
		}

		if cfg.Auth.JWT.Enabled {
			t.Fatal("expected JWT to be disabled via env override")
		}
		if !cfg.Auth.APIKey.Enabled {
			t.Fatal("expected API key to be enabled via env override")
		}
		if cfg.Auth.APIKey.Value != "env-secret" {
			t.Fatalf("expected API key value 'env-secret', got %q", cfg.Auth.APIKey.Value)
		}

		h, err := proxy.NewHandler(cfg, discardLogger)
		if err != nil {
			t.Fatalf("failed to create handler: %v", err)
		}

		// API key should now work
		req := httptest.NewRequest("GET", "http://proxy.local/resource", nil)
		req.Header.Set("X-API-KEY", "env-secret")
		resp := httptest.NewRecorder()
		h.ServeHTTP(resp, req)

		if resp.Code != http.StatusOK {
			t.Fatalf("expected 200 with env-overridden API key, got %d: %s", resp.Code, resp.Body.String())
		}
	})

	t.Run("enable rate limit via env var", func(t *testing.T) {
		setEnvVars(t, map[string]string{
			"PROXY_AUTH_JWT_ENABLED":                     "false",
			"PROXY_AUTH_API_KEY_ENABLED":                 "false",
			"PROXY_SECURITY_RATE_LIMIT_ENABLED":          "true",
			"PROXY_SECURITY_RATE_LIMIT_REQUESTS_PER_MIN": "2",
		})

		cfgPath := writeTempConfig(t, tomlContent)
		cfg, err := config.Load(cfgPath)
		if err != nil {
			t.Fatalf("failed to load config: %v", err)
		}

		if !cfg.Security.RateLimit.Enabled {
			t.Fatal("expected rate limit enabled via env override")
		}
		if cfg.Security.RateLimit.RequestsPerMin != 2 {
			t.Fatalf("expected RPM=2, got %d", cfg.Security.RateLimit.RequestsPerMin)
		}

		h, err := proxy.NewHandler(cfg, discardLogger)
		if err != nil {
			t.Fatalf("failed to create handler: %v", err)
		}

		// 2 requests pass, 3rd rate limited
		for i := 0; i < 3; i++ {
			req := httptest.NewRequest("GET", "http://proxy.local/test", nil)
			req.RemoteAddr = "10.0.3.1:12345"
			resp := httptest.NewRecorder()
			h.ServeHTTP(resp, req)

			if i < 2 && resp.Code != http.StatusOK {
				t.Fatalf("request %d: expected 200, got %d", i+1, resp.Code)
			}
			if i == 2 && resp.Code != http.StatusTooManyRequests {
				t.Fatalf("request %d: expected 429, got %d", i+1, resp.Code)
			}
		}
	})
}

// TestConfigFile_EnvVarSubstitutionInTOML verifies {{ENV.VAR}} substitution
// in config values creates a working proxy.
func TestConfigFile_EnvVarSubstitutionInTOML(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	rsaKey, err := jwt.GenerateRSAKeyPair()
	if err != nil {
		t.Fatalf("failed to generate RSA key: %v", err)
	}

	jwksServer := setupRSAJWKS(t, rsaKey, "subst-key")
	defer jwksServer.Close()
	issuer := "http://" + jwksServer.Listener.Addr().String()

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	// Set env vars that will be substituted into the TOML
	setEnvVars(t, map[string]string{
		"TEST_JWT_ISSUER":  issuer,
		"TEST_JWT_AUD":     "subst-aud",
		"TEST_API_KEY_VAL": "substituted-key",
	})

	toml := `
[server]
port = 8888
target_url = "` + upstream.URL + `"
include_paths = ["/*"]

[security.rate_limit]
enabled = false

[auth]
header_prefix = "X-AUTH-"

[auth.jwt]
enabled = true
issuer = "{{ENV.TEST_JWT_ISSUER}}"
audience = "{{ENV.TEST_JWT_AUD}}"

[auth.api_key]
enabled = true
name = "X-API-KEY"
value = "{{ENV.TEST_API_KEY_VAL}}"
`
	cfgPath := writeTempConfig(t, toml)
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("failed to load config: %v", err)
	}

	// Verify substitution worked
	if cfg.Auth.JWT.Issuer != issuer {
		t.Fatalf("issuer not substituted: got %q", cfg.Auth.JWT.Issuer)
	}
	if cfg.Auth.JWT.Audience != "subst-aud" {
		t.Fatalf("audience not substituted: got %q", cfg.Auth.JWT.Audience)
	}
	if cfg.Auth.APIKey.Value != "substituted-key" {
		t.Fatalf("api key not substituted: got %q", cfg.Auth.APIKey.Value)
	}

	h, err := proxy.NewHandler(cfg, discardLogger)
	if err != nil {
		t.Fatalf("failed to create handler: %v", err)
	}

	// JWT auth should work with substituted issuer/audience
	token := buildJWT(t, rsaKey, "subst-key", issuer, "subst-aud", map[string]interface{}{
		"sub": "subst-user",
	})
	req := httptest.NewRequest("GET", "http://proxy.local/resource", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp := httptest.NewRecorder()
	h.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("JWT with substituted config: expected 200, got %d: %s", resp.Code, resp.Body.String())
	}

	// API key auth should also work
	req = httptest.NewRequest("GET", "http://proxy.local/resource", nil)
	req.Header.Set("X-API-KEY", "substituted-key")
	resp = httptest.NewRecorder()
	h.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("API key with substituted config: expected 200, got %d: %s", resp.Code, resp.Body.String())
	}
}

// TestConfigFile_RealConfigTOMLWithOverrides loads the actual config/config.toml
// from the repository, overrides settings via env vars, and verifies the proxy works.
func TestConfigFile_RealConfigTOMLWithOverrides(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	rsaKey, err := jwt.GenerateRSAKeyPair()
	if err != nil {
		t.Fatalf("failed to generate RSA key: %v", err)
	}

	jwksServer := setupRSAJWKS(t, rsaKey, "real-cfg-key")
	defer jwksServer.Close()
	issuer := "http://" + jwksServer.Listener.Addr().String()

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	// The real config.toml uses {{ENV.GOOGLE_CLOUD_PROJECT}} for JWT issuer/audience
	// and has JWT enabled by default. Override to point at our test infrastructure.
	setEnvVars(t, map[string]string{
		"GOOGLE_CLOUD_PROJECT": "test-project",
		// Override the JWT issuer/audience to our mock JWKS server
		"PROXY_AUTH_JWT_ISSUER":     issuer,
		"PROXY_AUTH_JWT_AUDIENCE":   "test-project",
		"PROXY_SERVER_TARGET_URL":   upstream.URL,
		"PROXY_SERVER_STRIP_PREFIX": "",
	})

	// Load the actual config/config.toml from the repository
	cfgPath := filepath.Join("..", "..", "config", "config.toml")
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("failed to load real config.toml: %v", err)
	}

	// Verify env overrides took effect
	if cfg.Auth.JWT.Issuer != issuer {
		t.Fatalf("expected issuer override to %q, got %q", issuer, cfg.Auth.JWT.Issuer)
	}
	if cfg.Server.TargetURL != upstream.URL {
		t.Fatalf("expected target_url override to %q, got %q", upstream.URL, cfg.Server.TargetURL)
	}

	h, err := proxy.NewHandler(cfg, discardLogger)
	if err != nil {
		t.Fatalf("failed to create handler: %v", err)
	}

	// JWT auth with the overridden config should work
	token := buildJWT(t, rsaKey, "real-cfg-key", issuer, "test-project", map[string]interface{}{
		"sub":   "real-user",
		"email": "user@test.com",
	})
	req := httptest.NewRequest("GET", "http://proxy.local/some-path", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	req.RemoteAddr = "10.0.4.1:12345"
	resp := httptest.NewRecorder()
	h.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("real config.toml with overrides: expected 200, got %d: %s", resp.Code, resp.Body.String())
	}

	// Health check endpoint should be accessible without auth (excluded path)
	req = httptest.NewRequest("GET", "http://proxy.local/healthz", nil)
	resp = httptest.NewRecorder()
	h.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("healthz: expected 200, got %d", resp.Code)
	}
}

// TestConfigFile_EnvVarOverrideMappingsAndFilters tests that JWT mappings and
// filters can be added/overridden via env vars.
func TestConfigFile_EnvVarOverrideMappingsAndFilters(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	rsaKey, err := jwt.GenerateRSAKeyPair()
	if err != nil {
		t.Fatalf("failed to generate RSA key: %v", err)
	}

	jwksServer := setupRSAJWKS(t, rsaKey, "map-key")
	defer jwksServer.Close()
	issuer := "http://" + jwksServer.Listener.Addr().String()

	var records []upstreamRecord
	var mu sync.Mutex
	upstream := setupUpstream(t, &records, &mu)
	defer upstream.Close()

	// Base TOML has minimal mappings
	toml := `
[server]
port = 8888
target_url = "` + upstream.URL + `"
include_paths = ["/*"]

[auth]
header_prefix = "X-AUTH-"

[auth.jwt]
enabled = true
issuer = "` + issuer + `"
audience = "map-aud"

[auth.jwt.mappings]
sub = "USER-ID"

[auth.jwt.filters]
email_verified = "true"

[auth.api_key]
enabled = false
`

	// Override: add a new mapping and a new filter via env var
	setEnvVars(t, map[string]string{
		"PROXY_AUTH_JWT_MAPPINGS_ROLE":    "USER-ROLE",
		"PROXY_AUTH_JWT_FILTERS_ROLE":     "admin",
	})

	cfgPath := writeTempConfig(t, toml)
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("failed to load config: %v", err)
	}

	// Verify the env overrides added the new mapping and filter
	if cfg.Auth.JWT.Mappings["role"] != "USER-ROLE" {
		t.Fatalf("expected role mapping added via env, got %v", cfg.Auth.JWT.Mappings)
	}
	if cfg.Auth.JWT.Filters["role"] != "admin" {
		t.Fatalf("expected role filter added via env, got %v", cfg.Auth.JWT.Filters)
	}

	h, err := proxy.NewHandler(cfg, discardLogger)
	if err != nil {
		t.Fatalf("failed to create handler: %v", err)
	}

	t.Run("token with matching claims succeeds", func(t *testing.T) {
		token := buildJWT(t, rsaKey, "map-key", issuer, "map-aud", map[string]interface{}{
			"sub":            "user-mapped",
			"role":           "admin",
			"email_verified": "true",
		})
		req := httptest.NewRequest("GET", "http://proxy.local/resource", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		resp := httptest.NewRecorder()
		h.ServeHTTP(resp, req)

		if resp.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", resp.Code, resp.Body.String())
		}

		mu.Lock()
		last := records[len(records)-1]
		mu.Unlock()
		if last.Headers.Get("X-Auth-User-Role") != "admin" {
			t.Errorf("expected X-AUTH-USER-ROLE=admin, got %s", last.Headers.Get("X-Auth-User-Role"))
		}
	})

	t.Run("token with wrong role rejected by env-added filter", func(t *testing.T) {
		token := buildJWT(t, rsaKey, "map-key", issuer, "map-aud", map[string]interface{}{
			"sub":            "user-wrong-role",
			"role":           "viewer",
			"email_verified": "true",
		})
		req := httptest.NewRequest("GET", "http://proxy.local/resource", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		resp := httptest.NewRecorder()
		h.ServeHTTP(resp, req)

		if resp.Code != http.StatusUnauthorized {
			t.Fatalf("expected 401 (role filter), got %d", resp.Code)
		}
	})
}

// TestConfigFile_AdminFromTOML loads admin config from TOML and verifies the
// admin API works end-to-end.
func TestConfigFile_AdminFromTOML(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	rsaKey, err := jwt.GenerateRSAKeyPair()
	if err != nil {
		t.Fatalf("failed to generate RSA key: %v", err)
	}

	jwksServer := setupRSAJWKS(t, rsaKey, "admin-toml-key")
	defer jwksServer.Close()
	issuer := "http://" + jwksServer.Listener.Addr().String()

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	toml := `
[server]
port = 8888
target_url = "` + upstream.URL + `"
include_paths = ["/*"]

[security.rate_limit]
enabled = false

[auth]
header_prefix = "X-AUTH-"

[auth.jwt]
enabled = true
issuer = "` + issuer + `"
audience = "admin-toml-aud"

[auth.api_key]
enabled = false

[admin]
enabled = true

[admin.jwt]
issuer = "` + issuer + `"
audience = "admin-toml-aud"
allowed_emails = ["admin@toml-test.com"]
`
	cfgPath := writeTempConfig(t, toml)
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("failed to load config: %v", err)
	}

	mux, deps, err := proxy.NewHandlerWithDeps(cfg, discardLogger)
	if err != nil {
		t.Fatalf("failed to create handler: %v", err)
	}
	defer deps.StopFn()

	adminToken := buildJWT(t, rsaKey, "admin-toml-key", issuer, "admin-toml-aud", map[string]interface{}{
		"sub":   "admin-sa",
		"email": "admin@toml-test.com",
	})

	// Set a throttle rule
	ruleBody, _ := json.Marshal(map[string]interface{}{
		"command": "set-rule",
		"rule": map[string]interface{}{
			"ruleId":          "throttle-test",
			"targetHost":      "throttle.example.com",
			"action":          "throttle",
			"maxRPM":          2,
			"durationSeconds": 120,
		},
	})
	req := httptest.NewRequest("POST", "http://proxy.local/admin/control", bytes.NewReader(ruleBody))
	req.Header.Set("Authorization", "Bearer "+adminToken)
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("set-rule: expected 200, got %d: %s", resp.Code, resp.Body.String())
	}

	// Make requests to the throttled host
	userToken := buildJWT(t, rsaKey, "admin-toml-key", issuer, "admin-toml-aud", map[string]interface{}{
		"sub": "user-throttled",
	})

	for i := 0; i < 3; i++ {
		req = httptest.NewRequest("GET", "http://throttle.example.com/api", nil)
		req.Header.Set("Authorization", "Bearer "+userToken)
		req.RemoteAddr = "10.0.5.1:12345"
		resp = httptest.NewRecorder()
		mux.ServeHTTP(resp, req)

		if i < 2 && resp.Code != http.StatusOK {
			t.Fatalf("throttle request %d: expected 200, got %d", i+1, resp.Code)
		}
		if i == 2 && resp.Code != http.StatusTooManyRequests {
			t.Fatalf("throttle request %d: expected 429, got %d: %s", i+1, resp.Code, resp.Body.String())
		}
	}
}

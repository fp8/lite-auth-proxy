package proxy

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/fp8/lite-auth-proxy/internal/auth/jwt"
	"github.com/fp8/lite-auth-proxy/internal/config"
)

type oidcConfig struct {
	JWKSUri string `json:"jwks_uri"`
}

type jwksResponse struct {
	Keys []jwt.JWK `json:"keys"`
}

func TestProxyJWTFlowWithMappingAndRewrite(t *testing.T) {
	rsaKey, err := jwt.GenerateRSAKeyPair()
	if err != nil {
		t.Fatalf("failed to generate RSA key: %v", err)
	}

	jwksServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/openid-configuration":
			config := oidcConfig{JWKSUri: "http://" + r.Host + "/jwks"}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(config)
		case "/jwks":
			jwks := jwksResponse{Keys: []jwt.JWK{
				{
					KTy: "RSA",
					Kid: "test-key",
					Use: "sig",
					Alg: "RS256",
					N:   base64.RawURLEncoding.EncodeToString(rsaKey.N.Bytes()),
					E:   base64.RawURLEncoding.EncodeToString(big.NewInt(int64(rsaKey.E)).Bytes()),
				},
			}}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(jwks)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer jwksServer.Close()

	issuer := "http://" + jwksServer.Listener.Addr().String()

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/users" {
			t.Fatalf("expected rewritten path /users, got %s", r.URL.Path)
		}
		if r.Header.Get("X-AUTH-USER-ID") != "user-123" {
			t.Fatalf("expected mapped header X-AUTH-USER-ID, got %s", r.Header.Get("X-AUTH-USER-ID"))
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	cfg := &config.Config{
		Server: config.ServerConfig{
			Port:         8888,
			TargetURL:    upstream.URL,
			StripPrefix:  "/api",
			IncludePaths: []string{"/api/*"},
		},
		Auth: config.AuthConfig{
			HeaderPrefix: "X-AUTH-",
			JWT: config.JWTConfig{
				Enabled:       true,
				Issuer:        issuer,
				Audience:      "test-aud",
				ToleranceSecs: 30,
				CacheTTLMins:  60,
				Filters:       map[string]string{"role": "admin"},
				Mappings:      map[string]string{"sub": "USER-ID"},
			},
			APIKey: config.APIKeyConfig{Enabled: false},
		},
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	h, err := NewHandler(cfg, logger)
	if err != nil {
		t.Fatalf("failed to create handler: %v", err)
	}

	builder := jwt.NewTokenBuilder("RS256", rsaKey, "test-key")
	builder.WithIssuer(issuer).
		WithAudience("test-aud").
		WithIssuedAt(time.Now()).
		WithExpiresAt(time.Now().Add(1*time.Hour)).
		WithClaim("sub", "user-123").
		WithClaim("role", "admin")

	token, err := builder.Build()
	if err != nil {
		t.Fatalf("failed to build token: %v", err)
	}

	req := httptest.NewRequest("GET", "http://proxy.local/api/users", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp := httptest.NewRecorder()

	h.ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", resp.Code)
	}
}

func TestProxyAPIKeyFlow(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-AUTH-SERVICE") != "internal" {
			t.Fatalf("expected X-AUTH-SERVICE header, got %s", r.Header.Get("X-AUTH-SERVICE"))
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
			JWT:          config.JWTConfig{Enabled: false},
			APIKey: config.APIKeyConfig{
				Enabled: true,
				Name:    "X-API-KEY",
				Value:   "secret",
				Payload: map[string]string{"service": "internal"},
			},
		},
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	h, err := NewHandler(cfg, logger)
	if err != nil {
		t.Fatalf("failed to create handler: %v", err)
	}

	req := httptest.NewRequest("GET", "http://proxy.local/anything", nil)
	req.Header.Set("X-API-KEY", "secret")
	resp := httptest.NewRecorder()

	h.ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", resp.Code)
	}
}

func TestProxyMissingCredentials(t *testing.T) {
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
			JWT:          config.JWTConfig{Enabled: true, Issuer: "https://example.com", Audience: "aud"},
			APIKey:       config.APIKeyConfig{Enabled: false},
		},
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	h, err := NewHandler(cfg, logger)
	if err != nil {
		t.Fatalf("failed to create handler: %v", err)
	}

	req := httptest.NewRequest("GET", "http://proxy.local/secure", nil)
	resp := httptest.NewRecorder()

	h.ServeHTTP(resp, req)
	if resp.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", resp.Code)
	}
}

func TestProxyHealthCheckLocal(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	cfg := &config.Config{
		Server: config.ServerConfig{
			Port:      8888,
			TargetURL: upstream.URL,
			HealthCheck: config.HealthCheck{
				Path: "/healthz",
			},
		},
		Auth: config.AuthConfig{
			HeaderPrefix: "X-AUTH-",
			JWT:          config.JWTConfig{Enabled: false},
			APIKey:       config.APIKeyConfig{Enabled: false},
		},
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	h, err := NewHandler(cfg, logger)
	if err != nil {
		t.Fatalf("failed to create handler: %v", err)
	}

	req := httptest.NewRequest("GET", "http://proxy.local/healthz", nil)
	resp := httptest.NewRecorder()

	h.ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.Code)
	}
	if !strings.Contains(resp.Body.String(), "ok") {
		t.Fatalf("expected health response to contain ok, got %s", resp.Body.String())
	}
}

func TestProxyHealthCheckProxyTarget(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusAccepted)
	}))
	defer target.Close()

	parsedTarget, err := url.Parse(target.URL + "/health")
	if err != nil {
		t.Fatalf("failed to parse target: %v", err)
	}

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	cfg := &config.Config{
		Server: config.ServerConfig{
			Port:      8888,
			TargetURL: upstream.URL,
			HealthCheck: config.HealthCheck{
				Path:   "/healthz",
				Target: parsedTarget.String(),
			},
		},
		Auth: config.AuthConfig{
			HeaderPrefix: "X-AUTH-",
			JWT:          config.JWTConfig{Enabled: false},
			APIKey:       config.APIKeyConfig{Enabled: false},
		},
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	h, err := NewHandler(cfg, logger)
	if err != nil {
		t.Fatalf("failed to create handler: %v", err)
	}

	req := httptest.NewRequest("GET", "http://proxy.local/healthz", nil)
	resp := httptest.NewRecorder()

	h.ServeHTTP(resp, req)
	if resp.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d", resp.Code)
	}
}

func TestProxyJWTRateLimitingPerUser(t *testing.T) {
	rsaKey, err := jwt.GenerateRSAKeyPair()
	if err != nil {
		t.Fatalf("failed to generate RSA key: %v", err)
	}

	jwksServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/openid-configuration":
			config := oidcConfig{JWKSUri: "http://" + r.Host + "/jwks"}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(config)
		case "/jwks":
			jwks := jwksResponse{Keys: []jwt.JWK{
				{
					KTy: "RSA",
					Kid: "test-key",
					Use: "sig",
					Alg: "RS256",
					N:   base64.RawURLEncoding.EncodeToString(rsaKey.N.Bytes()),
					E:   base64.RawURLEncoding.EncodeToString(big.NewInt(int64(rsaKey.E)).Bytes()),
				},
			}}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(jwks)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer jwksServer.Close()

	issuer := "http://" + jwksServer.Listener.Addr().String()
	requestCount := 0

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount++
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	cfg := &config.Config{
		Server: config.ServerConfig{
			Port:         8888,
			TargetURL:    upstream.URL,
			IncludePaths: []string{"/*"},
		},
		Security: config.SecurityConfig{
			RateLimit: config.RateLimitConfig{
				Enabled:        true,
				RequestsPerMin: 2, // Allow only 2 requests per minute per user
				BanForMin:      1,
			},
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
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	h, err := NewHandler(cfg, logger)
	if err != nil {
		t.Fatalf("failed to create handler: %v", err)
	}

	// Create token for user alice
	builderAlice := jwt.NewTokenBuilder("RS256", rsaKey, "test-key")
	builderAlice.WithIssuer(issuer).
		WithAudience("test-aud").
		WithIssuedAt(time.Now()).
		WithExpiresAt(time.Now().Add(1*time.Hour)).
		WithClaim("sub", "alice")

	tokenAlice, err := builderAlice.Build()
	if err != nil {
		t.Fatalf("failed to build token for alice: %v", err)
	}

	// Create token for user bob
	builderBob := jwt.NewTokenBuilder("RS256", rsaKey, "test-key")
	builderBob.WithIssuer(issuer).
		WithAudience("test-aud").
		WithIssuedAt(time.Now()).
		WithExpiresAt(time.Now().Add(1*time.Hour)).
		WithClaim("sub", "bob")

	tokenBob, err := builderBob.Build()
	if err != nil {
		t.Fatalf("failed to build token for bob: %v", err)
	}

	// Both requests come from the same IP (RemoteAddr)
	clientIP := "203.0.113.50:1234"

	// Alice makes 2 requests (should succeed - at limit)
	for i := 0; i < 2; i++ {
		req := httptest.NewRequest("GET", "http://proxy.local/resource", nil)
		req.RemoteAddr = clientIP
		req.Header.Set("Authorization", "Bearer "+tokenAlice)
		resp := httptest.NewRecorder()

		h.ServeHTTP(resp, req)
		if resp.Code != http.StatusOK {
			t.Fatalf("alice request %d: expected 200, got %d", i+1, resp.Code)
		}
	}

	// Alice's 3rd request should be rate limited
	req := httptest.NewRequest("GET", "http://proxy.local/resource", nil)
	req.RemoteAddr = clientIP
	req.Header.Set("Authorization", "Bearer "+tokenAlice)
	resp := httptest.NewRecorder()

	h.ServeHTTP(resp, req)
	if resp.Code != http.StatusTooManyRequests {
		t.Fatalf("alice 3rd request: expected 429, got %d", resp.Code)
	}
	if resp.Header().Get("Retry-After") == "" {
		t.Fatal("expected Retry-After header for alice")
	}

	// Bob (different user, same IP) should have independent rate limit
	// Bob makes 2 requests (should succeed)
	for i := 0; i < 2; i++ {
		req := httptest.NewRequest("GET", "http://proxy.local/resource", nil)
		req.RemoteAddr = clientIP // Same IP as Alice
		req.Header.Set("Authorization", "Bearer "+tokenBob)
		resp := httptest.NewRecorder()

		h.ServeHTTP(resp, req)
		if resp.Code != http.StatusOK {
			t.Fatalf("bob request %d: expected 200, got %d (should have independent rate limit from alice)", i+1, resp.Code)
		}
	}

	// Bob's 3rd request should now be rate limited
	req = httptest.NewRequest("GET", "http://proxy.local/resource", nil)
	req.RemoteAddr = clientIP
	req.Header.Set("Authorization", "Bearer "+tokenBob)
	resp = httptest.NewRecorder()

	h.ServeHTTP(resp, req)
	if resp.Code != http.StatusTooManyRequests {
		t.Fatalf("bob 3rd request: expected 429, got %d", resp.Code)
	}

	// Verify that 4 requests actually made it to upstream (2 alice + 2 bob)
	if requestCount != 4 {
		t.Fatalf("expected 4 upstream requests, got %d", requestCount)
	}
}

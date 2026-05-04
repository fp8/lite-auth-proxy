package proxy

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/fp8/lite-auth-proxy/internal/ratelimit"
)

func newTestLimiter(rpm int) *ratelimit.RateLimiter {
	return ratelimit.NewRateLimiter(ratelimit.RateLimiterConfig{
		Name: "test", Enabled: true, RequestsPerMin: rpm, BanDuration: 5 * time.Minute,
	})
}

func TestHeaderSanitizerStripsPrefix(t *testing.T) {
	h := HeaderSanitizer("X-AUTH-")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-AUTH-USER") != "" {
			t.Fatal("expected X-AUTH-USER header to be stripped")
		}
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "http://example.com", nil)
	req.Header.Set("X-AUTH-USER", "alice")

	resp := httptest.NewRecorder()
	h.ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", resp.Code)
	}
}

func TestShouldAuthenticateExcludeWins(t *testing.T) {
	requires := ShouldAuthenticate("/healthz", []string{"/*"}, []string{"/healthz"})
	if requires {
		t.Fatal("expected excluded path to bypass auth")
	}
}

func TestShouldAuthenticateIncludeMatch(t *testing.T) {
	requires := ShouldAuthenticate("/api/users", []string{"/api/*"}, []string{})
	if !requires {
		t.Fatal("expected included path to require auth")
	}
}

func TestShouldAuthenticateMultiSegmentPath(t *testing.T) {
	cases := []struct {
		path     string
		wantAuth bool
	}{
		{"/", true},
		{"/abc", true},
		{"/abc/", true},
		{"/abc/def", true},
		{"/api/limit-service/portfolio", true},
		{"/healthz", false},
	}
	for _, tc := range cases {
		got := ShouldAuthenticate(tc.path, []string{"/*"}, []string{"/healthz"})
		if got != tc.wantAuth {
			t.Errorf("ShouldAuthenticate(%q): got %v, want %v", tc.path, got, tc.wantAuth)
		}
	}
}

func TestPathFilterRequiresAuthWithQueryString(t *testing.T) {
	authRequired := false
	h := PathFilter([]string{"/*"}, []string{"/healthz"})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authRequired = AuthRequiredFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "http://localhost:8888/api/limit-service/portfolio?rptDate=2026-01-22&abi=08431&desk=STRATEGICO", nil)
	h.ServeHTTP(httptest.NewRecorder(), req)

	if !authRequired {
		t.Fatal("expected auth to be required for multi-segment path with query string")
	}
}

func TestIpRateLimitBlocksWhenLimited(t *testing.T) {
	limiter := newTestLimiter(1) // 1 RPM
	mw := IpRateLimit(limiter, false)

	h := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// First request should pass
	req := httptest.NewRequest("GET", "http://example.com/public", nil)
	req.RemoteAddr = "203.0.113.1:1234"
	resp := httptest.NewRecorder()
	h.ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.Code)
	}

	// Second request should be rate-limited
	resp2 := httptest.NewRecorder()
	h.ServeHTTP(resp2, req)
	if resp2.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429, got %d", resp2.Code)
	}
	if resp2.Header().Get("Retry-After") == "" {
		t.Fatal("expected Retry-After header")
	}
}

func TestIpRateLimitBlocksAuthRequiredPaths(t *testing.T) {
	limiter := newTestLimiter(1) // 1 RPM — exhaust it first
	mw := IpRateLimit(limiter, false)

	handlerCalled := false
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handlerCalled = true
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "http://example.com/protected", nil)
	req.RemoteAddr = "203.0.113.1:1234"
	ctxReq := req.WithContext(withAuthRequired(req.Context(), true))

	// Exhaust the limit
	resp0 := httptest.NewRecorder()
	h.ServeHTTP(resp0, ctxReq)

	// Now this request should be rate-limited
	handlerCalled = false
	resp := httptest.NewRecorder()
	h.ServeHTTP(resp, ctxReq)

	if handlerCalled {
		t.Fatal("expected handler NOT to be called when rate-limited")
	}
	if resp.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429, got %d", resp.Code)
	}
}

func TestIpRateLimitSkipsWhenJwtIdentified(t *testing.T) {
	limiter := newTestLimiter(1) // 1 RPM — would block on second request
	mw := IpRateLimit(limiter, true)

	h := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "http://example.com/api", nil)
	req.RemoteAddr = "203.0.113.1:1234"

	// Exhaust the IP limit
	resp0 := httptest.NewRecorder()
	h.ServeHTTP(resp0, req)

	// Second request without JWT context: should be blocked
	resp1 := httptest.NewRecorder()
	h.ServeHTTP(resp1, req)
	if resp1.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429 for non-JWT request, got %d", resp1.Code)
	}

	// Same request but with JWT identity in context: should pass through
	ctx := context.WithValue(req.Context(), jwtIdentifiedKey, true)
	jwtReq := req.WithContext(ctx)
	resp2 := httptest.NewRecorder()
	h.ServeHTTP(resp2, jwtReq)
	if resp2.Code != http.StatusOK {
		t.Fatalf("expected 200 for JWT-identified request, got %d", resp2.Code)
	}
}

func TestApiKeyRateLimitMatchesAndBlocks(t *testing.T) {
	limiter := newTestLimiter(1)
	matcher, _ := ratelimit.NewRequestMatcher([]ratelimit.RequestMatchRule{
		{Host: "example.com"},
	})
	mw := ApiKeyRateLimit(limiter, matcher, "x-api-key", false)

	h := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// First request with API key: allowed
	req := httptest.NewRequest("GET", "http://example.com/api", nil)
	req.Host = "example.com"
	req.RemoteAddr = "1.2.3.4:1234"
	req.Header.Set("x-api-key", "my-secret-key")
	resp := httptest.NewRecorder()
	h.ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.Code)
	}

	// Second request with same API key: blocked
	resp2 := httptest.NewRecorder()
	h.ServeHTTP(resp2, req)
	if resp2.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429, got %d", resp2.Code)
	}
}

func TestApiKeyRateLimitPassesThroughNonMatchingRequests(t *testing.T) {
	limiter := newTestLimiter(0) // blocks everything
	matcher, _ := ratelimit.NewRequestMatcher([]ratelimit.RequestMatchRule{
		{Host: "api.example.com"},
	})
	mw := ApiKeyRateLimit(limiter, matcher, "x-api-key", false)

	h := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// Non-matching host: should pass through
	req := httptest.NewRequest("GET", "http://other.com/api", nil)
	req.Host = "other.com"
	resp := httptest.NewRecorder()
	h.ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200 for non-matching host, got %d", resp.Code)
	}
}

func TestApiKeyRateLimitPerKeyIsolation(t *testing.T) {
	limiter := newTestLimiter(1)
	matcher, _ := ratelimit.NewRequestMatcher([]ratelimit.RequestMatchRule{
		{Host: "example.com"},
	})
	mw := ApiKeyRateLimit(limiter, matcher, "x-api-key", false)

	h := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// Key A: first request OK
	req := httptest.NewRequest("GET", "http://example.com/api", nil)
	req.Host = "example.com"
	req.RemoteAddr = "1.2.3.4:1234"
	req.Header.Set("x-api-key", "key-A")
	resp := httptest.NewRecorder()
	h.ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("key-A first request: expected 200, got %d", resp.Code)
	}

	// Key B: should be independent
	req2 := httptest.NewRequest("GET", "http://example.com/api", nil)
	req2.Host = "example.com"
	req2.RemoteAddr = "1.2.3.4:1234"
	req2.Header.Set("x-api-key", "key-B")
	resp2 := httptest.NewRecorder()
	h.ServeHTTP(resp2, req2)
	if resp2.Code != http.StatusOK {
		t.Fatalf("key-B first request: expected 200, got %d", resp2.Code)
	}
}

func TestJwtRateLimitBlocksPerSub(t *testing.T) {
	limiter := newTestLimiter(1)
	mw := JwtRateLimit(limiter, true)

	h := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	fakeJWT := buildFakeJWT("alice")

	req := httptest.NewRequest("GET", "http://example.com/api", nil)
	req.RemoteAddr = "1.2.3.4:1234"
	req.Header.Set("Authorization", "Bearer "+fakeJWT)

	// First request: allowed
	resp := httptest.NewRecorder()
	h.ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.Code)
	}

	// Second request: blocked
	resp2 := httptest.NewRecorder()
	h.ServeHTTP(resp2, req)
	if resp2.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429, got %d", resp2.Code)
	}
}

func TestJwtRateLimitPassesThroughWithoutBearer(t *testing.T) {
	limiter := newTestLimiter(0) // blocks everything
	mw := JwtRateLimit(limiter, true)

	h := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// No Bearer token: passes through
	req := httptest.NewRequest("GET", "http://example.com/api", nil)
	resp := httptest.NewRecorder()
	h.ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200 without Bearer token, got %d", resp.Code)
	}
}

func buildFakeJWT(sub string) string {
	payload := map[string]interface{}{"sub": sub, "iss": "example.com"}
	payloadBytes, _ := json.Marshal(payload)
	return "eyJhbGciOiJSUzI1NiJ9." +
		base64.RawURLEncoding.EncodeToString(payloadBytes) +
		".fakesignature"
}

func withAuthRequired(ctx context.Context, required bool) context.Context {
	return context.WithValue(ctx, authRequiredKey, required)
}

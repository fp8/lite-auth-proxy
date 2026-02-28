package proxy

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

type stubLimiter struct {
	allowed      bool
	retryAfter   int
	seenClientIP string
}

func (s *stubLimiter) Allow(ip string) (bool, int) {
	s.seenClientIP = ip
	return s.allowed, s.retryAfter
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

func TestRateLimiterBlocksWhenLimited(t *testing.T) {
	limiter := &stubLimiter{allowed: false, retryAfter: 42}
	mw := RateLimiter(limiter)

	h := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// Test rate limiting for non-auth-required paths (e.g., public paths)
	req := httptest.NewRequest("GET", "http://example.com/public", nil)
	req.RemoteAddr = "203.0.113.1:1234"
	ctxReq := req.WithContext(withAuthRequired(req.Context(), false))

	resp := httptest.NewRecorder()
	h.ServeHTTP(resp, ctxReq)

	if resp.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429, got %d", resp.Code)
	}
	if resp.Header().Get("Retry-After") != "42" {
		t.Fatalf("expected Retry-After header to be 42, got %s", resp.Header().Get("Retry-After"))
	}
}

func TestRateLimiterSkipsAuthRequiredPaths(t *testing.T) {
	// Limiter is called but should not block for auth-required paths
	// (rate limiting is deferred to handler after auth validation)
	limiter := &stubLimiter{allowed: false, retryAfter: 42}
	mw := RateLimiter(limiter)

	handlerCalled := false
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handlerCalled = true
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "http://example.com/protected", nil)
	req.RemoteAddr = "203.0.113.1:1234"
	ctxReq := req.WithContext(withAuthRequired(req.Context(), true))

	resp := httptest.NewRecorder()
	h.ServeHTTP(resp, ctxReq)

	// Should pass through to handler (not block at middleware level)
	if !handlerCalled {
		t.Fatal("expected handler to be called for auth-required paths")
	}
	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.Code)
	}
}

func withAuthRequired(ctx context.Context, required bool) context.Context {
	return context.WithValue(ctx, authRequiredKey, required)
}

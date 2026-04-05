package proxy

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/fp8/lite-auth-proxy/internal/admin"
)

// mockRuleChecker is a simple RuleChecker for testing.
type mockRuleChecker struct {
	allow bool
}

func (m *mockRuleChecker) ShouldAllow(_, _ string) bool {
	return m.allow
}

func alwaysOK() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
}

func TestDynamicRuleCheck_AlwaysAllow_PassesThrough(t *testing.T) {
	mw := DynamicRuleCheck(&mockRuleChecker{allow: true})
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rr := httptest.NewRecorder()
	mw(alwaysOK()).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
}

func TestDynamicRuleCheck_BlockRule_Returns429(t *testing.T) {
	mw := DynamicRuleCheck(&mockRuleChecker{allow: false})
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rr := httptest.NewRecorder()
	mw(alwaysOK()).ServeHTTP(rr, req)
	if rr.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429, got %d", rr.Code)
	}
}

func TestDynamicRuleCheck_NilChecker_PassesThrough(t *testing.T) {
	mw := DynamicRuleCheck(nil)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rr := httptest.NewRecorder()
	mw(alwaysOK()).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
}

func TestDynamicRuleCheck_ThrottleRule_AllowThenBlock(t *testing.T) {
	store := admin.NewRuleStore()
	defer store.Stop()

	rule := &admin.Rule{
		RuleID:          "t1",
		TargetHost:      "example.com",
		Action:          "throttle",
		MaxRPM:          2,
		DurationSeconds: 60,
	}
	if err := store.SetRule(rule); err != nil {
		t.Fatalf("SetRule: %v", err)
	}

	// Give the store a moment to be ready
	time.Sleep(10 * time.Millisecond)

	mw := DynamicRuleCheck(store)

	for i := 0; i < 2; i++ {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Host = "example.com"
		rr := httptest.NewRecorder()
		mw(alwaysOK()).ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("request %d: expected 200, got %d", i+1, rr.Code)
		}
	}

	// 3rd request exceeds maxRPM=2
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Host = "example.com"
	rr := httptest.NewRecorder()
	mw(alwaysOK()).ServeHTTP(rr, req)
	if rr.Code != http.StatusTooManyRequests {
		t.Fatalf("3rd request: expected 429, got %d", rr.Code)
	}
}

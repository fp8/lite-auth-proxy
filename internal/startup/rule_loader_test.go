package startup

import (
	"testing"
	"time"

	"github.com/fp8/lite-auth-proxy/internal/admin"
	"github.com/fp8/lite-auth-proxy/internal/ratelimit"
)

func newTestLoader(t *testing.T) (*RuleLoader, *admin.RuleStore, map[string]*ratelimit.RateLimiter) {
	t.Helper()
	store := admin.NewRuleStore()
	t.Cleanup(store.Stop)
	rateLimiters := map[string]*ratelimit.RateLimiter{
		"ip": ratelimit.NewRateLimiter(ratelimit.RateLimiterConfig{
			Name: "ip", Enabled: true, RequestsPerMin: 60, BanDuration: 5 * time.Minute,
		}),
		"apikey": ratelimit.NewRateLimiter(ratelimit.RateLimiterConfig{
			Name: "apikey", Enabled: false, RequestsPerMin: 60, BanDuration: 5 * time.Minute,
		}),
		"jwt": ratelimit.NewRateLimiter(ratelimit.RateLimiterConfig{
			Name: "jwt", Enabled: false, RequestsPerMin: 60, BanDuration: 5 * time.Minute,
		}),
	}
	return NewRuleLoader(store, rateLimiters, nil), store, rateLimiters
}

func setEnv(t *testing.T, key, value string) {
	t.Helper()
	t.Setenv(key, value)
}

func TestRuleLoader_EmptyEnvVar_NoRules(t *testing.T) {
	loader, store, _ := newTestLoader(t)
	setEnv(t, EnvThrottleRules, "")

	if err := loader.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if rules := store.GetStatus(); len(rules) != 0 {
		t.Fatalf("expected 0 rules, got %d", len(rules))
	}
}

func TestRuleLoader_ValidRules_SkipsExpired(t *testing.T) {
	loader, store, _ := newTestLoader(t)

	future := time.Now().Add(1 * time.Hour).UTC().Format(time.RFC3339)
	past := time.Now().Add(-1 * time.Minute).UTC().Format(time.RFC3339)

	raw := `[
		{"ruleId":"r1","targetHost":"api.example.com","action":"throttle","maxRPM":50,"expiresAt":"` + future + `","durationSeconds":3600},
		{"ruleId":"r2","targetHost":"api.example.com","action":"block","expiresAt":"` + past + `","durationSeconds":60}
	]`
	setEnv(t, EnvThrottleRules, raw)

	if err := loader.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	rules := store.GetStatus()
	if len(rules) != 1 {
		t.Fatalf("expected 1 rule (non-expired), got %d", len(rules))
	}
	if rules[0].RuleID != "r1" {
		t.Fatalf("expected rule r1, got %s", rules[0].RuleID)
	}
}

func TestRuleLoader_RemainingDuration_IsCorrect(t *testing.T) {
	loader, store, _ := newTestLoader(t)

	expiresAt := time.Now().Add(300 * time.Second).UTC().Format(time.RFC3339)
	raw := `[{"ruleId":"r-dur","targetHost":"api.example.com","action":"throttle","maxRPM":10,"expiresAt":"` + expiresAt + `","durationSeconds":300}]`
	setEnv(t, EnvThrottleRules, raw)

	if err := loader.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	rules := store.GetStatus()
	if len(rules) != 1 {
		t.Fatalf("expected 1 rule, got %d", len(rules))
	}
	if rules[0].Status != "active" {
		t.Fatalf("expected status 'active', got %s", rules[0].Status)
	}
}

func TestRuleLoader_LimiterRule_ConfiguresLimiter(t *testing.T) {
	loader, _, rateLimiters := newTestLoader(t)

	expiresAt := time.Now().Add(1 * time.Hour).UTC().Format(time.RFC3339)
	raw := `[{"ruleId":"ak1","targetHost":"api.example.com","action":"throttle","maxRPM":100,"limiter":"apikey","expiresAt":"` + expiresAt + `","durationSeconds":3600}]`
	setEnv(t, EnvThrottleRules, raw)

	if err := loader.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}

	apikeyLimiter := rateLimiters["apikey"]
	status := apikeyLimiter.GetStatus()
	if !status.Enabled {
		t.Fatal("expected apikey limiter to be enabled")
	}
	if status.RequestsPerMin != 100 {
		t.Fatalf("expected RPM=100, got %d", status.RequestsPerMin)
	}
}

func TestRuleLoader_MalformedJSON_ReturnsError(t *testing.T) {
	loader, store, _ := newTestLoader(t)
	setEnv(t, EnvThrottleRules, "not valid json")

	if err := loader.Load(); err == nil {
		t.Fatal("expected error for malformed JSON")
	}
	if rules := store.GetStatus(); len(rules) != 0 {
		t.Fatalf("expected 0 rules after parse error, got %d", len(rules))
	}
}

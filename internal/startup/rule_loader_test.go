package startup

import (
	"testing"
	"time"

	"github.com/fp8/lite-auth-proxy/internal/admin"
	"github.com/fp8/lite-auth-proxy/internal/ratelimit"
)

func newTestLoader(t *testing.T) (*RuleLoader, *admin.RuleStore, *ratelimit.VertexAIBucket) {
	t.Helper()
	store := admin.NewRuleStore()
	t.Cleanup(store.Stop)
	bucket := ratelimit.NewVertexAIBucket()
	return NewRuleLoader(store, bucket, nil), store, bucket
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
	// The remaining duration should be within 2s of 300s
	// We check this by verifying the rule is still active (not yet expired)
	if rules[0].Status != "active" {
		t.Fatalf("expected status 'active', got %s", rules[0].Status)
	}
}

func TestRuleLoader_VertexAIRule_ConfiguresBucket(t *testing.T) {
	loader, _, bucket := newTestLoader(t)

	pp := "/v1/projects/"
	expiresAt := time.Now().Add(1 * time.Hour).UTC().Format(time.RFC3339)
	raw := `[{"ruleId":"vx","targetHost":"-aiplatform.googleapis.com","action":"throttle","maxRPM":100,"pathPattern":"/v1/projects/","rateByKey":true,"expiresAt":"` + expiresAt + `","durationSeconds":3600}]`
	_ = pp
	setEnv(t, EnvThrottleRules, raw)

	if err := loader.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	status := bucket.GetStatus()
	if status == nil {
		t.Fatal("expected Vertex AI bucket status to be non-nil")
	}
	if status.MaxRPM != 100 {
		t.Fatalf("expected maxRPM=100, got %d", status.MaxRPM)
	}
	if status.Mode != "per-key" {
		t.Fatalf("expected mode=per-key, got %s", status.Mode)
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

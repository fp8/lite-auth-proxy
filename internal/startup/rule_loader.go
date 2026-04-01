package startup

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/fp8/lite-auth-proxy/internal/admin"
	"github.com/fp8/lite-auth-proxy/internal/ratelimit"
)

// EnvThrottleRules is the environment variable name for persisted throttle rules.
const EnvThrottleRules = "PROXY_THROTTLE_RULES"

// PersistedRule is the JSON shape stored in PROXY_THROTTLE_RULES.
// It mirrors admin.Rule but uses an absolute ExpiresAt timestamp.
type PersistedRule struct {
	RuleID      string  `json:"ruleId"`
	TargetHost  string  `json:"targetHost"`
	Action      string  `json:"action"`
	MaxRPM      int     `json:"maxRPM,omitempty"`
	PathPattern *string `json:"pathPattern,omitempty"`
	RateByKey   bool    `json:"rateByKey,omitempty"`
	Limiter     string  `json:"limiter,omitempty"` // "ip", "apikey", or "jwt"
	ExpiresAt   string  `json:"expiresAt"`         // RFC 3339
}

// RuleLoader loads persisted throttle rules from PROXY_THROTTLE_RULES on startup.
type RuleLoader struct {
	store        *admin.RuleStore
	rateLimiters map[string]*ratelimit.RateLimiter
	logger       *slog.Logger
}

// NewRuleLoader creates a RuleLoader. rateLimiters may be nil.
func NewRuleLoader(store *admin.RuleStore, rateLimiters map[string]*ratelimit.RateLimiter, logger *slog.Logger) *RuleLoader {
	return &RuleLoader{
		store:        store,
		rateLimiters: rateLimiters,
		logger:       logger,
	}
}

// Load reads PROXY_THROTTLE_RULES and populates the RuleStore.
// It is idempotent and safe to call multiple times.
// Rules where expiresAt is in the past are silently skipped.
// An overall JSON parse failure is returned as an error; per-rule errors are logged and skipped.
func (l *RuleLoader) Load() error {
	raw := os.Getenv(EnvThrottleRules)
	if raw == "" {
		return nil
	}

	var persisted []PersistedRule
	if err := json.Unmarshal([]byte(raw), &persisted); err != nil {
		return fmt.Errorf("PROXY_THROTTLE_RULES: invalid JSON: %w", err)
	}

	now := time.Now()
	loaded := 0

	for _, pr := range persisted {
		expiresAt, err := time.Parse(time.RFC3339, pr.ExpiresAt)
		if err != nil {
			if l.logger != nil {
				l.logger.Warn("startup rule loader: skipping rule with invalid expiresAt",
					"ruleId", pr.RuleID, "expiresAt", pr.ExpiresAt, "error", err)
			}
			continue
		}
		if !expiresAt.After(now) {
			continue // already expired
		}

		remaining := int(expiresAt.Sub(now).Seconds())
		if remaining <= 0 {
			continue
		}

		rule := &admin.Rule{
			RuleID:          pr.RuleID,
			TargetHost:      pr.TargetHost,
			Action:          pr.Action,
			MaxRPM:          pr.MaxRPM,
			PathPattern:     pr.PathPattern,
			RateByKey:       pr.RateByKey,
			Limiter:         pr.Limiter,
			DurationSeconds: remaining,
		}

		if err := l.store.SetRule(rule); err != nil {
			if l.logger != nil {
				l.logger.Warn("startup rule loader: skipping rule",
					"ruleId", pr.RuleID, "error", err)
			}
			continue
		}

		// Wire rate limiter if the rule targets one.
		if pr.Limiter != "" && pr.Action == "throttle" {
			if limiter, ok := l.rateLimiters[pr.Limiter]; ok {
				limiter.SetRequestsPerMin(pr.MaxRPM)
				limiter.Enable()
			}
		}

		loaded++
	}

	if l.logger != nil {
		l.logger.Info("startup rule loader: loaded rules from PROXY_THROTTLE_RULES", "count", loaded)
	}
	return nil
}

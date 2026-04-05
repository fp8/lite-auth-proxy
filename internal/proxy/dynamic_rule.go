package proxy

import "net/http"

// RuleChecker is the interface for dynamic rule evaluation.
// Implemented by *admin.RuleStore.
type RuleChecker interface {
	ShouldAllow(host string, path string) bool
}

// DynamicRuleCheck creates middleware that evaluates dynamic admin rules on every
// request before the per-IP rate limiter. This ensures ShockGuard-imposed limits
// take precedence and are enforced globally regardless of source IP.
//
// When ruleChecker is nil (admin disabled) the middleware is a no-op passthrough.
func DynamicRuleCheck(ruleChecker RuleChecker) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if ruleChecker == nil {
				next.ServeHTTP(w, r)
				return
			}
			if !ruleChecker.ShouldAllow(r.Host, r.URL.Path) {
				writeRateLimitResponse(w, 60)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

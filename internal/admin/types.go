package admin

import (
	"sync/atomic"
	"time"
)

// ControlRequest is the body of POST /admin/control.
type ControlRequest struct {
	Command string `json:"command"`
	Rule    *Rule  `json:"rule,omitempty"`
	RuleID  string `json:"ruleId,omitempty"`
}

// Rule is a dynamic rate-limit rule managed by the admin API.
type Rule struct {
	RuleID          string    `json:"ruleId"`
	TargetHost      string    `json:"targetHost"`
	Action          string    `json:"action"`
	MaxRPM          int       `json:"maxRPM,omitempty"`
	PathPattern     *string   `json:"pathPattern,omitempty"`
	RateByKey       bool      `json:"rateByKey,omitempty"`
	Limiter         string    `json:"limiter,omitempty"`         // "ip", "apikey", or "jwt" — targets a specific rate limiter
	ThrottleDelayMs int       `json:"throttleDelayMs,omitempty"` // optional: update the limiter's throttle delay (ms); 0 = no change
	MaxDelaySlots   int       `json:"maxDelaySlots,omitempty"`   // optional: update max concurrent throttled responses; 0 = no change
	DurationSeconds int       `json:"durationSeconds"`
	ExpiresAt       time.Time `json:"-"`
	currentRPM      atomic.Int64
}

// CurrentRPMValue returns the current RPM counter (atomic read).
func (r *Rule) CurrentRPMValue() int64 {
	return r.currentRPM.Load()
}

// IncrementRPM atomically increments and returns the new RPM counter value.
func (r *Rule) IncrementRPM() int64 {
	return r.currentRPM.Add(1)
}

// ResetRPM resets the RPM counter to 0.
func (r *Rule) ResetRPM() {
	r.currentRPM.Store(0)
}

// SetRuleResponse is the response body for a successful set-rule command.
type SetRuleResponse struct {
	RuleID    string `json:"ruleId"`
	Status    string `json:"status"`
	ExpiresAt string `json:"expiresAt"`
}

// RemoveResponse is the response body for remove-rule and remove-all commands.
type RemoveResponse struct {
	Status       string `json:"status"`
	RulesRemoved int    `json:"rulesRemoved"`
}

// StatusResponse is the response body for GET /admin/status.
type StatusResponse struct {
	Rules        []RuleStatus           `json:"rules"`
	RateLimiters map[string]interface{} `json:"rateLimiters"`
}

// RuleStatus is a snapshot of a single rule included in StatusResponse.
type RuleStatus struct {
	RuleID     string `json:"ruleId"`
	TargetHost string `json:"targetHost"`
	Action     string `json:"action"`
	MaxRPM     int    `json:"maxRPM"`
	CurrentRPM int64  `json:"currentRPM"`
	Status     string `json:"status"`
	ExpiresAt  string `json:"expiresAt"`
}

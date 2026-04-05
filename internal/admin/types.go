package admin

import (
	"github.com/fp8/lite-auth-proxy/internal/store"
)

// Rule is an alias for store.Rule for backwards compatibility.
type Rule = store.Rule

// RuleStatus is an alias for store.RuleStatus for backwards compatibility.
type RuleStatus = store.RuleStatus

// ControlRequest is the body of POST /admin/control.
type ControlRequest struct {
	Command string `json:"command"`
	Rule    *Rule  `json:"rule,omitempty"`
	RuleID  string `json:"ruleId,omitempty"`
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

package admin

import (
	"github.com/fp8/lite-auth-proxy/internal/store"
)

// RuleStore wraps store.MemoryRuleStore for backwards compatibility.
// New code should use store.RuleStore interface directly.
type RuleStore = store.MemoryRuleStore

// NewRuleStore creates a RuleStore and starts background cleanup/reset goroutines.
func NewRuleStore() *RuleStore {
	return store.NewMemoryRuleStore()
}

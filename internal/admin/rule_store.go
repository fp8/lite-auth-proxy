package admin

import (
	"fmt"
	"path"
	"strings"
	"sync"
	"time"
)

// RuleStore is a thread-safe in-memory store for dynamic rate-limit rules.
// Rules are auto-expired and RPM counters are reset every 60 seconds.
type RuleStore struct {
	mu      sync.RWMutex
	rules   map[string]*Rule
	stopCh  chan struct{}
	stopped bool
}

// NewRuleStore creates a RuleStore and starts background cleanup/reset goroutines.
func NewRuleStore() *RuleStore {
	s := &RuleStore{
		rules:  make(map[string]*Rule),
		stopCh: make(chan struct{}),
	}
	go s.cleanupLoop()
	go s.resetRPMLoop()
	return s
}

// SetRule creates or updates a rule. ExpiresAt is computed from DurationSeconds
// unless already set (e.g. by the startup loader which provides remaining duration).
// The currentRPM counter is reset to 0 on every upsert.
func (s *RuleStore) SetRule(rule *Rule) error {
	if rule.RuleID == "" {
		return fmt.Errorf("ruleId is required")
	}
	if rule.ExpiresAt.IsZero() {
		rule.ExpiresAt = time.Now().Add(time.Duration(rule.DurationSeconds) * time.Second)
	}
	rule.ResetRPM()

	s.mu.Lock()
	s.rules[rule.RuleID] = rule
	s.mu.Unlock()
	return nil
}

// RemoveRule deletes the rule with the given ID.
// Returns (true, nil) if found and removed, (false, nil) if not found.
func (s *RuleStore) RemoveRule(ruleID string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.rules[ruleID]; !ok {
		return false, nil
	}
	delete(s.rules, ruleID)
	return true, nil
}

// RemoveAll deletes all rules and returns the number removed.
func (s *RuleStore) RemoveAll() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	count := len(s.rules)
	s.rules = make(map[string]*Rule)
	return count
}

// GetStatus returns a read-locked snapshot of all currently active (non-expired) rules.
func (s *RuleStore) GetStatus() []RuleStatus {
	s.mu.RLock()
	defer s.mu.RUnlock()

	now := time.Now()
	result := make([]RuleStatus, 0, len(s.rules))
	for _, rule := range s.rules {
		if !rule.ExpiresAt.After(now) {
			continue
		}
		result = append(result, RuleStatus{
			RuleID:     rule.RuleID,
			TargetHost: rule.TargetHost,
			Action:     rule.Action,
			MaxRPM:     rule.MaxRPM,
			CurrentRPM: rule.CurrentRPMValue(),
			Status:     "active",
			ExpiresAt:  rule.ExpiresAt.UTC().Format(time.RFC3339),
		})
	}
	return result
}

// ShouldAllow returns true if the request to host/path should proceed.
// It evaluates all active rules in order:
//   - "block"    → return false immediately
//   - "allow"    → return true immediately
//   - "throttle" → increment RPM; return false when over maxRPM
//
// Returns true (allow) when no rule matches.
func (s *RuleStore) ShouldAllow(host, reqPath string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()

	now := time.Now()
	for _, rule := range s.rules {
		if !rule.ExpiresAt.After(now) {
			continue
		}
		if rule.TargetHost != host {
			continue
		}
		if rule.PathPattern != nil && !matchRulePath(*rule.PathPattern, reqPath) {
			continue
		}
		switch rule.Action {
		case "block":
			return false
		case "allow":
			return true
		case "throttle":
			return rule.IncrementRPM() <= int64(rule.MaxRPM)
		}
	}
	return true
}

// Stop signals the background goroutines to exit. Safe to call multiple times.
func (s *RuleStore) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.stopped {
		s.stopped = true
		close(s.stopCh)
	}
}

func (s *RuleStore) cleanupLoop() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			s.deleteExpired()
		case <-s.stopCh:
			return
		}
	}
}

func (s *RuleStore) deleteExpired() {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	for id, rule := range s.rules {
		if !rule.ExpiresAt.After(now) {
			delete(s.rules, id)
		}
	}
}

func (s *RuleStore) resetRPMLoop() {
	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			s.resetAllRPM()
		case <-s.stopCh:
			return
		}
	}
}

func (s *RuleStore) resetAllRPM() {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, rule := range s.rules {
		rule.ResetRPM()
	}
}

// matchRulePath returns true if reqPath matches the rule pattern.
// Non-glob patterns use prefix matching; glob patterns use path.Match.
func matchRulePath(pattern, reqPath string) bool {
	if pattern == "" {
		return true
	}
	if containsGlobChars(pattern) {
		matched, _ := path.Match(pattern, reqPath)
		return matched
	}
	return strings.HasPrefix(reqPath, pattern)
}

func containsGlobChars(s string) bool {
	return strings.ContainsAny(s, "*?[")
}

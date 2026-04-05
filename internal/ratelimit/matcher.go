package ratelimit

import (
	"fmt"
	"net/http"
	"regexp"
	"strings"
)

// RequestMatchRule defines a single matching rule for rate-limit targeting.
// All non-empty fields within a rule must match (AND logic).
// Multiple rules are evaluated with OR logic.
type RequestMatchRule struct {
	Host   string `toml:"host"`   // exact string or /regex/
	Path   string `toml:"path"`   // exact string or /regex/
	Header string `toml:"header"` // header name that must be present
}

type compiledRule struct {
	hostExact  string
	hostRegex  *regexp.Regexp
	pathExact  string
	pathRegex  *regexp.Regexp
	headerName string
}

// RequestMatcher evaluates HTTP requests against a set of match rules.
// If no rules are configured, Matches always returns false (no-op).
type RequestMatcher struct {
	rules []compiledRule
}

// NewRequestMatcher compiles match rules into a matcher.
func NewRequestMatcher(rules []RequestMatchRule) (*RequestMatcher, error) {
	compiled := make([]compiledRule, 0, len(rules))
	for i, r := range rules {
		cr := compiledRule{headerName: r.Header}

		if r.Host != "" {
			if isRegexValue(r.Host) {
				re, err := regexp.Compile(r.Host[1 : len(r.Host)-1])
				if err != nil {
					return nil, fmt.Errorf("rule[%d].host: invalid regex %q: %w", i, r.Host, err)
				}
				cr.hostRegex = re
			} else {
				cr.hostExact = r.Host
			}
		}

		if r.Path != "" {
			if isRegexValue(r.Path) {
				re, err := regexp.Compile(r.Path[1 : len(r.Path)-1])
				if err != nil {
					return nil, fmt.Errorf("rule[%d].path: invalid regex %q: %w", i, r.Path, err)
				}
				cr.pathRegex = re
			} else {
				cr.pathExact = r.Path
			}
		}

		compiled = append(compiled, cr)
	}
	return &RequestMatcher{rules: compiled}, nil
}

// Matches returns true if the request matches ANY of the configured rules.
// Returns false if no rules are configured.
func (m *RequestMatcher) Matches(r *http.Request) bool {
	if len(m.rules) == 0 {
		return false
	}
	for _, rule := range m.rules {
		if rule.matches(r) {
			return true
		}
	}
	return false
}

func (cr *compiledRule) matches(r *http.Request) bool {
	// All non-empty fields must match (AND logic).
	if cr.hostRegex != nil {
		if !cr.hostRegex.MatchString(r.Host) {
			return false
		}
	} else if cr.hostExact != "" {
		if r.Host != cr.hostExact {
			return false
		}
	}

	if cr.pathRegex != nil {
		if !cr.pathRegex.MatchString(r.URL.Path) {
			return false
		}
	} else if cr.pathExact != "" {
		if r.URL.Path != cr.pathExact {
			return false
		}
	}

	if cr.headerName != "" {
		if r.Header.Get(cr.headerName) == "" {
			return false
		}
	}

	return true
}

// isRegexValue returns true if the value is wrapped in /.../ delimiters.
func isRegexValue(value string) bool {
	return len(value) >= 2 && strings.HasPrefix(value, "/") && strings.HasSuffix(value, "/")
}

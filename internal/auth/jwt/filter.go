package jwt

import (
	"fmt"
	"regexp"
	"strings"
)

// EvaluateFilters validates claims against configured filters.
// Filter values support exact matching or regex matching when wrapped in /.../.
func EvaluateFilters(claims Claims, filters map[string]string) error {
	for claimName, filterValue := range filters {
		claimValue, exists := claims[claimName]
		if !exists {
			return fmt.Errorf("required claim %q is missing", claimName)
		}

		matcher, err := newFilterMatcher(filterValue)
		if err != nil {
			return fmt.Errorf("invalid filter for claim %q: %w", claimName, err)
		}

		if err := matcher.matches(claimName, claimValue); err != nil {
			return err
		}
	}

	return nil
}

type filterMatcher struct {
	rawValue string
	regex    *regexp.Regexp
}

func newFilterMatcher(filterValue string) (*filterMatcher, error) {
	if isRegexFilter(filterValue) {
		pattern := filterValue[1 : len(filterValue)-1]
		compiled, err := regexp.Compile(pattern)
		if err != nil {
			return nil, fmt.Errorf("invalid regex %q: %w", filterValue, err)
		}
		return &filterMatcher{rawValue: filterValue, regex: compiled}, nil
	}

	return &filterMatcher{rawValue: filterValue}, nil
}

func (m *filterMatcher) matches(claimName string, claimValue interface{}) error {
	switch v := claimValue.(type) {
	case []interface{}:
		for _, item := range v {
			if m.matchesScalar(item) {
				return nil
			}
		}
		return fmt.Errorf("claim %q did not match filter %q (actual: %v)", claimName, m.rawValue, claimValue)
	default:
		if m.matchesScalar(v) {
			return nil
		}
		return fmt.Errorf("claim %q did not match filter %q (actual: %v)", claimName, m.rawValue, claimValue)
	}
}

func (m *filterMatcher) matchesScalar(value interface{}) bool {
	valueStr := claimValueToString(value)
	if m.regex != nil {
		return m.regex.MatchString(valueStr)
	}

	return valueStr == m.rawValue
}

func claimValueToString(value interface{}) string {
	return fmt.Sprintf("%v", value)
}

func isRegexFilter(filterValue string) bool {
	return len(filterValue) >= 2 && strings.HasPrefix(filterValue, "/") && strings.HasSuffix(filterValue, "/")
}

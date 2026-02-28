package jwt

import (
	"strings"
	"testing"
)

func TestEvaluateFiltersExactMatchPass(t *testing.T) {
	claims := Claims{"role": "admin"}
	filters := map[string]string{"role": "admin"}

	if err := EvaluateFilters(claims, filters); err != nil {
		t.Fatalf("expected filter to pass, got error: %v", err)
	}
}

func TestEvaluateFiltersExactMatchFail(t *testing.T) {
	claims := Claims{"role": "user"}
	filters := map[string]string{"role": "admin"}

	err := EvaluateFilters(claims, filters)
	if err == nil {
		t.Fatal("expected filter mismatch error, got nil")
	}

	if !strings.Contains(err.Error(), "role") {
		t.Fatalf("expected error to mention claim name, got: %v", err)
	}
}

func TestEvaluateFiltersRegexPass(t *testing.T) {
	claims := Claims{"email": "alice@company.com"}
	filters := map[string]string{"email": `/@company\.com$/`}

	if err := EvaluateFilters(claims, filters); err != nil {
		t.Fatalf("expected regex filter to pass, got error: %v", err)
	}
}

func TestEvaluateFiltersRegexFail(t *testing.T) {
	claims := Claims{"email": "alice@example.com"}
	filters := map[string]string{"email": `/@company\.com$/`}

	err := EvaluateFilters(claims, filters)
	if err == nil {
		t.Fatal("expected regex filter mismatch error, got nil")
	}
}

func TestEvaluateFiltersArrayORPass(t *testing.T) {
	claims := Claims{"roles": []interface{}{"viewer", "admin", "editor"}}
	filters := map[string]string{"roles": "admin"}

	if err := EvaluateFilters(claims, filters); err != nil {
		t.Fatalf("expected array OR filter to pass, got error: %v", err)
	}
}

func TestEvaluateFiltersArrayORFail(t *testing.T) {
	claims := Claims{"roles": []interface{}{"viewer", "editor"}}
	filters := map[string]string{"roles": "admin"}

	err := EvaluateFilters(claims, filters)
	if err == nil {
		t.Fatal("expected array OR filter mismatch error, got nil")
	}
}

func TestEvaluateFiltersMissingClaim(t *testing.T) {
	claims := Claims{"sub": "user-1"}
	filters := map[string]string{"email": `/@company\.com$/`}

	err := EvaluateFilters(claims, filters)
	if err == nil {
		t.Fatal("expected missing claim error, got nil")
	}

	if !strings.Contains(err.Error(), "missing") {
		t.Fatalf("expected missing claim error message, got: %v", err)
	}
}

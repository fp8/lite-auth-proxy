package ratelimit

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRequestMatcher_NoRules(t *testing.T) {
	m, err := NewRequestMatcher(nil)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/data", nil)
	if m.Matches(req) {
		t.Fatal("expected no match when no rules configured")
	}
}

func TestRequestMatcher_ExactHost(t *testing.T) {
	m, err := NewRequestMatcher([]RequestMatchRule{
		{Host: "example.com"},
	})
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/data", nil)
	req.Host = "example.com"
	if !m.Matches(req) {
		t.Fatal("expected match for exact host")
	}

	req.Host = "other.com"
	if m.Matches(req) {
		t.Fatal("expected no match for different host")
	}
}

func TestRequestMatcher_RegexHost(t *testing.T) {
	m, err := NewRequestMatcher([]RequestMatchRule{
		{Host: "/.*-aiplatform\\.googleapis\\.com/"},
	})
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodPost, "/v1/predict", nil)
	req.Host = "us-central1-aiplatform.googleapis.com"
	if !m.Matches(req) {
		t.Fatal("expected match for Vertex AI host")
	}

	req.Host = "myapp.run.app"
	if m.Matches(req) {
		t.Fatal("expected no match for non-Vertex host")
	}
}

func TestRequestMatcher_RegexPath(t *testing.T) {
	m, err := NewRequestMatcher([]RequestMatchRule{
		{Path: "/\\/v1\\/projects\\/.*\\/(endpoints|publishers|models)\\//"},
	})
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodPost,
		"/v1/projects/my-proj/locations/us-central1/publishers/google/models/gemini-pro:predict", nil)
	if !m.Matches(req) {
		t.Fatal("expected match for Vertex AI path")
	}

	req = httptest.NewRequest(http.MethodGet, "/api/data", nil)
	if m.Matches(req) {
		t.Fatal("expected no match for non-Vertex path")
	}
}

func TestRequestMatcher_HeaderPresence(t *testing.T) {
	m, err := NewRequestMatcher([]RequestMatchRule{
		{Header: "x-goog-api-key"},
	})
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("x-goog-api-key", "some-key")
	if !m.Matches(req) {
		t.Fatal("expected match when header present")
	}

	req2 := httptest.NewRequest(http.MethodGet, "/", nil)
	if m.Matches(req2) {
		t.Fatal("expected no match when header absent")
	}
}

func TestRequestMatcher_ANDWithinRule(t *testing.T) {
	m, err := NewRequestMatcher([]RequestMatchRule{
		{Host: "example.com", Path: "/api/v1"},
	})
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1", nil)
	req.Host = "example.com"
	if !m.Matches(req) {
		t.Fatal("expected match when both host and path match")
	}

	req.Host = "other.com"
	if m.Matches(req) {
		t.Fatal("expected no match when host doesn't match")
	}

	req.Host = "example.com"
	req.URL.Path = "/api/v2"
	if m.Matches(req) {
		t.Fatal("expected no match when path doesn't match")
	}
}

func TestRequestMatcher_ORBetweenRules(t *testing.T) {
	m, err := NewRequestMatcher([]RequestMatchRule{
		{Host: "/.*-aiplatform\\.googleapis\\.com/"},
		{Path: "/\\/v1\\/projects\\//"},
	})
	if err != nil {
		t.Fatal(err)
	}

	// Match by host only
	req := httptest.NewRequest(http.MethodPost, "/v1/predict", nil)
	req.Host = "us-central1-aiplatform.googleapis.com"
	if !m.Matches(req) {
		t.Fatal("expected match by host rule")
	}

	// Match by path only
	req2 := httptest.NewRequest(http.MethodPost, "/v1/projects/my-proj/endpoints/abc", nil)
	req2.Host = "my-proxy.run.app"
	if !m.Matches(req2) {
		t.Fatal("expected match by path rule")
	}

	// Match neither
	req3 := httptest.NewRequest(http.MethodGet, "/api/data", nil)
	req3.Host = "other.com"
	if m.Matches(req3) {
		t.Fatal("expected no match")
	}
}

func TestRequestMatcher_InvalidRegex(t *testing.T) {
	_, err := NewRequestMatcher([]RequestMatchRule{
		{Host: "/[invalid/"},
	})
	if err == nil {
		t.Fatal("expected error for invalid regex")
	}
}

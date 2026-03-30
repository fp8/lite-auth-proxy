package ratelimit

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestExtractVertexAICallerIdentity_APIKeyHeader(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("x-goog-api-key", "key-A")
	idA := ExtractVertexAICallerIdentity(req)

	req2 := httptest.NewRequest(http.MethodGet, "/", nil)
	req2.Header.Set("x-goog-api-key", "key-B")
	idB := ExtractVertexAICallerIdentity(req2)

	if !strings.HasPrefix(idA, "k:") {
		t.Fatalf("expected identity to start with 'k:', got %s", idA)
	}
	if idA == idB {
		t.Fatal("different keys should produce different identities")
	}
	// Same key should produce same identity
	req3 := httptest.NewRequest(http.MethodGet, "/", nil)
	req3.Header.Set("x-goog-api-key", "key-A")
	if ExtractVertexAICallerIdentity(req3) != idA {
		t.Fatal("same key should produce same identity")
	}
}

func TestExtractVertexAICallerIdentity_KeyQueryParam(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/?key=myapikey", nil)
	id := ExtractVertexAICallerIdentity(req)
	if !strings.HasPrefix(id, "k:") {
		t.Fatalf("expected identity to start with 'k:', got %s", id)
	}
}

func TestExtractVertexAICallerIdentity_BearerJWTSub(t *testing.T) {
	// Build a fake JWT with sub claim (unsigned, just for identity extraction)
	payload := map[string]interface{}{"sub": "user-123", "iss": "example.com"}
	payloadBytes, _ := json.Marshal(payload)
	fakeJWT := "eyJhbGciOiJSUzI1NiJ9." +
		base64.RawURLEncoding.EncodeToString(payloadBytes) +
		".fakesignature"

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer "+fakeJWT)
	id := ExtractVertexAICallerIdentity(req)
	if id != "s:user-123" {
		t.Fatalf("expected 's:user-123', got %s", id)
	}
}

func TestExtractVertexAICallerIdentity_IPFallback(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	// No relevant headers set
	id := ExtractVertexAICallerIdentity(req)
	if !strings.HasPrefix(id, "ip:") {
		t.Fatalf("expected identity to start with 'ip:', got %s", id)
	}
}

func TestVertexAIKeyBucket_PerKeyIsolation(t *testing.T) {
	b := NewVertexAIKeyBucket(3)
	defer b.Stop()

	// Exhaust limit for key-A
	for i := 0; i < 3; i++ {
		if !b.ShouldAllow("k:aaa") {
			t.Fatalf("request %d for k:aaa should be allowed", i+1)
		}
	}
	if b.ShouldAllow("k:aaa") {
		t.Fatal("4th request for k:aaa should be blocked")
	}

	// key-B should be unaffected
	if !b.ShouldAllow("k:bbb") {
		t.Fatal("request for k:bbb should be allowed (fresh bucket)")
	}
}

func TestVertexAIKeyBucket_ResetCounters(t *testing.T) {
	b := NewVertexAIKeyBucket(2)
	defer b.Stop()

	// Exhaust limit
	b.ShouldAllow("k:aaa")
	b.ShouldAllow("k:aaa")
	if b.ShouldAllow("k:aaa") {
		t.Fatal("3rd request should be blocked")
	}

	// Reset counters
	b.ResetAll()

	// Should be allowed again
	if !b.ShouldAllow("k:aaa") {
		t.Fatal("request should be allowed after counter reset")
	}
}

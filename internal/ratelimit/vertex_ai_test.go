package ratelimit

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestIsVertexAIRequest_AIPlatformHost(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/v1/predict", nil)
	req.Host = "us-central1-aiplatform.googleapis.com"
	if !IsVertexAIRequest(req) {
		t.Fatal("expected IsVertexAIRequest=true for aiplatform host")
	}
}

func TestIsVertexAIRequest_NonVertexAI(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/data", nil)
	req.Host = "myapp-abc123.run.app"
	if IsVertexAIRequest(req) {
		t.Fatal("expected IsVertexAIRequest=false for non-Vertex AI host")
	}
}

func TestIsVertexAIRequest_PublisherModelPath(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost,
		"/v1/projects/my-proj/locations/us-central1/publishers/google/models/gemini-pro:predict", nil)
	req.Host = "my-proxy.run.app"
	if !IsVertexAIRequest(req) {
		t.Fatal("expected IsVertexAIRequest=true for publisher/model path")
	}
}

func TestVertexAIBucket_GlobalMode_BlocksAfterLimit(t *testing.T) {
	b := NewVertexAIBucket()
	b.SetMaxRPM(10, false)

	for i := 0; i < 10; i++ {
		if !b.ShouldAllow("test-identity") {
			t.Fatalf("request %d should be allowed", i+1)
		}
	}
	if b.ShouldAllow("test-identity") {
		t.Fatal("11th request should be blocked in global mode")
	}
}

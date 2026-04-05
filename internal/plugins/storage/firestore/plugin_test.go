package firestore

import (
	"testing"

	"github.com/fp8/lite-auth-proxy/internal/config"
	"github.com/fp8/lite-auth-proxy/internal/plugin"
)

func TestPluginRegistered(t *testing.T) {
	p := plugin.Get("storage-firestore")
	if p == nil {
		t.Fatal("storage-firestore plugin not registered")
	}
	if p.Priority() != 5 {
		t.Errorf("expected priority 5, got %d", p.Priority())
	}
}

func TestPluginIsStorageBackend(t *testing.T) {
	sb := plugin.StorageBackend()
	if sb == nil {
		t.Fatal("expected storage backend to be registered")
	}
	if sb.Name() != "storage-firestore" {
		t.Errorf("expected name 'storage-firestore', got %q", sb.Name())
	}
}

func TestValidateConfig_NoBackend(t *testing.T) {
	p := &Plugin{}
	cfg := &config.Config{}
	if err := p.ValidateConfig(cfg); err != nil {
		t.Errorf("expected no error when backend is empty, got: %v", err)
	}
}

func TestValidateConfig_MissingProjectID(t *testing.T) {
	p := &Plugin{}
	cfg := &config.Config{
		Storage: config.StorageConfig{Enabled: true},
	}
	t.Setenv("GOOGLE_CLOUD_PROJECT", "")
	if err := p.ValidateConfig(cfg); err == nil {
		t.Error("expected error for missing project ID")
	}
}

func TestValidateConfig_InvalidPrefix(t *testing.T) {
	p := &Plugin{}
	cfg := &config.Config{
		Storage: config.StorageConfig{
			Enabled:          true,
ProjectID:        "test-project",
			CollectionPrefix: "INVALID PREFIX!",
		},
	}
	if err := p.ValidateConfig(cfg); err == nil {
		t.Error("expected error for invalid prefix")
	}
}

func TestValidateConfig_Valid(t *testing.T) {
	p := &Plugin{}
	cfg := &config.Config{
		Storage: config.StorageConfig{
			Enabled:          true,
ProjectID:        "test-project",
			CollectionPrefix: "my-proxy",
		},
	}
	if err := p.ValidateConfig(cfg); err != nil {
		t.Errorf("expected no error, got: %v", err)
	}
}

func TestSanitizeDocID(t *testing.T) {
	tests := []struct {
		input, expected string
	}{
		{"simple", "simple"},
		{"path/with/slashes", "path__with__slashes"},
		{"no-slash", "no-slash"},
	}
	for _, tt := range tests {
		got := sanitizeDocID(tt.input)
		if got != tt.expected {
			t.Errorf("sanitizeDocID(%q) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}

package main

import (
	"io"
	"testing"
)

func TestParseFlagsDefaults(t *testing.T) {
	configPath, healthcheckOnly, err := parseFlags(nil, io.Discard)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if configPath != defaultConfigPath {
		t.Fatalf("expected default config path %q, got %q", defaultConfigPath, configPath)
	}
	if healthcheckOnly {
		t.Fatal("expected healthcheck flag to be false by default")
	}
}

package plugin

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/fp8/lite-auth-proxy/internal/config"
	"github.com/fp8/lite-auth-proxy/internal/ratelimit"
	"github.com/fp8/lite-auth-proxy/internal/store"
)

// Middleware wraps an http.Handler.
type Middleware = func(http.Handler) http.Handler

// Plugin is the base interface that every plugin implements.
type Plugin interface {
	// Name returns the unique plugin identifier (e.g. "ratelimit", "admin").
	Name() string

	// Priority determines middleware ordering. Lower values run earlier
	// in the request pipeline. Core middleware uses priorities 0-49.
	// Plugins should use 50+.
	Priority() int
}

// MiddlewareProvider contributes middleware to the HTTP request pipeline.
type MiddlewareProvider interface {
	Plugin
	BuildMiddleware(deps *Deps) ([]Middleware, error)
}

// RouteProvider registers additional HTTP routes (e.g. /admin/*).
type RouteProvider interface {
	Plugin
	RegisterRoutes(mux *http.ServeMux, deps *Deps) error
}

// AuthProvider adds an authentication method beyond core JWT.
// Core JWT auth is always present and is NOT a plugin.
type AuthProvider interface {
	Plugin
	// Authenticate attempts to authenticate the request.
	// Returns injected headers on success, ErrNotApplicable if this
	// auth method doesn't apply (e.g. no API key header present),
	// or an error with an HTTP status code on failure.
	Authenticate(r *http.Request, cfg *config.AuthConfig) (headers map[string]string, err error)
}

// StorageBackendProvider provides persistent implementations of the core store
// interfaces. It is a factory: the core calls its methods to obtain store
// implementations that replace the default in-memory ones.
//
// CONSTRAINT: Only one StorageBackendProvider may exist in a binary.
// Register() panics if a second one is registered.
type StorageBackendProvider interface {
	Plugin
	// Open initializes the storage connection. Called once during startup.
	Open(cfg *config.Config, logger *slog.Logger) error
	// NewRuleStore returns a persistent RuleStore implementation.
	NewRuleStore(cfg *config.Config, logger *slog.Logger) (store.RuleStore, error)
	// NewKeyValueStore returns a persistent KeyValueStore scoped to the given namespace.
	NewKeyValueStore(namespace string) (store.KeyValueStore, error)
}

// ConfigValidator allows a plugin to validate its config section at startup.
type ConfigValidator interface {
	Plugin
	ValidateConfig(cfg *config.Config) error
}

// Starter is called after all plugins are initialized but before the
// HTTP server starts accepting traffic.
type Starter interface {
	Start(ctx context.Context) error
}

// Stopper is called during graceful shutdown, after the HTTP server
// has stopped accepting new requests.
type Stopper interface {
	Stop() error
}

// Deps is the shared context that the core passes to plugins during initialization.
type Deps struct {
	Config        *config.Config
	Logger        *slog.Logger
	RuleStore     store.RuleStore
	KeyValueStore func(ns string) store.KeyValueStore
	StoragePersist bool
	RateLimiters  map[string]*ratelimit.RateLimiter
	AuthProviders []AuthProvider
}

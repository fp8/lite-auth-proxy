# Step 01: Plugin Infrastructure

## Objective

Introduce a compile-time plugin system that allows lite-auth-proxy to be built with different feature sets. The core binary provides only reverse-proxying, JWT authentication, and a health check endpoint. All other features — rate limiting, admin control plane, API-key authentication, and persistent storage — are opt-in plugins activated by Go blank imports.

This step defines the plugin registry, interfaces, lifecycle, and pipeline assembly mechanism. Subsequent steps define each plugin.

## Context

lite-auth-proxy has grown beyond its original "lite" scope. Every feature is compiled into every binary, regardless of whether it is needed. This increases binary size, attack surface, dependency footprint (e.g. Firestore SDK), and cognitive load for operators who only need basic JWT proxying.

The plugin system addresses this by allowing:
- **Two official Docker images**: a minimal "lite" image and a "full" image with all plugins.
- **Custom builds**: users import only the plugins they need.
- **Zero runtime cost**: unused plugins are not compiled in. The request hot path is identical with or without the plugin system.

## Reference

- Caddy's plugin registration pattern: `caddy.RegisterModule()` with blank imports
- CoreDNS plugin architecture: `init()` + `plugin.Register()`
- Go `database/sql` driver pattern: `sql.Register()` with `_ "github.com/lib/pq"`

## Design Principles

1. **Blank-import activation.** A plugin is activated by adding `_ "path/to/plugin"` to the main package. No YAML, no runtime discovery, no `.so` loading.

2. **Compile-time safety.** If a plugin is not imported, its code is not compiled. The compiler catches registration errors, not runtime.

3. **Loose coupling via interfaces.** Plugins communicate through interfaces defined in the plugin registry package, never through direct package imports between plugins. A plugin may *optionally* consume a capability provided by another plugin (e.g. admin optionally uses storage), but must function without it.

4. **Config-gated validation.** If a TOML config section references a plugin that is not compiled in, the proxy must fail at startup with a clear error message — not silently ignore the config.

5. **Ordered pipeline assembly.** Middleware plugins declare a priority. The core assembles the middleware chain by sorting plugins, producing a deterministic pipeline regardless of import order.

6. **Core-owned store interfaces.** The `RuleStore` and `KeyValueStore` interfaces are defined in core, not in the plugin package. Core ships default in-memory implementations. Storage plugins provide alternative implementations of the same interfaces. Every consumer — admin, API-key, rate limiter — programs against the interface, never a concrete type. This means the store abstraction exists even in the lightest build, and adding persistence is just swapping the implementation at startup.

---

## Plugin Registry

### Registration

The plugin registry is a package-level singleton. Each plugin registers itself in its `init()` function:

```go
package plugin

var registry = &pluginRegistry{
    plugins: make(map[string]Plugin),
}

type pluginRegistry struct {
    plugins        map[string]Plugin
    storageBackend StorageBackend // at most one; enforced by Register()
}

// Register adds a plugin to the global registry.
// Panics if a plugin with the same name is already registered.
// For StorageBackend plugins: panics if any StorageBackend is already
// registered, even under a different name. Only one storage backend
// may exist in a binary.
func Register(p Plugin) {
    if _, exists := registry.plugins[p.Name()]; exists {
        panic("plugin already registered: " + p.Name())
    }
    if _, ok := p.(StorageBackend); ok {
        if existing := registry.storageBackend; existing != nil {
            panic(fmt.Sprintf(
                "only one storage backend allowed: %q is already registered, cannot register %q",
                existing.Name(), p.Name(),
            ))
        }
        registry.storageBackend = p.(StorageBackend)
    }
    registry.plugins[p.Name()] = p
}

// Get returns a registered plugin by name, or nil if not found.
func Get(name string) Plugin {
    return registry.plugins[name]
}

// StorageBackend returns the single registered StorageBackend, or nil.
func StorageBackend() StorageBackend {
    return registry.storageBackend
}

// All returns all registered plugins.
func All() []Plugin { ... }
```

### Querying by capability

The registry provides typed accessors using Go generics (Go 1.18+):

```go
// OfType returns all registered plugins that implement the given interface,
// sorted by Priority() ascending (lower = earlier in pipeline).
func OfType[T Plugin]() []T {
    var result []T
    for _, p := range registry.plugins {
        if t, ok := p.(T); ok {
            result = append(result, t)
        }
    }
    sort.Slice(result, func(i, j int) bool {
        return result[i].Priority() < result[j].Priority()
    })
    return result
}
```

This allows the core to query, for example, all `MiddlewareProvider` plugins sorted by priority, without knowing which concrete plugins are registered.

---

## Plugin Interfaces

### Base interface

Every plugin implements this:

```go
type Plugin interface {
    // Name returns the unique plugin identifier (e.g. "ratelimit", "admin").
    Name() string

    // Priority determines middleware ordering. Lower values run earlier
    // in the request pipeline. Core middleware uses priorities 0-49.
    // Plugins should use 50+.
    Priority() int
}
```

### Capability interfaces

A plugin implements one or more of these to declare what it provides:

```go
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
    Authenticate(r *http.Request, cfg AuthConfig) (headers map[string]string, err error)
}

// StorageBackend provides persistent implementations of the core store
// interfaces. It is a factory: the core calls its methods to obtain store
// implementations that replace the default in-memory ones.
//
// CONSTRAINT: Only one StorageBackend may exist in a binary.
// Register() panics if a second one is registered. This is enforced at
// compile time (binary won't start) rather than at config time, because
// the store interfaces are singletons — there is one RuleStore and one
// KeyValueStore factory for the entire process.
type StorageBackend interface {
    Plugin
    // Open initializes the storage connection. Called once during startup.
    Open(cfg StorageConfig, logger *slog.Logger) error
    // NewRuleStore returns a persistent RuleStore implementation.
    // The returned implementation satisfies the same store.RuleStore
    // interface as the in-memory default. It is a full replacement,
    // not a wrapper — it manages its own internal caching and sync.
    NewRuleStore(cfg StorageConfig, logger *slog.Logger) (store.RuleStore, error)
    // NewKeyValueStore returns a persistent KeyValueStore scoped to
    // the given namespace.
    NewKeyValueStore(namespace string) (store.KeyValueStore, error)
}

// ConfigValidator allows a plugin to validate its config section at startup.
type ConfigValidator interface {
    Plugin
    ValidateConfig(cfg *config.Config) error
}
```

### Lifecycle interfaces

Optional interfaces for startup/shutdown hooks:

```go
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
```

---

## Core Store Package

The store interfaces and their in-memory implementations live in a core package (e.g. `internal/store`), not in the plugin package. This is a deliberate design choice:

- The interfaces are **part of the core**, available even in the lightest build.
- The in-memory implementations ship with core and are the **default for all builds**.
- Storage plugins provide **alternative implementations** of the same interfaces.
- No plugin ever needs to import another plugin — they all program against `store.RuleStore` and `store.KeyValueStore`.

### RuleStore interface

```go
package store

// RuleStore is the interface for dynamic rule management.
// Core provides an in-memory implementation (MemoryRuleStore).
// Storage plugins provide persistent implementations (e.g. FirestoreRuleStore).
type RuleStore interface {
    SetRule(rule *Rule) error
    RemoveRule(ruleID string) (bool, error)
    RemoveAll() int
    ShouldAllow(host, path string) bool
    GetStatus() []RuleStatus
    Stop()
}
```

### KeyValueStore interface

```go
package store

// KeyValueStore provides key-value storage for plugins.
// Core provides an in-memory implementation (MemoryKeyValueStore).
// Storage plugins provide persistent implementations.
type KeyValueStore interface {
    Get(ctx context.Context, key string) ([]byte, error)
    Set(ctx context.Context, key string, value []byte) error
    Delete(ctx context.Context, key string) error
    List(ctx context.Context, prefix string) ([]string, error)
}
```

### In-memory implementations (shipped with core)

```go
package store

// MemoryRuleStore is a thread-safe in-memory RuleStore.
// It is the default implementation used when no storage plugin is registered.
// Background goroutines handle expiry cleanup (30s) and RPM counter reset (60s).
func NewMemoryRuleStore() RuleStore { ... }

// MemoryKeyValueStore is a thread-safe in-memory KeyValueStore.
// Suitable for single-instance deployments or testing.
// Data is lost on process exit.
func NewMemoryKeyValueStore() KeyValueStore { ... }
```

The `MemoryRuleStore` is a direct extraction of today's `admin.RuleStore`. The `MemoryKeyValueStore` is a new, simple `sync.RWMutex`-protected `map[string][]byte`.

### Why in-memory KeyValueStore matters

Even without a storage plugin, the in-memory `KeyValueStore` provides value:
- The API-key plugin can use it for its internal bookkeeping in single-key mode.
- Tests can exercise the full store interface without needing Firestore.
- A future admin dashboard could use it for session state or caching.

The in-memory implementation makes the store abstraction zero-cost in the lite build — no external dependencies, no goroutines beyond what the `MemoryRuleStore` already runs.

---

## Shared Plugin Types

These types are defined in the plugin package for inter-plugin communication:

### Middleware type

```go
type Middleware = func(http.Handler) http.Handler
```

### Deps (dependency bag)

The `Deps` struct is the shared context that the core passes to plugins during initialization. Plugins populate and consume it:

```go
type Deps struct {
    Config         *config.Config
    Logger         *slog.Logger
    RuleStore      store.RuleStore                    // created by core, may be replaced by storage plugin
    KeyValueStore  func(ns string) store.KeyValueStore // factory; default = in-memory per namespace
    StoragePersist bool                               // true if a storage plugin replaced the defaults
    RateLimiters   map[string]*ratelimit.RateLimiter  // set by ratelimit plugin
    AuthProviders  []AuthProvider                     // populated during init
}
```

Note: `RuleStore` and `KeyValueStore` are populated by the **core** before any plugin runs. If a storage plugin is registered, the core replaces the defaults with the storage plugin's implementations. Plugins never need to check whether these are nil — they always have a valid implementation.

---

## Pipeline Assembly

The core `Run()` function queries the registry and assembles the application in phases. The phases have a defined order to satisfy inter-plugin dependencies:

```
Phase 1: Create default stores (core, always runs)
    → deps.RuleStore     = store.NewMemoryRuleStore()
    → deps.KeyValueStore = func(ns) { return store.NewMemoryKeyValueStore() }
    → Every build gets a working store from the start.

Phase 2: Storage backend (optional, replaces defaults)
    → storagePlugin := plugin.StorageBackend()   // singleton, at most one
    → If storagePlugin != nil AND storage.backend is configured:
      → Opens the storage connection.
      → deps.RuleStore      = storagePlugin.NewRuleStore(...)
      → deps.KeyValueStore  = storagePlugin.NewKeyValueStore
      → deps.StoragePersist = true
    → If no storage plugin: defaults from Phase 1 remain. No error.

Phase 3: Config validation
    → Validates that config sections referencing unregistered plugins
      produce a clear error (see Config Validation below).
    → For every registered plugin that implements ConfigValidator,
      calls ValidateConfig(). Fails fast on first error.

Phase 4: Route providers
    → Calls RegisterRoutes() on each RouteProvider, sorted by priority.
    → Admin plugin registers /admin/* routes using deps.RuleStore
      (which is already either in-memory or persistent).

Phase 5: Auth providers
    → Collects all AuthProvider plugins into deps.AuthProviders.
    → The core base handler iterates AuthProviders during request
      handling (after core JWT, which is not a plugin).

Phase 6: Middleware providers
    → Calls BuildMiddleware() on each MiddlewareProvider, sorted by priority.
    → Prepends core middleware (logger, body limiter, sanitizer, path filter).
    → Assembles the final middleware chain.

Phase 7: Lifecycle
    → Calls Start() on all Starter plugins.
    → HTTP server starts accepting traffic.
    → On shutdown signal: stops HTTP server, calls Stop() on all Stopper plugins
      (including deps.RuleStore.Stop()).
```

The key insight: **stores are created in Phase 1 and optionally replaced in Phase 2.** By the time any plugin runs (Phase 4+), `deps.RuleStore` and `deps.KeyValueStore` are guaranteed to be non-nil and ready. No plugin ever checks "is there a storage backend?" — it just uses the store interface it receives.

### Middleware ordering

The full pipeline for a "full" build (all plugins) is:

| Priority | Source | Middleware |
|----------|--------|-----------|
| 10 | core | RequestLogger |
| 20 | core | BodyLimiter |
| 30 | core | HeaderSanitizer |
| 40 | core | PathFilter |
| 50 | admin plugin | DynamicRuleCheck |
| 60 | ratelimit plugin | ApiKeyRateLimit |
| 70 | ratelimit plugin | JwtRateLimit |
| 80 | ratelimit plugin | IpRateLimit |

For a "lite" build (no plugins), only priorities 10–40 are present.

---

## Config Validation: Unregistered Plugin Detection

A critical requirement: if the TOML config enables a feature that requires a plugin that is not compiled in, the proxy must fail at startup with a clear message.

### Detection rules

| Config section | Required plugin | Error if plugin absent |
|---|---|---|
| `security.rate_limit.enabled = true` | `ratelimit` | `"rate limiting is configured but the ratelimit plugin is not compiled in. Use the full build or add: _ \"path/to/plugins/ratelimit\""` |
| `security.apikey_rate_limit.enabled = true` | `ratelimit` | same |
| `security.jwt_rate_limit.enabled = true` | `ratelimit` | same |
| `admin.enabled = true` | `admin` | `"admin API is configured but the admin plugin is not compiled in..."` |
| `auth.api_key.enabled = true` | `apikey` | `"API-key authentication is configured but the apikey plugin is not compiled in..."` |
| `storage.backend = "firestore"` | `storage-firestore` | `"storage backend \"firestore\" is configured but the storage-firestore plugin is not compiled in..."` |

### Implementation approach

The core performs this check *before* calling plugin-specific `ValidateConfig()`:

```go
func validatePluginAvailability(cfg *config.Config) error {
    checks := []struct {
        condition bool
        plugin    string
        message   string
    }{
        {
            cfg.Security.RateLimit.Enabled ||
            cfg.Security.ApiKeyRateLimit.Enabled ||
            cfg.Security.JwtRateLimit.Enabled,
            "ratelimit",
            "rate limiting is configured but the ratelimit plugin is not compiled in",
        },
        {cfg.Admin.Enabled, "admin", "admin API is configured..."},
        {cfg.Auth.APIKey.Enabled, "apikey", "API-key authentication is configured..."},
        {cfg.Storage.Backend != "", "storage-" + cfg.Storage.Backend, "storage backend is configured..."},
    }

    for _, c := range checks {
        if c.condition && plugin.Get(c.plugin) == nil {
            return fmt.Errorf("%s — use the full build image or add the plugin import", c.message)
        }
    }
    return nil
}
```

### Converse: unused config is harmless

If a plugin is compiled in but its config section is absent or `enabled = false`, the plugin simply does not activate. No error. This allows users to use the "full" image and selectively enable features via config.

---

## Build Variants

### Entry points

Two `main.go` files, each with different blank imports:

**Lite build** — proxy + JWT + healthz only:

```go
package main

import "github.com/fp8/lite-auth-proxy/internal/core"

func main() {
    core.Run()
}
```

**Full build** — all plugins:

```go
package main

import (
    "github.com/fp8/lite-auth-proxy/internal/core"

    _ "github.com/fp8/lite-auth-proxy/internal/plugins/ratelimit"
    _ "github.com/fp8/lite-auth-proxy/internal/plugins/admin"
    _ "github.com/fp8/lite-auth-proxy/internal/plugins/apikey"
    _ "github.com/fp8/lite-auth-proxy/internal/plugins/storage/firestore"
)

func main() {
    core.Run()
}
```

**Custom build** — user picks plugins:

```go
package main

import (
    "github.com/fp8/lite-auth-proxy/internal/core"

    _ "github.com/fp8/lite-auth-proxy/internal/plugins/ratelimit"
    _ "github.com/fp8/lite-auth-proxy/internal/plugins/apikey"
    // No admin, no storage — user only needs rate limiting and API keys.
)

func main() {
    core.Run()
}
```

### Docker images

| Image | Tag suffix | Plugins included | Expected size |
|-------|-----------|-----------------|---------------|
| `lite-auth-proxy:X.Y.Z-lite` | `-lite` | none (core only) | ~8–10 MB |
| `lite-auth-proxy:X.Y.Z` | (default) | all | ~15–20 MB |

Both use the same base image (`gcr.io/distroless/static-debian12:nonroot`).

### Makefile targets

```makefile
build-lite:
	go build -o ./bin/lite-auth-proxy-lite ./cmd/proxy-lite

build:
	go build -o ./bin/lite-auth-proxy ./cmd/proxy

docker-build-lite:
	docker build -f Dockerfile.lite -t lite-auth-proxy:lite .

docker-build:
	docker build -t lite-auth-proxy:latest .
```

---

## Plugin Registration Example

A complete example showing how a plugin registers itself:

```go
// internal/plugins/ratelimit/plugin.go
package ratelimit

import "github.com/fp8/lite-auth-proxy/internal/plugin"

func init() {
    plugin.Register(&rateLimitPlugin{})
}

type rateLimitPlugin struct{}

func (p *rateLimitPlugin) Name() string     { return "ratelimit" }
func (p *rateLimitPlugin) Priority() int    { return 60 }

func (p *rateLimitPlugin) ValidateConfig(cfg *config.Config) error {
    // Plugin-specific validation (e.g. check match rules are valid regex)
    return nil
}

func (p *rateLimitPlugin) BuildMiddleware(deps *plugin.Deps) ([]plugin.Middleware, error) {
    // Create rate limiters, store them in deps.RateLimiters
    // Return middleware closures
    return middlewares, nil
}
```

---

## Testing Strategy

### Unit tests

- **Registry tests**: Register, duplicate name panics, Get, All, OfType returns correct types, OfType sorts by priority. Registering two `StorageBackend` plugins panics with a message naming both plugins. `plugin.StorageBackend()` returns the single registered backend or nil.
- **Core store tests**: `MemoryRuleStore` — SetRule, RemoveRule, RemoveAll, ShouldAllow, expiry cleanup, RPM reset, thread safety under `-race`. `MemoryKeyValueStore` — Get, Set, Delete, List with prefix, thread safety.
- **Pipeline assembly tests**: Given a set of registered plugins, verify middleware chain order matches priority. Verify phases execute in correct order. Verify `deps.RuleStore` and `deps.KeyValueStore` are non-nil even with zero plugins registered.
- **Config validation tests**: Verify that enabling a config section without the required plugin produces the expected error message. Verify that absent config + present plugin is a no-op.

### Integration tests

- **Lite build tests**: Build the lite binary, verify it starts with only JWT + health endpoints. Verify rate-limit config produces a clear error.
- **Full build tests**: Build the full binary, verify all features work as they do today.
- **Custom build tests**: Build with a subset of plugins, verify only those features are available.

### Backwards compatibility

All existing integration tests must pass against the full build without modification. The plugin system is a structural refactor — the external behavior is identical.

---

## Verification

```bash
# Unit tests for plugin registry
go test ./internal/plugin/... -race -count=1

# Lite build compiles and starts
go build -o /dev/null ./cmd/proxy-lite
./bin/lite-auth-proxy-lite -config config/config-lite.toml &
curl -s http://localhost:8888/healthz  # 200 OK

# Full build matches current behavior
go build -o /dev/null ./cmd/proxy
go test ./... -race -count=1

# Config validation: rate limit enabled in lite build fails
./bin/lite-auth-proxy-lite -config config/config.toml
# Expected: startup error mentioning "ratelimit plugin is not compiled in"
```

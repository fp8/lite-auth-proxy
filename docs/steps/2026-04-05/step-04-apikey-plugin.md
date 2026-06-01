# Step 04: API-Key Authentication Plugin

## Objective

Extract API-key authentication into a self-contained plugin that operates in two modes:

1. **Single-key mode** (default) — one static API key configured via TOML or env var. Works with no dependencies on other plugins. This is the current behavior.
2. **Multi-key mode** — multiple API keys stored in persistent storage, manageable via the admin API. Activates automatically when both the storage plugin and admin plugin are present.

The plugin must not require storage or admin — it must function standalone.

## Dependencies

- Step 01 (plugin infrastructure, `AuthProvider` interface)
- Step 03 (admin plugin) — optional, enables admin management of API keys
- Step 05 (storage plugin) — optional, enables multi-key persistence

## Context

API-key authentication is currently compiled into every build, even when not used. This step makes it a plugin so the lite build excludes it entirely.

More importantly, this step introduces **multi-key support** for deployments where multiple clients need individual API keys with distinct payloads (header injection). Today, all clients share one key and one payload set. With multi-key mode, each key can inject different headers — e.g. identifying which team or service is calling.

---

## Plugin Specification

| Property | Value |
|----------|-------|
| **Name** | `apikey` |
| **Priority** | `90` (auth runs after rate limiting) |
| **Implements** | `AuthProvider`, `ConfigValidator` |

### Authentication flow

The core base handler iterates registered `AuthProvider` plugins after attempting core JWT auth. The API-key plugin's `Authenticate()` method:

1. Extracts the API key from the configured header (`auth.api_key.name`, default `X-API-KEY`).
2. If no key is present, returns `ErrNotApplicable` — the core tries the next auth provider or returns 401.
3. Validates the key against the configured key(s).
4. On match: returns the payload headers to inject.
5. On mismatch: returns 401 error.

Constant-time comparison is used for all key comparisons to prevent timing attacks.

---

## Single-Key Mode

Active when: `auth.api_key.enabled = true` and no storage backend is available (or multi-key is not configured).

Behavior is identical to the current implementation:

- One key configured via `auth.api_key.value` (or `API_KEY_SECRET` env var).
- One payload set configured via `[auth.api_key.payload]`.
- Constant-time compare.
- On success: inject payload headers with the configured `auth.header_prefix`.

### Config

```toml
[auth.api_key]
enabled = true
name = "X-API-KEY"
value = "{{ENV.API_KEY_SECRET}}"

[auth.api_key.payload]
service = "internal"
source = "backend-job"
```

No changes from today's config format.

---

## Multi-Key Mode

Active when: `auth.api_key.enabled = true` AND `auth.api_key.multi_key = true` AND a storage backend is available.

### How it works

Instead of one hardcoded key, multiple keys are stored in the storage backend's `KeyValueStore` under the `apikeys` namespace. Each key entry contains:

```json
{
  "keyId": "team-alpha-prod",
  "keyHash": "<sha256-hex>",
  "payload": {
    "service": "alpha-service",
    "team": "alpha",
    "environment": "production"
  },
  "enabled": true,
  "createdAt": "2026-04-01T10:00:00Z",
  "updatedAt": "2026-04-01T10:00:00Z"
}
```

**Key storage**:
- Keys are stored as SHA-256 hashes, never in plaintext.
- The actual key value is only known at creation time and shown once in the API response.
- Comparison is done by hashing the incoming key and comparing hashes (constant-time).

**Cache layer**:
- On startup, all enabled keys are loaded from storage into an in-memory map (`keyHash → payload`).
- A storage change listener keeps the in-memory cache synchronized.
- The hot path (per-request authentication) reads only from the in-memory cache.

### Admin API extensions

When both the admin plugin and API-key plugin are present, the admin plugin's `/admin/control` endpoint accepts additional commands for key management:

```json
{"command": "add-api-key", "apiKey": { ... }}
{"command": "remove-api-key", "keyId": "..."}
{"command": "list-api-keys"}
{"command": "disable-api-key", "keyId": "..."}
{"command": "enable-api-key", "keyId": "..."}
```

#### add-api-key

Creates a new API key. The proxy generates a cryptographically random key, stores its hash, and returns the plaintext key exactly once.

Request:
```json
{
  "command": "add-api-key",
  "apiKey": {
    "keyId": "team-alpha-prod",
    "payload": {
      "service": "alpha-service",
      "team": "alpha"
    }
  }
}
```

Response (200):
```json
{
  "keyId": "team-alpha-prod",
  "key": "lap_a1b2c3d4e5f6g7h8i9j0k1l2m3n4o5p6",
  "status": "active",
  "message": "Store this key securely. It cannot be retrieved again."
}
```

The key prefix `lap_` (lite-auth-proxy) makes keys identifiable in logs and secret managers.

#### remove-api-key

Request:
```json
{"command": "remove-api-key", "keyId": "team-alpha-prod"}
```

Response (200):
```json
{"status": "ok", "keyId": "team-alpha-prod"}
```

#### list-api-keys

Returns all keys with metadata (never the key value or hash).

Response (200):
```json
{
  "keys": [
    {
      "keyId": "team-alpha-prod",
      "payload": {"service": "alpha-service", "team": "alpha"},
      "enabled": true,
      "createdAt": "2026-04-01T10:00:00Z"
    }
  ]
}
```

#### disable-api-key / enable-api-key

Toggles a key's `enabled` flag without deleting it.

---

## Graceful Degradation Matrix

| Storage plugin | Admin plugin | Mode | Key management |
|---|---|---|---|
| absent | absent | Single-key | Config file / env var only |
| absent | present | Single-key | Config file / env var only (admin cannot manage keys without storage) |
| present | absent | Single-key | Config file / env var only (no admin API to manage keys) |
| present | present | **Multi-key** | Admin API: add, remove, list, enable, disable |

The `auth.api_key.multi_key = true` config flag is required to activate multi-key mode. Even if storage and admin are both present, the plugin stays in single-key mode unless explicitly opted in.

### Fallback behavior

When `multi_key = true` but the store is an in-memory implementation (no storage plugin):

```
FATAL: auth.api_key.multi_key is enabled but no storage plugin is compiled in.
Multi-key mode requires persistent storage — keys stored in the in-memory
KeyValueStore are lost on restart. Either:
  - Add the storage plugin to your build, or
  - Set auth.api_key.multi_key = false and use single-key mode
```

**How does the plugin detect this?** The core sets a flag in `Deps` indicating whether a storage plugin replaced the default stores. The API-key plugin checks this flag — it does not inspect the concrete type of the `KeyValueStore`.

When `multi_key = true` and storage is available but admin is absent:

```
WARNING: auth.api_key.multi_key is enabled but the admin plugin is not compiled in.
Keys can only be managed by directly writing to the storage backend.
The admin API key management commands (add-api-key, remove-api-key, etc.) are
not available.
```

This is a warning, not an error — keys can still be pre-populated in storage by external tooling.

---

## Config Sections Owned

```toml
[auth.api_key]
enabled = false
name = "X-API-KEY"
value = "{{ENV.API_KEY_SECRET}}"
multi_key = false                    # NEW: enable multi-key mode (requires storage)

[auth.api_key.payload]
service = "internal"
```

**If the plugin is not compiled in** and `auth.api_key.enabled = true`, the core must fail at startup:

```
FATAL: API-key authentication is configured (auth.api_key.enabled = true) but the
apikey plugin is not compiled in. Use the full build image or add the plugin
import to your custom build.
```

### Config validation

- Single-key mode: `auth.api_key.value` must be non-empty (current behavior).
- Multi-key mode: `auth.api_key.value` is optional (keys come from storage). If `value` is set, it is treated as a "bootstrap" key that is always valid in addition to storage-managed keys.
- `multi_key = true` without storage plugin: fatal error (see above).

---

## Interaction with Other Plugins

### Rate limiter plugin

No direct interaction. The API-key rate limiter (if configured) uses the key header value for rate-limiting identity, regardless of whether the key is valid. This is unchanged from current behavior — rate limiting runs before authentication.

### Admin plugin

When both plugins are present and `multi_key = true`:
- The API-key plugin registers additional command handlers with the admin plugin.
- The admin plugin delegates `add-api-key`, `remove-api-key`, `list-api-keys`, `disable-api-key`, `enable-api-key` commands to the API-key plugin.
- This delegation uses an interface so neither plugin imports the other directly.

```go
// Defined in the plugin package (shared types):
type APIKeyManager interface {
    AddKey(ctx context.Context, keyId string, payload map[string]string) (plaintext string, err error)
    RemoveKey(ctx context.Context, keyId string) error
    ListKeys(ctx context.Context) ([]APIKeyInfo, error)
    DisableKey(ctx context.Context, keyId string) error
    EnableKey(ctx context.Context, keyId string) error
}
```

The API-key plugin sets `deps.APIKeyManager` during initialization. The admin plugin checks for it when handling commands.

### Store (provided by core via `deps.KeyValueStore`)

The API-key plugin gets its `KeyValueStore` from `deps.KeyValueStore("apikeys")`. It does not know or care which implementation is behind it:

- **With storage plugin**: The `KeyValueStore` is persistent (e.g. Firestore-backed). Keys survive restarts and are synced across instances.
- **Without storage plugin**: The `KeyValueStore` is `MemoryKeyValueStore`. Keys are lost on restart. This is why `multi_key = true` without a storage plugin is a fatal error — it would silently lose keys.

In multi-key mode, keys are serialized as JSON and stored via the `KeyValueStore` interface. The persistent implementation handles cross-instance synchronization internally (e.g. Firestore snapshot listeners).

---

## Tests

### Plugin registration

1. When the plugin package is imported, `plugin.Get("apikey")` returns non-nil.
2. The plugin implements `AuthProvider` and `ConfigValidator`.

### Single-key mode

3. Valid API key returns injected payload headers.
4. Invalid API key returns 401.
5. Missing API key header returns `ErrNotApplicable` (core falls through).
6. Constant-time comparison: timing does not vary with key prefix match length.

### Multi-key mode

7. Multiple keys with different payloads: each key injects its own headers.
8. Disabled key returns 401.
9. Removed key returns 401.
10. New key added via admin API is usable immediately.
11. Key created on instance A is usable on instance B (via storage sync).

### Config validation

12. `multi_key = true` without storage plugin: fatal error.
13. `multi_key = true` with storage, without admin: warning logged but startup succeeds.
14. `enabled = true` without `value` in single-key mode: fatal error.
15. `enabled = true` with `multi_key = true` and no `value`: succeeds (keys come from storage).

### Behavioral parity

16. All existing API-key integration tests pass in single-key mode.

---

## Verification

```bash
# Plugin-specific unit tests
go test ./internal/plugins/apikey/... -race -count=1

# Full build: all existing tests pass
go test ./... -race -count=1

# Multi-key integration test (requires storage plugin)
go test ./internal/plugins/apikey/... -tags=integration -race -count=1
```

# Step 03: Admin Plugin

## Objective

Extract the admin control plane (`/admin/control`, `/admin/status`) into a self-contained plugin. The admin plugin must function with or without the storage plugin and with or without the rate limiter plugin.

This step also introduces explicit documentation requirements around **single-instance vs. multi-instance deployment** depending on whether storage is enabled.

## Dependencies

- Step 01 (plugin infrastructure, registry, `RouteProvider` and `MiddlewareProvider` interfaces)
- Step 02 (rate limiter plugin) — optional runtime dependency, not a compile-time dependency

## Context

The admin control plane manages dynamic throttle/block/allow rules at runtime. Today, it stores rules in an in-memory map and directly wires rate limiter settings. This step decouples it:

1. **Without storage plugin**: Rules are in-memory only. The proxy must be deployed as a **single instance** in Cloud Run (`max-instances=1`) or accept that rules are per-instance. This must be documented clearly.
2. **With storage plugin**: Rules are persisted and synchronized across instances. Multi-instance deployment is safe. The `PROXY_THROTTLE_RULES` env var workaround becomes unnecessary.
3. **Without rate limiter plugin**: The admin plugin still functions — it manages rules and evaluates them for block/allow/throttle decisions. The `limiter` field in `set-rule` is silently ignored (no limiter to target). The `rateLimiters` section is omitted from `/admin/status`.
4. **With rate limiter plugin**: Full current behavior — `set-rule` with `limiter` field tunes the targeted rate limiter at runtime.

---

## Plugin Specification

| Property | Value |
|----------|-------|
| **Name** | `admin` |
| **Priority** | `50` |
| **Implements** | `RouteProvider`, `MiddlewareProvider`, `ConfigValidator`, `Starter`, `Stopper` |

### Routes registered

When `admin.enabled = true`:

| Method | Path | Handler |
|--------|------|---------|
| `POST` | `/admin/control` | ControlHandler — manages rules |
| `GET` | `/admin/status` | StatusHandler — returns rule + limiter snapshots |

Both routes are wrapped with `AdminAuthMiddleware` which validates GCP identity tokens independently of the main proxy auth pipeline.

### Middleware contributed

The plugin contributes one middleware:

- **DynamicRuleCheck** (priority 50) — evaluates all active rules against the request's `Host` and path. Returns 429 on throttle exceeded, blocks on `action=block`, passes through on `action=allow` or no match.

When `admin.enabled = false`, the plugin contributes no middleware and registers no routes.

---

## RuleStore: The Admin Plugin is a Consumer, Not a Creator

The admin plugin does **not** create or choose the `RuleStore` implementation. By the time the admin plugin initializes, `deps.RuleStore` is already set by the core (Phase 1 and Phase 2 of pipeline assembly — see Step 01):

- **No storage plugin**: `deps.RuleStore` is `store.NewMemoryRuleStore()` (in-memory, created by core).
- **With storage plugin**: `deps.RuleStore` is the persistent implementation (e.g. `FirestoreRuleStore`), created by the storage plugin and injected by core.

The admin plugin simply **uses** `deps.RuleStore` — it calls `SetRule`, `RemoveRule`, `GetStatus`, etc. through the `store.RuleStore` interface. It does not know or care which implementation is behind it.

### Decision flow at startup

```
admin.enabled = true?
  ├── no  → plugin is inert (no routes, no middleware)
  └── yes
        │
        ├── Use deps.RuleStore (always non-nil)
        │     → In-memory if no storage plugin (single-instance required)
        │     → Persistent if storage plugin is present (multi-instance safe)
        │
        └── deps.RateLimiters != nil?
              ├── yes → set-rule with limiter field tunes the limiter
              └── no  → limiter field in set-rule is silently ignored
```

### In-memory mode (no storage plugin)

When `deps.RuleStore` is the `MemoryRuleStore`:
- Rules are stored in a Go `map[string]*Rule` protected by `sync.RWMutex`.
- Background goroutines clean up expired rules (every 30s) and reset RPM counters (every 60s).
- Rules are lost on process exit.

**Operational requirement**: When running without the storage plugin, the admin API is instance-local. In a serverless environment like Cloud Run:

- **Deploy with `max-instances=1`**, or
- **Accept per-instance rules** — a rule set on one instance does not apply to others, and
- **Use `PROXY_THROTTLE_RULES`** env var for rule persistence across restarts.

This requirement must be documented prominently in the admin plugin's config section and in the deployment guide.

### Persistent mode (with storage plugin)

When `deps.RuleStore` is a persistent implementation (e.g. `FirestoreRuleStore`), the store handles everything internally:

- **Writes** go to both an internal in-memory cache and the persistent backend.
- **Reads** (`ShouldAllow`) hit the internal in-memory cache only — zero network calls on the hot path.
- **Cross-instance sync** is handled by the store implementation (e.g. Firestore snapshot listener). Other instances see rule changes within 1–2 seconds.
- **Startup load** reads all non-expired rules from the backend before serving traffic.

The admin plugin's code is identical in both modes — it calls the same interface methods. The behavioral difference is entirely inside the `RuleStore` implementation.

This eliminates the need for `PROXY_THROTTLE_RULES` when storage is enabled.

---

## Config Sections Owned

```toml
[admin]
enabled = false

[admin.jwt]
issuer         = "https://accounts.google.com"
audience       = "https://your-proxy.run.app"
allowed_emails = ["sa@my-project.iam.gserviceaccount.com"]
tolerance_secs = 30
cache_ttl_mins = 1440

[admin.jwt.filters]
# hd = "your-domain.com"
```

**If the plugin is not compiled in** and `admin.enabled = true`, the core must fail at startup:

```
FATAL: admin API is configured (admin.enabled = true) but the admin plugin is
not compiled in. Use the full build image or add the plugin import to your
custom build.
```

### Config validation (plugin-specific)

When `admin.enabled = true`, the plugin validates:
- `admin.jwt.issuer` is non-empty.
- `admin.jwt.audience` is non-empty.
- At least one of `admin.jwt.allowed_emails` or `admin.jwt.filters` is configured.

---

## Interaction with Other Plugins

### Rate limiter plugin (optional)

| `deps.RateLimiters` | `set-rule` with `limiter` field | `/admin/status` response |
|---|---|---|
| present (map of 3 limiters) | Updates targeted limiter's RPM, delay, slots, and enables it | Includes `rateLimiters` section with all 3 limiter states |
| `nil` (plugin not compiled in) | `limiter` field is silently ignored; rule still stored and evaluated for block/allow/throttle RPM check | `rateLimiters` key is omitted from response |

### Store implementation (determined by core, transparent to admin)

| Storage plugin registered? | `deps.RuleStore` implementation | Behavior |
|---|---|---|
| yes | Persistent (e.g. `FirestoreRuleStore`) | Rules persist, cross-instance sync, `PROXY_THROTTLE_RULES` unnecessary |
| no | `MemoryRuleStore` | Rules in-memory only, per-instance, `PROXY_THROTTLE_RULES` needed for restart survival |

The admin plugin does not check which implementation it received. It calls the same `store.RuleStore` methods either way.

### API-key plugin (no direct interaction)

The admin plugin does not interact with the API-key plugin directly. If both plugins are present and multi-key mode is enabled, the admin plugin delegates key management commands to the API-key plugin via a shared `APIKeyManager` interface (see Step 04). The API-key plugin gets its own `KeyValueStore` from `deps.KeyValueStore("apikeys")` independently.

---

## Deployment Guidance (must be documented)

### Without storage plugin

```
┌─────────────────────────────────────────────────────────────┐
│  WARNING: Single-instance deployment required                │
│                                                              │
│  Without the storage plugin, admin rules exist only in the   │
│  memory of the instance that received the API call.          │
│                                                              │
│  In Cloud Run or similar serverless environments:            │
│  • Set max-instances=1, or                                   │
│  • Accept that rules are per-instance (each instance has     │
│    its own rule set), or                                     │
│  • Use PROXY_THROTTLE_RULES env var for persistence          │
│                                                              │
│  For multi-instance deployments with consistent rules,       │
│  enable the storage plugin.                                  │
└─────────────────────────────────────────────────────────────┘
```

This warning must appear in:
1. The admin plugin's config documentation section.
2. The deployment guide's Cloud Run section.
3. The startup log when `admin.enabled = true` and no storage backend is registered.

### With storage plugin

Multi-instance deployment is fully supported. Rules are synchronized across all instances. No special deployment constraints.

---

## Tests

### Plugin registration

1. When the plugin package is imported, `plugin.Get("admin")` returns non-nil.
2. The plugin implements `RouteProvider`, `MiddlewareProvider`, `ConfigValidator`, `Starter`, `Stopper`.

### Without storage (in-memory mode)

3. `set-rule` creates a rule; `ShouldAllow()` enforces it.
4. `remove-rule` removes a rule.
5. `remove-all` clears all rules.
6. Expired rules are cleaned up within 30 seconds.
7. RPM counters reset every 60 seconds.

### Without rate limiter plugin

8. `set-rule` with `limiter: "ip"` succeeds — rule is stored, `limiter` field is ignored.
9. `GET /admin/status` returns rules but no `rateLimiters` key.

### With rate limiter plugin

10. `set-rule` with `limiter: "ip"` updates the IP limiter's RPM.
11. `GET /admin/status` includes `rateLimiters` section.

### With storage plugin

12. Rules persist across simulated restarts (stop plugin, create new instance, verify rules loaded from storage).
13. Rule set on one instance is visible on another instance (via change listener).

### Config validation

14. `admin.enabled = true` without `admin.jwt.issuer` produces an error.
15. `admin.enabled = true` without any of `allowed_emails` or `filters` produces an error.

### Startup log

16. When `admin.enabled = true` and no storage backend: startup log includes a warning about single-instance deployment.

---

## Verification

```bash
# Plugin-specific unit tests
go test ./internal/plugins/admin/... -race -count=1

# Full build: all existing admin tests pass
go test ./... -race -count=1

# Lite build: admin config rejected
./bin/lite-auth-proxy-lite -config config/config-admin.toml
# Expected error: "admin plugin is not compiled in"
```

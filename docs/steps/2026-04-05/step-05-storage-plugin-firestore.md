# Step 05: Storage Plugin — Firestore

## Objective

Implement the first `StorageBackend` plugin using Google Cloud Firestore (Native mode). This provides persistent, cross-instance rule storage and key-value storage for the admin and API-key plugins.

This step also defines the `StorageBackend` contract so future storage plugins (GCS, AWS DynamoDB, Azure Cosmos DB) can be implemented against the same interface.

## Dependencies

- Step 01 (plugin infrastructure, `StorageBackend` interface, `RuleStore` interface, `KeyValueStore` interface)
- Step 03 (admin plugin) — optional consumer
- Step 04 (API-key plugin) — optional consumer

## Context

The admin plugin's in-memory rule store is the primary limitation of lite-auth-proxy in multi-instance serverless deployments. The storage plugin solves this by providing:

1. **Rule persistence** — rules survive process restarts without the `PROXY_THROTTLE_RULES` env var workaround.
2. **Cross-instance synchronization** — a rule set on one instance is visible on all instances within 1–2 seconds.
3. **Multi-key API-key storage** — the API-key plugin stores and retrieves key entries from Firestore.

Firestore is chosen as the first implementation because:
- Truly serverless (no provisioning, no VPC connector required).
- Free tier covers this use case easily (50K reads/day, 20K writes/day, 1 GiB storage).
- Real-time snapshot listeners enable cross-instance sync without polling or Pub/Sub.
- Go SDK is mature (`cloud.google.com/go/firestore`).
- Natural fit for the GCP/Cloud Run deployment target.

---

## Plugin Specification

| Property | Value |
|----------|-------|
| **Name** | `storage-firestore` |
| **Priority** | `5` (storage initializes first, before all other plugins) |
| **Implements** | `StorageBackend`, `ConfigValidator`, `Starter`, `Stopper` |

### StorageBackend contract

The `StorageBackend` interface (defined in Step 01) is a factory that produces implementations of the core `store.RuleStore` and `store.KeyValueStore` interfaces:

```go
type StorageBackend interface {
    Plugin
    Open(cfg StorageConfig, logger *slog.Logger) error
    NewRuleStore(cfg StorageConfig, logger *slog.Logger) (store.RuleStore, error)
    NewKeyValueStore(namespace string) (store.KeyValueStore, error)
}
```

#### Open

Initializes the Firestore client. Called once during Phase 2 of pipeline assembly.

- Uses Application Default Credentials (ADC) — no explicit credentials needed in Cloud Run.
- Connects to the Firestore database in the project specified by `storage.project_id` (or defaults to `GOOGLE_CLOUD_PROJECT`).
- Validates the connection by performing a lightweight read.

#### NewRuleStore

Returns a `FirestoreRuleStore` that implements `store.RuleStore`. This is a **complete implementation**, not a wrapper around the in-memory store. It manages its own internal in-memory cache for the hot path plus Firestore persistence and cross-instance sync. It satisfies the exact same interface as `store.MemoryRuleStore` — the admin plugin cannot distinguish between them.

#### NewKeyValueStore

Returns a `FirestoreKeyValueStore` scoped to the given namespace. Each namespace maps to a Firestore collection (e.g. namespace `"apikeys"` → collection `proxy-apikeys`). Implements `store.KeyValueStore` — identical interface to `store.MemoryKeyValueStore`.

---

## Firestore Data Model

### Collection: `{prefix}-rules`

Each document represents one admin rule. Document ID = `ruleId`.

```
proxy-rules/
  sg-throttle-my-api:
    ruleId: "sg-throttle-my-api"
    targetHost: "my-api.run.app"
    action: "throttle"
    maxRPM: 50
    pathPattern: "/v1/projects/"
    rateByKey: true
    limiter: "apikey"
    throttleDelayMs: 200
    maxDelaySlots: 50
    durationSeconds: 600
    expiresAt: 2026-03-30T15:10:00Z  (Firestore Timestamp)
    createdAt: 2026-03-30T15:00:00Z
    updatedAt: 2026-03-30T15:00:00Z
```

**Firestore TTL policy**: A TTL policy is configured on the `expiresAt` field. Firestore automatically deletes expired documents within 24 hours. The in-memory cleanup goroutine (every 30s) handles immediate expiry; the Firestore TTL is a secondary cleanup.

### Collection: `{prefix}-apikeys`

Each document represents one API key. Document ID = `keyId`.

```
proxy-apikeys/
  team-alpha-prod:
    keyId: "team-alpha-prod"
    keyHash: "a1b2c3..."  (SHA-256 hex)
    payload:
      service: "alpha-service"
      team: "alpha"
    enabled: true
    createdAt: 2026-04-01T10:00:00Z
    updatedAt: 2026-04-01T10:00:00Z
```

### Collection prefix

All collections use a configurable prefix (default `proxy`) to avoid conflicts with other applications sharing the same Firestore database:

| Namespace | Collection name |
|-----------|----------------|
| `rules` | `{prefix}-rules` |
| `apikeys` | `{prefix}-apikeys` |

---

## Firestore RuleStore Implementation

`FirestoreRuleStore` is a standalone implementation of the `store.RuleStore` interface. It is not a wrapper around `MemoryRuleStore` — it is a complete replacement that manages its own internal in-memory cache, Firestore persistence, and cross-instance synchronization.

### Architecture

```
                    ┌──────────────┐
                    │   Firestore  │
                    │  proxy-rules │
                    └──────┬───────┘
                           │
            ┌──────────────┼──────────────┐
            │              │              │
       write + listen   listen         listen
            │              │              │
     ┌──────▼──────┐ ┌────▼─────┐ ┌────▼─────┐
     │  Instance 1 │ │ Inst. 2  │ │ Inst. 3  │
     │  internal   │ │ internal │ │ internal │
     │  cache (map)│ │ cache    │ │ cache    │
     └─────────────┘ └──────────┘ └──────────┘
```

Each `FirestoreRuleStore` instance contains its own internal `map[string]*Rule` (protected by `sync.RWMutex`) that serves as the hot-path cache. This is structurally similar to `MemoryRuleStore` but the data lifecycle is different — writes go to Firestore and the cache is kept in sync via the snapshot listener.

### Write path

`SetRule(rule)`:
1. Write to the internal cache (immediate — this instance's `ShouldAllow()` sees it instantly).
2. Write to Firestore (async fire-and-forget with error logging).
3. Other instances receive the change via snapshot listener within 1–2 seconds.

`RemoveRule(ruleId)`:
1. Remove from the internal cache.
2. Delete from Firestore.

`RemoveAll()`:
1. Clear the internal cache.
2. Batch-delete all documents in the `proxy-rules` collection.

### Read path

`ShouldAllow(host, path)`:
- Reads from the internal cache **only**. Zero Firestore calls on the hot path.
- Latency is identical to `MemoryRuleStore`.

### Sync path (snapshot listener)

On startup, the `FirestoreRuleStore` opens a snapshot listener on the `proxy-rules` collection:

```go
client.Collection(collectionName).Snapshots(ctx)
```

The listener receives real-time updates whenever any document in the collection changes. On each snapshot:

1. Iterate document changes.
2. For `ADDED` or `MODIFIED`: upsert the rule into the in-memory store.
3. For `REMOVED`: remove the rule from the in-memory store.

The listener handles:
- **Initial load**: On startup, the first snapshot contains all existing documents. The `FirestoreRuleStore` populates its internal cache before returning from `NewRuleStore()`, ensuring it is fully loaded before the proxy serves traffic. This replaces the `PROXY_THROTTLE_RULES` env var mechanism.
- **Ongoing sync**: Subsequent snapshots contain only changed documents. The listener updates the internal cache.
- **Reconnection**: The Firestore SDK handles connection drops and reconnects transparently.

### Conflict resolution

If two instances set a rule with the same `ruleId` at the same time:
- Firestore uses last-writer-wins semantics.
- Both instances will converge to the same state via the snapshot listener.
- This is acceptable because admin operations are rare and operator-initiated.

### Error handling

- **Firestore write failure**: Log error, rule remains in the internal cache. The rule works on this instance but may not propagate to others. An operator can retry.
- **Snapshot listener disconnection**: Log warning. The Firestore SDK automatically reconnects. During the gap, instances may diverge. When the listener reconnects, a full snapshot resynchronizes the internal cache.
- **Startup connection failure**: Fatal error. If Firestore is unreachable at startup, the proxy should not start — the operator configured storage for a reason.

---

## Firestore KeyValueStore Implementation

`FirestoreKeyValueStore` implements the core `store.KeyValueStore` interface, backed by a Firestore collection. It is a drop-in replacement for `store.MemoryKeyValueStore`.

### Firestore mapping

- `key` → document ID
- `value` → stored as a `data` field (Firestore `[]byte` / Blob)
- `List(prefix)` uses a Firestore range query on the document ID: `>=prefix` and `<prefix\uffff`

### Usage by API-key plugin

The API-key plugin (Step 04) calls `deps.KeyValueStore("apikeys")`. When Firestore is the storage backend, this returns a `FirestoreKeyValueStore` backed by the `proxy-apikeys` collection. The API-key plugin serializes key entries as JSON and stores them via the `store.KeyValueStore` interface — it never imports the Firestore package.

The `FirestoreKeyValueStore` handles cross-instance synchronization internally. The API-key plugin maintains its own in-memory key cache on top of the `KeyValueStore`, refreshing it by polling or by using a change notification mechanism exposed through an optional `Watchable` interface extension:

```go
// Optional interface that a KeyValueStore may implement.
// If the store supports it, consumers can watch for changes.
type Watchable interface {
    Watch(ctx context.Context, prefix string, callback func(key string, value []byte, deleted bool)) error
}
```

If the `KeyValueStore` does not implement `Watchable` (e.g. the in-memory implementation), the API-key plugin falls back to periodic polling or operates without cross-instance sync.

---

## Config Sections Owned

```toml
[storage]
backend = "firestore"             # Storage backend name. Must match a registered
                                  # storage plugin's name suffix (e.g. "firestore"
                                  # matches the "storage-firestore" plugin).
                                  # Empty string = no storage (default).

[storage.firestore]
project_id = ""                   # GCP project ID. Defaults to GOOGLE_CLOUD_PROJECT.
collection_prefix = "proxy"       # Prefix for Firestore collection names.
                                  # Collections: {prefix}-rules, {prefix}-apikeys, etc.
```

**Environment variable overrides:**

| Variable | Config field | Type |
|----------|-------------|------|
| `PROXY_STORAGE_BACKEND` | `storage.backend` | string |
| `PROXY_STORAGE_FIRESTORE_PROJECT_ID` | `storage.firestore.project_id` | string |
| `PROXY_STORAGE_FIRESTORE_COLLECTION_PREFIX` | `storage.firestore.collection_prefix` | string |

### Startup validation

**If `storage.backend` names a backend that is not compiled in**:

```
FATAL: storage backend "firestore" is configured but the storage-firestore plugin
is not compiled in. Use the full build image or add the plugin import to your
custom build.
```

**If `storage.backend` is empty**: No error. No storage is used. Plugins that optionally consume storage fall back to their non-persistent behavior.

**If `storage.backend = "firestore"` and the Firestore plugin is present**:
- Validates that `project_id` resolves to a non-empty string (via env var or GCP metadata).
- Validates that `collection_prefix` is non-empty and contains only `[a-z0-9-]`.

---

## GCP Permissions Required

The Cloud Run service account needs:

```
roles/datastore.user
```

This grants read/write access to Firestore. No other permissions are needed.

```bash
gcloud projects add-iam-policy-binding $GOOGLE_CLOUD_PROJECT \
  --member="serviceAccount:$SERVICE_ACCOUNT" \
  --role="roles/datastore.user"
```

### Firestore database setup

Firestore must be initialized in **Native mode** (not Datastore mode) in the GCP project:

```bash
gcloud firestore databases create --location=your-region
```

If the database already exists, no action is needed. The plugin does not create collections or indexes — Firestore creates them automatically on first write.

### TTL policy (optional, recommended)

Configure a TTL policy on the `expiresAt` field for the rules collection to automatically clean up expired rules:

```bash
gcloud firestore fields ttls update expiresAt \
  --collection-group=proxy-rules \
  --enable-ttl
```

This is optional because the in-memory cleanup goroutine handles expiry on the hot path. The Firestore TTL is a secondary cleanup to avoid accumulating stale documents.

---

## Future Storage Backends

The `StorageBackend` interface is designed to be implemented by other cloud providers:

| Backend | Plugin name | Notes |
|---------|------------|-------|
| Firestore | `storage-firestore` | This step |
| Google Cloud Storage | `storage-gcs` | For environments where Firestore is not available. Higher latency. No real-time listeners (would need polling). |
| AWS DynamoDB | `storage-dynamodb` | DynamoDB Streams for change events. |
| Azure Cosmos DB | `storage-cosmosdb` | Change feed for cross-instance sync. |
| Redis | `storage-redis` | Requires VPC connector in Cloud Run. Sub-ms latency. Pub/Sub for sync. |
| PostgreSQL | `storage-postgres` | LISTEN/NOTIFY for change events. Requires Cloud SQL or AlloyDB. |

Each backend implements the same `StorageBackend` interface. The admin and API-key plugins are backend-agnostic — they interact only with `store.RuleStore` and `store.KeyValueStore` interfaces.

**Only one storage backend can exist in a binary.** The plugin registry enforces this at registration time — if two storage plugins are imported (e.g. both Firestore and GCS), `plugin.Register()` panics at startup:

```
panic: only one storage backend allowed: "storage-firestore" is already registered,
cannot register "storage-gcs"
```

This is a deliberate compile-time constraint. The store interfaces are process-level singletons — there is one `RuleStore` and one `KeyValueStore` factory for the entire proxy. Mixing backends (e.g. rules in Firestore, API keys in GCS) adds complexity with no clear use case. If this need arises in the future, the `StorageBackend` interface can be evolved to support it.

To add a new backend:
1. Create a new package under `plugins/storage/{backend}/`.
2. Implement `StorageBackend`, including `Open`, `NewRuleStore`, and `NewKeyValueStore`.
3. Register via `init()` + `plugin.Register()`.
4. Add a blank import to the desired build's `main.go` — **replacing** any other storage import.

---

## Tests

### Plugin registration

1. When the plugin package is imported, `plugin.Get("storage-firestore")` returns non-nil.
2. The plugin implements `StorageBackend`, `ConfigValidator`, `Starter`, `Stopper`.
2b. `plugin.StorageBackend()` returns the Firestore plugin.
2c. Importing a second storage plugin (e.g. a test `storage-mock`) alongside Firestore panics with a message naming both plugins.

### Config validation

3. `storage.backend = "firestore"` with empty project ID and no `GOOGLE_CLOUD_PROJECT`: fatal error.
4. `storage.backend = "firestore"` with valid project ID: passes validation.
5. `storage.backend = "nonexistent"` with no matching plugin: fatal error.
6. `storage.backend = ""`: passes (no storage configured).

### RuleStore (integration tests, require Firestore emulator)

7. SetRule persists to Firestore and is readable from a second client.
8. RemoveRule deletes from Firestore.
9. RemoveAll batch-deletes all rules.
10. Snapshot listener: rule set by client A appears in client B's in-memory store within 5 seconds.
11. Expired rules are not loaded on startup.
12. Startup loads all non-expired rules from Firestore into the in-memory store.

### KeyValueStore (integration tests)

13. Set and Get round-trip: stored bytes are returned exactly.
14. Delete removes the entry; subsequent Get returns not-found.
15. List with prefix returns matching keys only.

### Error handling

16. Firestore write failure: rule remains in in-memory store, error is logged.
17. Connection failure at startup: fatal error, proxy does not start.

### Firestore emulator

All integration tests run against the Firestore emulator, not a live GCP project:

```bash
# Start emulator
gcloud emulators firestore start --host-port=localhost:8081

# Run tests
FIRESTORE_EMULATOR_HOST=localhost:8081 \
  go test ./internal/plugins/storage/firestore/... -tags=integration -race -count=1
```

---

## Verification

```bash
# Unit tests (no emulator needed)
go test ./internal/plugins/storage/firestore/... -race -count=1

# Integration tests (require Firestore emulator)
FIRESTORE_EMULATOR_HOST=localhost:8081 \
  go test ./internal/plugins/storage/firestore/... -tags=integration -race -count=1

# Full build: all existing tests pass
go test ./... -race -count=1

# Lite build: storage config rejected
./bin/lite-auth-proxy-lite -config config/config-storage.toml
# Expected error: "storage-firestore plugin is not compiled in"
```

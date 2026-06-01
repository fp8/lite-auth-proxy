# gRPC Transcoding

The `grpctranscode` plugin lets a REST/JSON client talk to a gRPC backend
through the proxy. Clients send ordinary JSON over HTTP; the proxy translates
each request into a **unary gRPC call** on an upstream service and translates
the gRPC reply back into JSON.

It is **fully generic**: there are no per-service code stubs and no transcoding
config files. The plugin learns each backend's services, methods, message
schemas, and (optionally) REST mappings **at runtime** via gRPC server
reflection, using dynamic protobuf messages to build requests and decode
responses.

> This plugin is part of the **flex** build only. It is registered at priority
> `95` and runs ahead of the auth and rate-limiting middleware that wrap it.

- For the at-a-glance config table see [PLUGINS.md → gRPC Transcoding Plugin](PLUGINS.md#grpc-transcoding-plugin).
- For the env-var naming convention see [ENVIRONMENT.md](ENVIRONMENT.md).
- This document covers **how the implementation works** and **how to run it locally**.

---

## At a glance

```
            HTTP / JSON                         gRPC (h2c or TLS)
 client ───────────────▶  lite-auth-proxy  ───────────────────────▶  gRPC backend
        ◀───────────────  (grpctranscode)  ◀───────────────────────  greeter.v1.Greeter
            JSON / problem+json                  protobuf

 unmatched path ──────────────────────────▶  404 (no such gRPC method)
```

A request that matches a discovered gRPC route is transcoded. When gRPC
transcoding is enabled the backend is a gRPC service (by default
`server.target_url`), so a request that matches no gRPC method is rejected with
**404** (`application/problem+json`) — there is no HTTP fall-through.

---

## Source layout

All code lives in [`internal/plugins/grpctranscode/`](../internal/plugins/grpctranscode):

| File | Responsibility |
|------|----------------|
| [`plugin.go`](../internal/plugins/grpctranscode/plugin.go) | Plugin registration, lifecycle (`Start`/`Stop`), the middleware, the `/healthz` readiness probe (`Ready`/`probeAndDiscover`), and route building. |
| [`backend.go`](../internal/plugins/grpctranscode/backend.go) | Dials a backend (`grpc.NewClient`), runs the `grpc.health.v1.Health` check, and closes the connection. |
| [`reflection.go`](../internal/plugins/grpctranscode/reflection.go) | gRPC server-reflection client: lists services, recursively fetches `FileDescriptorProto`s, resolves them into a registry, and extracts unary methods. |
| [`annotations.go`](../internal/plugins/grpctranscode/annotations.go) | Parses the `google.api.http` method option (field `72295728`) — via protoreflect when registered, else a minimal wire-format parser. |
| [`routetable.go`](../internal/plugins/grpctranscode/routetable.go) | The concurrent route table, plus path-template → regex compilation and the convention/annotation route builders. |
| [`transcode.go`](../internal/plugins/grpctranscode/transcode.go) | The per-request hot path: JSON → dynamic message → gRPC `Invoke` → JSON, plus RFC 9457 error rendering. |
| [`status.go`](../internal/plugins/grpctranscode/status.go) | gRPC `codes.Code` → HTTP status mapping (the standard grpc-gateway mapping). |
| [`plugin_test.go`](../internal/plugins/grpctranscode/plugin_test.go) | Integration tests with an in-process gRPC server (`//go:build integration`). |

The implementation depends only on `google.golang.org/grpc` and
`google.golang.org/protobuf` — both already required by the module. There is
**no protoc / code-generation step** anywhere, including the tests and the
local test backend.

---

## Startup & readiness: health-check driven

The proxy is designed to be a **sidecar**: it boots in ~100 ms while the gRPC
service it fronts may take several seconds to come up. So readiness is **gated by
the health check**, not established at boot, and there is **no background polling**.

### `Start` — boot, don't probe

When the flex proxy boots, `proxy.NewHandler` calls each plugin's `Start`
**before** the HTTP server binds. The gRPC `Start` (`plugin.go`):

1. **Resolves the backend(s).** Explicit `[[grpc.backends]]` take precedence;
   otherwise the single backend is derived from `server.target_url` (its
   `host:port`; an `https://` scheme implies TLS). The only required gRPC config
   is `grpc.enabled=true`. If neither a backend nor `server.target_url` is set,
   boot fails (static misconfiguration).
2. **Dials** each backend (`grpc.NewClient` is lazy — no connection yet).
3. Returns immediately. It does **not** health-check, reflect, or discover.

So boot always succeeds and `/healthz` is served right away. The orchestrator's
startup probe is what decides when the container is ready (and, on its own
policy, whether to restart it) — the proxy never crash-loops on a missing backend.

### `/healthz` — a live backend health probe

The plugin implements `plugin.ReadinessReporter`; the proxy's `/healthz` handler
calls it on **every** request. It probes **every configured backend** with a
**live** `grpc.health.v1.Health/Check` (the proxy's health probe is translated
into a backend health probe). The first time a backend reports `SERVING`, it
discovers the backend's services via reflection (`discoverServices` — every
**unary** method; streaming, `grpc.reflection.*` and `grpc.health.v1.Health` are
skipped), builds routes (`buildRoutes`, per `route_mode`), and installs them.
**Discovery happens once and is cached** for the process lifetime.

**Readiness is all-or-nothing across backends:** `/healthz` returns `200` only
when *every* backend is healthy. If any backend fails its check, `/healthz`
returns `503` and the body names each failing backend — so a multi-backend proxy
reports unready as soon as one of its upstreams is down.

The outcome maps to an HTTP status the orchestrator's startup probe understands:

| Backend state | `/healthz` |
|---------------|-----------|
| dest port not open / `UNAVAILABLE` / health `NOT_SERVING` | `503 "waiting: …"` (probe keeps waiting) |
| port open but health or reflection service absent (`UNIMPLEMENTED`) | `503 "unavailable: …"` |
| `SERVING` and discovered | `200 {"status":"ok"}` |

When any backend is not ready, `/healthz` returns
`503 {"status":"unavailable","errors":[ … ]}`, each entry naming the backend and
the reason (`waiting:` vs `unavailable:`). Only an all-ready result yields `200`.

> **`server.health_check.target` does not apply when gRPC is enabled.** The
> backend is a gRPC service whose health is probed over the gRPC health protocol,
> so that probe is the authoritative readiness signal — `/healthz` is driven
> solely by it and is **not** proxied to any HTTP `health_check.target` (a warning
> is logged at startup if one is configured alongside `grpc.enabled`).

> Backends are probed **concurrently**, so one slow or unreachable backend
> doesn't serialize the others — overall `/healthz` latency is the slowest single
> probe, not the sum. Reflection/discovery uses a 5 s timeout and the health
> check a 3 s timeout, so a probe stays within a typical startup-probe timeout.
> Discovery runs at most once per backend (on its first `SERVING`); subsequent
> probes are just a health check.

### Discovery is also bootstrapped by requests

Discovery does **not** depend on `/healthz` being wired up. The request path also
bootstraps it: when a request arrives and a backend is not yet discovered, the
middleware probes it (the same health-check + reflection, **throttled** to once
per few seconds per backend so a not-ready backend under traffic isn't hammered)
and then matches. So the very first request self-bootstraps the routes even when
nothing ever calls `/healthz`. Once every backend is discovered this is a no-op
on the hot path.

### Request outcomes

When gRPC transcoding is enabled the proxy is a gRPC-only transcoder — there is
no HTTP fall-through. Each non-health request resolves to one of:

| Condition | Response |
|-----------|----------|
| Matches a route, backend ready | transcoded → gRPC reply (or mapped gRPC error) |
| Matches a route, backend's last probe not ready | `503` "service not ready" (short-circuit; no dial) |
| No match, all backends discovered | `404` "no gRPC method matches …" |
| No match, discovery still pending | `503` "gRPC backend not ready" |

In a normal deployment the orchestrator's startup probe (`/healthz`) gates
traffic, so calls arrive only after the proxy is `200` — the `503` rows are the
cold-start / backend-down safety net.

These paths are covered by the integration tests (`TestGRPCTranscodeMissingHealth`,
`TestGRPCTranscodeMissingReflection`) and the e2e suite (`grpc_negative.feature`),
which assert the proxy stays up and `/healthz` reports the defect.

---

## Route modes

`route_mode` controls how discovered methods become HTTP routes:

### `convention`

Every unary method is exposed as:

```
POST /<package>.<Service>/<Method>
```

with the **entire JSON body** as the request message (`bodyField = "*"`). This
needs no annotations at all. Example: method `greeter.v1.Greeter/SayHello`
becomes `POST /greeter.v1.Greeter/SayHello`.

If a backend has a `base_url`, it is prefixed: `POST /<base_url>/<pkg>.<Svc>/<Method>`.

### `annotation`

Reads the `google.api.http` option from each method descriptor
(`annotations.go`) and builds the REST route it declares — e.g.

```protobuf
rpc GetBook(GetBookRequest) returns (Book) {
  option (google.api.http) = { get: "/v1/books/{name}" };
}
```

becomes `GET /v1/books/{name}`, with `{name}` bound from the path into the
request message. The `body` field of the rule selects which part of the message
the JSON body fills (`"*"`, empty, or a named sub-field). Backends must include
`google/api/annotations.proto` in their descriptors for this to resolve.

### `auto` (default)

For each method, try `annotation` first; if the method has no `google.api.http`
option, fall back to `convention`. This is the most forgiving mode and the
recommended default.

### Path templates

`compilePathTemplate` (`routetable.go`) converts a `google.api.http` path into a
regex and captures variable names. It supports `{var}` and `{var=*}` (single
segment → `[^/]+`) and `{var=**}` (multi-segment → `.+`).

---

## The request hot path

`transcodeRequest` (`transcode.go`) handles one matched request:

1. **Match** — the middleware (`BuildMiddleware`) looks up the method+path in
   the route table. No match → `404` (or `503` if discovery is still pending);
   there is no HTTP fall-through (see [Request outcomes](#request-outcomes)).
2. **Build the request message** — a `dynamicpb.NewMessage(inputDesc)` is
   populated, in order, from:
   - the **JSON body** (`protojson.Unmarshal` into the whole message or the
     selected `bodyField` sub-message; unknown fields are discarded);
   - **path variables** (`{name}` → field `name`);
   - **query parameters** (only for fields not already set by body/path).

   Scalar conversions from string path/query values to the proto field's type
   (int, bool, float, enum, …) are handled by `setStringField`.
3. **Forward auth as metadata** — when `forward_auth_headers = true`, every
   inbound header whose name starts with the configured auth header prefix
   (default `X-AUTH-`) is copied (lower-cased) into outgoing gRPC metadata. This
   is how the upstream sees the authenticated identity the proxy injected.
4. **Invoke** — `entry.backend.conn.Invoke(ctx, "/pkg.Service/Method", reqMsg,
   respMsg)` with the per-request timeout (`request_timeout_secs`).
5. **Respond** — on success, `protojson.Marshal` the response message and return
   `200 application/json`. JSON shape is controlled by `emit_unpopulated` and
   `use_proto_names`. On a gRPC error, render RFC 9457 `application/problem+json`
   (below).

### Status mapping & error format

gRPC errors are returned as **RFC 9457** problem documents
(`Content-Type: application/problem+json`) with the standard grpc-gateway code
mapping (`status.go`):

| gRPC code | HTTP | | gRPC code | HTTP |
|-----------|------|-|-----------|------|
| `OK` | 200 | | `RESOURCE_EXHAUSTED` | 429 |
| `INVALID_ARGUMENT` / `FAILED_PRECONDITION` / `OUT_OF_RANGE` | 400 | | `UNIMPLEMENTED` | 501 |
| `UNAUTHENTICATED` | 401 | | `UNAVAILABLE` | 503 |
| `PERMISSION_DENIED` | 403 | | `DEADLINE_EXCEEDED` | 504 |
| `NOT_FOUND` | 404 | | `CANCELED` | 499 |
| `ALREADY_EXISTS` / `ABORTED` | 409 | | *(default)* | 500 |

Body shape:

```json
{ "type": "about:blank", "title": "NOT_FOUND", "status": 404, "detail": "user not found" }
```

---

## Configuration

The full config and env-var table lives in
[PLUGINS.md](PLUGINS.md#grpc-transcoding-plugin). The **only required gRPC
setting is `enabled = true`** — the backend defaults to `server.target_url`:

```toml
[server]
target_url = "http://my-grpc-service:50051"   # the gRPC backend

[grpc]
enabled = true
```

`[[grpc.backends]]` is **optional** — supply it only for multiple backends or
`base_url` namespacing (it then replaces the `server.target_url` default). When
you do, `server.target_url` (still required core config) **must resolve to one of
the backend addresses**, or boot fails — so it can't drift into pointing at an
unrelated/dead endpoint. Each backend maps to `PROXY_GRPC_BACKENDS_{n}_ADDRESS` /
`…_BASE_URL`; section flags map to `PROXY_GRPC_*` (e.g. `PROXY_GRPC_ENABLED`,
`PROXY_GRPC_ROUTE_MODE`).

---

## Running a gRPC backend locally

The repo ships a tiny, self-contained gRPC server, [`cmd/grpc-echo`](../cmd/grpc-echo/main.go),
used to exercise the plugin without standing up a real service. It builds with
the modules already in the go.mod (no protoc) and exposes everything the plugin
requires:

- **server reflection** (so routes/schemas can be discovered), and
- **health checking** (`grpc.health.v1.Health`).

It serves `greeter.v1.Greeter`:

| Method | Request | Reply | Notes |
|--------|---------|-------|-------|
| `SayHello` | `{ "name": "..." }` | `{ "message": "Hello, ...!" }` | `name == "error"` → `NOT_FOUND`; `name == "invalid"` → `INVALID_ARGUMENT` |
| `Echo` | `{ "message": "..." }` | `{ "message": "...", "user_id": "..." }` | echoes `x-auth-user-id` metadata into `user_id` |

It can also be started in **degraded modes** to exercise the plugin's negative
startup paths (see [Startup & readiness](#startup--readiness-health-check-driven)):

| Flag | Env var | Effect |
|------|---------|--------|
| `-no-health` | `GRPC_ECHO_NO_HEALTH=1` | omit `grpc.health.v1.Health` |
| `-no-reflection` | `GRPC_ECHO_NO_REFLECTION=1` | omit server reflection |

### 1. Start the backend

```bash
go run ./cmd/grpc-echo -addr :50051        # or GRPC_ECHO_ADDR=:50051
```

### 2. Point the flex proxy at it

```bash
PROXY_GRPC_ENABLED=true \
PROXY_GRPC_ROUTE_MODE=convention \
PROXY_GRPC_BACKENDS_0_ADDRESS=127.0.0.1:50051 \
PROXY_AUTH_JWT_ENABLED=false \
PROXY_SERVER_TARGET_URL=http://127.0.0.1:9 \
./bin/flex-auth-proxy -config config/config-flex.toml
```

### 3. Call it over REST

```bash
# success → 200 application/json
curl -s -X POST -d '{"name":"world"}' localhost:8888/greeter.v1.Greeter/SayHello
# {"message":"Hello, world!"}

# gRPC NOT_FOUND → 404 application/problem+json
curl -s -X POST -d '{"name":"error"}' localhost:8888/greeter.v1.Greeter/SayHello
# {"type":"about:blank","title":"NotFound","status":404,"detail":"user not found"}
```

If you have [`grpcurl`](https://github.com/fullstorydev/grpcurl) installed you
can talk to the backend directly to confirm reflection and health:

```bash
grpcurl -plaintext localhost:50051 list
grpcurl -plaintext localhost:50051 grpc.health.v1.Health/Check
grpcurl -plaintext -d '{"name":"world"}' localhost:50051 greeter.v1.Greeter/SayHello
```

---

## Testing

### Integration tests (in-process)

[`plugin_test.go`](../internal/plugins/grpctranscode/plugin_test.go) builds the
same dynamic `greeter`-style server in-process and drives the full proxy stack.
Run them with the rest of the integration suite:

```bash
make test-all     # go test -tags=integration ./...
```

They cover convention-mode routing, `base_url` prefixes, gRPC→HTTP status
mapping, multi-method discovery, auth-header forwarding, and the negative boot
failures for a backend missing health (`TestGRPCTranscodeMissingHealth`) or
reflection (`TestGRPCTranscodeMissingReflection`).

### End-to-end tests (black-box, Docker)

The Gherkin/behave e2e suite ([`e2e/`](../e2e)) runs the **real Docker image**
in front of the `grpc-echo` backend and asserts the externally-visible
behaviour. The stack (`e2e/compose/docker-compose.e2e.yml`) adds two services:

- **`grpc-echo`** — built from [`Dockerfile.grpcecho`](../Dockerfile.grpcecho).
- **`proxy-grpc`** — the image under test, with the gRPC plugin enabled
  (convention mode) pointed at `grpc-echo`, exposed on port `8890`.

The scenarios live in
[`e2e/features/grpc_transcoding.feature`](../e2e/features/grpc_transcoding.feature)
and are tagged `@flex-only @grpc` — they self-skip on the lite image and against
remote targets. Run them:

```bash
make e2e-flex                                   # whole suite (build the image first)
# or just this feature:
cd e2e && ./run.sh local flex features/grpc_transcoding.feature
```

They assert: JSON transcoding round-trips, a second method on the same backend
is reachable, gRPC `NOT_FOUND`/`INVALID_ARGUMENT` map to `404`/`400 problem+json`,
and a path matching no gRPC method returns `404 problem+json` (no fall-through).

**Negative e2e** lives in
[`e2e/features/grpc_negative.feature`](../e2e/features/grpc_negative.feature)
(tagged `@flex-only @grpc @negative @local-only`). It stands up a throwaway
stack with a deliberately broken `grpc-echo` (`-no-health` / `-no-reflection`,
via `docker-compose.grpc-negative.yml`) and asserts the proxy **stays up** and
its `/healthz` returns `503` naming `health` / `reflection` respectively — i.e.
it reports the unusable backend rather than crash-looping.

---

## Limitations

- **Unary only.** Client/server/bidi streaming methods are skipped during
  discovery.
- **Backends must expose reflection and health.** There is config for a baked
  `descriptor_set_path` fallback, but reflection is the supported discovery path
  today.
- **First-match routing.** The route table returns the first matching entry; in
  `auto` mode an annotation route shadows the convention route for the same
  method, which is the intended precedence.

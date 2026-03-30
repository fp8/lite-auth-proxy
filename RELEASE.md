# lite-auth-proxy

# 1.1.0 [TBD]

* Added optional `/admin` endpoint to configure proxy on the fly

## DDoS Hardening

* **Pre-auth rate limiting:** Rate limiter now applies to all requests regardless of authentication status, preventing attackers from sending unlimited requests with invalid credentials
* **Request body size limit:** Added `BodyLimiter` middleware that rejects requests exceeding `max_body_bytes` (default 1 MiB) with 413 status; checks `Content-Length` header upfront and wraps body with `http.MaxBytesReader` for streaming protection
* **Server timeouts:** Added `ReadTimeout` (30s), `WriteTimeout` (60s), and `IdleTimeout` (120s) to mitigate slowloris-style attacks
* **Removed API key rate limit double-counting:** Handler-level IP rate limiting for API key auth removed since middleware already enforces it
* Added `PROXY_SECURITY_MAX_BODY_BYTES` env var and `security.max_body_bytes` TOML config option

# 1.0.4 [2026-03-01]

* Added `-healthcheck` command line option for docker compose
* Merged `newReverseProxy` and `newHealthProxy` to fix `PROXY_SERVER_HEALTH_CHECK_TARGET`
* Renamed `configs` directory to `config`
* Fixed the bug where `auth.jwt.filters` and `auth.jwt.mappings` was not applied the ENV override
* Added ENV overrides: `PROXY_SERVER_INCLUDE_PATHS`, `PROXY_SERVER_EXCLUDE_PATHS`, `PROXY_AUTH_JWT_FILTERS_*`, `PROXY_AUTH_JWT_MAPPINGS_*`, `PROXY_AUTH_JWT_TOLERANCE_SECS`, `PROXY_AUTH_JWT_CACHE_TTL_MINS`, `PROXY_AUTH_API_KEY_PAYLOAD_*`

# 1.0.2 [2026-02-28]

* Initial Release

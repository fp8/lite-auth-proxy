# lite-auth-proxy

# 1.0.4 [2026-03-01]

* Added `-healthcheck` command line option for docker compose
* Merged `newReverseProxy` and `newHealthProxy` to fix `PROXY_SERVER_HEALTH_CHECK_TARGET`
* Renamed `configs` directory to `config`
* Fixed the bug where `auth.jwt.filters` and `auth.jwt.mappings` was not applied the ENV override
* Added ENV overrides: `PROXY_SERVER_INCLUDE_PATHS`, `PROXY_SERVER_EXCLUDE_PATHS`, `PROXY_AUTH_JWT_FILTERS_*`, `PROXY_AUTH_JWT_MAPPINGS_*`, `PROXY_AUTH_JWT_TOLERANCE_SECS`, `PROXY_AUTH_JWT_CACHE_TTL_MINS`, `PROXY_AUTH_API_KEY_PAYLOAD_*`

# 1.0.2 [2026-02-28]

* Initial Release

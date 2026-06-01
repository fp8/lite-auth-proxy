@flex-only @grpc @negative @local-only
Feature: gRPC transcoding — a broken backend is reported, not fatal
  The transcoding plugin needs two things from a gRPC backend: a health check
  (so it knows the backend is ready) and server reflection (so it can discover
  services and message schemas). In a sidecar the backend may be slow to start,
  so the proxy must NOT crash when they are missing — it boots, keeps serving
  /healthz, and reports the backend as not-ready there with a clear message.
  Once the backend becomes available, /healthz recovers on its own.

  These scenarios stand up a deliberately broken backend in a throwaway stack
  and probe the proxy's /healthz. They are skipped on the lite image (no
  plugins) and against remote targets (no local Docker stack).

  Scenario: A backend with no health check is reported via /healthz, not fatal
    Given a gRPC backend that is missing its health check
    When the gRPC-transcoding proxy starts against that backend
    Then the proxy should still be serving its health endpoint
    And the health endpoint should report status 503
    And the health endpoint error should mention "health"

  Scenario: A backend with no reflection is reported via /healthz, not fatal
    Given a gRPC backend that is missing its server reflection
    When the gRPC-transcoding proxy starts against that backend
    Then the proxy should still be serving its health endpoint
    And the health endpoint should report status 503
    And the health endpoint error should mention "reflection"

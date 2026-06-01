@local-only
Feature: Rate limiting
  The proxy protects the upstream from floods. When a single client sends more
  requests than allowed in a short window, further requests are turned away
  with HTTP 429 and a Retry-After hint.

  This runs against a dedicated helper proxy configured with a tiny limit, so
  it does not interfere with the other scenarios (and is skipped against a
  remote deployment, where flooding a live service would be unwise).

  Scenario: A burst of requests gets rate limited
    Given the rate-limit proxy is running
    When I send 8 requests in quick succession to "/"
    Then at least one of the responses should have status 429
    And the last rate-limited response should report error "rate_limited"

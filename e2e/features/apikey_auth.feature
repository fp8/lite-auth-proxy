@flex-only
Feature: API key authentication
  As an alternative to a user login, internal callers can present a shared API
  key. A valid key is accepted and a service identity is forwarded upstream.

  Scenario: A request with a valid API key is allowed through
    Given the proxy is running
    When I send a request to "/" with a valid API key
    Then the response status should be 200
    And the upstream should have received header "X-AUTH-SERVICE" equal to "internal"

  Scenario: A request with a wrong API key is rejected
    Given the proxy is running
    When I send a request to "/" with an invalid API key
    Then the response status should be 401
    And the response JSON field "error" should be "unauthorized"

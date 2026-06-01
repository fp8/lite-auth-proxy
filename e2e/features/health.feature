Feature: Health check
  The proxy must answer its health endpoint without requiring any credentials,
  so that load balancers and orchestrators can tell it is alive.

  @smoke
  Scenario: The health endpoint is open and reports OK
    Given the proxy is running
    When I send a request to "/healthz"
    Then the response status should be 200
    And the response JSON field "status" should be "ok"

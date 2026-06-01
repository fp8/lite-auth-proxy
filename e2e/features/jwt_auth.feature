@jwt
Feature: JWT authentication
  Requests carrying a valid Firebase login token are let through and the user's
  identity is passed to the upstream service. Anything else is rejected.

  Scenario: A request with a valid token is allowed through
    Given the proxy is running
    When I send a request to "/" with a valid token
    Then the response status should be 200
    And the upstream should have received header "X-AUTH-USER-EMAIL" for the logged-in user

  Scenario: A request with no credentials is rejected
    Given the proxy is running
    When I send a request to "/"
    Then the response status should be 401
    And the response JSON field "error" should be "unauthorized"

  Scenario: A request with a malformed token is rejected
    Given the proxy is running
    When I send a request to "/" with an invalid token
    Then the response status should be 401 or 400
    And the response JSON field "error" should be "unauthorized"

@flex-only
Feature: Admin control plane is protected
  The admin API can change rate-limiting rules at runtime, so it must never be
  reachable without a valid administrator identity token. These scenarios prove
  the door is locked (we do not need an admin token to verify it is shut).

  Scenario: Changing a rule without credentials is rejected
    Given the proxy is running
    When I POST to "/admin/control" without credentials
    Then the response status should be 401

  Scenario: Reading admin status without credentials is rejected
    Given the proxy is running
    When I send a request to "/admin/status"
    Then the response status should be 401

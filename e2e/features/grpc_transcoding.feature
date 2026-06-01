@flex-only @grpc
Feature: gRPC transcoding
  The flex proxy can speak plain REST/JSON to clients while the upstream is a
  gRPC service. It learns the service, its methods and message shapes at
  runtime via gRPC server reflection — no per-service config — and translates
  JSON requests into unary gRPC calls (and the gRPC replies back into JSON).

  These scenarios run against a dedicated proxy (gRPC plugin enabled, in
  convention mode) sitting in front of the grpc-echo backend, which exposes
  greeter.v1.Greeter with SayHello and Echo. They are skipped on the lite image
  (no plugins) and against remote targets (no local backend).

  Scenario: A JSON request is transcoded to a gRPC call and back
    Given the grpc-transcoding proxy is running
    When I POST JSON '{"name": "world"}' to "/greeter.v1.Greeter/SayHello"
    Then the response status should be 200
    And the response JSON field "message" should be "Hello, world!"

  Scenario: A second method on the same backend is also reachable
    Given the grpc-transcoding proxy is running
    When I POST JSON '{"message": "ping"}' to "/greeter.v1.Greeter/Echo"
    Then the response status should be 200
    And the response JSON field "message" should be "ping"

  Scenario: A gRPC NOT_FOUND is surfaced as HTTP 404 problem+json
    Given the grpc-transcoding proxy is running
    When I POST JSON '{"name": "error"}' to "/greeter.v1.Greeter/SayHello"
    Then the response status should be 404
    And the response content type should be "application/problem+json"

  Scenario: A gRPC INVALID_ARGUMENT is surfaced as HTTP 400 problem+json
    Given the grpc-transcoding proxy is running
    When I POST JSON '{"name": "invalid"}' to "/greeter.v1.Greeter/SayHello"
    Then the response status should be 400
    And the response content type should be "application/problem+json"

  Scenario: A request that matches no gRPC method is rejected with 404 (no HTTP fall-through)
    Given the grpc-transcoding proxy is running
    When I send a request to "/not-a-grpc-route"
    Then the response status should be 404
    And the response content type should be "application/problem+json"

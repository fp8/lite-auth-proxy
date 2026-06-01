"""Step definitions: the reusable vocabulary behind the plain-English scenarios.

These steps are deliberately deterministic — they make real HTTP calls and
assert on the result. They do not interpret anything with AI; the .feature
files are simply a human-readable layer over these fixed building blocks.

We use the regex step matcher (behave anchors each pattern at both ends) so
that closely-related phrases like "...to PATH" and "...to PATH with a valid
token" stay distinct.
"""

import requests
from behave import given, then, use_step_matcher, when

from proxylib.compose import docker_available, probe_degraded_proxy

use_step_matcher("re")

DEFAULT_TIMEOUT = 15


# --------------------------------------------------------------------------- #
# Given — pick which proxy this scenario talks to
# --------------------------------------------------------------------------- #
@given(r"the proxy is running")
def step_proxy_running(context):
    context.target_url = context.settings.base_url
    context.headers = {}


@given(r"the rate-limit proxy is running")
def step_rl_proxy_running(context):
    context.target_url = context.settings.rate_limit_base_url
    context.headers = {}


@given(r"the grpc-transcoding proxy is running")
def step_grpc_proxy_running(context):
    context.target_url = context.settings.grpc_base_url
    context.headers = {}


# --------------------------------------------------------------------------- #
# When — build and send a request. We record the response(s) on the context.
# --------------------------------------------------------------------------- #
def _send(context, path):
    url = context.target_url.rstrip("/") + path
    return requests.get(url, headers=context.headers, timeout=DEFAULT_TIMEOUT)


@when(r'I send a request to "(?P<path>[^"]*)"')
def step_send(context, path):
    context.response = _send(context, path)


@when(r'I send a request to "(?P<path>[^"]*)" with header '
      r'"(?P<name>[^"]*)" set to "(?P<value>[^"]*)"')
def step_send_with_header(context, path, name, value):
    context.headers[name] = value
    context.response = _send(context, path)


@when(r'I send a request to "(?P<path>[^"]*)" with a valid token')
def step_send_valid_token(context, path):
    context.headers["Authorization"] = "Bearer " + context.firebase_token
    context.response = _send(context, path)


@when(r'I send a request to "(?P<path>[^"]*)" with an invalid token')
def step_send_invalid_token(context, path):
    context.headers["Authorization"] = "Bearer not.a.valid.token"
    context.response = _send(context, path)


@when(r'I send a request to "(?P<path>[^"]*)" with a valid API key')
def step_send_valid_apikey(context, path):
    context.headers[context.settings.api_key_header] = context.settings.api_key
    context.response = _send(context, path)


@when(r'I send a request to "(?P<path>[^"]*)" with an invalid API key')
def step_send_invalid_apikey(context, path):
    context.headers[context.settings.api_key_header] = "wrong-key-000000"
    context.response = _send(context, path)


@when(r'I POST to "(?P<path>[^"]*)" without credentials')
def step_post_no_creds(context, path):
    url = context.target_url.rstrip("/") + path
    context.response = requests.post(url, json={}, timeout=DEFAULT_TIMEOUT)


@when(r"I POST JSON '(?P<body>.*)' to \"(?P<path>[^\"]*)\"")
def step_post_json(context, body, path):
    # `body` is sent verbatim as the request payload (the proxy/transcoder is
    # responsible for parsing it), with an explicit JSON content type.
    url = context.target_url.rstrip("/") + path
    headers = dict(context.headers)
    headers["Content-Type"] = "application/json"
    context.response = requests.post(
        url, data=body.encode("utf-8"), headers=headers, timeout=DEFAULT_TIMEOUT
    )


@when(r'I send (?P<count>\d+) requests in quick succession to "(?P<path>[^"]*)"')
def step_send_burst(context, count, path):
    context.responses = [_send(context, path) for _ in range(int(count))]


# --------------------------------------------------------------------------- #
# Then — assertions
# --------------------------------------------------------------------------- #
@then(r"the response status should be (?P<code>\d+)")
def step_status(context, code):
    actual = context.response.status_code
    assert actual == int(code), (
        f"expected status {code} but got {actual}. body: {context.response.text[:300]}"
    )


@then(r"the response status should be (?P<a>\d+) or (?P<b>\d+)")
def step_status_either(context, a, b):
    actual = context.response.status_code
    assert actual in (int(a), int(b)), (
        f"expected status {a} or {b} but got {actual}. "
        f"body: {context.response.text[:300]}"
    )


@then(r'the response JSON field "(?P<field>[^"]*)" should be "(?P<value>[^"]*)"')
def step_json_field(context, field, value):
    data = context.response.json()
    assert str(data.get(field)) == value, (
        f'expected JSON field "{field}" to be "{value}" but got "{data.get(field)}"'
    )


@then(r'the upstream should have received header "(?P<name>[^"]*)" '
      r'equal to "(?P<value>[^"]*)"')
def step_upstream_header(context, name, value):
    # The echo upstream reflects the request it received (headers lower-cased).
    data = context.response.json()
    headers = {k.lower(): v for k, v in data.get("headers", {}).items()}
    actual = headers.get(name.lower())
    assert actual == value, (
        f'expected upstream to receive header "{name}: {value}" but got "{actual}". '
        f"reflected headers: {sorted(headers)}"
    )


@then(r'the upstream should have received header "(?P<name>[^"]*)" '
      r'for the logged-in user')
def step_upstream_header_logged_in_user(context, name):
    # Compares against whoever the login secret identifies — no hard-coded email.
    data = context.response.json()
    headers = {k.lower(): v for k, v in data.get("headers", {}).items()}
    actual = headers.get(name.lower())
    assert actual == context.firebase_email, (
        f'expected upstream to receive header "{name}: {context.firebase_email}" '
        f'but got "{actual}". reflected headers: {sorted(headers)}'
    )


@then(r'the response content type should be "(?P<value>[^"]*)"')
def step_response_content_type(context, value):
    # Compare the media type only, ignoring any "; charset=..." suffix.
    actual = context.response.headers.get("Content-Type", "").split(";")[0].strip()
    assert actual == value, (
        f'expected Content-Type "{value}" but got '
        f'"{context.response.headers.get("Content-Type")}"'
    )


@then(r'the response header "(?P<name>[^"]*)" should be present')
def step_response_header_present(context, name):
    assert name in context.response.headers, (
        f'expected response header "{name}" to be present, '
        f"got: {sorted(context.response.headers)}"
    )


@then(r"at least one of the responses should have status (?P<code>\d+)")
def step_burst_contains_status(context, code):
    statuses = [r.status_code for r in context.responses]
    assert int(code) in statuses, (
        f"expected a {code} among burst responses, got {statuses}"
    )


@then(r'the last rate-limited response should report error "(?P<value>[^"]*)"')
def step_last_429_error(context, value):
    limited = [r for r in context.responses if r.status_code == 429]
    assert limited, "no rate-limited (429) response was returned"
    last = limited[-1]
    assert "Retry-After" in last.headers, "429 response missing Retry-After header"
    assert last.json().get("error") == value, (
        f'expected error "{value}", got "{last.json().get("error")}"'
    )


# --------------------------------------------------------------------------- #
# Negative gRPC startup — a broken backend must be reported via /healthz, not
# crash the proxy. Drive a throwaway stack and probe its health endpoint.
# --------------------------------------------------------------------------- #
@given(r"a gRPC backend that is missing its health check")
def step_backend_no_health(context):
    context.grpc_neg_mode = "no-health"


@given(r"a gRPC backend that is missing its server reflection")
def step_backend_no_reflection(context):
    context.grpc_neg_mode = "no-reflection"


@when(r"the gRPC-transcoding proxy starts against that backend")
def step_start_degraded_proxy(context):
    assert docker_available(), "docker is required for @negative scenarios"
    context.health = probe_degraded_proxy(context.grpc_neg_mode)


@then(r"the proxy should still be serving its health endpoint")
def step_proxy_still_serving(context):
    assert context.health.reachable, (
        "expected the proxy to stay up and serve /healthz, but it was never reachable"
    )


@then(r"the health endpoint should report status (?P<code>\d+)")
def step_health_status(context, code):
    assert context.health.status == int(code), (
        f"expected /healthz status {code}, got {context.health.status}.\n"
        f"body: {context.health.body[:400]}"
    )


@then(r'the health endpoint error should mention "(?P<text>[^"]*)"')
def step_health_error_mentions(context, text):
    assert text.lower() in context.health.body.lower(), (
        f'expected /healthz body to mention "{text}".\nbody: {context.health.body[:400]}'
    )

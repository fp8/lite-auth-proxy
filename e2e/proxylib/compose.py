"""Helper for the @negative gRPC scenarios.

These scenarios assert that the proxy does NOT crash when a gRPC backend is
missing the infrastructure the transcoding plugin requires (health checking or
server reflection). Instead the proxy must boot, serve /healthz, and report the
backend as not-ready there with a clear message.

We drive a tiny dedicated docker-compose stack with a deliberately broken
backend, then probe the proxy's /healthz over HTTP. The proxy image comes from
the PROXY_IMAGE env var (exported by run.sh for the local profile); the
grpc-echo backend is built from source.
"""

from __future__ import annotations

import os
import subprocess
import time
from dataclasses import dataclass

import requests

# This file lives in e2e/proxylib/; the compose file is in e2e/compose/.
_HERE = os.path.dirname(os.path.abspath(__file__))
_COMPOSE_FILE = os.path.join(_HERE, "..", "compose", "docker-compose.grpc-negative.yml")

_PORT = os.environ.get("E2E_GRPC_NEG_PORT", "8896")
_HEALTHZ = f"http://localhost:{_PORT}/healthz"

# Which backend defect each mode injects (env var understood by grpc-echo).
_MODE_ENV = {
    "no-health": "GRPC_ECHO_NO_HEALTH",
    "no-reflection": "GRPC_ECHO_NO_REFLECTION",
}


@dataclass
class HealthResult:
    reachable: bool  # did the proxy's /healthz ever respond?
    status: int  # last /healthz status code (0 if never reachable)
    body: str  # last /healthz body


def docker_available() -> bool:
    try:
        subprocess.run(
            ["docker", "info"],
            stdout=subprocess.DEVNULL,
            stderr=subprocess.DEVNULL,
            check=True,
            timeout=20,
        )
        return True
    except (subprocess.SubprocessError, FileNotFoundError, OSError):
        return False


def probe_degraded_proxy(mode: str, settle_marker: str = "not enabled", timeout: int = 120) -> HealthResult:
    """Bring up a degraded backend + proxy and probe the proxy's /healthz.

    ``mode`` is one of "no-health" / "no-reflection". Returns once /healthz
    reports a settled error (body contains ``settle_marker`` — both the
    "health service not enabled" and "reflection not enabled" errors say "not
    enabled", distinguishing them from the transient "waiting: not reachable"
    state while the backend container is still starting) or the timeout elapses.
    The stack is always torn down afterwards.
    """
    if mode not in _MODE_ENV:
        raise ValueError(f"unknown mode {mode!r}; expected one of {list(_MODE_ENV)}")

    project = "lite-auth-proxy-e2e-neg-" + mode.replace("-", "")
    env = dict(os.environ)
    for var in _MODE_ENV.values():
        env.pop(var, None)
    env[_MODE_ENV[mode]] = "1"

    base = ["docker", "compose", "-f", _COMPOSE_FILE, "-p", project]
    result = HealthResult(reachable=False, status=0, body="")
    try:
        subprocess.run(
            base + ["up", "-d", "--build"],
            env=env,
            stdout=subprocess.PIPE,
            stderr=subprocess.STDOUT,
            text=True,
            check=True,
            timeout=timeout,
        )

        deadline = time.time() + 60
        while time.time() < deadline:
            try:
                resp = requests.get(_HEALTHZ, timeout=5)
                result.reachable = True
                result.status = resp.status_code
                result.body = resp.text
                # Stop once the backend error has settled (not the transient
                # "waiting for backend to become ready" message).
                if resp.status_code == 503 and settle_marker in resp.text:
                    break
            except requests.RequestException:
                pass
            time.sleep(0.5)
        return result
    finally:
        subprocess.run(
            base + ["down", "-v", "--remove-orphans"],
            env=env,
            stdout=subprocess.DEVNULL,
            stderr=subprocess.DEVNULL,
            timeout=60,
        )

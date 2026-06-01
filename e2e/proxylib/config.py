"""Test-run configuration, read from environment variables.

Every value has a sensible default so that, in the common case, you only need
to pick a *profile* (local vs remote) — see ``run.sh``. Override any value by
exporting the matching ``E2E_*`` variable before running.
"""

from __future__ import annotations

import os
from dataclasses import dataclass


def _env(name: str, default: str | None = None) -> str | None:
    value = os.environ.get(name)
    if value is None or value == "":
        return default
    return value


@dataclass
class Settings:
    # --- Targets -------------------------------------------------------------
    # The proxy under test. For the "local" profile this is the docker-compose
    # service; for "remote" it is the deployed service URL.
    base_url: str
    # A second proxy instance configured with a tiny rate limit, used only by
    # the rate-limiting feature. None when not available (e.g. remote profile),
    # in which case @local-only scenarios are skipped.
    rate_limit_base_url: str | None
    # A proxy instance with the gRPC-transcoding plugin enabled, pointed at the
    # grpc-echo backend. None when not available (e.g. remote profile), in which
    # case @grpc scenarios are skipped.
    grpc_base_url: str | None
    # Which image variant is under test: "flex" (all plugins) or "lite".
    build: str

    # --- API key auth (flex only) -------------------------------------------
    api_key: str
    api_key_header: str

    # --- Firebase JWT auth ---------------------------------------------------
    # If a token is supplied directly we use it verbatim (handy for CI).
    # Otherwise we mint one via Firebase using the secrets below. Both secrets
    # live in the same GCP project. The login secret holds "<email>:<password>".
    jwt_token: str | None
    firebase_project: str
    firebase_api_key_secret: str
    firebase_login_secret: str

    @property
    def is_flex(self) -> bool:
        return self.build == "flex"

    @classmethod
    def from_env(cls) -> "Settings":
        return cls(
            base_url=_env("E2E_BASE_URL", "http://localhost:8888"),
            rate_limit_base_url=_env("E2E_RL_BASE_URL"),
            grpc_base_url=_env("E2E_GRPC_BASE_URL"),
            build=(_env("E2E_BUILD", "flex") or "flex").lower(),
            api_key=_env("E2E_API_KEY", "test-api-key-123456"),
            api_key_header=_env("E2E_API_KEY_HEADER", "X-API-KEY"),
            jwt_token=_env("E2E_JWT_TOKEN"),
            # The Firebase Web API key and the login secret both live here.
            firebase_project=_env("E2E_FIREBASE_PROJECT", "fp8devel"),
            firebase_api_key_secret=_env(
                "E2E_FIREBASE_API_KEY_SECRET", "APIKEY_FIREBASE_AUTH_DEV"
            ),
            # Holds "<email>:<password>" for the test user.
            firebase_login_secret=_env(
                "E2E_FIREBASE_LOGIN_SECRET", "LOGIN_FIREBASE_AUTH_DEV"
            ),
        )

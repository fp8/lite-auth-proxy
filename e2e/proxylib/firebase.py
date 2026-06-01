"""Obtain a real Firebase ID token for the e2e test user.

Why real tokens? The proxy validates JWTs against Google's live JWKS endpoint
for the ``fp8devel`` project, so we cannot fake one. Instead we sign in as a
dedicated test user and use the returned ID token as a normal Bearer token.

Mirrors the Go integration test in internal/auth/jwt/realworld_test.go.

Secrets are read from Google Secret Manager via the ``gcloud`` CLI (no extra
Python/GCP dependency, and reuses the developer's existing gcloud login).
"""

from __future__ import annotations

import json
import os
import shutil
import subprocess
import urllib.request
from dataclasses import dataclass
from pathlib import Path

from .config import Settings


@dataclass
class Login:
    """A minted Firebase identity: the Bearer token and the user it belongs to."""

    token: str
    email: str

# gcloud is not always on PATH in non-interactive shells; probe common spots.
_GCLOUD_CANDIDATES = [
    os.environ.get("GCLOUD_BIN", ""),
    "gcloud",
    str(Path.home() / "Developer/google-cloud-sdk/bin/gcloud"),
    str(Path.home() / "google-cloud-sdk/bin/gcloud"),
    "/opt/homebrew/share/google-cloud-sdk/bin/gcloud",
    "/usr/local/share/google-cloud-sdk/bin/gcloud",
]


class TokenError(RuntimeError):
    """Raised when a token could not be obtained. Callers skip, not fail."""


def _gcloud() -> str:
    for candidate in _GCLOUD_CANDIDATES:
        if not candidate:
            continue
        resolved = shutil.which(candidate) or (
            candidate if Path(candidate).is_file() else None
        )
        if resolved:
            return resolved
    raise TokenError(
        "gcloud CLI not found — install it or set GCLOUD_BIN, "
        "or supply a token directly via E2E_JWT_TOKEN"
    )


def _read_secret(project: str, name: str) -> str:
    try:
        out = subprocess.run(
            [
                _gcloud(),
                "secrets",
                "versions",
                "access",
                "latest",
                f"--secret={name}",
                f"--project={project}",
            ],
            capture_output=True,
            text=True,
            timeout=30,
            check=True,
        )
    except subprocess.CalledProcessError as exc:
        raise TokenError(
            f"could not read secret {name} from project {project}: "
            f"{exc.stderr.strip() or exc}"
        ) from exc
    except (OSError, subprocess.TimeoutExpired) as exc:
        raise TokenError(f"gcloud failed reading {name}: {exc}") from exc
    return out.stdout.strip()


def _sign_in(api_key: str, email: str, password: str) -> str:
    url = (
        "https://identitytoolkit.googleapis.com/v1/accounts:"
        f"signInWithPassword?key={api_key}"
    )
    body = json.dumps(
        {"email": email, "password": password, "returnSecureToken": True}
    ).encode()
    req = urllib.request.Request(
        url, data=body, headers={"Content-Type": "application/json"}
    )
    try:
        with urllib.request.urlopen(req, timeout=20) as resp:
            data = json.load(resp)
    except urllib.error.HTTPError as exc:  # type: ignore[attr-defined]
        detail = exc.read().decode(errors="replace")[:300]
        raise TokenError(f"Firebase sign-in failed ({exc.code}): {detail}") from exc
    except OSError as exc:
        raise TokenError(f"Firebase sign-in request failed: {exc}") from exc

    token = data.get("idToken")
    if not token:
        raise TokenError("Firebase sign-in returned no idToken")
    return token


def login(settings: Settings) -> Login:
    """Return a minted Firebase login, raising TokenError on failure.

    The login secret holds "<email>:<password>"; the email is not hard-coded
    here so the test user can be changed by editing the secret alone.
    """
    if settings.jwt_token:
        # A token was supplied directly; recover the email from its claims so
        # downstream assertions still work without contacting Firebase.
        return Login(token=settings.jwt_token, email=_email_from_token(settings.jwt_token))

    api_key = _read_secret(settings.firebase_project, settings.firebase_api_key_secret)
    login_str = _read_secret(settings.firebase_project, settings.firebase_login_secret)

    email, sep, password = login_str.partition(":")
    if not sep or not email or not password:
        raise TokenError(
            f"login secret {settings.firebase_login_secret} is not in "
            '"<email>:<password>" format'
        )

    token = _sign_in(api_key, email, password)
    return Login(token=token, email=email)


def _email_from_token(token: str) -> str:
    import base64

    try:
        payload = token.split(".")[1]
        payload += "=" * (-len(payload) % 4)
        claims = json.loads(base64.urlsafe_b64decode(payload))
        return claims.get("email", "")
    except Exception:  # noqa: BLE001 - best effort for a supplied token
        return ""

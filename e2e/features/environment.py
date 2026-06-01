"""behave lifecycle hooks.

Responsibilities:
  * load test settings once,
  * obtain a Firebase token once (best effort), and
  * automatically SKIP scenarios whose prerequisites aren't met for the
    current run, so the same feature files work for both flex/lite images and
    for local/remote targets.

Tag conventions:
  @flex-only   needs a flex build (API key, admin, gRPC) — skipped on lite
  @local-only  needs the local rate-limit helper container — skipped remotely
  @jwt         needs a Firebase token — skipped if one can't be obtained
  @grpc        needs the local grpc-transcoding proxy + grpc-echo backend —
               skipped remotely (and on lite, via @flex-only)
  @negative    drives a throwaway Docker stack to assert a boot failure —
               needs a local Docker daemon and a built proxy image (PROXY_IMAGE)
"""

import os
import sys

# Make the proxylib package importable from step files (run dir is e2e/).
sys.path.insert(0, os.path.dirname(os.path.dirname(os.path.abspath(__file__))))

from proxylib.compose import docker_available  # noqa: E402
from proxylib.config import Settings  # noqa: E402
from proxylib.firebase import TokenError, login  # noqa: E402


def before_all(context):
    context.settings = Settings.from_env()
    context.firebase_token = None
    context.firebase_email = None
    context.firebase_token_error = None

    # Try once up front so every @jwt scenario reuses the same token.
    try:
        creds = login(context.settings)
        context.firebase_token = creds.token
        context.firebase_email = creds.email
    except TokenError as exc:
        context.firebase_token_error = str(exc)

    s = context.settings
    print(f"\n[e2e] build={s.build} base_url={s.base_url} "
          f"rate_limit_url={s.rate_limit_base_url or '(none)'} "
          f"jwt={'ready' if context.firebase_token else 'unavailable'}")
    if context.firebase_token_error:
        print(f"[e2e] JWT scenarios will be skipped: {context.firebase_token_error}")


def before_scenario(context, scenario):
    tags = set(scenario.effective_tags)
    s = context.settings

    if "flex-only" in tags and not s.is_flex:
        scenario.skip("requires the flex build (lite has no plugins)")
        return

    if "local-only" in tags and not s.rate_limit_base_url:
        scenario.skip("requires the local rate-limit helper container")
        return

    if "grpc" in tags and not s.grpc_base_url:
        scenario.skip("requires the local grpc-transcoding proxy + grpc-echo backend")
        return

    if "negative" in tags and (not os.environ.get("PROXY_IMAGE") or not docker_available()):
        scenario.skip("requires a local Docker daemon and a built proxy image (PROXY_IMAGE)")
        return

    if "jwt" in tags and not context.firebase_token:
        scenario.skip(
            "no Firebase token available: " + (context.firebase_token_error or "")
        )
        return

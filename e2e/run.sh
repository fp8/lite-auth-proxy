#!/usr/bin/env bash
#
# run.sh — run the end-to-end test suite against the proxy.
#
# Usage:
#   ./run.sh local  [flex|lite]      Test the local Docker image (reuses the
#                                    existing image — build it first, or pass
#                                    E2E_BUILD_IMAGE=1 to build it here).
#   ./run.sh remote  <BASE_URL>      Test an already-deployed service.
#   ./run.sh                         Same as: ./run.sh local flex
#
# Examples:
#   ./run.sh local flex
#   ./run.sh local lite
#   E2E_BUILD_IMAGE=1 ./run.sh local flex     # (re)build the image first
#   ./run.sh remote https://my-proxy-abc123-uc.a.run.app
#
# Extra arguments after the required ones are passed straight to behave, e.g.:
#   ./run.sh local flex --tags=@smoke
#   ./run.sh local flex features/jwt_auth.feature
#
# Environment knobs:
#   E2E_BUILD_IMAGE=1  (Re)build the Docker image before testing. Default: off
#                      (reuse the existing local image).
#   E2E_JWT_TOKEN=...  Supply a Bearer token directly (skips Firebase sign-in).
#   E2E_API_KEY=...    Override the API key (default test-api-key-123456).

set -euo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT="$(cd "$HERE/.." && pwd)"
COMPOSE="$HERE/compose/docker-compose.e2e.yml"
PROJECT="lite-auth-proxy-e2e"

# Make uv and gcloud reachable even from a bare non-interactive shell.
export PATH="$HOME/.local/bin:$HOME/Developer/google-cloud-sdk/bin:$HOME/google-cloud-sdk/bin:$PATH"

# Load optional overrides from the repo-root .env (see .env.example). Profile
# values computed below (E2E_BASE_URL, etc.) still take precedence.
if [[ -f "$ROOT/.env" ]]; then
  set -a; . "$ROOT/.env"; set +a
fi

command -v uv >/dev/null 2>&1 || {
  echo "error: 'uv' not found. Install from https://docs.astral.sh/uv/ then retry." >&2
  exit 1
}

profile="${1:-local}"

wait_for_health() {
  local url="$1" name="$2" tries=60
  echo "[run] waiting for $name at $url ..."
  while (( tries-- > 0 )); do
    if curl -fsS -o /dev/null "$url" 2>/dev/null; then
      echo "[run] $name is up."
      return 0
    fi
    sleep 1
  done
  echo "error: $name did not become healthy at $url" >&2
  return 1
}

run_behave() {
  # behave auto-skips scenarios whose prerequisites aren't met (see
  # features/environment.py), so we always run the full suite.
  ( cd "$HERE" && uv run --quiet -- behave "$@" )
}

case "$profile" in
  local)
    variant="${2:-flex}"
    shift "$(( $# >= 2 ? 2 : 1 ))" || true
    if [[ "$variant" != "flex" && "$variant" != "lite" ]]; then
      echo "error: variant must be 'flex' or 'lite', got '$variant'" >&2
      exit 2
    fi

    version="$(grep 'Version = ' "$ROOT/cmd/flex/main.go" | head -1 | sed 's/.*"\(.*\)".*/\1/')"
    export PROXY_IMAGE="farport/${variant}-auth-proxy:${version}"

    # By default we reuse the existing local image (fast). Set E2E_BUILD_IMAGE=1
    # to (re)build it first. If it's missing and we're not building, bail early
    # with a clear hint instead of a confusing compose error.
    if [[ "${E2E_BUILD_IMAGE:-0}" == "1" ]]; then
      echo "[run] building ${variant} image ($PROXY_IMAGE) ..."
      make -C "$ROOT" "docker-build-${variant}"
    elif ! docker image inspect "$PROXY_IMAGE" >/dev/null 2>&1; then
      echo "error: image $PROXY_IMAGE not found locally." >&2
      echo "       build it with:  make docker-build-${variant}" >&2
      echo "       or re-run with: E2E_BUILD_IMAGE=1 $0 local ${variant}" >&2
      exit 1
    fi

    # Flex exposes API-key auth and the admin control plane; lite does not.
    if [[ "$variant" == "flex" ]]; then
      export E2E_APIKEY_ENABLED=true E2E_ADMIN_ENABLED=true
    else
      export E2E_APIKEY_ENABLED=false E2E_ADMIN_ENABLED=false
    fi

    cleanup() {
      echo "[run] tearing down stack ..."
      docker compose -f "$COMPOSE" -p "$PROJECT" down --remove-orphans >/dev/null 2>&1 || true
    }
    trap cleanup EXIT

    echo "[run] starting stack ..."
    # --build keeps the locally-built grpc-echo helper image (the only service
    # with a build context) in sync with the source on every run.
    docker compose -f "$COMPOSE" -p "$PROJECT" up -d --build --remove-orphans

    wait_for_health "http://localhost:8888/healthz" "proxy"
    wait_for_health "http://localhost:8889/healthz" "rate-limit proxy"
    wait_for_health "http://localhost:8890/healthz" "grpc-transcoding proxy"

    export E2E_BUILD="$variant"
    export E2E_BASE_URL="http://localhost:8888"
    export E2E_RL_BASE_URL="http://localhost:8889"
    export E2E_GRPC_BASE_URL="http://localhost:8890"
    run_behave "$@"
    ;;

  remote)
    url="${2:-}"
    if [[ -z "$url" ]]; then
      echo "error: remote profile requires a BASE_URL, e.g. ./run.sh remote https://host" >&2
      exit 2
    fi
    shift "$(( $# >= 2 ? 2 : 1 ))" || true

    export E2E_BUILD="${E2E_BUILD:-flex}"
    export E2E_BASE_URL="$url"
    unset E2E_RL_BASE_URL  # never flood a live service: @local-only scenarios skip
    echo "[run] testing remote target $url (build=$E2E_BUILD)"
    run_behave "$@"
    ;;

  *)
    echo "error: first argument must be 'local' or 'remote', got '$profile'" >&2
    exit 2
    ;;
esac

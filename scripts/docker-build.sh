#!/usr/bin/env bash
#
# docker-build.sh — single source of truth for building (and optionally
# pushing) the flex/lite proxy Docker images. Shared by the Makefile and the
# GitHub Actions release workflow so both produce identical images and tags.
#
# Usage:
#   scripts/docker-build.sh <flex|lite>
#
# Configuration (environment variables):
#   IMAGE             Full image repo. Default: farport/<variant>-auth-proxy
#   VERSION           Version string. Default: read from cmd/flex/main.go
#   PUSH              true|false — push to registry (buildx --push) instead of
#                     loading into the local docker (--load). Default: false
#   PLATFORMS         buildx --platform value. Default: linux/amd64,linux/arm64
#                     (amd64 = Intel, arm64 = Apple Silicon / ARM Linux).
#   EXTRA_TAGS        Space-separated extra tag suffixes (e.g. "edge rc1")
#   BUILD_EXTRA_ARGS  Extra args appended to `docker buildx build`
#                     (e.g. cache flags used by CI)
#   BUILDX_BUILDER    buildx builder to use for multi-platform builds.
#                     Default: auto-create/use a "multiarch" docker-container.
#
# Image is always tagged with the full VERSION and the MAJOR.MINOR derived
# from it (plus any EXTRA_TAGS). It is never tagged :latest.
#
# Multi-platform note: a multi-arch manifest can only live in a registry, so
# PUSH=true builds all PLATFORMS, while a local build (--load) can only load a
# single arch and therefore builds for the host architecture only.

set -euo pipefail

VARIANT="${1:-${VARIANT:-}}"
if [[ "$VARIANT" != "flex" && "$VARIANT" != "lite" ]]; then
  echo "Usage: $0 <flex|lite>  (or set VARIANT)" >&2
  exit 1
fi

# Resolve repo root (this script lives in <root>/scripts/).
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

# Version is read from cmd/flex/main.go — the project's source of truth.
VERSION="${VERSION:-$(grep 'Version = ' cmd/flex/main.go | head -1 | sed 's/.*"\(.*\)".*/\1/')}"
if [[ -z "$VERSION" ]]; then
  echo "Error: could not resolve VERSION from cmd/flex/main.go" >&2
  exit 1
fi
MAJOR_MINOR="$(echo "$VERSION" | cut -d. -f1,2)"

IMAGE="${IMAGE:-farport/${VARIANT}-auth-proxy}"
PUSH="${PUSH:-false}"
PLATFORMS="${PLATFORMS:-linux/amd64,linux/arm64}"
DOCKERFILE="Dockerfile.${VARIANT}"

# Assemble the tag list: full version + major.minor (never :latest).
tags=("${IMAGE}:${VERSION}" "${IMAGE}:${MAJOR_MINOR}")
for t in ${EXTRA_TAGS:-}; do
  tags+=("${IMAGE}:${t}")
done

tag_args=()
for t in "${tags[@]}"; do
  tag_args+=("-t" "$t")
done

if [[ "$PUSH" == "true" ]]; then
  # Push the full multi-platform manifest to the registry.
  output_arg="--push"
  build_platforms="$PLATFORMS"
else
  # --load can only import a single architecture into the local docker, so a
  # local build targets the host architecture only.
  output_arg="--load"
  host_arch="$(uname -m)"
  case "$host_arch" in
    x86_64 | amd64) host_arch=amd64 ;;
    arm64 | aarch64) host_arch=arm64 ;;
  esac
  build_platforms="linux/${host_arch}"
  if [[ "$PLATFORMS" == *,* ]]; then
    echo "Note: local build loads linux/${host_arch} only; pushed images (PUSH=true) cover ${PLATFORMS}."
  fi
fi

# Multi-platform builds require a "docker-container" buildx builder; the default
# "docker" driver can only build for the host. Select/create one when needed.
builder_args=()
if [[ "$build_platforms" == *,* ]]; then
  if [[ -n "${BUILDX_BUILDER:-}" ]]; then
    builder_args=(--builder "$BUILDX_BUILDER")
  elif ! docker buildx inspect 2>/dev/null | grep -q 'Driver:[[:space:]]*docker-container'; then
    # Current builder can't do multi-platform — create/reuse a dedicated one.
    docker buildx inspect multiarch >/dev/null 2>&1 \
      || docker buildx create --name multiarch --driver docker-container >/dev/null
    builder_args=(--builder multiarch)
  fi
fi

echo "Building ${IMAGE} (variant=${VARIANT}, version=${VERSION}, push=${PUSH}, platforms=${build_platforms})"
printf '  tag: %s\n' "${tags[@]}"

set -x
docker buildx build \
  ${builder_args[@]+"${builder_args[@]}"} \
  --platform "$build_platforms" \
  --build-arg "VERSION=${VERSION}" \
  -f "$DOCKERFILE" \
  "${tag_args[@]}" \
  ${BUILD_EXTRA_ARGS:-} \
  "$output_arg" \
  .

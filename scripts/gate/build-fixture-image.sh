#!/usr/bin/env bash
# build-fixture-image.sh — build the mcpfixture:gate Docker image for the
# release QA gate's docker-isolated stdio matrix cell (Spec 081, FR-006/FR-009).
#
# The gate configures the docker cell's upstream as:
#
#   command:   /mcpfixture
#   args:      ["--transport", "stdio"]
#   isolation: { enabled: true, image: "mcpfixture:gate" }
#
# Verified argv composition (internal/upstream/core/isolation.go +
# connection_docker.go):
#   - DetectRuntimeType(filepath.Base("/mcpfixture")) → "binary", so
#     TransformCommandForContainer passes command+args through unchanged.
#   - GetDockerImage → buildFullImageName("mcpfixture:gate") →
#     "docker.io/library/mcpfixture:gate" (no slash ⇒ registry+library prefix).
#     Docker normalizes local short tags identically, so the locally built
#     `mcpfixture:gate` resolves from the local image store without any pull —
#     the gate remains free of third-party network dependencies (base image is
#     `scratch`, which is virtual and never pulled).
#   - Final: docker run --rm -i --name mcpproxy-<server>-<rand> <labels> ...
#            docker.io/library/mcpfixture:gate /mcpfixture --transport stdio
#
# The binary is fully static (CGO_ENABLED=0, pure Go), so the image is
# FROM scratch with no ENTRYPOINT (see cmd/mcpfixture/Dockerfile for why).
#
# Usage: scripts/gate/build-fixture-image.sh [--tag mcpfixture:gate] [--arch amd64|arm64]
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
TAG="mcpfixture:gate"
# Match the docker daemon's architecture by default so the image runs without
# emulation (linux/amd64 on GitHub runners, linux/arm64 on Apple Silicon).
ARCH=""

while [[ $# -gt 0 ]]; do
  case "$1" in
    --tag)  TAG="$2"; shift 2 ;;
    --arch) ARCH="$2"; shift 2 ;;
    *) echo "unknown argument: $1" >&2; exit 2 ;;
  esac
done

if ! command -v docker >/dev/null 2>&1; then
  echo "ERROR: docker CLI not found — the docker gate cell requires Docker (FR-009: no silent fallback)" >&2
  exit 1
fi
if ! docker info >/dev/null 2>&1; then
  echo "ERROR: docker daemon unreachable — the docker gate cell requires Docker (FR-009: no silent fallback)" >&2
  exit 1
fi

if [[ -z "$ARCH" ]]; then
  ARCH="$(docker info --format '{{.Architecture}}' 2>/dev/null || true)"
  case "$ARCH" in
    x86_64|amd64) ARCH=amd64 ;;
    aarch64|arm64) ARCH=arm64 ;;
    *) echo "WARN: could not detect daemon arch (got '${ARCH}'), defaulting to amd64" >&2; ARCH=amd64 ;;
  esac
fi

BUILD_DIR="$(mktemp -d)"
trap 'rm -rf "$BUILD_DIR"' EXIT

echo "==> building static linux/${ARCH} mcpfixture binary"
(cd "$REPO_ROOT" && CGO_ENABLED=0 GOOS=linux GOARCH="$ARCH" \
  go build -trimpath -ldflags="-s -w" -o "$BUILD_DIR/mcpfixture" ./cmd/mcpfixture)

cp "$REPO_ROOT/cmd/mcpfixture/Dockerfile" "$BUILD_DIR/Dockerfile"

echo "==> docker build ${TAG} (linux/${ARCH}, FROM scratch — zero pulls)"
docker build --platform "linux/${ARCH}" -t "$TAG" "$BUILD_DIR"

echo "==> smoke: image runs and answers an MCP initialize over stdio"
SMOKE_REQ='{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"smoke","version":"0"}}}'
SMOKE_OUT="$(printf '%s\n' "$SMOKE_REQ" | docker run --rm -i --platform "linux/${ARCH}" "$TAG" /mcpfixture --transport stdio | head -n 1)"
if [[ "$SMOKE_OUT" != *'"mcpfixture"'* ]]; then
  echo "ERROR: smoke initialize failed; got: $SMOKE_OUT" >&2
  exit 1
fi

echo "OK: built ${TAG} (linux/${ARCH})"

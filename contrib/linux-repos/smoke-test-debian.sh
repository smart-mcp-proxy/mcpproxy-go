#!/usr/bin/env bash
# Smoke-test: install mcpproxy via apt in a debian:stable-slim container.
#
# Usage:
#   smoke-test-debian.sh VERSION
# Where VERSION is the expected version string (without leading v), e.g. 0.24.7.

set -euo pipefail

VERSION="${1:-}"
if [[ -z "${VERSION}" ]]; then
  echo "error: usage: smoke-test-debian.sh VERSION" >&2
  exit 1
fi

echo "smoke-test-debian: expecting version ${VERSION}"

docker run --rm debian:stable-slim bash -c '
  set -euo pipefail
  apt-get -qq update
  DEBIAN_FRONTEND=noninteractive apt-get -qq install -y curl ca-certificates gnupg >/dev/null
  install -m 0755 -d /etc/apt/keyrings
  curl -fsSL https://apt.mcpproxy.app/mcpproxy.gpg -o /etc/apt/keyrings/mcpproxy.gpg
  echo "deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/mcpproxy.gpg] https://apt.mcpproxy.app stable main" \
    > /etc/apt/sources.list.d/mcpproxy.list
  apt-get -qq update
  apt-get -qq install -y mcpproxy
  mcpproxy --version
' | tee /tmp/smoke-debian.out

actual=$(grep -oE 'v?[0-9]+\.[0-9]+\.[0-9]+' /tmp/smoke-debian.out | head -1 | sed 's/^v//')
if [[ "${actual}" != "${VERSION}" ]]; then
  echo "error: mcpproxy --version reported '${actual}' (extracted), expected '${VERSION}'" >&2
  exit 1
fi

echo "smoke-test-debian: OK (installed ${actual})"

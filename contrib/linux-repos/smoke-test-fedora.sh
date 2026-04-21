#!/usr/bin/env bash
# Smoke-test: install mcpproxy via dnf in a fedora:latest container.
#
# Usage:
#   smoke-test-fedora.sh VERSION

set -euo pipefail

VERSION="${1:-}"
if [[ -z "${VERSION}" ]]; then
  echo "error: usage: smoke-test-fedora.sh VERSION" >&2
  exit 1
fi

echo "smoke-test-fedora: expecting version ${VERSION}"

docker run --rm fedora:latest bash -c '
  set -euo pipefail
  dnf -q -y install dnf-plugins-core >/dev/null
  # Fedora 41+ uses dnf5 which dropped config-manager; fall back to writing the .repo file directly
  if dnf config-manager --help 2>/dev/null | grep -q "add-repo"; then
    dnf -q config-manager addrepo --from-repofile=https://rpm.mcpproxy.app/mcpproxy.repo 2>/dev/null \
      || dnf -q config-manager --add-repo=https://rpm.mcpproxy.app/mcpproxy.repo
  else
    curl -fsSL https://rpm.mcpproxy.app/mcpproxy.repo -o /etc/yum.repos.d/mcpproxy.repo
  fi
  dnf -q -y install mcpproxy
  mcpproxy --version
' | tee /tmp/smoke-fedora.out

actual=$(grep -oE 'v?[0-9]+\.[0-9]+\.[0-9]+' /tmp/smoke-fedora.out | head -1 | sed 's/^v//')
if [[ "${actual}" != "${VERSION}" ]]; then
  echo "error: mcpproxy --version reported '${actual}' (extracted), expected '${VERSION}'" >&2
  exit 1
fi

echo "smoke-test-fedora: OK (installed ${actual})"

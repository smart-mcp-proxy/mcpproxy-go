#!/usr/bin/env bash
# Render the scoop manifest for a release (Spec 121 FR-010).
#
#   scripts/gen-scoop-manifest.sh v0.71.1 checksums.txt > mcpproxy.json
#
# Archive URLs and SHA-256 hashes come from the release's own checksums.txt
# (the single hash source for every asset), so a manifest can never describe a
# file the release did not publish. Scoop installs the flat release zip
# (mcpproxy.exe + mcpproxy-tray.exe): no installer, no admin rights, and a
# path the update-channel detector recognises as scoop-owned.
set -euo pipefail

TAG="${1:-}"
SUMS="${2:-}"
REPO="${REPO:-smart-mcp-proxy/mcpproxy-go}"

if [[ ! "$TAG" =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
  echo "gen-scoop-manifest: tag must be a stable vX.Y.Z (got '${TAG}')" >&2
  exit 2
fi
if [[ -z "$SUMS" || ! -f "$SUMS" ]]; then
  echo "gen-scoop-manifest: checksums file '${SUMS}' not found" >&2
  exit 2
fi
VER="${TAG#v}"

hash_for() { # <asset name>
  local h
  h="$(awk -v f="$1" '$2 == f {print $1; exit}' "$SUMS")"
  if [[ ! "$h" =~ ^[a-f0-9]{64}$ ]]; then
    echo "gen-scoop-manifest: no sha256 for $1 in $SUMS" >&2
    exit 3
  fi
  printf '%s' "$h"
}

AMD="mcpproxy-${VER}-windows-amd64.zip"
ARM="mcpproxy-${VER}-windows-arm64.zip"
BASE="https://github.com/${REPO}/releases/download/${TAG}"

# Resolve hashes into variables first: an `exit` inside a command substitution
# that sits in an argument list would not abort the script.
AMD_HASH="$(hash_for "$AMD")" || exit 3
ARM_HASH="$(hash_for "$ARM")" || exit 3

jq -n \
  --arg ver "$VER" --arg repo "$REPO" \
  --arg amd_url "${BASE}/${AMD}" --arg amd_hash "$AMD_HASH" \
  --arg arm_url "${BASE}/${ARM}" --arg arm_hash "$ARM_HASH" '
{
  version: $ver,
  description: "Smart MCP proxy: BM25 tool discovery, security quarantine and token savings for AI agents",
  homepage: "https://mcpproxy.app",
  license: "MIT",
  architecture: {
    "64bit": {url: $amd_url, hash: $amd_hash},
    arm64:   {url: $arm_url, hash: $arm_hash}
  },
  bin: ["mcpproxy.exe"],
  shortcuts: [["mcpproxy-tray.exe", "MCPProxy"]],
  notes: "Start the proxy with `mcpproxy serve` (or launch MCPProxy from the Start menu), then connect your AI client with `mcpproxy connect <client>`. Update with `scoop update mcpproxy`.",
  checkver: {github: ("https://github.com/" + $repo)},
  autoupdate: {
    architecture: {
      "64bit": {url: ("https://github.com/" + $repo + "/releases/download/v$version/mcpproxy-$version-windows-amd64.zip")},
      arm64:   {url: ("https://github.com/" + $repo + "/releases/download/v$version/mcpproxy-$version-windows-arm64.zip")}
    },
    hash: {url: ("https://github.com/" + $repo + "/releases/download/v$version/checksums.txt")}
  }
}'

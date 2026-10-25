#!/usr/bin/env bash
# Tests for scripts/gen-scoop-manifest.sh (Spec 121 T030).
set -euo pipefail
cd "$(dirname "$0")/.."
G=scripts/gen-scoop-manifest.sh
SUMS=scripts/testdata/checksums-v0.71.1.txt
fail() { echo "FAIL: $*" >&2; exit 1; }

out=$($G v0.71.1 "$SUMS")
jq -e . >/dev/null <<<"$out" || fail "not valid JSON"
[[ "$(jq -r .version <<<"$out")" == "0.71.1" ]] || fail "version"
[[ "$(jq -r '.architecture["64bit"].url' <<<"$out")" == "https://github.com/smart-mcp-proxy/mcpproxy-go/releases/download/v0.71.1/mcpproxy-0.71.1-windows-amd64.zip" ]] || fail "amd64 url"
[[ "$(jq -r '.architecture["64bit"].hash' <<<"$out")" == "b12a610322c76eac3a283ae4099497c32c2568be69a46952228a94c6fa9d6d8e" ]] || fail "amd64 hash"
[[ "$(jq -r '.architecture.arm64.hash' <<<"$out")" == "ff924617ed04072ce9a221ce23b608ad6f7e4fff7d59df4471513503dc277ee4" ]] || fail "arm64 hash"
[[ "$(jq -r '.bin[0]' <<<"$out")" == "mcpproxy.exe" ]] || fail "bin"
[[ "$(jq -r '.autoupdate.hash.url' <<<"$out")" == *'v$version/checksums.txt' ]] || fail "autoupdate hash url"
[[ "$(jq -r '.checkver.github' <<<"$out")" == "https://github.com/smart-mcp-proxy/mcpproxy-go" ]] || fail "checkver"

# the -latest aliases must never be picked: the pinned asset name is exact
grep -q 'mcpproxy-latest-windows' <<<"$out" && fail "manifest references a -latest alias"

# fails when the release lacks an asset hash
tmp=$(mktemp); trap 'rm -f "$tmp"' EXIT
grep -v 'windows-arm64.zip' "$SUMS" >"$tmp"
$G v0.71.1 "$tmp" >/dev/null 2>&1 && fail "accepted checksums without arm64"
$G v0.71.1-rc.1 "$SUMS" >/dev/null 2>&1 && fail "accepted an RC tag"
$G v9.9.9 "$SUMS" >/dev/null 2>&1 && fail "accepted a version with no hashes"
echo "ok gen-scoop-manifest"

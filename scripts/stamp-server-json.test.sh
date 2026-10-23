#!/usr/bin/env bash
# Tests for scripts/stamp-server-json.sh (Spec 121 T020).
set -euo pipefail
cd "$(dirname "$0")/.."
S=scripts/stamp-server-json.sh
fail() { echo "FAIL: $*" >&2; exit 1; }

out=$($S v0.71.1)
[[ "$(jq -r .version <<<"$out")" == "0.71.1" ]] || fail "version not stamped without v"
[[ "$(jq -r '.packages[0].identifier' <<<"$out")" == "ghcr.io/smart-mcp-proxy/mcpproxy-server:v0.71.1" ]] || fail "identifier tag not stamped with v: $(jq -r '.packages[0].identifier' <<<"$out")"
[[ "$(jq -r .name <<<"$out")" == "$(jq -r .name server.json)" ]] || fail "name changed"

# idempotent
tmp=$(mktemp); trap 'rm -f "$tmp"' EXIT
echo "$out" >"$tmp"
out2=$($S v0.71.1 "$tmp")
[[ "$out" == "$out2" ]] || fail "not idempotent"

# a digest-less identifier with a registry port keeps its host:port
jq '.packages[0].identifier = "localhost:5000/x/y:v0.0.1"' server.json >"$tmp"
[[ "$($S v1.2.3 "$tmp" | jq -r '.packages[0].identifier')" == "localhost:5000/x/y:v1.2.3" ]] || fail "registry port mangled"

# refuses: rc tag, bad tag, no packages
$S v1.2.3-rc.1 >/dev/null 2>&1 && fail "accepted an RC tag"
$S 1.2.3 >/dev/null 2>&1 && fail "accepted a tag without v"
jq '.packages = []' server.json >"$tmp"
$S v1.2.3 "$tmp" >/dev/null 2>&1 && fail "accepted empty packages"
echo "ok stamp-server-json"

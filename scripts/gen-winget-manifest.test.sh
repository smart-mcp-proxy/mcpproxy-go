#!/usr/bin/env bash
# Tests for scripts/gen-winget-manifest.sh (Spec 121 T030).
set -euo pipefail
cd "$(dirname "$0")/.."
G=scripts/gen-winget-manifest.sh
SUMS=scripts/testdata/checksums-v0.71.1.txt
fail() { echo "FAIL: $*" >&2; exit 1; }
out=$(mktemp -d); trap 'rm -rf "$out"' EXIT

$G v0.71.1 "$SUMS" "$out" 2>/dev/null
for f in smart-mcp-proxy.mcpproxy.yaml smart-mcp-proxy.mcpproxy.installer.yaml smart-mcp-proxy.mcpproxy.locale.en-US.yaml; do
  [[ -s "$out/$f" ]] || fail "missing $f"
done
I="$out/smart-mcp-proxy.mcpproxy.installer.yaml"
grep -q '^PackageVersion: 0.71.1$' "$I" || fail "version"
grep -q '^InstallerType: inno$' "$I" || fail "installer type"
grep -q '^  Custom: /CHANNEL=winget$' "$I" || fail "winget channel switch missing"
grep -q '/VERYSILENT /SUPPRESSMSGBOXES /NORESTART' "$I" || fail "silent switches"
grep -q 'InstallerUrl: https://github.com/smart-mcp-proxy/mcpproxy-go/releases/download/v0.71.1/mcpproxy-setup-v0.71.1-amd64.exe' "$I" || fail "amd64 url"
grep -q 'InstallerSha256: 3061B1EC1A2E298CF781C5FF48F48C0C697FB2C8E7AF402F6D956E04179533F5' "$I" || fail "amd64 sha (upper-case)"
grep -q 'InstallerSha256: 14C3030D11765AE31F668D6968B1355989A5323E141C8DA9736F65892902AA33' "$I" || fail "arm64 sha"
# architectures paired with the right exe
awk '/Architecture: x64/{a=1} a&&/InstallerUrl/{print; exit}' "$I" | grep -q 'amd64.exe' || fail "x64 paired with wrong installer"
awk '/Architecture: arm64/{a=1} a&&/InstallerUrl/{print; exit}' "$I" | grep -q 'arm64.exe' || fail "arm64 paired with wrong installer"
# every file parses as YAML and shares identifier+version
for f in "$out"/*.yaml; do
  python3 -I - "$f" <<'PY' || fail "$f is not valid YAML or lacks identity"
import sys, re
t = open(sys.argv[1]).read()
assert re.search(r'^PackageIdentifier: smart-mcp-proxy\.mcpproxy$', t, re.M)
assert re.search(r'^PackageVersion: 0\.71\.1$', t, re.M)
assert re.search(r'^ManifestVersion: 1\.9\.0$', t, re.M)
PY
done
# Schema validation against Microsoft's published schemas when check-jsonschema
# (or uvx) and the network are available; a network failure only warns.
CJ=""; command -v check-jsonschema >/dev/null 2>&1 && CJ="check-jsonschema"
[[ -z "$CJ" ]] && command -v uvx >/dev/null 2>&1 && CJ="uvx check-jsonschema"
if [[ -n "$CJ" ]]; then
  for pair in "version:smart-mcp-proxy.mcpproxy.yaml" "installer:smart-mcp-proxy.mcpproxy.installer.yaml" "defaultLocale:smart-mcp-proxy.mcpproxy.locale.en-US.yaml"; do
    kind="${pair%%:*}"; file="$out/${pair#*:}"
    if ! res=$($CJ --schemafile "https://aka.ms/winget-manifest.${kind}.1.9.0.schema.json" "$file" 2>&1); then
      if grep -qiE 'validation error|is not valid|Failed validating' <<<"$res"; then fail "winget ${kind} manifest violates the schema: $res"; fi
      echo "note: ${kind} schema check skipped (offline?)" >&2
    fi
  done
fi
rm -rf "$out"; out=$(mktemp -d)
grep -v 'setup-v0.71.1-arm64.exe' "$SUMS" >"$out/sums"
$G v0.71.1 "$out/sums" "$out/o" >/dev/null 2>&1 && fail "accepted checksums without arm64 installer"
$G v0.71.1-rc.1 "$SUMS" "$out/o" >/dev/null 2>&1 && fail "accepted an RC tag"
echo "ok gen-winget-manifest"

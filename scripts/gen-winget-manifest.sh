#!/usr/bin/env bash
# Render the winget (microsoft/winget-pkgs) manifest set for a release
# (Spec 121 FR-011/FR-013).
#
#   scripts/gen-winget-manifest.sh v0.71.1 checksums.txt OUTDIR
#
# Writes the three-file multi-manifest (version / installer / defaultLocale)
# for package smart-mcp-proxy.mcpproxy. Installer URLs and SHA-256 come from
# the release's own checksums.txt. The installer is the signed Inno Setup exe;
# winget passes /CHANNEL=winget so the install records its channel marker
# (scripts/installer.iss), and the silent switches keep it dialog-free.
set -euo pipefail

TAG="${1:-}"; SUMS="${2:-}"; OUT="${3:-}"
REPO="${REPO:-smart-mcp-proxy/mcpproxy-go}"
PKG="${PACKAGE_ID:-smart-mcp-proxy.mcpproxy}"
MANIFEST_VERSION="1.9.0"
# Inno AppId (scripts/installer.iss UpgradeCode) + "_is1" = the ARP ProductCode.
PRODUCT_CODE="{A1B2C3D4-E5F6-7890-ABCD-EF1234567890}_is1"

if [[ ! "$TAG" =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
  echo "gen-winget-manifest: tag must be a stable vX.Y.Z (got '${TAG}')" >&2; exit 2
fi
[[ -f "$SUMS" ]] || { echo "gen-winget-manifest: checksums file '${SUMS}' not found" >&2; exit 2; }
[[ -n "$OUT" ]] || { echo "usage: gen-winget-manifest.sh vX.Y.Z checksums.txt OUTDIR" >&2; exit 2; }
VER="${TAG#v}"
BASE="https://github.com/${REPO}/releases/download/${TAG}"

sha_for() { # winget wants upper-case hex
  local h
  h="$(awk -v f="$1" '$2 == f {print toupper($1); exit}' "$SUMS")"
  [[ "$h" =~ ^[A-F0-9]{64}$ ]] || { echo "gen-winget-manifest: no sha256 for $1 in $SUMS" >&2; return 1; }
  printf '%s' "$h"
}
AMD="mcpproxy-setup-${TAG}-amd64.exe"; ARM="mcpproxy-setup-${TAG}-arm64.exe"
AMD_SHA="$(sha_for "$AMD")" || exit 3
ARM_SHA="$(sha_for "$ARM")" || exit 3

mkdir -p "$OUT"
cat >"$OUT/${PKG}.yaml" <<EOF
# yaml-language-server: \$schema=https://aka.ms/winget-manifest.version.${MANIFEST_VERSION}.schema.json
PackageIdentifier: ${PKG}
PackageVersion: ${VER}
DefaultLocale: en-US
ManifestType: version
ManifestVersion: ${MANIFEST_VERSION}
EOF

cat >"$OUT/${PKG}.installer.yaml" <<EOF
# yaml-language-server: \$schema=https://aka.ms/winget-manifest.installer.${MANIFEST_VERSION}.schema.json
PackageIdentifier: ${PKG}
PackageVersion: ${VER}
InstallerLocale: en-US
MinimumOSVersion: 10.0.19044.0
InstallerType: inno
Scope: machine
InstallModes:
- interactive
- silent
- silentWithProgress
InstallerSwitches:
  Silent: /VERYSILENT /SUPPRESSMSGBOXES /NORESTART
  SilentWithProgress: /SILENT /SUPPRESSMSGBOXES /NORESTART
  Custom: /CHANNEL=winget
UpgradeBehavior: install
ProductCode: '${PRODUCT_CODE}'
ReleaseDate: $(date -u +%Y-%m-%d)
Installers:
- Architecture: x64
  InstallerUrl: ${BASE}/${AMD}
  InstallerSha256: ${AMD_SHA}
- Architecture: arm64
  InstallerUrl: ${BASE}/${ARM}
  InstallerSha256: ${ARM_SHA}
ManifestType: installer
ManifestVersion: ${MANIFEST_VERSION}
EOF

cat >"$OUT/${PKG}.locale.en-US.yaml" <<EOF
# yaml-language-server: \$schema=https://aka.ms/winget-manifest.defaultLocale.${MANIFEST_VERSION}.schema.json
PackageIdentifier: ${PKG}
PackageVersion: ${VER}
PackageLocale: en-US
Publisher: Smart MCP Proxy
PublisherUrl: https://github.com/smart-mcp-proxy
PublisherSupportUrl: https://github.com/${REPO}/issues
PackageName: MCPProxy
PackageUrl: https://mcpproxy.app
License: MIT
LicenseUrl: https://github.com/${REPO}/blob/main/LICENSE
ShortDescription: Smart MCP proxy with BM25 tool discovery, security quarantine and token savings for AI agents
Description: MCPProxy sits between your AI client and your MCP servers. It indexes every upstream tool for BM25 discovery, quarantines unreviewed servers and tool changes, and cuts tool-schema token cost.
Tags:
- ai
- mcp
- model-context-protocol
- proxy
- agents
ReleaseNotesUrl: https://github.com/${REPO}/releases/tag/${TAG}
ManifestType: defaultLocale
ManifestVersion: ${MANIFEST_VERSION}
EOF
echo "wrote winget manifests for ${PKG} ${VER} to ${OUT}" >&2

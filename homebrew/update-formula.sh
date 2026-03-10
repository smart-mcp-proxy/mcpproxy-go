#!/bin/bash
set -euo pipefail

# Update Homebrew formula and cask with new version and SHA256 hashes.
# Usage:
#   ./homebrew/update-formula.sh v0.21.0
#   ./homebrew/update-formula.sh          # auto-detect latest release

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
FORMULA="${SCRIPT_DIR}/Formula/mcpproxy.rb"
CASK="${SCRIPT_DIR}/Casks/mcpproxy.rb"
REPO="smart-mcp-proxy/mcpproxy-go"

# Determine version
if [ -n "${1:-}" ]; then
    VERSION="${1#v}"
    TAG="v${VERSION}"
else
    echo "Auto-detecting latest release..."
    TAG=$(gh release view --repo "${REPO}" --json tagName -q '.tagName')
    VERSION="${TAG#v}"
fi

echo "Updating Homebrew files to version ${VERSION} (tag ${TAG})"

# --- Source tarball (formula) ---
SOURCE_URL="https://github.com/${REPO}/archive/refs/tags/${TAG}.tar.gz"
echo "Downloading source tarball..."
SOURCE_SHA256=$(curl -sL "${SOURCE_URL}" | shasum -a 256 | awk '{print $1}')
echo "  Source SHA256: ${SOURCE_SHA256}"

# --- DMG assets (cask) ---
ARM64_DMG_URL="https://github.com/${REPO}/releases/download/${TAG}/mcpproxy-${VERSION}-darwin-arm64-installer.dmg"
AMD64_DMG_URL="https://github.com/${REPO}/releases/download/${TAG}/mcpproxy-${VERSION}-darwin-amd64-installer.dmg"

echo "Downloading arm64 DMG..."
ARM64_SHA256=$(curl -sL "${ARM64_DMG_URL}" | shasum -a 256 | awk '{print $1}')
echo "  arm64 DMG SHA256: ${ARM64_SHA256}"

echo "Downloading amd64 DMG..."
AMD64_SHA256=$(curl -sL "${AMD64_DMG_URL}" | shasum -a 256 | awk '{print $1}')
echo "  amd64 DMG SHA256: ${AMD64_SHA256}"

# --- Update formula ---
echo "Updating formula..."

# Update URL
sed -i '' "s|url \"https://github.com/${REPO}/archive/refs/tags/v[^\"]*\.tar\.gz\"|url \"https://github.com/${REPO}/archive/refs/tags/${TAG}.tar.gz\"|" "${FORMULA}"

# Update SHA256
sed -i '' "/^  url.*archive\/refs\/tags/{ n; s/sha256 \"[a-f0-9]*\"/sha256 \"${SOURCE_SHA256}\"/; }" "${FORMULA}"

echo "  Formula updated."

# --- Update cask ---
echo "Updating cask..."

# Update version
sed -i '' "s/version \"[^\"]*\"/version \"${VERSION}\"/" "${CASK}"

# Update arm64 SHA256 (first sha256 in the file, inside on_arm block)
# Use awk for precise block-aware replacement
awk -v arm_sha="${ARM64_SHA256}" -v amd_sha="${AMD64_SHA256}" '
  /on_arm do/ { in_arm=1 }
  /on_intel do/ { in_arm=0; in_intel=1 }
  /^  end$/ { in_arm=0; in_intel=0 }
  in_arm && /sha256/ { sub(/sha256 "[a-f0-9]*"/, "sha256 \"" arm_sha "\"") }
  in_intel && /sha256/ { sub(/sha256 "[a-f0-9]*"/, "sha256 \"" amd_sha "\"") }
  { print }
' "${CASK}" > "${CASK}.tmp" && mv "${CASK}.tmp" "${CASK}"

echo "  Cask updated."

# --- Summary ---
echo ""
echo "=== Update Summary ==="
echo "Version:          ${VERSION}"
echo "Source SHA256:     ${SOURCE_SHA256}"
echo "arm64 DMG SHA256: ${ARM64_SHA256}"
echo "amd64 DMG SHA256: ${AMD64_SHA256}"
echo ""
echo "Files updated:"
echo "  ${FORMULA}"
echo "  ${CASK}"
echo ""
echo "Next steps:"
echo "  1. Review changes: git diff homebrew/"
echo "  2. Test formula:   brew install --build-from-source homebrew/Formula/mcpproxy.rb"
echo "  3. Test cask:      brew install --cask homebrew/Casks/mcpproxy.rb"
echo "  4. Commit:         git add homebrew/ && git commit -m 'homebrew: update to ${VERSION}'"

#!/bin/bash
set -euo pipefail

# =============================================================================
# build-macos-tray.sh — Build MCPProxy macOS Swift tray app + Go core binary
#
# Creates a signed, notarized .app bundle inside a DMG installer.
#
# Usage (run from repo root):
#   ./native/macos/MCPProxy/scripts/build-macos-tray.sh --version v0.22.0
#   ./native/macos/MCPProxy/scripts/build-macos-tray.sh --version v0.22.0 --arch arm64
#   ./native/macos/MCPProxy/scripts/build-macos-tray.sh --version v0.22.0 --skip-notarize
#
# Requirements:
#   - macOS with Xcode 15+ / Command Line Tools
#   - Go 1.24+
#   - Swift 5.9+
#   - Developer ID Application certificate in keychain (for signing)
#   - Apple notarytool credentials in keychain profile "AC_PASSWORD" (for notarization)
#
# Make executable: chmod +x native/macos/MCPProxy/scripts/build-macos-tray.sh
# =============================================================================

# ---- Constants ----
readonly SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
readonly SWIFT_PROJECT_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
readonly REPO_ROOT="$(cd "$SCRIPT_DIR/../../../.." && pwd)"
readonly APP_NAME="MCPProxy"
readonly BUNDLE_ID="com.smartmcpproxy.mcpproxy"
readonly DMG_VOLNAME="MCPProxy"
readonly MIN_MACOS="13.0"
readonly ENTITLEMENTS_APP="${SWIFT_PROJECT_DIR}/MCPProxy/MCPProxy.entitlements"
readonly ENTITLEMENTS_CORE="${REPO_ROOT}/scripts/entitlements.plist"
readonly INFO_PLIST_SRC="${SWIFT_PROJECT_DIR}/MCPProxy/Info.plist"

# ---- Defaults ----
VERSION=""
ARCH=""
SKIP_NOTARIZE=0

# ---- Temp directory with cleanup ----
WORK_DIR=""
cleanup() {
    if [ -n "$WORK_DIR" ] && [ -d "$WORK_DIR" ]; then
        echo "[cleanup] Removing temporary directory: $WORK_DIR"
        rm -rf "$WORK_DIR"
    fi
}
trap cleanup EXIT

# =============================================================================
# Argument parsing
# =============================================================================
usage() {
    cat <<EOF
Usage: $0 --version <version> [--arch <arch>] [--skip-notarize]

Options:
  --version        Required. Semantic version with v prefix (e.g., v0.22.0)
  --arch           Optional. Target architecture: arm64, amd64, or universal (default: universal)
  --skip-notarize  Optional. Skip notarization (for local dev builds)
  -h, --help       Show this help message
EOF
    exit 1
}

while [[ $# -gt 0 ]]; do
    case "$1" in
        --version)
            VERSION="$2"
            shift 2
            ;;
        --arch)
            ARCH="$2"
            shift 2
            ;;
        --skip-notarize)
            SKIP_NOTARIZE=1
            shift
            ;;
        -h|--help)
            usage
            ;;
        *)
            echo "Error: Unknown argument: $1"
            usage
            ;;
    esac
done

if [ -z "$VERSION" ]; then
    echo "Error: --version is required"
    usage
fi

# Validate version format
if ! [[ "$VERSION" =~ ^v[0-9]+\.[0-9]+\.[0-9]+(-[a-zA-Z0-9.]+)?$ ]]; then
    echo "Error: Version must match vX.Y.Z[-prerelease] (e.g., v0.22.0, v1.0.0-beta.1)"
    exit 1
fi

# Strip leading 'v' for numeric version
VERSION_NUM="${VERSION#v}"

# Default to universal if no arch specified
if [ -z "$ARCH" ]; then
    ARCH="universal"
fi

# Validate arch
case "$ARCH" in
    arm64|amd64|universal) ;;
    *)
        echo "Error: --arch must be arm64, amd64, or universal"
        exit 1
        ;;
esac

# =============================================================================
# Environment checks
# =============================================================================
echo "==========================================="
echo "  MCPProxy macOS Tray App Builder"
echo "==========================================="
echo "  Version:       $VERSION ($VERSION_NUM)"
echo "  Architecture:  $ARCH"
echo "  Notarize:      $([ "$SKIP_NOTARIZE" -eq 1 ] && echo "SKIP" || echo "YES")"
echo "  Repo root:     $REPO_ROOT"
echo "  Swift project: $SWIFT_PROJECT_DIR"
echo "==========================================="
echo ""

# Verify we are on macOS
if [[ "$(uname -s)" != "Darwin" ]]; then
    echo "Error: This script must be run on macOS"
    exit 1
fi

# Verify toolchain
echo "[check] Verifying build tools..."

if ! command -v go &>/dev/null; then
    echo "Error: Go is not installed or not in PATH"
    exit 1
fi
echo "  Go:     $(go version)"

if ! command -v swift &>/dev/null; then
    echo "Error: Swift is not installed or not in PATH"
    exit 1
fi
echo "  Swift:  $(swift --version 2>&1 | head -1)"

if ! command -v codesign &>/dev/null; then
    echo "Error: codesign not found (Xcode Command Line Tools required)"
    exit 1
fi

if ! command -v hdiutil &>/dev/null; then
    echo "Error: hdiutil not found"
    exit 1
fi

echo ""

# =============================================================================
# Prepare working directory
# =============================================================================
WORK_DIR="$(mktemp -d "${TMPDIR:-/tmp}/mcpproxy-build.XXXXXXXX")"
echo "[prep] Working directory: $WORK_DIR"

DIST_DIR="${REPO_ROOT}/dist"
mkdir -p "$DIST_DIR"

# Determine git metadata for ldflags
COMMIT="$(cd "$REPO_ROOT" && git rev-parse --short HEAD 2>/dev/null || echo "unknown")"
BUILD_DATE="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
LDFLAGS="-X main.version=$VERSION -X main.commit=$COMMIT -X main.date=$BUILD_DATE -X github.com/smart-mcp-proxy/mcpproxy-go/internal/httpapi.buildVersion=$VERSION -s -w"

# =============================================================================
# Step 1: Build Go core binary
# =============================================================================
echo ""
echo "==========================================="
echo "  Step 1: Building Go core binary"
echo "==========================================="

GO_BIN_DIR="${WORK_DIR}/go-bin"
mkdir -p "$GO_BIN_DIR"

build_go_binary() {
    local goarch="$1"
    local output="${GO_BIN_DIR}/mcpproxy-${goarch}"
    echo "[go] Building mcpproxy for darwin/${goarch}..."
    (
        cd "$REPO_ROOT"
        CGO_ENABLED=0 GOOS=darwin GOARCH="$goarch" \
            go build -ldflags "$LDFLAGS" -o "$output" ./cmd/mcpproxy
    )
    echo "[go] Built: $output ($(du -h "$output" | cut -f1))"
}

CORE_BINARY="${GO_BIN_DIR}/mcpproxy"

if [ "$ARCH" = "universal" ]; then
    build_go_binary "arm64"
    build_go_binary "amd64"
    echo "[go] Creating universal binary with lipo..."
    lipo -create \
        "${GO_BIN_DIR}/mcpproxy-arm64" \
        "${GO_BIN_DIR}/mcpproxy-amd64" \
        -output "$CORE_BINARY"
    echo "[go] Universal binary: $CORE_BINARY ($(du -h "$CORE_BINARY" | cut -f1))"
    lipo -info "$CORE_BINARY"
elif [ "$ARCH" = "arm64" ]; then
    build_go_binary "arm64"
    cp "${GO_BIN_DIR}/mcpproxy-arm64" "$CORE_BINARY"
else
    build_go_binary "amd64"
    cp "${GO_BIN_DIR}/mcpproxy-amd64" "$CORE_BINARY"
fi

chmod +x "$CORE_BINARY"

# =============================================================================
# Step 2: Build Swift tray app
# =============================================================================
echo ""
echo "==========================================="
echo "  Step 2: Building Swift tray app"
echo "==========================================="

SWIFT_BUILD_DIR="${WORK_DIR}/swift-build"
SWIFT_BINARY=""

# Determine swift build architecture flags
SWIFT_ARCH_FLAGS=""
case "$ARCH" in
    universal)
        SWIFT_ARCH_FLAGS="--arch arm64 --arch x86_64"
        ;;
    arm64)
        SWIFT_ARCH_FLAGS="--arch arm64"
        ;;
    amd64)
        SWIFT_ARCH_FLAGS="--arch x86_64"
        ;;
esac

# Check if there is an .xcodeproj — prefer xcodebuild if so
XCODEPROJ="$(find "$SWIFT_PROJECT_DIR" -maxdepth 1 -name "*.xcodeproj" -print -quit 2>/dev/null || true)"

if [ -n "$XCODEPROJ" ] && [ -d "$XCODEPROJ" ]; then
    echo "[swift] Found Xcode project: $XCODEPROJ"
    echo "[swift] Building with xcodebuild..."

    XCODE_DEST=""
    case "$ARCH" in
        arm64)    XCODE_DEST="generic/platform=macOS,arch=arm64" ;;
        amd64)    XCODE_DEST="generic/platform=macOS,arch=x86_64" ;;
        universal) XCODE_DEST="generic/platform=macOS" ;;
    esac

    xcodebuild \
        -project "$XCODEPROJ" \
        -scheme "$APP_NAME" \
        -configuration Release \
        -destination "$XCODE_DEST" \
        -derivedDataPath "$SWIFT_BUILD_DIR" \
        MARKETING_VERSION="$VERSION_NUM" \
        CURRENT_PROJECT_VERSION="$VERSION_NUM" \
        ONLY_ACTIVE_ARCH=NO \
        clean build

    # Find the built binary
    SWIFT_BINARY="$(find "$SWIFT_BUILD_DIR" -name "$APP_NAME" -type f -perm +111 ! -name "*.dSYM" | head -1)"
else
    echo "[swift] No .xcodeproj found, building with Swift Package Manager..."
    echo "[swift] Architecture flags: $SWIFT_ARCH_FLAGS"

    (
        cd "$SWIFT_PROJECT_DIR"
        # shellcheck disable=SC2086
        swift build \
            -c release \
            $SWIFT_ARCH_FLAGS \
            --build-path "$SWIFT_BUILD_DIR"
    )

    # Locate the built binary — SPM places it under .build/release/ or .build/apple/Products/Release/
    SWIFT_BINARY="$(find "$SWIFT_BUILD_DIR" -name "$APP_NAME" -type f -perm +111 2>/dev/null | grep -v ".build/repositories" | grep -v ".dSYM" | head -1)"
fi

if [ -z "$SWIFT_BINARY" ] || [ ! -f "$SWIFT_BINARY" ]; then
    echo "Error: Swift build succeeded but could not locate the $APP_NAME binary"
    echo "Contents of build directory:"
    find "$SWIFT_BUILD_DIR" -type f -name "$APP_NAME*" 2>/dev/null || true
    exit 1
fi

echo "[swift] Built: $SWIFT_BINARY ($(du -h "$SWIFT_BINARY" | cut -f1))"
file "$SWIFT_BINARY"

# =============================================================================
# Step 3: Assemble .app bundle
# =============================================================================
echo ""
echo "==========================================="
echo "  Step 3: Assembling .app bundle"
echo "==========================================="

BUNDLE_DIR="${WORK_DIR}/bundle"
APP_BUNDLE="${BUNDLE_DIR}/${APP_NAME}.app"

mkdir -p "${APP_BUNDLE}/Contents/MacOS"
mkdir -p "${APP_BUNDLE}/Contents/Resources/bin"
mkdir -p "${APP_BUNDLE}/Contents/Frameworks"

# 3a. Copy Swift tray binary as main executable
echo "[bundle] Copying Swift binary -> Contents/MacOS/${APP_NAME}"
cp "$SWIFT_BINARY" "${APP_BUNDLE}/Contents/MacOS/${APP_NAME}"
chmod +x "${APP_BUNDLE}/Contents/MacOS/${APP_NAME}"

# 3b. Embed Go core binary
echo "[bundle] Copying Go core binary -> Contents/Resources/bin/mcpproxy"
cp "$CORE_BINARY" "${APP_BUNDLE}/Contents/Resources/bin/mcpproxy"
chmod +x "${APP_BUNDLE}/Contents/Resources/bin/mcpproxy"

# 3c. Copy Sparkle framework if present in swift build output
SPARKLE_FW="$(find "$SWIFT_BUILD_DIR" -name "Sparkle.framework" -type d 2>/dev/null | head -1)"
if [ -n "$SPARKLE_FW" ] && [ -d "$SPARKLE_FW" ]; then
    echo "[bundle] Copying Sparkle.framework -> Contents/Frameworks/"
    cp -R "$SPARKLE_FW" "${APP_BUNDLE}/Contents/Frameworks/"
fi

# 3d. Generate Info.plist with version injected
echo "[bundle] Generating Info.plist (version: $VERSION_NUM)"
if [ -f "$INFO_PLIST_SRC" ]; then
    cp "$INFO_PLIST_SRC" "${APP_BUNDLE}/Contents/Info.plist"
    # Inject version numbers using PlistBuddy
    /usr/libexec/PlistBuddy -c "Set :CFBundleVersion $VERSION_NUM" "${APP_BUNDLE}/Contents/Info.plist"
    /usr/libexec/PlistBuddy -c "Set :CFBundleShortVersionString $VERSION_NUM" "${APP_BUNDLE}/Contents/Info.plist"
    echo "[bundle] Version injected into Info.plist from source"
else
    echo "[bundle] Source Info.plist not found at $INFO_PLIST_SRC, generating from scratch"
    cat > "${APP_BUNDLE}/Contents/Info.plist" <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>CFBundleExecutable</key>
    <string>${APP_NAME}</string>
    <key>CFBundleIdentifier</key>
    <string>${BUNDLE_ID}</string>
    <key>CFBundleName</key>
    <string>MCPProxy</string>
    <key>CFBundleDisplayName</key>
    <string>Smart MCP Proxy</string>
    <key>CFBundleVersion</key>
    <string>${VERSION_NUM}</string>
    <key>CFBundleShortVersionString</key>
    <string>${VERSION_NUM}</string>
    <key>CFBundlePackageType</key>
    <string>APPL</string>
    <key>CFBundleSignature</key>
    <string>MCPP</string>
    <key>LSMinimumSystemVersion</key>
    <string>${MIN_MACOS}</string>
    <key>LSUIElement</key>
    <true/>
    <key>LSBackgroundOnly</key>
    <false/>
    <key>NSHighResolutionCapable</key>
    <true/>
    <key>NSRequiresAquaSystemAppearance</key>
    <false/>
    <key>LSApplicationCategoryType</key>
    <string>public.app-category.utilities</string>
    <key>SUFeedURL</key>
    <string>https://mcpproxy.app/appcast.xml</string>
    <key>SUEnableAutomaticChecks</key>
    <true/>
    <key>SUScheduledCheckInterval</key>
    <integer>14400</integer>
</dict>
</plist>
PLIST
fi

# 3e. PkgInfo
echo "APPLMCPP" > "${APP_BUNDLE}/Contents/PkgInfo"

# 3f. Copy icon assets if available
if [ -f "${REPO_ROOT}/assets/mcpproxy.icns" ]; then
    cp "${REPO_ROOT}/assets/mcpproxy.icns" "${APP_BUNDLE}/Contents/Resources/"
    echo "[bundle] Copied app icon: mcpproxy.icns"
fi

# 3g. Copy asset catalog resources (built by Swift, if present)
SWIFT_RESOURCES="$(find "$SWIFT_BUILD_DIR" -name "Assets.car" -type f 2>/dev/null | head -1)"
if [ -n "$SWIFT_RESOURCES" ] && [ -f "$SWIFT_RESOURCES" ]; then
    cp "$SWIFT_RESOURCES" "${APP_BUNDLE}/Contents/Resources/"
    echo "[bundle] Copied compiled asset catalog"
fi

echo "[bundle] App bundle assembled at: $APP_BUNDLE"
echo "[bundle] Contents:"
find "$APP_BUNDLE" -type f | sort | while read -r f; do
    echo "  $(echo "$f" | sed "s|$APP_BUNDLE/||")"
done

# =============================================================================
# Step 4: Code signing
# =============================================================================
echo ""
echo "==========================================="
echo "  Step 4: Code signing"
echo "==========================================="

# Find Developer ID Application certificate
CERT_IDENTITY="$(security find-identity -v -p codesigning 2>/dev/null | grep "Developer ID Application" | head -1 | grep -o '"[^"]*"' | tr -d '"' || true)"

# Choose entitlements file — prefer the app-specific one, fall back to repo-level
ENTITLEMENTS_FILE=""
if [ -f "$ENTITLEMENTS_APP" ]; then
    ENTITLEMENTS_FILE="$ENTITLEMENTS_APP"
    echo "[sign] Using app entitlements: $ENTITLEMENTS_FILE"
elif [ -f "$ENTITLEMENTS_CORE" ]; then
    ENTITLEMENTS_FILE="$ENTITLEMENTS_CORE"
    echo "[sign] Using repo entitlements: $ENTITLEMENTS_FILE"
else
    echo "[sign] Warning: No entitlements file found"
fi

# Validate entitlements format
if [ -n "$ENTITLEMENTS_FILE" ]; then
    if plutil -lint "$ENTITLEMENTS_FILE" >/dev/null 2>&1; then
        echo "[sign] Entitlements file validated OK"
    else
        echo "Error: Entitlements file has formatting issues: $ENTITLEMENTS_FILE"
        exit 1
    fi
fi

sign_binary() {
    local binary="$1"
    local identifier="$2"
    local entitlements="${3:-}"
    local label="$4"

    local sign_args=(
        --force
        --options runtime
        --sign "${CERT_IDENTITY}"
        --identifier "$identifier"
        --timestamp
    )
    if [ -n "$entitlements" ]; then
        sign_args+=(--entitlements "$entitlements")
    fi
    sign_args+=("$binary")

    echo "[sign] Signing $label..."
    codesign "${sign_args[@]}"
}

if [ -n "$CERT_IDENTITY" ]; then
    echo "[sign] Found Developer ID certificate: $CERT_IDENTITY"

    # Sign inside-out: embedded binaries first, then frameworks, then the bundle

    # 4a. Sign the embedded Go core binary
    sign_binary \
        "${APP_BUNDLE}/Contents/Resources/bin/mcpproxy" \
        "${BUNDLE_ID}.core" \
        "$ENTITLEMENTS_FILE" \
        "Go core binary"

    # 4b. Sign Sparkle framework if present
    if [ -d "${APP_BUNDLE}/Contents/Frameworks/Sparkle.framework" ]; then
        echo "[sign] Signing Sparkle.framework..."
        codesign --force --options runtime \
            --sign "$CERT_IDENTITY" \
            --timestamp \
            "${APP_BUNDLE}/Contents/Frameworks/Sparkle.framework"
    fi

    # 4c. Sign the main app bundle (covers the Swift executable)
    sign_binary \
        "$APP_BUNDLE" \
        "$BUNDLE_ID" \
        "$ENTITLEMENTS_FILE" \
        "app bundle"

    # 4d. Verify signature
    echo ""
    echo "[sign] Verifying signature..."
    codesign --verify --verbose "$APP_BUNDLE"

    echo "[sign] Strict verification (notarization requirements)..."
    if codesign -vvv --deep --strict "$APP_BUNDLE"; then
        echo "[sign] PASSED strict verification"
    else
        echo "Error: App bundle failed strict signature verification"
        exit 1
    fi

    # Check secure timestamp
    TIMESTAMP_INFO="$(codesign -dvv "$APP_BUNDLE" 2>&1)"
    if echo "$TIMESTAMP_INFO" | grep -q "Timestamp="; then
        echo "[sign] Secure timestamp present: $(echo "$TIMESTAMP_INFO" | grep "Timestamp=")"
    else
        echo "[sign] Warning: No secure timestamp found"
    fi
else
    echo "[sign] WARNING: No Developer ID certificate found in keychain"
    echo "[sign] Using ad-hoc signature (will NOT pass notarization)"
    codesign --force --deep --sign - --identifier "$BUNDLE_ID" "$APP_BUNDLE"
fi

# =============================================================================
# Step 5: Create DMG
# =============================================================================
echo ""
echo "==========================================="
echo "  Step 5: Creating DMG installer"
echo "==========================================="

# Determine DMG filename
DMG_ARCH="$ARCH"
if [ "$ARCH" = "amd64" ]; then
    DMG_ARCH="x86_64"
fi
DMG_NAME="mcpproxy-${VERSION_NUM}-darwin-${DMG_ARCH}"
DMG_PATH="${DIST_DIR}/${DMG_NAME}.dmg"

# Remove any previous DMG
rm -f "$DMG_PATH"

# Prepare DMG staging area
DMG_STAGING="${WORK_DIR}/dmg-staging"
mkdir -p "$DMG_STAGING"

# Copy .app bundle
cp -R "$APP_BUNDLE" "$DMG_STAGING/"

# Create Applications symlink for drag-and-drop install
ln -s /Applications "$DMG_STAGING/Applications"

# Include release notes if available
for notes_file in "${REPO_ROOT}/RELEASE_NOTES-${VERSION}.md" "${REPO_ROOT}/RELEASE_NOTES.md"; do
    if [ -f "$notes_file" ]; then
        cp "$notes_file" "$DMG_STAGING/RELEASE_NOTES.md"
        echo "[dmg] Included release notes: $notes_file"
        break
    fi
done

# Create initial DMG
echo "[dmg] Creating DMG: $DMG_NAME.dmg"
hdiutil create \
    -size 200m \
    -fs HFS+ \
    -volname "${DMG_VOLNAME} ${VERSION_NUM}" \
    -srcfolder "$DMG_STAGING" \
    "${WORK_DIR}/${DMG_NAME}-raw.dmg"

# Compress DMG
echo "[dmg] Compressing DMG..."
hdiutil convert \
    "${WORK_DIR}/${DMG_NAME}-raw.dmg" \
    -format UDZO \
    -o "$DMG_PATH"

echo "[dmg] DMG created: $DMG_PATH ($(du -h "$DMG_PATH" | cut -f1))"

# Sign the DMG itself if we have a certificate
if [ -n "$CERT_IDENTITY" ]; then
    echo "[dmg] Signing DMG..."
    codesign --force --sign "$CERT_IDENTITY" --timestamp "$DMG_PATH"
    echo "[dmg] DMG signed"
fi

# =============================================================================
# Step 6: Notarization (optional)
# =============================================================================
if [ "$SKIP_NOTARIZE" -eq 0 ] && [ -n "$CERT_IDENTITY" ]; then
    echo ""
    echo "==========================================="
    echo "  Step 6: Notarizing with Apple"
    echo "==========================================="

    # Apple notarytool requires credentials stored in keychain profile.
    # Set up with: xcrun notarytool store-credentials "AC_PASSWORD" ...
    NOTARY_PROFILE="${NOTARY_PROFILE:-AC_PASSWORD}"

    echo "[notarize] Submitting DMG to Apple notary service..."
    echo "[notarize] Using keychain profile: $NOTARY_PROFILE"

    NOTARIZE_OUTPUT="$(xcrun notarytool submit "$DMG_PATH" \
        --keychain-profile "$NOTARY_PROFILE" \
        --wait \
        2>&1)" || {
        echo "Error: Notarization failed"
        echo "$NOTARIZE_OUTPUT"
        echo ""
        echo "Hint: Set up credentials with:"
        echo "  xcrun notarytool store-credentials \"AC_PASSWORD\" \\"
        echo "    --apple-id your@email.com \\"
        echo "    --team-id YOUR_TEAM_ID \\"
        echo "    --password app-specific-password"
        exit 1
    }

    echo "$NOTARIZE_OUTPUT"

    # Check for success
    if echo "$NOTARIZE_OUTPUT" | grep -q "status: Accepted"; then
        echo "[notarize] Notarization ACCEPTED"

        # Staple the notarization ticket to the DMG
        echo "[notarize] Stapling ticket to DMG..."
        xcrun stapler staple "$DMG_PATH"
        echo "[notarize] Stapled successfully"
    else
        echo "Error: Notarization was not accepted"
        # Extract submission ID for log retrieval
        SUBMISSION_ID="$(echo "$NOTARIZE_OUTPUT" | grep -o 'id: [a-f0-9-]*' | head -1 | awk '{print $2}')"
        if [ -n "$SUBMISSION_ID" ]; then
            echo "[notarize] Fetching notarization log..."
            xcrun notarytool log "$SUBMISSION_ID" \
                --keychain-profile "$NOTARY_PROFILE" \
                "${WORK_DIR}/notarize-log.json" 2>/dev/null || true
            if [ -f "${WORK_DIR}/notarize-log.json" ]; then
                cat "${WORK_DIR}/notarize-log.json"
            fi
        fi
        exit 1
    fi
else
    if [ "$SKIP_NOTARIZE" -eq 1 ]; then
        echo ""
        echo "[notarize] Skipped (--skip-notarize flag set)"
    elif [ -z "$CERT_IDENTITY" ]; then
        echo ""
        echo "[notarize] Skipped (no Developer ID certificate — ad-hoc builds cannot be notarized)"
    fi
fi

# =============================================================================
# Step 7: Post-install symlink instructions
# =============================================================================
echo ""
echo "==========================================="
echo "  Build Complete"
echo "==========================================="
echo ""
echo "  DMG:     $DMG_PATH"
echo "  Size:    $(du -h "$DMG_PATH" | cut -f1)"
echo "  Version: $VERSION"
echo "  Arch:    $ARCH"
echo ""
echo "Post-install: The tray app creates a symlink on first launch so the"
echo "'mcpproxy' CLI is available system-wide. The SymlinkService checks:"
echo ""
echo "  /usr/local/bin/mcpproxy -> <app>/Contents/Resources/bin/mcpproxy"
echo ""
echo "If the symlink is missing or stale, the app will prompt the user to"
echo "create it (requires admin privileges for /usr/local/bin)."
echo ""
echo "To manually create the symlink after installing:"
echo "  sudo ln -sf /Applications/MCPProxy.app/Contents/Resources/bin/mcpproxy /usr/local/bin/mcpproxy"
echo ""
echo "Done."

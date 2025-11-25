#!/bin/bash
set -e

# Script to create macOS DMG containing app bundle (for PR/development builds)
APP_BUNDLE_PATH="$1"
VERSION="$2"
ARCH="$3"

if [ -z "$APP_BUNDLE_PATH" ] || [ -z "$VERSION" ] || [ -z "$ARCH" ]; then
    echo "Usage: $0 <app_bundle_path> <version> <arch>"
    echo "Example: $0 ./pkg_temp/pkg_root/Applications/mcpproxy.app v1.0.0 arm64"
    exit 1
fi

if [ ! -d "$APP_BUNDLE_PATH" ]; then
    echo "App bundle not found: $APP_BUNDLE_PATH"
    exit 1
fi

# Variables
APP_NAME=$(basename "$APP_BUNDLE_PATH" .app)
DMG_NAME="mcpproxy-${VERSION#v}-darwin-${ARCH}-installer"
TEMP_DIR="dmg_app_temp"

echo "Creating app DMG for ${APP_NAME} ${VERSION} (${ARCH})"

# Clean up previous builds
rm -rf "$TEMP_DIR"
rm -f "${DMG_NAME}.dmg"

# Create temporary directory
mkdir -p "$TEMP_DIR"

# Copy app bundle to temp directory
echo "Copying app bundle: $APP_BUNDLE_PATH"
cp -R "$APP_BUNDLE_PATH" "$TEMP_DIR/"

# Create README file
cat > "$TEMP_DIR/README.txt" << EOF
Smart MCP Proxy ${VERSION#v}

DEVELOPMENT BUILD - For testing purposes

INSTALLATION:
1. Drag mcpproxy.app to your Applications folder
2. Right-click and select "Open" on first launch (bypass Gatekeeper)
3. The app will appear in your system tray

CLI USAGE:
The CLI binary is embedded in the app bundle. To use it:
- Right-click mcpproxy.app → Show Package Contents
- Navigate to Contents/Resources/bin/
- Run ./mcpproxy from Terminal

Or add to PATH:
export PATH="/Applications/mcpproxy.app/Contents/Resources/bin:\$PATH"

FEATURES:
• Intelligent MCP server proxy with tool discovery
• System tray application for easy management
• Built-in security quarantine for new servers
• HTTP by default (no certificate setup needed)

GETTING STARTED:
• Launch mcpproxy.app from Applications
• Configure MCP servers via the tray menu
• Or use the CLI: mcpproxy --help

NOTE: This is an ad-hoc signed development build.
Production builds are fully signed and notarized.

Visit https://github.com/smart-mcp-proxy/mcpproxy-go for documentation.
EOF

# Create a simple Applications symlink for drag-and-drop installation
ln -s /Applications "$TEMP_DIR/Applications"

# Sync filesystem to ensure all writes complete
sync

# Wait a moment for macOS to finish any background operations (Spotlight indexing, etc.)
sleep 2

# Create DMG using hdiutil
echo "Creating DMG..."
hdiutil create -size 100m -fs HFS+ -volname "Smart MCP Proxy ${VERSION#v}" -srcfolder "$TEMP_DIR" "${DMG_NAME}.dmg"

# Clean up
rm -rf "$TEMP_DIR"

echo "DMG created: ${DMG_NAME}.dmg"

# Make DMG read-only and compressed
echo "Compressing DMG..."
hdiutil convert "${DMG_NAME}.dmg" -format UDZO -o "${DMG_NAME}-compressed.dmg"
mv "${DMG_NAME}-compressed.dmg" "${DMG_NAME}.dmg"

# Sign DMG (ad-hoc for development)
echo "Signing DMG..."

# Use certificate identity from environment if available
if [ -n "${APP_CERT_IDENTITY}" ] && [ "${APP_CERT_IDENTITY}" != "" ]; then
    CERT_IDENTITY="${APP_CERT_IDENTITY}"
    echo "✅ Using provided certificate for DMG: ${CERT_IDENTITY}"
else
    # Fallback to ad-hoc signing
    CERT_IDENTITY="-"
    echo "Using ad-hoc signature for DMG (development build)"
fi

# Sign DMG
codesign --force \
    --sign "${CERT_IDENTITY}" \
    "${DMG_NAME}.dmg"

# Verify DMG signing
echo "=== Verifying DMG signature ==="
codesign --verify --verbose "${DMG_NAME}.dmg" || echo "⚠️  Ad-hoc signed (expected for development)"

echo "✅ DMG created successfully: ${DMG_NAME}.dmg"

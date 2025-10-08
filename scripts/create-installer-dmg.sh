#!/bin/bash
set -e

# Script to create macOS DMG containing PKG installer
PKG_PATH="$1"
VERSION="$2"
ARCH="$3"

if [ -z "$PKG_PATH" ] || [ -z "$VERSION" ] || [ -z "$ARCH" ]; then
    echo "Usage: $0 <pkg_path> <version> <arch>"
    echo "Example: $0 ./mcpproxy-v1.0.0-darwin-arm64.pkg v1.0.0 arm64"
    exit 1
fi

if [ ! -f "$PKG_PATH" ]; then
    echo "PKG file not found: $PKG_PATH"
    exit 1
fi

# Variables
APP_NAME="mcpproxy"
DMG_NAME="mcpproxy-${VERSION#v}-darwin-${ARCH}-installer"
TEMP_DIR="dmg_installer_temp"

echo "Creating installer DMG for ${APP_NAME} ${VERSION} (${ARCH})"

# Clean up previous builds
rm -rf "$TEMP_DIR"
rm -f "${DMG_NAME}.dmg"

# Create temporary directory
mkdir -p "$TEMP_DIR"

# Copy PKG to temp directory
cp "$PKG_PATH" "$TEMP_DIR/"
PKG_FILENAME=$(basename "$PKG_PATH")

# Create README file
cat > "$TEMP_DIR/README.txt" << EOF
Smart MCP Proxy ${VERSION#v} Installer

Welcome to Smart MCP Proxy!

INSTALLATION:
1. Double-click the ${PKG_FILENAME} file to start installation
2. Follow the installer instructions
3. The app will be installed to your Applications folder
4. CLI tool 'mcpproxy' will be available in Terminal

FEATURES:
• Intelligent MCP server proxy with tool discovery
• System tray application for easy management
• Built-in security quarantine for new servers
• HTTP by default, optional HTTPS with certificate trust

GETTING STARTED:
• Open mcpproxy from Applications folder
• Or run 'mcpproxy --help' in Terminal
• Default mode: HTTP (works immediately)
• For HTTPS: run 'mcpproxy trust-cert' first

OPTIONAL HTTPS SETUP:
1. Trust certificate: mcpproxy trust-cert
2. Enable HTTPS: export MCPPROXY_TLS_ENABLED=true
3. Start server: mcpproxy serve

For Claude Desktop with HTTPS, add to config:
  "env": {
    "NODE_EXTRA_CA_CERTS": "~/.mcpproxy/certs/ca.pem"
  }

Visit https://github.com/smart-mcp-proxy/mcpproxy-go for documentation.

Happy proxying! 🚀
EOF

# Create background image directory (optional)
mkdir -p "$TEMP_DIR/.background"

# Copy or create a simple background if you want one
# For now, we'll skip the background image

# Create DMG using hdiutil
echo "Creating DMG..."
hdiutil create -size 100m -fs HFS+ -volname "Smart MCP Proxy ${VERSION#v} Installer" -srcfolder "$TEMP_DIR" "${DMG_NAME}.dmg"

# Clean up
rm -rf "$TEMP_DIR"

echo "DMG created: ${DMG_NAME}.dmg"

# Make DMG read-only and compressed
echo "Compressing DMG..."
hdiutil convert "${DMG_NAME}.dmg" -format UDZO -o "${DMG_NAME}-compressed.dmg"
mv "${DMG_NAME}-compressed.dmg" "${DMG_NAME}.dmg"

# Sign DMG
echo "Signing DMG..."

# Use certificate identity passed from GitHub workflow environment
if [ -n "${APP_CERT_IDENTITY}" ]; then
    CERT_IDENTITY="${APP_CERT_IDENTITY}"
    echo "✅ Using provided Developer ID Application certificate for DMG: ${CERT_IDENTITY}"
else
    # Fallback: Find the Developer ID certificate locally
    CERT_IDENTITY=$(security find-identity -v -p codesigning | grep "Developer ID Application" | head -1 | grep -o '"[^"]*"' | tr -d '"')
    if [ -n "${CERT_IDENTITY}" ]; then
        echo "✅ Found Developer ID certificate locally for DMG: ${CERT_IDENTITY}"
    fi
fi

# Verify we found a valid certificate
if [ -n "${CERT_IDENTITY}" ]; then

    # Sign DMG with proper certificate and timestamp
    codesign --force \
        --sign "${CERT_IDENTITY}" \
        --timestamp \
        "${DMG_NAME}.dmg"

    # Verify DMG signing
    echo "=== Verifying DMG signature ==="
    codesign --verify --verbose "${DMG_NAME}.dmg"
    echo "DMG verification: $?"

    codesign --display --verbose=4 "${DMG_NAME}.dmg"

    echo "✅ DMG created and signed successfully: ${DMG_NAME}.dmg"
else
    echo "❌ No Developer ID certificate found for DMG, using ad-hoc signature"
    echo "This will NOT work for notarization!"
    codesign --force --sign - "${DMG_NAME}.dmg"
    echo "⚠️  DMG created with ad-hoc signature: ${DMG_NAME}.dmg"
fi

echo "Installer DMG created successfully: ${DMG_NAME}.dmg"
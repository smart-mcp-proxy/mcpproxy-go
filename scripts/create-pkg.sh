#!/bin/bash
set -e

# Script to create macOS PKG installer
TRAY_BINARY_PATH="$1"
CORE_BINARY_PATH="$2"
VERSION="$3"
ARCH="$4"

if [ -z "$TRAY_BINARY_PATH" ] || [ -z "$CORE_BINARY_PATH" ] || [ -z "$VERSION" ] || [ -z "$ARCH" ]; then
    echo "Usage: $0 <tray_binary> <core_binary> <version> <arch>"
    echo "Example: $0 ./mcpproxy-tray ./mcpproxy v1.0.0 arm64"
    exit 1
fi

if [ ! -f "$TRAY_BINARY_PATH" ]; then
    echo "Tray binary not found: $TRAY_BINARY_PATH"
    exit 1
fi

if [ ! -f "$CORE_BINARY_PATH" ]; then
    echo "Core binary not found: $CORE_BINARY_PATH"
    exit 1
fi

# Variables
APP_NAME="mcpproxy"
BUNDLE_ID="com.smartmcpproxy.mcpproxy"
PKG_NAME="mcpproxy-${VERSION#v}-darwin-${ARCH}"
TEMP_DIR="pkg_temp"
APP_BUNDLE="${APP_NAME}.app"
PKG_ROOT="$TEMP_DIR/pkg_root"
PKG_SCRIPTS="$TEMP_DIR/pkg_scripts"

echo "Creating PKG for ${APP_NAME} ${VERSION} (${ARCH})"

# Clean up previous builds
rm -rf "$TEMP_DIR"
rm -f "${PKG_NAME}.pkg"
rm -f "${PKG_NAME}-component.pkg"

# Create temporary directories
mkdir -p "$PKG_ROOT/Applications"
mkdir -p "$PKG_SCRIPTS"

# Create app bundle structure in PKG root
mkdir -p "$PKG_ROOT/Applications/$APP_BUNDLE/Contents/MacOS"
mkdir -p "$PKG_ROOT/Applications/$APP_BUNDLE/Contents/Resources/bin"

# Copy tray binary as main executable
cp "$TRAY_BINARY_PATH" "$PKG_ROOT/Applications/$APP_BUNDLE/Contents/MacOS/$APP_NAME"
chmod +x "$PKG_ROOT/Applications/$APP_BUNDLE/Contents/MacOS/$APP_NAME"

# Copy core binary inside Resources/bin for the tray to manage
cp "$CORE_BINARY_PATH" "$PKG_ROOT/Applications/$APP_BUNDLE/Contents/Resources/bin/mcpproxy"
chmod +x "$PKG_ROOT/Applications/$APP_BUNDLE/Contents/Resources/bin/mcpproxy"

# Generate CA certificate for bundling (HTTP mode by default, HTTPS optional)
echo "Generating CA certificate for bundling..."
mkdir -p "$PKG_ROOT/Applications/$APP_BUNDLE/Contents/Resources/certs"

# Use the core binary to generate certificates in a temporary directory
TEMP_CERT_DIR=$(mktemp -d)
export MCPPROXY_TLS_ENABLED=true
"$CORE_BINARY_PATH" serve --data-dir="$TEMP_CERT_DIR" --config=/dev/null &
SERVER_PID=$!

# Wait for certificate generation (server will create certs on startup)
sleep 3

# Kill the temporary server
kill $SERVER_PID 2>/dev/null || true
wait $SERVER_PID 2>/dev/null || true

# Copy generated CA certificate to bundle
if [ -f "$TEMP_CERT_DIR/certs/ca.pem" ]; then
    cp "$TEMP_CERT_DIR/certs/ca.pem" "$PKG_ROOT/Applications/$APP_BUNDLE/Contents/Resources/"
    chmod 644 "$PKG_ROOT/Applications/$APP_BUNDLE/Contents/Resources/ca.pem"
    echo "✅ CA certificate bundled"
else
    echo "⚠️  Failed to generate CA certificate for bundling"
fi

# Clean up temporary certificate directory
rm -rf "$TEMP_CERT_DIR"

# Copy icon if available
if [ -f "assets/mcpproxy.icns" ]; then
    cp "assets/mcpproxy.icns" "$PKG_ROOT/Applications/$APP_BUNDLE/Contents/Resources/"
    ICON_FILE="mcpproxy.icns"
else
    echo "Warning: mcpproxy.icns not found, using default icon"
    ICON_FILE=""
fi

# Create Info.plist
cat > "$PKG_ROOT/Applications/$APP_BUNDLE/Contents/Info.plist" << EOF
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>CFBundleExecutable</key>
    <string>${APP_NAME}</string>
    <key>CFBundleIdentifier</key>
    <string>${BUNDLE_ID}</string>
    <key>CFBundleName</key>
    <string>mcpproxy</string>
    <key>CFBundleDisplayName</key>
    <string>Smart MCP Proxy</string>
    <key>CFBundleVersion</key>
    <string>${VERSION#v}</string>
    <key>CFBundleShortVersionString</key>
    <string>${VERSION#v}</string>
    <key>CFBundlePackageType</key>
    <string>APPL</string>
    <key>CFBundleSignature</key>
    <string>MCPP</string>
    <key>LSMinimumSystemVersion</key>
    <string>10.15</string>
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
    <key>NSUserNotificationAlertStyle</key>
    <string>alert</string>
EOF

if [ -n "$ICON_FILE" ]; then
cat >> "$PKG_ROOT/Applications/$APP_BUNDLE/Contents/Info.plist" << EOF
    <key>CFBundleIconFile</key>
    <string>mcpproxy</string>
EOF
fi

cat >> "$PKG_ROOT/Applications/$APP_BUNDLE/Contents/Info.plist" << EOF
</dict>
</plist>
EOF

# Create empty PkgInfo file (required for proper app bundle)
echo "APPLMCPP" > "$PKG_ROOT/Applications/$APP_BUNDLE/Contents/PkgInfo"

# Sign the app bundle properly with Developer ID certificate
echo "Signing app bundle with Developer ID certificate..."

# Use certificate identity passed from GitHub workflow environment
if [ -n "${APP_CERT_IDENTITY}" ]; then
    CERT_IDENTITY="${APP_CERT_IDENTITY}"
    echo "✅ Using provided Developer ID Application certificate: ${CERT_IDENTITY}"
else
    # Fallback: Find the Developer ID certificate locally
    CERT_IDENTITY=$(security find-identity -v -p codesigning | grep "Developer ID Application" | head -1 | grep -o '"[^"]*"' | tr -d '"')
    if [ -n "${CERT_IDENTITY}" ]; then
        echo "✅ Found Developer ID certificate locally: ${CERT_IDENTITY}"
    fi
fi

if [ -n "${CERT_IDENTITY}" ]; then

    # Validate entitlements file formatting (Apple's recommendation)
    if [ -f "scripts/entitlements.plist" ]; then
        echo "=== Validating entitlements file ==="
        if plutil -lint scripts/entitlements.plist; then
            echo "✅ Entitlements file is properly formatted"
        else
            echo "❌ Entitlements file has formatting issues"
            exit 1
        fi

        # Convert to XML format if needed
        plutil -convert xml1 scripts/entitlements.plist
        echo "✅ Entitlements converted to XML format"
    fi

    # Sign with proper Developer ID certificate, hardened runtime, and production entitlements
    if [ -f "scripts/entitlements.plist" ]; then
        echo "Using production entitlements..."
        codesign --force --deep \
            --options runtime \
            --sign "${CERT_IDENTITY}" \
            --identifier "$BUNDLE_ID" \
            --entitlements "scripts/entitlements.plist" \
            --timestamp \
            "$PKG_ROOT/Applications/$APP_BUNDLE"
    else
        echo "No entitlements file found, signing without..."
        codesign --force --deep \
            --options runtime \
            --sign "${CERT_IDENTITY}" \
            --identifier "$BUNDLE_ID" \
            --timestamp \
            "$PKG_ROOT/Applications/$APP_BUNDLE"
    fi

    # Verify signing using Apple's recommended methods
    echo "=== Verifying app bundle signature ==="
    codesign --verify --verbose "$PKG_ROOT/Applications/$APP_BUNDLE"

    # Apple's recommended strict verification for notarization
    echo "=== Strict verification (matches notarization requirements) ==="
    if codesign -vvv --deep --strict "$PKG_ROOT/Applications/$APP_BUNDLE"; then
        echo "✅ App bundle strict verification PASSED - ready for notarization"
    else
        echo "❌ App bundle strict verification FAILED - will not pass notarization"
        exit 1
    fi

    echo "✅ App bundle signed successfully"
else
    echo "❌ No Developer ID certificate found - using ad-hoc signature"
    echo "This will NOT work for notarization!"
    codesign --force --deep --sign - --identifier "$BUNDLE_ID" "$PKG_ROOT/Applications/$APP_BUNDLE"
fi

# Copy postinstall script
cp "scripts/postinstall.sh" "$PKG_SCRIPTS/postinstall"
chmod +x "$PKG_SCRIPTS/postinstall"

# Create component PKG
echo "Creating component PKG..."
pkgbuild --root "$PKG_ROOT" \
         --scripts "$PKG_SCRIPTS" \
         --identifier "$BUNDLE_ID.pkg" \
         --version "${VERSION#v}" \
         --install-location "/" \
         "${PKG_NAME}-component.pkg"

# Create Distribution.xml for product archive
cat > "$TEMP_DIR/Distribution.xml" << EOF
<?xml version="1.0" encoding="utf-8"?>
<installer-gui-script minSpecVersion="1">
    <title>Smart MCP Proxy ${VERSION#v}</title>
    <organization>com.smartmcpproxy</organization>
    <domains enable_localSystem="true"/>
    <options customize="never" require-scripts="true" rootVolumeOnly="true" />

    <!-- Define documents displayed at various steps -->
    <welcome language="en" mime-type="text/rtf">welcome_en.rtf</welcome>
    <conclusion language="en" mime-type="text/rtf">conclusion_en.rtf</conclusion>

    <!-- List all component packages -->
    <pkg-ref id="$BUNDLE_ID.pkg"/>

    <!-- Define the order of installation -->
    <choices-outline>
        <line choice="default">
            <line choice="$BUNDLE_ID.pkg"/>
        </line>
    </choices-outline>

    <!-- Define the choices -->
    <choice id="default"/>
    <choice id="$BUNDLE_ID.pkg" visible="false">
        <pkg-ref id="$BUNDLE_ID.pkg"/>
    </choice>

    <!-- Define package references -->
    <pkg-ref id="$BUNDLE_ID.pkg"
             version="${VERSION#v}"
             auth="root">${PKG_NAME}-component.pkg</pkg-ref>
</installer-gui-script>
EOF

# Create welcome RTF
cat > "$TEMP_DIR/welcome_en.rtf" << 'EOF'
{\rtf1\ansi\deff0 {\fonttbl {\f0 Times New Roman;}}
\f0\fs24 Welcome to Smart MCP Proxy installer.

This will install mcpproxy on your computer.

Features:
• CLI tool available in Terminal
• System tray application
• Intelligent MCP server proxy
• Built-in security features

Click Continue to proceed with the installation.
}
EOF

# Create conclusion RTF
cat > "$TEMP_DIR/conclusion_en.rtf" << 'EOF'
{\rtf1\ansi\deff0 {\fonttbl {\f0 Times New Roman;}}
\f0\fs24 Installation completed successfully!

Smart MCP Proxy has been installed to your Applications folder.

To get started:
• Open mcpproxy from Applications
• Or use 'mcpproxy' command in Terminal

For HTTPS support (optional):
• Run: mcpproxy trust-cert
• Set: export MCPPROXY_TLS_ENABLED=true

Visit https://github.com/smart-mcp-proxy/mcpproxy-go for documentation.
}
EOF

# Create product PKG (installer)
echo "Creating product PKG..."
productbuild --distribution "$TEMP_DIR/Distribution.xml" \
             --package-path "$TEMP_DIR" \
             --resources "$TEMP_DIR" \
             "${PKG_NAME}.pkg"

# Sign the PKG with Developer ID Installer certificate
echo "Signing PKG installer..."

# Use certificate identity passed from GitHub workflow environment
if [ -n "${PKG_CERT_IDENTITY}" ]; then
    INSTALLER_CERT_IDENTITY="${PKG_CERT_IDENTITY}"
    echo "✅ Using provided Developer ID Installer certificate: ${INSTALLER_CERT_IDENTITY}"
else
    # Fallback: Find the Developer ID Installer certificate locally
    INSTALLER_CERT_IDENTITY=$(security find-identity -v -p basic | grep "Developer ID Installer" | head -1 | grep -o '"[^"]*"' | tr -d '"')
    if [ -n "${INSTALLER_CERT_IDENTITY}" ]; then
        echo "✅ Found Developer ID Installer certificate locally: ${INSTALLER_CERT_IDENTITY}"
    fi
fi

if [ -n "${INSTALLER_CERT_IDENTITY}" ]; then

    # Sign the PKG
    productsign --sign "${INSTALLER_CERT_IDENTITY}" \
                --timestamp \
                "${PKG_NAME}.pkg" \
                "${PKG_NAME}-signed.pkg"

    # Replace unsigned with signed
    mv "${PKG_NAME}-signed.pkg" "${PKG_NAME}.pkg"

    # Verify PKG signing
    echo "=== Verifying PKG signature ==="
    pkgutil --check-signature "${PKG_NAME}.pkg"

    echo "✅ PKG signed successfully"
else
    echo "❌ No Developer ID Installer certificate found"
    echo "PKG will be unsigned (will not pass notarization)"
fi

# Clean up
rm -rf "$TEMP_DIR"

echo "PKG installer created successfully: ${PKG_NAME}.pkg"
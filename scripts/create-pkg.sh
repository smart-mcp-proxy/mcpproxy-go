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
    <string>MCP Proxy</string>
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

# Resolve installer signing identity (must be Developer ID Installer)
INSTALLER_CERT_IDENTITY="${PKG_CERT_IDENTITY}"

if [ -z "${INSTALLER_CERT_IDENTITY}" ]; then
    INSTALLER_CERT_IDENTITY=$(security find-identity -v -p basic | grep "Developer ID Installer" | head -1 | grep -o '"[^"]*"' | tr -d '"')
fi

if [ -n "${INSTALLER_CERT_IDENTITY}" ] && echo "${INSTALLER_CERT_IDENTITY}" | grep -q "Developer ID Installer"; then
    echo "Using product PKG approach with Installer certificate: ${INSTALLER_CERT_IDENTITY}"
    CREATE_PRODUCT_PKG=true
elif [ "${PKG_CERT_IDENTITY}" = "adhoc" ] || [ "${PKG_CERT_IDENTITY}" = "skip" ]; then
    echo "⚠️  Skipping PKG creation (adhoc mode for development/PR builds)"
    echo "   App bundle will be created but not packaged into PKG"
    echo "   Use create-app-dmg.sh to create a DMG with the app bundle for distribution"
    CREATE_PRODUCT_PKG=false

    # Export the app bundle path for follow-up scripts
    echo "APP_BUNDLE_PATH=$PKG_ROOT/Applications/$APP_BUNDLE" >> "$GITHUB_ENV" || true

    # Don't exit - let the script complete with app bundle creation only
else
    echo "❌ Developer ID Installer certificate not available"
    echo "   PKG installers must be signed with a 'Developer ID Installer' identity to satisfy Gatekeeper"
    echo "   Ensure the certificate is imported and expose it to this script via PKG_CERT_IDENTITY"
    echo "   For PR builds, set PKG_CERT_IDENTITY=adhoc to create app bundle without PKG"
    exit 1
fi

if [ "$CREATE_PRODUCT_PKG" = "true" ]; then

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
    <title>MCP Proxy ${VERSION#v}</title>
    <organization>com.smartmcpproxy</organization>
    <domains enable_localSystem="true"/>
    <options customize="never" require-scripts="true" rootVolumeOnly="true" />

    <!-- Define documents displayed at various steps -->
    <welcome file="welcome_en.rtf" mime-type="text/rtf"/>
    <conclusion file="conclusion_en.rtf" mime-type="text/rtf"/>

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

# Copy RTF files from installer-resources, or create inline as fallback
if [ -f "scripts/installer-resources/welcome_en.rtf" ]; then
    echo "Using external welcome_en.rtf from installer-resources/"
    cp "scripts/installer-resources/welcome_en.rtf" "$TEMP_DIR/welcome_en.rtf"
    ls -la "$TEMP_DIR/welcome_en.rtf"
else
    echo "Warning: scripts/installer-resources/welcome_en.rtf not found, using inline fallback"
    cat > "$TEMP_DIR/welcome_en.rtf" << 'EOF'
{\rtf1\ansi\deff0 {\fonttbl {\f0 Times New Roman;}}
\f0\fs28 MCP Proxy Installer.
\fs24 Welcome to the MCP Proxy installer. This guided setup installs the desktop tray, CLI, and secure proxy that coordinate your AI tools across multiple MCP servers.

What this installer sets up:
• Federated MCP hub so agents can discover dozens of tools without hitting provider limits
• Security quarantine that keeps untrusted servers isolated until you approve them
• Local certificate authority to enable HTTPS connections with a single command

Before continuing, close any running copies of MCP Proxy and make sure you have administrator privileges.

Click Continue to start installing MCP Proxy.
}
EOF
fi

if [ -f "scripts/installer-resources/conclusion_en.rtf" ]; then
    echo "Using external conclusion_en.rtf from installer-resources/"
    cp "scripts/installer-resources/conclusion_en.rtf" "$TEMP_DIR/conclusion_en.rtf"
    ls -la "$TEMP_DIR/conclusion_en.rtf"
else
    echo "Warning: scripts/installer-resources/conclusion_en.rtf not found, using inline fallback"
    cat > "$TEMP_DIR/conclusion_en.rtf" << 'EOF'
{\rtf1\ansi\deff0 {\fonttbl {\f0 Times New Roman;}}
\f0\fs28 MCP Proxy Ready.
\fs24 Installation completed successfully!

Next steps:
• Launch MCP Proxy from Applications to access the menu bar controls.
• Run the mcpproxy serve command from Terminal if you prefer the CLI workflow.
• Enable HTTPS clients later by running mcpproxy trust-cert to trust the bundled certificate.

Helpful resources:
• Documentation: https://mcpproxy.app/docs
• GitHub releases & support: https://github.com/smart-mcp-proxy/mcpproxy-go

Thank you for installing MCP Proxy—enjoy faster, safer MCP tooling.
}
EOF
fi

    # Verify RTF files are in place
    echo "=== Verifying resources for product PKG ==="
    ls -la "$TEMP_DIR"/*.rtf 2>/dev/null || echo "Warning: No RTF files found in $TEMP_DIR"
    echo "=== Distribution.xml contents ==="
    grep -A2 -E "(welcome|conclusion)" "$TEMP_DIR/Distribution.xml"

    # Create product PKG (installer)
    echo "Creating product PKG..."
    productbuild --distribution "$TEMP_DIR/Distribution.xml" \
                 --package-path "$TEMP_DIR" \
                 --resources "$TEMP_DIR" \
                 "${PKG_NAME}.pkg"

fi  # End of CREATE_PRODUCT_PKG conditional

# Sign the PKG with Developer ID Installer certificate (only for product PKGs)
if [ "$CREATE_PRODUCT_PKG" = "true" ]; then
    echo "Signing product PKG installer with ${INSTALLER_CERT_IDENTITY}..."

    if ! productsign --sign "${INSTALLER_CERT_IDENTITY}" \
                     --timestamp \
                     "${PKG_NAME}.pkg" \
                     "${PKG_NAME}-signed.pkg"; then
        echo "❌ PKG signing with productsign failed"
        exit 1
    fi

    mv "${PKG_NAME}-signed.pkg" "${PKG_NAME}.pkg"

    echo "=== Verifying PKG signature ==="
    if ! pkgutil --check-signature "${PKG_NAME}.pkg"; then
        echo "❌ PKG signature verification failed"
        exit 1
    fi

    echo "✅ PKG signed successfully with Developer ID Installer certificate"

    # Clean up temp directory after PKG creation
    rm -rf "$TEMP_DIR"
    echo "PKG installer created successfully: ${PKG_NAME}.pkg"
elif [ "$CREATE_PRODUCT_PKG" = "false" ]; then
    echo "⚠️  PKG creation skipped (adhoc mode)"
    echo "   App bundle created at: $PKG_ROOT/Applications/$APP_BUNDLE"
    echo "   To create a DMG: scripts/create-app-dmg.sh \"$PKG_ROOT/Applications/$APP_BUNDLE\" ${VERSION} ${ARCH}"
    # Don't clean up TEMP_DIR yet - the app bundle is still there for DMG creation
fi

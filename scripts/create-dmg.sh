#!/bin/bash
set -e

# Script to create macOS DMG installer
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
DMG_NAME="mcpproxy-${VERSION#v}-darwin-${ARCH}"
TEMP_DIR="dmg_temp"
APP_BUNDLE="${APP_NAME}.app"

echo "Creating DMG for ${APP_NAME} ${VERSION} (${ARCH})"

# Clean up previous builds
rm -rf "$TEMP_DIR"
rm -f "${DMG_NAME}.dmg"

# Create temporary directory
mkdir -p "$TEMP_DIR"

# Create app bundle structure
mkdir -p "$TEMP_DIR/$APP_BUNDLE/Contents/MacOS"
mkdir -p "$TEMP_DIR/$APP_BUNDLE/Contents/Resources"

# Copy tray binary
cp "$TRAY_BINARY_PATH" "$TEMP_DIR/$APP_BUNDLE/Contents/MacOS/$APP_NAME"
chmod +x "$TEMP_DIR/$APP_BUNDLE/Contents/MacOS/$APP_NAME"

# Copy core binary inside Resources/bin for the tray to manage
mkdir -p "$TEMP_DIR/$APP_BUNDLE/Contents/Resources/bin"
cp "$CORE_BINARY_PATH" "$TEMP_DIR/$APP_BUNDLE/Contents/Resources/bin/mcpproxy"
chmod +x "$TEMP_DIR/$APP_BUNDLE/Contents/Resources/bin/mcpproxy"

# Generate CA certificate for bundling
echo "Generating CA certificate for bundling..."
mkdir -p "$TEMP_DIR/$APP_BUNDLE/Contents/Resources/certs"

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
    cp "$TEMP_CERT_DIR/certs/ca.pem" "$TEMP_DIR/$APP_BUNDLE/Contents/Resources/"
    chmod 644 "$TEMP_DIR/$APP_BUNDLE/Contents/Resources/ca.pem"
    echo "✅ CA certificate bundled"
else
    echo "⚠️  Failed to generate CA certificate for bundling"
fi

# Clean up temporary certificate directory
rm -rf "$TEMP_CERT_DIR"

# Copy icon if available
if [ -f "assets/mcpproxy.icns" ]; then
    cp "assets/mcpproxy.icns" "$TEMP_DIR/$APP_BUNDLE/Contents/Resources/"
    ICON_FILE="mcpproxy.icns"
else
    echo "Warning: mcpproxy.icns not found, using default icon"
    ICON_FILE=""
fi

# Create Info.plist
cat > "$TEMP_DIR/$APP_BUNDLE/Contents/Info.plist" << EOF
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
cat >> "$TEMP_DIR/$APP_BUNDLE/Contents/Info.plist" << EOF
    <key>CFBundleIconFile</key>
    <string>mcpproxy</string>
EOF
fi

cat >> "$TEMP_DIR/$APP_BUNDLE/Contents/Info.plist" << EOF
</dict>
</plist>
EOF

# Create empty PkgInfo file (required for proper app bundle)
echo "APPLMCPP" > "$TEMP_DIR/$APP_BUNDLE/Contents/PkgInfo"

# Sign the app bundle properly with Developer ID certificate
echo "Signing app bundle with Developer ID certificate..."

# Find the Developer ID certificate (same logic as in workflow)
CERT_IDENTITY=$(security find-identity -v -p codesigning | grep "Developer ID Application" | head -1 | grep -o '"[^"]*"' | tr -d '"')

if [ -n "${CERT_IDENTITY}" ]; then
    echo "✅ Found Developer ID certificate: ${CERT_IDENTITY}"
    
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
            "$TEMP_DIR/$APP_BUNDLE"
    else
        echo "No entitlements file found, signing without..."
        codesign --force --deep \
            --options runtime \
            --sign "${CERT_IDENTITY}" \
            --identifier "$BUNDLE_ID" \
            --timestamp \
            "$TEMP_DIR/$APP_BUNDLE"
    fi
    
    # Verify signing using Apple's recommended methods
    echo "=== Verifying app bundle signature ==="
    codesign --verify --verbose "$TEMP_DIR/$APP_BUNDLE"
    
    # Apple's recommended strict verification for notarization
    echo "=== Strict verification (matches notarization requirements) ==="
    if codesign -vvv --deep --strict "$TEMP_DIR/$APP_BUNDLE"; then
        echo "✅ App bundle strict verification PASSED - ready for notarization"
    else
        echo "❌ App bundle strict verification FAILED - will not pass notarization"
        exit 1
    fi
    
    # Check for secure timestamp
    echo "=== Checking app bundle timestamp ==="
    TIMESTAMP_CHECK=$(codesign -dvv "$TEMP_DIR/$APP_BUNDLE" 2>&1)
    if echo "$TIMESTAMP_CHECK" | grep -q "Timestamp="; then
        echo "✅ App bundle has secure timestamp:"
        echo "$TIMESTAMP_CHECK" | grep "Timestamp="
    else
        echo "❌ App bundle missing secure timestamp"
    fi
    
    # Show detailed signature information
    echo "=== App bundle signature details ==="
    codesign --display --verbose=4 "$TEMP_DIR/$APP_BUNDLE"
    
    # Check entitlements
    echo "=== App bundle entitlements ==="
    codesign --display --entitlements - "$TEMP_DIR/$APP_BUNDLE"
    
else
    echo "❌ No Developer ID certificate found - using ad-hoc signature"
    echo "This will NOT work for notarization!"
    codesign --force --deep --sign - --identifier "$BUNDLE_ID" "$TEMP_DIR/$APP_BUNDLE"
fi

# Create Applications symlink
ln -s /Applications "$TEMP_DIR/Applications"

# Include release notes if available
# Look for release notes file in current directory (downloaded from artifact)
RELEASE_NOTES_FILE=""
for file in RELEASE_NOTES-*.md RELEASE_NOTES.md; do
    if [ -f "$file" ]; then
        RELEASE_NOTES_FILE="$file"
        break
    fi
done

if [ -n "$RELEASE_NOTES_FILE" ]; then
    cp "$RELEASE_NOTES_FILE" "$TEMP_DIR/RELEASE_NOTES.md"
    echo "✅ Release notes included in DMG: $RELEASE_NOTES_FILE"
else
    echo "⚠️  No release notes file found, DMG will be created without release notes"
fi

# Create DMG using hdiutil
echo "Creating DMG..."
hdiutil create -size 100m -fs HFS+ -volname "mcpproxy ${VERSION#v}" -srcfolder "$TEMP_DIR" "${DMG_NAME}.dmg"

# Clean up
rm -rf "$TEMP_DIR"

echo "DMG created: ${DMG_NAME}.dmg"

# Make DMG read-only and compressed
echo "Compressing DMG..."
hdiutil convert "${DMG_NAME}.dmg" -format UDZO -o "${DMG_NAME}-compressed.dmg"
mv "${DMG_NAME}-compressed.dmg" "${DMG_NAME}.dmg"

echo "DMG installer created successfully: ${DMG_NAME}.dmg" 

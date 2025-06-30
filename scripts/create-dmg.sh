#!/bin/bash
set -e

# Script to create macOS DMG installer
BINARY_PATH="$1"
VERSION="$2"
ARCH="$3"

if [ -z "$BINARY_PATH" ] || [ -z "$VERSION" ] || [ -z "$ARCH" ]; then
    echo "Usage: $0 <binary_path> <version> <arch>"
    echo "Example: $0 ./mcpproxy v1.0.0 arm64"
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

# Copy binary
cp "$BINARY_PATH" "$TEMP_DIR/$APP_BUNDLE/Contents/MacOS/$APP_NAME"
chmod +x "$TEMP_DIR/$APP_BUNDLE/Contents/MacOS/$APP_NAME"

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

# Sign the app bundle properly
echo "Signing app bundle..."

# Use development entitlements if available, otherwise sign without entitlements
if [ -f "scripts/entitlements-dev.plist" ]; then
    echo "Using development entitlements..."
    codesign --force --deep --sign - --identifier "$BUNDLE_ID" --entitlements "scripts/entitlements-dev.plist" "$TEMP_DIR/$APP_BUNDLE"
else
    echo "Signing without entitlements..."
    codesign --force --deep --sign - --identifier "$BUNDLE_ID" "$TEMP_DIR/$APP_BUNDLE"
fi

# Verify signing
codesign --verify --verbose "$TEMP_DIR/$APP_BUNDLE"
echo "App bundle signed successfully"

# Create Applications symlink
ln -s /Applications "$TEMP_DIR/Applications"

# Create DMG using hdiutil
echo "Creating DMG..."
hdiutil create -size 50m -fs HFS+ -volname "mcpproxy ${VERSION#v}" -srcfolder "$TEMP_DIR" "${DMG_NAME}.dmg"

# Clean up
rm -rf "$TEMP_DIR"

echo "DMG created: ${DMG_NAME}.dmg"

# Make DMG read-only and compressed
echo "Compressing DMG..."
hdiutil convert "${DMG_NAME}.dmg" -format UDZO -o "${DMG_NAME}-compressed.dmg"
mv "${DMG_NAME}-compressed.dmg" "${DMG_NAME}.dmg"

echo "DMG installer created successfully: ${DMG_NAME}.dmg" 
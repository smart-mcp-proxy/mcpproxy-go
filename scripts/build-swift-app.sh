#!/bin/bash
set -e

# Build the Swift macOS tray app and assemble a .app bundle
# Usage: ./scripts/build-swift-app.sh <version> <arch> <output_dir>
# Example: ./scripts/build-swift-app.sh v0.22.0 arm64 /tmp

VERSION="${1:-v0.0.0}"
ARCH="${2:-arm64}"
OUTPUT_DIR="${3:-.}"

SWIFT_DIR="native/macos/MCPProxy"
BUNDLE_ID="com.smartmcpproxy.mcpproxy"
APP_NAME="MCPProxy"

# Map Go arch names to Swift/Apple arch names
case "$ARCH" in
  arm64) SWIFT_ARCH="arm64" ;;
  amd64|x86_64) SWIFT_ARCH="x86_64" ;;
  *) echo "Unknown arch: $ARCH"; exit 1 ;;
esac

echo "Building Swift tray app ${VERSION} for ${SWIFT_ARCH}..."

cd "$SWIFT_DIR"

# Try swift build first (needs Xcode), fall back to swiftc (works with Command Line Tools)
BINARY_PATH=""
if swift build -c release --arch "$SWIFT_ARCH" 2>&1; then
  # Find the built binary from SPM
  BINARY_PATH=".build/release/${APP_NAME}"
  if [ ! -f "$BINARY_PATH" ]; then
    BINARY_PATH=".build/${SWIFT_ARCH}-apple-macosx/release/${APP_NAME}"
  fi
  if [ ! -f "$BINARY_PATH" ]; then
    BINARY_PATH="$(find .build -name "${APP_NAME}" -type f -perm +111 2>/dev/null | grep -v repositories | grep -v dSYM | head -1)"
  fi
fi

if [ -z "$BINARY_PATH" ] || [ ! -f "$BINARY_PATH" ]; then
  echo "SPM build failed or binary not found, falling back to swiftc..."
  SDK=$(xcrun --sdk macosx --show-sdk-path 2>/dev/null || echo "/Library/Developer/CommandLineTools/SDKs/MacOSX.sdk")
  BINARY_PATH="/tmp/${APP_NAME}-build"
  swiftc -target "${SWIFT_ARCH}-apple-macosx13.0" -sdk "$SDK" \
    -module-name "$APP_NAME" -emit-executable -O \
    -o "$BINARY_PATH" \
    $(find MCPProxy -name "*.swift" -not -path "*/Tests/*" | sort | tr '\n' ' ') 2>&1
fi

if [ ! -f "$BINARY_PATH" ]; then
  echo "❌ Swift binary not found after both build methods"
  exit 1
fi
echo "✅ Swift binary built: $BINARY_PATH ($(du -sh "$BINARY_PATH" | cut -f1))"

# Assemble .app bundle
APP_BUNDLE="${OUTPUT_DIR}/${APP_NAME}.app"
rm -rf "$APP_BUNDLE"
mkdir -p "$APP_BUNDLE/Contents/MacOS"
mkdir -p "$APP_BUNDLE/Contents/Resources"

# Copy binary
cp "$BINARY_PATH" "$APP_BUNDLE/Contents/MacOS/${APP_NAME}"
chmod +x "$APP_BUNDLE/Contents/MacOS/${APP_NAME}"

# Copy Info.plist from source (update version)
if [ -f "MCPProxy/Info.plist" ]; then
  cp "MCPProxy/Info.plist" "$APP_BUNDLE/Contents/Info.plist"
  /usr/libexec/PlistBuddy -c "Set :CFBundleShortVersionString ${VERSION#v}" "$APP_BUNDLE/Contents/Info.plist" 2>/dev/null || true
  /usr/libexec/PlistBuddy -c "Set :CFBundleVersion ${VERSION#v}" "$APP_BUNDLE/Contents/Info.plist" 2>/dev/null || true
else
  # Generate minimal Info.plist
  cat > "$APP_BUNDLE/Contents/Info.plist" << EOF
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>CFBundleExecutable</key>
    <string>${APP_NAME}</string>
    <key>CFBundleIdentifier</key>
    <string>${BUNDLE_ID}</string>
    <key>CFBundleName</key>
    <string>Smart MCP Proxy</string>
    <key>CFBundleVersion</key>
    <string>${VERSION#v}</string>
    <key>CFBundleShortVersionString</key>
    <string>${VERSION#v}</string>
    <key>CFBundlePackageType</key>
    <string>APPL</string>
    <key>LSMinimumSystemVersion</key>
    <string>13.0</string>
    <key>LSUIElement</key>
    <false/>
    <key>NSHighResolutionCapable</key>
    <true/>
</dict>
</plist>
EOF
fi

# Copy entitlements
if [ -f "MCPProxy/MCPProxy.entitlements" ]; then
  cp "MCPProxy/MCPProxy.entitlements" "$APP_BUNDLE/Contents/"
fi

# Copy asset catalog (icons) if compiled
if [ -f "MCPProxy/Assets.xcassets/AppIcon.appiconset/Contents.json" ]; then
  # For CI, we need actool to compile assets; skip if not available
  if command -v actool &>/dev/null; then
    actool --compile "$APP_BUNDLE/Contents/Resources" \
      --platform macosx --minimum-deployment-target 13.0 \
      "MCPProxy/Assets.xcassets" 2>/dev/null || true
  fi
fi

# Copy .icns icon if available
if [ -f "../../assets/mcpproxy.icns" ]; then
  cp "../../assets/mcpproxy.icns" "$APP_BUNDLE/Contents/Resources/"
elif [ -f "../../../assets/mcpproxy.icns" ]; then
  cp "../../../assets/mcpproxy.icns" "$APP_BUNDLE/Contents/Resources/"
fi

# PkgInfo
echo "APPLMCPP" > "$APP_BUNDLE/Contents/PkgInfo"

# Copy Sparkle framework if built
SPARKLE_FRAMEWORK=".build/artifacts/sparkle/Sparkle.xcframework/macos-arm64_x86_64/Sparkle.framework"
if [ -d "$SPARKLE_FRAMEWORK" ]; then
  mkdir -p "$APP_BUNDLE/Contents/Frameworks"
  cp -R "$SPARKLE_FRAMEWORK" "$APP_BUNDLE/Contents/Frameworks/"
fi

cd - > /dev/null

echo "✅ Swift app bundle assembled: $APP_BUNDLE"
echo "   Binary: $APP_BUNDLE/Contents/MacOS/${APP_NAME}"
echo "   Size: $(du -sh "$APP_BUNDLE" | cut -f1)"
echo "SWIFT_APP_PATH=$APP_BUNDLE"

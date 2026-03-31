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

# Resolve OUTPUT_DIR to absolute path before cd
OUTPUT_DIR="$(cd "$OUTPUT_DIR" && pwd)"

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

# Compile asset catalog if actool is available (Xcode must be installed)
if xcrun --find actool &>/dev/null 2>&1; then
  echo "Compiling asset catalog..."
  xcrun actool --compile "$APP_BUNDLE/Contents/Resources" \
    --platform macosx --minimum-deployment-target 13.0 \
    --app-icon AppIcon --output-partial-info-plist /dev/null \
    "MCPProxy/Assets.xcassets" 2>&1 | tail -3
  echo "✅ Asset catalog compiled"
else
  echo "⚠️  actool not available, copying icon PNGs directly..."
fi

# Always copy raw icon PNGs as fallback (tray icon loads by filename)
if [ -f "MCPProxy/Assets.xcassets/TrayIcon.imageset/icon-mono-44.png" ]; then
  cp "MCPProxy/Assets.xcassets/TrayIcon.imageset/icon-mono-44.png" "$APP_BUNDLE/Contents/Resources/"
  echo "✅ Tray icon copied: icon-mono-44.png"
fi
for icon in MCPProxy/Assets.xcassets/AppIcon.appiconset/icon-*.png; do
  [ -f "$icon" ] && cp "$icon" "$APP_BUNDLE/Contents/Resources/"
done

# Copy .icns icon — check Swift project first, then repo root
if [ -f "MCPProxy/mcpproxy.icns" ]; then
  cp "MCPProxy/mcpproxy.icns" "$APP_BUNDLE/Contents/Resources/"
  echo "✅ App icon copied: mcpproxy.icns (from Swift project)"
else
  REPO_ROOT="$(cd ../../.. 2>/dev/null && pwd)"
  if [ -f "$REPO_ROOT/assets/mcpproxy.icns" ]; then
    cp "$REPO_ROOT/assets/mcpproxy.icns" "$APP_BUNDLE/Contents/Resources/"
    echo "✅ App icon copied: mcpproxy.icns (from repo assets)"
  else
    # Generate .icns from PNGs if iconutil is available
    if command -v iconutil &>/dev/null && [ -d "MCPProxy/Assets.xcassets/AppIcon.appiconset" ]; then
      ICONSET="/tmp/mcpproxy-build.iconset"
      mkdir -p "$ICONSET"
      SRC="MCPProxy/Assets.xcassets/AppIcon.appiconset"
      cp "$SRC/icon-16.png" "$ICONSET/icon_16x16.png" 2>/dev/null || true
      cp "$SRC/icon-32.png" "$ICONSET/icon_16x16@2x.png" 2>/dev/null || true
      cp "$SRC/icon-32.png" "$ICONSET/icon_32x32.png" 2>/dev/null || true
      cp "$SRC/icon-128.png" "$ICONSET/icon_128x128.png" 2>/dev/null || true
      cp "$SRC/icon-256.png" "$ICONSET/icon_128x128@2x.png" 2>/dev/null || true
      cp "$SRC/icon-256.png" "$ICONSET/icon_256x256.png" 2>/dev/null || true
      cp "$SRC/icon-512.png" "$ICONSET/icon_256x256@2x.png" 2>/dev/null || true
      cp "$SRC/icon-512.png" "$ICONSET/icon_512x512.png" 2>/dev/null || true
      iconutil -c icns "$ICONSET" -o "$APP_BUNDLE/Contents/Resources/mcpproxy.icns" 2>/dev/null && \
        echo "✅ App icon generated: mcpproxy.icns" || \
        echo "⚠️  Failed to generate .icns"
      rm -rf "$ICONSET"
    fi
  fi
fi

# PkgInfo
echo "APPLMCPP" > "$APP_BUNDLE/Contents/PkgInfo"

# Copy Sparkle.framework — required at runtime (@rpath linked)
# Search multiple locations where swift build may place it
SPARKLE_FRAMEWORK=""
for candidate in \
  ".build/artifacts/sparkle/Sparkle.xcframework/macos-arm64_x86_64/Sparkle.framework" \
  ".build/artifacts/sparkle/Sparkle/Sparkle.framework" \
  "$(find .build -name "Sparkle.framework" -type d 2>/dev/null | head -1)"; do
  if [ -d "$candidate" ]; then
    SPARKLE_FRAMEWORK="$candidate"
    break
  fi
done

if [ -d "$SPARKLE_FRAMEWORK" ]; then
  mkdir -p "$APP_BUNDLE/Contents/Frameworks"
  cp -R "$SPARKLE_FRAMEWORK" "$APP_BUNDLE/Contents/Frameworks/"
  echo "✅ Sparkle.framework bundled from: $SPARKLE_FRAMEWORK"
else
  echo "⚠️  Sparkle.framework NOT found — app will crash at launch!"
  echo "   This is expected with swiftc fallback (no SPM dependency resolution)"
  echo "   CI builds with 'swift build' should have it available"
  # For swiftc builds, we need to either:
  # 1. Download Sparkle manually, or
  # 2. Remove the Sparkle dependency from the binary
  # Let's download it as a fallback
  SPARKLE_VERSION="2.9.1"
  SPARKLE_URL="https://github.com/sparkle-project/Sparkle/releases/download/${SPARKLE_VERSION}/Sparkle-${SPARKLE_VERSION}.tar.xz"
  SPARKLE_TMP="/tmp/sparkle-download"
  mkdir -p "$SPARKLE_TMP"
  if curl -sL "$SPARKLE_URL" -o "$SPARKLE_TMP/Sparkle.tar.xz" 2>/dev/null; then
    tar xf "$SPARKLE_TMP/Sparkle.tar.xz" -C "$SPARKLE_TMP" 2>/dev/null
    if [ -d "$SPARKLE_TMP/Sparkle.framework" ]; then
      mkdir -p "$APP_BUNDLE/Contents/Frameworks"
      cp -R "$SPARKLE_TMP/Sparkle.framework" "$APP_BUNDLE/Contents/Frameworks/"
      echo "✅ Sparkle.framework downloaded and bundled (v${SPARKLE_VERSION})"
    fi
  fi
  rm -rf "$SPARKLE_TMP"
fi

cd - > /dev/null

# Fix rpath to find Sparkle.framework in Contents/Frameworks/
# swift build sets @rpath to @loader_path but we need @loader_path/../Frameworks
if otool -l "$APP_BUNDLE/Contents/MacOS/${APP_NAME}" 2>/dev/null | grep -q "LC_RPATH"; then
  # Check if ../Frameworks is already in rpath
  if ! otool -l "$APP_BUNDLE/Contents/MacOS/${APP_NAME}" 2>/dev/null | grep -A2 "LC_RPATH" | grep -q "../Frameworks"; then
    install_name_tool -add_rpath "@executable_path/../Frameworks" "$APP_BUNDLE/Contents/MacOS/${APP_NAME}" 2>/dev/null || true
    echo "✅ Added @executable_path/../Frameworks to rpath"
  fi
fi

echo "✅ Swift app bundle assembled: $APP_BUNDLE"
echo "   Binary: $APP_BUNDLE/Contents/MacOS/${APP_NAME}"
echo "   Size: $(du -sh "$APP_BUNDLE" | cut -f1)"
echo "SWIFT_APP_PATH=$APP_BUNDLE"

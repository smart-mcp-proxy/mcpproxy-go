# Quickstart: Native macOS Swift Tray App

**Feature**: 037-macos-swift-tray
**Date**: 2026-03-23

## Prerequisites

- macOS 13 Ventura or later
- Xcode 15+ with Command Line Tools
- Go 1.24+ (for building the core binary)
- Developer ID Application certificate in Keychain

## Build & Run (Development)

### 1. Build the Go core binary

```bash
cd /Users/user/repos/mcpproxy-go
go build -o mcpproxy ./cmd/mcpproxy
```

### 2. Open the Xcode project

```bash
open native/macos/MCPProxy/MCPProxy.xcodeproj
```

### 3. Configure the scheme

- Set the scheme to "MCPProxy" (macOS)
- In scheme settings → Run → Arguments, add environment variable:
  - `MCPPROXY_CORE_PATH=/Users/user/repos/mcpproxy-go/mcpproxy`
  (Points to the Go binary built in step 1, overrides bundled binary resolution)

### 4. Build & Run

Press Cmd+R. The app appears in the menu bar (no dock icon).

### 5. Verify

- Click the tray icon — menu should show "Launching..."
- After a few seconds, menu updates to show version and server status
- If `~/.mcpproxy/mcp_config.json` has servers configured, they appear in the Servers submenu

## Build for Distribution

### Full build (tray + core + DMG)

```bash
# From repo root
./native/macos/MCPProxy/scripts/build-macos-tray.sh --version v0.22.0

# Output: dist/mcpproxy-0.22.0-darwin-arm64.dmg (signed + notarized)
```

### Manual steps

```bash
# 1. Build universal Go binary
GOOS=darwin GOARCH=arm64 go build -o mcpproxy-arm64 ./cmd/mcpproxy
GOOS=darwin GOARCH=amd64 go build -o mcpproxy-amd64 ./cmd/mcpproxy
lipo -create mcpproxy-arm64 mcpproxy-amd64 -output mcpproxy

# 2. Build Swift tray app
cd native/macos/MCPProxy
xcodebuild -scheme MCPProxy -configuration Release \
  -archivePath build/MCPProxy.xcarchive archive
xcodebuild -exportArchive \
  -archivePath build/MCPProxy.xcarchive \
  -exportOptionsPlist ExportOptions.plist \
  -exportPath build/

# 3. Bundle core into app
cp mcpproxy build/MCPProxy.app/Contents/Resources/bin/mcpproxy

# 4. Sign
codesign --force --deep --sign "Developer ID Application: YOUR NAME" \
  --options runtime --entitlements MCPProxy/MCPProxy.entitlements \
  build/MCPProxy.app

# 5. Notarize
xcrun notarytool submit build/MCPProxy.app.zip --apple-id ... --wait

# 6. Create DMG
hdiutil create -volname MCPProxy -srcfolder build/MCPProxy.app \
  -ov -format UDZO mcpproxy.dmg
```

## Testing

```bash
# Unit tests
cd native/macos/MCPProxy
xcodebuild test -scheme MCPProxy -destination 'platform=macOS'

# Or from Xcode: Cmd+U
```

## Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| MCPPROXY_CORE_PATH | (bundled) | Override path to mcpproxy binary |
| MCPPROXY_TRAY_SKIP_CORE | (unset) | Set to "1" to skip core launch (attach to external) |
| MCPPROXY_CORE_URL | (socket) | Override core URL (e.g., http://localhost:8080) |

## Debugging

- Tray logs: `~/Library/Logs/mcpproxy/tray.log`
- Core logs: `~/.mcpproxy/logs/main.log`
- Debug Sparkle updates: Set `SUEnableAutomaticChecks` to YES in UserDefaults

# Design: CI Build for Swift macOS Tray App + Installer Updates

**Created**: 2026-03-30
**Status**: Approved
**Scope**: CI/CD pipeline changes to build, sign, and package the new Swift macOS tray app

## Summary

Replace the Go tray binary with the Swift tray app in the macOS installer (PKG in DMG). Build the Swift app via `xcodebuild` in CI. Windows installer continues using the Go tray app unchanged.

## Platform Matrix

| Platform | Tray App | Installer | Signing | Changes |
|----------|----------|-----------|---------|---------|
| macOS | Swift (new) | PKG in DMG | Developer ID + notarize | Build Swift app in CI, swap binary in bundle |
| Windows | Go (existing) | Inno Setup .exe | SignPath | None |
| Linux | None | tar.gz | None | None |

## macOS .app Bundle Structure (after)

```
MCPProxy.app/
└── Contents/
    ├── MacOS/
    │   └── MCPProxy              # Swift binary (from xcodebuild)
    ├── Resources/
    │   ├── bin/
    │   │   └── mcpproxy          # Go core binary
    │   ├── Assets.car            # Compiled asset catalog (icons)
    │   ├── mcpproxy.icns         # App icon
    │   └── ca.pem                # Auto-generated CA cert
    ├── Info.plist                # From Swift project
    ├── MCPProxy.entitlements     # Swift entitlements
    └── PkgInfo                   # APPLMCPP
```

## CI Workflow Changes

### New Step: Build Swift Tray App

Added to `release.yml`, `prerelease.yml`, and `pr-build.yml` for macOS jobs only:

```yaml
- name: Build Swift tray app
  if: matrix.goos == 'darwin'
  run: |
    cd native/macos/MCPProxy
    xcodebuild -scheme MCPProxy \
      -configuration Release \
      -derivedDataPath build \
      -destination 'generic/platform=macOS' \
      MACOSX_DEPLOYMENT_TARGET=13.0 \
      MARKETING_VERSION=${{ env.VERSION }} \
      CURRENT_PROJECT_VERSION=${{ env.BUILD_NUMBER }} \
      CODE_SIGN_IDENTITY="-" \
      CODE_SIGNING_ALLOWED=NO
```

Signing is disabled during build — handled by the existing dedicated signing step with Developer ID certificate.

### Modified: App Bundle Assembly (create-pkg.sh, create-dmg.sh)

Instead of copying Go tray binary to `Contents/MacOS/`:
1. Copy Swift binary from `build/Build/Products/Release/MCPProxy.app/Contents/MacOS/MCPProxy`
2. Merge Swift Resources (Assets.car, etc.) into `Contents/Resources/` without replacing Go core binary
3. Use Swift project's `Info.plist` instead of generating one at runtime
4. Use Swift entitlements file for signing

### Modified: Signing Step

- Sign Swift binary with same Developer ID cert + hardened runtime
- Use `native/macos/MCPProxy/MCPProxy/MCPProxy.entitlements` instead of `scripts/entitlements.plist`
- Sign Go core binary separately with `scripts/entitlements.plist` (it has `network.server` which Swift app doesn't need)

### Unchanged

- Notarization (same xcrun notarytool flow)
- PKG creation and signing
- DMG wrapping
- Windows Inno Setup installer
- Linux tar.gz
- SignPath Windows signing
- Release note generation

## Deployment Target

- Bumped from macOS 12.0 to macOS 13.0
- Matches Swift app's `LSMinimumSystemVersion`
- Go core also built with `MACOSX_DEPLOYMENT_TARGET=13.0`

## Version Injection

- Go core: `-ldflags "-X main.version=$VERSION"` (unchanged)
- Swift app: `MARKETING_VERSION=$VERSION` via xcodebuild argument
- Info.plist `CFBundleShortVersionString` set at build time

## Xcode Build Approach

Use `xcodebuild` directly with the Swift Package Manager package (no `.xcodeproj` needed). Xcode 15+ on macOS CI runners supports building SPM packages natively via `-scheme`.

If scheme auto-detection fails, generate with `swift package generate-xcodeproj` as fallback.

## Architecture Support

Build Swift app for both architectures:
- `arm64` (Apple Silicon) — primary
- `x86_64` (Intel) — if universal binary needed

Current approach: build per-architecture matching the Go core binary. If universal binary is desired, use `lipo -create` to merge.

## Files to Modify

| File | Change |
|------|--------|
| `.github/workflows/release.yml` | Add Swift build step, modify bundle assembly, bump deployment target |
| `.github/workflows/prerelease.yml` | Same Swift build step |
| `.github/workflows/pr-build.yml` | Same Swift build step (ad-hoc signed) |
| `scripts/create-pkg.sh` | Use Swift binary + resources, Swift Info.plist |
| `scripts/create-dmg.sh` | Same as create-pkg.sh |
| `scripts/create-app-dmg.sh` | Same (PR builds) |

## Risks

- **Xcode version on CI runner**: macOS CI runners need Xcode 15+ for SPM scheme support. GitHub Actions `macos-latest` (currently macOS 14) includes Xcode 15.
- **Build time**: Swift compilation adds ~30-60s to the macOS build job.
- **SPM dependencies**: If the Swift app adds SPM dependencies (e.g., Sparkle), CI needs network access to fetch them.

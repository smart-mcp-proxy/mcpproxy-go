# Prerelease Builds

MCPProxy supports automated prerelease builds from the `next` branch with signed and notarized macOS installers.

## Branch Strategy

- **`main` branch**: Stable releases (hotfixes and production builds)
- **`next` branch**: Prerelease builds with latest features

## Downloading Prerelease Builds

### Option 1: GitHub Web Interface

1. Go to [GitHub Actions](https://github.com/smart-mcp-proxy/mcpproxy-go/actions)
2. Click on the latest successful "Prerelease" workflow run
3. Scroll to **Artifacts** section
4. Download:
   - `dmg-darwin-arm64` (Apple Silicon Macs)
   - `dmg-darwin-amd64` (Intel Macs)
   - `versioned-linux-amd64`, `versioned-windows-amd64`, etc. (other platforms)

### Option 2: Command Line

```bash
# List recent prerelease runs
gh run list --workflow="Prerelease" --limit 5

# Download specific artifacts from a run
gh run download <RUN_ID> --name dmg-darwin-arm64    # Apple Silicon
gh run download <RUN_ID> --name dmg-darwin-amd64    # Intel Mac
gh run download <RUN_ID> --name versioned-linux-amd64  # Linux
```

## Prerelease Versioning

- Format: `{last_git_tag}-next.{commit_hash}`
- Example: `v0.8.4-next.5b63e2d`
- Version embedded in both `mcpproxy` and `mcpproxy-tray` binaries

## Release Candidate (RC) Builds

Release candidates are opt-in, fully-built prereleases published to the GitHub **pre-release** channel for testers who want to validate an upcoming version before it ships to stable.

### Version scheme

- Format: **semver** `vMAJOR.MINOR.PATCH-rc.N` — e.g. `v0.37.0-rc.1`, `v0.37.0-rc.2`.
- The `-rc.N` suffix (hyphen + dot before the number) is required. It is what keeps RC tags off the stable channels: the Homebrew, Linux-repo, docs, marketing, MCP-registry, and core build/release jobs in `release.yml` are gated on `!contains(github.ref_name, '-')`.
- Do **not** use forms like `v0.37.0.RC1` or `v0.37.0RC1` — without the hyphen they read as stable tags and would bypass those guards.

### What an RC publishes

A `v*-rc.*` tag runs **only** `prerelease.yml`, which mirrors the full stable build matrix:

| Platform | Artifacts | Signing |
|----------|-----------|---------|
| macOS (arm64, amd64) | DMG + PKG installers, tar.gz | Apple Developer ID signed **and notarized** |
| Linux (arm64, amd64) | tar.gz, `.deb`, `.rpm` | — |
| Windows (arm64, amd64) | `.zip`, installer (`.exe`) | **Not** SignPath-signed (see note below) |

The GitHub release is created with `prerelease: true`, so it does **not** become `releases/latest`.

> **Windows signing:** stable releases are Authenticode-signed via a dedicated SignPath job (`sign-windows`) in `release.yml`. `prerelease.yml` intentionally omits that step (SignPath signing adds ~1h per arch), so **RC Windows installers are unsigned** and will trigger a SmartScreen prompt. If signed Windows RCs become a requirement, port the `sign-windows` job into `prerelease.yml`.

### What an RC does NOT do

- Not published to **Homebrew** (`update-homebrew` guarded).
- Not published to the **Linux apt/rpm repos** (`publish-linux-repos` guarded).
- Not published to the **official MCP registry** (`mcp-registry` guarded).
- Does not deploy docs or trigger marketing automation (`deploy-docs`, `trigger-marketing-update` guarded).
- Not offered as an update on **stable channels**:
  - The macOS tray uses GitHub `releases/latest`, which excludes prereleases (`native/macos/MCPProxy/MCPProxy/Services/UpdateService.swift`), plus a semver downgrade guard so an `-rc` is never treated as "newer" than the matching stable.
  - The backend/tray update check is stable-only by default (`internal/tray/tray.go` → `releases/latest`). Set `MCPPROXY_ALLOW_PRERELEASE_UPDATES=true` to opt in to RC update offers.

### Installing an RC

Download the assets directly from the pre-release on the [Releases page](https://github.com/smart-mcp-proxy/mcpproxy-go/releases), or with the CLI:

```bash
gh release list --repo smart-mcp-proxy/mcpproxy-go        # pre-releases are tagged "Pre-release"
gh release download v0.37.0-rc.1 --repo smart-mcp-proxy/mcpproxy-go
```

## Security Features

- **macOS DMG installers**: Signed with Apple Developer ID and notarized
- **Code signing**: All macOS binaries are signed for Gatekeeper compatibility
- **Automatic quarantine protection**: New servers are quarantined by default

## GitHub Workflows

- **Prerelease workflow** (`prerelease.yml`): Triggered on `next` branch pushes and on `v*-rc.*` / `v*-next.*` tags. Publishes a GitHub pre-release.
- **Release workflow** (`release.yml`): Triggered on `v*` tags, but every job is gated on `!contains(github.ref_name, '-')` so RC/prerelease tags are skipped — a `v*-rc.*` tag therefore fires **only** `prerelease.yml`, never the stable release pipeline.
- **Unit Tests**: Run on all branches with comprehensive test coverage
- **Frontend CI**: Validates web UI components and build process

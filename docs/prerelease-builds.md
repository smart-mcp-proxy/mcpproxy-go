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

## Security Features

- **macOS DMG installers**: Signed with Apple Developer ID and notarized
- **Code signing**: All macOS binaries are signed for Gatekeeper compatibility
- **Automatic quarantine protection**: New servers are quarantined by default

## GitHub Workflows

- **Prerelease workflow**: Triggered on `next` branch pushes
- **Release workflow**: Triggered on `main` branch tags
- **Unit Tests**: Run on all branches with comprehensive test coverage
- **Frontend CI**: Validates web UI components and build process

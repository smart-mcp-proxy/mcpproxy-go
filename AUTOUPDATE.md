# Auto-Update & Release System

This document describes the auto-update functionality and release automation system implemented for mcpproxy.

## Update Modes

### 🔄 Auto-Update (Default - Recommended)

- **Automatic Updates**: Downloads and applies updates automatically
- **Daily Checks**: Background checks every 24 hours
- **Manual Checks**: "Check for Updates..." in system tray
- **Smart Detection**: Avoids conflicts with package managers

```bash
# Default mode - auto-update enabled
./mcpproxy serve --tray=true
```

### 🚫 Disabled Mode

- **No Updates**: Completely disables update checking
- **Manual Only**: User must update manually

```bash
# Disable auto-update completely
export MCPPROXY_DISABLE_AUTO_UPDATE=true
./mcpproxy serve --tray=true

# Or add to shell profile for permanent setting
echo 'export MCPPROXY_DISABLE_AUTO_UPDATE=true' >> ~/.zshrc
```

### 🔔 Notification-Only Mode

- **Check Only**: Checks for updates but doesn't download
- **User Choice**: Notifies user, lets them decide
- **Log Messages**: Shows update info in logs

```bash
# Notification-only mode
export MCPPROXY_UPDATE_NOTIFY_ONLY=true
./mcpproxy serve --tray=true
```

### 🧪 Prerelease/Canary Mode

- **Prerelease Updates**: Allows updates to RC and development versions
- **Latest Available**: Gets the newest version regardless of prerelease status
- **For Testing**: Ideal for early adopters and testing new features

```bash
# Enable prerelease updates (canary behavior)
export MCPPROXY_ALLOW_PRERELEASE_UPDATES=true
./mcpproxy serve --tray=true

# Or add to shell profile for permanent setting
echo 'export MCPPROXY_ALLOW_PRERELEASE_UPDATES=true' >> ~/.zshrc
```

## Package Manager Integration

### 🍺 Homebrew (macOS)

Auto-update is **automatically disabled** when installed via Homebrew to prevent conflicts:

```bash
# Homebrew installation - auto-update disabled automatically
brew install smart-mcp-proxy/tap/mcpproxy

# Use Homebrew for updates
brew upgrade mcpproxy
```

### 📦 Other Package Managers

The system detects common package manager paths and disables auto-update accordingly.

## Technical Details

### Update Process Flow

```
1. Check for Updates (Daily timer or manual click)
2. Query GitHub API for latest release
3. Compare versions using semantic versioning
4. Detect installation type (standalone vs package manager)
5. Download appropriate archive for OS/architecture
6. Extract binary from archive
7. Replace current executable atomically
8. Restart application
```

### File Format Support

- **Windows**: `.zip` archives
- **macOS**: `.tar.gz` archives  
- **Linux**: `.tar.gz` archives

### Asset Detection Priority

1. **Latest Assets**: `mcpproxy-latest-{os}-{arch}.{ext}`
2. **Versioned Assets**: `mcpproxy-{version}-{os}-{arch}.{ext}`

### Security Features

- **HTTPS Only**: All downloads use HTTPS
- **Atomic Updates**: Safe binary replacement with rollback
- **Version Validation**: Semantic version checking prevents downgrades
- **Package Manager Detection**: Prevents conflicts with system package managers

## Configuration

### Environment Variables

| Variable | Values | Description |
|----------|---------|-------------|
| `MCPPROXY_DISABLE_AUTO_UPDATE` | `true`/`false` | Completely disable auto-update |
| `MCPPROXY_UPDATE_NOTIFY_ONLY` | `true`/`false` | Check for updates but don't download |
| `MCPPROXY_ALLOW_PRERELEASE_UPDATES` | `true`/`false` | Allow auto-updates to prerelease versions (default: false) |

### System Tray Menu

```
mcpproxy
├── Status: Running (localhost:8080)
├── ─────────────────────────────
├── Start/Stop Server
├── ─────────────────────────────  
├── Check for Updates...          ← Manual update check
│   ├── Auto-update: Enabled      ← Shows current mode
│   └── [Disabled for Homebrew]   ← If package manager detected
├── ─────────────────────────────
└── Quit
```

## Troubleshooting

### Common Issues

#### ❌ "no suitable asset found for darwin-arm64.zip"
**Fixed**: Now looks for correct `.tar.gz` files on macOS/Linux

#### ⚠️ "auto-update disabled for Homebrew installations"
**Expected**: Prevents conflicts with `brew upgrade`

#### 🔄 Updates Not Working
1. Check network connectivity
2. Verify GitHub access: `curl -I https://api.github.com/repos/smart-mcp-proxy/mcpproxy-go/releases/latest`
3. Check logs for specific errors
4. Try notification-only mode to debug

### Manual Update Methods

#### Standalone Installation
```bash
# Download from GitHub releases
curl -L -o mcpproxy.tar.gz https://github.com/smart-mcp-proxy/mcpproxy-go/releases/latest/download/mcpproxy-latest-darwin-arm64.tar.gz
tar -xzf mcpproxy.tar.gz
chmod +x mcpproxy
./mcpproxy --version
```

#### Homebrew
```bash
brew upgrade mcpproxy
```

#### Go Install (Development)
```bash
go install github.com/smart-mcp-proxy/mcpproxy-go/cmd/mcpproxy@latest
```

## Best Practices

### For End Users
- **Use Default Mode**: Auto-update provides the best experience
- **Homebrew Users**: Let Homebrew handle updates
- **Corporate/Restricted**: Use notification-only mode

### For Developers
- **Test Updates**: Use development builds before releases
- **Version Tags**: Follow semantic versioning (v1.0.0)
- **Asset Naming**: Maintain consistent asset naming conventions

### For System Administrators
- **Disable Auto-Update**: Use `MCPPROXY_DISABLE_AUTO_UPDATE=true` for controlled environments
- **Monitor Logs**: Watch for update notifications and errors
- **Network Access**: Ensure GitHub API access for update checks

## Release Process

### Creating a Release
```bash
# Tag a new version
git tag v1.0.0
git push origin v1.0.0

# GitHub Actions will:
# 1. Build cross-platform binaries
# 2. Create both versioned and latest assets
# 3. Create GitHub release with download links
```

### Asset Structure
Each release includes:
- `mcpproxy-v1.0.0-linux-amd64.tar.gz` (versioned)
- `mcpproxy-latest-linux-amd64.tar.gz` (latest)
- Similar files for all supported platforms

The auto-updater prioritizes "latest" assets for consistency with website download links. 
---
id: version-updates
title: Version Updates
sidebar_label: Version Updates
sidebar_position: 6
description: How MCPProxy checks for and displays version updates
keywords: [updates, version, upgrade, notifications]
---

# Version Updates

MCPProxy includes built-in update checking to help you stay current with the latest features and security fixes.

## How It Works

MCPProxy automatically checks for new versions by querying GitHub Releases:

1. **Background checking**: The core server checks for updates on startup and every 4 hours thereafter
2. **Non-blocking**: Update checks run in the background and don't affect proxy performance
3. **Graceful degradation**: If the check fails (e.g., no internet), MCPProxy continues working normally

## Where Updates Are Displayed

### System Tray

When an update is available, a new menu item appears in the tray menu:

- **"New version available (vX.Y.Z)"** - Click to open the GitHub releases page
- For Homebrew users: **"Update available: vX.Y.Z (use brew upgrade)"**

The menu item only appears when an update is detected - no menu clutter when you're up to date.

### Web Control Panel

The sidebar displays the current version at the bottom. When an update is available:

- A small "update available" badge appears next to the version
- Click to view the release notes

### CLI Doctor Command

The `mcpproxy doctor` command shows version information:

```bash
$ mcpproxy doctor
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
🔍 MCPProxy Health Check
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
Version: v1.2.3 (update available: v1.3.0)
Download: https://github.com/smart-mcp-proxy/mcpproxy-go/releases/tag/v1.3.0

✅ All systems operational! No issues detected.
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
```

## Configuration

### Environment Variables

| Variable | Description | Default |
|----------|-------------|---------|
| `MCPPROXY_DISABLE_AUTO_UPDATE` | Disable background update checks entirely | `false` |
| `MCPPROXY_ALLOW_PRERELEASE_UPDATES` | Include prerelease/beta versions in update checks | `false` |

### Examples

```bash
# Disable update checking
MCPPROXY_DISABLE_AUTO_UPDATE=true mcpproxy serve

# Enable prerelease updates (for beta testers)
MCPPROXY_ALLOW_PRERELEASE_UPDATES=true mcpproxy serve
```

## API Endpoint

Update information is available via the REST API:

```bash
curl -H "X-API-Key: your-key" http://127.0.0.1:8080/api/v1/info
```

Response includes an `update` field when version information is available:

```json
{
  "success": true,
  "data": {
    "version": "v1.2.3",
    "update": {
      "available": true,
      "latest_version": "v1.3.0",
      "release_url": "https://github.com/smart-mcp-proxy/mcpproxy-go/releases/tag/v1.3.0",
      "checked_at": "2025-01-15T10:30:00Z",
      "is_prerelease": false
    }
  }
}
```

See [REST API Documentation](../api/rest-api.md#get-apiv1info) for complete details.

## Updating MCPProxy

### Homebrew (macOS/Linux)

```bash
brew upgrade mcpproxy
```

### Manual Download

1. Click the update notification in the tray menu or web UI
2. Download the appropriate binary for your platform
3. Replace the existing binary and restart MCPProxy

### Windows Installer

Download the latest `.msi` installer from GitHub Releases and run it. The installer will upgrade your existing installation.

## Development Builds

When running a development build (version shows as "development"), update checking is automatically disabled since there's no meaningful version to compare against.

## Troubleshooting

### Update check not working

1. Ensure you have internet connectivity
2. Check if `MCPPROXY_DISABLE_AUTO_UPDATE` is set
3. Run `mcpproxy doctor` to see current version status
4. Check logs for any GitHub API errors:
   ```bash
   tail -f ~/.mcpproxy/logs/main.log | grep -i update
   ```

### Prerelease not showing

By default, prerelease versions are excluded. To enable:

```bash
export MCPPROXY_ALLOW_PRERELEASE_UPDATES=true
```

### Behind corporate firewall

MCPProxy checks updates via `https://api.github.com`. Ensure this domain is accessible from your network.

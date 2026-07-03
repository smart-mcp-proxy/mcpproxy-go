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

The dashboard additionally shows a **dismissible update banner** ("Update
available: vX — you are running vY") with a release-notes link. Dismissal is
**per version**: dismissing the banner for v1.3.0 keeps it hidden for v1.3.0
(persisted in the browser), but the banner reappears when a newer release
becomes available. The banner is non-modal and never blocks the UI.

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

### Config File (`update_check`)

Update checking is controlled from `mcp_config.json` via the `update_check`
block:

```json
{
  "update_check": {
    "enabled": true,
    "channel": "stable"
  }
}
```

| Option | Type | Default | Description |
|--------|------|---------|-------------|
| `enabled` | boolean | `true` | Master switch. When `false`, no network check is performed (background poll and the manual re-check) and no upgrade nudge appears on any surface — the `update` object is omitted from `/api/v1/info`. |
| `channel` | string | `"stable"` | Release channel: `"stable"` (GitHub `releases/latest`; prereleases never offered) or `"rc"` (prerelease tags such as `v0.47.0-rc.1` included). |

Both keys are **hot-reloadable**: editing the config file or applying it via
`POST /api/v1/config/apply` takes effect without a restart. Re-enabling (or
switching channels) triggers a prompt re-check instead of waiting for the next
4-hour tick.

### Environment Variables

| Variable | Description | Default |
|----------|-------------|---------|
| `MCPPROXY_DISABLE_AUTO_UPDATE` | Disable background update checks entirely | `false` |
| `MCPPROXY_ALLOW_PRERELEASE_UPDATES` | Include prerelease/beta versions in update checks | `false` |

### Precedence (env vs config)

The environment switches **win over** the `update_check` config keys — they
are the operator override:

- `MCPPROXY_DISABLE_AUTO_UPDATE=true` disables checking even when
  `update_check.enabled` is `true`.
- `MCPPROXY_ALLOW_PRERELEASE_UPDATES=true` selects the prerelease channel even
  when `update_check.channel` is `"stable"`.

The env vars only widen in one direction (disable checks / include
prereleases). They cannot re-enable checking that the config disabled: with
`update_check.enabled: false`, no check runs regardless of environment.

### Examples

```bash
# Disable update checking (config file — persistent, hot-reloads)
#   "update_check": { "enabled": false }

# Disable update checking (environment — wins over config)
MCPPROXY_DISABLE_AUTO_UPDATE=true mcpproxy serve

# Opt in to prerelease (RC) updates via config
#   "update_check": { "channel": "rc" }

# Enable prerelease updates via environment (for beta testers)
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
2. Check if `MCPPROXY_DISABLE_AUTO_UPDATE` is set, or `update_check.enabled` is `false` in `mcp_config.json`
3. Run `mcpproxy doctor` to see current version status
4. Check logs for any GitHub API errors:
   ```bash
   tail -f ~/.mcpproxy/logs/main.log | grep -i update
   ```

### Prerelease not showing

By default, prerelease versions are excluded. To enable, set
`"update_check": { "channel": "rc" }` in `mcp_config.json`, or:

```bash
export MCPPROXY_ALLOW_PRERELEASE_UPDATES=true
```

### Behind corporate firewall

MCPProxy checks updates via `https://api.github.com`. Ensure this domain is accessible from your network.

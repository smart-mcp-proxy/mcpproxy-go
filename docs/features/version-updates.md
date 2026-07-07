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

`enabled: false` also gates the Go tray's built-in daily self-update check, so
no surface performs a network check while disabled. The tray does **not** read
`mcp_config.json` itself (it holds no state); instead it asks the core via
`GET /api/v1/info` before checking — the core omits the `update` object when
update checking is disabled, and the tray then skips its own network check. If
the core is unreachable the tray skips that tick and retries, rather than
falling open to a check the operator may have disabled. The tray's own check
still selects prereleases via `MCPPROXY_ALLOW_PRERELEASE_UPDATES` only —
converging it fully onto the shared checker (including `channel`) is a separate
Spec 079 work item (FR-001a).

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
      "is_prerelease": false,
      "install_channel": "homebrew",
      "update_command": "brew upgrade mcpproxy"
    }
  }
}
```

See [REST API Documentation](../api/rest-api.md#get-apiv1info) for complete details.

## How the Install Channel Is Detected

MCPProxy identifies how it was installed so it can show the right update
instruction for your setup (`install_channel` in `/api/v1/info`, plus the
guided command in `mcpproxy status`, `mcpproxy doctor`, and the Web UI
banner).

Detection prefers a **build-time channel marker** stamped into
single-channel artifacts at packaging time (the Docker image and the Windows
installer). When no marker is present — the release archives feed the
tarball, Homebrew, and DMG channels from one binary — runtime heuristics run
in decreasing confidence order:

1. **Homebrew**: the (symlink-resolved) executable path lives under a
   Homebrew prefix (`/opt/homebrew/`, a `Cellar/` path, or
   `/home/linuxbrew/.linuxbrew`).
2. **Docker**: `/.dockerenv` exists.
3. **deb / rpm**: on Linux, the executable is exactly `/usr/bin/mcpproxy`
   **and** the owning package manager confirms it owns the file
   (`/var/lib/dpkg/info/mcpproxy.list` for deb; for rpm, an rpm database
   exists **and** a one-shot `rpm -qf /usr/bin/mcpproxy` query names the
   `mcpproxy` package). Both signals are required — a binary merely copied to
   `/usr/bin` (e.g. an AUR or manual install), even on an RPM-based distro,
   stays `unknown`.
4. **DMG**: on macOS, the executable runs from an `.app/Contents/MacOS` or
   `.app/Contents/Resources/bin` bundle path, or is the tray-staged core at
   `~/Library/Application Support/mcpproxy/bin/mcpproxy` (the process that
   actually serves the API for DMG installs; only the tray's bundle-staging
   writes that directory).
5. **go install**: the Go toolchain stamped a real module version into the
   binary's build info while no release version was stamped via ldflags.
6. Otherwise the channel is **`unknown`**.

Ambiguity always resolves to `unknown`: MCPProxy never guesses a channel,
because a wrong update command is worse than a generic instruction.

### Update Commands per Channel

| Channel | `update_command` | Guidance shown instead |
|---------|------------------|------------------------|
| `homebrew` | `brew upgrade mcpproxy` | — |
| `deb` | `sudo apt update && sudo apt install --only-upgrade mcpproxy` | — |
| `rpm` | `sudo dnf upgrade mcpproxy` | — |
| `go-install` | `go install github.com/smart-mcp-proxy/mcpproxy-go/cmd/mcpproxy@latest` | — |
| `dmg` | — | Download the latest DMG (release page is deep-linked) |
| `windows-installer` | — | Download the latest Windows installer |
| `docker` | — | Pull or rebuild the newer image for your deployment |
| `tarball` / `unknown` | — | Download the latest release from the releases page |

Every surface always deep-links the release notes for the latest version,
whether or not a command is available.

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

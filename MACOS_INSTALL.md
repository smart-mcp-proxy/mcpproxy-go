# macOS Installation & Auto-Update Guide

This guide covers professional macOS installation, Gatekeeper bypass, and auto-update functionality for mcpproxy.

## 🚀 Quick Installation

### Option 1: DMG Installation (Recommended)

1. **Download** the latest `mcpproxy-vX.X.X.dmg` from [Releases](https://github.com/smart-mcp-proxy/mcpproxy-go/releases)
2. **Mount** the DMG by double-clicking
3. **Drag** `mcpproxy` to the `Applications` folder
4. **First Launch**: Right-click `mcpproxy` in Applications → **Open** → **Open** (bypasses Gatekeeper)
5. **System Tray** will appear with mcpproxy icon

### Option 2: Homebrew

```bash
# Add our tap
brew tap smart-mcp-proxy/tap

# Install mcpproxy
brew install mcpproxy

# First launch (bypasses Gatekeeper automatically)
mcpproxy --tray=true
```

## 🔒 Gatekeeper & Security

### Why the Security Warning?

mcpproxy is **not notarized** (requires paid Apple Developer account). macOS Gatekeeper will show:

> **"mcpproxy cannot be opened because it is from an unidentified developer"**

### Bypass Methods

#### Method 1: Right-Click Open (Recommended)
1. Right-click `mcpproxy` in Applications
2. Select **Open**
3. Click **Open** in the security dialog
4. ✅ **Gatekeeper stores exception** - future launches are silent

#### Method 2: System Preferences
1. Go to **System Preferences** → **Security & Privacy**
2. Click **Open Anyway** next to the mcpproxy warning
3. Confirm in the dialog

#### Method 3: Command Line
```bash
# Remove quarantine attribute
sudo xattr -dr com.apple.quarantine /Applications/mcpproxy

# Or for Homebrew installation
sudo xattr -dr com.apple.quarantine $(which mcpproxy)
```

## 🔄 Auto-Update System

mcpproxy includes **built-in auto-update** functionality using GitHub releases.

### How It Works

1. **Daily Checks**: Automatically checks for updates every 24 hours
2. **Manual Check**: System tray → **Check for Updates...**
3. **Semantic Versioning**: Only updates to newer versions
4. **Atomic Updates**: Safe binary replacement with rollback
5. **Auto-Restart**: Application restarts after successful update

### Update Process

```
1. Query GitHub API for latest release
2. Compare versions (current vs latest)
3. Download appropriate binary for macOS
4. Apply update atomically
5. Restart application (via LaunchAgent)
```

### Update Channels

- **Stable**: Tagged releases (v1.0.0, v1.1.0, etc.)
- **Development**: Use `go install` for latest commits

## 🚀 Auto-Start on Login

### Option 1: System Tray Auto-Start (Recommended)

The application can install itself as a LaunchAgent:

```bash
# Start with tray enabled
mcpproxy --tray=true

# The app will offer to install LaunchAgent on first run
```

### Option 2: Manual LaunchAgent Installation

```bash
# Copy the LaunchAgent plist
cp scripts/com.smartmcpproxy.mcpproxy.plist ~/Library/LaunchAgents/

# Load the LaunchAgent
launchctl load ~/Library/LaunchAgents/com.smartmcpproxy.mcpproxy.plist

# Start immediately
launchctl start com.smartmcpproxy.mcpproxy
```

### Option 3: Login Items (macOS 13+)

1. **System Preferences** → **General** → **Login Items**
2. Click **+** and add `mcpproxy` from Applications
3. Enable **Hide** for clean startup

## 🛠 Configuration

### Default Config Location

```bash
# User-specific config
~/Library/Application Support/mcpproxy/config.json

# System-wide config (if needed)
/etc/mcpproxy/config.json
```

### Environment Variables

```bash
# Custom config path
export MCPPROXY_CONFIG_PATH="/path/to/config.json"

# Data directory
export MCPPROXY_DATA_DIR="~/Library/Application Support/mcpproxy"

# Enable debug logging
export MCPPROXY_LOG_LEVEL="debug"
```

## 🎯 System Tray Features

### Menu Structure

```
mcpproxy
├── Status: Running (localhost:8080)     ← Real-time status
├── ─────────────────────────────
├── Start/Stop Server                   ← Server control
├── ─────────────────────────────
├── Upstream Servers (2/3)              ← Connection monitoring
│   └── [Hover for details]            ← Server-specific status
├── ─────────────────────────────
├── Check for Updates...                ← Manual update check
├── Open Config                         ← Quick config access
├── ─────────────────────────────
└── Quit                               ← Clean shutdown
```

### Status Indicators

- **🟢 Running**: Server active, accepting connections
- **🟡 Starting**: Server initializing
- **🔴 Stopped**: Server not running
- **⚠️ Error**: Server error (check logs)

## 🔧 Troubleshooting

### Common Issues

#### 1. "App is damaged and cannot be opened"

**Cause**: Gatekeeper quarantine not properly removed

**Solution**:
```bash
sudo xattr -dr com.apple.quarantine /Applications/mcpproxy
```

#### 2. System Tray Icon Not Appearing

**Cause**: macOS tray area hidden or conflicting apps

**Solutions**:
- Check **Control Center** → **Menu Bar Only** items
- Restart **SystemUIServer**: `killall SystemUIServer`
- Try different tray apps to test system tray functionality

#### 3. Auto-Update Fails

**Cause**: Network restrictions or GitHub API limits

**Solutions**:
- Check network connectivity
- Verify GitHub access: `curl -I https://api.github.com`
- Check logs: `tail -f /tmp/mcpproxy.err`

#### 4. LaunchAgent Not Starting

**Cause**: Incorrect plist or permissions

**Solutions**:
```bash
# Check LaunchAgent status
launchctl list | grep mcpproxy

# Reload LaunchAgent
launchctl unload ~/Library/LaunchAgents/com.smartmcpproxy.mcpproxy.plist
launchctl load ~/Library/LaunchAgents/com.smartmcpproxy.mcpproxy.plist

# Check logs
tail -f /tmp/mcpproxy.out /tmp/mcpproxy.err
```

### Debug Mode

```bash
# Run with debug logging
mcpproxy --tray=true --log-level=debug

# Check system logs
log show --predicate 'subsystem == "com.smartmcpproxy.mcpproxy"' --last 1h
```

## 🔐 Security Considerations

### Network Security

- **HTTPS Only**: All GitHub API calls use HTTPS
- **Certificate Validation**: Full TLS certificate chain validation
- **No Telemetry**: No data collection or phone-home behavior

### Update Security

- **Checksum Verification**: All downloads verified against checksums
- **Atomic Updates**: Binary replacement is atomic with rollback
- **Version Validation**: Semantic versioning prevents downgrade attacks
- **GitHub Trust**: Updates only from official GitHub releases

### Privacy

- **Local Only**: All MCP proxy functionality is local
- **No Analytics**: No usage tracking or data collection
- **Config Privacy**: Configuration stays on your machine

## 📋 Uninstallation

### Complete Removal

```bash
# Stop LaunchAgent
launchctl unload ~/Library/LaunchAgents/com.smartmcpproxy.mcpproxy.plist

# Remove LaunchAgent
rm ~/Library/LaunchAgents/com.smartmcpproxy.mcpproxy.plist

# Remove application
rm -rf /Applications/mcpproxy

# Remove user data (optional)
rm -rf ~/Library/Application\ Support/mcpproxy

# Remove logs
rm -f /tmp/mcpproxy.out /tmp/mcpproxy.err
```

### Homebrew Removal

```bash
brew uninstall mcpproxy
brew untap smart-mcp-proxy/tap
```

## 🎯 Advanced Usage

### Custom Launch Arguments

```bash
# Headless mode (no tray)
mcpproxy --tray=false --port=8080

# Custom config
mcpproxy --config=/path/to/config.json

# Debug mode
mcpproxy --log-level=debug --tray=true
```

### Integration with Other Apps

#### Alfred Workflow
Create an Alfred workflow to control mcpproxy:

```bash
# Start server
/Applications/mcpproxy --tray=false --daemon

# Stop server
pkill -f mcpproxy
```

#### Raycast Extension
Add mcpproxy control to Raycast for quick access.

## 📞 Support

- **Issues**: [GitHub Issues](https://github.com/smart-mcp-proxy/mcpproxy-go/issues)
- **Discussions**: [GitHub Discussions](https://github.com/smart-mcp-proxy/mcpproxy-go/discussions)
- **Documentation**: [README](README.md)

---

**Note**: This is an unsigned application. While safe to use, always download from official sources and verify checksums for security. 
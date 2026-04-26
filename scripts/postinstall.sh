#!/bin/bash
# Post-installation script for mcpproxy PKG installer

set -e

# Function to log messages
log() {
    echo "$(date '+%Y-%m-%d %H:%M:%S') - $1"
}

log "Starting mcpproxy post-installation setup..."

# 1. Create CLI symlink in /usr/local/bin (works without password)
log "Setting up mcpproxy CLI..."

# Ensure /usr/local/bin exists
mkdir -p /usr/local/bin

# Create symlink for CLI tool
CLI_SOURCE="/Applications/mcpproxy.app/Contents/Resources/bin/mcpproxy"
CLI_TARGET="/usr/local/bin/mcpproxy"

if [ -f "$CLI_SOURCE" ]; then
    # Remove existing symlink if it exists
    if [ -L "$CLI_TARGET" ]; then
        rm "$CLI_TARGET"
    fi

    # Create new symlink
    ln -sf "$CLI_SOURCE" "$CLI_TARGET"
    chmod 755 "$CLI_TARGET"
    log "✅ CLI symlink created: $CLI_TARGET"
else
    log "⚠️  CLI binary not found at: $CLI_SOURCE"
fi

# 2. Prepare certificate directory for users
log "Preparing certificate directories..."

# Create certificates directory in app bundle for bundled CA cert
BUNDLE_CERT_DIR="/Applications/mcpproxy.app/Contents/Resources/certs"
mkdir -p "$BUNDLE_CERT_DIR"

# Set proper permissions
chmod 755 "$BUNDLE_CERT_DIR"

# Copy bundled CA certificate if it exists
BUNDLED_CA="/Applications/mcpproxy.app/Contents/Resources/ca.pem"
if [ -f "$BUNDLED_CA" ]; then
    cp "$BUNDLED_CA" "$BUNDLE_CERT_DIR/ca.pem"
    chmod 644 "$BUNDLE_CERT_DIR/ca.pem"
    log "✅ Bundled CA certificate available"
fi

# 3. Create user config directories (for current user and future users)
# Note: $USER might not be set in PKG context, so we handle this gracefully
if [ -n "$USER" ] && [ "$USER" != "root" ]; then
    USER_HOME=$(eval echo "~$USER")
    if [ -d "$USER_HOME" ]; then
        USER_CONFIG_DIR="$USER_HOME/.mcpproxy"
        USER_CERT_DIR="$USER_CONFIG_DIR/certs"

        # Create user directories
        mkdir -p "$USER_CERT_DIR"

        # Copy CA certificate to user directory if bundled version exists
        if [ -f "$BUNDLE_CERT_DIR/ca.pem" ]; then
            cp "$BUNDLE_CERT_DIR/ca.pem" "$USER_CERT_DIR/ca.pem"
        fi

        # Set proper ownership and permissions
        chown -R "$USER:staff" "$USER_CONFIG_DIR" 2>/dev/null || true
        chmod -R 755 "$USER_CONFIG_DIR" 2>/dev/null || true
        chmod 644 "$USER_CERT_DIR"/*.pem 2>/dev/null || true

        log "✅ User configuration directory created: $USER_CONFIG_DIR"
    fi
fi

# 4. Auto-start configuration
#
# Auto-start (Launch at Login) is now managed by the macOS tray app via
# `SMAppService.mainApp` (see native/macos/MCPProxy/MCPProxy/Services/AutoStartService.swift).
# Users opt in via the first-run dialog or the tray menu's "Launch at Login"
# toggle — this is the per-user, sandbox-friendly mechanism Apple recommends
# for GUI tray apps on macOS 13+.
#
# Earlier installer versions wrote a system LaunchAgent at
# /Library/LaunchAgents/com.smartmcpproxy.mcpproxy.plist that was labeled
# "auto-start" but had RunAtLoad=false and KeepAlive=false (issue #405),
# so it never actually started anything. Clean it up if present so users
# upgrading from those builds don't have a stale, misleading plist on disk.
LEGACY_LAUNCH_AGENT="/Library/LaunchAgents/com.smartmcpproxy.mcpproxy.plist"
if [ -f "$LEGACY_LAUNCH_AGENT" ]; then
    log "Removing legacy LaunchAgent left by older installer: $LEGACY_LAUNCH_AGENT"
    launchctl unload -w "$LEGACY_LAUNCH_AGENT" 2>/dev/null || true
    rm -f "$LEGACY_LAUNCH_AGENT"
    log "✅ Legacy LaunchAgent removed (auto-start is now via the tray app's Launch-at-Login toggle)"
fi

# 5. Linux systemd service (if on Linux)
if command -v systemctl >/dev/null 2>&1; then
    log "Detected systemd, setting up user service..."
    log "To enable mcpproxy to start automatically, run:"
    log "  systemctl --user enable mcpproxy"
    log "  systemctl --user start mcpproxy"
fi

# 6. Display installation summary
log "🎉 mcpproxy installation complete!"
echo ""
echo "📋 Installation Summary:"
echo "  • CLI tool available: type 'mcpproxy' in Terminal"
echo "  • GUI app installed: /Applications/mcpproxy.app"
echo "  • Default mode: HTTP (works immediately)"
echo ""
echo "🔧 Optional HTTPS Setup:"
echo "  1. Trust certificate: mcpproxy trust-cert"
echo "  2. Enable HTTPS: export MCPPROXY_TLS_ENABLED=true"
echo "  3. Start server: mcpproxy serve"
echo ""
echo "🌐 For Claude Desktop with HTTPS:"
echo "  Add to claude_desktop_config.json:"
echo '  "env": {'
echo '    "NODE_EXTRA_CA_CERTS": "~/.mcpproxy/certs/ca.pem"'
echo '  }'
echo ""
echo "📖 Get started: mcpproxy --help"

# Spec 044 (T057/T058): launch the tray app tagged as installer-launched so
# the first telemetry heartbeat reports launch_source=installer. The flag is
# one-shot (cleared after the first heartbeat) and lives in the BBolt
# activation bucket, so a crash between install and heartbeat is recovered
# from on next startup. See packaging/macos/postinstall.sh for the standalone
# version of this step.
#
# Critical: launch via `launchctl asuser <uid> env -i …` so the tray app
# (and its core child) start in the user's GUI bootstrap context with a
# clean env. A bare `open -a` from a postinstall script propagates the
# PKInstallSandbox env (minimal PATH, SHELL=/bin/sh, INSTALLER_*) into the
# long-running daemon — which broke Docker discovery in mcpproxy core. The
# clean-env launch mimics what users get when they double-click from Finder.
APP_BUNDLE=""
if [ -d "/Applications/MCPProxy.app" ]; then
    APP_BUNDLE="/Applications/MCPProxy.app"
elif [ -d "/Applications/mcpproxy.app" ]; then
    # Some build paths produce the lowercase-named bundle.
    APP_BUNDLE="/Applications/mcpproxy.app"
fi

if [ -n "$APP_BUNDLE" ]; then
    log "Launching mcpproxy tray (installer-tagged) with clean env: $APP_BUNDLE"

    REAL_USER="${USER:-}"
    if [ -z "$REAL_USER" ] || [ "$REAL_USER" = "root" ]; then
        REAL_USER=$(stat -f%Su /dev/console 2>/dev/null || echo "")
    fi
    REAL_UID=""
    USER_HOME=""
    if [ -n "$REAL_USER" ]; then
        REAL_UID=$(id -u "$REAL_USER" 2>/dev/null || echo "")
        USER_HOME=$(/usr/bin/dscl . -read "/Users/$REAL_USER" NFSHomeDirectory 2>/dev/null | awk '{print $2}')
        [ -z "$USER_HOME" ] && USER_HOME=$(eval echo "~$REAL_USER")
    fi

    SANE_PATH="/usr/local/bin:/opt/homebrew/bin:/usr/bin:/bin:/usr/sbin:/sbin"

    if [ -n "$REAL_UID" ] && [ "$REAL_UID" != "0" ]; then
        /bin/launchctl asuser "$REAL_UID" /usr/bin/env -i \
            HOME="$USER_HOME" \
            USER="$REAL_USER" \
            LOGNAME="$REAL_USER" \
            PATH="$SANE_PATH" \
            /usr/bin/open -a "$APP_BUNDLE" --env MCPPROXY_LAUNCHED_BY=installer || true
    else
        /usr/bin/env -i \
            HOME="${USER_HOME:-/var/empty}" \
            PATH="$SANE_PATH" \
            /usr/bin/open -a "$APP_BUNDLE" --env MCPPROXY_LAUNCHED_BY=installer || true
    fi
fi

exit 0

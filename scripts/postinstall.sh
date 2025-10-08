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

# 4. Create LaunchAgent for auto-start (optional)
LAUNCH_AGENT_DIR="/Library/LaunchAgents"
LAUNCH_AGENT_FILE="$LAUNCH_AGENT_DIR/com.smartmcpproxy.mcpproxy.plist"

mkdir -p "$LAUNCH_AGENT_DIR"

# Create LaunchAgent plist file
cat > "$LAUNCH_AGENT_FILE" << 'EOF'
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>com.smartmcpproxy.mcpproxy</string>
    <key>ProgramArguments</key>
    <array>
        <string>/Applications/mcpproxy.app/Contents/MacOS/mcpproxy</string>
    </array>
    <key>RunAtLoad</key>
    <false/>
    <key>KeepAlive</key>
    <false/>
    <key>StandardOutPath</key>
    <string>/tmp/mcpproxy.log</string>
    <key>StandardErrorPath</key>
    <string>/tmp/mcpproxy.error</string>
</dict>
</plist>
EOF

chmod 644 "$LAUNCH_AGENT_FILE"
log "✅ LaunchAgent installed (disabled by default)"

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

exit 0 
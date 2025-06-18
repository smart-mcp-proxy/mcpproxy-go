#!/bin/bash
# Post-installation script for mcpproxy

set -e

# Enable systemd user service (if systemd is available)
if command -v systemctl >/dev/null 2>&1; then
    echo "Enabling mcpproxy systemd user service..."
    # Note: This will be done per-user when they first run the service
    echo "To enable mcpproxy to start automatically, run:"
    echo "  systemctl --user enable mcpproxy@\$USER"
    echo "  systemctl --user start mcpproxy@\$USER"
fi

# Create default config directory
if [ -n "$HOME" ] && [ -d "$HOME" ]; then
    CONFIG_DIR="$HOME/.mcpproxy"
    if [ ! -d "$CONFIG_DIR" ]; then
        mkdir -p "$CONFIG_DIR"
        echo "Created configuration directory: $CONFIG_DIR"
    fi
fi

echo "mcpproxy installation complete!"
echo "Run 'mcpproxy --help' for usage information." 
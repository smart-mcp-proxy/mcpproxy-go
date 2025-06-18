#!/bin/bash
# Pre-removal script for mcpproxy

set -e

# Stop and disable systemd user service (if systemd is available)
if command -v systemctl >/dev/null 2>&1; then
    echo "Stopping mcpproxy systemd user service..."
    # This will fail gracefully if service is not running or enabled
    systemctl --user stop mcpproxy@$USER 2>/dev/null || true
    systemctl --user disable mcpproxy@$USER 2>/dev/null || true
    echo "mcpproxy systemd service stopped and disabled"
fi

echo "mcpproxy pre-removal complete!" 
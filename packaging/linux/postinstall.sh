#!/bin/sh
set -e

# Create system user/group if missing
if ! getent group mcpproxy >/dev/null 2>&1; then
    groupadd --system mcpproxy
fi
if ! getent passwd mcpproxy >/dev/null 2>&1; then
    useradd --system \
        --gid mcpproxy \
        --home-dir /var/lib/mcpproxy \
        --shell /usr/sbin/nologin \
        --comment "MCPProxy service user" \
        mcpproxy
fi

# Ensure data + log dirs exist with correct ownership
install -d -m 0750 -o mcpproxy -g mcpproxy /var/lib/mcpproxy
install -d -m 0750 -o mcpproxy -g mcpproxy /var/log/mcpproxy

# Make sure the shipped config is readable by the service user
if [ -f /etc/mcpproxy/mcp_config.json ]; then
    chown root:mcpproxy /etc/mcpproxy/mcp_config.json 2>/dev/null || true
    chmod 0640         /etc/mcpproxy/mcp_config.json 2>/dev/null || true
fi

# systemd integration
if command -v systemctl >/dev/null 2>&1; then
    systemctl daemon-reload || true
    if [ -d /run/systemd/system ]; then
        # Enable on first install; for upgrades, restart if it was running
        if systemctl is-enabled --quiet mcpproxy.service 2>/dev/null; then
            systemctl try-restart mcpproxy.service || true
        else
            systemctl enable --now mcpproxy.service || true
        fi
    fi
fi

exit 0

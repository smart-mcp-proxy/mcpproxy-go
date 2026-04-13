#!/bin/sh
set -e

# User/group already created in preinstall; conffile ownership is set by the
# package metadata. This script only handles runtime state and systemd.

install -d -m 0750 -o mcpproxy -g mcpproxy /var/lib/mcpproxy

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

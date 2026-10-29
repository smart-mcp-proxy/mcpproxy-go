#!/bin/sh
set -e

# User/group already created in preinstall; conffile ownership is set by the
# package metadata. This script only handles runtime state and systemd.

install -d -m 0750 -o mcpproxy -g mcpproxy /var/lib/mcpproxy

# /etc/mcpproxy must be writable by the service, not just readable. Saving from
# the web UI goes through config.SaveConfig -> atomicWriteFile, which creates
# mcp_config.json.tmp.<hex> IN THIS DIRECTORY and renames it over the config —
# so the mcpproxy user needs write+search on the directory itself, not merely
# on the file. nfpm creates /etc/mcpproxy implicitly (root:root 0755) because
# only the files inside it are declared, which left the service unable to
# create the temp file. Adding /etc/mcpproxy to ReadWritePaths= in the unit
# (PR #822) lifts systemd's ProtectSystem=strict namespace restriction; this
# lifts the POSIX one. Both are required.
#
# 2770: group-writable so mcpproxy can write, setgid so files created here keep
# the mcpproxy group, and no access for others (the config holds API keys).
# Doing it here rather than as an nfpm dir entry also repairs the permissions
# on hosts that installed an earlier package.
install -d -m 2770 -o root -g mcpproxy /etc/mcpproxy

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

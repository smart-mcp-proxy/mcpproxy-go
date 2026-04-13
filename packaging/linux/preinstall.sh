#!/bin/sh
set -e

# Runs BEFORE files are unpacked so that file_info ownership in the package
# (root:mcpproxy on /etc/mcpproxy/mcp_config.json) resolves to a real GID.

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

exit 0

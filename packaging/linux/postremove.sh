#!/bin/sh
set -e

ACTION="${1:-}"

case "$ACTION" in
    purge)
        # Debian purge: remove data and the service user
        rm -rf /var/lib/mcpproxy /etc/mcpproxy
        if getent passwd mcpproxy >/dev/null 2>&1; then
            userdel mcpproxy || true
        fi
        if getent group mcpproxy >/dev/null 2>&1; then
            groupdel mcpproxy || true
        fi
        ;;
    0)
        # RPM uninstall (not upgrade): drop systemd unit cache
        if command -v systemctl >/dev/null 2>&1; then
            systemctl daemon-reload || true
        fi
        ;;
esac

exit 0

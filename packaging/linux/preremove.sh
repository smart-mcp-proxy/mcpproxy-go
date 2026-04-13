#!/bin/sh
set -e

# On Debian, $1 is "remove" or "upgrade"; on RPM, $1 is 0 (uninstall) or 1 (upgrade)
ACTION="${1:-}"

case "$ACTION" in
    remove|0)
        if command -v systemctl >/dev/null 2>&1 && [ -d /run/systemd/system ]; then
            systemctl stop mcpproxy.service    || true
            systemctl disable mcpproxy.service || true
        fi
        ;;
    upgrade|1)
        # Leave the service running across upgrades; postinstall will try-restart
        ;;
esac

exit 0

#!/bin/bash
#
# Spec 044 (T057) — macOS post-install launcher.
#
# Invoked by the DMG "Install" step (when the DMG wraps an installer .pkg) or
# by a future productbuild-based installer. The sole purpose is to launch the
# tray app tagged as "installer-launched" so the core can emit a single
# telemetry heartbeat with launch_source=installer (see research.md R4/R10).
#
# The `--env MCPPROXY_LAUNCHED_BY=installer` flag is honored by macOS's `open`
# command and inherited by the tray and core child processes. The core
# consumes the env var in Runtime.SetTelemetry (cmd wire-up) and stores a
# one-shot installer_heartbeat_pending=true flag in the activation BBolt
# bucket. The flag is cleared after the very first heartbeat is built —
# subsequent heartbeats report launch_source=login_item or =tray.
#
# This script is idempotent: re-running it simply re-launches the app.
# Existing instances stay up (`open -a` activates rather than duplicating).
#
# Exit codes:
#   0 — launched (or already running)
#   1 — MCPProxy.app not found in /Applications (installer bug)

set -euo pipefail

APP_PATH="/Applications/MCPProxy.app"

if [ ! -d "$APP_PATH" ]; then
    echo "postinstall: $APP_PATH not found — installer did not copy the bundle." >&2
    exit 1
fi

open -a "$APP_PATH" --env MCPPROXY_LAUNCHED_BY=installer

exit 0

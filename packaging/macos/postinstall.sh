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
# **Critical**: we launch via `launchctl asuser <uid> ... env -i ...` so the
# tray app starts in the user's GUI bootstrap context with a clean env. A
# bare `open -a` invoked from postinstall propagates the PKInstallSandbox
# environment (PATH=/bin:/sbin:/usr/bin:/usr/sbin:/usr/libexec, SHELL=/bin/sh,
# INSTALLER_*) into the launched app and every child it spawns — that broke
# Docker discovery in the long-running mcpproxy core (sync.Once permanently
# cached the failed lookup). The clean-env launch mirrors what users get when
# they double-click the app from Finder.
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

# Resolve the actual console user (the human who triggered the install) and
# their uid. $USER inside postinstall is the human's account, but LOGNAME is
# typically root and the env is sandboxed.
REAL_USER="${USER:-}"
if [ -z "$REAL_USER" ] || [ "$REAL_USER" = "root" ]; then
    REAL_USER=$(stat -f%Su /dev/console)
fi
REAL_UID=$(id -u "$REAL_USER" 2>/dev/null || echo "")
USER_HOME=$(/usr/bin/dscl . -read "/Users/$REAL_USER" NFSHomeDirectory 2>/dev/null | awk '{print $2}')
[ -z "$USER_HOME" ] && USER_HOME=$(eval echo "~$REAL_USER")

# Sane PATH covering Docker Desktop (/usr/local/bin), Apple Silicon Homebrew
# (/opt/homebrew/bin), system tools, and the standard system bins. This is
# the env launchd would give a normal user GUI session.
SANE_PATH="/usr/local/bin:/opt/homebrew/bin:/usr/bin:/bin:/usr/sbin:/sbin"

if [ -n "$REAL_UID" ] && [ "$REAL_UID" != "0" ]; then
    # Preferred path: bootstrap into the user's GUI session with `launchctl
    # asuser` so the app inherits launchd's per-user env, then `env -i` wipes
    # any inherited installer vars before invoking `open`.
    /bin/launchctl asuser "$REAL_UID" /usr/bin/env -i \
        HOME="$USER_HOME" \
        USER="$REAL_USER" \
        LOGNAME="$REAL_USER" \
        PATH="$SANE_PATH" \
        /usr/bin/open -a "$APP_PATH" --env MCPPROXY_LAUNCHED_BY=installer
else
    # Fallback for unusual installers (no real user, e.g. CI imaging): launch
    # with a clean PATH but skip the asuser hop. This still avoids leaking
    # PKInstallSandbox env into the long-running daemon.
    /usr/bin/env -i \
        HOME="$USER_HOME" \
        PATH="$SANE_PATH" \
        /usr/bin/open -a "$APP_PATH" --env MCPPROXY_LAUNCHED_BY=installer
fi

exit 0

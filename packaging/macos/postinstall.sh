#!/bin/bash
#
# macOS post-install: quit the old instance, then launch the newly installed one.
#
# Spec 044 (T057) — launch the tray tagged as "installer-launched".
# Spec 092 (FR-004, issue #957) — quit whatever is already running FIRST.
#
# Invoked by the DMG "Install" step (when the DMG wraps an installer .pkg) or
# by a future productbuild-based installer.
#
# Why the quit step exists. `open -a` ACTIVATES a running instance rather than
# starting a new one, so an installer that only calls `open -a` brings the OLD,
# just-overwritten app to the foreground: the user watches an upgrade complete
# and is handed the previous version, still serving from the previous core.
# That is the reported bug. Quitting first is what makes the launch below a
# launch.
#
# Politeness ladder — `osascript … to quit`, then SIGTERM, then SIGKILL — so a
# graceful exit is always tried first. The app installs signal handlers that
# route SIGTERM through its normal quit path (AppLifecycle.installSignalHandlers),
# so even the fallback stops the managed core rather than orphaning it. Should
# a core survive anyway (one the user started themselves), the new tray's
# stale-core supersede (Spec 092 FR-001/FR-002) picks it up.
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
# This script is idempotent: re-running it quits and relaunches the app.
#
# Exit codes:
#   0 — launched
#   1 — MCPProxy.app not found in /Applications (installer bug)

set -euo pipefail

APP_PATH="/Applications/MCPProxy.app"
BUNDLE_ID="com.smartmcpproxy.mcpproxy"

# Matches the tray executable inside ANY MCPProxy.app, wherever it was
# installed. Deliberately the app's own executable path and not the bare name
# "MCPProxy": a looser pattern would also match this script's own command line
# and the core process, and killing the core directly would skip the tray's
# orderly shutdown.
APP_EXEC_PATTERN="MCPProxy.app/Contents/MacOS/MCPProxy"

# How long the old instance gets to quit on its own, and then to die after
# SIGTERM. Tenths of a second.
GRACEFUL_TENTHS=50   # 5s
SIGTERM_TENTHS=30    # 3s

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

# Run a command in the installing user's GUI session with a clean environment.
# Both the AppleScript quit and the launch need this: an `osascript … tell
# application` sent from root's context cannot reach the user's running app.
run_as_user() {
    if [ -n "$REAL_UID" ] && [ "$REAL_UID" != "0" ]; then
        /bin/launchctl asuser "$REAL_UID" /usr/bin/env -i \
            HOME="$USER_HOME" \
            USER="$REAL_USER" \
            LOGNAME="$REAL_USER" \
            PATH="$SANE_PATH" \
            "$@"
    else
        # Fallback for unusual installers (no real user, e.g. CI imaging):
        # clean env but no asuser hop. Still avoids leaking PKInstallSandbox
        # env into the long-running daemon.
        /usr/bin/env -i \
            HOME="$USER_HOME" \
            PATH="$SANE_PATH" \
            "$@"
    fi
}

app_is_running() {
    /usr/bin/pgrep -f "$APP_EXEC_PATTERN" >/dev/null 2>&1
}

# Wait up to $1 tenths of a second for the app to disappear. Returns 0 if it
# did.
wait_for_exit() {
    local budget="$1" waited=0
    while [ "$waited" -lt "$budget" ]; do
        app_is_running || return 0
        /bin/sleep 0.1
        waited=$((waited + 1))
    done
    ! app_is_running
}

quit_running_instance() {
    if ! app_is_running; then
        echo "postinstall: no MCPProxy instance running"
        return 0
    fi

    echo "postinstall: asking the running MCPProxy to quit"
    run_as_user /usr/bin/osascript -e "tell application id \"$BUNDLE_ID\" to quit" \
        >/dev/null 2>&1 || true
    if wait_for_exit "$GRACEFUL_TENTHS"; then
        echo "postinstall: the old instance quit"
        return 0
    fi

    echo "postinstall: MCPProxy did not quit in time — sending SIGTERM" >&2
    /usr/bin/pkill -f "$APP_EXEC_PATTERN" || true
    if wait_for_exit "$SIGTERM_TENTHS"; then
        echo "postinstall: the old instance terminated"
        return 0
    fi

    echo "postinstall: MCPProxy ignored SIGTERM — sending SIGKILL" >&2
    /usr/bin/pkill -9 -f "$APP_EXEC_PATTERN" || true
    # Never fail the install over this. A survivor is handled by the new tray's
    # stale-core supersede rather than by aborting an upgrade that has already
    # copied the bundle.
    wait_for_exit "$SIGTERM_TENTHS" || \
        echo "postinstall: an MCPProxy process survived SIGKILL — continuing" >&2
    return 0
}

quit_running_instance

run_as_user /usr/bin/open -a "$APP_PATH" --env MCPPROXY_LAUNCHED_BY=installer

exit 0

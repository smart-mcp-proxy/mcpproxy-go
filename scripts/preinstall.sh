#!/bin/bash
#
# Pre-installation script for the mcpproxy PKG installer: quit the running
# tray BEFORE the installer replaces its bundle (issues #957, #2016).
#
# Without this an upgrade ends in one of two wrong states, depending on how the
# old bundle was replaced:
#   - the postinstall `open -a` re-activates the old, still running tray, and
#     the user is handed the previous version (#957);
#   - or it starts a second tray beside the old one, and the menu bar shows two
#     icons (#2016, `brew upgrade`).
#
# Why preinstall and not postinstall: the old tray stops its core on the way
# out, and it should do that while its own bundle is still on disk rather than
# on top of the new version's files. The Sparkle update path uses the same
# order (stop, then replace). scripts/postinstall.sh launches the new tray.
#
# How it quits. SIGTERM, then SIGKILL. The tray catches SIGTERM and runs its
# normal quit path, which stops the managed core
# (AppLifecycle.installSignalHandlers), so this is a graceful quit and not a
# kill. An AppleScript `quit` is deliberately not used: sent from the
# installer's root context it depends on an Automation consent the user never
# gave, and can stall the install waiting for it. Should a core outlive the
# tray anyway, the new tray's stale-core supersede (Spec 092) picks it up.
#
# This script never fails the install: every path exits 0.
#
# Tested by scripts/preinstall.test.sh, which sources this file; nothing below
# may run at source time except definitions.

set -euo pipefail

# The tray executable inside any MCPProxy app bundle, wherever it is installed.
# Used with `pgrep -i`: the package installs the bundle as `mcpproxy.app`, the
# Swift tray's executable is `MCPProxy`, older builds used `MCPProxy.app` or a
# Go tray named `mcpproxy`, and `pgrep -f` is otherwise case-sensitive — a
# pattern spelling just one of these silently matches nothing.
#
# Anchored on `.app/Contents/MacOS/` and on the end of the name, so it matches
# neither the core (`…/Contents/Resources/bin/mcpproxy`), nor a longer
# executable name, nor this script's own command line.
APP_EXEC_PATTERN='[^/]*\.app/Contents/MacOS/mcpproxy( |$)'

# How long the tray gets after each signal, in tenths of a second. The tray
# itself gives its polite quit 5s before letting SIGTERM take its default
# action, so 8s covers that with margin.
SIGTERM_TENTHS=80
SIGKILL_TENTHS=30

# Empty, or "-U <uid>". See resolve_console_user.
PGREP_SCOPE=""

# Scope every process operation to the installing user.
#
# The script runs as root, and `pgrep` / `pkill` as root match every user's
# processes. On a shared Mac that would turn "upgrade my copy" into "quit
# everybody's tray". `-U <uid>` restricts the match to the console user;
# without one (CI imaging, login window) there is no other session to protect.
resolve_console_user() {
    local user="${USER:-}" uid=""
    if [ -z "$user" ] || [ "$user" = "root" ]; then
        user=$(/usr/bin/stat -f%Su /dev/console 2>/dev/null || echo "")
    fi
    if [ -n "$user" ]; then
        uid=$(/usr/bin/id -u "$user" 2>/dev/null || echo "")
    fi
    if [ -n "$uid" ] && [ "$uid" != "0" ]; then
        PGREP_SCOPE="-U $uid"
    fi
}

# shellcheck disable=SC2086  # PGREP_SCOPE is empty or "-U <numeric uid>"
app_is_running() {
    pgrep $PGREP_SCOPE -i -f "$APP_EXEC_PATTERN" >/dev/null 2>&1
}

# shellcheck disable=SC2086
signal_app() {
    pkill "$1" $PGREP_SCOPE -i -f "$APP_EXEC_PATTERN" >/dev/null 2>&1 || true
}

# Wait up to $1 tenths of a second for the app to disappear. Returns 0 if it
# did.
wait_for_exit() {
    local budget="$1" waited=0
    while [ "$waited" -lt "$budget" ]; do
        app_is_running || return 0
        sleep 0.1
        waited=$((waited + 1))
    done
    ! app_is_running
}

quit_running_instance() {
    if ! app_is_running; then
        echo "preinstall: no MCPProxy instance running"
        return 0
    fi

    echo "preinstall: asking the running MCPProxy to quit (SIGTERM)"
    signal_app -TERM
    if wait_for_exit "$SIGTERM_TENTHS"; then
        echo "preinstall: the old instance quit"
        return 0
    fi

    echo "preinstall: MCPProxy did not quit in time — sending SIGKILL" >&2
    signal_app -KILL
    # Never fail the install over this: a survivor leaves the user no worse off
    # than before this script existed.
    wait_for_exit "$SIGKILL_TENTHS" || \
        echo "preinstall: an MCPProxy process survived SIGKILL — continuing" >&2
    return 0
}

main() {
    export PATH="/usr/bin:/bin:/usr/sbin:/sbin"
    resolve_console_user
    quit_running_instance || true
    exit 0
}

if [ "${BASH_SOURCE[0]}" = "$0" ]; then
    main "$@"
fi

#!/usr/bin/env bash
# preinstall.test.sh — red/green self-test for the macOS PKG preinstall
# (scripts/preinstall.sh), which quits a running tray before the installer
# replaces its bundle (issues #957, #2016).
#
# The quit step once lived in a script that no package shipped, and matched a
# process path the installed app never has. Both mistakes are checked here:
#   - the matching and the SIGTERM -> SIGKILL ladder run against real
#     processes started from fake bundles in a temp dir;
#   - create-pkg.sh must ship this preinstall, and no second copy may exist.
#
# The test never touches a real MCPProxy: every match is confined to the temp
# dir by prefixing the script's own pattern with it.
#
# Run directly: ./scripts/preinstall.test.sh (bash 3.2 ok; macOS and Linux)
#
# shellcheck disable=SC2016,SC2034
# SC2016: fixture bodies and grep patterns hold a literal `$`. SC2034: the
# variables set after `source` belong to the script under test.
set -uo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SUT="$ROOT/scripts/preinstall.sh"
TMP="$(mktemp -d)"
# Resolve symlinks (/tmp -> /private/tmp on macOS) so the prefix matches the
# command line the kernel reports.
TMP="$(cd "$TMP" && pwd -P)"

pass=0; fail=0
ok()  { echo "ok   - $1"; pass=$((pass+1)); }
bad() { echo "FAIL - $1"; fail=$((fail+1)); }

cleanup() {
    pkill -9 -f "$TMP/" >/dev/null 2>&1 || true
    rm -rf "$TMP"
}
trap cleanup EXIT

[ -f "$SUT" ] || { echo "FAIL - $SUT does not exist"; exit 1; }

if bash -n "$SUT"; then ok "preinstall.sh parses"; else bad "preinstall.sh has a syntax error"; fi

# make_exec <path> [stubborn] — a long-running fake executable. "stubborn"
# ignores SIGTERM, like a tray whose main thread never answers.
make_exec() {
    mkdir -p "$(dirname "$1")"
    {
        echo '#!/bin/bash'
        [ "${2:-}" = stubborn ] && echo "trap '' TERM"
        # Signals sent before the trap line runs would kill even a stubborn
        # fixture, so the test waits for this marker and not just for the pid.
        echo ': > "$0.ready"'
        echo 'while :; do sleep 0.2; done'
    } > "$1"
    chmod +x "$1"
}

# Start detached (reparented to init), so a killed process is reaped at once
# and never lingers as a zombie of this shell.
spawn() { ( "$@" >/dev/null 2>&1 & ); }

alive() { pgrep -f "$1" >/dev/null 2>&1; }

wait_alive() {
    local n=0
    while [ "$n" -lt 50 ]; do
        if [ -e "$1.ready" ] && alive "$1"; then return 0; fi
        sleep 0.1; n=$((n+1))
    done
    return 1
}

# expect_dead / expect_alive <command-line pattern> <what it proves>
expect_dead()  { if alive "$1"; then bad "$2"; else ok "$2"; fi; }
expect_alive() { if alive "$1"; then ok "$2"; else bad "$2"; fi; }
expect_rc0()   { if [ "$1" -eq 0 ]; then ok "$2"; else bad "$2 (rc=$1)"; fi; }

# Run the script's quit step with every match confined to one case dir, and
# with short waits. Sourcing must not run the script's main.
run_quit() {
    local scope="$1"
    (
        # shellcheck disable=SC1090
        source "$SUT"
        APP_EXEC_PATTERN="$scope/$APP_EXEC_PATTERN"
        PGREP_SCOPE="-U $(id -u)"
        SIGTERM_TENTHS=30
        SIGKILL_TENTHS=30
        quit_running_instance
    )
}

# --- 1. nothing running ------------------------------------------------------
out="$(run_quit "$TMP/none" 2>&1)"; rc=$?
if [ "$rc" -eq 0 ] && printf '%s' "$out" | grep -q "no MCPProxy instance running"; then
    ok "nothing running: exits 0 and says so"
else
    bad "nothing running: rc=$rc out=$out"
fi

# --- 2. the shipped layout: lowercase bundle, MCPProxy executable (#2016) ----
C="$TMP/shipped"
TRAY="$C/mcpproxy.app/Contents/MacOS/MCPProxy"
CORE="$C/mcpproxy.app/Contents/Resources/bin/mcpproxy"
HELPER="$C/mcpproxy.app/Contents/MacOS/MCPProxyHelper"
make_exec "$TRAY"; make_exec "$CORE"; make_exec "$HELPER"
spawn "$TRAY" -psn_0_12345
spawn "$CORE" serve
spawn "$HELPER"
if wait_alive "$TRAY" && wait_alive "$CORE" && wait_alive "$HELPER"; then
    out="$(run_quit "$C" 2>&1)"; rc=$?
    expect_rc0 "$rc" "shipped layout: exits 0"
    expect_dead "$TRAY " "shipped layout: tray in lowercase bundle was quit"
    expect_alive "$CORE" "shipped layout: core left for the tray to stop"
    expect_alive "$HELPER" "shipped layout: longer executable name not matched"
else
    bad "shipped layout: fixtures did not start"
fi

# --- 3. other spellings: MCPProxy.app, and the legacy Go tray executable -----
C="$TMP/spellings"
UPPER="$C/MCPProxy.app/Contents/MacOS/MCPProxy"
LEGACY="$C/legacy/mcpproxy.app/Contents/MacOS/mcpproxy"
make_exec "$UPPER"; make_exec "$LEGACY"
spawn "$UPPER"; spawn "$LEGACY"
if wait_alive "$UPPER" && wait_alive "$LEGACY"; then
    run_quit "$C" >/dev/null 2>&1
    expect_dead "$UPPER" "MCPProxy.app tray was quit"
    run_quit "$C/legacy" >/dev/null 2>&1
    expect_dead "$LEGACY" "legacy Go tray was quit"
else
    bad "spellings: fixtures did not start"
fi

# --- 4. a tray that ignores SIGTERM is killed, and the install goes on -------
C="$TMP/stubborn"
STUB="$C/mcpproxy.app/Contents/MacOS/MCPProxy"
make_exec "$STUB" stubborn
spawn "$STUB"
if wait_alive "$STUB"; then
    out="$(run_quit "$C" 2>&1)"; rc=$?
    expect_rc0 "$rc" "stubborn tray: exits 0"
    expect_dead "$STUB" "stubborn tray was killed"
    if printf '%s' "$out" | grep -q "SIGKILL"; then
        ok "stubborn tray: escalation is logged"
    else
        bad "stubborn tray: no SIGKILL line in: $out"
    fi
else
    bad "stubborn tray: fixture did not start"
fi

# --- 5. the package ships it, once -------------------------------------------
if grep -Eq 'cp +"scripts/preinstall\.sh" +"\$PKG_SCRIPTS/preinstall"' "$ROOT/scripts/create-pkg.sh"; then
    ok "create-pkg.sh ships scripts/preinstall.sh as preinstall"
else
    bad "create-pkg.sh does not copy scripts/preinstall.sh to \$PKG_SCRIPTS/preinstall"
fi
if [ -e "$ROOT/packaging/macos/postinstall.sh" ]; then
    bad "packaging/macos/postinstall.sh exists again: no package ships it (see #2016)"
else
    ok "no unshipped second postinstall"
fi
stale="$(grep -rn "packaging/macos/postinstall" "$ROOT/scripts" "$ROOT/internal" "$ROOT/native/macos/MCPProxy/MCPProxy" \
    --include='*.sh' --include='*.go' --include='*.swift' 2>/dev/null | grep -v "preinstall.test.sh")"
if [ -z "$stale" ]; then
    ok "no reference to the removed script"
else
    bad "stale reference to packaging/macos/postinstall.sh: $stale"
fi

echo
echo "passed: $pass  failed: $fail"
[ "$fail" -eq 0 ]

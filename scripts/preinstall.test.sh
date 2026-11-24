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
# Everything below kills by, and finally deletes, this directory. An empty or
# wrong value here would aim that at the caller's working directory, so a
# failed mktemp stops the test before the cleanup trap exists.
TMP="$(mktemp -d)" || { echo "FAIL - mktemp -d failed"; exit 1; }
[ -n "$TMP" ] && [ -d "$TMP" ] || { echo "FAIL - mktemp -d returned no directory"; exit 1; }
# Resolve symlinks (/tmp -> /private/tmp on macOS) so the prefix matches the
# command line the kernel reports.
TMP="$(cd "$TMP" && pwd -P)" || { echo "FAIL - cannot resolve temp dir"; exit 1; }
case "$TMP" in
    /*/*) ;;
    *) echo "FAIL - refusing to use temp dir '$TMP'"; exit 1 ;;
esac

pass=0; fail=0
ok()  { echo "ok   - $1"; pass=$((pass+1)); }
bad() { echo "FAIL - $1"; fail=$((fail+1)); }

# The temp path goes into pgrep/pkill patterns, which are regular expressions:
# unescaped, the "." in "tmp.Ab12" would also match some other directory.
re_escape() { printf '%s' "$1" | sed 's/[][\\.*^$(){}?+|]/\\&/g'; }

cleanup() {
    pkill -9 -f "^$(re_escape "$TMP")/" >/dev/null 2>&1 || true
    rm -rf "$TMP"
}
trap cleanup EXIT

[ -f "$SUT" ] || { echo "FAIL - $SUT does not exist"; exit 1; }

if bash -n "$SUT"; then ok "preinstall.sh parses"; else bad "preinstall.sh has a syntax error"; fi

# Fixtures are sleep(1) started under another name (`exec -a`), so each
# process's command line STARTS with the fixture path, as a real tray's does.
# A shell script would run as "/bin/bash <path>", which the anchored pattern
# rightly ignores, and macOS kills a copied system binary on launch.
#
# All are started detached (reparented to init), so a killed process is reaped
# at once and never lingers as a zombie of this shell.
spawn() { ( exec -a "$1" sleep 600 >/dev/null 2>&1 & ); }

# Ignores SIGTERM, like a tray whose main thread never answers. An ignored
# signal stays ignored across exec.
spawn_stubborn() { ( trap '' TERM; exec -a "$1" sleep 600 >/dev/null 2>&1 & ); }

# spawn_with_arg <name> <argument> — a process whose ARGUMENT is some path.
# cat(1) blocks opening a FIFO nobody writes to, so it never reads <argument>
# and has no child process to leak.
spawn_with_arg() {
    [ -p "$TMP/never-written" ] || mkfifo "$TMP/never-written"
    ( exec -a "$1" cat "$TMP/never-written" "$2" >/dev/null 2>&1 & )
}

alive() { pgrep -f "^$(re_escape "$1")" >/dev/null 2>&1; }

wait_alive() {
    local n=0
    while [ "$n" -lt 50 ]; do
        if alive "$1"; then return 0; fi
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
        APP_ROOT_PATTERN="$(re_escape "$scope")/"
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
# Names the tray's path only as an argument: must not be taken for the tray.
BYSTANDER="$C/bin/viewer"
spawn "$TRAY"
spawn "$CORE"
spawn "$HELPER"
spawn_with_arg "$BYSTANDER" "$TRAY"
if wait_alive "$TRAY " && wait_alive "$CORE" && wait_alive "$HELPER" && wait_alive "$BYSTANDER"; then
    out="$(run_quit "$C" 2>&1)"; rc=$?
    expect_rc0 "$rc" "shipped layout: exits 0"
    expect_dead "$TRAY " "shipped layout: tray in lowercase bundle was quit"
    expect_alive "$CORE" "shipped layout: core left for the tray to stop"
    expect_alive "$HELPER" "shipped layout: longer executable name not matched"
    expect_alive "$BYSTANDER" "shipped layout: a process only naming the tray path is spared"
else
    bad "shipped layout: fixtures did not start"
fi

# --- 3. other spellings: MCPProxy.app, and the legacy Go tray executable -----
C="$TMP/spellings"
UPPER="$C/MCPProxy.app/Contents/MacOS/MCPProxy"
LEGACY="$C/legacy/mcpproxy.app/Contents/MacOS/mcpproxy"
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
spawn_stubborn "$STUB"
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

#!/usr/bin/env bash
# build-swift-app.test.sh — red/green self-test for the SwiftPM retry in
# build-swift-app.sh. Hermetic: fake `swift`, `swiftc` and `curl` on PATH,
# a throwaway HOME holding a stale SwiftPM artifact-cache entry and a
# throwaway package dir, so the test proves that
#   1. a `swift build` that dies on a stale binary-artifact download
#      ("already exists in file system") purges the stale cache entries and
#      is retried once — the swiftc fallback is never reached;
#   2. an ordinary compile error is NOT retried and leaves the cache alone;
#   3. a build that succeeds first time runs once and purges nothing.
# Run directly: ./scripts/build-swift-app.test.sh (bash 3.2 ok)
set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
SUT="$SCRIPT_DIR/build-swift-app.sh"

pass=0
fail=0
check() { # desc, then the condition command (exit 0 = pass)
    local desc="$1"
    shift
    if "$@"; then
        echo "ok   - $desc"
        pass=$((pass + 1))
    else
        echo "FAIL - $desc"
        fail=$((fail + 1))
    fi
}

T="$(mktemp -d)"
trap 'rm -rf "$T"' EXIT

STALE_ENTRY="https___github_com_sparkle_project_Sparkle_releases_download_2_9_3_Sparkle_for_Swift_Package_Manager_zip"
SPM_ERROR="error: failed downloading 'https://github.com/sparkle-project/Sparkle/releases/download/2.9.3/Sparkle-for-Swift-Package-Manager.zip' which is required by binary target 'Sparkle': \$HOME/Library/Caches/org.swift.swiftpm/artifacts/$STALE_ENTRY already exists in file system"

# setup <case> <first-call-behaviour>: builds a fresh HOME, package dir and
# shim PATH. The fake swift reads its behaviour for call N from
# $CASE/behaviour.N (missing file = succeed by writing the binary).
setup() {
    CASE="$T/$1"
    mkdir -p "$CASE/home/Library/Caches/org.swift.swiftpm/artifacts/$STALE_ENTRY" \
             "$CASE/pkg/.build/artifacts/sparkle" "$CASE/bin" "$CASE/out"
    echo stale > "$CASE/home/Library/Caches/org.swift.swiftpm/artifacts/$STALE_ENTRY/partial"
    echo stale > "$CASE/pkg/.build/artifacts/sparkle/partial"
    : > "$CASE/swift-calls"
    cat > "$CASE/bin/swift" <<SHIM
#!/usr/bin/env bash
n=\$(( \$(wc -l < "$CASE/swift-calls") + 1 ))
echo "call \$n: \$*" >> "$CASE/swift-calls"
{ [ -e "$CASE/home/Library/Caches/org.swift.swiftpm/artifacts/$STALE_ENTRY" ] && echo "home:present" || echo "home:absent"; } >> "$CASE/state.\$n"
{ [ -e "$CASE/pkg/.build/artifacts/sparkle" ] && echo "pkg:present" || echo "pkg:absent"; } >> "$CASE/state.\$n"
if [ -f "$CASE/behaviour.\$n" ]; then
    cat "$CASE/behaviour.\$n"
    exit 1
fi
mkdir -p .build/release && : > .build/release/MCPProxy && chmod +x .build/release/MCPProxy
echo "Build complete!"
SHIM
    cat > "$CASE/bin/swiftc" <<SHIM
#!/usr/bin/env bash
: > "$CASE/swiftc-called"
exit 1
SHIM
    printf '#!/usr/bin/env bash\nexit 1\n' > "$CASE/bin/curl"
    chmod +x "$CASE/bin/swift" "$CASE/bin/swiftc" "$CASE/bin/curl"
}

run_sut() { # runs the script with the shims; output to $CASE/out.log
    (
        cd "$ROOT" &&
        HOME="$CASE/home" PATH="$CASE/bin:$PATH" \
        BUILD_SWIFT_APP_SWIFT_DIR="$CASE/pkg" \
        BUILD_SWIFT_APP_STOP_AFTER_BUILD=1 \
        bash "$SUT" v0.0.0-test arm64 "$CASE/out"
    ) > "$CASE/out.log" 2>&1
    echo $? > "$CASE/exit"
}

calls() { wc -l < "$CASE/swift-calls" | tr -d ' '; }

# --- 1. stale artifact: purge + one retry, no swiftc ----------------------
setup stale
echo "$SPM_ERROR" > "$CASE/behaviour.1"
run_sut
check "stale artifact: exit 0"                      [ "$(cat "$CASE/exit")" = 0 ]
check "stale artifact: swift build ran twice"       [ "$(calls)" = 2 ]
check "stale artifact: HOME cache entry purged before the retry" \
      grep -q "home:absent" "$CASE/state.2"
check "stale artifact: package .build/artifacts/sparkle purged before the retry" \
      grep -q "pkg:absent" "$CASE/state.2"
check "stale artifact: swiftc fallback never reached" [ ! -e "$CASE/swiftc-called" ]
check "stale artifact: log names the purge"         grep -q "stale SwiftPM artifact" "$CASE/out.log"

# --- 2. compile error: no retry, cache untouched ---------------------------
setup compile
echo "MCPProxy/App.swift:1:1: error: cannot find 'foo' in scope" > "$CASE/behaviour.1"
echo "same again" > "$CASE/behaviour.2"
run_sut
check "compile error: swift build ran once"         [ "$(calls)" = 1 ]
check "compile error: HOME cache entry left alone" \
      [ -e "$CASE/home/Library/Caches/org.swift.swiftpm/artifacts/$STALE_ENTRY/partial" ]
check "compile error: swiftc fallback reached"      [ -e "$CASE/swiftc-called" ]
check "compile error: exit 1"                       [ "$(cat "$CASE/exit")" = 1 ]

# --- 3. first-time success: one run, nothing purged ------------------------
setup clean
run_sut
check "clean build: exit 0"                         [ "$(cat "$CASE/exit")" = 0 ]
check "clean build: swift build ran once"           [ "$(calls)" = 1 ]
check "clean build: HOME cache entry left alone" \
      [ -e "$CASE/home/Library/Caches/org.swift.swiftpm/artifacts/$STALE_ENTRY/partial" ]
check "clean build: no purge in the log"            bash -c "! grep -q 'stale SwiftPM artifact' '$CASE/out.log'"

echo
echo "passed=$pass failed=$fail"
[ "$fail" -eq 0 ]

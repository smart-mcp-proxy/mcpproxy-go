#!/usr/bin/env bash
# go-test-retry-runtime-fault.test.sh — red/green self-test for
# go-test-retry-runtime-fault.sh (issue #1794). Hermetic: a fake `go` on PATH
# replays canned output, counts its invocations and records the package list
# it was given, so the test proves the retry fires only when every failed
# package died of a runtime fault, re-runs only those packages, keeps the
# coverage profile, and never retries when any package shows a genuine
# failure (assertion, timeout, data race, build failure) — including a
# genuine failure sitting next to a fault, in the same package or another.
# Run directly: ./scripts/go-test-retry-runtime-fault.test.sh (bash 3.2 ok)
set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SUT="$SCRIPT_DIR/go-test-retry-runtime-fault.sh"

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

work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT
mkdir -p "$work/bin"
cat > "$work/bin/go" <<'FAKE'
#!/usr/bin/env bash
# Fake `go`: CASE_DIR holds run1.out/run1.exit and run2.out/run2.exit; the
# invocation count lives in CASE_DIR/count and every argv is appended to
# CASE_DIR/argv.
n=$(( $(cat "$CASE_DIR/count" 2>/dev/null || echo 0) + 1 ))
echo "$n" > "$CASE_DIR/count"
printf '%s\n' "$*" >> "$CASE_DIR/argv"
for a in "$@"; do
    case "$a" in
        -coverprofile=*) printf 'mode: atomic\nrun%d.go:1.1,2.2 1 1\n' "$n" > "${a#-coverprofile=}" ;;
    esac
done
cat "$CASE_DIR/run$n.out"
exit "$(cat "$CASE_DIR/run$n.exit")"
FAKE
chmod +x "$work/bin/go"
export PATH="$work/bin:$PATH"

new_case() { # name -> sets CASE_DIR (exported for the fake go) and d
    d="$work/$1"
    mkdir -p "$d"
    export CASE_DIR="$d"
}
count() { cat "$d/count" 2>/dev/null || echo 0; }
argv_line() { sed -n "${1}p" "$d/argv"; }

FAULT_OUT=$'=== RUN   TestSolR2\nunexpected fault address 0xffffffffffffffff\nfatal error: fault\n[signal 0xc0000005 code=0x0 addr=0xffffffffffffffff pc=0x7ff7]\nFAIL\tgithub.com/x/y/internal/security/scanner\t12.345s\nok  \tgithub.com/x/y/internal/other\t0.100s\nFAIL'
ASSERT_OUT=$'=== RUN   TestThing\n    thing_test.go:12: expected 1, got 2\n--- FAIL: TestThing (0.00s)\nFAIL\tgithub.com/x/y/internal/thing\t0.200s\nFAIL'
TIMEOUT_OUT=$'panic: test timed out after 20m0s\nFAIL\tgithub.com/x/y/cmd/mcpproxy\t1200.360s\nFAIL'
BUILD_OUT=$'# github.com/x/y/internal/broken\nbroken.go:3:1: syntax error\nFAIL\tgithub.com/x/y/internal/broken [build failed]\nFAIL'
OK_OUT=$'ok  \tgithub.com/x/y/internal/security/scanner\t11.000s'
# A fault in one package and a genuine failure in another, in one run.
FAULT_PLUS_ASSERT_OUT="$ASSERT_OUT"$'\n'"$FAULT_OUT"
FAULT_PLUS_TIMEOUT_OUT="$FAULT_OUT"$'\n'"$TIMEOUT_OUT"
FAULT_PLUS_BUILD_OUT="$FAULT_OUT"$'\n'"$BUILD_OUT"
# A fault AND an assertion failure inside the same package block.
FAULT_SAME_PKG_ASSERT_OUT=$'=== RUN   TestA\n    a_test.go:9: boom\n--- FAIL: TestA (0.00s)\n=== RUN   TestB\nunexpected fault address 0xffffffffffffffff\nfatal error: fault\nFAIL\tgithub.com/x/y/internal/mixed\t3.000s\nFAIL'
# A data race report next to a fault in the same package.
FAULT_PLUS_RACE_OUT=$'=== RUN   TestR\nWARNING: DATA RACE\nRead at 0x00c0 by goroutine 7:\nunexpected fault address 0xffffffffffffffff\nfatal error: fault\nFAIL\tgithub.com/x/y/internal/racy\t2.000s\nFAIL'
# Test diagnostics that merely CONTAIN the fault words (indented by testing).
DIAG_TEXT_OUT=$'=== RUN   TestResponse\n    thing_test.go:12: expected no "fatal error: fault" in response, got unexpected fault address 0xdead\n--- FAIL: TestResponse (0.00s)\nFAIL\tgithub.com/x/y/internal/diag\t0.100s\nFAIL'

# 1. Runtime fault, retry passes: exit 0, exactly two invocations, the second
#    narrowed to the failed package, flags preserved, warning annotation out.
new_case fault_then_pass
printf '%s\n' "$FAULT_OUT" > "$d/run1.out"; echo 2 > "$d/run1.exit"
printf '%s\n' "$OK_OUT" > "$d/run2.out"; echo 0 > "$d/run2.exit"
out=$("$SUT" -v -race -timeout 40m -- ./... 2>&1); st=$?
check "fault then pass: exit 0" [ "$st" -eq 0 ]
check "fault then pass: two go invocations" [ "$(count)" = 2 ]
check "fault then pass: retry narrowed to the failed package with the same flags" \
    [ "$(argv_line 2)" = "test -v -race -timeout 40m github.com/x/y/internal/security/scanner" ]
check "fault then pass: ::warning:: annotation names the package" \
    grep -q '::warning.*github.com/x/y/internal/security/scanner' <<<"$out"

# 2. Runtime fault, retry fails too: non-zero exit, two invocations.
new_case fault_then_fail
printf '%s\n' "$FAULT_OUT" > "$d/run1.out"; echo 2 > "$d/run1.exit"
printf '%s\n' "$ASSERT_OUT" > "$d/run2.out"; echo 1 > "$d/run2.exit"
"$SUT" -v -- ./... >/dev/null 2>&1; st=$?
check "fault then fail: exit non-zero" [ "$st" -ne 0 ]
check "fault then fail: two go invocations" [ "$(count)" = 2 ]

# 3. Ordinary assertion failure: never retried, original exit kept.
new_case assertion
printf '%s\n' "$ASSERT_OUT" > "$d/run1.out"; echo 1 > "$d/run1.exit"
"$SUT" -v -- ./... >/dev/null 2>&1; st=$?
check "assertion failure: exit 1 unchanged" [ "$st" -eq 1 ]
check "assertion failure: one go invocation" [ "$(count)" = 1 ]

# 4. Test timeout: not a fault, never retried.
new_case timeout
printf '%s\n' "$TIMEOUT_OUT" > "$d/run1.out"; echo 1 > "$d/run1.exit"
"$SUT" -v -- ./... >/dev/null 2>&1; st=$?
check "timeout: exit 1 unchanged" [ "$st" -eq 1 ]
check "timeout: one go invocation" [ "$(count)" = 1 ]

# 5. Build failure: not a fault, never retried.
new_case build
printf '%s\n' "$BUILD_OUT" > "$d/run1.out"; echo 1 > "$d/run1.exit"
"$SUT" -v -- ./... >/dev/null 2>&1; st=$?
check "build failure: exit 1 unchanged" [ "$st" -eq 1 ]
check "build failure: one go invocation" [ "$(count)" = 1 ]

# 6. Green run: one invocation, exit 0.
new_case green
printf '%s\n' "$OK_OUT" > "$d/run1.out"; echo 0 > "$d/run1.exit"
"$SUT" -v -- ./... >/dev/null 2>&1; st=$?
check "green: exit 0" [ "$st" -eq 0 ]
check "green: one go invocation" [ "$(count)" = 1 ]

# 7. Coverage profile: the retry writes FILE.retry and it is folded into FILE
#    with a single mode line.
new_case coverage
printf '%s\n' "$FAULT_OUT" > "$d/run1.out"; echo 2 > "$d/run1.exit"
printf '%s\n' "$OK_OUT" > "$d/run2.out"; echo 0 > "$d/run2.exit"
cov="$d/coverage.out"
"$SUT" -v "-coverprofile=$cov" -- ./... >/dev/null 2>&1; st=$?
check "coverage: exit 0" [ "$st" -eq 0 ]
check "coverage: retry wrote to FILE.retry" grep -q -- "-coverprofile=$cov.retry" <<<"$(argv_line 2)"
check "coverage: one mode line" [ "$(grep -c '^mode:' "$cov")" = 1 ]
check "coverage: first run rows kept" grep -q 'run1.go' "$cov"
check "coverage: retry rows appended" grep -q 'run2.go' "$cov"
check "coverage: FILE.retry removed" [ ! -f "$cov.retry" ]

# 9. Fault in one package, assertion failure in another: never retried, the
#    first run's exit is kept (a retry could let an order-dependent failure
#    pass).
new_case fault_plus_assert
printf '%s\n' "$FAULT_PLUS_ASSERT_OUT" > "$d/run1.out"; echo 1 > "$d/run1.exit"
"$SUT" -v -- ./... >/dev/null 2>&1; st=$?
check "fault + assertion elsewhere: exit 1 unchanged" [ "$st" -eq 1 ]
check "fault + assertion elsewhere: one go invocation" [ "$(count)" = 1 ]

# 10. Fault in one package, timeout in another: never retried.
new_case fault_plus_timeout
printf '%s\n' "$FAULT_PLUS_TIMEOUT_OUT" > "$d/run1.out"; echo 1 > "$d/run1.exit"
"$SUT" -v -- ./... >/dev/null 2>&1; st=$?
check "fault + timeout elsewhere: exit 1 unchanged" [ "$st" -eq 1 ]
check "fault + timeout elsewhere: one go invocation" [ "$(count)" = 1 ]

# 11. Fault in one package, build failure in another: never retried.
new_case fault_plus_build
printf '%s\n' "$FAULT_PLUS_BUILD_OUT" > "$d/run1.out"; echo 1 > "$d/run1.exit"
"$SUT" -v -- ./... >/dev/null 2>&1; st=$?
check "fault + build failure elsewhere: exit 1 unchanged" [ "$st" -eq 1 ]
check "fault + build failure elsewhere: one go invocation" [ "$(count)" = 1 ]

# 12. Fault and an assertion failure in the SAME package: never retried.
new_case fault_same_pkg_assert
printf '%s\n' "$FAULT_SAME_PKG_ASSERT_OUT" > "$d/run1.out"; echo 1 > "$d/run1.exit"
"$SUT" -v -- ./... >/dev/null 2>&1; st=$?
check "fault + assertion in the same package: exit 1 unchanged" [ "$st" -eq 1 ]
check "fault + assertion in the same package: one go invocation" [ "$(count)" = 1 ]

# 13. Fault next to a data race report: never retried.
new_case fault_plus_race
printf '%s\n' "$FAULT_PLUS_RACE_OUT" > "$d/run1.out"; echo 1 > "$d/run1.exit"
"$SUT" -v -race -- ./... >/dev/null 2>&1; st=$?
check "fault + data race: exit 1 unchanged" [ "$st" -eq 1 ]
check "fault + data race: one go invocation" [ "$(count)" = 1 ]

# 14. Test diagnostics that only mention the fault words (indented) are not
#     a runtime fault: never retried.
new_case diag_text
printf '%s\n' "$DIAG_TEXT_OUT" > "$d/run1.out"; echo 1 > "$d/run1.exit"
"$SUT" -v -- ./... >/dev/null 2>&1; st=$?
check "diagnostic text only: exit 1 unchanged" [ "$st" -eq 1 ]
check "diagnostic text only: one go invocation" [ "$(count)" = 1 ]

# 15. Two faulted packages, nothing else: both retried, nothing more.
new_case two_faults
printf '%s\n' "$FAULT_OUT" $'unexpected fault address 0xffffffffffffffff\nfatal error: fault\nFAIL\tgithub.com/x/y/internal/second\t1.000s\nFAIL' > "$d/run1.out"; echo 2 > "$d/run1.exit"
printf '%s\n' "$OK_OUT" $'ok  \tgithub.com/x/y/internal/second\t0.100s' > "$d/run2.out"; echo 0 > "$d/run2.exit"
"$SUT" -v -- ./... >/dev/null 2>&1; st=$?
check "two faults: exit 0" [ "$st" -eq 0 ]
check "two faults: retry lists exactly the two packages" \
    [ "$(argv_line 2)" = "test -v github.com/x/y/internal/security/scanner github.com/x/y/internal/second" ]

# 16. No flags at all (`-- ./...`) must work under set -u on bash 3.2.
new_case no_flags
printf '%s\n' "$OK_OUT" > "$d/run1.out"; echo 0 > "$d/run1.exit"
"$SUT" -- ./... >/dev/null 2>&1; st=$?
check "no flags: exit 0" [ "$st" -eq 0 ]
check "no flags: go ran with the packages only" [ "$(argv_line 1)" = "test ./..." ]

# 8. Usage: no `--` separator is a usage error, go never runs.
new_case usage
"$SUT" -v ./... >/dev/null 2>&1; st=$?
check "usage: missing -- exits 64" [ "$st" -eq 64 ]
check "usage: go never ran" [ ! -f "$d/count" ]

echo
echo "$pass passed, $fail failed"
[ "$fail" -eq 0 ]

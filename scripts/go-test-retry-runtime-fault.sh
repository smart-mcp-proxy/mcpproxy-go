#!/usr/bin/env bash
# go-test-retry-runtime-fault.sh — run `go test` once and, ONLY when every
# failed package died of a Go runtime fault (issue #1794: `unexpected fault
# address 0xffffffffffffffff / fatal error: fault` inside
# internal/testlog.Getenv on windows-latest, a Go/Windows runtime bug —
# golang/go#81189, #81238), re-run those packages, once.
#
# A package is retried only when its own output block carries a runtime-fault
# line AND no sign of a genuine failure: no `--- FAIL:`, no `panic: test timed
# out`, no `WARNING: DATA RACE`. If ANY failed package is not retryable
# (assertion failure, timeout, data race, `[build failed]`, `[setup failed]`),
# nothing is retried and the first run's exit status is returned unchanged —
# a red test stays red, and a fault never buys an order-dependent failure a
# second chance.
#
# Usage:
#   scripts/go-test-retry-runtime-fault.sh [go test flags...] -- <packages...>
#
# The `--` separates flags from packages so the retry can keep the flags and
# narrow the package list. A `-coverprofile=FILE` flag is honoured on the
# retry by writing the retried packages' profile to FILE.retry and appending
# it to FILE (one mode line), so the coverage upload still covers them.
#
# Every retry is announced with a GitHub `::warning::` annotation naming the
# packages, so the flake rate stays visible instead of being silently eaten.
# Portable to bash 3.2 (macOS) and Git Bash on windows-latest.
# Self-test: scripts/go-test-retry-runtime-fault.test.sh
set -uo pipefail

flags=()
pkgs=()
seen_sep=0
for arg in "$@"; do
    if [ "$seen_sep" -eq 0 ] && [ "$arg" = "--" ]; then
        seen_sep=1
        continue
    fi
    if [ "$seen_sep" -eq 0 ]; then
        flags+=("$arg")
    else
        pkgs+=("$arg")
    fi
done
if [ "$seen_sep" -eq 0 ] || [ "${#pkgs[@]}" -eq 0 ]; then
    echo "usage: $0 [go test flags...] -- <packages...>" >&2
    exit 64
fi

log="$(mktemp "${TMPDIR:-/tmp}/go-test-retry.XXXXXX")"
trap 'rm -f "$log"' EXIT

# `go test` output must stream to the job log unchanged (the -v test names
# are what the humans and the log-grep canaries read), so tee it.
# ${arr[@]+"${arr[@]}"} is the bash 3.2 spelling of "expand, even if empty"
# under set -u.
set +e
go test ${flags[@]+"${flags[@]}"} "${pkgs[@]}" 2>&1 | tee "$log"
status=${PIPESTATUS[0]}
set -e

if [ "$status" -eq 0 ]; then
    exit 0
fi

# Classify every failed package from its own output block. `go test` prints
# each package's output followed by its summary line (`ok  \t<pkg>\t<dur>`,
# `FAIL\t<pkg>\t<dur>`, `FAIL\t<pkg> [build failed]`, `?   \t<pkg>\t[no test
# files]` — `ok` and `?` are padded with spaces before the tab), so the lines
# since the previous summary belong to this package. `--- FAIL:` is matched
# with leading whitespace because subtest results are indented.
# The fault signatures are anchored at column 0: the Go runtime prints them
# unindented, while a test's own diagnostics are indented by `testing`.
# Output: one `<verdict>\t<pkg>` line per failed package, verdict `retry` or
# `hard`.
classified="$(awk '
    function reset() { fault = 0; hard = 0 }
    BEGIN { reset() }
    /^unexpected fault address 0x/ { fault = 1 }
    /^fatal error: fault$/ { fault = 1 }
    /^fatal error: unexpected signal during runtime execution/ { fault = 1 }
    /^Exception 0x[cC]0000005/ { fault = 1 }
    /^[ \t]*--- FAIL: / { hard = 1 }
    /^panic: test timed out/ { hard = 1 }
    /^WARNING: DATA RACE/ { hard = 1 }
    /^FAIL\t[^ \t]+ \[(build|setup) failed\]/ { print "hard\t" $2; reset(); next }
    /^FAIL\t[^ \t]+\t/ {
        if (fault && !hard) print "retry\t" $2; else print "hard\t" $2
        reset(); next
    }
    /^(ok|\?) *\t/ { reset(); next }
' FS='\t' "$log")"

retry=()
hard=()
while IFS=$'\t' read -r verdict pkg; do
    [ -n "$pkg" ] || continue
    case "$verdict" in
        retry) retry+=("$pkg") ;;
        *) hard+=("$pkg") ;;
    esac
done <<EOF_CLASSIFIED
$classified
EOF_CLASSIFIED

if [ "${#hard[@]}" -gt 0 ]; then
    # At least one genuine failure: never retry anything, keep the exit.
    if [ "${#retry[@]}" -gt 0 ]; then
        echo "go-test-retry: runtime fault in ${retry[*]} but genuine failure in ${hard[*]}; not retrying, keeping exit $status" >&2
    fi
    exit "$status"
fi
if [ "${#retry[@]}" -eq 0 ]; then
    # Non-zero exit with no fault-only package (e.g. the fault landed outside
    # any package block): nothing to retry, keep the exit.
    exit "$status"
fi

echo "::warning title=Go runtime fault, retrying once (issue #1794)::${retry[*]}"
echo "go-test-retry: runtime fault in the first run; re-running ${#retry[@]} package(s) once: ${retry[*]}" >&2

retry_flags=()
cover_file=""
for f in ${flags[@]+"${flags[@]}"}; do
    case "$f" in
        -coverprofile=*)
            cover_file="${f#-coverprofile=}"
            retry_flags+=("-coverprofile=${cover_file}.retry")
            ;;
        *) retry_flags+=("$f") ;;
    esac
done

set +e
go test ${retry_flags[@]+"${retry_flags[@]}"} "${retry[@]}" 2>&1 | tee -a "$log"
retry_status=${PIPESTATUS[0]}
set -e

if [ -n "$cover_file" ] && [ -f "${cover_file}.retry" ]; then
    if [ -f "$cover_file" ]; then
        # Both files start with the same `mode:` line; keep one.
        tail -n +2 "${cover_file}.retry" >> "$cover_file"
    else
        mv "${cover_file}.retry" "$cover_file"
    fi
    rm -f "${cover_file}.retry"
fi

if [ "$retry_status" -ne 0 ]; then
    echo "go-test-retry: retry failed too (exit $retry_status)" >&2
    exit "$retry_status"
fi
echo "go-test-retry: retry passed" >&2
exit 0

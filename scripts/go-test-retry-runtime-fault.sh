#!/usr/bin/env bash
# go-test-retry-runtime-fault.sh — run `go test` once and, ONLY when the run
# died of a Go runtime fault (issue #1794: `unexpected fault address
# 0xffffffffffffffff / fatal error: fault` inside internal/testlog.Getenv on
# windows-latest, a Go/Windows runtime bug — golang/go#81189, #81238), re-run
# the packages that failed, once. Ordinary assertion failures, build errors
# and `panic: test timed out` are NOT retried: the first run's exit status is
# returned unchanged, so a red test stays red.
#
# Usage:
#   scripts/go-test-retry-runtime-fault.sh [go test flags...] -- <packages...>
#
# The `--` separates flags from packages so the retry can keep the flags and
# narrow the package list to the ones that printed `FAIL <pkg>`. A
# `-coverprofile=FILE` flag is honoured on the retry by writing the retried
# packages' profile to FILE.retry and appending it to FILE (same mode line),
# so the coverage upload still covers them.
#
# Every retry is announced with a GitHub `::warning::` annotation naming the
# packages, so the flake rate stays visible instead of being silently eaten.
# Self-test: scripts/go-test-retry-runtime-fault.test.sh
set -uo pipefail

FAULT_PATTERN='unexpected fault address 0x|fatal error: fault|unexpected signal during runtime execution|Exception 0x[cC]0000005'

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
set +e
go test "${flags[@]}" "${pkgs[@]}" 2>&1 | tee "$log"
status=${PIPESTATUS[0]}
set -e

if [ "$status" -eq 0 ]; then
    exit 0
fi

if ! grep -Eq "$FAULT_PATTERN" "$log"; then
    # A real failure (assertion, build error, timeout): never retried.
    exit "$status"
fi

# Packages whose binary reported FAIL. `go test` prints `FAIL\t<pkg>\t<dur>`
# for a package whose test binary exited non-zero, and `FAIL\t<pkg> [build
# failed]` for a build error; the latter is not a runtime fault, skip it.
failed=()
while IFS= read -r pkg; do
    [ -n "$pkg" ] && failed+=("$pkg")
done < <(grep -E $'^FAIL\t[^ \t]+\t' "$log" | awk -F'\t' '{print $2}' | sort -u)
if [ "${#failed[@]}" -eq 0 ]; then
    echo "go-test-retry: runtime fault seen but no 'FAIL <pkg>' line to retry; keeping exit $status" >&2
    exit "$status"
fi

echo "::warning title=Go runtime fault, retrying once (issue #1794)::$(printf '%s ' "${failed[@]}")"
echo "go-test-retry: runtime fault in the first run; re-running ${#failed[@]} package(s) once: ${failed[*]}" >&2

retry_flags=()
cover_file=""
for f in "${flags[@]}"; do
    case "$f" in
        -coverprofile=*)
            cover_file="${f#-coverprofile=}"
            retry_flags+=("-coverprofile=${cover_file}.retry")
            ;;
        *) retry_flags+=("$f") ;;
    esac
done

set +e
go test "${retry_flags[@]}" "${failed[@]}" 2>&1 | tee -a "$log"
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

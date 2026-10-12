#!/usr/bin/env bash
# check-upstream-write-sites.sh - Spec 116 FR-009 (T002): the live configuration
# is the only source of server membership and fields, and storage's `upstreams`
# bucket is its projection, written only by the config commit
# (internal/runtime/commit*.go). Any other writer of an `upstreams` row is a
# second source of truth: its change is lost on the next commit, or it
# resurrects a removed server (#1558, #1559, #1560).
#
# Fails when a non-test Go file outside internal/storage/ and
# internal/runtime/commit*.go calls, or takes a method value of, one of the
# row-writing storage methods,
# including the exported alias AddUpstream (it calls SaveUpstreamServer) and
# the AsyncManager queue writers (SaveServerAsync, DeleteServerAsync,
# EnableServerAsync, QuarantineServerAsync), which write rows from a worker.
# A selector split across lines (`st.` then `SaveUpstreamServer(sc)`) is
# matched too. Whole-line comments are ignored.
#
# Usage: scripts/check-upstream-write-sites.sh [ROOT]
# ROOT defaults to the repository root. Exit 0 = clean, 1 = a hit (printed).
set -uo pipefail

ROOT="${1:-$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)}"
cd "$ROOT" || { echo "cannot enter $ROOT" >&2; exit 2; }

methods='SaveUpstreamServer|AddUpstream|CreateUpstreamServer|UpdateUpstream|DeleteUpstreamServer|RemoveUpstream|EnableUpstreamServer|QuarantineUpstreamServer|SaveUpstreamKeepingQuarantine|ApplyServerCommit|SaveUpstream|CreateUpstream|DeleteUpstream|EnableServerSync|QuarantineServerSync|SaveServerAsync|DeleteServerAsync|EnableServerAsync|QuarantineServerAsync'

dirs=()
for d in internal cmd; do
  [[ -d "$d" ]] && dirs+=("$d")
done
[[ ${#dirs[@]} -gt 0 ]] || { echo "upstream write sites: nothing to scan"; exit 0; }

# A selector of one of the methods followed by anything but an identifier
# character: a call `st.SaveUpstreamServer(sc)` AND a method value or method
# expression (`save := st.SaveUpstreamServer`, `run(st.SaveServerAsync, x)`,
# `(*BoltDB).SaveUpstreamServer`) that writes the row when it is called later
# (review round 7). A longer identifier such as `.SaveUpstreamServerCount`
# does not match.
hits="$(grep -rnE "\.(${methods})([^A-Za-z0-9_]|$)" "${dirs[@]}" --include='*.go' 2>/dev/null \
  | grep -vE '^[^:]+_test\.go:' \
  | grep -vE '^internal/storage/' \
  | grep -vE '^internal/runtime/commit[^/]*\.go:' \
  | grep -vE '^[^:]+:[0-9]+:[[:space:]]*(//|\*|/\*)' || true)"

# A selector split across lines is valid Go and gofmt keeps it split:
# `return st.` then `SaveUpstreamServer(sc)` (review round 8). Join each code
# line ending in `.` with the next code line (blank lines, line comments and
# whole-line block-comment lines in between are skipped) and apply the same
# match to the method name that starts it.
files="$(find "${dirs[@]}" -type f -name '*.go' ! -name '*_test.go' 2>/dev/null \
  | grep -vE '^internal/storage/' \
  | grep -vE '^internal/runtime/commit[^/]*\.go$' || true)"
if [[ -n "$files" ]]; then
  split_hits="$(printf '%s\n' "$files" | tr '\n' '\0' | xargs -0 awk -v re="^(${methods})([^A-Za-z0-9_]|$)" '
    FNR == 1 { prev = "" }
    {
      code = $0
      sub(/\/\/.*$/, "", code)
      gsub(/^[ \t]+|[ \t]+$/, "", code)
      if (code == "" || code ~ /^(\*|\/\*)/) next
      if (prev ~ /\.$/ && code ~ re) print FILENAME ":" FNR ":" $0
      prev = code
    }' 2>/dev/null || true)"
  if [[ -n "$split_hits" ]]; then
    hits="${hits:+$hits
}$split_hits"
  fi
fi

if [[ -n "$hits" ]]; then
  echo "Spec 116 FR-009: upstreams rows written outside the config commit (use runtime.Commit):"
  echo "$hits"
  exit 1
fi
echo "Spec 116 FR-009: every upstreams row write goes through the config commit"
exit 0

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
# matched too, as is a comment or blanks between the dot and the method name.
# Comments and string literals are ignored; block comments are
# tracked across lines, so a code line starting with `*` (`*save = ...`) is
# still scanned.
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

# One pass over every non-test Go file outside the allowed writers. Each line
# is reduced to its CODE by a small Go lexer that carries state across lines:
# block comments (`/* ... */`, including continuation lines that start with
# `*`), line comments, interpreted strings, rune literals and raw strings
# (which may span lines) are removed. Tracking the block-comment state rather
# than skipping every line that starts with `*` matters: `*save =
# st.SaveUpstreamServer` is code, not a comment continuation (review round 9).
#
# On that code:
#  - a selector of one of the methods followed by anything but an identifier
#    character is a hit: a call `st.SaveUpstreamServer(sc)` AND a method value
#    or method expression (`save := st.SaveUpstreamServer`,
#    `run(st.SaveServerAsync, x)`, `(*BoltDB).SaveUpstreamServer`) that writes
#    the row when it is called later (review round 7). A longer identifier such
#    as `.SaveUpstreamServerCount` does not match.
#  - blanks may separate the dot from the method name: Go allows them, and the
#    lexer turns a comment there into a blank, so `st. /* x */
#    SaveUpstreamServer(sc)` reaches the matcher as `st.   SaveUpstreamServer(sc)`
#    (review round 10).
#  - a selector split across lines is valid Go and gofmt keeps it split:
#    `return st.` then `SaveUpstreamServer(sc)` (review round 8). A code line
#    ending in `.` joined with the next code line (lines that are blank or only
#    comments in between are skipped) is matched on the method name that starts
#    it.
files="$(find "${dirs[@]}" -type f -name '*.go' ! -name '*_test.go' 2>/dev/null \
  | grep -vE '^internal/storage/' \
  | grep -vE '^internal/runtime/commit[^/]*\.go$' || true)"
hits=""
if [[ -n "$files" ]]; then
  # shellcheck disable=SC2016 # the awk program is single-quoted on purpose
  hits="$(printf '%s\n' "$files" | tr '\n' '\0' | xargs -0 awk \
    -v sel="[.][ \t]*(${methods})([^A-Za-z0-9_]|\$)" \
    -v head="^(${methods})([^A-Za-z0-9_]|\$)" '
    function strip(line,    out, i, n, c, nx, q, d) {
      out = ""; n = length(line); i = 1
      while (i <= n) {
        c = substr(line, i, 1); nx = substr(line, i + 1, 1)
        if (inblock) {
          if (c == "*" && nx == "/") { inblock = 0; i += 2; out = out " "; continue }
          i++; continue
        }
        if (inraw) {
          if (c == "`") { inraw = 0; out = out "\"\"" }
          i++; continue
        }
        if (c == "/" && nx == "/") break
        if (c == "/" && nx == "*") { inblock = 1; i += 2; continue }
        if (c == "`") { inraw = 1; i++; continue }
        if (c == "\"" || c == "\047") {
          q = c; i++
          while (i <= n) {
            d = substr(line, i, 1)
            if (d == "\\") { i += 2; continue }
            i++
            if (d == q) break
          }
          out = out q q; continue
        }
        out = out c; i++
      }
      return out
    }
    FNR == 1 { prev = ""; inblock = 0; inraw = 0 }
    {
      code = strip($0)
      gsub(/^[ \t]+|[ \t]+$/, "", code)
      if (code == "") next
      if (code ~ sel || (prev ~ /\.$/ && code ~ head)) print FILENAME ":" FNR ":" $0
      prev = code
    }' 2>/dev/null || true)"
fi

if [[ -n "$hits" ]]; then
  echo "Spec 116 FR-009: upstreams rows written outside the config commit (use runtime.Commit):"
  echo "$hits"
  exit 1
fi
echo "Spec 116 FR-009: every upstreams row write goes through the config commit"
exit 0

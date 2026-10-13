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
# ROOT defaults to the repository root. Exit 0 = clean, 1 = a hit (printed),
# 2 = the scan could not run (file discovery or the scanner failed).
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
# An operational failure (file discovery or the scanner itself) exits 2: a scan
# that could not run proves nothing, so it never reports a clean tree (review
# rounds 18-20).
# The Go-code lexer shared by both scans below (strips comments and string
# literals, tracking block comments and raw strings across lines).
# shellcheck disable=SC2016 # awk source, single-quoted on purpose
STRIP_AWK='
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
'

if ! listing="$(find "${dirs[@]}" -type f -name '*.go' ! -name '*_test.go')"; then
  echo "upstream write sites: file discovery failed" >&2
  exit 2
fi
files=()
while IFS= read -r f; do
  [[ -n "$f" ]] || continue
  case "$f" in
    internal/storage/*) continue ;;
  esac
  [[ "$f" =~ ^internal/runtime/commit[^/]*\.go$ ]] && continue
  files+=("$f")
done <<<"$listing"
hits=""
if [[ ${#files[@]} -gt 0 ]]; then
  # awk is called directly (no xargs) so its own exit status is the one seen.
  # shellcheck disable=SC2016 # the awk program is single-quoted on purpose
  hits="$(awk \
    -v sel="[.][ \t]*(${methods})([^A-Za-z0-9_]|\$)" \
    -v head="^(${methods})([^A-Za-z0-9_]|\$)" '
'"$STRIP_AWK"'
    FNR == 1 { prev = ""; inblock = 0; inraw = 0 }
    {
      code = strip($0)
      gsub(/^[ \t]+|[ \t]+$/, "", code)
      if (code == "") next
      if (code ~ sel || (prev ~ /\.$/ && code ~ head)) print FILENAME ":" FNR ":" $0
      prev = code
    }' "${files[@]}")"
  rc=$?
  if [[ $rc -ne 0 ]]; then
    echo "upstream write sites: scanner failed (awk exit $rc)" >&2
    exit 2
  fi
fi

# Spec 116 PR3 (FR-020, FR-020a, T076): the upstream manager's registry is
# changed only by generation-bound calls.
#  - internal/runtime and internal/server (non-test) never call, or take a
#    method value of, the unbound legacy mutators AddServerConfig /
#    AddServer / RemoveServer / DisconnectServer on the manager (a receiver
#    named upstreamManager, UpstreamManager(), manager or upstream); they use
#    the *At forms.
#  - internal/upstream/*.go (non-test) never calls them internally (m.X(...)),
#    and only unregisterLocked / installLocked delete from, clear or assign
#    into m.clients. An assignment is found by its operator, not by its
#    shape: the left-hand side of the first assignment operator (`=`, `:=`,
#    `op=`; never `==`, `!=`, `<=`, `>=`) of each statement is split on its
#    top-level commas, and any operand that is m.clients or an index of it is
#    a write, so a multi-target assignment (`m.clients[name], ok = c, true`)
#    is one too (Sol round 2). The keyword of an `if`, `else if`, `for` or
#    `switch` initializer, a `for ... = range` clause or a `select` receive
#    `case` is dropped first, so `if m.clients[n] = c; ok {` is a write too
#    (Sol round 3).
#  A statement split across lines (a code line ending in `.`, `(`, `[`, `,`
#  or `=` continues on the next code line, and so does a line that leaves a
#  `(` or `[` open as its innermost open delimiter: `m.clients[prefix+` then
#  `name] = c`, Sol round 4) is matched as one logical line. Delimiter depth
#  is tracked on the lexed code across lines; a `{` (a func literal or a
#  composite literal body) ends the continuation, so the statements inside a
#  func literal passed as an argument are still separate lines.
#  The enclosing function is tracked on the lexed CODE, and only from a
#  top-level declaration (a line starting in column 0): a declaration inside a
#  raw string, a string or a comment never changes it (Sol round 2).
#  Not covered (documented): writes through an alias of the map
#  (`cs := m.clients; cs[n] = c`); review catches those.
pr3_files=()
while IFS= read -r f; do
  [[ -n "$f" ]] || continue
  case "$f" in
    internal/runtime/*|internal/server/*|internal/upstream/*) pr3_files+=("$f") ;;
  esac
done <<<"$listing"
hits2=""
if [[ ${#pr3_files[@]} -gt 0 ]]; then
  # shellcheck disable=SC2016 # the awk program is single-quoted on purpose
  hits2="$(awk '
'"$STRIP_AWK"'
    # writes_clients reports whether one logical line assigns to m.clients
    # or an index of it in the left-hand side of an assignment.
    function writes_clients(line,    segs, ns, k, seg, n, i, c, depth, nx, pv, pp, lhs, el, ne, j) {
      ns = split(line, segs, /[;{}]/)
      for (k = 1; k <= ns; k++) {
        seg = segs[k]; n = length(seg); depth = 0; lhs = ""
        for (i = 1; i <= n; i++) {
          c = substr(seg, i, 1)
          if (c == "(" || c == "[") { depth++; continue }
          if (c == ")" || c == "]") { if (depth > 0) depth--; continue }
          if (depth > 0 || c != "=") continue
          nx = substr(seg, i + 1, 1); pv = substr(seg, i - 1, 1); pp = substr(seg, i - 2, 1)
          if (nx == "=") { i++; continue }                      # ==
          if (pv == "!" || pv == "=") continue                  # !=
          if ((pv == "<" || pv == ">") && pp != pv) continue    # <= >=
          lhs = substr(seg, 1, i - 1)
          sub(/(<<|>>|&\^|[-:+*\/%&|^])$/, "", lhs)
          # The statement keyword that introduces a simple statement is not
          # part of its left-hand side: `if m.clients[n] = c; ...`, `for`,
          # `switch` and `else if` initializers, a `for ... = range` clause
          # and a `select` receive `case m.clients[n] = <-ch:` (Sol round 3).
          if (match(lhs, /^[ \t]*((else[ \t]+)?if|for|switch|case)[^A-Za-z0-9_]/))
            lhs = substr(lhs, RLENGTH)
          break
        }
        if (lhs == "") continue
        ne = 0; el = ""; depth = 0
        for (j = 1; j <= length(lhs); j++) {
          c = substr(lhs, j, 1)
          if (c == "(" || c == "[") depth++
          if (c == ")" || c == "]") depth--
          if (c == "," && depth == 0) { if (is_clients(el)) return 1; el = ""; continue }
          el = el c
        }
        if (is_clients(el)) return 1
      }
      return 0
    }
    function is_clients(el) {
      gsub(/^[ \t(*]+|[ \t)]+$/, "", el)
      return el ~ /^m[ \t]*[.][ \t]*clients[ \t)]*(\[.*\])?$/
    }
    # track updates the open-delimiter stack (one character per open `(`,
    # `[` or `{`) with one line of lexed code; it persists across lines.
    function track(line,    i, n, c) {
      n = length(line)
      for (i = 1; i <= n; i++) {
        c = substr(line, i, 1)
        if (c == "(" || c == "[" || c == "{") stack = stack c
        else if ((c == ")" || c == "]" || c == "}") && stack != "") stack = substr(stack, 1, length(stack) - 1)
      }
    }
    FNR == 1 { inblock = 0; inraw = 0; fn = ""; joined = ""; stack = ""; cont = 0 }
    {
      raw = $0
      code = strip(raw)
      decl = code
      sub(/^[ \t]+/, "", decl)
      if (raw !~ /^[ \t]/ && decl ~ /^func[ \t(]/) {
        fn = decl
        sub(/^func[ \t]*(\([^)]*\)[ \t]*)?/, "", fn)
        sub(/[^A-Za-z0-9_].*$/, "", fn)
        joined = ""; stack = ""; cont = 0
      }
      gsub(/^[ \t]+|[ \t]+$/, "", code)
      if (code == "") next
      # A statement continued on the next line (the code ends with `.`, `(`,
      # `[`, `,` or `=`) is matched as one logical line, so a selector, a
      # call or an assignment split across lines is still seen (Sol round 1).
      # It also continues while a `(` or `[` it opened is still the innermost
      # open delimiter (Sol round 4): `m.clients[prefix+` then `name] = c`.
      # Inside a func literal body the innermost open delimiter is `{`, so
      # its statements stay separate logical lines.
      if (joined != "" && (cont || joined ~ /[.([,=]$/)) joined = joined " " code
      else joined = code
      track(code)
      cont = (stack ~ /[([]$/)
      # A legacy method followed by anything but an identifier character:
      # a call AND a method value (`remove := r.upstreamManager.RemoveServer`).
      legacy = "[.][ \t]*(AddServerConfig|AddServer|RemoveServer|DisconnectServer)([^A-Za-z0-9_]|$)"
      hit = 0
      if (FILENAME ~ /^internal\/(runtime|server)\//) {
        if (joined ~ ("(^|[^A-Za-z0-9_])(upstreamManager|manager|upstream)[ \t]*" legacy) || joined ~ ("UpstreamManager[(][)][ \t]*" legacy))
          hit = 1
      } else if (FILENAME ~ /^internal\/upstream\/[^\/]*\.go$/) {
        if (joined ~ ("(^|[^A-Za-z0-9_])m[ \t]*" legacy)) hit = 1
        else if (fn != "unregisterLocked" && fn != "installLocked" && (joined ~ /(delete|clear)[ \t]*[(][ \t(]*m[ \t]*[.][ \t]*clients[ \t)]*[,)]/ || writes_clients(joined)))
          hit = 1
      }
      if (hit) { print FILENAME ":" FNR ":" raw; joined = "" }
    }' "${pr3_files[@]}")"
  rc=$?
  if [[ $rc -ne 0 ]]; then
    echo "upstream write sites: registry scanner failed (awk exit $rc)" >&2
    exit 2
  fi
fi

status=0
if [[ -n "$hits" ]]; then
  echo "Spec 116 FR-009: upstreams rows written outside the config commit (use runtime.Commit):"
  echo "$hits"
  status=1
else
  echo "Spec 116 FR-009: every upstreams row write goes through the config commit"
fi
if [[ -n "$hits2" ]]; then
  echo "Spec 116 FR-020: unbound upstream manager registry mutations (use the generation-bound *At methods; only unregisterLocked/installLocked touch m.clients):"
  echo "$hits2"
  status=1
else
  echo "Spec 116 FR-020: every upstream manager registry mutation is generation-bound"
fi
exit $status

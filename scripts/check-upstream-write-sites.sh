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
#    A parenthesized receiver or a (*upstream.Manager) method expression is
#    the same call (Sol round 6).
#  - internal/upstream/*.go (non-test) never calls them internally: no
#    selector of them appears at all, whatever the receiver spelling (m.X,
#    (m).X, (*m).X, an alias, a method expression (*Manager).X; Sol round 6),
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
#  or `=` continues on the next code line, and so does a line that leaves any
#  `(` or `[` open: `m.clients[prefix+` then `name] = c`, Sol round 4, and a
#  composite literal split across lines inside an index, Sol round 5) is
#  matched as one logical line. Delimiter depth is tracked on the lexed code
#  across lines. Statements are split by brace NESTING, not on every brace
#  (Sol round 5): a `{...}` body is checked as its own statements and leaves
#  a `{}` placeholder in the enclosing statement, so
#  `m.clients[strings.Join([]string{prefix, name}, "")] = c` keeps its
#  left-hand side, and the statements inside a func literal passed as an
#  argument (joined with `;`) are still checked one by one.
#  The enclosing function is tracked on the lexed CODE, and only from a
#  top-level declaration (a line starting in column 0): a declaration inside a
#  raw string, a string or a comment never changes it (Sol round 2).
#  The receiver of a .clients write, delete or clear may be spelled in any
#  way, `m`, `(m)`, `(*m)` or an alias of the manager (Sol round 7).
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
    # segment splits one logical line into statements (SEG[1..NSEG]) by
    # brace nesting, not by a flat split on every brace (Sol round 5): the
    # body of each `{...}` is its own statement list (split on `;`), and the
    # enclosing statement keeps a `{}` placeholder in its place. So a
    # composite literal inside an index expression,
    # `m.clients[strings.Join([]string{prefix, name}, "")] = c`, leaves the
    # outer statement whole, and the statements of a func literal body are
    # still checked one by one. A `}` closing a brace opened on an earlier
    # logical line ends the current statement.
    function segment(line,    n, i, c, lvl, buf) {
      NSEG = 0; lvl = 0; buf[0] = ""; n = length(line)
      for (i = 1; i <= n; i++) {
        c = substr(line, i, 1)
        if (c == "{") { buf[lvl] = buf[lvl] "{}"; lvl++; buf[lvl] = ""; continue }
        if (c == "}") {
          SEG[++NSEG] = buf[lvl]; buf[lvl] = ""
          if (lvl > 0) lvl--
          continue
        }
        if (c == ";") { SEG[++NSEG] = buf[lvl]; buf[lvl] = ""; continue }
        buf[lvl] = buf[lvl] c
      }
      for (; lvl >= 0; lvl--) SEG[++NSEG] = buf[lvl]
    }
    function writes_clients(line,    k, seg, n, i, c, depth, nx, pv, pp, lhs, el, ne, j) {
      segment(line)
      for (k = 1; k <= NSEG; k++) {
        seg = SEG[k]; n = length(seg); depth = 0; lhs = ""
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
      # Any receiver spelling (Sol round 7): `m`, `(m)`, `(*m)`, `( m )` and an
      # alias `mm` all name the same registry; leading `(`/`*` are stripped
      # above, a `)` may follow the receiver identifier.
      return el ~ /^[A-Za-z_][A-Za-z0-9_]*[ \t)]*[.][ \t]*clients[ \t)]*(\[.*\])?$/
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
      } else if (raw !~ /^[ \t]/ && decl ~ /^(}|(var|const|type|import)([ \t(]|$))/) {
        # The helper exemption covers only the helper BODY (Sol round 8):
        # gofmt closes a top-level func at a column-zero `}`, and any new
        # top-level declaration ends it too, so a package-level
        # `var x = func(m *Manager, ...) { m.clients[n] = c }` declared after
        # installLocked/unregisterLocked is not exempt. The column-zero
        # `) {` line of a multi-line signature does not clear it, and the
        # lexed code (not the raw line) is tested, so a `}` inside a raw
        # string or comment does not either.
        fn = ""
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
      # Since Sol round 5 it continues while ANY `(` or `[` it opened is
      # still open, even under a `{` opened after it, so a composite literal
      # split across lines inside an index (`m.clients[f([]string{` ...
      # `}, "")] = c`) or a func literal body passed as an argument is one
      # logical line. A line that starts a new statement inside such a `{`
      # (the innermost open delimiter is `{` and the previous line does not
      # end in a continuation token) is joined with `;`, so segment() keeps
      # each statement of the body separate.
      if (joined != "" && joined ~ /[.([,=]$/) joined = joined " " code
      else if (joined != "" && cont && stack ~ /[{]$/) joined = joined "; " code
      else if (joined != "" && cont) joined = joined " " code
      else joined = code
      track(code)
      cont = (stack ~ /[([]/)
      # A legacy method followed by anything but an identifier character:
      # a call AND a method value (`remove := r.upstreamManager.RemoveServer`).
      legacy = "[.][ \t]*(AddServerConfig|AddServer|RemoveServer|DisconnectServer)([^A-Za-z0-9_]|$)"
      hit = 0
      if (FILENAME ~ /^internal\/(runtime|server)\//) {
        # The receiver may be parenthesized, `(r.upstreamManager).RemoveServer`,
        # or a method expression, `(*upstream.Manager).RemoveServer(mgr, n)`
        # (Sol round 6).
        if (joined ~ ("(^|[^A-Za-z0-9_])(upstreamManager|manager|upstream|Manager)[ \t)]*" legacy) || joined ~ ("UpstreamManager[(][)][ \t)]*" legacy))
          hit = 1
      } else if (FILENAME ~ /^internal\/upstream\/[^\/]*\.go$/) {
        # Nothing in the package calls a legacy mutator, so ANY selector of
        # one is a hit whatever the receiver spelling: `m.X`, `(m).X`,
        # `(*m).X`, an alias `mm.X`, a method expression `(*Manager).X`
        # (Sol round 6). The declarations `func (m *Manager) X(` have no dot.
        if (joined ~ legacy) hit = 1
        else if (fn != "unregisterLocked" && fn != "installLocked" && (joined ~ /(^|[^A-Za-z0-9_.])(delete|clear)[ \t]*[(][ \t(*]*[A-Za-z_][A-Za-z0-9_]*[ \t)]*[.][ \t]*clients[ \t)]*[,)]/ || writes_clients(joined)))
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

# Spec 116 PR4 (FR-024a, T099): upstream HTTP never bypasses the instance's
# dial gate. In internal/oauth, internal/upstream/core and internal/transport
# (non-test) these are hits:
#  - the package-level helpers http.Get/Head/Post/PostForm( (they use
#    http.DefaultClient), http.DefaultClient, http.DefaultTransport;
#  - an http.Client{ composite literal with no Transport: field before its
#    closing brace (its zero Transport is http.DefaultTransport).
# In internal/upstream/** a GetTransport() call outside
# internal/upstream/core/gate.go is a hit: core asserts transport
# capabilities only on the unwrapped transport, through innerTransport
# (FR-023b, review R8.1).
# scripts/ungated-http-allowlist.txt lists the deliberate exceptions, one
# `<path> <text>` pair per line: a hit in <path> whose line contains <text>
# is allowed (the process-default fallbacks used outside an instance).
allowlist="scripts/ungated-http-allowlist.txt"
pr4_files=()
while IFS= read -r f; do
  [[ -n "$f" ]] || continue
  case "$f" in
    internal/oauth/*|internal/upstream/core/*|internal/transport/*|internal/upstream/*) pr4_files+=("$f") ;;
  esac
done <<<"$listing"
hits3=""
if [[ ${#pr4_files[@]} -gt 0 ]]; then
  [[ -f "$allowlist" ]] || : >/dev/null
  # shellcheck disable=SC2016 # the awk program is single-quoted on purpose
  hits3="$(awk -v allowfile="$allowlist" '
'"$STRIP_AWK"'
    BEGIN {
      na = 0
      while ((getline line < allowfile) > 0) {
        if (line ~ /^[ \t]*(#|$)/) continue
        sp = index(line, " ")
        if (sp == 0) continue
        na++; apath[na] = substr(line, 1, sp - 1); atext[na] = substr(line, sp + 1)
      }
    }
    function allowed(file, raw,    k) {
      for (k = 1; k <= na; k++) if (apath[k] == file && index(raw, atext[k]) > 0) return 1
      return 0
    }
    function report(file, n, raw) {
      if (!allowed(file, raw)) print file ":" n ":" raw
    }
    # scan_clients tracks every http.Client{ literal with brace depth
    # (review rounds 1 and 2): each open literal is on a stack with the depth
    # of its own body, and a Transport: key counts only at that depth. A
    # Transport: of a nested literal (another client, a struct, a func body)
    # never excuses the outer one, and the closing brace of a nested func
    # literal does not close the client. A literal that closes without a
    # Transport: key is a hit, reported at its opening line; one still open
    # at the end of its file too. bd is the brace depth of the file.
    #
    # Slice, array and map literals of clients (review round 5):
    # []http.Client{, [N]http.Client{, [...]http.Client{,
    # map[K]http.Client{ (and the *http.Client element forms) open a
    # container, kept on its own stack with its depth and its dimension
    # count (the bracket groups before the element type). The client
    # literals are its elements, whose type is elided: a brace at the
    # container depth plus its dimensions that starts an element (it
    # follows {, a comma, a key colon, or starts its line). An explicit
    # http.Client{ element is a client in its own right. A type followed by
    # its brace right after a closing parenthesis is a func result type
    # before the body, not a literal.
    function scan_clients(code, file, n, raw,    i, L, ch, pc, pre, m, dims) {
      L = length(code)
      for (i = 1; i <= L; i++) {
        ch = substr(code, i, 1)
        if (ch == "{") {
          bd++
          pre = substr(code, 1, i - 1)
          # The type may be spaced from its brace and its selector dot
          # spaced too (review round 3): `http.Client /* x */ {` (strip
          # turns the comment into a space) and `http . Client{` are the
          # same literal. A brace on a later line cannot open it (Go
          # inserts a semicolon after Client).
          if (pre ~ /[)][ \t]*((map[ \t]*)?\[[^]]*\][ \t]*)*[*]?[ \t]*http[ \t]*[.][ \t]*Client[ \t]*$/) {
            # a func result type: the brace opens the body
          } else if (match(pre, /(^|[^A-Za-z0-9_.])((map[ \t]*)?\[[^]]*\][ \t]*)+[*]?[ \t]*http[ \t]*[.][ \t]*Client[ \t]*$/)) {
            m = substr(pre, RSTART, RLENGTH)
            sub(/[*]?[ \t]*http[ \t]*[.][ \t]*Client[ \t]*$/, "", m)
            dims = gsub(/\[/, "[", m)
            nc++; cdepth[nc] = bd; cdims[nc] = dims
          } else if (pre ~ /(^|[^*A-Za-z0-9_.])http[ \t]*[.][ \t]*Client[ \t]*$/ || (nc > 0 && bd == cdepth[nc] + cdims[nc] && pre ~ /(^|[{,:])[ \t]*$/)) {
            ns++; sdepth[ns] = bd; shas[ns] = 0; sfile[ns] = file; sline[ns] = n; sraw[ns] = raw
          }
        } else if (ch == "}") {
          if (ns > 0 && sdepth[ns] == bd) {
            if (!shas[ns]) report(sfile[ns], sline[ns], sraw[ns])
            ns--
          }
          if (nc > 0 && cdepth[nc] == bd) nc--
          bd--
        } else if (ch == "T" && ns > 0 && bd == sdepth[ns] && substr(code, i, 9) == "Transport") {
          pc = (i > 1) ? substr(code, i - 1, 1) : ""
          if (pc !~ /[A-Za-z0-9_.]/ && substr(code, i + 9) ~ /^[ \t]*:/) shas[ns] = 1
        }
      }
    }
    function flush_clients(    k) {
      for (k = 1; k <= ns; k++) if (!shas[k]) report(sfile[k], sline[k], sraw[k])
      ns = 0; nc = 0; bd = 0
    }
    FNR == 1 {
      flush_clients()
      inblock = 0; inraw = 0
      hpend = 0
    }
    {
      raw = $0
      code = strip(raw)
      http_scope = (FILENAME ~ /^internal\/(oauth|transport|upstream\/core)\//)
      if (http_scope) {
        # A selector split after its dot (review round 4): Go inserts no
        # semicolon after a dot, so `http.` ending a line continues on the
        # next code line, past blank and comment-only lines (strip blanks
        # comments). The pending `http.` is prefixed to that line, so the
        # helper, Default* and Client{ checks see one selector. The state is
        # reset per file.
        if (hpend) {
          if (code ~ /^[ \t]*$/) next
          code = "http." code
          hpend = 0
        }
        if (code ~ /(^|[^A-Za-z0-9_.])http[ \t]*[.][ \t]*$/) {
          hpend = 1
          sub(/http[ \t]*[.][ \t]*$/, "", code)
        }
        if (code ~ /(^|[^A-Za-z0-9_.])http[ \t]*[.][ \t]*(Get|Head|Post|PostForm)[ \t]*[(]/ || code ~ /(^|[^A-Za-z0-9_.])http[ \t]*[.][ \t]*Default(Client|Transport)([^A-Za-z0-9_]|$)/)
          report(FILENAME, FNR, raw)
        scan_clients(code, FILENAME, FNR, raw)
      }
      if (FILENAME ~ /^internal\/upstream\// && FILENAME != "internal/upstream/core/gate.go" && code ~ /GetTransport[ \t]*[(][ \t]*[)]/)
        report(FILENAME, FNR, raw)
    }
    END { flush_clients() }' "${pr4_files[@]}")"
  rc=$?
  if [[ $rc -ne 0 ]]; then
    echo "upstream write sites: ungated-HTTP scanner failed (awk exit $rc)" >&2
    exit 2
  fi
fi

# Spec 116 PR2b (#1607, T055d, review R6.2, R8.3): every OAuth write, clear
# and DCR save an upstream instance makes is bound to its credential binding
# (storage/oauth_binding.go). In internal/upstream/** and internal/oauth/**
# (non-test) a selector of one of the unconditional oauth_tokens mutators is
# a hit (a call or a method value, also split across lines). The one exported
# current-state clear, ClearOAuthTokenForStandaloneLogout, is allowed in
# exactly one file, internal/upstream/cli/client.go (the standalone CLI
# logout), and is a hit in every other non-test file outside internal/storage.
oauth_methods='SaveOAuthToken|UpdateOAuthToken|DeleteOAuthToken|UpdateOAuthClientCredentials|SaveOAuthClientCredentialsIfUnsetOrSame|SaveOAuthClientCredentialsIfSameOrEmpty|ClearOAuthClientCredentials|ClearOAuthClientCredentialsIf|ClearOAuthClientCredentialsIfClientID'
standalone_method='ClearOAuthTokenForStandaloneLogout'
pr2b_files=()
for f in "${files[@]}"; do
  pr2b_files+=("$f")
done
hits4=""
if [[ ${#pr2b_files[@]} -gt 0 ]]; then
  # shellcheck disable=SC2016 # the awk program is single-quoted on purpose
  hits4="$(awk \
    -v sel="[.][ \t]*(${oauth_methods})([^A-Za-z0-9_]|\$)" \
    -v head="^(${oauth_methods})([^A-Za-z0-9_]|\$)" \
    -v ssel="[.][ \t]*(${standalone_method})([^A-Za-z0-9_]|\$)" \
    -v shead="^(${standalone_method})([^A-Za-z0-9_]|\$)" '
'"$STRIP_AWK"'
    FNR == 1 {
      prev = ""; inblock = 0; inraw = 0
      bound = (FILENAME ~ /^internal\/(upstream|oauth)\//)
      standalone_ok = (FILENAME == "internal/upstream/cli/client.go")
    }
    {
      code = strip($0)
      gsub(/^[ \t]+|[ \t]+$/, "", code)
      if (code == "") next
      if (bound && (code ~ sel || (prev ~ /\.$/ && code ~ head))) print FILENAME ":" FNR ":" $0
      else if (!standalone_ok && (code ~ ssel || (prev ~ /\.$/ && code ~ shead))) print FILENAME ":" FNR ":" $0
      prev = code
    }' "${pr2b_files[@]}")"
  rc=$?
  if [[ $rc -ne 0 ]]; then
    echo "upstream write sites: OAuth binding scanner failed (awk exit $rc)" >&2
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
if [[ -n "$hits3" ]]; then
  echo "Spec 116 FR-024a: upstream HTTP outside the instance's dial gate (use the instance transport / oauth.Env; capabilities via core.innerTransport):"
  echo "$hits3"
  status=1
else
  echo "Spec 116 FR-024a: every upstream HTTP request runs on a gated transport"
fi
if [[ -n "$hits4" ]]; then
  echo "Spec 116 PR2b: unconditional OAuth writes or clears from an upstream instance (use the storage …IfBound methods with the instance's credential binding; ClearOAuthTokenForStandaloneLogout only in internal/upstream/cli/client.go):"
  echo "$hits4"
  status=1
else
  echo "Spec 116 PR2b: every upstream OAuth write and clear is bound to the instance's credential binding"
fi
exit $status

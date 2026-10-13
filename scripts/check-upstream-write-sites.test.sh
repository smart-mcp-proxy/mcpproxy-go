#!/usr/bin/env bash
# check-upstream-write-sites.test.sh - the red/green self-test for
# check-upstream-write-sites.sh (Spec 116 T002, quickstart T1.8).
#
# Builds temp trees: a planted direct SaveUpstreamServer in internal/server
# (must exit 1), each AsyncManager queue writer (SaveServerAsync,
# DeleteServerAsync, EnableServerAsync, QuarantineServerAsync; must exit 1), the same call in a test file, in internal/storage and in
# internal/runtime/commit.go (must exit 0), the AddUpstream alias (exit 1), a
# method value such as `save := st.SaveUpstreamServer` (exit 1), a selector
# split across lines (exit 1), a code line starting with `*` such as
# `*save = st.SaveUpstreamServer` (exit 1), code after a closed block comment
# (exit 1), a block comment or blanks between the dot and the method name on
# one line or across lines (exit 1), the FR-020 legacy manager mutators as
# calls, method values or split across lines (and, Sol round 6, with a
# parenthesized receiver, an alias or a method expression such as
# `(*Manager).RemoveServer`), multiline and multi-target
# m.clients writes (any receiver spelling: (m), (*m), an alias; Sol round 7) (including an index expression split inside its brackets
# a write inside a func literal argument, and a composite literal inside the
# index expression on one line or across lines), clear(m.clients) and writes after a fake helper
# declaration in a raw string, string or block comment (exit 1), and whole-line, block and string-literal mentions (must exit 0), a failing
# find or awk (must exit 2, not report a clean tree). Then runs the real script
# on the repository (must exit 0).
set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
CHECK="$SCRIPT_DIR/check-upstream-write-sites.sh"
TMP="$(mktemp -d)" || { echo "mktemp -d failed: cannot build the self-test trees" >&2; exit 2; }
[[ -n "$TMP" && -d "$TMP" ]] || { echo "no temp directory: refusing to build trees at /" >&2; exit 2; }
trap 'rm -rf "$TMP"' EXIT

pass=0; fail=0
expect() { # desc expected-exit root
  bash "$CHECK" "$3" >/dev/null 2>&1
  local rc=$?
  if [[ "$rc" == "$2" ]]; then echo "ok   - $1 (exit $rc)"; pass=$((pass+1));
  else echo "FAIL - $1 (expected exit $2, got $rc)"; fail=$((fail+1)); fi
}

mkroot() {
  local r="$TMP/$1"
  mkdir -p "$r/internal/server" "$r/internal/storage" "$r/internal/runtime" "$r/cmd/mcpproxy"
  echo "$r"
}

clean="$(mkroot clean)"
echo 'package server' > "$clean/internal/server/a.go"
expect "clean tree" 0 "$clean"

planted="$(mkroot planted-server)"
echo '	_ = s.runtime.StorageManager().SaveUpstreamServer(sc)' > "$planted/internal/server/a.go"
expect "planted SaveUpstreamServer in internal/server" 1 "$planted"

quarantine="$(mkroot planted-quarantine)"
echo '	err := r.storageManager.QuarantineUpstreamServer(name, true)' > "$quarantine/internal/runtime/lifecycle.go"
expect "planted QuarantineUpstreamServer outside commit.go" 1 "$quarantine"

cmdplant="$(mkroot planted-cmd)"
echo '	_ = db.UpdateUpstream(name, sc)' > "$cmdplant/cmd/mcpproxy/x.go"
expect "planted UpdateUpstream in cmd" 1 "$cmdplant"

# The AsyncManager queue writes upstream rows too (internal/storage/async_ops.go).
for m in 'SaveServerAsync(sc)' 'DeleteServerAsync(name)' 'EnableServerAsync(name, true)' 'QuarantineServerAsync(name, true)'; do
  r="$(mkroot "planted-async-${m%%(*}")"
  echo "	am.$m" > "$r/internal/server/a.go"
  expect "planted ${m%%(*} in internal/server" 1 "$r"
done

# Exported aliases that write a row (internal/storage/manager.go): AddUpstream
# calls SaveUpstreamServer (review round 4).
alias="$(mkroot planted-alias)"
echo '	_, _ = s.runtime.StorageManager().AddUpstream(sc)' > "$alias/internal/server/a.go"
expect "planted AddUpstream in internal/server" 1 "$alias"

# A method VALUE (no call parenthesis) writes the row when it is called later
# (review round 7): `save := st.SaveUpstreamServer; save(sc)`.
methodval="$(mkroot planted-method-value)"
printf '\tsave := st.SaveUpstreamServer\n\t_ = save(sc)\n' > "$methodval/internal/server/a.go"
expect "planted SaveUpstreamServer method value in internal/server" 1 "$methodval"
methodarg="$(mkroot planted-method-arg)"
echo '	run(st.QuarantineServerAsync, name)' > "$methodarg/internal/server/a.go"
expect "planted QuarantineServerAsync method value passed as an argument" 1 "$methodarg"

# A selector split across lines (valid Go, kept split by gofmt) is the same
# call or method value (review round 8): `return st.` then
# `SaveUpstreamServer(sc)`, a blank line or a comment in between, or
# `(*BoltDB).` then the method value.
multicall="$(mkroot planted-multiline-call)"
printf '\treturn st.\n\t\tSaveUpstreamServer(sc)\n' > "$multicall/internal/server/a.go"
expect "planted multiline SaveUpstreamServer call in internal/server" 1 "$multicall"
multigap="$(mkroot planted-multiline-gap)"
printf '\treturn st. // the store\n\n\t\t// still the store\n\t\tQuarantineServerAsync(name, true)\n' > "$multigap/internal/server/a.go"
expect "planted multiline call across a blank line and comments" 1 "$multigap"
multival="$(mkroot planted-multiline-value)"
printf '\tsave := (*BoltDB).\n\t\tSaveUpstreamServer\n' > "$multival/internal/server/a.go"
expect "planted multiline SaveUpstreamServer method value in internal/server" 1 "$multival"

# A code line whose first non-blank character is `*` is code, not a block
# comment continuation (review round 9): `*save = st.SaveUpstreamServer` then
# `(*save)(sc)` writes the row. Block-comment state is tracked across lines,
# so genuine continuation lines stay skipped (see the allowed tree below).
deref="$(mkroot planted-deref-assign)"
printf 'package server\n\nfunc f(st *S, sc *C) {\n\tvar save *func(*C) error\n\t*save = st.SaveUpstreamServer\n\t_ = (*save)(sc)\n}\n' > "$deref/internal/server/a.go"
expect "planted SaveUpstreamServer method value assigned through a pointer (line starts with *)" 1 "$deref"
derefsplit="$(mkroot planted-deref-split)"
printf '\t*save = st.\n\t\tSaveUpstreamServer\n' > "$derefsplit/internal/server/a.go"
expect "planted split selector on a line starting with *" 1 "$derefsplit"
afterblock="$(mkroot planted-after-block)"
printf '\t/* closed */ _ = st.SaveUpstreamServer(sc)\n' > "$afterblock/internal/server/a.go"
expect "planted call after a closed block comment on the same line" 1 "$afterblock"
afterblockml="$(mkroot planted-after-multiline-block)"
printf '\t/*\n\t * notes\n\t */\n\t_ = st.SaveUpstreamServer(sc)\n' > "$afterblockml/internal/server/a.go"
expect "planted call after a multi-line block comment" 1 "$afterblockml"
strslash="$(mkroot planted-after-string-opener)"
printf '\tpat := "/*"\n\t_ = st.SaveUpstreamServer(sc)\n' > "$strslash/internal/server/a.go"
expect "planted call after a string containing /* (not a comment opener)" 1 "$strslash"

# A comment between the dot and the method name is valid, gofmt-stable Go
# (review round 10): `return st. /* bypass */ SaveUpstreamServer(sc)`. The
# lexer turns the comment into whitespace, so the selector must allow blanks
# between its tokens, on one line and across lines.
dotcomment="$(mkroot planted-dot-comment)"
printf '\treturn st. /* projection bypass */ SaveUpstreamServer(sc)\n' > "$dotcomment/internal/server/a.go"
expect "planted call with a block comment between the dot and the method" 1 "$dotcomment"
dotcommentml="$(mkroot planted-dot-comment-multiline)"
printf '\treturn st. /* projection\n\t   bypass */ SaveUpstreamServer(sc)\n' > "$dotcommentml/internal/server/a.go"
expect "planted call with a multi-line block comment between the dot and the method" 1 "$dotcommentml"
dotcommentboth="$(mkroot planted-comment-around-dot)"
printf '\tsave := st /* a */ . /* b */ QuarantineServerAsync\n' > "$dotcommentboth/internal/server/a.go"
expect "planted method value with block comments around the dot" 1 "$dotcommentboth"
dotspace="$(mkroot planted-dot-space)"
printf '\t_ = st.\t SaveServerAsync(sc)\n' > "$dotspace/internal/server/a.go"
expect "planted call with blanks between the dot and the method" 1 "$dotspace"

allowed="$(mkroot allowed)"
echo '	_ = m.SaveUpstreamServer(sc)' > "$allowed/internal/storage/manager.go"
echo '	save := m.SaveUpstreamServer' > "$allowed/internal/storage/value.go"
echo '	f := r.storageManager.ApplyServerCommit' > "$allowed/internal/runtime/commit_apply.go"
echo '	save := st.SaveUpstreamServer' > "$allowed/internal/server/value_test.go"
echo '	_ = st.SaveUpstreamServerCount' > "$allowed/internal/server/longer.go"
echo '	_, err := m.AddUpstream(sc)' > "$allowed/internal/storage/alias.go"
echo '	_, _ = st.AddUpstream(sc)' > "$allowed/internal/server/c_test.go"
echo '	err := r.storageManager.ApplyServerCommit(batch)' > "$allowed/internal/runtime/commit.go"
echo '	require.NoError(t, st.SaveUpstreamServer(sc))' > "$allowed/internal/server/a_test.go"
echo '	// a direct SaveUpstreamServer( here would be a second source' > "$allowed/internal/server/b.go"
printf '\treturn st.\n\t\tSaveUpstreamServer(sc)\n' > "$allowed/internal/storage/multi.go"
printf '\treturn st.\n\t\tSaveUpstreamServer(sc)\n' > "$allowed/internal/server/multi_test.go"
printf '\tx := st.\n\t\tSaveUpstreamServerCount\n' > "$allowed/internal/server/multi_longer.go"
printf '\tx := st. /* c */ SaveUpstreamServerCount\n' > "$allowed/internal/server/comment_longer.go"
printf '\t// ends with a dot.\n\tSaveUpstreamServer := 1\n' > "$allowed/internal/server/not_selector.go"
printf '/*\n * a direct st.SaveUpstreamServer(sc) here would be a second source\n *save = st.SaveUpstreamServer\n */\n' > "$allowed/internal/server/block.go"
printf '\t/* st.SaveUpstreamServer(sc) */\n' > "$allowed/internal/server/block_inline.go"
# shellcheck disable=SC2016 # literal backticks: a Go raw string
printf '\tmsg := "st.SaveUpstreamServer(sc)"\n\tdoc := `\n st.QuarantineServerAsync(name, true)\n`\n' > "$allowed/internal/server/strings.go"
expect "storage, commit.go, tests and comments are allowed" 0 "$allowed"

# Spec 116 PR3 (FR-020, T076): unbound manager registry mutations.
for line in '	r.upstreamManager.RemoveServer(name)' '	_ = r.upstreamManager.AddServer(name, cfg)' \
            '	_ = p.manager.AddServerConfig(name, cfg)' '	_ = rt.UpstreamManager().DisconnectServer(name)'; do
  r="$(mkroot "pr3-runtime-$RANDOM")"
  echo "$line" > "$r/internal/runtime/lifecycle.go"
  expect "planted unbound manager mutation in internal/runtime: ${line#	}" 1 "$r"
done
r="$(mkroot pr3-server)"
echo '	s.runtime.UpstreamManager().RemoveServer(name)' > "$r/internal/server/a.go"
expect "planted unbound manager mutation in internal/server" 1 "$r"
r="$(mkroot pr3-bound-ok)"
printf '	_ = r.upstreamManager.AddServerAt(name, cfg, gen)
	_ = tx.RemoveServer(name)
	s.stateView.RemoveServer(name)
	// r.upstreamManager.RemoveServer(name)
' > "$r/internal/runtime/lifecycle.go"
expect "bound calls, other receivers and comments are allowed in internal/runtime" 0 "$r"
r="$(mkroot pr3-manager-internal)"
mkdir -p "$r/internal/upstream"
printf 'func (m *Manager) ForceReconnectAll() {
	m.RemoveServer(id)
}
' > "$r/internal/upstream/manager.go"
expect "planted internal legacy call in internal/upstream/manager.go" 1 "$r"
r="$(mkroot pr3-manager-delete)"
mkdir -p "$r/internal/upstream"
printf 'func (m *Manager) Other(id string) {
	delete(m.clients, id)
}
' > "$r/internal/upstream/manager.go"
expect "planted delete(m.clients) outside unregisterLocked" 1 "$r"
r="$(mkroot pr3-manager-assign)"
mkdir -p "$r/internal/upstream"
printf 'func (m *Manager) Other(id string, c *managed.Client) {
	m.clients[id] = c
}
' > "$r/internal/upstream/manager.go"
expect "planted m.clients[id] = outside installLocked" 1 "$r"
# Sol round 1: a method value, a call split across lines, a multiline
# delete and a multiline assignment are the same unbound mutations.
r="$(mkroot pr3-method-value)"
printf '\tremove := r.upstreamManager.RemoveServer\n\tremove(name)\n' > "$r/internal/runtime/lifecycle.go"
expect "planted legacy method value in internal/runtime" 1 "$r"
r="$(mkroot pr3-method-value-arg)"
printf '\trun(s.runtime.UpstreamManager().DisconnectServer, name)\n' > "$r/internal/server/a.go"
expect "planted legacy method value passed as an argument in internal/server" 1 "$r"
r="$(mkroot pr3-multiline-call)"
printf '\tr.upstreamManager.\n\t\tRemoveServer(name)\n' > "$r/internal/runtime/lifecycle.go"
expect "planted legacy call split across lines in internal/runtime" 1 "$r"
r="$(mkroot pr3-multiline-call-gap)"
printf '\tr.upstreamManager. // the manager\n\n\t\t// still the manager\n\t\tAddServer(name, cfg)\n' > "$r/internal/runtime/lifecycle.go"
expect "planted legacy call split across a blank line and comments" 1 "$r"
r="$(mkroot pr3-manager-internal-value)"
mkdir -p "$r/internal/upstream"
printf 'func (m *Manager) Other() {\n\tf := m.RemoveServer\n\t_ = f\n}\n' > "$r/internal/upstream/manager.go"
expect "planted internal legacy method value in internal/upstream" 1 "$r"
r="$(mkroot pr3-manager-internal-multiline)"
mkdir -p "$r/internal/upstream"
printf 'func (m *Manager) Other() {\n\t_ = m.\n\t\tDisconnectServer(id)\n}\n' > "$r/internal/upstream/manager.go"
expect "planted internal legacy call split across lines in internal/upstream" 1 "$r"
r="$(mkroot pr3-manager-delete-multiline)"
mkdir -p "$r/internal/upstream"
printf 'func (m *Manager) Other(name string) {\n\tdelete(\n\t\tm.clients, name)\n}\n' > "$r/internal/upstream/manager.go"
expect "planted multiline delete(m.clients) outside unregisterLocked" 1 "$r"
r="$(mkroot pr3-manager-assign-multiline)"
mkdir -p "$r/internal/upstream"
printf 'func (m *Manager) Other(name string, client *managed.Client) {\n\tm.clients[name] =\n\t\tclient\n}\n' > "$r/internal/upstream/manager.go"
expect "planted multiline m.clients[name] = outside installLocked" 1 "$r"
r="$(mkroot pr3-bound-multiline-ok)"
printf '\t_ = r.upstreamManager.\n\t\tAddServerAt(name, cfg, gen)\n\tremove := r.upstreamManager.RemoveServerAt\n\t_ = remove\n\tx := r.upstreamManager.\n\t\tGetClient\n' > "$r/internal/runtime/lifecycle.go"
expect "bound calls, bound method values and reads split across lines are allowed" 0 "$r"
r="$(mkroot pr3-manager-multiline-ok)"
mkdir -p "$r/internal/upstream"
printf 'func (m *Manager) unregisterLocked(name string) *managed.Client {\n\tdelete(\n\t\tm.clients, name)\n\treturn nil\n}\n\nfunc (m *Manager) installLocked(name string, c *managed.Client) {\n\tm.clients[name] =\n\t\tc\n}\n\nfunc (m *Manager) read(name string) bool {\n\tok :=\n\t\tm.clients[name] == nil\n\treturn ok\n}\n' > "$r/internal/upstream/manager_generation.go"
expect "multiline writes in the permitted helpers and multiline reads are allowed" 0 "$r"
# Sol round 2: a multi-target assignment writes the registry too, and a fake
# helper declaration inside a raw string or a block comment never moves the
# tracked function into an allowlisted helper.
r="$(mkroot pr3-manager-multi-target)"
mkdir -p "$r/internal/upstream"
printf 'func (m *Manager) Other(name string, client *managed.Client) {\n\tvar ok bool\n\tm.clients[name], ok = client, true\n\t_ = ok\n}\n' > "$r/internal/upstream/manager.go"
expect "planted multi-target m.clients[name], ok = assignment" 1 "$r"
r="$(mkroot pr3-manager-multi-target-second)"
mkdir -p "$r/internal/upstream"
printf 'func (m *Manager) Other(name string, client *managed.Client) {\n\tvar ok bool\n\tok, m.clients[name] = true, client\n\t_ = ok\n}\n' > "$r/internal/upstream/manager.go"
expect "planted multi-target assignment with m.clients second" 1 "$r"
r="$(mkroot pr3-manager-multi-target-split)"
mkdir -p "$r/internal/upstream"
printf 'func (m *Manager) Other(name string, client *managed.Client) {\n\tvar ok bool\n\tm.clients[name],\n\t\tok = client, true\n\t_ = ok\n}\n' > "$r/internal/upstream/manager.go"
expect "planted multi-target assignment split across lines" 1 "$r"
r="$(mkroot pr3-manager-clear)"
mkdir -p "$r/internal/upstream"
printf 'func (m *Manager) Other() {\n\tclear(m.clients)\n}\n' > "$r/internal/upstream/manager.go"
expect "planted clear(m.clients) outside the helpers" 1 "$r"
r="$(mkroot pr3-manager-multi-read-ok)"
mkdir -p "$r/internal/upstream"
printf 'func (m *Manager) Other(name string) bool {\n\tvar c *managed.Client\n\tvar ok bool\n\tc, ok = m.clients[name]\n\tif d, found := m.clients[name]; found && d == c {\n\t\treturn ok\n\t}\n\tif m.clients[name] != nil || len(m.clients) >= 1 || m.clients[name] == c {\n\t\treturn true\n\t}\n\treturn ok\n}\n' > "$r/internal/upstream/manager.go"
expect "multi-target reads and comparisons of m.clients are allowed" 0 "$r"
# Sol round 3: an assignment in an if/for/switch initializer (or a select
# receive case, or a range clause) is a write too; read-only initializers stay
# allowed.
r="$(mkroot pr3-manager-if-init)"
mkdir -p "$r/internal/upstream"
printf 'func (m *Manager) Other(name string, c *managed.Client) {\n\tif m.clients[name] = c; c != nil {\n\t\treturn\n\t}\n}\n' > "$r/internal/upstream/manager.go"
expect "planted if m.clients[name] = c; initializer" 1 "$r"
r="$(mkroot pr3-manager-for-init)"
mkdir -p "$r/internal/upstream"
printf 'func (m *Manager) Other(name string, c *managed.Client) {\n\tfor m.clients[name] = c; false; {\n\t\treturn\n\t}\n}\n' > "$r/internal/upstream/manager.go"
expect "planted for m.clients[name] = c; initializer" 1 "$r"
r="$(mkroot pr3-manager-else-if-init)"
mkdir -p "$r/internal/upstream"
printf 'func (m *Manager) Other(name string, c *managed.Client) {\n\tif c == nil {\n\t\treturn\n\t} else if m.clients[name] = c; true {\n\t\treturn\n\t}\n}\n' > "$r/internal/upstream/manager.go"
expect "planted else if initializer" 1 "$r"
r="$(mkroot pr3-manager-switch-init)"
mkdir -p "$r/internal/upstream"
printf 'func (m *Manager) Other(name string, c *managed.Client) {\n\tswitch m.clients[name] = c; {\n\t}\n}\n' > "$r/internal/upstream/manager.go"
expect "planted switch initializer" 1 "$r"
r="$(mkroot pr3-manager-for-range)"
mkdir -p "$r/internal/upstream"
printf 'func (m *Manager) Other(names []string) {\n\tfor _, m.clients[names[0]] = range []*managed.Client{nil} {\n\t}\n}\n' > "$r/internal/upstream/manager.go"
expect "planted for range assigning into m.clients" 1 "$r"
r="$(mkroot pr3-manager-select-recv)"
mkdir -p "$r/internal/upstream"
printf 'func (m *Manager) Other(name string, ch chan *managed.Client) {\n\tselect {\n\tcase m.clients[name] = <-ch:\n\t}\n}\n' > "$r/internal/upstream/manager.go"
expect "planted select receive case into m.clients" 1 "$r"
r="$(mkroot pr3-manager-init-read-ok)"
mkdir -p "$r/internal/upstream"
printf 'func (m *Manager) Other(name string) bool {\n\tif c, ok := m.clients[name]; ok && c != nil {\n\t\treturn true\n\t}\n\tfor n, c := range m.clients {\n\t\t_, _ = n, c\n\t}\n\tfor i := 0; i < len(m.clients); i++ {\n\t}\n\tswitch c := m.clients[name]; {\n\tcase c == nil:\n\t}\n\tif m.clients[name] == nil {\n\t\treturn false\n\t}\n\treturn false\n}\n' > "$r/internal/upstream/manager.go"
expect "read-only if/for/switch initializers and range over m.clients are allowed" 0 "$r"
# Sol round 4: a statement continues while a `(` or `[` it opened is still
# open (delimiter depth), not only when the line ends in `.`, `(`, `[`, `,`
# or `=`: gofmt keeps `m.clients[prefix+` / `name] = c` split, and that is a
# registry write. Inside a func literal body the innermost open delimiter is
# `{`, so its statements stay separate lines.
r="$(mkroot pr3-manager-multiline-index)"
mkdir -p "$r/internal/upstream"
printf 'func (m *Manager) Other(prefix, name string, c *managed.Client) {\n\tm.clients[prefix+\n\t\tname] = c\n}\n' > "$r/internal/upstream/manager.go"
expect "planted multiline index expression m.clients[prefix+ / name] = c" 1 "$r"
r="$(mkroot pr3-manager-multiline-index-call)"
mkdir -p "$r/internal/upstream"
printf 'func (m *Manager) Other(name string, c *managed.Client) {\n\tvar ok bool\n\tm.clients[key(name,\n\t\t"x")], ok = c, true\n\t_ = ok\n}\n' > "$r/internal/upstream/manager.go"
expect "planted multi-target write with a call split inside the index" 1 "$r"
r="$(mkroot pr3-manager-funclit-write)"
mkdir -p "$r/internal/upstream"
printf 'func (m *Manager) Other(name string, c *managed.Client) {\n\trun(func() {\n\t\tx := 1\n\t\tm.clients[name] = c\n\t\t_ = x\n\t})\n}\n' > "$r/internal/upstream/manager.go"
expect "planted write inside a func literal passed as an argument" 1 "$r"
r="$(mkroot pr3-manager-multiline-index-read-ok)"
mkdir -p "$r/internal/upstream"
printf 'func (m *Manager) Other(prefix, name string) bool {\n\tc := m.clients[prefix+\n\t\tname]\n\trun(func() {\n\t\t_ = m.clients[name]\n\t})\n\treturn c == m.clients[prefix+\n\t\tname]\n}\n' > "$r/internal/upstream/manager.go"
expect "multiline index reads and reads in a func literal are allowed" 0 "$r"
r="$(mkroot pr3-manager-multiline-index-helper-ok)"
mkdir -p "$r/internal/upstream"
printf 'func (m *Manager) installLocked(prefix, name string, c *managed.Client) {\n\tm.clients[prefix+\n\t\tname] = c\n}\n' > "$r/internal/upstream/manager_generation.go"
expect "a multiline index write inside installLocked is allowed" 0 "$r"
r="$(mkroot pr3-manager-fake-helper-raw)"
mkdir -p "$r/internal/upstream"
# shellcheck disable=SC2016 # a Go raw string, single-quoted on purpose
printf 'func (m *Manager) Other(name string) {\n\tdoc := `\nfunc (m *Manager) installLocked() {}\n`\n\t_ = doc\n\tdelete(m.clients, name)\n}\n' > "$r/internal/upstream/manager.go"
expect "a fake installLocked declaration in a raw string does not exempt a later delete" 1 "$r"
r="$(mkroot pr3-manager-fake-helper-block)"
mkdir -p "$r/internal/upstream"
printf 'func (m *Manager) Other(name string, c *managed.Client) {\n\t/*\nfunc (m *Manager) unregisterLocked() {}\n\t*/\n\tm.clients[name] = c\n}\n' > "$r/internal/upstream/manager.go"
expect "a fake unregisterLocked declaration in a block comment does not exempt a later write" 1 "$r"
r="$(mkroot pr3-manager-fake-helper-string)"
mkdir -p "$r/internal/upstream"
printf 'func (m *Manager) Other(name string, c *managed.Client) {\n\ts := "func (m *Manager) installLocked() {}"\n\t_ = s\n\tm.clients[name], _ = c, 0\n}\n' > "$r/internal/upstream/manager.go"
expect "a fake helper declaration in a string literal does not exempt a later write" 1 "$r"
r="$(mkroot pr3-manager-ok)"
mkdir -p "$r/internal/upstream"
printf 'func (m *Manager) unregisterLocked(name string) *managed.Client {
	delete(m.clients, name)
	return nil
}

func (m *Manager) installLocked(name string, c *managed.Client) {
	m.clients[name] = c
}

func (m *Manager) read(name string) bool {
	return m.clients[name] == nil
}
' > "$r/internal/upstream/manager_generation.go"
expect "unregisterLocked/installLocked and reads are allowed in internal/upstream" 0 "$r"

# Sol round 5: statement splitting respects nested delimiters. A composite
# literal inside an index expression (`[]string{prefix, name}`) does not cut
# the statement in two, so its left-hand side is still m.clients[...]; the
# body of a brace pair is checked as its own statements. The same holds when
# the composite literal spans lines.
r="$(mkroot pr3-manager-composite-index)"
mkdir -p "$r/internal/upstream"
printf 'func (m *Manager) Other(prefix, name string, c *managed.Client) {\n\tm.clients[strings.Join([]string{prefix, name}, "")] = c\n}\n' > "$r/internal/upstream/manager.go"
expect "planted composite-literal index write m.clients[strings.Join([]string{prefix, name}, \"\")] = c" 1 "$r"
r="$(mkroot pr3-manager-composite-index-multiline)"
mkdir -p "$r/internal/upstream"
printf 'func (m *Manager) Other(prefix, name string, c *managed.Client) {\n\tm.clients[strings.Join([]string{\n\t\tprefix,\n\t\tname,\n\t}, "")] = c\n}\n' > "$r/internal/upstream/manager.go"
expect "planted composite-literal index write split across lines" 1 "$r"
r="$(mkroot pr3-manager-funclit-composite-write)"
mkdir -p "$r/internal/upstream"
printf 'func (m *Manager) Other(prefix, name string, c *managed.Client) {\n\trun(func() {\n\t\tparts := []string{prefix, name}\n\t\tm.clients[strings.Join(parts, "")] = c\n\t})\n}\n' > "$r/internal/upstream/manager.go"
expect "planted write after a composite literal inside a func literal argument" 1 "$r"
r="$(mkroot pr3-manager-composite-index-read-ok)"
mkdir -p "$r/internal/upstream"
printf 'func (m *Manager) Other(prefix, name string) bool {\n\tc := m.clients[strings.Join([]string{prefix, name}, "")]\n\td := m.clients[strings.Join([]string{\n\t\tprefix,\n\t\tname,\n\t}, "")]\n\trun(func() {\n\t\tparts := []string{prefix, name}\n\t\t_ = m.clients[strings.Join(parts, "")]\n\t})\n\treturn c == d\n}\n' > "$r/internal/upstream/manager.go"
expect "composite-literal index reads (one line, multiline, in a func literal) are allowed" 0 "$r"
r="$(mkroot pr3-manager-composite-index-helper-ok)"
mkdir -p "$r/internal/upstream"
printf 'func (m *Manager) installLocked(prefix, name string, c *managed.Client) {\n\tm.clients[strings.Join([]string{prefix, name}, "")] = c\n}\n' > "$r/internal/upstream/manager_generation.go"
expect "a composite-literal index write inside installLocked is allowed" 0 "$r"
# Sol round 6: a parenthesized receiver or a method expression is the same
# unbound mutation. In internal/upstream/*.go no selector of a legacy mutator
# is allowed at all (nothing there calls one), whatever its receiver
# spelling; in internal/runtime and internal/server a parenthesized manager
# receiver and a (*upstream.Manager) method expression are caught.
for line in '	(m).RemoveServer(name)' '	go (*Manager).RemoveServer(m, name)' '	_ = (*m).AddServer(name, cfg)' \
            '	mm := m\n	mm.AddServerConfig(name, cfg)' '	f := Manager.DisconnectServer'; do
  r="$(mkroot "pr3-manager-spelling-$RANDOM")"
  mkdir -p "$r/internal/upstream"
  printf 'func (m *Manager) Other(name string) {\n%b\n}\n' "$line" > "$r/internal/upstream/manager.go"
  expect "planted legacy mutator spelled ${line#	} in internal/upstream" 1 "$r"
done
for line in '	(r.upstreamManager).RemoveServer(name)' '	go (*upstream.Manager).RemoveServer(r.upstreamManager, name)' \
            '	_ = (s.runtime.UpstreamManager()).AddServer(name, cfg)'; do
  r="$(mkroot "pr3-runtime-spelling-$RANDOM")"
  echo "$line" > "$r/internal/runtime/lifecycle.go"
  expect "planted legacy mutator spelled ${line#	} in internal/runtime" 1 "$r"
done
r="$(mkroot pr3-manager-spelling-ok)"
mkdir -p "$r/internal/upstream"
printf 'func (m *Manager) RemoveServer(id string) {\n\tm.RemoveServerAt(id, 0)\n\t(m).RemoveServerAt(id, 0)\n\t_ = (*Manager).AddServerAt\n\t// m.RemoveServer(id)\n}\n' > "$r/internal/upstream/manager_generation.go"
expect "legacy declarations, generation-bound calls in any spelling and comments are allowed in internal/upstream" 0 "$r"

# Sol round 7: a parenthesized, dereferenced or aliased receiver writes the
# same registry. Outside unregisterLocked/installLocked every spelling of a
# .clients write, delete or clear is a hit; inside them it is allowed.
for line in '	(m).clients[name] = c' '	(*m).clients[name] = c' '	delete((m).clients, name)' \
            '	clear((m).clients)' '	delete((*m).clients, name)' '	clear((*m).clients)' \
            '	mm := m\n	mm.clients[name] = c' '	mm := m\n	delete(mm.clients, name)' \
            '	( m ).clients[name], ok = c, true'; do
  r="$(mkroot "pr3-registry-spelling-$RANDOM")"
  mkdir -p "$r/internal/upstream"
  printf 'func (m *Manager) Other(name string, c *managed.Client) {\n\tvar ok bool\n%b\n\t_ = ok\n}\n' "$line" > "$r/internal/upstream/manager.go"
  expect "planted registry write spelled ${line#	} outside the helpers" 1 "$r"
done
r="$(mkroot pr3-registry-spelling-helpers-ok)"
mkdir -p "$r/internal/upstream"
printf 'func (m *Manager) unregisterLocked(name string) *managed.Client {\n\tdelete((m).clients, name)\n\tdelete((*m).clients, name)\n\treturn nil\n}\n\nfunc (m *Manager) installLocked(name string, c *managed.Client) {\n\t(m).clients[name] = c\n\t(*m).clients[name] = c\n}\n\nfunc (m *Manager) read(name string) bool {\n\treturn (m).clients[name] == nil || len((*m).clients) > 0\n}\n' > "$r/internal/upstream/manager_generation.go"
expect "parenthesized registry writes inside unregisterLocked/installLocked and reads elsewhere are allowed" 0 "$r"

# The helper exemption covers only the helper BODY (Sol round 8): a
# package-level func literal, or any later top-level declaration, after
# installLocked/unregisterLocked does not inherit it.
helpers='func (m *Manager) unregisterLocked(name string) *managed.Client {\n\tdelete(m.clients, name)\n\treturn nil\n}\n\nfunc (m *Manager) installLocked(name string, c *managed.Client) {\n\tm.clients[name] = c\n}\n'
for tail in 'var reconnect = func(m *Manager, name string, c *managed.Client) {\n\tm.clients[name] = c\n}\n' \
            'var drop = func(m *Manager, name string) {\n\tdelete(m.clients, name)\n}\n' \
            'var (\n\treset = func(m *Manager) {\n\t\tclear(m.clients)\n\t}\n)\n' \
            'type hook struct{}\n\nvar h = func(m *Manager, c *managed.Client) { m.clients["x"] = c }\n'; do
  case=$((${case:-0}+1))
  for order in install unregister; do
    r="$(mkroot "pr3-registry-pkglevel-$order-$RANDOM")"
    mkdir -p "$r/internal/upstream"
    if [[ $order == install ]]; then
      body="$helpers"
    else
      body='func (m *Manager) installLocked(name string, c *managed.Client) {\n\tm.clients[name] = c\n}\n\nfunc (m *Manager) unregisterLocked(name string) *managed.Client {\n\tdelete(m.clients, name)\n\treturn nil\n}\n'
    fi
    printf '%b\n%b' "$body" "$tail" > "$r/internal/upstream/manager_generation.go"
    expect "package-level registry write after the $order helper (case $case) is not exempt" 1 "$r"
  done
done
r="$(mkroot pr3-registry-multiline-signature-ok)"
mkdir -p "$r/internal/upstream"
printf 'func (m *Manager) installLocked(\n\tname string,\n\tc *managed.Client,\n) {\n\tm.clients[name] = c\n}\n\nfunc (m *Manager) unregisterLocked(\n\tname string,\n) *managed.Client {\n\tdelete(m.clients, name)\n\treturn nil\n}\n' > "$r/internal/upstream/manager_generation.go"
expect "helpers with a multi-line signature keep the exemption for their body" 0 "$r"

# Spec 116 PR4 (FR-024a, T099): ungated upstream HTTP.
ungated() { # name dir file content expected
  local r
  r="$(mkroot "pr4-$1")"
  mkdir -p "$r/$2"
  printf '%s\n' "$4" > "$r/$2/$3"
  expect "FR-024a: $1" "$5" "$r"
}
ungated "http.Head in oauth" internal/oauth a.go 'package oauth
func f() { resp, _ := http.Head(u); _ = resp }' 1
ungated "http.Get in core" internal/upstream/core a.go 'package core
func f() { _, _ = http.Get(u) }' 1
ungated "http.DefaultTransport in transport" internal/transport a.go 'package transport
var rt = http.DefaultTransport' 1
ungated "http.DefaultClient in core" internal/upstream/core a.go 'package core
func f() { _, _ = http.DefaultClient.Do(r) }' 1
ungated "transport-less http.Client literal" internal/oauth a.go 'package oauth
func f() { c := &http.Client{Timeout: t}; _ = c }' 1
ungated "transport-less multi-line http.Client literal" internal/oauth a.go 'package oauth
func f() {
	c := &http.Client{
		Timeout: t,
	}
	_ = c
}' 1
ungated "http.Client literal with a Transport" internal/oauth a.go 'package oauth
func f() {
	c := &http.Client{
		Timeout:   t,
		Transport: env.transport(),
	}
	_ = c
}' 0
ungated "*http.Client return type" internal/transport a.go 'package transport
func f() *http.Client {
	return nil
}' 0
ungated "GetTransport outside core/gate.go" internal/upstream/core a.go 'package core
func f() { _ = cl.GetTransport() }' 1
ungated "GetTransport in core/gate.go" internal/upstream/core gate.go 'package core
func innerTransport() { _ = cl.GetTransport() }' 0
ungated "http.Get outside the scoped packages" internal/server a.go 'package server
func f() { _, _ = http.Get(u) }' 0
ungated "http.Get in a comment" internal/oauth a.go 'package oauth
// http.Get(u) is not used here' 0
r="$(mkroot pr4-allowlisted)"
mkdir -p "$r/internal/oauth" "$r/scripts"
printf 'package oauth\nfunc f() { return http.DefaultTransport }\n' > "$r/internal/oauth/env.go"
echo 'internal/oauth/env.go return http.DefaultTransport' > "$r/scripts/ungated-http-allowlist.txt"
expect "FR-024a: an allowlisted fallback" 0 "$r"

# An operational failure never reports a clean tree (review rounds 18-20): a
# failing file discovery or scanner exits 2, whether the tool is shadowed on
# PATH or by an exported shell function.
for tool in find awk; do
  shim="$TMP/shim-$tool"
  mkdir -p "$shim"
  printf '#!/bin/sh\nexit 2\n' > "$shim/$tool"
  chmod +x "$shim/$tool"
  PATH="$shim:$PATH" bash "$CHECK" "$clean" >/dev/null 2>&1
  rc=$?
  if [[ "$rc" == 2 ]]; then echo "ok   - failing $tool on PATH (exit $rc)"; pass=$((pass+1));
  else echo "FAIL - failing $tool on PATH (expected exit 2, got $rc)"; fail=$((fail+1)); fi
  # shellcheck disable=SC2016 # the function body is expanded by the inner bash
  bash -c "$tool() { return 2; }; export -f $tool; bash \"\$0\" \"\$1\" >/dev/null 2>&1" "$CHECK" "$clean"
  rc=$?
  if [[ "$rc" == 2 ]]; then echo "ok   - failing $tool shell function (exit $rc)"; pass=$((pass+1));
  else echo "FAIL - failing $tool shell function (expected exit 2, got $rc)"; fail=$((fail+1)); fi
done

if bash "$CHECK" >/dev/null 2>&1; then
  echo "ok   - the repository is clean"; pass=$((pass+1))
else
  echo "FAIL - the repository has upstream write sites outside the commit"; fail=$((fail+1))
  bash "$CHECK"
fi

echo "passed: $pass, failed: $fail"
[[ $fail -eq 0 ]]

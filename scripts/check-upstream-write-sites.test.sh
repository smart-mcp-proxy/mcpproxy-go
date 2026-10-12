#!/usr/bin/env bash
# check-upstream-write-sites.test.sh - the red/green self-test for
# check-upstream-write-sites.sh (Spec 116 T002, quickstart T1.8).
#
# Builds temp trees: a planted direct SaveUpstreamServer in internal/server
# (must exit 1), each AsyncManager queue writer (SaveServerAsync,
# DeleteServerAsync, EnableServerAsync, QuarantineServerAsync; must exit 1), the same call in a test file, in internal/storage and in
# internal/runtime/commit.go (must exit 0), the AddUpstream alias (exit 1), a
# method value such as `save := st.SaveUpstreamServer` (exit 1), a selector
# split across lines (exit 1), and a whole-line comment (must exit 0). Then runs the real script on the repository (must exit 0).
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
printf '\t// ends with a dot.\n\tSaveUpstreamServer := 1\n' > "$allowed/internal/server/not_selector.go"
expect "storage, commit.go, tests and comments are allowed" 0 "$allowed"

if bash "$CHECK" >/dev/null 2>&1; then
  echo "ok   - the repository is clean"; pass=$((pass+1))
else
  echo "FAIL - the repository has upstream write sites outside the commit"; fail=$((fail+1))
  bash "$CHECK"
fi

echo "passed: $pass, failed: $fail"
[[ $fail -eq 0 ]]

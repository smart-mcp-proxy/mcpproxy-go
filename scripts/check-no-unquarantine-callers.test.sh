#!/usr/bin/env bash
# check-no-unquarantine-callers.test.sh - the red/green self-test for
# check-no-unquarantine-callers.sh (Spec 109 T147a).
#
# Builds temp trees: a planted source caller (must exit 1), a planted
# test-file caller (must exit 0), a planted approveTools( call (must exit 1)
# and a clean tree (must exit 0). Then runs the real script on the repository
# (must exit 0).
set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
CHECK="$SCRIPT_DIR/check-no-unquarantine-callers.sh"
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

pass=0; fail=0
expect() { # desc expected-exit root
  bash "$CHECK" "$3" >/dev/null 2>&1
  local rc=$?
  if [[ "$rc" == "$2" ]]; then echo "ok   - $1 (exit $rc)"; pass=$((pass+1));
  else echo "FAIL - $1 (expected exit $2, got $rc)"; fail=$((fail+1)); fi
}

mkroot() { # name -> creates the directory layout
  local r="$TMP/$1"
  mkdir -p "$r/frontend/src" "$r/frontend/tests/unit" "$r/native/macos/MCPProxy/MCPProxy/Views" \
           "$r/cmd/mcpproxy" "$r/cmd/mcpproxy-tray" "$r/internal/tray"
  echo "$r"
}

clean="$(mkroot clean)"
echo "export const x = 1" > "$clean/frontend/src/a.ts"
expect "clean tree" 0 "$clean"

src="$(mkroot planted-source)"
echo 'await fetch("/api/v1/servers/x/unquarantine", { method: "POST" })' > "$src/frontend/src/a.ts"
expect "planted source caller" 1 "$src"

gotray="$(mkroot planted-go-tray)"
echo 'path := "/api/v1/servers/" + name + "/unquarantine"' > "$gotray/internal/tray/client.go"
expect "planted Go tray caller" 1 "$gotray"

tests="$(mkroot planted-tests)"
echo 'expect(requests).not.toContain("/unquarantine")' > "$tests/frontend/tests/unit/a.spec.ts"
echo 'if strings.Contains(path, "/unquarantine") { t.Fatal() }' > "$tests/cmd/mcpproxy/x_test.go"
mkdir -p "$tests/native/macos/MCPProxy/MCPProxyTests"
echo 'XCTAssertFalse(path.contains("/unquarantine"))' > "$tests/native/macos/MCPProxy/MCPProxyTests/T.swift"
expect "test-file callers are excluded" 0 "$tests"

comment="$(mkroot comment-only)"
echo '// the one-click approve/unquarantine call was removed (SC-005)' > "$comment/native/macos/MCPProxy/MCPProxy/Views/A.swift"
expect "a whole-line comment is not a caller" 0 "$comment"

approve="$(mkroot planted-approve)"
echo 'Button("Approve") { Task { await api.approveTools(server) } }' > "$approve/native/macos/MCPProxy/MCPProxy/Views/HomeView.swift"
expect "planted approveTools( call in HomeView" 1 "$approve"

other="$(mkroot approve-elsewhere)"
echo 'func approveTools(_ s: String) {}' > "$other/native/macos/MCPProxy/MCPProxy/Views/SomethingElse.swift"
expect "approveTools( outside the listed views is fine" 0 "$other"

expect "the repository itself" 0 "$SCRIPT_DIR/.."

echo "passed=$pass failed=$fail"
[[ $fail -eq 0 ]]

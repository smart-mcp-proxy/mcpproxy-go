#!/usr/bin/env bash
# Spec 123 T070: end-to-end check of OAuth token encryption against the OAuth
# test fixture (tests/oauthserver). No browser: a fake `open` / `xdg-open` on
# PATH completes the authorization headlessly.
#
#   1. login with a key set          -> tokens are sealed in config.db (US1, SC-001)
#   2. restart without any key       -> server unhealthy with MCPX_OAUTH_TOKEN_UNDECRYPTABLE,
#                                       no new login offered, sealed row bytes unchanged (US2)
#   3. restart with a missing key file -> exit 4 before config.db is opened, bytes unchanged (US3)
#   4. restart with the key          -> authenticated again, no browser launched (SC-002)
#   5. offline `auth tokens status|decrypt|encrypt` + `db compact` round trip: a running
#      core refuses (exit 3), decrypt needs --yes, the marker survives a default compaction,
#      `--keep-backup=false` clears it, a raw scan of config.db then finds no token text,
#      a leftover config.db.bak is reported until deleted (PR-3)
#   6. restart after the round trip  -> authenticated, no browser launched
#
# Usage: ./scripts/test-oauth-token-encryption-e2e.sh   (needs go, curl, jq, openssl)

set -uo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
WORK="$(mktemp -d)"
OAUTH_PORT="${OAUTH_PORT:-19431}"
PROXY_PORT="${PROXY_PORT:-19432}"
API_KEY="e2e-test-api-key"
DATA="$WORK/data"
KEYFILE="$WORK/keys/oauth-key"
BIN="$WORK/mcpproxy"
OPEN_LOG="$WORK/open.log"
PIDS=()
FAIL=0

cleanup() {
	for p in "${PIDS[@]:-}"; do [ -n "$p" ] && kill "$p" 2>/dev/null; done
	wait 2>/dev/null
	rm -rf "$WORK"
}
trap cleanup EXIT

ok()   { echo "  PASS: $*"; }
bad()  { echo "  FAIL: $*"; FAIL=$((FAIL + 1)); }
step() { echo; echo "== $*"; }
need() { command -v "$1" >/dev/null || { echo "missing tool: $1"; exit 2; }; }
for t in go curl jq openssl shasum; do need "$t"; done

mkdir -p "$DATA" "$WORK/keys" "$WORK/fakebin"
umask 077
openssl rand -base64 32 > "$KEYFILE"

# --- fake browser: complete the authorization headlessly -------------------
for name in open xdg-open; do
	cat > "$WORK/fakebin/$name" <<EOF
#!/usr/bin/env bash
echo "\$1" >> "$OPEN_LOG"
curl -sS -L -o /dev/null -X POST "\$1" -d "username=testuser&password=testpass&consent=on&action=approve" >/dev/null 2>&1
EOF
	chmod +x "$WORK/fakebin/$name"
done

cat > "$WORK/config.json" <<EOF
{
  "listen": "127.0.0.1:$PROXY_PORT",
  "data_dir": "$DATA",
  "api_key": "$API_KEY",
  "enable_socket": false,
  "enable_web_ui": false,
  "mcpServers": [
    {
      "name": "oauth-test-server",
      "url": "http://127.0.0.1:$OAUTH_PORT/mcp",
      "protocol": "streamable-http",
      "enabled": true,
      "oauth": { "scopes": ["read", "write"] }
    }
  ]
}
EOF

echo "building binaries"
(cd "$ROOT" && go build -o "$BIN" ./cmd/mcpproxy && go build -o "$WORK/oauthserver" ./tests/oauthserver/cmd/server) || { echo "build failed"; exit 2; }

"$WORK/oauthserver" -port "$OAUTH_PORT" >"$WORK/oauthserver.log" 2>&1 &
PIDS+=($!)
for _ in $(seq 1 50); do curl -sf "http://127.0.0.1:$OAUTH_PORT/jwks.json" >/dev/null && break; sleep 0.2; done
curl -sf "http://127.0.0.1:$OAUTH_PORT/jwks.json" >/dev/null || { echo "fixture did not start"; exit 2; }

API="http://127.0.0.1:$PROXY_PORT/api/v1"
DB="$DATA/config.db"
PROXY_PID=""

# start_proxy [env assignments...] -> starts the daemon and waits for the API.
start_proxy() {
	# Drop any inherited key variables first: the step's own assignments are the
	# only source of a key.
	env -u MCPPROXY_OAUTH_TOKEN_ENCRYPTION_KEY -u MCPPROXY_OAUTH_TOKEN_ENCRYPTION_KEY_FILE \
		PATH="$WORK/fakebin:$PATH" MCPPROXY_TELEMETRY=false "$@" \
		"$BIN" serve --config "$WORK/config.json" >>"$WORK/proxy.log" 2>&1 &
	PROXY_PID=$!
	for _ in $(seq 1 100); do
		curl -sf -H "X-API-Key: $API_KEY" "$API/status" >/dev/null && return 0
		kill -0 "$PROXY_PID" 2>/dev/null || return 1
		sleep 0.2
	done
	return 1
}

stop_proxy() {
	[ -n "$PROXY_PID" ] && kill "$PROXY_PID" 2>/dev/null
	wait "$PROXY_PID" 2>/dev/null
	PROXY_PID=""
}

server_json() { curl -sf -H "X-API-Key: $API_KEY" "$API/servers" | jq -c '.data.servers[]? | select(.name=="oauth-test-server")'; }

wait_authenticated() {
	for _ in $(seq 1 60); do
		[ "$(server_json | jq -r '.authenticated // false')" = "true" ] && return 0
		sleep 0.5
	done
	return 1
}

# sealed_values prints every live oauth_tokens key/value pair (the whole value,
# nonce and ciphertext included), read-only, from config.db. Call it only while
# the daemon is stopped: bbolt holds a file lock. Only enc:v1 values are kept,
# so an unsealed (plaintext) row yields nothing.
cat > "$WORK/dumpoauth.go" <<'EOF'
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"go.etcd.io/bbolt"
)

func main() {
	plain := len(os.Args) > 2 && os.Args[2] == "plain"
	db, err := bbolt.Open(os.Args[1], 0o600, &bbolt.Options{ReadOnly: true, Timeout: 5 * time.Second})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer db.Close()
	_ = db.View(func(tx *bbolt.Tx) error {
		b := tx.Bucket([]byte("oauth_tokens"))
		if b == nil {
			return nil
		}
		return b.ForEach(func(k, v []byte) error {
			if plain {
				// plain mode: the access_token value of every plaintext row
				var rec struct {
					AccessToken string `json:"access_token"`
				}
				if !strings.HasPrefix(string(v), "enc:") && json.Unmarshal(v, &rec) == nil {
					fmt.Println(rec.AccessToken)
				}
				return nil
			}
			if strings.HasPrefix(string(v), "enc:v1:") {
				fmt.Printf("%s=%s\n", k, v)
			}
			return nil
		})
	})
}
EOF
(cd "$ROOT" && go build -o "$WORK/dumpoauth" "$WORK/dumpoauth.go") || { echo "could not build the bucket reader"; exit 2; }
sealed_values() { "$WORK/dumpoauth" "$DB" | sort; }
plain_access_tokens() { "$WORK/dumpoauth" "$DB" plain; }

step "1. login with the key set"
# E2E_CONTROL_NO_KEY=1 runs step 1 without a key: the scan below must then fail,
# which proves it is not vacuous.
STEP1_KEY="MCPPROXY_OAUTH_TOKEN_ENCRYPTION_KEY_FILE=$KEYFILE"
[ "${E2E_CONTROL_NO_KEY:-}" = "1" ] && STEP1_KEY="E2E_UNUSED=1"
start_proxy "$STEP1_KEY" || { bad "proxy did not start"; cat "$WORK/proxy.log"; exit 1; }
curl -sf -X POST -H "X-API-Key: $API_KEY" "$API/servers/oauth-test-server/login" >/dev/null
if wait_authenticated; then ok "server authenticated through the headless login"; else bad "server never authenticated"; server_json; fi
LOGINS_AFTER_FIRST=$(wc -l < "$OPEN_LOG" 2>/dev/null | tr -d ' ' || echo 0)
if [ "${LOGINS_AFTER_FIRST:-0}" -ge 1 ]; then ok "the headless login ran ($LOGINS_AFTER_FIRST browser launch)"; else bad "the fake browser was never launched"; LOGINS_AFTER_FIRST=0; fi
stop_proxy

if [ -n "$(sealed_values)" ]; then ok "config.db holds an enc:v1 envelope"; else bad "no sealed OAuth row in config.db"; fi
if grep -a -q '"access_token"' "$DB"; then bad "plaintext \"access_token\" field found in config.db"; else ok "no plaintext token field in config.db"; fi
if grep -a -q '"refresh_token"' "$DB"; then bad "plaintext \"refresh_token\" field found in config.db"; else ok "no plaintext refresh_token field in config.db"; fi
SEALED_BEFORE="$(sealed_values)"

step "2. restart with no key configured"
start_proxy || { bad "proxy did not start without a key"; tail -20 "$WORK/proxy.log"; }
sleep 3
S="$(server_json)"
if echo "$S" | jq -e '(.error_code // "") == "MCPX_OAUTH_TOKEN_UNDECRYPTABLE" or ((.health.detail // "") | test("cannot be decrypted"))' >/dev/null; then
	ok "server reports the undecryptable token (health: $(echo "$S" | jq -r '.health.level + "/" + .health.action'))"
else
	bad "server health does not name the undecryptable token: $S"
fi
if [ "$(echo "$S" | jq -r '.health.action // ""')" = "login" ]; then bad "health offers login"; else ok "health does not offer login"; fi
stop_proxy
if [ "$(sealed_values)" = "$SEALED_BEFORE" ]; then ok "sealed row bytes unchanged"; else bad "sealed row changed while the key was missing"; fi
if [ "$(wc -l < "$OPEN_LOG" | tr -d ' ')" = "$LOGINS_AFTER_FIRST" ]; then ok "no browser launched"; else bad "a login was started without the key"; fi

step "3. restart with a missing key file"
HASH_BEFORE="$(shasum -a 256 "$DB" | cut -d' ' -f1)"
env -u MCPPROXY_OAUTH_TOKEN_ENCRYPTION_KEY -u MCPPROXY_OAUTH_TOKEN_ENCRYPTION_KEY_FILE \
	PATH="$WORK/fakebin:$PATH" MCPPROXY_TELEMETRY=false MCPPROXY_OAUTH_TOKEN_ENCRYPTION_KEY_FILE="$WORK/keys/does-not-exist" \
	"$BIN" serve --config "$WORK/config.json" >"$WORK/missing.log" 2>&1
CODE=$?
if [ "$CODE" = "4" ]; then ok "exit code 4"; else bad "exit code $CODE, want 4"; fi
if [ "$(shasum -a 256 "$DB" | cut -d' ' -f1)" = "$HASH_BEFORE" ]; then ok "config.db bytes unchanged"; else bad "config.db changed"; fi
if grep -q "$WORK/keys" "$WORK/missing.log"; then bad "error output contains the key path"; else ok "error output does not contain the key path"; fi

step "4. restart with the key"
start_proxy "MCPPROXY_OAUTH_TOKEN_ENCRYPTION_KEY_FILE=$KEYFILE" || bad "proxy did not start with the key"
if wait_authenticated; then ok "authenticated again"; else bad "not authenticated after restoring the key"; server_json; fi
if [ "$(wc -l < "$OPEN_LOG" | tr -d ' ')" = "$LOGINS_AFTER_FIRST" ]; then ok "no browser launched"; else bad "restoring the key launched a login"; fi
stop_proxy

# --- 5. offline auth tokens + db compact round trip (PR-3) -------------------
# cli runs the binary offline (no daemon) with the key from the key file and
# records stdout+stderr in $CLI_OUT and the exit code in $CLI_CODE.
CLI_OUT=""; CLI_CODE=0
cli() {
	CLI_OUT="$(env -u MCPPROXY_OAUTH_TOKEN_ENCRYPTION_KEY MCPPROXY_TELEMETRY=false MCPPROXY_OAUTH_TOKEN_ENCRYPTION_KEY_FILE="$KEYFILE" \
		"$BIN" "$@" --config "$WORK/config.json" --data-dir "$DATA" 2>&1)"
	CLI_CODE=$?
	ALL_CLI_OUT="$ALL_CLI_OUT"$'\n'"$CLI_OUT"
}
ALL_CLI_OUT=""
# dbcli is `db ...` (no --config; it only needs the data dir).
dbcli() {
	CLI_OUT="$(env MCPPROXY_TELEMETRY=false "$BIN" "$@" --data-dir "$DATA" 2>&1)"
	CLI_CODE=$?
	ALL_CLI_OUT="$ALL_CLI_OUT"$'\n'"$CLI_OUT"
}
# absent PATTERN FILE WHAT: passes only when the scan ran and found nothing
# (grep exit 1); a match (0) and a scan that could not run (2+) both fail.
absent() {
	grep -a -q -e "$1" "$2"
	case $? in
	1) ok "no $3" ;;
	0) bad "found $3" ;;
	*) bad "could not scan $2 for $3" ;;
	esac
}
# status_field FILTER sets STATUS_VAL. Call it in this shell, never inside
# $(...): a subshell would drop the output from ALL_CLI_OUT (the leak scan).
status_field() { cli auth tokens status -o json; STATUS_VAL="$(jq -r "$1" <<< "$CLI_OUT")"; }

step "5. offline auth tokens + db compact round trip"

# a. a running core holds the database: the write commands refuse with exit 3
start_proxy "MCPPROXY_OAUTH_TOKEN_ENCRYPTION_KEY_FILE=$KEYFILE" || bad "proxy did not start for the lock check"
cli auth tokens encrypt
if [ "$CLI_CODE" = "3" ]; then ok "auth tokens encrypt with a running core exits 3"; else bad "exit $CLI_CODE with a running core, want 3: $CLI_OUT"; fi
stop_proxy

# b. status
cli auth tokens status -o json
if echo "$CLI_OUT" | jq -e '.oauth_token_storage.encryption_enabled == true and .oauth_token_storage.sealed == 1 and .oauth_token_storage.plaintext == 0 and (.rows[0].state == "sealed")' >/dev/null; then
	ok "status reports one sealed row under the key"
else
	bad "unexpected status: $CLI_OUT"
fi

# c. decrypt needs --yes, then the rows are plaintext (the raw scan sees them: control)
cli auth tokens decrypt
if [ "$CLI_CODE" = "1" ]; then ok "decrypt without --yes exits 1"; else bad "decrypt without --yes exited $CLI_CODE"; fi
cli auth tokens decrypt --yes -o json
if [ "$CLI_CODE" = "0" ] && [ "$(echo "$CLI_OUT" | jq -r '.decrypted')" = "1" ]; then ok "decrypt --yes rewrote the row"; else bad "decrypt --yes failed ($CLI_CODE): $CLI_OUT"; fi
ACCESS="$(plain_access_tokens | head -1)"
if [ -n "$ACCESS" ]; then ok "the row is readable plaintext after decrypt"; else bad "no plaintext access token after decrypt"; fi
TOKEN_TAIL="${ACCESS: -32}"
if grep -a -q "$TOKEN_TAIL" "$DB"; then ok "control: the raw scan sees the token text while the row is plaintext"; else bad "control failed: the raw scan cannot see a plaintext token"; fi

# d. encrypt seals again and sets the compaction marker
cli auth tokens encrypt -o json
if [ "$CLI_CODE" = "0" ] && [ "$(echo "$CLI_OUT" | jq -r '.sealed')" = "1" ]; then ok "encrypt sealed the row"; else bad "encrypt failed ($CLI_CODE): $CLI_OUT"; fi
cli auth tokens encrypt -o json
if [ "$CLI_CODE" = "0" ] && [ "$(echo "$CLI_OUT" | jq -r '.sealed')" = "0" ]; then ok "encrypt is idempotent"; else bad "second encrypt: ($CLI_CODE) $CLI_OUT"; fi
status_field '.oauth_token_storage.compaction_advised'
if [ "$STATUS_VAL" = "true" ]; then ok "status says compaction is pending"; else bad "compaction not reported as pending"; fi

# e. a default compaction keeps the marker and leaves config.db.bak
dbcli db compact
if [ "$CLI_CODE" = "0" ] && echo "$CLI_OUT" | grep -q "config.db.bak"; then ok "default db compact warns about config.db.bak"; else bad "default compact ($CLI_CODE): $CLI_OUT"; fi
status_field '.oauth_token_storage.compaction_advised'
if [ "$STATUS_VAL" = "true" ]; then ok "the marker survives a compaction that keeps a backup"; else bad "marker cleared by a compaction that kept a backup"; fi
if [ -f "$DB.bak" ]; then ok "config.db.bak exists"; else bad "no config.db.bak after the default compaction"; fi
if grep -a -q "$TOKEN_TAIL" "$DB.bak"; then ok "the retained backup still holds the old plaintext (why the marker stays)"; else echo "  NOTE: the backup no longer shows the plaintext (page reuse); not a failure"; fi

# f. --keep-backup=false clears the marker; the raw file holds no token text
dbcli db compact --keep-backup=false
if [ "$CLI_CODE" = "0" ]; then ok "db compact --keep-backup=false succeeded"; else bad "compact --keep-backup=false ($CLI_CODE): $CLI_OUT"; fi
status_field '.oauth_token_storage.compaction_advised'
if [ "$STATUS_VAL" = "false" ]; then ok "the marker is cleared"; else bad "marker still set after --keep-backup=false"; fi
absent "$TOKEN_TAIL" "$DB" "token text in config.db after compaction"
absent '"access_token"' "$DB" "plaintext access_token field in config.db after compaction"
absent '"refresh_token"' "$DB" "plaintext refresh_token field in config.db after compaction"
status_field '.oauth_token_storage.warnings | index("backup_file_present") != null'
if [ "$STATUS_VAL" = "true" ]; then ok "the leftover config.db.bak is still reported"; else bad "status does not report the leftover config.db.bak"; fi
rm -f "$DB.bak"
cli auth tokens status -o json >/dev/null
if [ "$CLI_CODE" = "0" ] && echo "$CLI_OUT" | jq -e '.oauth_token_storage.warnings | type == "array" and length == 0' >/dev/null; then ok "no warning remains once the backup is deleted"; else bad "warnings remain: $CLI_OUT"; fi

step "6. restart after the round trip"
start_proxy "MCPPROXY_OAUTH_TOKEN_ENCRYPTION_KEY_FILE=$KEYFILE" || bad "proxy did not start after the round trip"
if wait_authenticated; then ok "authenticated, same login"; else bad "not authenticated after the round trip"; server_json; fi
if [ "$(wc -l < "$OPEN_LOG" | tr -d ' ')" = "$LOGINS_AFTER_FIRST" ]; then ok "no browser launched"; else bad "the round trip forced a new login"; fi
DOCTOR="$(curl -sf -H "X-API-Key: $API_KEY" "$API/diagnostics" | jq -c '.data.oauth_token_storage')"
if echo "$DOCTOR" | jq -e '.encryption_enabled == true and .sealed == 1 and .compaction_advised == false' >/dev/null; then ok "doctor section: encryption on, 1 sealed, no compaction pending"; else bad "doctor section: $DOCTOR"; fi
grep -q -F -e "$(tr -d '\n' < "$KEYFILE")" <<< "$DOCTOR"
case $? in
1) ok "doctor output holds no key bytes" ;;
0) bad "doctor output contains key bytes" ;;
*) bad "could not scan the doctor output for key bytes" ;;
esac
stop_proxy


KEY_TEXT="$(tr -d '\n' < "$KEYFILE")"
for LOG in "$WORK/proxy.log" "$WORK/missing.log"; do
	grep -a -q -F -e "$KEY_TEXT" "$LOG"
	case $? in
	1) ok "key bytes absent from $(basename "$LOG")" ;;
	0) bad "key bytes found in $(basename "$LOG")" ;;
	*) bad "could not scan $LOG for key bytes" ;;
	esac
done
# A here-string, not a pipe: under pipefail an early match would kill the
# producer (SIGPIPE) and read as "absent".
grep -q -F -e "$KEY_TEXT" <<< "$ALL_CLI_OUT"
case $? in
1) ok "key bytes absent from all CLI output" ;;
0) bad "key bytes found in CLI output" ;;
*) bad "could not scan the CLI output for key bytes" ;;
esac

echo
if [ "$FAIL" = "0" ]; then echo "OAuth token encryption e2e: all checks passed"; exit 0; fi
echo "OAuth token encryption e2e: $FAIL check(s) failed"
echo "--- proxy.log (tail) ---"; tail -40 "$WORK/proxy.log"
exit 1

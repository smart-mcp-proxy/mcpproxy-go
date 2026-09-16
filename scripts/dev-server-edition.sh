#!/usr/bin/env bash
# dev-server-edition.sh — Spec 107 local verification rig (quickstart.md).
#
# Isolated, synthetic, loopback-only. Builds the server edition into a scratch
# directory, starts the fake OpenID Provider from tests/oauthserver, writes a
# scratch config + data dir, boots the server edition on a high port, performs
# the headless login as alice@example.com and prints /api/v1/auth/me. Nothing
# here touches ~/.mcpproxy, the tray's core, or a production IdP.
#
# Usage:
#   scripts/dev-server-edition.sh [--phase b|c|d] [--scratch DIR] [--port N]
#                                 [--idp-port N] [--idp-args "<flags>"]
#                                 [--keep] [--skip-build]
#
#   --phase b   quickstart §0–§4: build, fake IdP, config, boot, headless login,
#               open-redirect check (default)
#   --phase c   + §5: tenant principal on core REST (PR-C)
#   --phase d   + §6: mint an agent token as Alice, call /mcp, audit tail (PR-D)
#   --scratch   root for the binary, config, data dir and logs
#               (default: $MCPPROXY_RIG_SCRATCH or mktemp under $TMPDIR)
#   --idp-args  extra flags for the fake IdP, e.g. "-token-error bad-signature";
#               any tamper flag switches the login step to expect a refusal —
#               403 (FR-024's closed-reason class) for most flags, but 503
#               ("Sign-in is temporarily unavailable") for the unavailability
#               class: -discovery-http-token-endpoint, -token-endpoint-redirect,
#               and -userinfo-error redirect|non-json|unavailable
#   --keep      keep the scratch directory on success (always kept on failure)
#   --skip-build reuse <scratch>/mcpproxy-server and <scratch>/oauthserver
#
# Rules this script encodes (all verified, see quickstart.md "Rules"):
#   * the server binary is built with `-o` — a bare `go build -tags server
#     ./cmd/mcpproxy` clobbers ./mcpproxy;
#   * the instance runs with BOTH --config and --data-dir, else the generated
#     API key lands in a file the next boot never reads;
#   * teardown kills only the PIDs this script started — never pkill by name;
#   * every curl carries --max-time and every wait loop is bounded, so a
#     missing feature (e.g. the OIDC provider on an older branch) is reported,
#     never hung on.
set -euo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO="$(cd "$HERE/.." && pwd)"
cd "$REPO"

PHASE="b"
SCRATCH="${MCPPROXY_RIG_SCRATCH:-}"
PORT=""
IDP_PORT=""
IDP_EXTRA=""
KEEP="0"
SKIP_BUILD="0"
SCRATCH_OWNED="0"

usage() { sed -n '2,34p' "${BASH_SOURCE[0]}" | sed 's/^# \{0,1\}//'; }

while [[ $# -gt 0 ]]; do
	case "$1" in
	--phase) PHASE="${2:-}"; shift 2 ;;
	--phase=*) PHASE="${1#*=}"; shift ;;
	--scratch) SCRATCH="${2:-}"; shift 2 ;;
	--scratch=*) SCRATCH="${1#*=}"; shift ;;
	--port) PORT="${2:-}"; shift 2 ;;
	--port=*) PORT="${1#*=}"; shift ;;
	--idp-port) IDP_PORT="${2:-}"; shift 2 ;;
	--idp-port=*) IDP_PORT="${1#*=}"; shift ;;
	--idp-args) IDP_EXTRA="${2:-}"; shift 2 ;;
	--idp-args=*) IDP_EXTRA="${1#*=}"; shift ;;
	--keep) KEEP="1"; shift ;;
	--skip-build) SKIP_BUILD="1"; shift ;;
	-h | --help) usage; exit 0 ;;
	*) echo "unknown argument: $1" >&2; usage >&2; exit 2 ;;
	esac
done

case "$PHASE" in
b | c | d) ;;
*) echo "--phase must be b, c or d (got: '$PHASE')" >&2; exit 2 ;;
esac

# ---------------------------------------------------------------------------
# Output helpers. Assertions are fail-fast: later steps depend on earlier ones.
# ---------------------------------------------------------------------------
ts() { date '+%H:%M:%S'; }
log() { echo "$(ts) [rig] $*"; }
ok() { echo "$(ts) [rig]   ok   $*"; }
FAILED="0"
die() {
	FAILED="1"
	echo "$(ts) [rig] FAIL $*" >&2
	exit 1
}
tail_log() { # tail_log <label> <file>
	[[ -f "$2" ]] || return 0
	echo "----- last 30 lines of $1 ($2) -----" >&2
	tail -n 30 "$2" >&2
	echo "----- end $1 -----" >&2
}
need() { command -v "$1" >/dev/null 2>&1 || die "missing tool: $1"; }
# curl wrapper: bounded, silent, never follows redirects unless asked.
c() { curl -s --max-time 10 "$@"; }
port_free() { ! (exec 3<>"/dev/tcp/127.0.0.1/$1") 2>/dev/null; }
pick_port() { # pick_port <prefix>
	local p n=0
	while :; do
		p="$1$((RANDOM % 900 + 100))"
		port_free "$p" && { echo "$p"; return 0; }
		n=$((n + 1))
		[[ $n -lt 20 ]] || die "no free 1${1#1}xxx port found"
	done
}
wait_http() { # wait_http <url> <seconds> [curl args...]
	local url="$1" secs="$2" i
	shift 2
	for ((i = 0; i < secs * 2; i++)); do
		if c -f -o /dev/null "$@" "$url"; then return 0; fi
		sleep 0.5
	done
	return 1
}
pid_alive() { kill -0 "$1" 2>/dev/null; }

# ---------------------------------------------------------------------------
# Teardown: PID-scoped, runs on every exit path.
# ---------------------------------------------------------------------------
MP_PID=""
IDP_PID=""
cleanup() {
	local rc=$?
	trap - EXIT
	set +e
	for pid in "$MP_PID" "$IDP_PID"; do
		[[ -n "$pid" ]] || continue
		pid_alive "$pid" || continue
		kill -TERM "$pid" 2>/dev/null
	done
	for pid in "$MP_PID" "$IDP_PID"; do
		[[ -n "$pid" ]] || continue
		for _ in 1 2 3 4 5 6 7 8 9 10; do
			pid_alive "$pid" || break
			sleep 0.5
		done
		pid_alive "$pid" && kill -KILL "$pid" 2>/dev/null
		wait "$pid" 2>/dev/null
	done
	if [[ "$rc" -ne 0 || "$FAILED" == "1" ]]; then
		[[ -n "${SCRATCH:-}" ]] && tail_log "main.log" "$SCRATCH/main.log"
		echo "$(ts) [rig] scratch kept for inspection: $SCRATCH" >&2
		exit "${rc:-1}"
	fi
	if [[ "$KEEP" == "1" || "$SCRATCH_OWNED" != "1" ]]; then
		log "scratch kept: $SCRATCH"
	else
		rm -rf "$SCRATCH" # only a directory this run created via mktemp
		log "scratch removed"
	fi
	exit "$rc"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

# ---------------------------------------------------------------------------
# §0 One-time: tools, ports, scratch, build, fixture deps.
# ---------------------------------------------------------------------------
need go; need node; need npm; need jq; need curl
[[ -d "$REPO/cmd/mcpproxy" && -d "$REPO/tests/oauthserver/cmd/server" ]] || die "not a mcpproxy-go checkout: $REPO"

if [[ -z "$SCRATCH" ]]; then
	tmp_root="${TMPDIR:-/tmp}"
	SCRATCH="$(mktemp -d "${tmp_root%/}/mcpproxy-107.XXXXXX")"
	SCRATCH_OWNED="1"
else
	mkdir -p "$SCRATCH"
	SCRATCH="$(cd "$SCRATCH" && pwd)"
fi
[[ -n "$PORT" ]] || PORT="$(pick_port 18)"
[[ -n "$IDP_PORT" ]] || IDP_PORT="$(pick_port 19)"
port_free "$PORT" || die "port $PORT is busy"
port_free "$IDP_PORT" || die "port $IDP_PORT is busy"
log "phase=$PHASE scratch=$SCRATCH port=$PORT idp_port=$IDP_PORT"

BIN="$SCRATCH/mcpproxy-server"
IDP_BIN="$SCRATCH/oauthserver"
if [[ "$SKIP_BUILD" == "1" && -x "$BIN" && -x "$IDP_BIN" ]]; then
	log "§0 reusing $BIN and $IDP_BIN (--skip-build)"
else
	log "§0 go build -tags server -o $BIN ./cmd/mcpproxy"
	go build -tags server -o "$BIN" ./cmd/mcpproxy || die "server edition build failed"
	log "§0 go build -o $IDP_BIN ./tests/oauthserver/cmd/server"
	go build -o "$IDP_BIN" ./tests/oauthserver/cmd/server || die "fake IdP build failed (tests/oauthserver)"
fi
# `version` prints "MCPProxy vX (server)" as text; the JSON form carries the
# edition key the quickstart greps for.
"$BIN" version -o json | jq -e '.edition=="server"' >/dev/null || die "binary does not self-identify as the server edition"
ok "§0 $BIN self-identifies: $("$BIN" version | head -n 1)"

FIXTURE="$REPO/tests/echo-rugpull-server"
if [[ ! -d "$FIXTURE/node_modules/@modelcontextprotocol/sdk" ]]; then
	log "§0 npm ci --prefix $FIXTURE (once)"
	npm ci --prefix "$FIXTURE" --no-audit --no-fund >"$SCRATCH/npm-ci.log" 2>&1 || {
		tail_log "npm-ci.log" "$SCRATCH/npm-ci.log"
		die "npm ci for the stdio fixture failed"
	}
fi
ok "§0 stdio fixture ready: node $FIXTURE/index.js"

# ---------------------------------------------------------------------------
# §1 Fake OIDC IdP.
# ---------------------------------------------------------------------------
IDP_ARGS=(
	-port "$IDP_PORT" -oidc
	-redirect-uri "http://127.0.0.1:$PORT/api/v1/auth/callback"
	-user 'alice@example.com:pass:eng' -user 'bob@example.com:pass:'
	-user 'carol@example.com:pass:ops' -user 'dana@example.com:pass:'
	-groups-claim groups
)
TAMPER="0"
GROUPS_TAMPER="0"
if [[ -n "$IDP_EXTRA" ]]; then
	read -r -a extra <<<"$IDP_EXTRA"
	IDP_ARGS+=("${extra[@]}")
	case " $IDP_EXTRA " in
	*" -groups-error "*)
		# FR-008 fail-closed groups fixtures (absent/non-array/overage): the
		# login still SUCCEEDS (groups land as [] and groups_claim_missing is
		# logged) — this is NOT a login-refusal tamper case (cross-review
		# round 5, chunk 4 P3).
		GROUPS_TAMPER="1"
		;;
	*)
		TAMPER="1"
		;;
	esac
fi
IDP_URL="http://127.0.0.1:$IDP_PORT"
log "§1 starting fake IdP: $IDP_BIN ${IDP_ARGS[*]}"
"$IDP_BIN" "${IDP_ARGS[@]}" >"$SCRATCH/idp.log" 2>&1 &
IDP_PID=$!
if ! wait_http "$IDP_URL/.well-known/openid-configuration" 20; then
	tail_log "idp.log" "$SCRATCH/idp.log"
	die "fake IdP did not serve /.well-known/openid-configuration within 20s (pid $IDP_PID)"
fi
c "$IDP_URL/.well-known/openid-configuration" |
	jq -e '.userinfo_endpoint and .jwks_uri and (.id_token_signing_alg_values_supported|index("RS256"))' >/dev/null ||
	die "OIDC discovery document lacks userinfo_endpoint / jwks_uri / RS256"
# The discovery endpoint answers as soon as the listener is bound — before
# the startup banner (Confidential ID/Secret included) finishes printing to
# idp.log — so a single read right after wait_http can race an empty file.
# Poll the same bounded way wait_http does.
OIDC_CLIENT_ID=""
OIDC_CLIENT_SECRET=""
for ((i = 0; i < 40; i++)); do
	OIDC_CLIENT_ID="$(sed -n 's/^Confidential ID:[[:space:]]*//p' "$SCRATCH/idp.log" 2>/dev/null | head -n1)"
	OIDC_CLIENT_SECRET="$(sed -n 's/^Confidential Secret:[[:space:]]*//p' "$SCRATCH/idp.log" 2>/dev/null | head -n1)"
	[[ -n "$OIDC_CLIENT_ID" && -n "$OIDC_CLIENT_SECRET" ]] && break
	sleep 0.5
done
[[ -n "$OIDC_CLIENT_ID" && -n "$OIDC_CLIENT_SECRET" ]] || die "could not read Confidential ID/Secret from idp.log within 20s"
export OIDC_CLIENT_ID OIDC_CLIENT_SECRET
ok "§1 fake IdP up (pid $IDP_PID), client_id=$OIDC_CLIENT_ID, tamper=$TAMPER"

# ---------------------------------------------------------------------------
# §2 Scratch configuration (the config references the IdP client through
# ${env:} — nested server_edition.* keys have no env override, FR-020).
# ---------------------------------------------------------------------------
CONFIG="$SCRATCH/mcp_config.json"
AUDIT="$SCRATCH/audit.jsonl"
cat >"$CONFIG" <<EOF
{
  "listen": "127.0.0.1:$PORT",
  "require_mcp_auth": false,
  "trusted_proxies": ["127.0.0.1/32"],
  "audit_log": {"enabled": true, "path": "$AUDIT", "stdout": false},
  "mcpServers": [
    {"name": "a",    "command": "node", "args": ["$FIXTURE/index.js"], "protocol": "stdio", "enabled": true, "shared": true},
    {"name": "b",    "command": "node", "args": ["$FIXTURE/index.js"], "protocol": "stdio", "enabled": true, "shared": true},
    {"name": "a__b", "command": "node", "args": ["$FIXTURE/index.js"], "protocol": "stdio", "enabled": true, "shared": true}
  ],
  "server_edition": {
    "enabled": true,
    "admin_emails": ["dana@example.com"],
    "public_url": "http://127.0.0.1:$PORT",
    "session_cookie_secure": "auto",
    "oauth": {
      "provider": "oidc",
      "issuer_url": "$IDP_URL",
      "allow_insecure_issuer": true,
      "client_id": "\${env:OIDC_CLIENT_ID}",
      "client_secret": "\${env:OIDC_CLIENT_SECRET}",
      "scopes": ["openid", "profile", "email", "groups"],
      "groups_claim": "groups",
      "email_verified_policy": "refuse_false",
      "display_name": "Example Corp"
    },
    "access": {"group_servers": {"eng": ["a"], "ops": ["a", "b"]}, "default_servers": []}
  }
}
EOF
jq -e . "$CONFIG" >/dev/null || die "scratch config is not valid JSON"
ok "§2 wrote $CONFIG"

# ---------------------------------------------------------------------------
# §3 Run.
# ---------------------------------------------------------------------------
BASE="http://127.0.0.1:$PORT"
MAIN_LOG="$SCRATCH/main.log"
log "§3 $BIN serve --config $CONFIG --data-dir $SCRATCH --log-level=debug"
"$BIN" serve --config "$CONFIG" --data-dir "$SCRATCH" --log-level=debug >"$MAIN_LOG" 2>&1 &
MP_PID=$!
for ((i = 0; i < 120; i++)); do
	if ! pid_alive "$MP_PID"; then
		wait "$MP_PID" && rc=0 || rc=$?
		reason="$(grep -E '^Error:|"level":"error"|ERROR' "$MAIN_LOG" | tail -n 1)"
		[[ "$rc" -eq 3 ]] && die "server exited 3 (BBolt lock) — another instance holds $SCRATCH/config.db"
		[[ "$rc" -eq 4 ]] && die "server refused the scratch config (exit 4): ${reason:-see main.log} — on a branch without the oidc provider this is the expected stop"
		die "server exited early with code $rc: ${reason:-see main.log}"
	fi
	c -f -o /dev/null "$BASE/readyz" && break
	sleep 0.5
done
c -f -o /dev/null "$BASE/readyz" || die "server not ready on $BASE/readyz within 60s (pid $MP_PID)"
API_KEY="$(jq -r '.api_key // empty' "$CONFIG")"
[[ -n "$API_KEY" ]] || die "no api_key written back to $CONFIG (is --config + --data-dir wiring intact?)"
c -f -H "X-API-Key: $API_KEY" "$BASE/api/v1/status" | jq -e '.data.edition=="server"' >/dev/null ||
	die "/api/v1/status does not report edition=server"
ok "§3 server edition ready (pid $MP_PID), api_key read from config (operator key; tenants never see it)"
if grep -q 'Failed to initialize server features' "$MAIN_LOG"; then
	grep 'Failed to initialize server features' "$MAIN_LOG" | tail -n 1 >&2
	die "server-edition features did not initialise — the /api/v1/auth/* front door is NOT mounted on this build (see the line above; on a branch without the oidc provider this is expected)"
fi
log "§3 boot notices:"
grep -E 'require_mcp_auth.*overrid|public_url|audit_log.*enabled' "$MAIN_LOG" | head -n 5 | sed 's/^/    /' || true

# ---------------------------------------------------------------------------
# §4 Headless login (Alice) — no browser, no API key.
# ---------------------------------------------------------------------------
# headless_login <jar> <redirect_uri> -> prints "<callback http code> <redirect_url>"
headless_login() {
	local jar="$1" want="$2" auth cb
	# 4a. /auth/login stores state+nonce server-side and 302s to the IdP.
	auth="$(c -o /dev/null -w '%{http_code} %{redirect_url}' -c "$jar" "$BASE/api/v1/auth/login?redirect_uri=$want")"
	[[ "${auth%% *}" == "302" || "${auth%% *}" == "303" || "${auth%% *}" == "307" ]] ||
		die "4a /auth/login answered HTTP ${auth%% *} instead of a redirect to the IdP (OIDC front door missing?)"
	auth="${auth#* }"
	[[ "$auth" == "$IDP_URL"/* ]] || die "4a /auth/login redirected off the fake IdP: $auth"
	echo "$auth" | grep -Eq 'code_challenge_method=S256' || die "4a authorize URL lacks code_challenge_method=S256 (FR-021)"
	echo "$auth" | grep -q 'nonce=' || die "4a authorize URL lacks nonce (FR-021)"
	# 4b. submit the fake IdP's form; do NOT follow the redirect.
	cb="$(c -o /dev/null -w '%{redirect_url}' -X POST "$auth" \
		--data-urlencode username=alice@example.com --data-urlencode password=pass \
		--data consent=on --data action=approve)"
	[[ "$cb" == "$BASE/api/v1/auth/callback?"* ]] || die "4b fake IdP did not redirect to our callback (got: '$cb')"
	# 4c. complete the callback; the session cookie lands in the jar.
	c -o /dev/null -w '%{http_code} %{redirect_url}' -b "$jar" -c "$jar" "$cb"
}

J="$SCRATCH/alice.jar"
res="$(headless_login "$J" "/my/tokens")"
code="${res%% *}"; loc="${res#* }"
if [[ "$TAMPER" == "1" ]]; then
	# FR-024: unavailability (discovery/provider/internal failures) is the one
	# distinct class — 503, not the 403 closed-reason page — so a tamper flag
	# that injects one of those must expect 503, never a blanket 403.
	want="403"
	case " $IDP_EXTRA " in
	*" -discovery-http-token-endpoint "*|*" -token-endpoint-redirect "*|\
	*" -userinfo-error redirect "*|*" -userinfo-error non-json "*|*" -userinfo-error unavailable "*|\
	*" -token-error invalid_client "*|*" -token-error invalid_grant "*|\
	*" -token-error invalid_scope "*|*" -token-error server_error "*)
		# These are HTTP-level token-endpoint failures (ExchangeCode returns
		# an error on a non-200 response), not ID-token content defects: the
		# callback maps them to provider_error, not authorization_denied
		# (cross-review round 5, chunk 4 P3). The other -token-error values
		# are id_token defects checked after a successful exchange and stay
		# in the 403 bucket (bad-signature, wrong-iss, wrong-aud, etc.).
		want="503"
		;;
	esac
	[[ "$code" == "$want" ]] || die "4c tamper case: expected $want from the callback, got HTTP $code ($loc)"
	[[ "$(grep -c mcpproxy_session "$J" 2>/dev/null || true)" == "0" ]] || die "4c tamper case: a session cookie was issued"
	ok "4c tamper case refused with $want and no session (idp-args: $IDP_EXTRA)"
	log "§4 done (tamper run); phases c/d are not exercised under tamper"
	exit 0
fi
[[ "$code" == "302" && "$loc" == "$BASE/my/tokens" ]] || die "4c callback: expected 302 -> $BASE/my/tokens, got HTTP $code -> '$loc'"
[[ "$(grep -c mcpproxy_session "$J")" == "1" ]] || die "4c expected exactly one mcpproxy_session cookie in $J"
ME="$(c -b "$J" "$BASE/api/v1/auth/me")"
if [[ "$GROUPS_TAMPER" == "1" ]]; then
	# FR-008 fail-closed: a missing/non-array/oversized groups claim stores
	# [] (never the pre-existing value) rather than refusing the login.
	echo "$ME" | jq -e '.email=="alice@example.com" and .groups==[]' >/dev/null ||
		die "4c groups-error tamper case: expected alice with groups [] (fail-closed): $ME"
	ok "4c groups-error tamper case: alice logged in with groups [] (fail-closed, idp-args: $IDP_EXTRA)"
	log "§4 done (groups-tamper run); phases c/d are not exercised under tamper"
	exit 0
fi
echo "$ME" | jq -e '.email=="alice@example.com" and .groups==["eng"]' >/dev/null ||
	die "4c /auth/me is not alice with groups [eng]: $ME"
ok "§4 alice logged in; /api/v1/auth/me:"
echo "$ME" | jq . | sed 's/^/    /'

# Open-redirect check (US2.7): an absolute redirect_uri must fall back to /ui/.
J2="$SCRATCH/alice-evil.jar"
res="$(headless_login "$J2" "https://evil.example/")"
code="${res%% *}"; loc="${res#* }"
[[ "$code" == "302" && "$loc" == "$BASE/ui/" ]] || die "4d open redirect: expected 302 -> $BASE/ui/, got HTTP $code -> '$loc'"
ok "4d redirect_uri=https://evil.example/ landed on /ui/ (US2.7)"

[[ "$PHASE" == "b" ]] && { log "phase b complete"; exit 0; }

# ---------------------------------------------------------------------------
# §5 Tenant principal on core REST (PR-C).
# ---------------------------------------------------------------------------
c "$BASE/api/v1/auth/provider" | jq -e '.display_name=="Example Corp" and (keys|length==1)' >/dev/null ||
	die "5 /auth/provider must expose exactly display_name (FR-030)"
c -b "$J" "$BASE/api/v1/servers" | jq -e '[.servers[].name]==["a"]' >/dev/null ||
	die "5 /servers as alice must list exactly [a] (entitlement-filtered)"
c -b "$J" "$BASE/api/v1/user/servers" | jq -e '[.shared[].name]==["a"]' >/dev/null ||
	die "5 /user/servers as alice must list shared=[a]"
code="$(c -o /dev/null -w '%{http_code}' -b "$J" "$BASE/api/v1/config")"
[[ "$code" == "403" ]] || die "5 /config with a tenant cookie must be 403 (got $code)"
code="$(c -o /dev/null -w '%{http_code}' -b "$J" -X POST -d '{}' "$BASE/api/v1/tools/call")"
[[ "$code" == "403" ]] || die "5 /tools/call with a tenant cookie must be 403 before body parse (got $code)"
code="$(c -o /dev/null -w '%{http_code}' -b "$J" -H 'X-API-Key: wrong' "$BASE/api/v1/status")"
[[ "$code" == "401" ]] || die "5 wrong X-API-Key beside a valid cookie must be 401 (FR-001 precedence, got $code)"
ok "§5 tenant principal on core REST"

[[ "$PHASE" == "c" ]] && { log "phase c complete"; exit 0; }

# ---------------------------------------------------------------------------
# §6 Mint an agent token as Alice, call /mcp, read the audit line (PR-D).
# ---------------------------------------------------------------------------
TOK="$(c -b "$J" -X POST "$BASE/api/v1/user/tokens" -H 'Content-Type: application/json' \
	-d '{"name":"t1","allowed_servers":["*"],"permissions":["read"],"expires_in":"720h"}' | jq -r '.raw_token // empty')"
[[ -n "$TOK" ]] || die "6 token mint as alice returned no raw_token"
c -b "$J" "$BASE/api/v1/user/tokens" | jq -e '.tokens[0].allowed_servers==["a"]' >/dev/null ||
	die "6 minted token must have '*' materialised to [a] (US1.3)"
code="$(c -o /dev/null -w '%{http_code}' -b "$J" -X POST "$BASE/mcp" -d '{}')"
[[ "$code" == "401" ]] || die "6 cookie on /mcp must be 401 (FR-003, got $code)"
code="$(c -o /dev/null -w '%{http_code}' -X POST "$BASE/mcp" -d '{}')"
[[ "$code" == "401" ]] || die "6 no credential on /mcp must be 401 despite require_mcp_auth:false (FR-029, got $code)"
mcp() {
	c -X POST "$BASE/mcp" -H "Authorization: Bearer $TOK" -H 'Content-Type: application/json' \
		-H 'Accept: application/json, text/event-stream' "$@"
}
mcp -d '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"quickstart","version":"1"}}}' \
	-D "$SCRATCH/init.h" >/dev/null
SID="$(grep -i '^mcp-session-id' "$SCRATCH/init.h" | tr -d '\r' | cut -d' ' -f2)"
[[ -n "$SID" ]] || die "6 initialize returned no Mcp-Session-Id"
allowed="$(mcp -H "Mcp-Session-Id: $SID" -d '{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"call_tool_read","arguments":{"name":"a:echo","args":{"text":"hello","secret":"AKIAQUICKSTART7SENTINEL0"}}}}' | jq -c '.result.isError')"
[[ "$allowed" == "false" ]] || die "6 a:echo through the token must succeed (isError=$allowed)"
hidden="$(mcp -H "Mcp-Session-Id: $SID" -d '{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"call_tool_read","arguments":{"name":"b:echo","args":{}}}}' | jq -r '.result.content[0].text // empty')"
log "6 b:echo refusal text: ${hidden:-<empty>}"
[[ -f "$AUDIT" ]] || die "6 audit log $AUDIT was not written"
log "6 last audit lines:"
tail -n 3 "$AUDIT" | jq -c '{event,decision,outcome,server,tool,reason,disclosed,caller:.caller.kind,email:.caller.user_email,rid:.request_id}' | sed 's/^/    /'
[[ "$(grep -c 'AKIAQUICKSTART7SENTINEL0' "$AUDIT" || true)" == "0" ]] || die "6 secret bytes leaked into the audit log"
[[ "$(grep -c '"event":"auth_event"' "$AUDIT" || true)" -ge 1 ]] || die "6 no auth_event line for alice's login"
MCPPROXY_AUDIT_JSONL="$AUDIT" go test ./internal/audit -run TestExternalJSONLValidates -count=1 >"$SCRATCH/audit-schema.log" 2>&1 || {
	tail_log "audit-schema.log" "$SCRATCH/audit-schema.log"
	die "6 audit JSONL failed schema validation"
}
ok "§6 token mint, /mcp gate, audit lines and schema validation"
log "phase d complete"

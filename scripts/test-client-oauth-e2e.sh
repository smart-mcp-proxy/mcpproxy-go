#!/usr/bin/env bash
# Spec 132 T043: end-to-end check of browser login for MCP clients against a
# real ./mcpproxy binary. A scratch data dir, a scratch HOME and a high port
# are used; nothing under ~/.mcpproxy or the real client configs is touched.
# There is no browser: the consent step is done through the admin REST
# (/api/v1/oauth/authorizations/{id}/approve), as PR-1 ships no consent page.
#
#   1. default off            -> OAuth paths are not served, nothing is written to config.db
#   2. on: discovery          -> 401 challenge + both metadata documents, unknown client 404
#   3. register / authorize   -> RFC 7591 registration, authorize redirect, bad requests
#   4. approve / token        -> admin approve, deny, double approve, PKCE exchange, code reuse
#   5. MCP over the login     -> initialize + tools/list on the OAuth address
#   6. wrong doors            -> token on /mcp, on another client address, on localhost;
#                                API key on the OAuth address
#   7. refresh                -> rotation, forged refresh token, replay revokes the login
#   8. revoke / forget        -> revoking a login (and forgetting a client) ends access at once
#   9. guard refusal          -> named profile with require_mcp_auth off is refused
#  10. off after use          -> 404 on OAuth paths, API key /mcp still works
#  11. on again               -> the surviving login works again
#
# Usage: ./scripts/test-client-oauth-e2e.sh   (needs ./mcpproxy or go, curl, jq, openssl)
#   PORT=19561      listen port of the scratch core
#   MCPPROXY_BIN    use this binary instead of building one

set -uo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
WORK="$(mktemp -d)"
PORT="${PORT:-19561}"
API_KEY="e2e-client-oauth-key"
DATA="$WORK/data"
HOME_DIR="$WORK/home"
BIN="${MCPPROXY_BIN:-$WORK/mcpproxy}"
B="http://127.0.0.1:$PORT"
API="$B/api/v1"
RU="http://127.0.0.1:54321/callback"
PROXY_PID=""
NO_RETRY=0
PASS=0
FAIL=0

cleanup() {
	[ -n "$PROXY_PID" ] && kill "$PROXY_PID" 2>/dev/null
	wait 2>/dev/null
	rm -rf "$WORK"
}
trap cleanup EXIT

ok()   { echo "  PASS: $*"; PASS=$((PASS + 1)); }
bad()  { echo "  FAIL: $*"; FAIL=$((FAIL + 1)); }
step() { echo; echo "== $*"; }
need() { command -v "$1" >/dev/null || { echo "missing tool: $1"; exit 2; }; }
for t in curl jq openssl; do need "$t"; done

# eq WHAT WANT GOT
eq() { if [ "$2" = "$3" ]; then ok "$1"; else bad "$1 (want '$2', got '$3')"; fi; }
# has WHAT HAYSTACK NEEDLE (fixed string)
has() { case "$2" in *"$3"*) ok "$1" ;; *) bad "$1 (no '$3' in '${2:0:200}')" ;; esac; }
hasnt() { case "$2" in *"$3"*) bad "$1 (found '$3')" ;; *) ok "$1" ;; esac; }
# mask hides token material in anything printed.
mask() { sed -E 's/mcp_o[ar]t_[A-Za-z0-9_-]+/<token>/g'; }

mkdir -p "$DATA" "$HOME_DIR"
if [ -z "${MCPPROXY_BIN:-}" ]; then
	echo "building mcpproxy"
	(cd "$ROOT" && go build -o "$BIN" ./cmd/mcpproxy) || { echo "build failed"; exit 2; }
fi
if curl -s -o /dev/null "http://127.0.0.1:$PORT/"; then echo "port $PORT is in use"; exit 2; fi

# write_config REQUIRE_MCP_AUTH
write_config() {
	cat > "$WORK/config.json" <<EOF
{
  "listen": "127.0.0.1:$PORT",
  "data_dir": "$DATA",
  "api_key": "$API_KEY",
  "require_mcp_auth": $1,
  "enable_socket": false,
  "enable_web_ui": false,
  "mcpServers": [
    { "name": "dummy", "command": "true", "protocol": "stdio", "enabled": false }
  ],
  "profiles": [ { "name": "dev", "servers": ["dummy"] } ]
}
EOF
}

# start_proxy OAUTH_ENV(true|false|unset)
start_proxy() {
	local envv=()
	case "$1" in
	true | false) envv=(MCPPROXY_MCP_OAUTH_ENABLED="$1") ;;
	esac
	env -u MCPPROXY_MCP_OAUTH_ENABLED HOME="$HOME_DIR" MCPPROXY_TELEMETRY=false "${envv[@]+"${envv[@]}"}" \
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

# --- HTTP helpers -----------------------------------------------------------
# req METHOD URL [curl args...] -> sets CODE, HDRS, BODY
# A 429 from the public endpoints (burst 5 register / 10 authorize per address) is waited out
# once via Retry-After, so a fast script does not trip over the limiter it is not testing.
req() {
	local m="$1" u="$2"
	shift 2
	for _ in 1 2 3; do
		CODE=$(curl -s -o "$WORK/body" -D "$WORK/hdrs" -w '%{http_code}' -X "$m" "$u" "$@")
		BODY=$(cat "$WORK/body")
		HDRS=$(tr -d '\r' < "$WORK/hdrs")
		[ "$CODE" = 429 ] && [ "$NO_RETRY" != 1 ] || return 0
		sleep "$(hdr Retry-After | grep -E '^[0-9]+$' || echo 5)"
	done
}
hdr() { printf '%s\n' "$HDRS" | awk -v k="$(printf %s "$1" | tr 'A-Z' 'a-z')" 'BEGIN{FS=": "} tolower($1)==k {sub(/^[^:]*: /,""); print; exit}'; }
admin() { req "$1" "$API$2" -H "X-API-Key: $API_KEY" -H 'Content-Type: application/json' "${@:3}"; }

INIT='{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"e2e-client","version":"1"}}}'
# mcp_post URL TOKEN-HEADER... BODY: POST an MCP message (extra headers before body).
mcp_post() {
	local u="$1" body="$2"
	shift 2
	req POST "$u" -H 'Content-Type: application/json' -H 'Accept: application/json, text/event-stream' "$@" -d "$body"
}
# mcp_session URL BEARER -> prints session id of a successful initialize
mcp_init() {
	mcp_post "$1" "$INIT" -H "Authorization: Bearer $2"
	SID=""
	[ "$CODE" = 200 ] && SID=$(hdr Mcp-Session-Id)
}

# --- OAuth client helpers ---------------------------------------------------
pkce() {
	VER=$(openssl rand -base64 48 | tr -d '=+/\n' | head -c 64)
	CHAL=$(printf %s "$VER" | openssl dgst -sha256 -binary | openssl base64 -A | tr '+/' '-_' | tr -d '=')
}
register() { # NAME -> CID
	req POST "$B/oauth/register" -H 'Content-Type: application/json' -d "{\"redirect_uris\":[\"$RU\"],\"client_name\":\"$1\"}"
	CID=$(printf %s "$BODY" | jq -r '.client_id // empty')
}
authorize() { # CID RESOURCE-PATH -> sets RID (pkce first)
	pkce
	req GET "$B/oauth/authorize" -G --data-urlencode response_type=code --data-urlencode "client_id=$1" \
		--data-urlencode "redirect_uri=$RU" --data-urlencode "code_challenge=$CHAL" --data-urlencode code_challenge_method=S256 \
		--data-urlencode "resource=$B$2" --data-urlencode state=st-1
	RID=$(hdr Location | sed -n 's/.*[?&]request=\([^&]*\).*/\1/p')
	[ -n "$RID" ] || [ -z "${E2E_DEBUG:-}" ] || echo "  (authorize answered $CODE: $(printf %s "$BODY" | head -c 200))"
}
approve() { # RID JSON -> CODE_VAL (empty on refusal)
	admin POST "/oauth/authorizations/$1/approve" -d "$2"
	CODE_VAL=$(printf %s "$BODY" | jq -r '.data.redirect_url // empty' | sed -n 's/.*[?&]code=\([^&]*\).*/\1/p')
	[ -n "$CODE_VAL" ] || [ -z "${E2E_DEBUG:-}" ] || echo "  (approve answer $CODE: $(printf %s "$BODY" | head -c 300))"
}
# approve_fixed RID CLIENT: approve on a client address, binding revision taken from the request
# when the client already has a binding (a client that has had a login keeps one).
approve_fixed() {
	admin GET "/oauth/authorizations/$1"
	local rev
	rev=$(printf %s "$BODY" | jq -r '.data.client.binding.revision // empty')
	approve "$1" "$(jq -nc --arg c "$2" --arg r "$rev" '{client:{id:$c},mode:"switchable",profile:""} + (if $r != "" then {binding_revision:$r} else {} end)')"
}
exchange() { # CID CODE RESOURCE-PATH [verifier]
	req POST "$B/oauth/token" -d grant_type=authorization_code --data-urlencode "code=$2" --data-urlencode "redirect_uri=$RU" \
		-d "client_id=$1" --data-urlencode "code_verifier=${4:-$VER}" --data-urlencode "resource=$B$3"
	AT=$(printf %s "$BODY" | jq -r '.access_token // empty')
	RT=$(printf %s "$BODY" | jq -r '.refresh_token // empty')
	[ "$CODE" = 200 ] || [ -z "${E2E_DEBUG:-}" ] || echo "  (token answer: $(printf %s "$BODY" | mask))"
}
refresh() { # CID RT RESOURCE-PATH
	req POST "$B/oauth/token" -d grant_type=refresh_token --data-urlencode "refresh_token=$2" -d "client_id=$1" --data-urlencode "resource=$B$3"
}
# full_login NAME CLIENT-ID -> CID AT RT GRANT (client address /mcp/oauth/CLIENT-ID, switchable all-servers binding)
full_login() {
	register "$1"
	authorize "$CID" "/mcp/oauth/$2"
	approve_fixed "$RID" "$2"
	exchange "$CID" "$CODE_VAL" "/mcp/oauth/$2"
	GRANT=$(admin GET "/clients/$2" && printf %s "$BODY" | jq -r '[.data.credentials[]? | select(.kind=="browser_login" and .state=="active")][0].grant_id // empty')
}

#############################################################################
step "1. default off"
write_config false
start_proxy unset || { bad "core did not start"; tail -20 "$WORK/proxy.log"; exit 1; }
req POST "$B/mcp/oauth/cursor" -H 'Content-Type: application/json' -d "$INIT"
eq "OAuth MCP address gives no sign-in challenge" "" "$(hdr WWW-Authenticate)"
req GET "$B/.well-known/oauth-authorization-server"
hasnt "no authorization-server metadata" "$BODY" "token_endpoint"
req GET "$B/.well-known/oauth-protected-resource/mcp/oauth/cursor"
hasnt "no protected-resource metadata" "$BODY" "authorization_servers"
req POST "$B/oauth/register" -H 'Content-Type: application/json' -d "{\"redirect_uris\":[\"$RU\"]}"
[ "$CODE" != 201 ] && ok "registration not served (HTTP $CODE)" || bad "registration answered 201 while off"
req GET "$B/oauth/authorize?response_type=code&client_id=x"
[ "$CODE" != 302 ] && ok "authorize not served (HTTP $CODE)" || bad "authorize redirected while off"
admin GET "/oauth/authorizations"
eq "admin sign-in request list is 404 while off" 404 "$CODE"
has "feature_disabled code" "$BODY" "feature_disabled"
admin GET "/clients"
hasnt "client payload carries no browser-login field" "$BODY" "browser_login"
req POST "$B/mcp" -H "X-API-Key: $API_KEY" -H 'Content-Type: application/json' -H 'Accept: application/json, text/event-stream' -d "$INIT"
eq "API-key /mcp works" 200 "$CODE"
stop_proxy
for bucket in oauth_registrations oauth_grants oauth_grant_access_index oauth_client_bindings oauth_login_ever_used; do
	if grep -a -q -F "$bucket" "$DATA/config.db"; then bad "config.db contains $bucket while the feature never ran"; else ok "no $bucket in config.db"; fi
done

#############################################################################
step "2. on: discovery"
write_config true
start_proxy true || { bad "core did not start"; tail -20 "$WORK/proxy.log"; exit 1; }
req POST "$B/mcp/oauth/cursor" -H 'Content-Type: application/json' -d "$INIT"
eq "401 on the per-client address" 401 "$CODE"
CH=$(hdr WWW-Authenticate)
has "challenge names the per-client metadata" "$CH" "resource_metadata=\"$B/.well-known/oauth-protected-resource/mcp/oauth/cursor\""
req GET "$B/.well-known/oauth-protected-resource/mcp/oauth/cursor"
eq "protected-resource document: resource" "$B/mcp/oauth/cursor" "$(printf %s "$BODY" | jq -r .resource)"
eq "protected-resource document: authorization server" "$B" "$(printf %s "$BODY" | jq -r '.authorization_servers[0]')"
req GET "$B/.well-known/oauth-authorization-server"
eq "AS metadata: issuer" "$B" "$(printf %s "$BODY" | jq -r .issuer)"
eq "AS metadata: S256 only" '["S256"]' "$(printf %s "$BODY" | jq -c .code_challenge_methods_supported)"
eq "AS metadata: public clients only" '["none"]' "$(printf %s "$BODY" | jq -c .token_endpoint_auth_methods_supported)"
eq "AS metadata: registration endpoint" "$B/oauth/register" "$(printf %s "$BODY" | jq -r .registration_endpoint)"
req POST "$B/mcp/oauth" -H 'Content-Type: application/json' -d "$INIT"
eq "401 on the generic address" 401 "$CODE"
has "generic challenge" "$(hdr WWW-Authenticate)" "oauth-protected-resource/mcp/oauth\""
req POST "$B/mcp/oauth/no-such-client" -H 'Content-Type: application/json' -d "$INIT"
eq "unknown client address is 404" 404 "$CODE"
req GET "$B/.well-known/oauth-protected-resource/mcp/oauth/no-such-client"
eq "unknown client metadata is 404" 404 "$CODE"

#############################################################################
step "3. register and authorize"
req POST "$B/oauth/register" -H 'Content-Type: application/json' -d '{"redirect_uris":["ftp://example.com/cb"]}'
eq "non-http redirect URI refused" 400 "$CODE"
has "invalid_redirect_uri" "$BODY" "invalid_redirect_uri"
req POST "$B/oauth/register" -H 'Content-Type: application/json' -d "{\"redirect_uris\":[\"$RU\"],\"token_endpoint_auth_method\":\"client_secret_post\"}"
eq "confidential client refused" 400 "$CODE"
register "Cursor IDE"
CID_A="$CID"
eq "registration answers 201" 201 "$CODE"
hasnt "no client_secret issued" "$BODY" "client_secret"
[ -n "$CID_A" ] && ok "client_id issued" || bad "no client_id"
authorize "$CID_A" /mcp/oauth/cursor
eq "authorize redirects" 302 "$CODE"
has "redirect goes to the consent page" "$(hdr Location)" "$B/ui/authorize?request="
eq "no-store on authorize" "no-store" "$(hdr Cache-Control)"
[ -n "$RID" ] && ok "request id extracted" || bad "no request id"
req GET "$B/oauth/authorize" -G --data-urlencode response_type=code --data-urlencode "client_id=$CID_A" \
	--data-urlencode "redirect_uri=http://127.0.0.1:1/other" --data-urlencode "code_challenge=$CHAL" --data-urlencode code_challenge_method=S256
eq "unregistered redirect URI is a 400 page, not a redirect" 400 "$CODE"
eq "...with no Location header" "" "$(hdr Location)"
authorize "$CID_A" /mcp/oauth/no-such-client
eq "resource of an unknown client is 404, no redirect" 404 "$CODE"
authorize "$CID_A" /somewhere/else
eq "foreign resource redirects" 302 "$CODE"
has "invalid_target error to the client" "$(hdr Location)" "error=invalid_target"
has "state echoed" "$(hdr Location)" "state=st-1"
req GET "$B/oauth/authorize" -G --data-urlencode response_type=code --data-urlencode "client_id=$CID_A" \
	--data-urlencode "redirect_uri=$RU" --data-urlencode "code_challenge=$CHAL" --data-urlencode code_challenge_method=plain --data-urlencode "resource=$B/mcp/oauth/cursor"
has "plain PKCE refused" "$(hdr Location)" "error=invalid_request"

#############################################################################
step "4. approve and token exchange"
authorize "$CID_A" /mcp/oauth/cursor
admin GET "/oauth/authorizations/$RID"
eq "admin sees the request pending" pending "$(printf %s "$BODY" | jq -r .data.status)"
eq "address kind is client" client "$(printf %s "$BODY" | jq -r .data.address_kind)"
eq "fixed client id" cursor "$(printf %s "$BODY" | jq -r .data.client.id)"
admin GET "/oauth/authorizations"
has "pending list names the request" "$BODY" "$RID"
req GET "$B/api/v1/oauth/authorizations/$RID"
[ "$CODE" = 401 ] || [ "$CODE" = 403 ] && ok "admin REST refuses a call without the key (HTTP $CODE)" || bad "admin REST answered $CODE without the key"
approve "$RID" '{"client":{"id":"cursor"},"mode":"switchable","profile":""}'
eq "approve answers 200" 200 "$CODE"
[ -n "$CODE_VAL" ] && ok "approve returned a code" || bad "no code in redirect_url"
has "state carried in the redirect" "$BODY" "state=st-1"
admin POST "/oauth/authorizations/$RID/approve" -d '{"client":{"id":"cursor"},"mode":"switchable","profile":""}'
eq "double approve is 409" 409 "$CODE"
has "already_decided" "$BODY" "already_decided"
CODE_1="$CODE_VAL"
exchange "$CID_A" "$CODE_1" /mcp/oauth/cursor "wrong-verifier-wrong-verifier-wrong-verifier-wrong-verif"
eq "wrong PKCE verifier refused" 400 "$CODE"
has "invalid_grant" "$BODY" "invalid_grant"
authorize "$CID_A" /mcp/oauth/cursor
admin POST "/oauth/authorizations/$RID/deny"
eq "deny answers 200" 200 "$CODE"
has "access_denied redirect" "$BODY" "error=access_denied"
# the wrong-verifier attempt may have consumed the first code; get a fresh one
authorize "$CID_A" /mcp/oauth/cursor
approve "$RID" '{"client":{"id":"cursor"},"mode":"switchable","profile":""}'
exchange "$CID_A" "$CODE_VAL" /mcp/oauth/cursor
eq "token exchange answers 200" 200 "$CODE"
eq "no-store on the token answer" "no-store" "$(hdr Cache-Control)"
case "$AT" in mcp_oat_*) ok "access token has the mcp_oat_ prefix" ;; *) bad "access token shape" ;; esac
case "$RT" in mcp_ort_*) ok "refresh token has the mcp_ort_ prefix" ;; *) bad "refresh token shape" ;; esac
eq "expires_in is 3600" 3600 "$(printf %s "$BODY" | jq -r .expires_in)"
CODE_USED="$CODE_VAL"
req POST "$B/oauth/token" -d grant_type=authorization_code --data-urlencode "code=$CODE_USED" --data-urlencode "redirect_uri=$RU" \
	-d "client_id=$CID_A" --data-urlencode "code_verifier=$VER" --data-urlencode "resource=$B/mcp/oauth/cursor"
eq "reusing the code is refused" 400 "$CODE"
has "reuse is invalid_grant" "$BODY" "invalid_grant"
admin GET "/clients/cursor"
G=$(printf %s "$BODY" | jq -r '[.data.credentials[]? | select(.kind=="browser_login")][0].state // empty')
eq "code reuse revoked the login it produced" revoked "$G"
# the reuse revoked that login (and its registration); a fresh client is used from here on
register "Cursor IDE"
CID_A="$CID"
authorize "$CID_A" /mcp/oauth/cursor
approve_fixed "$RID" cursor
exchange "$CID_A" "$CODE_VAL" /mcp/oauth/cursor
eq "fresh login after the replay" 200 "$CODE"
AT_C="$AT"; RT_C="$RT"
admin GET "/clients/cursor"
GRANT_C=$(printf %s "$BODY" | jq -r '[.data.credentials[]? | select(.kind=="browser_login" and .state=="active")][0].grant_id // empty')
[ -n "$GRANT_C" ] && ok "active login listed on the client (kind browser_login)" || bad "no active login on the client"
eq "browser_login_disabled false while on" false "$(printf %s "$BODY" | jq -r .data.browser_login_disabled)"
if printf %s "$BODY" | grep -Eq 'mcp_o[ar]t_[A-Za-z0-9_-]{20}'; then bad "client payload carries token material"; else ok "client payload carries no token material"; fi

#############################################################################
step "5. MCP over the login"
mcp_init "$B/mcp/oauth/cursor" "$AT_C"
[ -n "$SID" ] && ok "initialize accepted on the OAuth address" || bad "initialize refused (HTTP $CODE)"
eq "server name" mcpproxy-go "$(printf %s "$BODY" | jq -r '.result.serverInfo.name // empty')"
mcp_post "$B/mcp/oauth/cursor" '{"jsonrpc":"2.0","method":"notifications/initialized"}' -H "Authorization: Bearer $AT_C" -H "Mcp-Session-Id: $SID"
mcp_post "$B/mcp/oauth/cursor" '{"jsonrpc":"2.0","id":2,"method":"tools/list"}' -H "Authorization: Bearer $AT_C" -H "Mcp-Session-Id: $SID"
eq "tools/list answers 200" 200 "$CODE"
NT=$(printf %s "$BODY" | jq -r '.result.tools | length' 2>/dev/null)
[ "${NT:-0}" -gt 0 ] 2>/dev/null && ok "tools/list returned $NT tools" || bad "tools/list had no tools: ${BODY:0:200}"
has "retrieve_tools is offered" "$BODY" "retrieve_tools"
admin GET "/clients/cursor"
eq "initialize name recorded as observed name" e2e-client "$(printf %s "$BODY" | jq -r '[.data.credentials[]? | select(.state=="active")][0].observed_name // empty')"

#############################################################################
step "6. wrong doors"
# token for cursor on another client address, the classic endpoint and localhost
req POST "$B/mcp/oauth/claude-code" -H "Authorization: Bearer $AT_C" -H 'Content-Type: application/json' -d "$INIT"
eq "cursor token on /mcp/oauth/claude-code" 401 "$CODE"
req POST "$B/mcp/oauth" -H "Authorization: Bearer $AT_C" -H 'Content-Type: application/json' -d "$INIT"
eq "cursor token on the generic address" 401 "$CODE"
req POST "$B/mcp" -H "Authorization: Bearer $AT_C" -H 'Content-Type: application/json' -H 'Accept: application/json, text/event-stream' -d "$INIT"
eq "access token on classic /mcp (require_mcp_auth on)" 401 "$CODE"
req POST "http://localhost:$PORT/mcp/oauth/cursor" -H "Authorization: Bearer $AT_C" -H 'Content-Type: application/json' -d "$INIT"
[ "$CODE" = 401 ] || [ "$CODE" = 404 ] && ok "token replayed against localhost instead of 127.0.0.1 refused (HTTP $CODE)" || bad "localhost answered $CODE"
req POST "$B/mcp/oauth/cursor" -H "X-API-Key: $API_KEY" -H 'Content-Type: application/json' -d "$INIT"
eq "API key header on the OAuth address" 401 "$CODE"
req POST "$B/mcp/oauth/cursor?apikey=$API_KEY" -H 'Content-Type: application/json' -d "$INIT"
eq "?apikey= on the OAuth address" 401 "$CODE"
req POST "$B/mcp/oauth/cursor" -H "Authorization: Bearer $API_KEY" -H 'Content-Type: application/json' -d "$INIT"
eq "admin key as bearer on the OAuth address" 401 "$CODE"
req POST "$B/mcp/oauth/cursor" -H "Authorization: Bearer mcp_oat_0000000000000000000000000000000000000000000" -H 'Content-Type: application/json' -d "$INIT"
eq "forged access token" 401 "$CODE"
req POST "$B/mcp" -H "X-API-Key: $API_KEY" -H 'Content-Type: application/json' -H 'Accept: application/json, text/event-stream' -d "$INIT"
eq "API key on classic /mcp still works" 200 "$CODE"
req POST "$B/mcp" -H 'Content-Type: application/json' -H 'Accept: application/json, text/event-stream' -d "$INIT"
eq "classic /mcp without credentials refused (require_mcp_auth on)" 401 "$CODE"

#############################################################################
step "7. refresh, forged refresh token, replay"
GID="${RT_C#mcp_ort_}"; GID="${GID%%_*}"
FORGED="mcp_ort_${GID}_AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA_AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
refresh "$CID_A" "$FORGED" /mcp/oauth/cursor
eq "forged refresh token (real grant id) refused" 400 "$CODE"
has "forged is invalid_grant" "$BODY" "invalid_grant"
admin GET "/clients/cursor"
eq "forged token changed nothing: login still active" active "$(printf %s "$BODY" | jq -r --arg g "$GRANT_C" '[.data.credentials[]? | select(.grant_id==$g)][0].state // empty')"
refresh "$CID_A" "$RT_C" /mcp/oauth/cursor
eq "refresh answers 200" 200 "$CODE"
AT_2=$(printf %s "$BODY" | jq -r '.access_token // empty'); RT_2=$(printf %s "$BODY" | jq -r '.refresh_token // empty')
[ -n "$RT_2" ] && [ "$RT_2" != "$RT_C" ] && ok "refresh token rotated" || bad "refresh token not rotated"
[ -n "$AT_2" ] && [ "$AT_2" != "$AT_C" ] && ok "new access token issued" || bad "access token not renewed"
mcp_post "$B/mcp/oauth/cursor" "$INIT" -H "Authorization: Bearer $AT_2"
eq "new access token works" 200 "$CODE"
req POST "$B/oauth/token" -d grant_type=refresh_token --data-urlencode "refresh_token=$RT_C" -d "client_id=$CID_A" --data-urlencode "resource=$B/mcp/oauth/cursor"
eq "replaying the old refresh token is refused" 400 "$CODE"
mcp_post "$B/mcp/oauth/cursor" "$INIT" -H "Authorization: Bearer $AT_2"
eq "replay revoked the login: its access token stops" 401 "$CODE"
refresh "$CID_A" "$RT_2" /mcp/oauth/cursor
eq "the rotated refresh token is dead too" 400 "$CODE"
admin GET "/clients/cursor"
eq "grant listed as revoked" revoked "$(printf %s "$BODY" | jq -r --arg g "$GRANT_C" '[.data.credentials[]? | select(.grant_id==$g)][0].state // empty')"

#############################################################################
step "8. revoke one login, forget a client"
full_login "Cursor again" cursor
eq "fresh cursor login" 200 "$CODE"
AT_R="$AT"; CID_R="$CID"; GRANT_R="$GRANT"
mcp_init "$B/mcp/oauth/cursor" "$AT_R"
[ -n "$SID" ] && ok "session open on the new login" || bad "could not open a session"
admin DELETE "/clients/cursor/logins/$GRANT_R"
eq "revoke login answers 200" 200 "$CODE"
mcp_post "$B/mcp/oauth/cursor" '{"jsonrpc":"2.0","id":3,"method":"tools/list"}' -H "Authorization: Bearer $AT_R" -H "Mcp-Session-Id: $SID"
eq "open session refused right after the revoke" 401 "$CODE"
mcp_post "$B/mcp/oauth/cursor" "$INIT" -H "Authorization: Bearer $AT_R"
eq "new session refused after the revoke" 401 "$CODE"
refresh "$CID_R" "none" /mcp/oauth/cursor
eq "refresh refused after the revoke" 400 "$CODE"
admin DELETE "/clients/cursor/logins/no-such-grant"
eq "revoking an unknown login is 404" 404 "$CODE"
full_login "Claude Code" claude-code
AT_F="$AT"
mcp_post "$B/mcp/oauth/claude-code" "$INIT" -H "Authorization: Bearer $AT_F"
eq "claude-code login works" 200 "$CODE"
admin DELETE "/clients/claude-code"
[ "$CODE" = 200 ] && ok "forget client answers 200" || bad "forget client answered $CODE: ${BODY:0:200}"
mcp_post "$B/mcp/oauth/claude-code" "$INIT" -H "Authorization: Bearer $AT_F"
eq "forgotten client's login refused" 401 "$CODE"

#############################################################################
step "9. guard refusal and stale binding"
# a client with a binding: approve with a stale revision
full_login "VS Code" vscode
AT_Z="$AT"
authorize "$CID" /mcp/oauth/vscode
admin GET "/oauth/authorizations/$RID"
REV=$(printf %s "$BODY" | jq -r '.data.client.binding.revision // empty')
[ -n "$REV" ] && ok "existing client carries a binding revision" || bad "no binding revision in $(printf %s "$BODY" | head -c 200)"
admin POST "/oauth/authorizations/$RID/approve" -d '{"client":{"id":"vscode"},"binding_revision":"stale-revision"}'
eq "stale binding revision refused" 409 "$CODE"
has "binding_changed" "$BODY" "binding_changed"
admin POST "/oauth/authorizations/$RID/approve" -d '{"client":{"id":"other"}}'
eq "a client other than the address' client refused" 422 "$CODE"
has "client_fixed" "$BODY" "client_fixed"
admin POST "/oauth/authorizations/$RID/approve" -d "{\"client\":{\"id\":\"vscode\"},\"binding_revision\":\"$REV\"}"
eq "approve with the current revision" 200 "$CODE"
stop_proxy
# require_mcp_auth off: a named profile must be refused (the client could bypass it on /mcp)
write_config false
start_proxy true || { bad "core did not restart"; tail -20 "$WORK/proxy.log"; exit 1; }
register "Acme"
authorize "$CID" /mcp/oauth
admin GET "/oauth/authorizations/$RID"
eq "generic address kind" generic "$(printf %s "$BODY" | jq -r .data.address_kind)"
admin POST "/oauth/authorizations/$RID/preview" -d '{"client":{"new":{"id":"acme","display_name":"Acme"}},"profile":"dev","mode":"locked"}'
has "preview shows the guard verdict" "$BODY" "binding_bypassable_without_auth"
admin POST "/oauth/authorizations/$RID/approve" -d '{"client":{"new":{"id":"acme","display_name":"Acme"}},"profile":"dev","mode":"locked"}'
eq "named profile with require_mcp_auth off refused" 409 "$CODE"
has "guard code" "$BODY" "binding_bypassable_without_auth"
admin POST "/oauth/authorizations/$RID/approve" -d '{"client":{"new":{"id":"acme","display_name":"Acme"}},"profile":"nope","mode":"locked"}'
eq "unknown profile" 404 "$CODE"
approve "$RID" '{"client":{"new":{"id":"acme","display_name":"Acme"}},"profile":"","mode":"switchable"}'
eq "all-servers access is allowed" 200 "$CODE"
exchange "$CID" "$CODE_VAL" /mcp/oauth
eq "generic-address exchange" 200 "$CODE"
mcp_init "$B/mcp/oauth" "$AT"
[ -n "$SID" ] && ok "login on the generic address works" || bad "generic address refused the new login (HTTP $CODE)"
admin GET "/clients/acme"
eq "the new client exists with its login" browser_login "$(printf %s "$BODY" | jq -r '.data.credentials[0].kind // empty')"
mcp_post "$B/mcp/oauth/acme" "$INIT" -H "Authorization: Bearer $AT"
eq "generic-address token on the acme address" 401 "$CODE"

step "9b. rate limits"
NO_RETRY=1
GOT429=""
for _ in $(seq 1 25); do
	req POST "$B/oauth/register" -H 'Content-Type: application/json' -d '{}'
	if [ "$CODE" = 429 ]; then GOT429=1; break; fi
done
NO_RETRY=0
[ -n "$GOT429" ] && ok "registration is rate limited (429)" || bad "no 429 after 25 registrations"
case "$(hdr Retry-After)" in '' | *[!0-9]*) bad "429 without a numeric Retry-After" ;; *) ok "429 carries Retry-After" ;; esac
stop_proxy

#############################################################################
step "10. off after use"
write_config true
start_proxy false || { bad "core did not restart"; tail -20 "$WORK/proxy.log"; exit 1; }
req POST "$B/mcp/oauth/vscode" -H "Authorization: Bearer $AT_Z" -H 'Content-Type: application/json' -d "$INIT"
eq "OAuth MCP address is 404 after use" 404 "$CODE"
req GET "$B/.well-known/oauth-authorization-server"
eq "AS metadata 404" 404 "$CODE"
req GET "$B/.well-known/oauth-protected-resource/mcp/oauth/vscode"
eq "protected-resource metadata 404" 404 "$CODE"
req POST "$B/oauth/register" -H 'Content-Type: application/json' -d "{\"redirect_uris\":[\"$RU\"]}"
eq "registration 404" 404 "$CODE"
req GET "$B/oauth/authorize?response_type=code&client_id=x"
eq "authorize 404" 404 "$CODE"
req POST "$B/oauth/token" -d grant_type=refresh_token -d refresh_token=x -d client_id=x
eq "token endpoint 404" 404 "$CODE"
admin GET "/oauth/authorizations"
eq "admin request list 404 feature_disabled" 404 "$CODE"
req POST "$B/mcp" -H "X-API-Key: $API_KEY" -H 'Content-Type: application/json' -H 'Accept: application/json, text/event-stream' -d "$INIT"
eq "API-key /mcp still works" 200 "$CODE"
admin GET "/clients/vscode"
eq "browser_login_disabled true while off after use" true "$(printf %s "$BODY" | jq -r .data.browser_login_disabled)"
eq "the login is still listed" active "$(printf %s "$BODY" | jq -r '[.data.credentials[]? | select(.kind=="browser_login")][0].state // empty')"
stop_proxy

#############################################################################
step "11. on again"
start_proxy true || { bad "core did not restart"; tail -20 "$WORK/proxy.log"; exit 1; }
mcp_post "$B/mcp/oauth/vscode" "$INIT" -H "Authorization: Bearer $AT_Z"
eq "the surviving login works again" 200 "$CODE"
req GET "$B/.well-known/oauth-authorization-server"
eq "AS metadata served again" 200 "$CODE"
admin GET "/clients/vscode"
eq "browser_login_disabled false again" false "$(printf %s "$BODY" | jq -r .data.browser_login_disabled)"
stop_proxy

#############################################################################
echo
echo "passed: $PASS  failed: $FAIL"
if [ "$FAIL" -ne 0 ]; then
	echo "last core log lines:"
	tail -15 "$WORK/proxy.log" | mask
	exit 1
fi
echo "OK"

#!/usr/bin/env bash

set -euo pipefail

if [[ "${DEBUG:-}" == "1" ]]; then
  set -x
fi

BASE_URL_DEFAULT="http://127.0.0.1:8080"
API_BASE="${MCPPROXY_API_BASE:-$BASE_URL_DEFAULT}"

if [[ "$API_BASE" != *"/api/"* ]]; then
  API_BASE="${API_BASE%/}/api/v1"
fi

ARTIFACT_DIR="${ARTIFACT_DIR:-}" # optional path to dump responses

required() {
  if ! command -v "$1" >/dev/null 2>&1; then
    echo "missing required command: $1" >&2
    exit 1
  fi
}

required curl
required jq

mkdir -p "$ARTIFACT_DIR" 2>/dev/null || true

PASS=0
FAIL=0

log() {
  printf '\n[verify-api] %s\n' "$1"
}

record() {
  local name="$1" status="$2"
  case "$status" in
    pass) PASS=$((PASS + 1));;
    fail) FAIL=$((FAIL + 1));;
  esac
  printf '[%s] %s\n' "$status" "$name"
}

expect_success() {
  local name="$1" url="$2" jq_filter="$3" expect="$4"
  log "$name"
  local status body tmp
  tmp=$(mktemp)
  status=$(curl -sS -w '%{http_code}' -o "$tmp" "$url" || true)
  body=$(cat "$tmp")
  if [[ -n "$ARTIFACT_DIR" ]]; then
    printf '%s\n' "$body" > "$ARTIFACT_DIR/${name// /_}.json"
  fi
  if [[ "$status" != "200" ]]; then
    printf 'HTTP %s from %s\n%s\n' "$status" "$url" "$body" >&2
    record "$name" fail
    rm -f "$tmp"
    return 1
  fi
  local value
  value=$(jq -er "$jq_filter" <<<"$body" 2>/dev/null || true)
  if [[ "$value" != "$expect" ]]; then
    printf 'Unexpected payload for %s (expected %s, got %s)\n%s\n' "$url" "$expect" "$value" "$body" >&2
    record "$name" fail
    rm -f "$tmp"
    return 1
  fi
  record "$name" pass
  rm -f "$tmp"
  return 0
}

log "Checking /servers list"
expect_success "GET /servers" "$API_BASE/servers" '.success' 'true'

server_payload=$(curl -sS "$API_BASE/servers" || true)
server_id=""
if [[ -n "$server_payload" ]]; then
  server_id=$(jq -r '.data.servers[0].id // empty' <<<"$server_payload" 2>/dev/null || echo "")
fi

if [[ -z "$server_id" ]]; then
  log "No servers found; skipping per-server checks"
else
  expect_success "GET /servers/${server_id}/tools" "$API_BASE/servers/${server_id}/tools" '.success' 'true'
  expect_success "GET /servers/${server_id}/logs" "$API_BASE/servers/${server_id}/logs" '.success' 'true'
fi

expect_success "GET /index/search?q=ping" "$API_BASE/index/search?q=ping" '.success' 'true'

echo
echo "verify-api complete: ${PASS} passed, ${FAIL} failed"

if [[ $FAIL -ne 0 ]]; then
  exit 1
fi

exit 0

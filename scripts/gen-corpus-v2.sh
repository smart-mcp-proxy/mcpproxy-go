#!/usr/bin/env bash
#
# gen-corpus-v2.sh — Spec 083 one-time generator for the schema-bearing frozen
# corpus (specs/083-discovery-profiler/datasets/corpus_v2.tools.json).
#
# What it does (research D4):
#   1. builds mcpproxy from this worktree,
#   2. boots it over the committed Spec-065 snapshot config (7 no-auth
#      reference servers) on 127.0.0.1:8093 with a throwaway --data-dir
#      (never touches ~/.mcpproxy),
#   3. waits until GET /api/v1/tools reports the expected 45 tools,
#   4. kills the proxy, then exports the full tool definitions WITH input
#      schemas from the Bleve index (scripts/gen-corpus-v2-dump), canonicalizes
#      (tools sorted by tool_id; all object keys sorted via jq -S) and writes
#      the corpus file.
#
# Why the export reads the index instead of GET /api/v1/tools: that endpoint
# serves schemas from the supervisor StateView, which currently stores a stub
# ({"type":"object","properties":{}} — "TODO: Parse ParamsJSON" in
# internal/runtime/supervisor/supervisor.go), so every schema it returns is
# empty. The Bleve index holds the authoritative ParamsJSON the production
# retrieval funnel ingests — exactly what arm comparison must measure.
#
# The output is committed once and then immutable (Spec 065 CN-002 precedent:
# a refresh is corpus_v3, never an edit). Maintainers only.
#
# Config via env:
#   PORT            listen port          (default: 8093 — NOT 8092/8080, avoids
#                                         the docker-compose bench substrate and
#                                         a tray-owned local proxy)
#   EXPECTED_TOOLS  expected tool count  (default: 45, per Spec 065 datasets README)
#   WAIT_SECS       per-attempt wait for all servers to connect (default: 300)
#   OUT             output path          (default: specs/083-discovery-profiler/datasets/corpus_v2.tools.json)
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$REPO_ROOT"

PORT="${PORT:-8093}"
EXPECTED_TOOLS="${EXPECTED_TOOLS:-45}"
WAIT_SECS="${WAIT_SECS:-300}"
OUT="${OUT:-specs/083-discovery-profiler/datasets/corpus_v2.tools.json}"
CONFIG="specs/065-evaluation-foundation/datasets/snapshot-servers.config.json"
export MCPPROXY_API_KEY="eval-corpus-snapshot"
BASE_URL="http://127.0.0.1:${PORT}"

command -v jq >/dev/null || { echo "error: jq is required" >&2; exit 4; }
[ -f "$CONFIG" ] || { echo "error: snapshot config not found: $CONFIG" >&2; exit 4; }

WORK="$(mktemp -d)"
PROXY_PID=""
cleanup() {
  if [ -n "$PROXY_PID" ] && kill -0 "$PROXY_PID" 2>/dev/null; then
    kill "$PROXY_PID" 2>/dev/null || true
    wait "$PROXY_PID" 2>/dev/null || true
  fi
  rm -rf "$WORK"
}
trap cleanup EXIT

log() { printf '\n\033[1;34m==>\033[0m %s\n' "$*"; }

log "Building mcpproxy from the worktree"
go build -o "$WORK/mcpproxy" ./cmd/mcpproxy

# Serve from a COPY of the committed config: mcpproxy persists runtime config
# changes back to the file it was started with, and the Spec-065 snapshot
# config is a frozen committed fixture that must never be rewritten.
cp "$CONFIG" "$WORK/config.json"
CONFIG="$WORK/config.json"

# The snapshot config's sqlite server persists to this fixed path; the server
# creates the db file but not the directory.
mkdir -p /tmp/mcpproxy-corpus-snapshot

tool_count() {
  curl -fsS -m 10 -H "X-API-Key: $MCPPROXY_API_KEY" "$BASE_URL/api/v1/tools" 2>/dev/null \
    | jq -r '.data.tools | length' 2>/dev/null || echo 0
}

start_proxy() {
  local data_dir="$1"
  mkdir -p "$data_dir"
  "$WORK/mcpproxy" serve \
    --config "$CONFIG" \
    --listen "127.0.0.1:${PORT}" \
    --data-dir "$data_dir" \
    >"$WORK/proxy.log" 2>&1 &
  PROXY_PID=$!
}

stop_proxy() {
  if [ -n "$PROXY_PID" ] && kill -0 "$PROXY_PID" 2>/dev/null; then
    kill "$PROXY_PID" 2>/dev/null || true
    wait "$PROXY_PID" 2>/dev/null || true
  fi
  PROXY_PID=""
}

# wait_for_tools polls until EXPECTED_TOOLS is reached (echoes the count and
# returns 0) or WAIT_SECS elapses (echoes the last stable count, returns 1).
wait_for_tools() {
  local waited=0 count=0
  while [ "$waited" -lt "$WAIT_SECS" ]; do
    count="$(tool_count)"
    if [ "$count" -ge "$EXPECTED_TOOLS" ]; then
      echo "$count"
      return 0
    fi
    sleep 5
    waited=$((waited + 5))
  done
  echo "$count"
  return 1
}

# Attempt 1 (+ one full retry for transient npx/uvx download failures).
ATTEMPT=1
COUNT=0
DATA_DIR=""
while :; do
  log "Booting snapshot proxy on ${BASE_URL} (attempt ${ATTEMPT}, waiting up to ${WAIT_SECS}s for ${EXPECTED_TOOLS} tools)"
  DATA_DIR="$WORK/data-${ATTEMPT}"
  start_proxy "$DATA_DIR"
  if COUNT="$(wait_for_tools)"; then
    log "All ${COUNT} tools indexed"
    break
  fi
  if [ "$ATTEMPT" -ge 2 ]; then
    if [ "$COUNT" -eq 0 ]; then
      echo "error: no tools appeared after ${ATTEMPT} attempts — proxy log tail:" >&2
      tail -30 "$WORK/proxy.log" >&2
      exit 1
    fi
    echo "::warning:: only ${COUNT}/${EXPECTED_TOOLS} tools after ${ATTEMPT} attempts — proceeding with the REDUCED corpus. Record the reduced count + the missing server in datasets/README.md." >&2
    break
  fi
  echo "::warning:: ${COUNT}/${EXPECTED_TOOLS} tools after ${WAIT_SECS}s — restarting proxy once (transient npx/uvx download failures)" >&2
  stop_proxy
  ATTEMPT=$((ATTEMPT + 1))
done

# Stop the proxy BEFORE the export: the dump helper opens the Bleve index
# directly (single-writer lock) and reads the authoritative ParamsJSON.
stop_proxy

log "Exporting + canonicalizing corpus_v2 (${COUNT} tools) from the index at ${DATA_DIR}"
CAPTURE_DATE="$(date -u +%Y-%m-%d)"
mkdir -p "$(dirname "$OUT")"
go run ./scripts/gen-corpus-v2-dump -data-dir "$DATA_DIR" \
  | jq -S --arg version "corpus_v2@${CAPTURE_DATE}" --arg date "$CAPTURE_DATE" '{
      version: $version,
      generated_from: {
        note: "Spec 083 D4 schema-bearing frozen corpus: the 7 no-auth Spec-065 reference servers (filesystem, git, memory, sqlite, fetch, time, sequential-thinking) via snapshot-servers.config.json, exported WITH full JSON input schemas.",
        source: "Bleve index ParamsJSON via scripts/gen-corpus-v2-dump (GET /api/v1/tools serves stub schemas — see header comment)",
        config: "specs/065-evaluation-foundation/datasets/snapshot-servers.config.json",
        generator: "scripts/gen-corpus-v2.sh",
        date: $date
      },
      tools: .
    }' > "$OUT"

TOOLS_IN_FILE="$(jq '.tools | length' "$OUT")"
SCHEMALESS="$(jq '[.tools[] | select((.schema // empty | type) != "object" or (.schema | length) == 0)] | map(.tool_id)' "$OUT")"

log "Verification"
echo "  tools exported:      ${TOOLS_IN_FILE}"
echo "  schema-less tools:   ${SCHEMALESS}"
if [ "$SCHEMALESS" != "[]" ]; then
  echo "error: corpus_v2 must be schema-bearing — the tools above have empty/missing schemas" >&2
  exit 1
fi
if [ "$TOOLS_IN_FILE" -lt "$EXPECTED_TOOLS" ]; then
  echo "::warning:: reduced corpus (${TOOLS_IN_FILE}/${EXPECTED_TOOLS} tools) — note this explicitly in specs/083-discovery-profiler/datasets/README.md" >&2
fi

log "Wrote ${OUT} (${TOOLS_IN_FILE} tools, capture date ${CAPTURE_DATE})"
echo "Next: record tool count + provenance in specs/083-discovery-profiler/datasets/README.md and commit both."

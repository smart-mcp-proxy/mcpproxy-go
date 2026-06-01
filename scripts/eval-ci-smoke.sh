#!/usr/bin/env bash
#
# eval-ci-smoke.sh — Spec 065 / D2 security regression gate (and local smoke).
#
# Runs the deterministic D2 half of the Spec-065 evaluation end to end:
#   1. provenance/license guard over the security corpus (FR-007 / CN-005),
#   2. scan-eval (this repo) N times -> per-run verdict JSON (FR-010 averaging),
#   3. mcp-eval SecurityScorer gate (P/R/F1/FPR per detector, absolute thresholds).
#
# It is the single source of truth shared by `.github/workflows/eval.yml` (Job A)
# and local pre-flight, so the gate logic is proven the same way in both places.
#
# Exit non-zero if the corpus fails the provenance guard, scan-eval fails, or the
# SecurityScorer gate fails (FPR above ceiling / recall below floor).
#
# Config via env (all have CI-friendly defaults; paths may be relative to repo root):
#   DATASETS_DIR   dataset directory (default: specs/065-evaluation-foundation/datasets)
#   MCP_EVAL_DIR   checkout of smart-mcp-proxy/mcp-eval. If unset/missing, the
#                  SecurityScorer step is SKIPPED (Go-only smoke) with a warning.
#   OUT_DIR        report output dir (default: reports/security) — never committed (CN-003)
#   RUNS           number of scan-eval runs to average (default: 3)
#   FPR_CEILING    max allowed per-detector false-positive rate (default: 0.10)  # matches MCP-815
#   RECALL_FLOOR   min allowed detector recall (default: 0.05)                   # matches MCP-815
#
# Threshold provenance (critical): the SecurityScorer *defaults* are recall-floor
# 0.80, but the production `sensitive-data` detector measures recall ~= 0.10 on
# this corpus (most malicious entries are prompt-injection / tool-poisoning /
# rug-pull — out of scope for a secret/path detector). The gate therefore uses
# the MCP-815 thresholds below, not the defaults. Once MCP-815 lands a
# `security.gate` block in baseline_v1.json, source these from there so the gate
# and the baseline never drift.
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$REPO_ROOT"

DATASETS_DIR="${DATASETS_DIR:-specs/065-evaluation-foundation/datasets}"
OUT_DIR="${OUT_DIR:-reports/security}"
RUNS="${RUNS:-3}"
FPR_CEILING="${FPR_CEILING:-0.10}"   # matches MCP-815
RECALL_FLOOR="${RECALL_FLOOR:-0.05}" # matches MCP-815
CORPUS="${DATASETS_DIR}/security_corpus_v1.json"

ALLOWED_LICENSES="MIT Apache-2.0 BSD-3-Clause CC0-1.0 self-authored"

log() { printf '\n\033[1;34m==>\033[0m %s\n' "$*"; }

[ -f "$CORPUS" ] || { echo "error: corpus not found: $CORPUS" >&2; exit 4; }

log "Provenance / license guard (FR-007 / CN-005) over $CORPUS"
ALLOWED_LICENSES="$ALLOWED_LICENSES" python3 - "$CORPUS" <<'PY'
import json, os, sys
corpus = json.load(open(sys.argv[1]))
entries = corpus["entries"] if isinstance(corpus, dict) else corpus
allowed = set(os.environ["ALLOWED_LICENSES"].split())
bad = []
for e in entries:
    prov = e.get("provenance") or {}
    lic = prov.get("license")
    if not e.get("category"):
        bad.append(f'{e.get("id","?")}: missing category')
    if not prov.get("source"):
        bad.append(f'{e.get("id","?")}: missing provenance.source')
    if lic not in allowed:
        bad.append(f'{e.get("id","?")}: license {lic!r} not in allowlist {sorted(allowed)}')
if bad:
    print("PROVENANCE GUARD FAILED:", file=sys.stderr)
    for b in bad:
        print("  -", b, file=sys.stderr)
    sys.exit(5)
print(f"OK: {len(entries)} entries, all carry category + allowlisted provenance.license")
PY

WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT
VERDICT_ARGS=()
log "Running scan-eval x${RUNS} (deterministic detector; N for FR-010 averaging contract)"
for i in $(seq 1 "$RUNS"); do
  vf="${WORK}/verdicts_${i}.json"
  go run ./cmd/scan-eval --corpus "$CORPUS" --out "$vf"
  VERDICT_ARGS+=(--verdicts "$vf")
done
echo "Produced ${RUNS} verdict file(s)."

if [ -z "${MCP_EVAL_DIR:-}" ] || [ ! -d "${MCP_EVAL_DIR:-/nonexistent}" ]; then
  echo "::warning::MCP_EVAL_DIR unset or missing — skipping the SecurityScorer gate (Go-only smoke). Set MCP_EVAL_DIR to a smart-mcp-proxy/mcp-eval checkout to run the full gate."
  exit 0
fi

mkdir -p "$OUT_DIR"
ABS_CORPUS="$(cd "$(dirname "$CORPUS")" && pwd)/$(basename "$CORPUS")"
ABS_OUT="$(cd "$OUT_DIR" && pwd)"
log "SecurityScorer gate: fpr-ceiling=${FPR_CEILING} recall-floor=${RECALL_FLOOR} (MCP-815 thresholds)"
# mcp-eval is run as a module with PYTHONPATH=src (its console-script entry point
# is not installed by `uv sync`); uv supplies the synced 3.11 interpreter.
( cd "$MCP_EVAL_DIR" && PYTHONPATH=src uv run python -m mcp_eval.cli security \
    "${VERDICT_ARGS[@]}" \
    --corpus "$ABS_CORPUS" \
    --fpr-ceiling "$FPR_CEILING" \
    --recall-floor "$RECALL_FLOOR" \
    --out-dir "$ABS_OUT" )

log "D2 security gate PASSED — reports in $OUT_DIR"

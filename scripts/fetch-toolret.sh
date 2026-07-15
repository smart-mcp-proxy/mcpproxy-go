#!/usr/bin/env bash
#
# fetch-toolret.sh — Spec 083 runtime fetch of the ToolRet benchmark
# (research D5, FR-013). Downloads mangopy/ToolRet-Tools and
# mangopy/ToolRet-Queries from Hugging Face at PINNED revisions, converts the
# parquet shards to the JSON cache shape bench/corpusio/toolret.go loads, and
# writes them to bench/results/cache/toolret/<revision>/{tools.json,queries.json}.
#
# The cache is runtime-only and MUST NEVER be committed: the ToolRet dataset's
# license is unstated upstream (verified 2026-07-14). bench/.gitignore covers
# bench/results/ (and results/cache/ redundantly) so the fetched bytes are not
# committable.
#
# Upstream parquet schemas (inspected 2026-07-14 at the pinned revisions via
# pyarrow — NOT guessed):
#   ToolRet-Tools   (configs code|customized|web, split "tools"):
#       id: string, documentation: string            (44,453 rows total)
#   ToolRet-Queries (35 per-source configs, split "queries"):
#       id, query, instruction, labels, category: string   (7,961 rows total)
#   The `labels` column is a JSON-encoded array of {id, doc, relevance};
#   `relevance` is an integer (value 1 everywhere at the pinned revision —
#   binary labels); nested `doc` objects can contain bare NaN literals
#   (Python-tolerated, invalid strict JSON), so conversion happens here in
#   Python and the redundant `doc` is dropped — the cache keeps {id, relevance}.
#   Exactly one upstream row (mnms_query_17) has empty query text at the
#   pinned revision; it is dropped and counted in the queries.json envelope
#   (dropped_empty_queries) so the Go loader can surface it.
#
# Requirements: uv (https://docs.astral.sh/uv/) + network access to
# huggingface.co. Python deps (huggingface_hub, pyarrow) are resolved by
# `uv run --with` and are NOT project dependencies.
#
# Config via env (defaults are the pinned revisions recorded below):
#   TOOLRET_TOOLS_REVISION    HF revision (commit sha) of mangopy/ToolRet-Tools
#   TOOLRET_QUERIES_REVISION  HF revision (commit sha) of mangopy/ToolRet-Queries
#   OUT_DIR                   cache dir (default: bench/results/cache/toolret/<rev12>-<rev12>)
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$REPO_ROOT"

# Pinned HF revisions — the `main` commit shas of both datasets as of
# 2026-07-14 (last modified upstream 2025-03-08 / 2025-03-23). Bumping a pin
# is a reviewed event: it changes every downstream ToolRet report row.
TOOLRET_TOOLS_REVISION="${TOOLRET_TOOLS_REVISION:-e06c38c75612b6536bd959e08cdd345894aba6a7}"
TOOLRET_QUERIES_REVISION="${TOOLRET_QUERIES_REVISION:-b8c76ad3349ff17497b6bdb28bb5b8f61a0f6445}"

# Cache path stamps both pins (FR-014: revision + seed + size identify a subset).
REV_ID="${TOOLRET_TOOLS_REVISION:0:12}-${TOOLRET_QUERIES_REVISION:0:12}"
OUT_DIR="${OUT_DIR:-bench/results/cache/toolret/${REV_ID}}"

command -v uv >/dev/null || {
  echo "error: uv is required (https://docs.astral.sh/uv/getting-started/installation/)" >&2
  exit 4
}

log() { printf '\n\033[1;34m==>\033[0m %s\n' "$*"; }

if [ -f "$OUT_DIR/tools.json" ] && [ -f "$OUT_DIR/queries.json" ]; then
  log "Cache already present: $OUT_DIR (delete it to re-fetch)"
  exit 0
fi

log "Fetching ToolRet at pinned revisions into $OUT_DIR"
mkdir -p "$OUT_DIR"

TOOLRET_OUT_DIR="$OUT_DIR" \
TOOLRET_TOOLS_REVISION="$TOOLRET_TOOLS_REVISION" \
TOOLRET_QUERIES_REVISION="$TOOLRET_QUERIES_REVISION" \
uv run --with huggingface_hub,pyarrow python - <<'PYEOF'
import glob
import json
import os
import sys

import pyarrow.parquet as pq
from huggingface_hub import snapshot_download

TOOLS_REPO = "mangopy/ToolRet-Tools"
QUERIES_REPO = "mangopy/ToolRet-Queries"
tools_rev = os.environ["TOOLRET_TOOLS_REVISION"]
queries_rev = os.environ["TOOLRET_QUERIES_REVISION"]
out_dir = os.environ["TOOLRET_OUT_DIR"]


def die(msg):
    print(f"error: {msg}", file=sys.stderr)
    sys.exit(1)


def fetch_parquets(repo, rev):
    """Download all parquet shards of a dataset repo at the pinned revision."""
    try:
        snap = snapshot_download(
            repo, repo_type="dataset", revision=rev, allow_patterns=["*.parquet"]
        )
    except Exception as e:  # noqa: BLE001 — one actionable message for any HF failure
        die(
            f"download {repo}@{rev} failed: {e}\n"
            "  - check network access to huggingface.co\n"
            "  - check the pinned revision still exists (HF UI -> Files -> History)"
        )
    files = sorted(glob.glob(os.path.join(snap, "**", "*.parquet"), recursive=True))
    if not files:
        die(f"{repo}@{rev}: no parquet files in snapshot {snap}")
    return snap, files


def config_of(snap, path):
    """HF config name = first path component of the shard inside the snapshot."""
    rel = os.path.relpath(path, snap)
    return rel.split(os.sep)[0]


def write_atomic(path, payload):
    tmp = path + ".tmp"
    with open(tmp, "w", encoding="utf-8") as f:
        # sort_keys + compact separators + ensure_ascii: byte-deterministic
        # cache for identical inputs (FR-010). allow_nan=False: bare NaN must
        # never reach the Go loader (encoding/json rejects it).
        json.dump(payload, f, sort_keys=True, separators=(",", ":"),
                  ensure_ascii=True, allow_nan=False)
        f.write("\n")
    os.replace(tmp, path)


# --- tools ---
snap, files = fetch_parquets(TOOLS_REPO, tools_rev)
tools = []
seen = {}
for f in files:
    config = config_of(snap, f)
    for i, row in enumerate(pq.read_table(f).to_pylist()):
        tid = row.get("id")
        doc = row.get("documentation")
        if not tid:
            die(f"{TOOLS_REPO} {config} row {i}: missing id")
        if not isinstance(doc, str) or not doc.strip():
            die(f"{TOOLS_REPO} tool {tid!r}: empty documentation")
        if tid in seen:
            if seen[tid] != doc:
                die(f"{TOOLS_REPO}: conflicting duplicate tool id {tid!r} across configs")
            continue  # identical duplicate: keep first
        seen[tid] = doc
        tools.append({"id": tid, "config": config, "documentation": doc})
tools.sort(key=lambda t: t["id"])

# --- queries ---
snap, files = fetch_parquets(QUERIES_REPO, queries_rev)
queries = []
qseen = set()
dropped_empty = 0
for f in files:
    config = config_of(snap, f)
    for i, row in enumerate(pq.read_table(f).to_pylist()):
        qid = row.get("id")
        if not qid:
            die(f"{QUERIES_REPO} {config} row {i}: missing id")
        if qid in qseen:
            die(f"{QUERIES_REPO}: duplicate query id {qid!r}")
        qseen.add(qid)
        qtext = (row.get("query") or "").strip()
        if not qtext:
            # Known at the pinned revision: mnms_query_17 has empty query text.
            print(f"  dropping {qid}: empty query text", file=sys.stderr)
            dropped_empty += 1
            continue
        try:
            # json.loads tolerates the bare NaN literals inside nested `doc`s.
            raw_labels = json.loads(row["labels"])
        except Exception as e:  # noqa: BLE001
            die(f"{QUERIES_REPO} query {qid!r}: unparseable labels column: {e}")
        labels = []
        for j, lab in enumerate(raw_labels):
            lid = lab.get("id")
            if not lid:
                die(f"{QUERIES_REPO} query {qid!r} label {j}: missing id")
            rel = lab.get("relevance", 1)
            try:
                rel = int(rel)
            except (TypeError, ValueError):
                die(f"{QUERIES_REPO} query {qid!r} label {lid!r}: non-integer relevance {rel!r}")
            labels.append({"id": lid, "relevance": rel})
        labels.sort(key=lambda l: l["id"])
        queries.append({
            "id": qid,
            "config": config,
            "category": row.get("category") or "",
            "query": row.get("query"),
            "instruction": row.get("instruction") or "",
            "labels": labels,
        })
queries.sort(key=lambda q: q["id"])

write_atomic(os.path.join(out_dir, "tools.json"), {
    "dataset": TOOLS_REPO,
    "revision": tools_rev,
    "tools": tools,
})
write_atomic(os.path.join(out_dir, "queries.json"), {
    "dataset": QUERIES_REPO,
    "revision": queries_rev,
    "dropped_empty_queries": dropped_empty,
    "queries": queries,
})
print(f"tools: {len(tools)}  queries: {len(queries)}  dropped_empty_queries: {dropped_empty}")
PYEOF

log "Wrote $OUT_DIR/tools.json and $OUT_DIR/queries.json"
echo "Reminder: this cache is gitignored and must stay uncommitted (ToolRet license unstated upstream, FR-013)."

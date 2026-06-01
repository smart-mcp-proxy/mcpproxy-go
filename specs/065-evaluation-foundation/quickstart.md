# Quickstart: Build & run the Evaluation Foundation (D1+D2)

Operator runbook. Harness = `~/repos/mcp-eval`; datasets versioned in `specs/065-evaluation-foundation/datasets/`.

## 0. Prereqs
- `mcp-eval` repo (`~/repos/mcp-eval`, `uv`), a built `mcpproxy` binary, and the eval API key the harness already manages.
- Confirm the search endpoint is reachable: `curl -H "X-API-Key: <key>" "http://127.0.0.1:8080/api/v1/index/search?q=docker&limit=5"` → JSON `[{server,tool,score,…}]`.

## 1. Freeze the corpus (FR-012, CN-002)
```bash
# documented, repeatable; produces datasets/corpus_v1.json from current tool descriptions
mcp-eval datasets snapshot --out specs/065-evaluation-foundation/datasets/corpus_v1.json
```
Commit `corpus_v1.json`. Refresh later = `corpus_v2.json` (never mutate v1).

## 2. Build the D1 golden set
1. Generate synthetic queries (3–5 per tool, paraphrased — never name the tool): `mcp-eval datasets gen-queries --corpus corpus_v1.json --out retrieval_golden_v1.json`.
2. Add human-verified hard negatives (near-duplicate tools across servers).
3. Validate against `contracts/retrieval-dataset.schema.json` (every `tool_id` ∈ corpus). Commit.

## 3. Build the D2 security corpus
1. Vendor license-clear malicious samples (DVMCP MIT + self-authored per category) + benign descriptions from the corpus + attack-resembling hard negatives.
2. Each entry carries `label`, `category`, `provenance.license` (CI rejects missing/restricted — FR-007/CN-005).
3. Validate against `contracts/security-corpus.schema.json`. Commit.

## 4. Run D1 (retrieval)
```bash
mcp-eval retrieval --corpus corpus_v1.json --golden retrieval_golden_v1.json \
  --baseline datasets/baseline_v1.json --runs 1
# → Recall@1/3/5/10, MRR, nDCG@10, baseline deltas; HTML/JSON report (not committed)
```
Self-test (SC-003): re-run against a deliberately degraded index → scores drop.

## 5. Run D2 (security)
```bash
# bridge: cmd/scan-eval (in mcpproxy-go) emits per-entry detector verdicts as JSON
go run ./cmd/scan-eval --corpus specs/065-evaluation-foundation/datasets/security_corpus_v1.json --out /tmp/verdicts.json
mcp-eval security --verdicts /tmp/verdicts.json --corpus security_corpus_v1.json \
  --baseline datasets/baseline_v1.json --runs 3
# → per-detector precision/recall/F1/FPR + pass/fail vs FPR ceiling & recall floor
```
Self-test (SC-004): add a noisy detector → its FPR rises visibly.

## 6. CI gate (FR-009)
`.github/workflows/eval.yml`: freeze corpus → run D1 + D2 → fail if Recall@5 < baseline−tolerance or any detector FPR > ceiling. Reports uploaded as run artifacts (not committed).

## 7. Verify success criteria
- SC-001/002: one command each → the metric sets.
- SC-003/004: degraded index ↓ retrieval; noisy detector ↑ FPR.
- SC-005: CI fails on either regression.
- SC-006: `retrieval_golden_v1.json` is consumed unchanged by the future GEPA (D5) loop.
- SC-007: unchanged system reproduces baseline within tolerance.
- SC-008: every security entry has category + license; no restricted corpus vendored.

## Next (follow-on specs, out of scope here)
D3 dynamic ASR (AgentDojo/DVMCP through live proxy) · D4 sensitive-data eval · D5 GEPA (fitness = this D1 set) · D6 full CI dashboard.

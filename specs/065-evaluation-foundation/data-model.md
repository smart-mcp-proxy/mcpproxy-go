# Phase 1 Data Model: Evaluation Foundation (D1+D2)

No new persistent storage in mcpproxy-go. The "data model" is the **versioned JSON dataset + report artifacts**. Schemas formalized in `contracts/`.

## 1. Tool corpus snapshot (`datasets/corpus_v1.json`)
Frozen list the evaluations score against (CN-002).
- **Fields**: `version`, `generated_from` (source + note), `tools[]` where each = `{ tool_id: "<server>:<tool>", server, tool, description, schema }`.
- **Invariant**: `tool_id` unique; immutable once committed — refresh = new `corpus_v2.json` (FR-012).

## 2. Retrieval golden set (`datasets/retrieval_golden_v1.json`)
The D1 labeled set.
- **Fields**: `corpus_version` (FK → corpus snapshot), `queries[]` where each = `{ id, query (paraphrased intent, never names the tool — R-C), labels: [{ tool_id, relevance: 0|1|2 }], notes }`.
- **Validation**: every `labels[].tool_id` exists in the referenced corpus; ≥1 label with relevance≥1; hard-negative queries flagged (`notes: "hard-negative: near-dup of <tool_id>"`).
- **Used by**: RetrievalScorer (D1), and unchanged by GEPA (D5) as fitness (FR-011/SC-006).

## 3. Security corpus (`datasets/security_corpus_v1.json`)
The D2 labeled set.
- **Fields**: `entries[]` where each = `{ id, description (the tool description text under test), label: "malicious"|"benign", category: "tool_poisoning"|"prompt_injection"|"shadowing"|"rug_pull"|"benign"|"hard_negative", provenance: { source, license } }`.
- **Validation**: every entry has `label` + `category` + `provenance.license` (FR-007); no entry whose `provenance.license` is redistribution-restricted (CN-005); ≥1 hard-negative benign per attack category.

## 4. Retrieval score report (generated, NOT committed — CN-003)
- **Fields**: `corpus_version`, `golden_version`, `metrics: { recall_at: {1,3,5,10}, mrr, ndcg_at_10, map }`, `per_query[]`, `baseline_delta: { recall_at_5, mrr, … }`, `gate: { passed, tolerance }`.

## 5. Security score report (generated, NOT committed)
- **Fields**: `per_detector[]` = `{ detector, precision, recall, f1, fpr, tp, fp, tn, fn }`, `runs_averaged: N`, `gate: { detector, fpr_ceiling, recall_floor, passed }`.

## 6. Baseline + thresholds (committed)
- `datasets/baseline_v1.json`: reference metric values + per-gate thresholds (Recall@5 tolerance; per-detector FPR ceiling + recall floor). The CI gate (FR-009) diffs a fresh report against this.

## Cross-entity invariants (testable)
- **INV-1**: golden-set `tool_id`s ⊆ corpus `tool_id`s (no dangling labels).
- **INV-2**: removing a labeled tool from the corpus drives that query's Recall to 0 (US1 #3 / SC-003 — proves the scorer isn't trivially passing).
- **INV-3**: a known tool-poisoning entry is a true positive; an attack-resembling benign entry that gets flagged increments FP and is visible (SC-004).
- **INV-4**: every security entry carries category + license; build fails if any is missing or restricted (FR-007/CN-005).
- **INV-5**: re-running on an unchanged system reproduces baseline within tolerance (SC-007).

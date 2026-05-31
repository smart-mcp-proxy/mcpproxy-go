# Feature Specification: Evaluation Foundation — Measure Security & Discovery

**Feature Branch**: `065-evaluation-foundation`
**Created**: 2026-05-31
**Status**: Draft
**Lineage**: First implementation epic of the H2-2026 roadmap (`specs/ROADMAP-2026-H2.md`, the "Evaluation Foundation" section, deliverables D1–D6). Unblocks the TRUST + ADOPT pillars; D1 is the fitness function for later GEPA description-optimization.
**Input**: Move mcpproxy from *asserting* its security and tool-discovery features work to *measuring* them with regression-gated metrics. Today the scanners/detectors have no measured precision/recall, and the companion `mcp-eval` harness scores trajectory similarity only — there is no labeled query→tool dataset and no attack corpus. Build the labeled datasets, the metrics, and the harness extensions that verify these features actually work, and gate them in CI.

## Clarifications

### Session 2026-05-31

- Q: What is the goal? → A: A real evaluation foundation — datasets + metrics + harness — so security and discovery quality are measured and regression-gated, not asserted.
- Q: Build a new harness? → A: No. Extend the existing `mcp-eval` repo (`~/repos/mcp-eval`, MIT), which already provides YAML scenarios, Docker isolation, baseline-vs-current comparison, and HTML reports. Add an IR-metrics scorer and a security scorer.
- Q: Scope of this first spec? → A: The two foundational deliverables — **D1** (tool-retrieval golden set + Recall@k/MRR/nDCG scorer) and **D2** (security regression corpus + precision/recall/F1/FPR detector scorer). D3 (dynamic ASR), D4 (sensitive-data eval), D5 (GEPA), D6 (CI gate) are follow-on specs that compose on these.
- Q: Why these two first? → A: D1 unblocks all discovery verification and is GEPA's fitness function; D2 gives the scanners their first measured precision/recall, and because real attack base-rates are tiny, false-positive rate is the metric that most serves "quiet security." They are independent and parallelizable.
- Q: Contamination control? → A: Freeze the evaluated tool corpus as a versioned snapshot committed alongside the golden sets; never score against a live, drifting corpus.
- Q: Where do datasets live? → A: Committed under `specs/065-evaluation-foundation/datasets/` (and mirrored into the mcp-eval repo as needed); reports are generated locally, not committed into PRs (repo rule).

## User Scenarios & Testing *(mandatory)*

### User Story 1 — Verify tool discovery returns the right tools (D1) (Priority: P1)

A maintainer changes the search index, tokenizer, or a tool description and needs to know whether tool discovery got better or worse — objectively, not by spot-checking. They run the retrieval evaluation: every labeled query is sent through the proxy's tool search, and the result is scored against the known-relevant tool(s). The maintainer sees Recall@5, MRR, and nDCG@10 versus the previous baseline, and CI fails if discovery quality regressed beyond a small tolerance.

**Why this priority**: Discovery quality is mcpproxy's core value proposition (retrieval-first tool selection), yet it is currently unmeasured. This dataset is also the prerequisite for per-profile verification (A1), routing-mode evaluation (A3), and the GEPA description-optimizer's fitness function (D5). It is the single highest-leverage asset in the roadmap.

**Independent Test**: Run the retrieval scorer over the golden set against a frozen corpus; confirm it emits Recall@5/MRR/nDCG numbers, that a deliberately degraded index lowers the scores, and that an unchanged index reproduces the baseline within tolerance.

**Acceptance Scenarios**:

1. **Given** a labeled `query → relevant-tool(s)` golden set and a frozen tool corpus, **When** the retrieval scorer runs, **Then** it reports Recall@5, MRR, and nDCG@10 over all queries.
2. **Given** a baseline score set, **When** the index is changed and the scorer re-runs, **Then** the report shows the delta per metric and flags a regression if Recall@5 drops more than the configured tolerance.
3. **Given** a query whose relevant tool is intentionally removed from the corpus, **When** the scorer runs, **Then** that query's Recall is 0 (the harness correctly detects a miss, proving it isn't trivially passing).

---

### User Story 2 — Verify the security scanners actually catch attacks (D2) (Priority: P1)

A maintainer wants to know whether mcpproxy's quarantine / tool-poisoning detection / scanner plugins actually flag malicious tool descriptions, and — critically — how often they false-alarm on benign ones. They run the security detector evaluation over a labeled corpus of malicious and benign tool descriptions and see precision, recall, F1, and false-positive rate per detector, with a CI gate on the false-positive rate.

**Why this priority**: Security is the product's north star, but the scanners have zero measured precision/recall today. Because confirmed-malicious tools are rare in the real ecosystem, a noisy detector (high false-positive rate) is the dominant failure mode and the thing that makes users turn security off — so measuring and bounding FPR *is* the "quiet security" work. Equal priority to D1 because they are independent and both foundational.

**Independent Test**: Run the security scorer over the labeled corpus; confirm it emits per-detector precision/recall/F1/FPR, that a known tool-poisoning sample is flagged (true positive), and that a benign sample is not (no false positive).

**Acceptance Scenarios**:

1. **Given** a labeled corpus of malicious and benign tool descriptions, **When** the security scorer runs against a detector, **Then** it reports precision, recall, F1, and false-positive rate for that detector.
2. **Given** a canonical tool-poisoning sample, **When** scored, **Then** it is labeled a true positive by the detector under test.
3. **Given** a benign tool description that superficially resembles an attack (hard negative), **When** scored, **Then** a false positive is counted and visible in the report, so noisy detectors are exposed.
4. **Given** a configured false-positive-rate ceiling, **When** a detector exceeds it, **Then** the evaluation fails (the CI gate).

---

### User Story 3 — Both evaluations run in CI as regression gates (Priority: P2)

The two evaluations run automatically (locally and in CI), report against committed baselines, and fail the build when discovery quality regresses or a detector's false-positive rate exceeds its ceiling. Results are reproducible across runs (accounting for any non-determinism by averaging over N runs).

**Why this priority**: Datasets and scorers only prevent regressions if they run automatically. P2 because the datasets + scorers (P1) must exist first; this wires them into the gate.

**Independent Test**: Trigger the CI job on a change that degrades discovery or raises a detector's FPR; confirm the job fails with a clear report. Trigger on a no-op change; confirm it passes and reproduces the baseline within tolerance.

**Acceptance Scenarios**:

1. **Given** committed baselines and thresholds, **When** the eval CI job runs on a regression, **Then** it fails and names the regressed metric.
2. **Given** a non-deterministic scoring step, **When** the job runs, **Then** it averages over N runs and reports mean ± tolerance rather than a single flaky number.

## Requirements *(mandatory)*

### Context & Constraints (locked)

- **CN-001**: Extend the existing `mcp-eval` harness; do not build a new framework. Reuse its YAML scenarios, Docker isolation, baseline comparison, and HTML reporting.
- **CN-002**: The evaluated tool corpus MUST be a frozen, versioned snapshot committed with the golden sets; scoring never runs against a live, drifting corpus.
- **CN-003**: Generated reports MUST NOT be committed into PRs (repo rule); only the datasets, scorers, baselines, and thresholds are versioned.
- **CN-004**: Scope is D1 + D2 only. D3/D4/D5/D6 are separate follow-on specs that build on these.
- **CN-005**: Externally-licensed attack corpora that restrict redistribution MUST NOT be vendored into the repo; reference them as external benchmarks and vendor only permissively-licensed or self-authored samples.

### Functional Requirements

- **FR-001**: The system MUST provide a labeled tool-retrieval dataset of `query → relevant-tool(s)` pairs over a frozen tool corpus, covering both common intents and hard negatives (near-duplicate tools across servers).
- **FR-002**: The system MUST score tool retrieval with Recall@k (k=1,3,5,10), MRR, and nDCG@10 by sending each query through mcpproxy's tool-search path and comparing returned tools to the labels.
- **FR-003**: The retrieval scorer MUST compare against a committed baseline and report per-metric deltas.
- **FR-004**: The system MUST provide a labeled security corpus of malicious and benign tool descriptions, with malicious samples spanning the documented attack categories (tool poisoning, prompt injection in descriptions, shadowing, rug-pull) and benign samples drawn from realistic tool descriptions.
- **FR-005**: The system MUST score each security detector / scanner with precision, recall, F1, and false-positive rate over the labeled corpus.
- **FR-006**: The security evaluation MUST support a configurable false-positive-rate ceiling and a recall floor, and report pass/fail against them.
- **FR-007**: Each malicious corpus entry MUST carry a category label and a provenance note (source + license) so coverage and redistribution constraints are auditable.
- **FR-008**: Both evaluations MUST be runnable from a single documented command and produce an HTML/JSON report (report artifacts not committed).
- **FR-009**: Both evaluations MUST be runnable in CI as a gate, failing on discovery regression beyond tolerance or detector FPR above ceiling.
- **FR-010**: Scoring steps with run-to-run variance MUST be averaged over a configurable N runs and reported as mean ± tolerance.
- **FR-011**: The retrieval dataset and scorer MUST be structured so a later GEPA optimization (D5) can consume the same labeled set as its fitness function without modification.
- **FR-012**: The frozen corpus and golden sets MUST be regenerable by a documented, repeatable procedure (so the corpus can be refreshed deliberately, with a new version).

### Key Entities

- **Tool corpus snapshot**: A frozen, versioned list of `(server:tool, description, schema)` the evaluations score against.
- **Retrieval golden set**: Labeled `query → relevant-tool-id(s)` pairs (with graded relevance for nDCG), plus hard negatives.
- **Security corpus**: Labeled malicious + benign tool descriptions, each with category + provenance/license.
- **Retrieval score report**: Per-metric values (Recall@k/MRR/nDCG) + baseline deltas.
- **Security score report**: Per-detector precision/recall/F1/FPR + pass/fail vs thresholds.
- **Baseline + thresholds**: Committed reference scores and the regression/FPR gates.

## Success Criteria *(mandatory)*

- **SC-001**: A maintainer can run one command and get Recall@5/MRR/nDCG@10 for tool discovery over the golden set.
- **SC-002**: A maintainer can run one command and get precision/recall/F1/false-positive-rate per security detector over the labeled corpus.
- **SC-003**: A deliberately degraded index measurably lowers the retrieval scores (the harness detects real regressions, not just passes).
- **SC-004**: A deliberately noisy detector shows a measurably higher false-positive rate (the harness exposes noise, which is the quiet-security signal).
- **SC-005**: CI fails on a discovery regression beyond tolerance and on a detector exceeding its false-positive-rate ceiling.
- **SC-006**: The retrieval golden set is consumable unchanged as the GEPA (D5) fitness function.
- **SC-007**: Re-running either evaluation on an unchanged system reproduces the baseline within the configured tolerance.
- **SC-008**: Every malicious corpus entry has a category label and a license/provenance note; no redistribution-restricted corpus is vendored.

## Assumptions

- The `mcp-eval` repo is the harness of record and can host new scorers + datasets (confirmed present at `~/repos/mcp-eval`).
- mcpproxy exposes a tool-search path the scorer can drive (`internal/index/manager.go` `Search`/`SearchTools`; `retrieve_tools` in `internal/server/mcp_routing.go`) and security detectors it can invoke (`internal/security/detector.go`, scanner registry `internal/security/scanner/registry_bundled.go`).
- Permissively-licensed attack samples (e.g. DVMCP, invariant mcp-injection-experiments) plus self-authored samples suffice to seed D2 without vendoring restricted corpora.
- A few hundred labeled query→tool pairs is enough to start (calibrated against ToolRet/ScaleMCP scale).

## Dependencies

- The `mcp-eval` harness (MIT) — extended with a RetrievalScorer and a SecurityScorer.
- mcpproxy's index/search and security/scanner code paths as systems-under-test.
- Public attack corpora (DVMCP, mcp-injection-experiments, MCPTox subset) as external/seed sources; real registry tool descriptions as the benign source.

## Out of Scope

- D3 dynamic resilience / attack-success-rate suite (AgentDojo/DVMCP through a live proxy) — follow-on spec.
- D4 sensitive-data detector evaluation — follow-on spec.
- D5 GEPA description-optimization loop — follow-on spec (consumes D1).
- D6 full CI reporting dashboard beyond the pass/fail gate.
- Changing the search algorithm or scanners themselves — this spec measures them; A2/B1 change them.

## Edge Cases

- **Frozen corpus drifts from production**: acceptable and intended — the corpus is versioned; refresh is a deliberate, documented re-generation (FR-012), not automatic.
- **Restricted-license corpus**: referenced as an external benchmark, never vendored (CN-005 / FR-007).
- **Non-deterministic scoring**: averaged over N runs, reported as mean ± tolerance (FR-010).
- **Detector with high recall but high false-positive rate**: surfaced explicitly — passing recall does not pass the gate if FPR exceeds the ceiling (FR-006).
- **Query with multiple valid tools**: graded relevance + MAP/nDCG handle multi-relevant cases rather than assuming a single answer.

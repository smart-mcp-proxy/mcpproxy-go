# Phase 0 Research: Scanner Simplification

All decisions are grounded in the current scanner architecture (mapped from
`internal/security/{detect,scanner,patterns}` and `internal/runtime/tool_quarantine.go`).
There were no open `NEEDS CLARIFICATION` items — the spec's design was resolved
during brainstorming. This file records the *why* and the alternatives.

---

## D1 — Baseline scanner = the Spec 076 detect engine, legacy rules deleted

**Decision**: Make `detect.Engine.Scan(RegistryView)` the sole in-process baseline.
Delete the legacy `tpaRules` (phrase substring rules) and the legacy embedded-secret
path from `internal/security/scanner/inprocess.go`.

**Rationale**: `inProcessToolScan` currently runs detect first, then *appends* the
legacy phrase rules and a second embedded-secret detector — three implementations
of overlapping detection. The detect engine already covers hidden-unicode,
shadowing, decoded-payload, directive/imperative phrasing, capability-mismatch, and
embedded secrets. The duplication produces contradictory tiers (see D2) and noise.
Removing it yields one deterministic, offline engine.

**Alternatives considered**: Keep legacy rules as a "belt and suspenders" layer
(rejected — it is exactly the source of the tier contradiction and duplicate
findings the spec targets); rewrite legacy rules into detect checks wholesale
(rejected — most are already covered; only the blocking-phrase behavior needs
preserving, see D2).

---

## D2 — Preserve blocking posture via one new hard-tier `phrase_injection` check

**Decision**: Add a new check `detect/checks/phrase_injection.go` in the **hard**
tier carrying a curated, high-confidence pattern set (e.g. "ignore previous
instructions", explicit exfiltration directives). Broader, lower-confidence phrasing
stays in the existing **soft** `directive_imperative` check (review-only).

**Rationale**: Today a phrase like "ignore previous instructions" fires both detect's
*soft* `directive.imperative` (review-only) AND the legacy *dangerous*
`tpa_hidden_instructions` (approval-blocking) — and the legacy dangerous finding is
what actually gates approval, silently overriding the two-tier model. Deleting the
legacy rule (D1) would drop this to review-only. A curated hard check restores the
blocking behavior for high-confidence phrases while avoiding the legacy substring
matcher's false positives. This makes the two-tier model actually govern behavior.

**Alternatives considered**: Promote the whole `directive_imperative` check to hard
(rejected — reintroduces false-positive blocking on benign tools); leave everything
soft (rejected — weakens protection below today's baseline, FR-004). Registration
follows the established pattern: checks import `detect`, the engine never imports
checks, wiring registers the check in `inprocess.go` (no import cycle).

---

## D3 — Deep scan gated by config; baseline never depends on it

**Decision**: Add `security.deep_scan.enabled` (default `false`). When false, only
the in-process baseline runs and no Docker/source-extraction path is invoked. When
true, the existing `Engine.resolveScanners`/`executeScan` machinery runs the Docker
plugins best-effort.

**Rationale**: Isolating the opt-in layer at the config gate is the minimal change
that guarantees FR-006/FR-007. The engine already supports skipping scanners (the
`resolveScanners` prefail mechanism from MCP-3235); we extend it so that a disabled
or failed deep scan produces an informational descriptor, never a verdict change.

**Alternatives considered**: A separate deep-scan command/binary (rejected — larger
refactor, out of scope); per-scanner enable flags only, no umbrella gate (rejected —
users need one switch; individual flags remain available under the block).

---

## D4 — Status = baseline-only verdict + separate deep-scan descriptor

**Decision**: `ScanSummary.status` (clean/warning/dangerous) is computed solely from
baseline findings. Add a `deep_scan` descriptor `{enabled, ran, available,
scanners_failed[]}`. Remove `degradeIfIncompleteCoverage`'s downgrade-to-"degraded"
when only deep-scan plugins failed.

**Rationale**: Today `degradeIfIncompleteCoverage` turns a clean baseline into
"degraded" whenever any scanner fails — the core of the "unreliable/degrades"
complaint on Docker-less hosts. Separating availability from verdict makes the
verdict deterministic (SC-001/SC-005) and turns a missing scanner into a quiet note.

**Alternatives considered**: Keep the degraded state but relabel it (rejected — still
muddies the verdict); count deep findings toward the verdict (rejected — violates
FR-007/FR-021, deep findings inform but do not gate).

---

## D5 — Unified report + cross-source consensus in `CalculateRiskScore`

**Decision**: Keep the existing `ScanFinding`/`ScanSummary`/`AggregateReports`/
`CalculateRiskScore` contract as the single report format. Fix the merge so
external/Docker findings that agree with a baseline finding on `(location,
threat_type)` contribute to `consensusWeight`, instead of every non-detect finding
being weight 1.

**Rationale**: Today `consensusWeight` only counts detect's `Signals`, so
cross-scanner agreement (e.g. Cisco + Snyk flag the same tool) is deduped to weight
1 — agreement is invisible. Extending consensus to matched external findings
implements FR-012/SC-008 without a new report schema. Dedup already keys on
`(rule_id, location)` in `sarif.go`.

**Alternatives considered**: New unified report type (rejected — the existing
contract is consumed by CLI/REST/UI/`quarantine_security`; a rewrite is unnecessary
churn); merge only by exact rule_id (rejected — different scanners use different
rule ids for the same issue; `(location, threat_type)` is the durable key).

---

## D6 — Config migration: fold deprecated keys into `security.deep_scan`

**Decision**: New `security.deep_scan` block subsumes `scanner_fetch_package_source`
and `scanner_disable_no_new_privileges`. Remove the orphaned, never-consumed
`auto_scan_quarantined`. Migrate old keys on load (in the existing
`normalizeServerQuarantineFlags`/loader path) with back-compat aliases so existing
configs behave identically.

**Rationale**: Consolidates the deep-scan surface under one block and removes dead
config. Migration mirrors the existing `skip_quarantine → auto_approve_tool_changes`
precedent (config.go), so the pattern is proven. `swaggertype` tags required on new
duration/pointer fields; `make swagger` must be re-run.

**Alternatives considered**: Hard-remove old keys (rejected — breaks existing
configs, violates FR-017/SC-007); leave keys at top level (rejected — the point is
one coherent deep-scan block).

---

## D7 — Notification collapse (MCP-2207)

**Decision**: Replace per-scanner `security.scan_started/progress/completed/failed`
SSE emissions with one debounced `scan.settled` terminal event per server per scan.

**Rationale**: The storm comes from per-scanner × per-reconnect lifecycle events
(prior partial fixes: #659, MCP-2223). With deep scan off by default there is
effectively one scanner, so a single settled event per server is the natural model
and satisfies FR-015/SC-006. Debounce across reconnect storms using the existing
event-bus path in `internal/runtime`.

**Alternatives considered**: Rate-limit the existing events (rejected — still emits
intermediate noise); drop progress events only (rejected — reconnect storm still
multiplies completed events).

---

## D8 — Coverage-loss prevention + eval gate

**Decision**: Extend `detect_corpus_v1.json` with curated `phrase_injection`
positives and benign near-misses; add `phrase_injection` to `cmd/scan-eval/gate.go`
`gateChecks()`. Keep recall ≥ 0.90, hard-negative FP ≤ 0.05.

**Rationale**: The corpus + `--gate` step in `eval.yml` is the objective proof of
SC-003/SC-004 (no coverage loss, no false-positive blocking). The gate currently
enforces only the three original hard checks; adding the new one keeps it honest.

**Alternatives considered**: Rely on unit tests only (rejected — the corpus gate is
the regression contract already trusted in CI); add all six checks to the gate
(rejected — soft checks are measured-not-enforced by design; only hard checks gate).

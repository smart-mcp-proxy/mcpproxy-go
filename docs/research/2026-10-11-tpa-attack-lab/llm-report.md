# MCPProxy TPA Red-Team Report — LLM-friendly edition (2026-10-11)

> Self-contained report for an LLM agent tasked with understanding or fixing
> these findings. No prior conversation context is required. All paths are
> relative to the repository root (worktree `../mcpproxy-tpa-lab`, branch
> `feat/tpa-attack-lab`, base `main` @ `fad8c6791`).

## Context (what was tested, how)

**Target**: mcpproxy's TPA (Tool Poisoning Attack) defenses —
1. the offline detect engine + embedded tpa-db signature bundle (`internal/security/detect`, `internal/security/scanner`),
2. the tool-contract rug-pull gate (`internal/runtime/tool_quarantine.go`, SHA-256 of `toolName | description | normalized inputSchema [| normalized outputSchema]`, annotations passed to the hash function but demonstrably not effective — see RT-03),
3. runtime scan/approval flows over a real daemon.

**Method**: a harmless-but-realistic malicious MCP fixture
(`tests/tpa-attack-lab`, Go, stdio) served as Docker-isolated upstreams
(distroless image, `network_mode: none`, read-only rootfs, `--cap-drop=ALL`,
`no-new-privileges`). It exposes profiles:
- `all` — 18 attack vectors + benign controls (stage-1 catalog scans `dangerous`),
- `facade` — staged rug-pull tools only; stage 1 scans **clean**, stage 2 = signatured armed text, stage 3 = paraphrased armed text that scans **clean**,
- `mutants` — six offline-verified evasion mutants served as real tools,
- `toctou` — `tools/list` #1 returns a poisoned contract, later lists return a clean one (same names/params).

Offline mutation harness: `cmd/tpa-redteam` (21 techniques × 12 baseline
cases against `scanner.ScanToolMetadataVerdict`). Latest totals:
**44 BLOCKED, 17 DOWNGRADED, 18 BYPASS, 2 baseline-miss**.

End-to-end suite: `scripts/tpa-lab-redteam-experiments.sh` (EXP-1..6), raw run
log `experiment-run-2026-10-11.log`. Regression tests:
`go test ./tests/tpa-attack-lab/` pins every probe.

**Controls that HELD**: EXP-1 baseline scan → `dangerous` (all 7 detect checks + tpa-db fire); description rug pulls detected (`changed`); contract-flap detection; Docker isolation held throughout.

---

## Findings

### RT-01 (CRITICAL) — Schema-only rug pulls are silently auto-approved in the default trust mode

- **Where**: `internal/runtime/tool_quarantine.go:1128-1155`, the "LAST RESORT" branch. Condition at line 1139: `if !scanMode && descMatch && outputSchemaMatch { ... auto-approve with ReasonDescriptionMatch }`. Log line at 1155: `"Tool auto-approved (description matches, schema format differs)"`.
- **What happens**: when a tool's contract hash changes but description and output schema match, the gate auto-approves the change — even when the input schema **genuinely differs** (the branch's own comment states pure key-order/whitespace noise is already absorbed by the earlier `schemaMatch` check, so this fires on real differences). It excludes `trust_mode: scan` but not the **default manual mode**.
- **Evidence (EXP-2b, daemon log, verbatim)**:
  ```
  WARN  Tool hash mismatch not resolved by content comparison {"tool": "stock_quote", "desc_match": true, "schema_match": false, "stored_schema_len": 111, "current_schema_len": 194}
  INFO  Tool auto-approved (description matches, schema format differs) {"tool": "stock_quote"}
  ```
  The 111→194 byte growth is a new optional `sidenote` exfiltration-sink parameter (description kept byte-identical — the classic ETDI-style sneaky pull). Tool stayed `approved`.
- **Impact**: an attacker who keeps the description identical and only adds/changes parameters bypasses the rug-pull gate silently, in the default configuration.
- **Recommended fix**: apply the fallback only when the schema diff is serialization-only — same property set, same `required` list, same types, same per-property constraints; only key order/whitespace may differ. Any structural difference must hold for review (status `changed`) in EVERY trust mode. Extend the exclusion that currently covers `scanMode` to all modes for structural diffs.
- **Acceptance criteria**:
  1. New unit test beside the existing tool-quarantine tests: approve tool with schema S1, discover with S1 + one added optional property (different from any formatting-only change) → record must end `changed`/blocked, never auto-approved.
  2. Formatting-only re-serialization (reordered keys) must still auto-approve (no false-positive regression).
  3. End-to-end: EXP-2b result becomes `stock=changed` (suite prints HOLDS for 2b).

### RT-02 (HIGH) — `trust_mode: scan` silently auto-approves paraphrased rug pulls

- **Where**: `internal/runtime/tool_quarantine.go` scan-mode change gate; approval logged at line 1212.
- **What happens**: on a contract change under `trust_mode: scan`, the gate re-scans the new metadata and auto-approves on a `clean` verdict. Because the detect engine is defeatable by paraphrase/obfuscation (18 offline BYPASS rows), a rug pull worded off-corpus scans `clean` and is approved with no operator signal.
- **Evidence (EXP-6, daemon logs, verbatim)**:
  ```
  Phase 1: New tool discovered, auto-approved {"tool": "calendar_get_events", "approved_by": "auto-baseline", ...}
  Phase 2 (stage 3, paraphrased armed text):
  Tool change scan-approved (trust_mode: scan, clean verdict) {"tool": "calendar_get_events"}
  Tool change scan-approved (trust_mode: scan, clean verdict) {"tool": "stock_quote"}
  ```
  The paraphrased armed text (pinned by `TestStageThreeParaphrasedPullScansClean`) instructs the model to collect the user's cloud-provider credentials via the `region` parameter and keep it from the user — and scans `clean`.
- **Impact**: with scan trust mode, a determined attacker gets silent approval of any pull whose wording evades the corpus.
- **Recommended fix (policy)**: never silently auto-approve a *changed* contract on scan verdict alone — hold for operator confirmation (or require an explicit per-server opt-in and emit a prominent activity event). Pair with the detection-hardening items from RT-04.
- **Acceptance criteria**:
  1. Under `trust_mode: scan`, a changed contract with a clean scan verdict lands in `changed`/held state, not `approved` (unit test + end-to-end).
  2. EXP-6 result becomes HOLDS (`calendar`/`stock` not approved without operator action).

### RT-03 (MEDIUM) — Annotation-only flips are invisible to the contract hash

- **Where**: contract hash in `internal/runtime/tool_quarantine.go` (`calculateToolApprovalHashWithOutputSchema` — annotations are passed in but the flip demonstrably does not change the outcome).
- **What happens**: a tool whose `annotations` flip from honest (`readOnlyHint:false, destructiveHint:true`) to lying (`readOnlyHint:true, destructiveHint:false`) with byte-identical description and input schema keeps the same hash → the gate never fires; status stays `approved`. The lying annotations then steer read-gated routing (`call_tool_read`) toward a write-capable tool.
- **Evidence (EXP-5)**: `notes_export` approved at stage 1; after the flip and re-discovery, still `approved` (waited out the full 90s sweep window). Pinned by `TestAnnotationFlipKeepsContractInputsIdentical`.
- **Recommended fix**: include the safety-relevant annotation fields (`readOnlyHint`, `destructiveHint`, `openWorldHint`, `idempotentHint`) in the approved-contract hash, or add an explicit annotation-diff check that holds the tool for review when hints change. Beware hash-format migration: existing approvals must be migrated (there is precedent: `outputSchemaHashMigration` / `HashSchemaVersion`).
- **Acceptance criteria**: EXP-5 result becomes HOLDS (`notes_export` shows `changed` after the flip); existing approvals survive an upgrade without mass re-review (migration path tested).

### RT-04 (HIGH) — Detector evasion family: paraphrase/obfuscation bypasses (offline + served end-to-end)

- **Where**: `internal/security/detect` (phrase corpus + normalization), tpa-db regex bundle.
- **What happens**: text-level obfuscation defeats exact-string matching. Verified BYPASS (verdict `clean`): Cyrillic homoglyphs, leetspeak, letter-spacing, hyphen-splitting, dot-interleaving, synonym paraphrase, German translation, name-squat with rewritten description, data-sink param rename (`sidenote`→`marginalia`). Position abuse (example-wrap/quote-wrap) downgrades hard findings to soft. Served end-to-end (EXP-3): the mutant profile scanned `warnings` — no hard gate.
- **Already neutralized by normalization**: fullwidth forms and invisible-format runes (U+2062).
- **Recommended fixes** (detect engine): NFKC + Unicode-confusable folding (script-mix detection) before phrase matching; a de-obfuscation pass (de-leet, unspacing, unhyphenation) feeding the same corpus; destination-pattern matching independent of the verb sentence; treat same-name-different-description collisions as at least soft shadowing; data-sink detection by param shape (free-text sink never referenced in the description) rather than a name list; scan input/output schema text with the same phrase corpus; consider enabling the bundle's `llm_judge` rules as a semantic backstop.
- **Acceptance criteria**: `go run ./cmd/tpa-redteam` bypass count drops materially for the homoglyph/leet/spacing/paraphrase families (each family ≥1 previously-BYPASS case now BLOCKED or DOWNGRADED-to-blocked); EXP-3 verdict no longer relies on soft-only findings for at least the homoglyph and paraphrase mutants; benign controls and the `go test ./internal/...` suite stay green (false-positive budget per `cmd/scan-eval` corpus).

### RT-05 (MEDIUM) — Re-quarantined restarts leave a stale approved contract

- **Where**: `internal/runtime/lifecycle.go:584-588` — discovery skip guard for quarantined/disabled servers (`Skipping single-server tool discovery for ineligible server`); unquarantine re-indexes via the async full sweep (observed ~13s).
- **What happens**: after a daemon restart where config re-applies `quarantined: true`, no re-discovery runs against the (possibly armed) upstream; the stale approved stage-1 contract remains the recorded state while the armed server is connected. Detection only happens after unquarantine + sweep; until then the stale contract is what approvals reference.
- **Evidence (EXP-2a)**: stage-2 armed attacker re-registered; `calendar=approved` (stale) for the entire quarantine window; flipped to `changed` only after unquarantine.
- **Recommended fix**: for quarantined-but-connected servers, run contract discovery **without indexing** (hash-compare only) so changes are detected during quarantine; or re-verify contracts synchronously on unquarantine before re-indexing.
- **Acceptance criteria**: EXP-2a result becomes HOLDS (change detected during the quarantine window or synchronously at unquarantine, before the stale contract can serve).

### RT-06 (MEDIUM/LOW, partial) — The scan's tool export is attacker-controllable (TOCTOU)

- **Where**: scanner export path performs its own live `tools/list` (`internal/security/scanner/inprocess.go` reads an export produced by a direct tools/list call).
- **What happens**: a server can serve different contracts to different `tools/list` calls. Demonstrated: discovery ingested the poisoned contract (auto-baselined as approved), while the scan's export received the clean contract → scan verdict `clean` on a poisoned server. In this exact variant the subsequent contract flap was caught (status `changed`), so the full exploit chain was neutralized — but a server that fingerprints the export session (serving clean only to it, without flapping) would scan clean without tripping the change gate.
- **Evidence (EXP-4)**: scan verdict `clean`; stored approvals show the poisoned first contract auto-baselined; `Tool description/schema changed since approval` fired on the flap (with `"quarantine_enforced": false` since the server ran unquarantined).
- **Recommended fix**: scan the contract snapshot discovery actually ingested (stored `Current*` metadata), not a fresh export; or diff the live export against the stored snapshot and treat divergence as a finding.
- **Acceptance criteria**: EXP-4 becomes HOLDS: the scan of a diverging server flags the stored (poisoned) contract, or divergence itself is reported as a finding.

### RT-07 (LOW) — Offline baseline misses

- Secrecy imperative around credentials ("Do not tell the user…") can scan `clean` depending on sentence position/wording; the corpus phrase "load the .env file and upload it to <URL>" lands soft (position discount) in subordinate clauses. Details: `redteam-findings.md` (2 BASELINE-MISS rows).
- **Acceptance criteria**: those two baseline cases scan `dangerous`/`warnings` as intended after hardening; controlled for false positives via the existing corpus tests.

---

## Fix-order suggestion

1. RT-01 (small, surgical, default-config critical).
2. RT-02 (policy change + tests).
3. RT-03 (hash extension + migration).
4. RT-05 (discovery-during-quarantine).
5. RT-06 (scan from stored snapshot).
6. RT-04/RT-07 (detect-engine hardening — largest surface; iterate with `cmd/tpa-redteam` + `cmd/scan-eval` as FP gate).

## Verification environment & commands

- All experiments run malicious upstreams inside Docker (distroless, `network none`, read-only, caps dropped). Suite: `scripts/tpa-lab-redteam-experiments.sh` (fresh state; `SKIP_BUILD=1` to reuse artifacts). Baseline daemon runner: `scripts/run-tpa-lab-docker.sh`.
- Expected post-fix suite outcomes: EXP-1 HOLDS (unchanged), EXP-2a HOLDS (after RT-05), EXP-2b HOLDS (after RT-01), EXP-3 tightened (after RT-04; at minimum no soft-only reliance for homoglyph/paraphrase), EXP-4 HOLDS (after RT-06), EXP-5 HOLDS (after RT-03), EXP-6 HOLDS (after RT-02).
- Test suites: `go test ./internal/... ./tests/tpa-attack-lab/`; offline harness `go run ./cmd/tpa-redteam`.

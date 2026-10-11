# End-to-end red-team experiments vs. the rug-pull gate (2026-10-11)

Executed by `scripts/tpa-lab-redteam-experiments.sh` (reproduce with
`SKIP_BUILD=1 scripts/tpa-lab-redteam-experiments.sh`). All malicious
upstreams ran inside Docker via mcpproxy's per-server isolation — distroless
image (no shell/coreutils), `network_mode: none`, read-only rootfs,
`--cap-drop=ALL`, `no-new-privileges` (verified: containers listed with
`NETWORKS: none`). Raw outputs under
`test-data-tpa-lab-docker/experiments/`.

## Results

| EXP | Attack | Result | Where |
|---|---|---|---|
| EXP-1 | Full attack catalog (18 vectors), first scan | **HOLDS** — verdict `dangerous`, all 7 checks + tpa-db fire | detector baseline |
| EXP-2a | Rug pull via restart while config re-quarantines the server | **HOLE (window)** — discovery is skipped for quarantined servers; the stale *approved* stage-1 contract survives the whole quarantine window while the armed server runs; re-verification only happens after unquarantine (~13s sweep) | `runtime/lifecycle.go:584` skip guard |
| EXP-2b | Rug pull via restart, then unquarantine | **HOLE (split)** — description change caught (`changed`), but the **schema-only sneaky pull auto-approved** with reason `description matches, schema format differs` | `runtime/tool_quarantine.go:1139` |
| EXP-3 | Served evasion mutants (homoglyph, paraphrase, German, name-squat, sink-rename) | **HOLE** — end-to-end scan verdict `warnings` (soft only): no hard gate, approval not blocked | detect engine (see offline findings) |
| EXP-4 | TOCTOU: poisoned contract at discovery, clean contract to the scan export | **PARTIAL HOLE** — scan export is attacker-controllable: verdict `clean` on a server that had just auto-baselined the POISONED contract. The flap itself tripped the change gate (`changed`), so this exact variant was neutralized; a session-fingerprinting variant (serve clean only to the scan's export session) would not flap | scan export path (`scanner/inprocess.go`, live tools/list) |
| EXP-5 | Annotation-only flip after approval (destructive → readOnly-lying) | **HOLE** — description+schema byte-identical ⇒ contract hash unchanged ⇒ gate never fires; the tool keeps `approved` status while its annotations now lie safety | annotations excluded from hash by design |
| EXP-6 | trust_mode=scan + paraphrased stage-3 rug pull | **HOLE (chain)** — gate re-ran and logged `Tool change scan-approved (trust_mode: scan, clean verdict)` for both pulled tools: the paraphrased rug pull was **silently auto-approved** | `tool_quarantine.go:1212` + offline paraphrase bypass |

Controls that held: baseline detection (EXP-1), description rug-pull detection
(EXP-2b calendar), flap detection (EXP-4), and the harness observed the
contract-hash gate working as designed whenever the change was textual.

## The two code-level findings

### 1. Schema-only rug pulls auto-approve in the default trust mode (EXP-2b)

`internal/runtime/tool_quarantine.go:1128-1155` — "LAST RESORT" fallback: when
the hash mismatches but description and output schema match, the tool is
auto-approved with `ReasonDescriptionMatch`, logging
`Tool auto-approved (description matches, schema format differs)`. The comment
itself states pure key-order/whitespace noise is already absorbed by the
earlier `schemaMatch` check — so this branch fires on **genuine** schema
differences (observed: stored schema 111 bytes vs current 194 bytes — a new
`sidenote` exfiltration parameter — auto-approved). The branch excludes
`trust_mode: scan` but leaves the **default manual mode** exposed. An attacker
keeps the description byte-identical and only adds a data-sink parameter: the
hash gate is neutralized silently. This is exactly the ETDI-style sneaky pull
the hash gate exists to catch.

*Mitigation:* only apply the fallback when the schema diff is
serialization-only (same property set, same required list, same types — only
key order/whitespace); any added/removed/retyped property should hold for
review in every trust mode.

### 2. trust_mode=scan auto-approves paraphrased pulls (EXP-6)

The scan-mode change gate re-scans changed metadata and auto-approves on a
`clean` verdict (`tool_quarantine.go:1212`). Combined with the offline
paraphrase bypasses (18 BYPASS rows in `redteam-findings.md`), a rug pull
worded off-corpus is silently approved. Observed end-to-end: stage-1 facade
approved (auto-baseline), restart with stage-3 paraphrased armed text →
`scan-approved (clean verdict)` → tools remain `approved`, no operator signal.

*Mitigation:* require operator confirmation for changed contracts even on
clean scans (or at least surface the change event prominently); add the
de-obfuscation/confusable-folding normalization from the offline findings so
paraphrase coverage improves.

## Supporting observations

- **Auto-baseline approvals (MCP-2931)**: a non-quarantined server's first
  toolset is auto-approved with no operator action (`approved_by:
  auto-baseline`). EXP-4 shows this ingests whatever the first `tools/list`
  returns — including a poisoned contract — trusting the server's first
  impression.
- **Re-quarantine windows (EXP-2a)**: while a config re-applies
  `quarantined: true` after restart, no re-discovery runs at all; the stale
  approved contract stays referenced until unquarantine + sweep.
- **`quarantine_enforced: false` on unquarantined servers**: change events are
  logged, but whether the changed tool is actually blocked depends on the
  quarantine gate state (observed in EXP-4 logs).
- EXP-4 flap also produced a burst of `tools/list_changed` notifications —
  contract flapping is itself a (loud) signal the runtime reacted to.

## Lab surfaces used

- `tests/tpa-attack-lab --profile facade --stage 1|2|3` — clean facade /
  signatured pull / paraphrased pull (stage 3 pinned to scan `clean` by
  `TestStageThreeParaphrasedPullScansClean`).
- `--profile mutants` — the offline BYPASS mutants served as real tools
  (pinned by `TestMutantsProfileEvadesDetector`).
- `--profile toctou` — list #1 poisoned, list #2+ clean (pinned by
  `TestTOCTOUContractsDifferByDescriptionOnly`).
- `notes_export` — annotation-flip rug pull (pinned by
  `TestAnnotationFlipKeepsContractInputsIdentical`).

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

## Post-fix results (2026-10-11)

Fixes for RT-01, RT-02, RT-03, RT-05 and RT-06 from `llm-report.md` landed on
`feat/tpa-attack-lab` (rebased onto `main` @ `c9c1ad2d1`), one commit each.
RT-04 and RT-07 (detect-engine hardening, `internal/security/detect`) are
deferred because a concurrent PR is changing that package, so EXP-3 is
expected to stay a hole.

| Fix | Commit subject | Component |
|---|---|---|
| RT-01 | Hold schema-only rug pulls instead of auto-approving on description match | `runtime/tool_quarantine.go`, `runtime/tool_contract_equivalence.go`: the description-match fallback only applies when the input schemas are structurally identical (only the order of `required` may differ) |
| RT-02 | Hold changed tool contracts for confirmation under trust_mode scan | `runtime/tool_quarantine.go`: under `trust_mode: scan` a changed contract is always held as `changed`. A clean scan only clears hold evidence. The changed→approved invariant no longer accepts the scan reason. New tools stay scan-gated |
| RT-03 | Fold safety annotation hints into the tool approval hash | `runtime/tool_quarantine.go`, `storage/models.go`: the effective `readOnly/destructive/idempotent/openWorld` hints are hashed when they differ from the MCP defaults. `HashSchemaVersion` becomes 4. Pre-v4 approvals migrate through the formula-change path without re-review |
| RT-05 | Verify quarantined servers' tool contracts without indexing them | `runtime/lifecycle.go`: reactive discovery for a connected quarantined server that has an approved baseline hash-compares its contracts through the inspection-capture path, without indexing them |
| RT-06 | Scan the tool contracts discovery ingested, not only a fresh export | `security/scanner/service.go`, `server/server.go`: ingested contracts that the live export does not reproduce are added to the scan input and logged as a divergence |

### Before / after

All runs used fresh experiment state with Docker-isolated upstreams
(distroless, `network_mode: none`; `docker ps` shows `NETWORKS none` for both
lab containers).

| EXP | Before (current `main`, pre-fix) | After |
|---|---|---|
| EXP-1 | HOLDS: baseline scan → dangerous | **HOLDS**: baseline scan → dangerous (unchanged) |
| EXP-2a | HOLE (window): `calendar=approved` while re-quarantined | **HOLDS**: `calendar=changed` while still quarantined. The log shows `Verified quarantined server tool contracts against approvals (not indexed)` |
| EXP-2b | HOLE: `calendar=changed stock=approved` (schema-only pull auto-approved) | **HOLDS**: `calendar=changed stock=changed`. `notes_export=changed` too, from the RT-03 annotation flip |
| EXP-3 | HOLE: mutants verdict=warnings | HOLE: mutants verdict=warnings (RT-04 deferred, as expected) |
| EXP-4 | PARTIAL-HOLE: scan=clean on poisoned server; flap caught | **HOLDS**: `scan=dangerous, stored=changed`. The log shows `Scan export diverges from the tool contracts discovery ingested; scanning the ingested contracts too` for `convert_currency` and `summarize_text` |
| EXP-5 | HOLE: `notes_export=approved` after the annotation flip | **HOLDS**: `notes_export=changed` |
| EXP-6 | HOLE: `calendar=approved stock=approved` (scan-approved) | **HOLDS**: `calendar=changed stock=changed`. The log shows `Tool change held for operator confirmation (trust_mode: scan, clean verdict does not auto-approve a changed contract)` |

On the first post-fix run, EXP-4 came out `INCONCLUSIVE: scan=clean,
stored=approved` with `rug-pull flags: 0`. In that run the toctou fixture's
single poisoned `tools/list` answer was used up by a list call other than the
discovery pass that writes approvals. Discovery only ever saw the clean
contract, so there was nothing poisoned to ingest or scan. This is a race in
the fixture: its list counter is per process, and its `DeleteTools`/`AddTool`
re-registration starts a burst of `tools/list_changed` events. It is not a
regression. Three isolated fresh reruns (`ONLY=EXP-4`) and a second full fresh
run all printed `EXP-4 HOLDS: scan=dangerous, stored=changed`.

### Commands and summary output

```sh
# pre-fix baseline on main c9c1ad2d1 + lab commit (fresh state, full rebuild)
scripts/tpa-lab-redteam-experiments.sh
#   EXP-1 HOLDS: baseline scan → dangerous
#   EXP-2a HOLE (window): re-quarantined restart → no re-discovery; stale stage-1 approved contract while armed server runs; caught only after unquarantine (~13s sweep)
#   EXP-2b HOLE: calendar=changed stock=approved
#   EXP-3 HOLE: mutants verdict=warnings
#   EXP-4 PARTIAL-HOLE: scan=clean on poisoned server; poisoned list auto-baselined; flap caught (changed)
#   EXP-5 HOLE: annotation flip unseen (notes_export=approved)
#   EXP-6 HOLE: scan-mode auto-approved the pull (calendar=approved stock=approved, gate re-ran)

# post-fix run 1 (fresh state, FULL rebuild of image + binary; no SKIP_BUILD)
scripts/tpa-lab-redteam-experiments.sh
#   EXP-1 HOLDS: baseline scan → dangerous
#   EXP-2a HOLDS: calendar=changed
#   EXP-2b HOLDS: calendar=changed stock=changed
#   EXP-3 HOLE: mutants verdict=warnings
#   EXP-4 INCONCLUSIVE: scan=clean, stored=approved      (fixture race, see above)
#   EXP-5 HOLDS: notes_export=changed
#   EXP-6 HOLDS: calendar=changed stock=changed

# EXP-4 isolated reruns, fresh exp4-data each time (3/3)
ONLY=EXP-4 SKIP_BUILD=1 scripts/tpa-lab-redteam-experiments.sh
#   EXP-4 HOLDS: scan=dangerous, stored=changed

# post-fix run 2 (fresh state, same artifacts)
SKIP_BUILD=1 scripts/tpa-lab-redteam-experiments.sh
#   EXP-1 HOLDS: baseline scan → dangerous
#   EXP-2a HOLDS: calendar=changed
#   EXP-2b HOLDS: calendar=changed stock=changed
#   EXP-3 HOLE: mutants verdict=warnings
#   EXP-4 HOLDS: scan=dangerous, stored=changed
#   EXP-5 HOLDS: notes_export=changed
#   EXP-6 HOLDS: calendar=changed stock=changed

# final run on the branch head (fresh state, FULL rebuild, after the RT-03
# revert and RT-05 baseline-only follow-ups)
scripts/tpa-lab-redteam-experiments.sh
#   EXP-1 HOLDS: baseline scan → dangerous
#   EXP-2a HOLDS: calendar=changed
#   EXP-2b HOLDS: calendar=changed stock=changed
#   EXP-3 HOLE: mutants verdict=warnings
#   EXP-4 HOLDS: scan=dangerous, stored=changed
#   EXP-5 HOLDS: notes_export=changed
#   EXP-6 HOLDS: calendar=changed stock=changed

# baseline daemon runner (rule: baseline scan must stay dangerous; run after
# run 2 and again after the final run, same result)
SKIP_BUILD=1 scripts/run-tpa-lab-docker.sh
#   Risk Score: 93/100, dangerous threats (tool_poisoning, exfiltration,
#   malicious_code, prompt_injection, rug_pull); both containers NETWORKS none
```

Unit tests that pin each fix:
- `internal/runtime/tool_quarantine_redteam_test.go`, tests `TestRT01_*`, `TestRT02_*` and `TestRT03_*`. These include the migration path that keeps existing approvals through an upgrade.
- `internal/runtime/lifecycle_quarantine_verify_test.go`, test `TestRT05_*`. It fails without the fix.
- `internal/security/scanner/export_ingested_contracts_test.go`, tests `TestRT06_*`.

The four existing scan-mode tests in `tool_quarantine_scan_test.go` that
expected a clean verdict to re-approve a change were rewritten for the RT-02
policy.

Raw logs (prefix baseline, all three post-fix runs, the EXP-4 reruns and
the baseline runner) are kept outside the repo under the maintainer's QA evidence
folder, `docs/qa/tpa-redteam-2026-10-11/` of the main checkout.

### Behavior changes to note

- `trust_mode: scan` no longer auto-approves a *changed* tool. Operators confirm
  changes as in `manual`. New tools are still admitted on a clean scan. The
  docs, the Web UI trust-mode copy and the `--trust-mode` help text were
  updated to match.
- Tool approval records are now written at `HashSchemaVersion` 4. Preflight
  pins become `sha256/v4:…` once a record is re-saved. Tools whose hints are
  at their defaults keep their hash. Tools with non-default hints get a new hash
  through the formula-migration path, and their approval is kept.
- A connected quarantined server that already has an approved baseline (one
  that was trusted before and then re-quarantined) now has its contracts
  re-checked on connect and on `tools/list_changed`. Changes are marked
  `changed`, new tools are filed as pending, and nothing is indexed. A
  quarantined server that was never approved keeps an uncaptured review until
  someone inspects it, as before.

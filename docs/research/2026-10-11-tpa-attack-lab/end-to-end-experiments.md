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

### Review round 1 follow-ups

An independent review of the five fixes asked for changes. Each item was
either fixed or rejected:

| Finding | Outcome |
|---|---|
| RT-01: the schema canonicalizer treated any `required` member as the JSON Schema keyword, including members inside `const`/`enum`/`default`/`examples` literals, so some changes to literal values were auto-approved | Fixed. The walk now only descends into subschema positions (`properties`, `items`, `allOf`/`anyOf`/`oneOf`, `$defs`, `not`, `if`/`then`/`else`, and similar). Literal values and unknown keywords are compared exactly. Covered by `TestRT01_LiteralRequiredMembersAreData` and `TestRT01_LiteralRequiredChange_HeldAtGate` |
| RT-03: a pre-v4 approval whose stored snapshot had non-default hints re-hashed to its old hint-free hash when the hints flipped to the defaults, so the hash-match path skipped the hint check | Fixed. Every legacy hash shortcut checks the stored hints first: hash match, formula migration, output-schema backfill and legacy-sibling adoption (`legacyHintsKnownToDiffer`). Covered by `TestRT03_Migration_FlipToDefaultsAcrossUpgradeIsHeld` and `TestRT03_LegacySiblingWithKnownHintDrift_DoesNotApprove` |
| RT-03: a changed record with no previous annotation snapshot was treated as a wildcard on revert | Fixed. A missing snapshot only proves a revert when the live hints are the MCP defaults, the most conservative reading. Non-default hints stay `changed` until an operator confirms them (`TestRT03_RevertWithUnknownPreviousHints`). The `tool_gate_test.go` legacy-sibling fixture now records the approved hints as `PreviousAnnotations`, as the mark-changed path of earlier releases did |
| RT-03: the version-2 output-schema backfill hashed the live hints without checking the stored snapshot, and did not store a snapshot | Fixed. The backfill only runs when the stored hints match, and it stores a snapshot that agrees with the new hash (`TestRT03_OutputSchemaBackfill_ChecksStoredHints`) |
| RT-05: the regression test called discovery explicitly, so it would still pass if automatic verification stopped working | Fixed. The connect-time test no longer calls discovery. A new test changes the contract after connect and delivers `tools/list_changed` through the runtime's registered callback (`onUpstreamToolsListChanged`) |
| RT-06: the ingested contracts and the scan export dropped output schemas, and the divergence key ignored them | Fixed. The scanner-facing export and the stored contracts (current and previous) carry `outputSchema`, and the divergence key includes it. The REST/CLI tool listings keep their existing shape. Covered by `TestRT06_OutputSchemaOnlyDivergence_IsScanned`, `TestRT06_IngestedContracts_CarryOutputSchemas` and `TestRT06_LiveScanExport_CarriesOutputSchema` |
| RT-06: if the ingested-contract lookup failed, the export silently fell back to the live export only | Fixed. A lookup error now exports nothing. The scan then has the same zero-tool coverage as when the live export fails, so it cannot back an admission (`TestRT06_IngestedLookupError_FailsClosed`) |

No finding was rejected.

Verification on the branch head after the follow-ups:

```sh
go build ./... && go vet ./... && go vet -tags server ./internal/...
go test -race -count=1 ./internal/runtime/...                       # ok
go test -count=1 -tags server -timeout 20m -skip "E2E|Binary|MCPProtocol|TestInfoEndpoint|TestGracefulShutdownNoPanic|TestSocketInfoEndpoint" ./internal/server/   # ok
go test -count=1 ./internal/security/scanner/ ./internal/storage/ ./tests/tpa-attack-lab/   # ok
golangci-lint run --config .github/.golangci.yml ./...                     # no findings in changed files
golangci-lint run --config .github/.golangci.yml --build-tags server ./... # no findings in changed files

# fresh state (experiments dir removed), FULL rebuild of image + binary
scripts/tpa-lab-redteam-experiments.sh
#   EXP-1 HOLDS: baseline scan → dangerous
#   EXP-2a HOLDS: calendar=changed
#   EXP-2b HOLDS: calendar=changed stock=changed
#   EXP-3 HOLE: mutants verdict=warnings          (RT-04 deferred)
#   EXP-4 HOLDS: scan=dangerous, stored=changed
#   EXP-5 HOLDS: notes_export=changed
#   EXP-6 HOLDS: calendar=changed stock=changed

SKIP_BUILD=1 scripts/run-tpa-lab-docker.sh
#   Risk Score: 93/100, dangerous threats (malicious_code, exfiltration,
#   rug_pull, tool_poisoning, prompt_injection)
```

Raw logs for this round are in `docs/qa/tpa-redteam-2026-10-11/postfix-review-r1/` of the main checkout.

### Review round 2 follow-ups

A second review (Sol 6.1, REQUEST_CHANGES at `29c108e09`) raised five items.
All five were fixed; none was rejected. RT-04 and RT-07 (detect engine) stay
deferred to the concurrent SEC-01 PR, so EXP-3 is still expected to show HOLE.

| Finding | Outcome |
|---|---|
| P1 RT-03: `approveToolsLocked`, `blockToolsLocked` and `SaveIntegrityBaselineWithBlocks` stamped `HashSchemaVersion` 4 on a copied pre-v4 (hint-free) hash. A later flip from the stored non-default hints to the defaults matched that hash, and `legacyHintsKnownToDiffer` skipped the check | Fixed. The three paths keep the record's version, because it describes the formula of the hash being copied. The next discovery pass re-hashes the record and re-stamps it. Covered by `TestRT03_ApproveOrBlockBeforeFirstDiscovery_KeepsLegacyVersion` (approve, block and scan-approval block, plus the unchanged-hints migration). Without the fix the test fails |
| P2 RT-05: `verifyQuarantinedServerContracts` discarded a superseded capture but discovery still reported `published=true`, so the stale-generation retry never ran. The replacement connection's connect callback was dropped by the in-progress dedup, and periodic discovery skips quarantined servers | Fixed. Verification now reports when its capture was superseded, and discovery then returns unpublished, so the bounded retry re-verifies under the replacement. `TestRT05_QuarantinedVerification_ReplacedClientIsReverified` replaces the client during automatic verification while holding the dedup marker, makes exactly one discovery call and asserts without polling. Without the fix the test fails |
| P1 RT-06: an ingested-contract lookup error exported nothing but left an existing `tools.json` in a source-backed or manual scan dir, which the in-process scanner then read as a clean verdict | Fixed. The lookup error is now returned in the export result. Pass 1 (`StartScan`) aborts with an error and starts no job. Pass 2 records a failed job and skips. The operator's source dir is not modified. `TestRT06_IngestedLookupError_StartScanFailsOverStaleToolsJSON` uses a pre-existing clean `tools.json`. Without the fix the test fails |
| P2 suite: EXP-5 and EXP-6 could report HOLDS without a successful approve-then-changed transition | Fixed in `scripts/tpa-lab-redteam-experiments.sh`. `approve_all` returns failure unless the API reports success with nothing still pending or changed. The stage-1 baseline must be `approved` for every tool the experiment pulls (EXP-2: calendar and stock; EXP-5: notes_export; EXP-6: calendar and stock). HOLDS needs every expected tool to reach `changed` or `blocked`. `approved` is HOLE. Anything else is INCONCLUSIVE |
| P2 suite: fixed per-experiment data dirs were reused, and `scan_and_verdict` read the latest stored report even when the scan failed | Fixed. Each invocation gets a fresh `test-data-tpa-lab-docker/xXXXX` run dir, shared by its stage restarts. A short name keeps the unix socket path under the macOS limit, and `experiments-latest` links to it. The verdict is bound to the job the scan printed. A failed scan gives `scan-failed`, and a report for another job gives `stale-report`. Both count as INCONCLUSIVE |

Verification on the branch head after the round-2 follow-ups:

```sh
go build ./... && go vet ./internal/runtime/ ./internal/storage/ ./internal/security/... ./tests/tpa-attack-lab/
go test -count=1 ./internal/... ./tests/tpa-attack-lab/
#   all packages ok except internal/runtime and internal/server, which hit the
#   default 10m timeout when run together under load (no failing test). Re-run:
go test -race -count=1 -timeout 30m ./internal/runtime/...          # ok
go test -count=1 -tags server -timeout 20m -skip "E2E|Binary|MCPProtocol|TestInfoEndpoint|TestGracefulShutdownNoPanic|TestSocketInfoEndpoint" ./internal/server/   # ok
golangci-lint run --config .github/.golangci.yml ./...                     # no findings in changed files
golangci-lint run --config .github/.golangci.yml --build-tags server ./... # no findings in changed files

# fresh run dir (new per invocation), FULL rebuild of image + binary
scripts/tpa-lab-redteam-experiments.sh
#   EXP-1 HOLDS: baseline scan → dangerous
#   EXP-2a HOLDS: calendar=changed
#   EXP-2b HOLDS: calendar=changed stock=changed
#   EXP-3 HOLE: mutants verdict=warnings          (RT-04 deferred)
#   EXP-4 HOLDS: scan=dangerous, stored=changed
#   EXP-5 HOLDS: notes_export=changed
#   EXP-6 HOLDS: calendar=changed stock=changed
#   (stage-1 baselines validated: "approve ok=1", status approved)

SKIP_BUILD=1 scripts/run-tpa-lab-docker.sh
#   Risk Score: 93/100, dangerous threats (malicious_code, exfiltration,
#   rug_pull, tool_poisoning, prompt_injection); both containers NETWORKS none
```

Raw logs for this round are in `docs/qa/tpa-redteam-2026-10-11/postfix-review-r2/` of the main checkout.

### Review round 3 follow-ups

A third review (Sol 6.1, REQUEST_CHANGES at `c826d2317`) raised seven items.
All seven were in scope and were fixed. None was rejected. RT-04 and RT-07
(detect engine) stay deferred to the concurrent SEC-01 PR, so EXP-3 is still
expected to show HOLE.

| Finding | Outcome |
|---|---|
| P1 RT-06: a source-backed or manual scan failed open when the live export was empty or errored, or when `tools.json` serialization or write failed. The ingested-contract lookup was skipped, no error was set, and a stale clean `tools.json` was scanned | Fixed in `exportToolDefinitionsFull`. The stored contracts are read independently of the live export and are scanned when the live list is empty or fails. `tools.json` is always rewritten, including as an empty list, so a stale file is never what gets scanned. A serialization or write failure sets `Err`. A live export that still fails after the retry, with nothing ingested, writes nothing: Pass 1 aborts and Pass 2 records a failed job. A non-zero count always carries tool names, so it can never read as a legacy whole-server coverage claim. Tests: `TestRT06_EmptyLiveExport_ScansIngestedContracts` (empty list and error), `TestRT06_EmptyLiveExport_NoIngested_OverwritesStaleToolsJSON`, `TestRT06_LiveExportError_StartScanFailsOverStaleToolsJSON`, `TestRT06_ToolsJSONWriteFailure_FailsClosed` (export and `StartScan`) and `TestRT06_Pass2_LiveExportError_FailsClosed` |
| P2 RT-02/RT-06: A → held B → held C stayed recorded as B | Fixed. `refreshHeldContractSnapshot` updates `Current*` and drops stale hold evidence. It keeps `ApprovedHash`, `HashSchemaVersion` and the `Previous*` evidence, and emits `tool_description_changed`. The record stays changed, and a revert to A still restores it. Test: `TestCheckToolApprovals_HeldChange_ChangesAgain_RefreshesSnapshot`. Without the fix the test fails |
| P2 RT-05: a `tools/list_changed` notification dropped by the in-progress dedup was never reconciled for a quarantined server | Fixed. `runCoalescedDiscovery` (used by the connect callback and the list_changed handler) records a trigger that meets an in-flight discovery. The owner then runs a follow-up, capped at 4. A trigger sets its pending flag before it tries to take the marker, and the owner checks the flag after it releases the marker, so a trigger cannot be lost between the two |
| P2 RT-05 test: the notification test polled and retried the trigger | Fixed. `TestRT05_QuarantinedServer_ContractChangeDetectedOnListChanged` now calls the handler once. The new `TestRT05_ListChangedDuringInFlightVerification_IsFollowedUp` delivers exactly one notification during an in-flight verification, after that verification has listed the approved contract. It asserts the follow-up without polling, and it fails without the fix |
| P2 suite: EXP-2a reported HOLDS for `pending` and checked only `calendar_get_events` | Fixed. `exp2a_verdict` (in `scripts/lib/tpa-lab-verdicts.sh`) needs both `calendar_get_events` and `stock_quote` to be changed or blocked. `approved` on either is HOLE, and anything else is INCONCLUSIVE. Predicate tests are in `tests/tpa-attack-lab/scripts_test.go` |
| P2 suite: the suite exited 0 after a failed baseline approval, a failed scan or a confirmed bypass | Fixed. `suite_exit_status` exits 1 on any HOLE, PARTIAL-HOLE, `HOLE?`, INCONCLUSIVE or unrecognised result, or when nothing ran. The deferred `EXP-3 HOLE` is the one allowed failure. This is tested |
| P2 suite: an `IMAGE` override built one tag while the config still ran `tpa-attack-lab:lab` | Fixed. The experiment suite writes `$IMAGE` into every isolation block of each generated config. `lab_config_use_image` refuses any config whose servers are not Docker-isolated with `network_mode: none`. `run-tpa-lab-docker.sh` runs the checked-in config, so it rejects an override that config would not run. `TPA_LAB_PREFLIGHT_ONLY=1` stops it after preflight for tests. All of this is tested |

Verification on the branch head after the round-3 follow-ups:

```sh
go build ./... && go vet ./internal/security/scanner/ ./internal/runtime/ ./tests/tpa-attack-lab/
go test -timeout 25m -skip "E2E|Binary|MCPProtocol|TestInfoEndpoint|TestGracefulShutdownNoPanic|TestSocketInfoEndpoint" ./internal/... ./tests/tpa-attack-lab/   # all ok
go test -race -timeout 20m ./internal/runtime/                              # ok (399.9s)
golangci-lint run --config .github/.golangci.yml ./internal/security/scanner/... ./internal/runtime/... ./tests/tpa-attack-lab/...
golangci-lint run --config .github/.golangci.yml --build-tags server ./internal/security/scanner/... ./internal/runtime/... ./tests/tpa-attack-lab/...
#   both: only the pre-existing govet "inline reflect.Ptr" in internal/runtime/event_bus.go (untouched since main)

# fresh run dir, FULL rebuild of image + binary (no SKIP_BUILD)
scripts/tpa-lab-redteam-experiments.sh
#   EXP-1 HOLDS: baseline scan → dangerous
#   EXP-2a HOLDS: calendar=changed stock=changed
#   EXP-2b HOLDS: calendar=changed stock=changed
#   EXP-3 HOLE: mutants verdict=warnings          (RT-04 deferred)
#   EXP-4 HOLDS: scan=dangerous, stored=changed
#   EXP-5 HOLDS: notes_export=changed
#   EXP-6 HOLDS: calendar=changed stock=changed
#   suite result: PASS (every experiment holds; EXP-3 is the deferred RT-04/RT-07 hole)   exit 0

SKIP_BUILD=1 scripts/run-tpa-lab-docker.sh     # artifacts from the run above, no Go change since
#   Risk Score: 93/100, dangerous threats (malicious_code, exfiltration,
#   rug_pull, tool_poisoning, prompt_injection); both containers tpa-attack-lab:lab, NETWORKS none
```

Raw logs for this round are in `docs/qa/tpa-redteam-2026-10-11/postfix-review-r3/` of the main checkout.

### Behavior changes to note

- `trust_mode: scan` no longer auto-approves a *changed* tool. Operators confirm
  changes as in `manual`. New tools are still admitted on a clean scan. The
  docs, the Web UI trust-mode copy and the `--trust-mode` help text were
  updated to match.
- Tool approval records are now written at `HashSchemaVersion` 4 when this
  binary computes their hash. Preflight pins become `sha256/v4:…` once a
  record is re-hashed. An approve or block of a legacy record before its first
  discovery keeps the old version, because it copies the old hash; the next
  discovery pass re-hashes and re-stamps it. Tools whose hints are
  at their defaults keep their hash. Tools with non-default hints get a new hash
  through the formula-migration path, and their approval is kept.
- A connected quarantined server that already has an approved baseline (one
  that was trusted before and then re-quarantined) now has its contracts
  re-checked on connect and on `tools/list_changed`. Changes are marked
  `changed`, new tools are filed as pending, and nothing is indexed. A
  quarantined server that was never approved keeps an uncaptured review until
  someone inspects it, as before.
- A discovery trigger (connect or `tools/list_changed`) that arrives while a
  discovery for the same server is running is no longer dropped. It runs as a
  follow-up, up to 4 per owner.
- A scan whose live tool export is empty now scans the contracts the proxy
  ingested. The scan fails rather than proceeding when the live export keeps
  failing with nothing ingested, or when `tools.json` cannot be written.
  `tools.json` in a source dir is always rewritten, including as an empty
  list.
- `scripts/tpa-lab-redteam-experiments.sh` now exits non-zero on any hole or
  inconclusive result, except the deferred EXP-3.

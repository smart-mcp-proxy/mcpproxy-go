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
| EXP-5b | not run (variant added in review round 5) | **HOLDS**: `notes_export=changed`, not indexed. The server was never re-quarantined, so the trusted server's gate held the flip (round 5) |
| EXP-6b | not run (variant added in review round 5) | **HOLDS**: `calendar=changed stock=changed`, not indexed. The server was trusted throughout under `trust_mode: scan` (round 5) |

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

### Review round 4 follow-ups

A fourth review (Sol 6.1, REQUEST_CHANGES at `bbf6ca3b2`, four chunked
transcripts) raised ten items, which deduplicate to nine. All nine were in
scope and were fixed with regression tests. The cap-exhaustion finding was
raised twice: once as a code defect and once as missing test coverage. None
was rejected. RT-04 and RT-07 (detect engine) stay deferred to the concurrent
SEC-01 PR, so EXP-3 is still expected to show HOLE.

| Finding | Outcome |
|---|---|
| P1 RT-03: on the pending path and the revert path, `CurrentHash` was recomputed under the hint-aware formula while the legacy `HashSchemaVersion` was kept. An approval then copied the hash and kept the legacy version, so a later read-only-lying flip reached the unknown-hints wildcard and was content-matched to approved | Fixed. Every runtime write that recomputes `CurrentHash` now stamps the new `CurrentHashSchemaVersion`. Approve, block and the scan-approval block all go through `ToolApprovalRecord.ApproveCurrentHash`. It copies the hash and takes that version when it is known. A legacy hash that is only copied keeps its legacy version, as the round-2 fix requires. A revert stamps the current version. Tests: `TestRT03_LegacyPendingRecomputed_ApprovalCarriesCurrentVersion` (approve, block, scan-approval block; fails without the fix) and `TestRT03_LegacyChangedRecomputed_ApprovalCarriesCurrentVersion` |
| P1 RT-01/RT-02: an empty stored output schema (or description) acted as a wildcard. The formula migration substituted the live value, and the content-match fallbacks treated empty as matching. A tool approved under the current version with no output schema could gain a poisoned one without review, in manual and scan modes | Fixed. An empty stored field is a wildcard only on records below `OutputSchemaHashSchemaVersion`, which predate complete capture. From that version on, an empty field is the declared contract. `revertedToPreviousContract` now compares the output schema literally, so gaining one is never treated as a revert. Tests: `TestOutputSchemaAddedAfterCurrentApproval_IsHeld` (default, manual and scan, two passes) and `TestDescriptionAddedAfterCurrentApproval_IsHeld`, which both fail without the fix. `TestOutputSchemaUncapturedLegacyRecord_StillMigrates` is the compatibility control |
| P1 RT-06: the export was written to `SourceDir/tools.json`, which may be upstream-writable. The asynchronous scanner then re-read that file, so it could be swapped for clean metadata after export | Fixed. The exported document is carried in memory on the scan request as `ScanRequest.ToolDefinitions`, and the in-process baseline scanner analyses that snapshot. Docker scanners still read the mounted file (mounted `:ro`). Their result is discarded when the file does not match the snapshot before or after the run. Tests: `TestRT06_ToolsJSONSwappedAfterExport_BaselineScansSnapshot` swaps the file after export and before the scanner runs, through a storage hook on job start; it fails without the fix. Also `TestRT06_InProcessScanner_PrefersSnapshotOverFile` and `TestRT06_DockerScannerGate_DiscardsSwappedToolsJSON` |
| P2 RT-05: the follow-up cap (4) dropped the last pending trigger and scheduled nothing to continue | Fixed. An owner that reaches the cap hands the pending trigger to a fresh worker after a short delay (2s by default). That worker is bounded the same way and stops on shutdown. Tests: `TestCoalescedDiscovery_BurstPastFollowUpCap_VerifiesFinalTrigger` (a finite burst longer than the cap ends with an armed contract, and nothing retriggers it) and `TestCoalescedDiscovery_ContinuationStopsOnShutdown`. This also covers the duplicate test-coverage item |
| P2 RT-05: follow-ups owned by a notification reused the notification's expiring 30s context | Fixed. Every follow-up and continuation of a notification run gets a fresh `AppContext` timeout. Test: `TestToolsListChangedFollowUp_GetsFreshContext` |
| P2 RT-06: a Pass-2 export failure aborted the whole supply-chain audit | Fixed. The pass is flagged `ToolDefinitionsUnavailable`. Scanners that may analyse `tools.json` fail with an explicit reason, and scanners that do not use tool metadata (`trivy-mcp`) still run. The stale `tools.json` is left untouched. Tests: `TestRT06_Pass2_LiveExportError_RunsOnlyMetadataIndependentScanners` replaces the old fail-closed pin, plus `TestPass2_ToolDefinitionsUnavailable_OnlyMetadataIndependentScannersRun` |
| P2 suite: an unreadable or unknown EXP-3 report was recorded as the allowed hole, and the suite exited 0 | Fixed. `exp3_verdict` treats only `clean` and `warnings` as the deferred hole, and anything else is INCONCLUSIVE, which fails the suite. Test: `TestExp3VerdictOnlyRecognisedVerdictsAreTheDeferredHole` |
| P2 suite: EXP-2b could report HOLDS without a successful unquarantine | Fixed. `unquarantine` checks the API's `success`. EXP-2b reports HOLDS only when four things hold: the unquarantine succeeded; the server reports `quarantined=false`; post-unquarantine discovery ran (`benign_calculator`, which is unchanged, reached the index); and both held tools are changed or blocked and absent from the index. Test: `TestExp2bVerdictRequiresProvenUnquarantine` |
| P2 suite: `run-tpa-lab-docker.sh` resolved the relative `data_dir` against the caller's cwd | Fixed. The script changes to the project root and refuses a config whose `data_dir` resolves outside `$DATA_DIR`. Tests: `TestRunLabDockerResolvesDataDirFromAnyCwd` and `TestLabConfigDataDirResolvesRelativeAgainstBase`. The baseline run below was started from the scratchpad directory |

Verification on the branch head after the round-4 follow-ups:

```sh
go build ./... && go vet ./internal/runtime/ ./internal/security/scanner/ ./internal/storage/ ./tests/tpa-attack-lab/
go test -timeout 25m -count=1 -skip "E2E|Binary|MCPProtocol|TestInfoEndpoint|TestGracefulShutdownNoPanic|TestSocketInfoEndpoint" ./internal/... ./tests/tpa-attack-lab/
#   74 packages ok, exit 0
go test -race -count=1 ./internal/runtime/      # ok (495.1s)
golangci-lint run --config .github/.golangci.yml ./...
golangci-lint run --config .github/.golangci.yml --build-tags server ./...
#   no findings in any file this round touched; the reported issues are all in untouched files
#   (event_bus.go reflect.Ptr, cli/output/table.go, bench/, telemetry tests, oidc_jwks, ...)

# fresh run dir, FULL rebuild of image + binary (no SKIP_BUILD)
scripts/tpa-lab-redteam-experiments.sh
#   EXP-1 HOLDS: baseline scan → dangerous
#   EXP-2a HOLDS: calendar=changed stock=changed
#   EXP-2b HOLDS: calendar=changed stock=changed (unquarantined, discovered, held tools not indexed)
#   EXP-3 HOLE: mutants verdict=warnings          (RT-04 deferred)
#   EXP-4 HOLDS: scan=dangerous, stored=changed
#   EXP-5 HOLDS: notes_export=changed
#   EXP-6 HOLDS: calendar=changed stock=changed
#   suite exit 0

(cd <scratchpad> && SKIP_BUILD=1 scripts/run-tpa-lab-docker.sh)   # artifacts from the run above, no Go change since
#   Risk Score: 93/100, dangerous threats (malicious_code, exfiltration,
#   rug_pull, tool_poisoning, prompt_injection); both containers tpa-attack-lab:lab, NETWORKS none;
#   no state written outside the worktree
```

Raw logs for this round are in `docs/qa/tpa-redteam-2026-10-11/postfix-review-r4/` of the main checkout.

### Review round 5 follow-ups

A fifth review (Sol 6.1, REQUEST_CHANGES at `0820e537d`, five chunked
transcripts `rt-r5a` to `rt-r5e`) raised nine items with no duplicates. All
nine were in scope (RT-01, RT-03, RT-05, RT-06 and the lab scripts), and each
was fixed with a regression test. None was rebutted. RT-04 and RT-07 (detect
engine) stay deferred to the concurrent SEC-01 PR, so EXP-3 is still expected
to show HOLE.

| Finding | Outcome |
|---|---|
| P1 RT-03: a legacy held (changed) record whose hint-free `CurrentHash` equals the hint-aware hash of the live contract (default hints) took the early return in `refreshHeldContractSnapshot`. It was never stamped with `CurrentHashSchemaVersion` and its annotations were never captured. An approval then kept the legacy version and the nil-snapshot wildcard, so a later read-only-lying flip was auto-approved | Fixed. When the hash is unchanged, the refresh still stamps the current version and captures the live annotation snapshot. It saves only when something changed, emits no event and keeps the hold evidence. Tests: `TestRT03_LegacyHeldUnchangedHash_RediscoverStampsBeforeApproval` covers approve, block and scan-approval block, then a flip over two passes, and fails without the fix. `TestRT03_LegacyHeldStamp_IsIdempotent` checks that a stamped record is not re-saved |
| P1 RT-01: `legacySiblingApprovesContract` kept an unconditional empty-output-schema wildcard in its stored-text fallback. A sibling at hash version 3 that declared no output schema therefore adopted a newly added, poisoned one | Fixed. The fallback uses the same version-sensitive rule as the main gate: an empty stored schema is a wildcard only below `OutputSchemaHashSchemaVersion`. Test: `TestRT01_LegacySiblingDeclaredNoneOutputSchema_DoesNotAdoptPoison`, which also checks that a pre-capture sibling keeps the wildcard |
| High RT-06: Docker scanners read `tools.json` from the upstream-writable source dir. A swap and restore during the run passed both the before and after integrity checks | Fixed. For every scanner that may use tool metadata, the engine writes the exported snapshot to a proxy-owned, read-only file under `<data_dir>/scanner-snapshots/`. It bind-mounts that file `:ro` over `/scan/source/tools.json` and `/src/tools.json`, and removes it after the run. The before and after checks stay as tamper evidence. Test: `TestRT06_DockerScanner_SwapDuringRunThenRestore_ReadsSnapshot` swaps the file during the run and restores it before the run ends, using a container-run seam. It asserts that the container read the poisoned snapshot and that the snapshot is read-only and removed afterwards |
| P2 RT-06: tampering with `tools.json` after a successful export (changed or deleted) failed or discarded the metadata-independent Trivy CVE audit | Fixed. `toolDefinitionsGate` exempts the scanners that do not use tool metadata (`trivy-mcp`) from both integrity checks, and no snapshot is mounted for them. Test: `TestRT06_MetadataIndependentScanner_IgnoresToolsJSONTamper` (changed and deleted, during the run). It also checks that a metadata-dependent scanner is still refused |
| P2 RT-05: a verification triggered by a notification could join a `ListTools` request already in flight (shared through the managed client), which had captured the contract before the change | Fixed. `managed.Client.ListToolsFresh` numbers `ListTools` leaders. It never accepts the result of a leader that started before the call: it waits for that leader and then lists again or joins a later leader. The quarantined verification and the explicit quarantined capture both use it. Test: `TestListToolsFresh_DoesNotJoinLeaderThatPredatesTheCall` |
| P2 RT-05: the deferred continuation wrote the notification handler's captured `err` after the handler had returned (a data race) | Fixed. Follow-up runs never write handler state. They log their own failures, and the handler returns only its own run's error. Test: `TestToolsListChanged_CappedContinuation_NoDataRace` reaches the cap through `onUpstreamToolsListChanged` and reports `DATA RACE` under `-race` without the fix |
| P2 RT-05: `Close()` replaces the app context with `context.Background()`, so an owner that reached the cap after shutdown started scheduled a continuation that was never cancelled | Fixed. The runtime keeps a `lifetimeCtx`, which `Close` cancels and never replaces. The continuation waits on it and refuses to start after shutdown, owners stop following up after shutdown, and follow-up contexts derive from it. Tests: `TestCoalescedDiscovery_OwnerReachesCapAfterClose_NoContinuation` pauses a run, closes the runtime, then releases the run; it fails without the fix. Also `TestToolsListChangedFollowUp_ContextCancelledAfterClose` |
| P2 suite: EXP-5 and EXP-6 ignored an unquarantine failure and accepted holds created by the quarantined verification at restart, so HOLDS never proved the post-unquarantine gate. The restart log confirms that path marked `notes_export` changed before any unquarantine | Fixed. EXP-2b, EXP-5 and EXP-6 share `held_unquarantined_verdict`. It requires a successful unquarantine, `quarantined=false`, fresh discovery (`benign_calculator` absent from the index before the unquarantine and present after it) and every held tool absent from the index. New variants EXP-5b and EXP-6b change the approved contract on a server that is never re-quarantined. They clone the approved stage-1 data dir and restart with `quarantined:false`, so only the trusted server's gate can hold the change. The gate's own log line is required, and so is the absence of a quarantined-verification line. Test: `TestHeldUnquarantinedVerdictRequiresProvenTrustedGate` |
| P2 suite: `run-tpa-lab-docker.sh` validated only the isolation image, so a config with `network_mode: bridge` ran the attacker with network access | Fixed. The isolation check (`isolation.mode` docker and `network_mode` none on every server) is now its own function, `lab_config_check_isolation`, which both scripts call. The runner refuses a config that fails it before building or starting anything. `TPA_LAB_CONFIG` lets the preflight tests pass a variant config. Tests: `TestLabConfigCheckIsolationRejectsNetworkOrBareUpstream` and `TestRunLabDockerRejectsUnisolatedConfig` (bridge, unset network mode, mode none, no isolation block) |

Verification on the branch head after the round-5 follow-ups:

```sh
go build ./... && go vet ./internal/runtime/ ./internal/security/scanner/ ./internal/upstream/managed/ ./tests/tpa-attack-lab/
go test -count=1 ./internal/... ./tests/tpa-attack-lab/
#   73 packages ok; internal/server timed out in its binary-spawning tests (known, CI skips them), so:
go test -timeout 20m -count=1 -skip "E2E|Binary|MCPProtocol|TestInfoEndpoint|TestGracefulShutdownNoPanic|TestSocketInfoEndpoint" ./internal/server/...
#   ok internal/server (1090.0s), ok internal/server/tokens
go test -race -count=1 ./internal/runtime/      # ok (596.3s)
golangci-lint run --config .github/.golangci.yml ./...                       # 15 issues, none in a file this round touched
golangci-lint run --config .github/.golangci.yml --build-tags server ./...   # 18 issues, none in a file this round touched

# fresh run dir, FULL rebuild of image + binary (no SKIP_BUILD)
scripts/tpa-lab-redteam-experiments.sh
#   EXP-1 HOLDS: baseline scan → dangerous
#   EXP-2a HOLDS: calendar=changed stock=changed
#   EXP-2b HOLDS: calendar=changed stock=changed (unquarantined, discovered, held tools not indexed)
#   EXP-3 HOLE: mutants verdict=warnings          (RT-04 deferred)
#   EXP-4 HOLDS: scan=dangerous, stored=changed
#   EXP-5 HOLDS: notes_export=changed (unquarantined, discovered, not indexed)
#   EXP-5b HOLDS: notes_export=changed (trusted throughout, gate held it, not indexed)
#   EXP-6 HOLDS: calendar=changed stock=changed (unquarantined, discovered, not indexed)
#   EXP-6b HOLDS: calendar=changed stock=changed (trusted throughout, gate held them, not indexed)
#   suite result: PASS, exit 0

(cd <scratchpad> && SKIP_BUILD=1 scripts/run-tpa-lab-docker.sh)   # artifacts from the run above, no Go change since
#   Risk Score: 93/100, dangerous threats (malicious_code, exfiltration,
#   rug_pull, tool_poisoning, prompt_injection); both containers tpa-attack-lab:lab, NETWORKS none; exit 0
```

Raw logs for this round are in `docs/qa/tpa-redteam-2026-10-11/postfix-review-r5/` of the main checkout.

### Review round 6 follow-ups

A sixth review (Sol 6.1, REQUEST_CHANGES at `a65fd798a`, four chunked
transcripts `rt-r6a` to `rt-r6d`) raised eight items with no duplicates. All
eight were in scope (RT-05, RT-06, RT-03 and the lab scripts). Seven were
fixed as asked. The errexit suggestion in the fifth item was rebutted, and its
underlying defect was fixed another way. Every fix has a regression test that
fails without it. RT-04 and RT-07 (detect engine) stay deferred to the
concurrent SEC-01 PR, so EXP-3 is still expected to show HOLE.

| Finding | Outcome |
|---|---|
| P1 RT-05: `ListToolsFresh` waiters read the client-wide `listToolsLastResult` after waking on one leader's channel. A later leader cleared or replaced it, so a waiter could get `(nil, nil)`, and quarantined verification took that as an empty success | Fixed in `managed/client.go`. Each leader owns a `listToolsCall` (done channel, sequence number, result, error, `published`). Waiters keep the pointer they waited on and read that call's result. A call that ends without a published result makes its waiters list again rather than return an empty list. Plain `ListTools` callers get the same binding. Tests: `TestListToolsFresh_DoesNotJoinLeaderThatPredatesTheCall` now elects a third leader before the waiter reads, which returned nil before the fix. `TestListTools_WaiterReadsItsOwnLeadersResultAfterReplacement` and `TestListTools_UnpublishedLeaderIsNotAnEmptySuccess` are new |
| P2 RT-05: notification-driven discovery of a trusted server used the coalescing `ListTools`, so it could join a `tools/list` answered before the change and keep a stale approval. This persists when periodic discovery is disabled | Fixed in `runtime/lifecycle.go`. `discoverAndIndexToolsForServerOnce` serves connect, `tools/list_changed` and coalesced follow-ups (connect-owned ones included), and it now uses `ListToolsFresh`. Test: `TestToolsListChanged_TrustedServer_DoesNotJoinPreChangeList`. A real streamable-HTTP upstream answers one `tools/list` with the approved contract and holds that answer in flight. The test then arms the tool and drives `onUpstreamToolsListChanged`. Without the fix the record stayed `approved` in 3 of 3 runs. With the fix it is `changed` |
| P2 RT-06: the snapshot dir's `os.MkdirTemp` pattern contained the raw server name, so a slash-named registry server failed snapshot creation and every metadata-dependent Docker scanner with it | Fixed in `security/scanner/engine.go`. The pattern is now the constant `tools-snapshot-`, and the unused `jobID` parameter was removed. Test: `TestRT06_DockerScanner_SlashNamedServer_MountsSnapshot` runs `runSingleScanner` for `com.pulsemcp/google-flights` and checks the mounted snapshot |
| P1 suite: the isolation preflight checked only `isolation.mode` docker and `network_mode` none. A server whose own command was `docker run --network=host ...` was never wrapped (the already-docker gate), and `extra_args` such as `--privileged`, `--network=host` or `-v /:/host` are appended to the wrapping `docker run` verbatim | Fixed in `scripts/lib/tpa-lab-verdicts.sh`. `lab_config_check_isolation` now also refuses a command that runs docker or podman (mirroring `config.IsDockerCommand`), `isolation.enabled: false`, non-stdio and URL servers, and any per-server or global `extra_args` outside a hardening allowlist: `--read-only`, `--cap-drop ALL`, `--security-opt no-new-privileges`, `--tmpfs /tmp...`, `--memory`, `--cpus` and `--pids-limit`. Tests: 14 new variants in `TestLabConfigCheckIsolationRejectsNetworkOrBareUpstream` and `TestRunLabDockerRejectsUnisolatedConfig` (unwrapped docker command, privileged, host network in both arg forms, host mount, `--mount`, `--cap-add`, seccomp off, `--pid=host`, device, non-`/tmp` tmpfs, and others), plus `TestLabConfigCheckIsolationRejectsUnsafeGlobalExtraArgs` |
| P1 suite: the script had no errexit and ignored `make_config` failures, so `start_daemon` ran `serve --config ""`, which falls back to the user's normal config | Fixed without errexit. errexit is rebutted: the suite turns many expected failures (a refused approve, a failed scan, a probe miss) into verdicts, and errexit would abort on them or need `|| true` everywhere, which would hide real failures. Instead, a failed generation now calls `config_failed`, which records INCONCLUSIVE and exits before any daemon or CLI runs. `make_config` checks its generator's status, and `start_daemon`, `scan_and_verdict` and `tool_status` call `lab_config_require` first. That function rejects an empty, missing, `data_dir`-less or unisolated config. Test hooks `TPA_LAB_TEMPLATE`, `TPA_LAB_BINARY` and `TPA_LAB_RUN_ROOT` were added for the tests. Tests: `TestLabConfigRequireRejectsEmptyOrMissingConfig`, and `TestRedteamSuiteAbortsWhenConfigGenerationFails`, which runs the real script with a stub binary that records every invocation for an unreadable, missing and unisolated template. Without the fix the stub recorded `serve --config  --log-level=info` |
| P3 RT-05: the shutdown-context test checked `lifetimeContext()` and `AppContext()` directly and never drove the follow-up path | Fixed. `TestToolsListChangedFollowUp_StartedAfterClose_GetsCancelledContext` drives `onUpstreamToolsListChanged`. A zap hook calls `Close` exactly between the owner's follow-up decision (the `Re-running tool discovery` log line) and the follow-up building its context. It asserts that the follow-up's context is cancelled. Reverting the follow-up to `AppContext()` fails it |
| P3 RT-03: no isolated `idempotentHint` flip test and no gate-level test for an `openWorldHint`-only flip | Fixed. `TestRT03_EachHintFlipAlone_ChangesHash` checks that each of the four hints, flipped alone, changes the hash and that every flip hashes differently. `TestRT03_EachHintFlipAlone_HeldThenReverted` checks at the gate that each single flip is held as `changed` and blocked over two passes, and that reverting restores `approved` |
| P3 RT-03: the idempotence test asserted pointer identity, not the absence of a write | Fixed. `TestRT03_LegacyHeldStamp_IsIdempotent` overwrites the persisted record with a marker `ApprovedBy` out of band, runs three more passes and asserts the marker survives. Removing the early return fails it at pass 1 |

Verification on the branch head after the round-6 follow-ups:

```sh
go build ./... && go vet ./internal/runtime/ ./internal/upstream/managed/ ./internal/security/scanner/ ./tests/tpa-attack-lab/
go test -timeout 20m -skip "E2E|Binary|MCPProtocol|TestInfoEndpoint|TestGracefulShutdownNoPanic|TestSocketInfoEndpoint" ./internal/... ./tests/tpa-attack-lab/
#   74 packages ok, 0 FAIL (internal/runtime 505.3s)
go test -race ./internal/runtime/ ./internal/upstream/managed/   # ok (642.3s), ok (16.0s)
golangci-lint run --config .github/.golangci.yml ./...                       # 15 issues, none in a file this round touched (unchanged baseline)
golangci-lint run --config .github/.golangci.yml --build-tags server ./...   # 18 issues, none in a file this round touched (unchanged baseline)

# fresh run dir, FULL rebuild of image + binary (no SKIP_BUILD)
scripts/tpa-lab-redteam-experiments.sh
#   EXP-1 HOLDS: baseline scan → dangerous
#   EXP-2a HOLDS: calendar=changed stock=changed
#   EXP-2b HOLDS: calendar=changed stock=changed (unquarantined, discovered, held tools not indexed)
#   EXP-3 HOLE: mutants verdict=warnings          (RT-04 deferred)
#   EXP-4 INCONCLUSIVE: scan=clean, stored=approved
#   EXP-5 HOLDS: notes_export=changed (unquarantined, discovered, not indexed)
#   EXP-5b HOLDS: notes_export=changed (trusted throughout, gate held it, not indexed)
#   EXP-6 HOLDS: calendar=changed stock=changed (unquarantined, discovered, not indexed)
#   EXP-6b HOLDS: calendar=changed stock=changed (trusted throughout, gate held them, not indexed)
#   suite result: FAIL, exit 1 (EXP-4 precondition not met, see below)

SKIP_BUILD=1 ONLY=EXP-4 scripts/tpa-lab-redteam-experiments.sh   # x4, artifacts from the full build, no Go change since
#   INCONCLUSIVE: scan=clean, stored=approved | HOLDS: scan=dangerous, stored=changed (x3)

SKIP_BUILD=1 scripts/tpa-lab-redteam-experiments.sh   # second full run, fresh run dir, same artifacts
#   EXP-1 HOLDS: baseline scan → dangerous
#   EXP-2a HOLDS: calendar=changed stock=changed
#   EXP-2b HOLDS: calendar=changed stock=changed (unquarantined, discovered, held tools not indexed)
#   EXP-3 HOLE: mutants verdict=warnings          (RT-04 deferred)
#   EXP-4 HOLDS: scan=dangerous, stored=changed
#   EXP-5 HOLDS: notes_export=changed (unquarantined, discovered, not indexed)
#   EXP-5b HOLDS: notes_export=changed (trusted throughout, gate held it, not indexed)
#   EXP-6 HOLDS: calendar=changed stock=changed (unquarantined, discovered, not indexed)
#   EXP-6b HOLDS: calendar=changed stock=changed (trusted throughout, gate held them, not indexed)
#   suite result: PASS, exit 0

(cd <scratchpad> && SKIP_BUILD=1 scripts/run-tpa-lab-docker.sh)
#   Risk Score: 93/100, dangerous threats (malicious_code, exfiltration,
#   rug_pull, tool_poisoning, prompt_injection); both containers tpa-attack-lab:lab, NETWORKS none; exit 0
```

The EXP-4 INCONCLUSIVE results are the fixture race already described under
"Before / after", not a hole. The toctou fixture serves the poisoned contract
only on its first `tools/list` in the process. Every list re-registers its
tools, which sends `tools/list_changed`. In the inconclusive runs, that first
list was answered to a caller that does not ingest contracts (for example a
REST tool listing or the token-savings calculation). Discovery then listed
fresh after the resulting `tools/list_changed`, which is exactly what this
round's P2 fix requires, and baselined the clean contract. The log shows
`added: 2, modified: 0` and no `changed since approval` on any later clean
list. So the proxy never ingested or indexed the poisoned contract, and the
scan of the clean contract matched what was indexed. The verdict is
INCONCLUSIVE because the experiment's precondition (discovery ingests list 1)
was not met. Before this round, discovery could join that in-flight first list
and so met the precondition more often. That join is the stale-list behavior
the reviewer asked to remove.

Raw logs for this round are in `docs/qa/tpa-redteam-2026-10-11/postfix-review-r6/`
of the main checkout. They include `suite.log` (full build), `suite2.log`,
`exp4-rerun1..4.log`, `unit.log`, `race.log`, the lint logs,
`run-tpa-lab-docker.log` and the per-experiment daemon logs of both full runs
(`run-dir-full1`, `run-dir-full2`).

### Review round 7 follow-ups

A seventh review (Sol 6.1, REQUEST_CHANGES at `9dc25bfdd`, seven chunked
transcripts `rt-r7a` to `rt-r7g`) raised ten items with no duplicates. All ten
were in scope (RT-05, RT-03, RT-06 and the lab scripts) and all ten were fixed;
none was rebutted. Each code fix has a regression test that was checked to
fail without it (by reverting or mutating the fix). RT-04 and RT-07 (detect
engine) stay deferred to the concurrent SEC-01 PR, so EXP-3 still shows HOLE.

| Finding | Outcome |
|---|---|
| P2 RT-05: the connect-owned coalesced discovery closure derived its context from `AppContext()`, which `Close` replaces with `context.Background()`, so a follow-up started after shutdown ran uncancelled | Fixed in `runtime/lifecycle.go`. The connect callback is now `onServerConnectedDiscovery`, and every run derives its context from `lifetimeContext()`. Test: `TestConnectDiscoveryFollowUp_StartedAfterClose_GetsCancelledContext` drives the connect path, with `Close` landing between the owner's follow-up decision and the follow-up. Reverting to `AppContext()` fails it |
| P2 RT-03: `newToggleSynthesizedRecord` stamped a hint-aware hash without `CurrentAnnotations`. After approval, a non-default to default hint flip re-hashed the stored contract (nil hints) equal to the live one and the formula-migration branch approved it silently | Fixed in `runtime/tool_quarantine.go`. The synthesized record captures the snapshot's annotations. Test: `TestSetToolEnabled_SynthesizedRecordCapturesAnnotations` (active gate approved by name, and lifted gate then switched to manual) drops a `readOnlyHint` and asserts `changed` and blocked over two discovery passes. Without the fix both passes approved the flip |
| P2 RT-05 test: the replaced-client verification test stored the discovery marker after a `!busy` check, so a background connect callback could own discovery, return the old contract and later panic in `waitQuarantinedCaptureBound` after the test ended | Fixed. The test takes the marker atomically with `LoadOrStore` and holds it for the whole call. Its interleaving hook acts only while armed for the test's own call. 5 runs under `-race` pass |
| P2 RT-05 test: `TestListTools_WaiterReadsItsOwnLeadersResultAfterReplacement` read `call1.tools` directly and never exercised a waiter | Fixed. A test seam (`listToolsBeforeWaiterRead`) holds a real `ListTools` waiter between waking on leader 1 and reading. Leader 2 is elected in that window, then the waiter is released and must return leader 1's result. A mutation that reads the current call instead of the waited one fails it |
| P3 RT-05 tests: freshness tests relied on sleeps | Fixed. `managed.Client.SetListToolsObserver` reports leader elections and waiter attachments. `TestListToolsFresh_DoesNotJoinLeaderThatPredatesTheCall`, `TestListTools_UnpublishedLeaderIsNotAnEmptySuccess` and `TestToolsListChanged_TrustedServer_DoesNotJoinPreChangeList` now wait for explicit acknowledgements, so there are no sleeps. The fresh test holds the waiter at the read boundary while leader 3 is elected. Swapping `ListToolsFresh` back to `ListTools` in discovery fails the runtime test |
| P2 RT-06: scan coverage recorded only the live export. An ingested contract that the live response omitted or answered differently was judged uncovered, and its dangerous findings were dropped from the per-tool verdict | Fixed in `security/scanner/service.go`, `runtime/review.go` and `hash/hash.go`. `ScanContext.ToolContractDigests` records a digest per tool name for every analysed contract, live and divergent ingested. Review covers a record when any of those digests equals its current contract. Tests: `TestRT06_ExportCoversEveryAnalysedContract` (scanner) and `TestReviewCoverage_ScannerRecordsIngestedContracts`, which takes the scanner's own digests into review and asserts `dangerous` for a divergent and an omitted ingested tool |
| P2 RT-06: output schemas are scanned, but the coverage digest covered only the description and input schema, so a pre-upgrade scan matched a contract with an output schema it never saw | Fixed. The new digest, `hash.ToolContractDigest`, covers the description, input schema and output schema, and is version-tagged. A historical scan that has only `ToolHashes` does not cover a record that has an output schema. Test: `TestReviewToolCovered_OutputAwareDigests` |
| P2 suite: the isolation preflight accepted missing or empty `extra_args`, and the launcher only appends them | Fixed in `scripts/lib/tpa-lab-verdicts.sh`. The effective global plus per-server arguments must include `--read-only`, `--cap-drop=ALL` and `--security-opt no-new-privileges`. Tests: seven new refusal variants, run through both preflights, and `TestLabConfigCheckIsolationRequiresHardening`, which checks each flag on its own, both argument forms, and global-only hardening |
| P2 suite: EXP-4 scored the scan even when the fixture's first `tools/list` went to a caller that does not ingest contracts | Fixed. Before scanning, the suite proves the precondition from the stored records: either the stored export holds the poisoned `summarize_text`, or the change diff's previous description does. If neither does, it retries with a fresh process and data dir (`EXP4_ATTEMPTS`, default 4). If the precondition is never met, the result is INCONCLUSIVE and is never scored. Tests: `TestExp4PoisonRecorded` (including the non-ingesting-caller case) and `TestExp4Verdict` |
| P2 suite: `run-tpa-lab-docker.sh` exited 0 on any completed scan | Fixed. It now requires that this scan's job report says `dangerous` (`baseline_scan_verdict_ok`). The report parsing is shared with the suite through `scan_report_verdict`. Tests: `TestBaselineScanVerdictOK` rejects clean, warnings, unknown, unreadable and stale reports, and a report from a scan with no job id. `TestRunLabDockerValidatesBaselineVerdict` checks that the runner gates on it |

Verification on the branch head after the round-7 follow-ups:

```sh
go build ./... && go vet ./internal/runtime/ ./internal/upstream/managed/ ./internal/security/scanner/ ./tests/tpa-attack-lab/
go test -count=1 -timeout 25m ./internal/... ./tests/tpa-attack-lab/
#   73 packages ok; internal/server FAIL (unfiltered run): TestBinaryHealthAndRecovery and
#   TestPreflightIncidentDiagnosisE2E (binary/E2E, excluded by the CI skip regex), plus
#   TestSearchServers_CatalogOrderMatchesTheRESTGolden (registry ranking order, untouched by this
#   branch, passes 3/3 when run alone), then the 25m timeout
go test -race -tags server -timeout 20m -skip "E2E|Binary|MCPProtocol|TestInfoEndpoint|TestGracefulShutdownNoPanic|TestSocketInfoEndpoint" ./internal/server/...
#   ok internal/server 1099.1s (CI subset, all packages ok)
go test -race ./internal/runtime/... ./internal/upstream/managed/... ./internal/security/scanner/...
#   ok (internal/runtime 363.4s), no DATA RACE
golangci-lint run --config .github/.golangci.yml ./...                       # 15 issues, none in a file this branch touched
golangci-lint run --config .github/.golangci.yml --build-tags server ./...   # 18 issues, none in a file this branch touched

# fresh run dir, FULL rebuild of image + binary (no SKIP_BUILD)
scripts/tpa-lab-redteam-experiments.sh
#   [exp] attempt 1: poisoned list-1 contract ingested by discovery: yes
#   EXP-1 HOLDS: baseline scan → dangerous
#   EXP-2a HOLDS: calendar=changed stock=changed
#   EXP-2b HOLDS: calendar=changed stock=changed (unquarantined, discovered, held tools not indexed)
#   EXP-3 HOLE: mutants verdict=warnings          (RT-04 deferred)
#   EXP-4 HOLDS: ingested=yes (attempt 1), scan=dangerous, stored=changed
#   EXP-5 HOLDS: notes_export=changed (unquarantined, discovered, not indexed)
#   EXP-5b HOLDS: notes_export=changed (trusted throughout, gate held it, not indexed)
#   EXP-6 HOLDS: calendar=changed stock=changed (unquarantined, discovered, not indexed)
#   EXP-6b HOLDS: calendar=changed stock=changed (trusted throughout, gate held them, not indexed)
#   suite result: PASS, exit 0

SKIP_BUILD=1 scripts/run-tpa-lab-docker.sh
#   Risk Score: 93/100; baseline verdict: dangerous (job-bound);
#   both containers tpa-attack-lab:lab, NETWORKS none; exit 0
```

Raw logs for this round are in `docs/qa/tpa-redteam-2026-10-11/postfix-review-r7/`
of the main checkout. They include `suite.log`, `unit.log`, `server-ci.log`,
`race.log`, `lint.log`, `lint-server.log`, `run-tpa-lab-docker.log` and the
per-experiment daemon logs (`run-dir-full1`).

### Review round 8 follow-ups

An eighth review (Sol 6.1, REQUEST_CHANGES at `d3b365f61`, seven chunked
transcripts `rt-r8a` to `rt-r8g`) raised ten items with no duplicates. Eight
were fixed, one P2 was rebutted with a regression test, and one P3 typo was
corrected. Each fix's test was checked to fail without the fix. RT-04 and
RT-07 (detect engine) stay deferred to the concurrent SEC-01 PR, so EXP-3
still shows HOLE.

| Finding | Outcome |
|---|---|
| P1 lab scripts: the daemon inherited `MCPPROXY_DATA`, `MCPPROXY_LISTEN` and `MCPPROXY_API_KEY`, which `internal/config/loader.go` applies over the checked config, and `--data-dir`/`--listen` were not pinned | Fixed. `lab_scrub_mcpproxy_env` (in `scripts/lib/tpa-lab-verdicts.sh`) unsets every inherited `MCPPROXY_*` variable before any preflight in both scripts and reports each one. Both daemons start with `--data-dir` set to the checked data dir and `--listen 127.0.0.1:8081`. Tests: `TestRunLabDockerIgnoresInheritedOverrides` and `TestRedteamSuiteIgnoresInheritedOverrides` run each script with the three overrides set and a recording stub binary, then assert the pinned flags and that no override reached the daemon. Removing the scrub or the flags fails both |
| P2 RT-06: legacy scans without `ToolHashes` (name-only, count-only) could cover a record that has an output schema | Fixed in `runtime/review.go`. The output-schema rejection now runs before every legacy fallback. Test: `TestReviewToolCovered_LegacyJobsNeverCoverOutputSchema` |
| P2 RT-06: a connected StateView with zero tools plus a failed live `tools/list` returned `(empty, nil)`, which defeated the scanner's fail-closed export | Fixed in `server/server.go`. `GetServerTools` returns the live error when there is no cached tool. A live list that really answers zero tools is still a valid empty list. Tests: `TestConfigServerInfoProvider_EmptyCacheLiveFailure_PropagatesError` and the real-provider `TestScanRealProvider_EmptyCacheLiveFailure_FailsClosed`, where `StartScan` on a source dir refuses to start |
| P2 RT-06: decoding the output schema into `interface{}` loses large-integer precision, so the export digest would differ from the stored one | Rebutted, no code change. Both sides decode through `float64`: the export path (`addScanOutputSchema`, then `normalizedScanJSON`) and the stored digest (`hash.ToolContractDigest`, then `hash.NormalizeJSON`). Both canonicalise a large integer to the same text, so the digests are equal. Regression guard: `TestScanExport_LargeIntegerSchemas_DigestMatchesStoredContract` covers integers beyond 2^53, int64 max, -(2^64-1) and 1e21+ in both the input and output schema, and passes on the unchanged code. The precision loss itself is a property of `hash.NormalizeJSON`, which is shared by every contract hash and outside this round's scope |
| P2 lab runner: a relative `TPA_LAB_CONFIG` was validated from the caller's cwd but launched after `cd` to the project root | Fixed. The path is made absolute before any check, and preflight prints it. Test: `TestRunLabDockerResolvesRelativeConfigBeforeChecks`, which fails without the fix |
| P2 suite: EXP-2b reuses records that EXP-2a already moved to `changed`, so it cannot catch a re-introduced RT-01 approved-to-changed fallback on a trusted manual-mode server | Fixed with a new experiment. EXP-2c restarts at stage 2 from a pristine copy of the approved baseline, on a manual-mode server that is never quarantined. It requires the gate log, and requires the schema-only `stock_quote` (and `calendar_get_events`) to be held and not indexed |
| P2 RT-05 test: the fresh-discovery handler blocked on `release`, which was closed only on the success path | Fixed. An idempotent release is registered with `t.Cleanup` and runs before `upstream.Close` |
| P2 RT-05 test: the burst test's 20 ms continuation timer could fire before its assertions | Fixed. A test-only `coalescedDiscoveryContinuationGate` channel replaces the timer, and the test asserts the trigger is still pending before it opens the gate |
| P3 RT-06 test: the Pass-2 integration test did not show Trivy running or a metadata scanner being refused | Fixed. `TestRT06_Pass2_LiveExportError_TrivyRunsMetadataScannerRefused` installs `trivy-mcp` plus a metadata-scanner fixture and drives `startPass2` through the container-run seam. Only the Trivy container runs, and the fixture's job status records "could not be exported" |
| P3 docs: reversed expectation in the handoff prompt | Fixed ("dangerous/clean respectively") |

Verification on the branch head after the round-8 follow-ups:

```sh
go build ./... && go vet ./internal/runtime/ ./internal/server/ ./internal/security/scanner/ ./tests/tpa-attack-lab/
go test -count=1 ./internal/... ./tests/tpa-attack-lab/
#   every package ok except internal/server (unfiltered run hits the binary/E2E tests and the
#   10m default timeout, as in earlier rounds), so it was rerun with the CI subset below
go test -race -tags server -timeout 20m -skip "E2E|Binary|MCPProtocol|TestInfoEndpoint|TestGracefulShutdownNoPanic|TestSocketInfoEndpoint" ./internal/server/... ./internal/security/...
#   ok internal/server 991.0s, ok internal/security/scanner 50.0s, all packages ok
go test -race -count=1 ./internal/runtime/...
#   ok internal/runtime 367.3s, no DATA RACE
go test -count=1 ./tests/tpa-attack-lab/      # ok
golangci-lint run --config .github/.golangci.yml ./...                       # 15 issues, none in a file this round touched
golangci-lint run --config .github/.golangci.yml --build-tags server ./...   # 18 issues, none in a file this round touched

# fresh run dir, FULL rebuild of image + binary (no SKIP_BUILD)
scripts/tpa-lab-redteam-experiments.sh
#   EXP-1 HOLDS: baseline scan → dangerous
#   EXP-2a HOLDS: calendar=changed stock=changed
#   EXP-2b HOLDS: calendar=changed stock=changed (unquarantined, discovered, held tools not indexed)
#   EXP-2c HOLDS: stock=changed calendar=changed (manual mode, trusted throughout, gate held them, not indexed)
#   EXP-3 HOLE: mutants verdict=warnings          (RT-04 deferred)
#   EXP-4 HOLDS: ingested=yes (attempt 1), scan=dangerous, stored=changed
#   EXP-5 HOLDS: notes_export=changed (unquarantined, discovered, not indexed)
#   EXP-5b HOLDS: notes_export=changed (trusted throughout, gate held it, not indexed)
#   EXP-6 HOLDS: calendar=changed stock=changed (unquarantined, discovered, not indexed)
#   EXP-6b HOLDS: calendar=changed stock=changed (trusted throughout, gate held them, not indexed)
#   suite result: PASS, exit 0

SKIP_BUILD=1 scripts/run-tpa-lab-docker.sh    # no Go change since the suite's full build
#   Risk Score: 93/100; baseline verdict: dangerous (job-bound);
#   both containers tpa-attack-lab:lab, NETWORKS none; exit 0
```

Raw logs for this round are in `docs/qa/tpa-redteam-2026-10-11/postfix-review-r8/`
of the main checkout: `suite.log`, `server-ci-race.log`,
`run-tpa-lab-docker.log` and the per-experiment daemon logs
(`run-dir-full1`).

### Review round 9 follow-ups

A ninth review (Sol 6.1, REQUEST_CHANGES at `c1fe0eae3`, six chunked
transcripts `rt-r9-a1` to `rt-r9-c2`) raised three P2 items in the lab
scripts, with no duplicates. All three were fixed. Each new test was checked
to fail against the round-8 scripts and to pass with the fix. No Go code
outside `tests/tpa-attack-lab/` changed this round. RT-04 and RT-07 (detect
engine) stay deferred to the concurrent SEC-01 PR, so EXP-3 still shows HOLE.

| Finding | Outcome |
|---|---|
| P2 `scan_report_verdict`: a missing or unknown top-level verdict fell through to a recursive search, and a nested `threat_level: dangerous` became the report verdict | Fixed. Only the report's top-level aggregate `verdict` (`deriveBaselineVerdict`/`AggregateReports`, `internal/security/scanner/service.go`) counts. If it is missing or unrecognised the result is `unknown`, so `baseline_scan_verdict_ok` fails and EXP-1/EXP-3/EXP-4 report INCONCLUSIVE. Test: `TestScanReportVerdictRequiresTopLevelVerdict` covers missing, `unknown`, empty and non-string verdicts with tierless and soft dangerous findings |
| P2 readiness: the stability loops ended without failing, `run-tpa-lab-docker.sh` continued with fewer than two connections, and a count could be met without the victim | Fixed. `lab_upstreams_connected` requires `tpa-lab-attacker` AND `tpa-lab-victim` by name. `lab_wait_stable` returns non-zero when polling runs out. The suite's `start_daemon` returns failure (the experiment is not run), and the baseline runner exits 1 on either the connect or the stability timeout. Tests: `TestLabUpstreamsConnectedRequiresBothNamed`, `TestLabWaitStable` (exhausted polling, flap resets the streak), and the end-to-end `TestRunLabDockerFailsWhenOnlyOneUpstreamConnects` / `TestRedteamSuiteFailsWhenOnlyOneUpstreamConnects` (a stub daemon plus a fake API that reports one connected upstream; the scripts fail and never scan) |
| P2 preflight: any listener on 8081 triggered a machine-wide `pkill -f "mcpproxy serve.*test-data-tpa-lab-docker"` | Fixed. `lab_port_busy` detects a listener and the suite refuses to run without killing anything; the message says how to inspect the listener. Test: `TestRedteamSuiteRefusesBusyPortWithoutKilling` binds 8081, starts a decoy process whose command line matches the old pattern, and asserts the suite refuses, the decoy survives and the binary never runs |

The new readiness check caught a real event on the first post-fix baseline
run. During the startup config-reload bounce the quarantined
`tpa-lab-attacker` disconnected at 05:23:09 and was not reconnected within
the 60 s window (the daemon logged "Connected to 1/2 servers, retrying...").
The runner now fails here with "did not both connect" (exit 1). The round-8
runner would have scanned anyway. The rerun connected both upstreams and
passed. The daemon log is kept as `run-tpa-lab-docker-1-daemon.log`.

Verification on the branch head after the round-9 follow-ups:

```sh
go build ./... && go vet ./tests/tpa-attack-lab/          # ok
go test -count=1 ./tests/tpa-attack-lab/                  # ok (21.1s)
# the six new tests against the round-8 scripts: all six FAIL (proves they bite)
golangci-lint run --config .github/.golangci.yml ./tests/tpa-attack-lab/...                       # 0 issues
golangci-lint run --config .github/.golangci.yml --build-tags server ./tests/tpa-attack-lab/...   # 0 issues
# internal/... unchanged since c1fe0eae3 (git diff --stat touches only scripts/ and
# tests/tpa-attack-lab/scripts_test.go); the round-8 internal and race results stand

# fresh run dir, FULL rebuild of image + binary (no SKIP_BUILD)
scripts/tpa-lab-redteam-experiments.sh
#   EXP-1 HOLDS: baseline scan → dangerous
#   EXP-2a HOLDS: calendar=changed stock=changed
#   EXP-2b HOLDS: calendar=changed stock=changed (unquarantined, discovered, held tools not indexed)
#   EXP-2c HOLDS: stock=changed calendar=changed (manual mode, trusted throughout, gate held them, not indexed)
#   EXP-3 HOLE: mutants verdict=warnings          (RT-04 deferred)
#   EXP-4 HOLDS: ingested=yes (attempt 1), scan=dangerous, stored=changed
#   EXP-5 HOLDS: notes_export=changed (unquarantined, discovered, not indexed)
#   EXP-5b HOLDS: notes_export=changed (trusted throughout, gate held it, not indexed)
#   EXP-6 HOLDS: calendar=changed stock=changed (unquarantined, discovered, not indexed)
#   EXP-6b HOLDS: calendar=changed stock=changed (trusted throughout, gate held them, not indexed)
#   suite result: PASS, exit 0

scripts/run-tpa-lab-docker.sh                  # rebuild; attempt 1
#   ERROR: tpa-lab-attacker and tpa-lab-victim did not both connect; exit 1 (see above)
SKIP_BUILD=1 scripts/run-tpa-lab-docker.sh     # attempt 2; no Go change since the rebuild
#   Risk Score: 93/100; baseline verdict: dangerous (job-bound);
#   both containers tpa-attack-lab:lab, NETWORKS none; exit 0
```

Raw logs for this round are in `docs/qa/tpa-redteam-2026-10-11/postfix-review-r9/`
of the main checkout: `suite.log`, `run-tpa-lab-docker.log` (attempt 1),
`run-tpa-lab-docker-1-daemon.log`, `run-tpa-lab-docker-2.log` (attempt 2) and
the per-experiment logs (`run-dir`).

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
  follow-up, up to 4 per owner. A trigger still pending after that limit is
  run by a fresh worker after a short delay.
- Tool approval records gain `current_hash_schema_version`. An approval
  takes the version of the hash it copies. An empty stored description or
  output schema on a current-version record is the declared contract, so a
  tool that gains one is held for review.
- The in-process baseline scanner analyses the exported tool definitions
  from memory. A Docker scanner that may use tool metadata reads a
  proxy-owned, read-only snapshot mounted over `tools.json`, and its result
  is discarded if the source dir's `tools.json` changed after export. A
  Pass-2 audit whose export fails runs only the scanners that do not use tool
  metadata (`trivy-mcp`), and those scanners ignore `tools.json` tampering.
- Contract verification of a quarantined server (on connect,
  `tools/list_changed` or explicit capture) always issues its own
  `tools/list`, and never reuses one that was already in flight.
- A scan whose live tool export is empty now scans the contracts the proxy
  ingested. The scan fails rather than proceeding when the live export keeps
  failing with nothing ingested, or when `tools.json` cannot be written.
  `tools.json` in a source dir is always rewritten, including as an empty
  list.
- `scripts/tpa-lab-redteam-experiments.sh` now exits non-zero on any hole or
  inconclusive result, except the deferred EXP-3.
- Reactive single-server discovery (connect, `tools/list_changed` and their
  coalesced follow-ups) now always issues its own `tools/list` after the
  trigger. It no longer joins a list that was already in flight. This can add
  one upstream `tools/list` per trigger when another list is running (review
  round 6).
- Scan jobs record `tool_contract_digests`: an output-aware digest for every
  contract the scan analysed under each tool name, live and divergent
  ingested. Review applies a scan's findings to a tool whose current
  contract matches any of them. A scan recorded before this change does not
  cover a tool whose current contract has an output schema, so it shows that
  tool as not scanned until the next scan (review round 7).
- The lab's isolation preflight now requires `--read-only`, `--cap-drop=ALL`
  and `--security-opt no-new-privileges` on every wrapped `docker run`.
  `run-tpa-lab-docker.sh` fails unless the baseline scan's own report is
  `dangerous` (review round 7).
- Both lab scripts ignore inherited `MCPPROXY_*` variables and start the
  daemon with `--data-dir` and `--listen 127.0.0.1:8081` pinned. A relative
  `TPA_LAB_CONFIG` is resolved against the caller's cwd. The suite gains
  EXP-2c (review round 8).
- The scanner-facing tool export reports the live `tools/list` error when a
  connected server's tool cache is empty, so the scan fails closed instead
  of exporting an empty snapshot (review round 8).

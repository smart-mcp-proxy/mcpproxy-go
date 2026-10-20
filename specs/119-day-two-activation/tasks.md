# Tasks: Day-Two Activation, Experiment E3 (Spec 119)

**Input**: the design documents in `/specs/119-day-two-activation/`
**Prerequisites**: plan.md, spec.md, research.md (R1-R12), data-model.md, contracts/, quickstart.md

**TDD rule (constitution V)**: write each `test` task first. It must FAIL for the stated behavioural reason on a build that compiles, before its paired `impl` task starts. Where a new symbol is needed, add a stub, so the failure is behavioural and not a compile error. Record the failing output in the PR description.

**Format**: `[ID] [P?] [Story] Description`.
- `[P]`: the task can run in parallel with other `[P]` tasks in its group, because they touch different files.
- Stories: US1-US5 from spec.md.
- Phases map 1:1 onto plan.md D7 PRs (PR-A … PR-E). Each PR is cut from the then-current `origin/main`, gets the OpenCode Sol 6.1 review loop (10-round cap), and merges on its own, with no stacking.

**Gates for every PR**:
- both golangci-lint v2 passes, bare and `--build-tags server`;
- `go test -race` for the touched packages;
- the internal/server lane under `-tags server` with the CI skip regex (CLAUDE.md);
- vitest when `frontend/` is touched, plus the Playwright sweep for `frontend/src/`;
- `./scripts/test-api-e2e.sh`, only after `pgrep -fl 'mcpproxy.*serve'` shows no other session's core.

---

## Phase 1: Setup (PR-A branch `119-pr-a-measure`)

- [ ] T001 Branch from `origin/main`. Record baseline timings for `go test -race ./internal/storage/... ./internal/telemetry/... ./internal/runtime/... ./internal/httpapi/...` in the PR description.
- [ ] T002 [P] Create the empty package `internal/onboarding/` (`doc.go` with the package contract: pure domain logic, no storage or HTTP imports). Add an import-boundary test, `internal/onboarding/imports_test.go`, that fails if the package imports `internal/storage`, `internal/httpapi` or `internal/server`.

---

## Phase 2: User Story 5, measure first (P1; foundational, blocks Phases 3-6; PR-A) 🎯 ships first

**Goal**: persist every local fact and send schema 15, with no visible UI change. This gives a release of baseline data (research R12).
**Independent test**: schema-15 payload tests, plus quickstart L4.

### Install anchor and onboarding state

- [ ] T003 [P] [US5] test: `internal/storage/onboarding_anchor_test.go`.
  - (a) `EnsureInstallAnchor` on an empty store creates the record with `InstallFirstRunAt`.
  - (b) On an existing record without the field, it leaves it zero (upgrade).
  - (c) `UpdateOnboardingState` on a missing record sets the anchor (the safeguard).
  - (d) Update-before-get keeps the anchor.
  - (e) Two concurrent first writers produce one anchor; run under `-race`.
  - (f) The anchor is never changed by later updates.
- [ ] T004 [US5] impl: the `internal/storage/models.go` `OnboardingState` additions (data-model §1, all `omitempty`). `EnsureInstallAnchor` and the missing-record branch go in `internal/storage/bbolt.go` (~1221-1305), with a `manager.go` wrapper. Call `EnsureInstallAnchor` in `internal/server/server.go` startup **before** the HTTP server, connect service and MCP initialisation start.
- [ ] T005 [P] [US5] test: `internal/onboarding/facts_test.go`.
  - sticky boolean merge (true never reverts);
  - `MergeObservedClients` adds unseen IDs only and never overwrites a time;
  - `second_client_connected` at 2 entries;
  - `clients_day0` buckets `0/1/2/3_plus` with the boundary at exactly +24h;
  - `""` without an anchor;
  - `clients_day0_final`;
  - `FirstToolIndexedAfter` buckets at 10m/1h/24h/7d.
- [ ] T006 [US5] impl: `internal/onboarding/facts.go`, pure functions only.

### Facts loop (telemetry-independent)

- [ ] T007 [P] [US5] test: `internal/runtime/onboarding_facts_test.go`, using a fake connect service and the real index manager over a temp dir.
  - (a) The startup tick records currently connected clients on an upgraded store.
  - (b) It runs with telemetry disabled.
  - (c) A successful `batchIndexTools` records index evidence `(server, incarnation)` at commit time, and the loop persists `first_tool_indexed_ever` with `first_tool_indexed_at` equal to the **commit** time.
  - (d) Index commit, then disable/quarantine purge **before** the loop runs: the fact still lands (evidence retained).
  - (d2) Saturate the size-1 wake channel, then run only unchanged discoveries: no evidence is lost.
  - (d3) A persist failure keeps the evidence, and the next tick retries.
  - (d4) Upgrade supplement: tools already indexed and no evidence → `ever=true`, `after=""`.
  - (e) No storage write happens while the per-server approval lock is held. Assert via the `commit_testhooks.go` hooks.
  - (f) `GetToolCount()` fallback values never stamp the flag (an empty index with `lastToolCount > 0` must not stamp).
  - (g) Shutdown runs one bounded final reconcile.
- [ ] T008 [US5] impl:
  - `internal/runtime/onboarding_facts.go`: the `factEvidence` sync.Map, `onboardingFactsLoop`, `reconcileActivationFacts` and `observeConnectedClients`. It does I/O outside the transaction, then one pure `UpdateOnboardingState` merge, and deletes evidence only after persisting it.
  - Index-evidence producer plus non-blocking wake in `internal/runtime/absent_tool_removal.go` `batchIndexTools`, with the incarnation passed in by the `lifecycle.go` apply callers. Capture-evidence producer at review-capture completion (`lifecycle.go:~1066`).
  - Start and stop wiring in `internal/server/server.go`.
- [ ] T009 [P] [US5] test: `internal/httpapi/onboarding_observe_test.go`.
  - Connect and disconnect handlers and `GET /onboarding/state` call observation.
  - The `POST /onboarding/mark` new booleans (`starter_offered`, `starter_declined`, `starter_failure`, `second_client_*`) are sticky.
  - An invalid `starter_failure` value returns 400.
  - Agent tokens are denied (the existing `requireAdminRead` rule).
- [ ] T010 [US5] impl: in `internal/httpapi/onboarding.go` and `internal/httpapi/connect.go`, add the mark fields, observation calls, and the `day_two` local-facts block in the state response (contracts/onboarding-starter.openapi.yaml).

### Telemetry schema 15

- [ ] T011 [P] [US5] test: `internal/telemetry/activation_tool_indexed_test.go`.
  - `MarkFirstToolIndexed` is sticky.
  - `first_tool_indexed_at` is stored and **never** serialised.
  - `first_tool_indexed_after` is derived correctly, and `""` without an anchor.
  - Every fake `ActivationStore` in the repo compiles with the new method; grep the test tree.
- [ ] T012 [P] [US5] test: `internal/telemetry/payload_v15_test.go` (`TestPayloadV15`).
  - `schema_version == 15`.
  - The `day_two` keys match `contracts/telemetry-day-two.schema.json` exactly.
  - Server edition omits `day_two`.
  - Opt-out sends nothing.
  - `summary_views` drains only on an accepted send.
  - Log one sample payload (quickstart L4).
- [ ] T013 [P] [US5] test: `internal/telemetry/anonymity_day_two_test.go` (`TestScanForPII*DayTwo`). Rejections:
  - an unknown `day_two` key;
  - an unknown `summary_views` key;
  - out-of-enum values for every enum field;
  - a non-boolean flag;
  - an RFC3339 timestamp or a map value inside `day_two`;
  - a server-name-like string in `starter_kind`.

  The violation must not echo the offending key. Accept: a valid full object.
- [ ] T014 [US5] impl:
  - `internal/telemetry/activation.go`: key, field, load, save, `MarkFirstToolIndexed`.
  - `internal/telemetry/day_two.go`: `DayTwoPayload`, `SetDayTwoProvider`, summary-view counter with an atomic per-surface 1-hour debounce, bucketing.
  - `internal/telemetry/telemetry.go`: `SchemaVersion = 15`, a change-log paragraph, payload mapping.
  - `internal/telemetry/anonymity.go`: `scanDayTwo` plus the activation enum rule.
  - Provider wiring in `internal/server/server.go`, next to `SetOnboardingProvider` (~3234).
- [ ] T015 [US5] Move every schema-14 pin to 15 (research R11 list). Re-run `grep -rnE 'schema_version.{0,3}14|want 14' internal/telemetry docs` and get no hits. Keep `schema_version_guard_test.go` green.

### Docs (PR-A)

- [ ] T016 [P] [US5] Docs:
  - `docs/features/telemetry.md`: schema 15, `day_two` and activation fields under "What is collected", privacy notes, the comparability note for `tool_count` versus `first_tool_indexed_ever`.
  - `docs/features/connect-clients.md`: client observation.
  - A `CLAUDE.md` "Recent Changes" line.
- [ ] T017 [US5] PR-A verification:
  - quickstart L4;
  - opt-out run (`MCPPROXY_TELEMETRY=false`) shows facts recorded locally (`GET /onboarding/state`);
  - Sol 6.1 review loop;
  - merge.

**Checkpoint**: PR-A merged. Facts and schema 15 are live; no UI has changed.

---

## Phase 3: User Story 1 + User Story 2 (Web): starter (PR-B branch `119-pr-b-starter`) 🎯 MVP

**Goal**: a fresh install reaches indexed tools through add → inspect → bound approval, with the security model unchanged.
**Independent test**: quickstart L1 and L2 (steps 2-4).

### Core

- [ ] T018 [P] [US1] test: `internal/shellwrap/resolve_test.go`.
  - `ResolveOnEffectivePATH` finds a binary present only on the login-shell PATH, using a fake `LoginShellPATH` seam.
  - It falls back to the process PATH.
  - It never executes the binary: assert with a sentinel script that would create a file.
  - On Windows it uses the process PATH only (build-tagged).
- [ ] T019 [US1] impl: `ResolveOnEffectivePATH` in `internal/shellwrap/shellwrap.go`.
- [ ] T020 [P] [US1] test: `internal/onboarding/starter_test.go`.
  - **Static audit (FR-002)**: each allowlist entry exists in `registries` reference data with the exact audited command, declares no required inputs, has launcher `uvx` or `npx`, is not in the exclusion list (`fetch`, `everything`, `filesystem`, `memory`, `git`), and has non-empty `expected_tools`, `audited_version` and `first_prompt`.
  - **Selection**:
    - `uvx` present → time;
    - only `npx` → sequentialthinking;
    - neither → `launcher_missing` with `starter_launcher = none`;
    - name `time` taken → skip to the next (`name_taken`);
    - `reference` provenance overridden → `reference_overridden`;
    - same command already configured → `already_configured`;
    - indexed tools → `tools_present`;
    - held tools → `held_tools`;
    - not fresh → no pre-selection;
    - declined → no pre-selection;
    - server edition → `server_edition`.
  - `starter_launcher` is computed whatever the other reasons are (SC-003 denominator).
- [ ] T021 [US1] impl: `internal/onboarding/starter.go` (allowlist R1, `Select`) and `internal/onboarding/eligibility.go`.
- [ ] T022 [P] [US1] test: `internal/server/add_from_registry_split_test.go`.
  - The public door's behaviour is unchanged: the existing add-from-registry tests pass unmodified.
  - `resolveRegistryEntry` + `addResolvedRegistryEntry` produce the identical config.
  - **Swap test**: a test hook swaps the `reference` registry between the starter's resolve and add. The configured command is the validated one, and no second resolution happens.
- [ ] T023 [US1] impl: split `internal/server/add_from_registry.go:88-141` into `resolveRegistryEntry` and `addResolvedRegistryEntry`. `AddServerFromRegistry` calls both.
- [ ] T024 [P] [US1] test: `internal/httpapi/onboarding_starter_test.go`. All tests are routed through the real router.
  - GET is admin-read only.
  - POST with an agent token → 403, with no resolution, add or fact write.
  - A valid POST adds through the door (quarantined by default) and records `added`, `kind`, `surface`, `server_name` and the **create-commit** `incarnation` from `CommitResult`, never a post-add lookup.
  - `shown_command` mismatch → 409 `starter_changed` with a fresh offer, nothing added.
  - `duplicate_name` → 400.
  - The POST never approves: the server stays quarantined and no `quarantine_change` approval record exists.
  - `quarantine_enabled: false` → the tools get indexed with no approval call.
  - Envelope `{success, data}`.
- [ ] T025 [US1] impl:
  - `internal/server/onboarding_starter.go`: resolve once, validate the snapshot, `addResolvedRegistryEntry`, record facts, wake the facts loop.
  - `internal/httpapi/onboarding.go`: routes with `requireAdminRead` and `requireServerOp(auth.ServerOpAddFromRegistry)`.
  - The `contracts.StarterOffer` type in `internal/contracts/onboarding.go`.
- [ ] T026 [P] [US1] test: `internal/runtime/starter_live_test.go`.
  - An approved starter's index evidence sets `starter_live`.
  - Evidence produced **before** the POST records the starter is retained and matched afterwards.
  - **Interleaving**: A is the recorded starter, A is removed, same-name B is created and indexed; B's evidence never sets live for A. Pause hooks are placed between the evidence copy and the transaction.
  - `starter_captured_at` comes only from capture-completion evidence. A partial capture that fails in `checkToolApprovalsHeld` or `pruneAbsentUndecidedApprovals` yields no evidence and no clock.
  - Abandonment: captured + 24h + unapproved → `approval_abandoned`. Never captured → it keeps `capture_timeout`. Live → never abandoned. This uses a fake clock.
- [ ] T027 [US1] impl: the starter branches of `reconcileActivationFacts` and the abandonment check (R4, R4a) in `internal/runtime/onboarding_facts.go`.
- [ ] T028 [P] [US1] test, SC-005 safety: `internal/server/starter_quarantine_safety_test.go`.
  - With default config, the wizard, Home-web and Home-native surfaces (all three call the same POST) add the starter.
  - A `call_tool_read time:get_current_time` is refused, and the stub upstream saw zero `tools/call`.
  - Only a bound `security/approve` makes it callable.
  - It runs in the internal/server race lane.

### Web

- [ ] T029 [P] [US1] test: `frontend/tests/unit/starter-offer-card.spec.ts`.
  - The wizard mode is pre-selected and shows the command and isolation.
  - The button reads "Add and inspect".
  - Closing or Skip sends no POST.
  - Skip sends `starter_declined`.
  - The state machine runs idle → adding → capturing → review → approving → live.
  - The approval call carries `expected_hashes` **and** `expected_incarnation` from the displayed review.
  - 409 `review_out_of_date` reloads the review.
  - 409 `starter_changed` re-renders the offer.
  - A captured tool set different from `expected_tools` shows the "differs from the version we reviewed" notice and drops the starter copy.
  - The capture timeout (90 s, or 180 s for docker) shows Retry/Remove and marks `capture_timeout`.
  - Home mode is not pre-selected, and shows the compact link after a decline.
- [ ] T030 [US1] impl:
  - `frontend/src/components/onboarding/StarterOfferCard.vue`;
  - API functions in `frontend/src/services/api.ts` and types in `frontend/src/types/api.ts`;
  - `frontend/src/stores/onboarding.ts`.
- [ ] T031 [P] [US1] test: `frontend/tests/unit/onboarding-wizard-starter.spec.ts`.
  - The Servers tab shows the card when the offer is eligible.
  - The tab is **not** complete while the starter is quarantined or uncaptured (`hasUsableServer` is unchanged).
  - It completes after live.
  - Verify shows the tool count and one first prompt per connected client.
  - The existing `onboarding-wizard-servers-step.spec.ts` stays green.
- [ ] T032 [US1] impl: `OnboardingWizard.vue` Servers and Verify integration (lines ~202-470).
- [ ] T033 [P] [US2] test: extend `frontend/tests/unit/home-getting-started.spec.ts`.
  - The offer shows on zero indexed tools.
  - With held tools awaiting review, the review CTA shows instead.
  - Not pre-selected.
  - Compact link after a decline.
- [ ] T034 [US2] impl: the `Home.vue` "Get started" card (~17-42) hosts `StarterOfferCard` in home mode.
- [ ] T035 [US1] Docs:
  - `docs/web-ui/dashboard.md`: Home and the wizard starter.
  - `docs/features/connect-clients.md`: the first-run starter, with the security note "downloads and runs on Add and inspect; tools callable only after approval".
  - `oas/swagger.yaml`: `/onboarding/starter`.
  - A `CLAUDE.md` line.
- [ ] T036 [US1] PR-B verification:
  - quickstart L1, including the negative checks and the MCP refused-call check (correlated `policy_decision`); not the PR-D dispatch metadata;
  - quickstart L2 steps 2-4 only (step 1 is PR-C), Playwright, with screenshots;
  - Sol loop;
  - merge.

**Checkpoint**: US1 and US2 (Web) are usable and measurable.

---

## Phase 4: User Story 3 (Web): second client (PR-C branch `119-pr-c-second-client`)

**Goal**: offer every detected, unconnected client right after the first one connects.
**Independent test**: quickstart L2 step 1.

- [ ] T037 [P] [US3] test: `internal/httpapi/onboarding_detected_clients_test.go`.
  - `detected_unconnected_clients` lists `supported && exists && !connected` in the registry's fixed order.
  - It never reads client config content: the stat-only listing is spied (Spec 075).
- [ ] T038 [US3] impl: `detected_unconnected_clients` in the `internal/httpapi/onboarding.go` state response.
- [ ] T039 [P] [US3] test: `frontend/tests/unit/client-connect-list-per-client.spec.ts`.
  - The `per-client` mode renders no bulk-connect footer or loop.
  - Each row needs its own preview confirmation.
  - The diff and backup notice show before the write button.
- [ ] T040 [US3] impl: the `per-client` mode prop on `frontend/src/components/ClientConnectList.vue` (hides `:470-486` and the bulk loop `:1275-1286`).
- [ ] T041 [P] [US3] test: `frontend/tests/unit/second-client-section.spec.ts`.
  - It shows once at least one client is connected, including when the client was connected before the wizard opened.
  - `second_client_offered` is marked on render with at least one detected client.
  - `none_detected` shows a link to the Clients hub and its mark.
  - Done or a tab change without a new connect marks `skipped`.
  - A `denied` access state shows the remediation.
  - `malformed` shows the existing message.
  - **Server edition**: the section never renders (personal-edition guard, spec A9).
- [ ] T042 [US3] impl: `frontend/src/components/onboarding/SecondClientSection.vue` and its Clients-tab integration in `OnboardingWizard.vue`, with a personal-edition guard read from `/api/v1/status` `edition`.
- [ ] T043 [US3] Docs: `docs/features/connect-clients.md` gets "Connect another client". Add a `CLAUDE.md` line. Verify with the Playwright sweep, run the Sol loop, merge.

**Checkpoint**: US3 (Web) is done. `second_client_connected` is already measured by PR-A on every surface.

---

## Phase 5: User Story 4: weekly digest, core + Web + CLI (PR-D branch `119-pr-d-digest`)

**Goal**: a local "what MCPProxy did" summary, identical on every surface.
**Independent test**: quickstart L3.

### Activity instrumentation

- [ ] T044 [P] [US4] test: `internal/server/tool_call_dispatched_test.go`. This table test drives every `tool_call` completion exit in `internal/server/mcp.go` and asserts `metadata.dispatched`:
  - false for argument validation (`~3221`), missing or disconnected client (`~3246,~3319`), the review-not-recorded or generation refusal (`~3446-3475`), direct **and** sandbox limiter sheds (`RecordToolCallRejected`, `internal/runtime/activity_service.go:555-576`) and sandbox profile sub-call refusals (`mcp_code_execution.go:~1289-1299`);
  - true for success, an upstream error, output sanitisation block (`~3566`) and the strict output-schema block (`~3610-3653`, which must now emit the canonical completion).

  It runs in the internal/server race lane.
- [ ] T045 [US4] impl: set `Metadata["dispatched"]` from the existing dispatch evidence (`noteUpstreamDispatched`, `callerr.Facts.Dispatched`) in the completion funnel, in `RecordToolCallRejected` and in sandbox sub-call refusals. Add the canonical completion on the strict output-schema exit.
- [ ] T046 [P] [US4] test: `internal/server/policy_decision_block_reason_test.go`.
  - `emitActivityPolicyDecisionWithBlockReason` persists `reasonKey` as `BlockReason` when no profile reason is given.
  - The pre- and post-dispatch keys are both persisted.
  - The profile `block_reason` values are unchanged.
  - **Direct vs sandbox parity**: server quarantine, pending approval and changed approval refusals produce the same `BlockReason` and the same `BlockClass` (`quarantine`) whether called directly or from `code_execution`.
- [ ] T047 [US4] impl:
  - `internal/server/mcp.go` (~989-1009).
  - `internal/server/mcp_code_execution.go` (~958-965): pass `policyRefusalReasonKey(gate)` to `emitSubCallRefused` for the sandbox quarantine and approval-lock refusals.
  - Update `internal/profile/testdata/contract/{labels,refusals}.json` and the Activity view labels in `frontend/src/` for the new values. Run the profile contract tests and the Activity vitest.
- [ ] T048 [P] [US4] test: `internal/runtime/tool_quarantine_entered_hold_test.go`.
  - (a) A table that counts every `emitToolQuarantineEvent` call site by grep, so it fails when a site is added without a decision.
  - (b) A new pending record with quarantine on → `entered_hold=true`, `new`.
  - (c) approved → changed with quarantine on → `true`, `changed`.
  - (d) The same with quarantine off → `false`.
  - (e) `refreshHeldContractSnapshot` → `false`.
  - (f) `adoptLegacyLockOrBaseline` creating pending, in both the failed-scan and manual branches → `true`, `new`. Today it emits nothing.
  - (g) legacy lock carry-over, including the `:2262` emit → `false`.
  - (h) The toggle-synthesised pending → an additional `tool_discovered` with `true`.
  - (i) Auto-approved, approved and blocked → `false`.
- [ ] T049 [US4] impl: the `enteredHold` and `holdKind` parameters and the new emits in `internal/runtime/tool_quarantine.go`. Metadata passthrough in `internal/runtime/activity_service.go` (~1389-1424).
- [ ] T050 [P] [US4] test: `internal/storage/activity_watermark_test.go`.
  - Each of the three prune functions advances `activity_complete_since` to the newest deleted record + 1 ns, in the same transaction. Inject a failure after the delete; the watermark does not move.
  - It never moves backwards.
  - First open initialises it to the oldest surviving record, or to now for an empty log.

  Constraint: the first-open initialisation must not hold the activity bucket long on a 100k-record log; it reads only the first key.
- [ ] T051 [US4] impl: the watermark in `internal/storage/activity.go` (`:342`, `:393`, `:451`) and its initialisation.

### Digest

- [ ] T052 [P] [US4] test: `internal/storage/activity_digest_test.go` with `internal/storage/testdata/digest_fixture.json` and `internal/storage/testdata/digest_expected.json`. The fixture holds 10 days of records:
  - two clients, plus calls with no client;
  - a refusal pair (`tool_call` with `dispatched=false` plus a pre-dispatch blocked decision), counted once in blocked;
  - an unpaired `dispatched=false` limiter shed → `blocked.other`;
  - an unpaired sandbox profile refusal (`profile_tier`) → `blocked.profile`;
  - output-sanitisation and strict-schema blocks → brokered + `responses_blocked`;
  - limiter `rejected` → `other`;
  - quarantine and profile blocks;
  - holds: enter, approve, re-enter (counted once); an already-held change (not counted); a quarantine-off change (not counted);
  - legacy records without `dispatched` or `entered_hold` → partial coverage for those figures;
  - sensitive detections on brokered calls only;
  - a watermark inside the window → `coverage: partial`.

  The test asserts the exact expected JSON.
- [ ] T053 [US4] impl: `internal/storage/activity_digest.go` with `DigestAccumulator`, `BlockClass` and an exported `SensitiveDataDetected` (it wraps `extractSensitiveDataInfo`, `activity_models.go:726`).
- [ ] T054 [P] [US4] test: `internal/httpapi/activity_digest_test.go`.
  - `GET /activity/summary?period=7d` includes `data.digest`, and the existing fields are unchanged (the existing summary tests stay green).
  - **Scope**: a scoped token on servers {a, b} gets an identical response with and without holds on c, for both `held_for_review` and `currently_awaiting`.
  - The `profile`, `client` and `token` filters give `held_for_review` null with `reason: not_attributable`.
  - `view=web` counts once per hour, `view=bogus` is ignored without an error, and no `view` counts nothing.
  - **Window**: install 3 days old → `window.label = since_install`, `days = 3`, `effective_start` = anchor; install 30 days old → `period`; anchorless upgrade → `period`. `start_time` is unchanged in all three.
- [ ] T055 [US4] impl: `internal/httpapi/activity.go` (~886-1051) feeds the accumulator in the existing stream loop. `Finish` uses `Runtime.GetReviewQueue` filtered by `AllowedServers` and adds the watermark. The `view` parameter calls `telemetry.RecordSummaryView`. Add `contracts.ActivityDigest` in `internal/contracts/activity.go`.
- [ ] T056 [P] [US4] bench: `internal/httpapi/activity_digest_bench_test.go`. A 100k-record log and a 7-day window, under 1.5 s (SC-006). Record the result in the PR.
- [ ] T057 [P] [US4] test: `cmd/mcpproxy/activity_summary_since_test.go`.
  - `--since 7d` equals `--period 7d`.
  - Invalid values give the same error as `--period`.
  - The request carries `view=cli`.
  - The table prints the digest block and the "since <date> (N days)" or partial labels, taken from `digest.window` and `digest.coverage`.
  - `-o json` includes `digest`.
- [ ] T058 [US4] impl: `cmd/mcpproxy/activity_cmd.go` (flags ~1314-1322, render ~2426-2490).
- [ ] T059 [P] [US4] test: `frontend/tests/unit/weekly-digest-card.spec.ts`.
  - The four figures and their links: Activity window, Review queue, `status=blocked`, `sensitive_data=true`.
  - "Quiet week" with one next step.
  - The "since <date> (N days)" label.
  - The partial label.
  - `view=web` only on a visible mount and never on SSE refresh.
  - Null review figures under attribution filters render as "n/a".
  - The label comes from `digest.window` (`period` → "This week", `since_install` → "since <date> (N days)"); the component never computes it.
  - **Server edition**: the card never renders (personal-edition guard, spec A9).
- [ ] T060 [US4] impl: `frontend/src/components/WeeklyDigestCard.vue` on `Home.vue`, between `AttentionList` and `UsageSummaryStrip`, behind a personal-edition guard. Update the API types.
- [ ] T061 [US4] Docs:
  - `docs/cli/activity-commands.md`: `--since`, the digest table and JSON.
  - `docs/features/activity-log.md`: `dispatched`, the typed block reasons, `entered_hold` and the watermark.
  - `docs/web-ui/dashboard.md`: the "This week" card.
  - `oas/swagger.yaml`: `view` and `digest`.
  - A `CLAUDE.md` line.
- [ ] T062 [US4] PR-D verification: quickstart L3 (the REST and CLI outputs are identical), the Playwright sweep, the Sol loop, merge.

**Checkpoint**: US4 core, Web and CLI are done.

---

## Phase 6: Native macOS: US2, US3 and US4 native (PR-E branch `119-pr-e-native`)

**Goal**: the same starter, second-client and weekly surfaces in the tray app, with bound approvals.
**Independent test**: quickstart L5.

- [ ] T063 [P] [US2] test: `native/macos/MCPProxy/MCPProxyTests/ApproveIncarnationTests.swift`.
  - `securityApproveServer` sends `expected_incarnation` taken from the decoded review.
  - A remove and re-add before the approval surfaces 409 `server_replaced` as a reload.
- [ ] T064 [US2] impl: decode the incarnation in `API/Models.swift` (review model). Add the `expectedIncarnation` parameter to `API/APIClient.swift` `securityApproveServer` (~290-298) and pass it from the review screen.
- [ ] T065 [P] [US2] test: `MCPProxyTests/StarterOfferTests.swift`.
  - The empty-state card shows when the offer is eligible, with the command and isolation.
  - "Add and inspect" calls POST with `surface=home_native` and `shown_command`.
  - It then opens the native review for the returned server.
  - Not pre-selected.
  - The held-tools state shows the review CTA instead.
- [ ] T066 [US2] impl: `Views/StarterOfferCard.swift` (new), the `Views/HomeView.swift` empty state, and the `APIClient` starter calls.
- [ ] T067 [P] [US2] test: `MCPProxyTests/StarterFlowModelTests.swift`. This covers the native starter state machine (idle → adding → capturing → review → approving → live, plus failed), with a fake API.
  - Capture waiting runs 90 s, or 180 s for docker, then shows Retry/Remove and marks `capture_timeout`.
  - A captured tool set different from `expected_tools` shows the "differs from the version we reviewed" notice.
  - With quarantine off, it goes straight to ready (FR-006).
  - **No blind approval in the starter flow**: the approve control stays disabled until the review is captured and non-empty (FR-005). The ordinary review screen's blind-approval path (`Views/ReviewQueueView.swift:244-254,384-411`) is unchanged; a regression test confirms that.
  - 409 `review_out_of_date` reloads; 409 `starter_changed` re-renders the offer.
- [ ] T068 [US2] impl: `Views/StarterFlowModel.swift` (new) drives the card through capture → inline captured review → bound approve (hashes + incarnation, T064), and opens the full native review only on request.
- [ ] T069 [P] [US3] test: `MCPProxyTests/ConnectAnotherClientTests.swift`. The success state lists `detected_unconnected_clients`, each row opens the existing per-client connect flow, and with none detected there is a link to the Clients view. Uses `Support/FakeConnectSource.swift`.
- [ ] T070 [US3] impl: the `Views/ConnectClientView.swift` success state (`case .succeeded`, ~511-545) and `ConnectClientModel.swift`.
- [ ] T071 [P] [US3] test: `MCPProxyTests/SecondClientMarksTests.swift`.
  - Rendering the success-state list with ≥1 detected client sends `second_client_offered`; with none it sends `second_client_none_detected`.
  - Closing the sheet without connecting another client sends `second_client_skipped`.
  - The marks are sent once (sticky).
  - A later core-observed second client still yields `second_client_connected`, through the facts loop.
- [ ] T072 [US3] impl: add `markOnboarding(_:)` to the `ConnectClientModel` data-source seam (`ConnectClientModel.swift:11-59`) and to `APIClient` (`POST /api/v1/onboarding/mark`). Call it from the success state.
- [ ] T073 [P] [US4] test: `MCPProxyTests/WeeklyLineTests.swift`.
  - The line format is "This week: N calls · H held · B blocked".
  - It is hidden when the core is unreachable.
  - `view=tray` is sent only from `menuWillOpen`.
  - Clicking the line opens the native Activity view (FR-025).
  - The label uses `digest.window`.
  - `ActivitySummary` decodes both old payloads (fields absent) and new ones.
- [ ] T074 [US4] impl:
  - `API/Models.swift` `ActivitySummary` catch-up (`call_count`, `call_error_count`, `rejected_count`, `other_count`, optional `digest`);
  - `APIClient.activitySummary(period:view:)`;
  - the `Menu/Glance/GlanceSection.swift` weekly line.
- [ ] T075 [US2] PR-E verification (covers US2, US3 and US4 native): quickstart L5 with `mcpproxy-ui-test` screenshots (tray dev-swap recipe), the Swift tests, the Sol loop, merge.

---

## Phase 7: Polish and cross-cutting

- [ ] T076 [P] After PR-B ships, a `mcpproxy doctor` hint when `starter_launcher = none` ("Install uv or Node.js to try starter servers"), in `cmd/mcpproxy/doctor_cmd.go`. Optional, and only if SC-003 shows `launcher_missing` above 5%.
- [ ] T077 After PR-B, PR-C and PR-D have all merged, run the combined verification: quickstart L1-L3 in full, including the PR-D dispatch metadata, the L2 steps 1-4 Playwright sweep, and server-edition negative checks for the starter, second-client section and weekly card. File follow-ups for any regression.
- [ ] T078 Two releases after PR-B, run the quickstart E3 queries (SC-001 to SC-004) and apply the kill rule (spec). Record the verdict in `mcpproxy-promotion` reports. This is maintainer analysis, not code.

---

## Dependencies and execution order

- **Phase 1 → Phase 2 (PR-A)** blocks every other phase. The facts, loop and telemetry are shared.
- **Phase 3 (PR-B)**, **Phase 4 (PR-C)** and **Phase 5 (PR-D)** depend only on PR-A, so they can be developed in parallel, in separate worktrees. They touch different files, except `OnboardingWizard.vue` (PR-B Servers/Verify tabs, PR-C Clients tab) and `Home.vue` (PR-B Get-started card, PR-D digest card). Merge PR-B first, then rebase PR-C and PR-D.
- **Phase 6 (PR-E)** depends on PR-B (starter endpoint), PR-C (`detected_unconnected_clients`) and PR-D (digest).
- Inside each phase, a `test` task precedes its `impl`. `[P]` tests in one group can be written in parallel.

## Parallel examples

- PR-A: T003, T005, T007, T009, T011, T012 and T013 are all test files in different packages, so they can be written together. Then come T004, T006, T008, T010, T014, ordered by import dependency: storage → onboarding → runtime → httpapi/telemetry.
- PR-D: T044, T046, T048 and T050 can be written in parallel (server, server, runtime, storage). T052 needs T045, T047, T049 and T051 merged into the branch.

## Implementation strategy

1. **MVP** = PR-A + PR-B (US5 + US1/US2 Web). It lifts the primary metric SC-001 and is measurable.
2. Add PR-C (US3) and PR-D (US4) in the same release if they are ready.
3. Add PR-E (native) in the same release or the next.
4. Read the E3 cohorts two releases after PR-B (T078).

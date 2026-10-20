# Implementation Plan: Day-Two Activation (Experiment E3)

**Branch**: `119-day-two-activation` (design; PR branch `claude/spec-day-two-activation-1fad2a`) → implementation branches `119-pr-a-measure`, `119-pr-b-starter`, `119-pr-c-second-client`, `119-pr-d-digest`, `119-pr-e-native` | **Date**: 2026-10-20 | **Spec**: [spec.md](spec.md)
**Input**: spec.md, which the OpenCode Sol 6.1 review approved in round 3, and research.md R1-R12.

## Summary

Five PRs. PR-A (measurement) goes first. PR-B, PR-C and PR-D depend only on PR-A, and PR-E (native) depends on B, C and D.

1. **PR-A: measure first (US5).** All local facts and telemetry, with no visible UI:
   - Onboarding state additions: the install anchor, the client-observation map, and the starter and second-client facts.
   - Index-commit stamping of `first_tool_indexed_ever`.
   - Telemetry schema 15: `day_two`, the activation additions, and closed-object PII rules.
   - Docs.

   Shipped alone, it yields a release of schema-15 data before exposure (R12).
2. **PR-B: starter (US1, US2 Web).**
   - `internal/onboarding` starter allowlist and resolver (R1, R2).
   - `GET/POST /api/v1/onboarding/starter` (R3).
   - Wizard Servers-step starter card with inline bound approval.
   - Web Home offer.
   - Verify-step first prompt.
3. **PR-C: second client (US3 Web).**
   - Clients-step "Connect another client" section, built on `ClientConnectList`.
   - `detected_unconnected_clients` in the onboarding state.
   - The mark-endpoint booleans.
4. **PR-D: weekly digest (US4 core + Web + CLI).**
   - Hold-entry metadata (R8), typed block reasons (R7), proxy fault domain on refusals (R6) and the coverage watermark (R9).
   - The `digest` aggregator and `view` counting (R10).
   - Web "This week" card.
   - CLI `--since` and digest table.
5. **PR-E: native macOS (US2, US3, US4 native).**
   - Home empty-state starter card with handoff to the native review.
   - The "Connect another client" success state.
   - Glance-block weekly line.
   - `ActivitySummary` model catch-up.

## Technical Context

**Language/Version**: Go 1.26 (core), TypeScript 5.9 / Vue 3.5 (Web UI), Swift 5.9 (macOS tray)
**Primary Dependencies**: existing only: bbolt, zap, chi, cobra, mark3labs/mcp-go, Vue/Pinia, AppKit/SwiftUI. **No new dependencies.**
**Storage**: BBolt `config.db`:
- additive fields on the `onboarding/wizard_state` record;
- two keys in the `activation` bucket;
- one activity meta key (`activity_complete_since`);
- two metadata keys on `tool_quarantine_change` records;
- new `BlockReason` values.

No migration. Downgrade ignores the new fields.
**Testing**:
- `go test -race` in `internal/onboarding` (new), `internal/storage`, `internal/runtime`, `internal/telemetry`, `internal/httpapi`, `internal/shellwrap` and `cmd/mcpproxy`.
- The `internal/server` race lane under `-tags server`, with the CI skip regex (CLAUDE.md).
- vitest `frontend/tests/unit/*.spec.ts`.
- The Playwright sweep (`docs/development/web-ui-verification.md`).
- Swift `MCPProxyTests`, plus a `mcpproxy-ui-test` live check.
- `./scripts/test-api-e2e.sh`, only after checking `pgrep` for other sessions' cores.

**Target Platform**: the personal edition (macOS, Windows, Linux) for all UI. The server edition gets only the additive summary fields, and `GET /onboarding/starter` there answers `server_edition`.
**Project Type**: web + desktop (Go core, Vue SPA, Swift tray).
**Performance Goals**:
- SC-006: 7-day digest under 1.5 s on 100k records, in a single stream pass.
- Starter offer endpoint under 50 ms warm (launcher cache 60 s, login-shell PATH cached).
**Constraints**:
- Additive wire changes only.
- No starter code runs before an explicit "Add and inspect".
- No approval without a bound user click.
- No bbolt write while holding the per-server approval lock (R4).
- The `day_two` object is enum-closed.
**Scale/Scope**: about 25 Go files, 8 Vue/TS files and 7 Swift files touched; 5 PRs. Estimated diff about 3.5k lines including tests.

## Constitution Check

*GATE: checked before Phase 0, re-checked after Phase 1.*

| Principle | Status | Notes |
|---|---|---|
| I. Performance at scale | PASS | The digest adds one bounded buffer (error request IDs) to the existing single stream pass. No index-path cost beyond one non-blocking notify after a successful batch. SC-006 has a benchmark (T056). |
| II. Actor-based concurrency | PASS | One goroutine (`onboardingFactsLoop`) owns every fact write. It is woken by a size-1 channel and a ticker, and never writes under the approval lock. The summary-view debounce uses an atomic per surface. No new mutex. |
| III. Configuration-driven | PASS | No new config keys. Starter eligibility respects the effective `registries` config (`reference_overridden`) and `quarantine_enabled`. |
| IV. Security by default | PASS (with one inherited deviation, see Complexity Tracking) | Quarantine, trust mode and approval semantics are unchanged (FR-003, FR-005, SC-005). The starter is never auto-approved. Informed consent comes before code runs (FR-004). Docker isolation follows the global setting. |
| V. TDD | PASS | Every task group starts with failing tests (tasks.md). |
| VI. Documentation hygiene | PASS | `docs/features/telemetry.md`, `docs/cli/activity-commands.md`, `docs/web-ui/dashboard.md`, `docs/features/connect-clients.md`, `docs/features/activity-log.md`, `oas/swagger.yaml`, plus a CLAUDE.md "Recent Changes" line per PR. |
| Core + tray split | PASS | The tray reads the offer and digest over REST and holds no state. All eligibility logic is in the core (FR-012). |
| Event-driven updates | PASS | Onboarding fact changes emit the existing `onboarding` SSE refresh. No polling is added; the Home card refreshes on the existing activity SSE. |
| DDD layering | PASS | The new `internal/onboarding` holds the pure domain logic (allowlist, eligibility, bucket math). It sits behind a small interface implemented by `internal/server` (catalog door, server list) and `internal/storage` (state). |

No violations, so Complexity Tracking is empty.

## Project Structure

### Documentation (this feature)

```text
specs/119-day-two-activation/
├── spec.md          # approved spec (Sol r3)
├── plan.md          # this file
├── research.md      # R1-R12 decisions
├── data-model.md    # persisted and wire shapes, transitions
├── quickstart.md    # live checks L1-L5 + E3 telemetry queries
├── contracts/
│   ├── onboarding-starter.openapi.yaml
│   ├── activity-digest.openapi.yaml
│   └── telemetry-day-two.schema.json
├── checklists/requirements.md
└── tasks.md         # /speckit.tasks output
```

### Source code (repository root)

```text
internal/onboarding/                 # NEW pure domain package (PR-A facts math, PR-B starter)
├── starter.go                       # allowlist (R1), Candidate, Select()
├── starter_test.go                  # static audit test (FR-002), selection order, fail-closed cases
├── eligibility.go                   # Eligibility(inputs) → (reason, preselect); launcher enum
├── facts.go                         # sticky-bool merge, day-0 bucket, first-indexed-after bucket
└── facts_test.go
internal/shellwrap/shellwrap.go      # + ResolveOnEffectivePATH (R2)
internal/storage/
├── models.go                        # OnboardingState additions (data-model §1)
├── bbolt.go                         # install anchor on first Get; observeConnectedClients helper
├── activity.go                      # coverage watermark in the 3 prune txs (R9)
├── activity_digest.go               # NEW DigestAccumulator, BlockClass, brokered predicate (R6-R8)
└── activity_digest_test.go          # + testdata/digest_fixture.json, digest_expected.json
internal/telemetry/
├── telemetry.go                     # SchemaVersion 15, DayTwoPayload, SetDayTwoProvider
├── activation.go                    # first_tool_indexed_ever / _at, MarkFirstToolIndexed
├── anonymity.go                     # scanDayTwo + activation enum rule
├── day_two.go                       # NEW summary-view counter + bucketing
└── payload_v15_test.go              # NEW; schema pins moved 14→15 (R11 list)
internal/runtime/
├── absent_tool_removal.go           # post-success non-blocking wake (R4)
├── onboarding_facts.go              # NEW onboardingFactsLoop: observe clients, reconcile live/indexed, abandonment
├── tool_quarantine.go               # entered_hold / hold_kind on emits (R8)
└── activity_service.go              # metadata passthrough
internal/server/
├── server.go                        # providers wiring; EnsureInstallAnchor at startup; onboardingFactsLoop start
├── add_from_registry.go             # split into resolveRegistryEntry + addResolvedRegistryEntry (public door unchanged)
├── mcp.go                           # typed BlockReason persisted; metadata.dispatched on every tool_call completion (R6, R7)
└── onboarding_starter.go            # NEW: resolve once, validate snapshot, addResolvedRegistryEntry, record facts
internal/httpapi/
├── onboarding.go                    # GET/POST /onboarding/starter; mark booleans; observe clients; day_two in state
├── activity.go                      # digest + view param + scope rules (FR-023)
└── connect.go                       # observeConnectedClients on connect/disconnect
internal/contracts/{onboarding.go,activity.go}   # StarterOffer, ActivityDigest
internal/profile/testdata/contract/{labels,refusals}.json  # new BlockReason labels (R7)
cmd/mcpproxy/activity_cmd.go         # --since alias, view=cli, digest table
frontend/src/
├── components/onboarding/StarterOfferCard.vue      # NEW (wizard + Home variants)
├── components/onboarding/SecondClientSection.vue   # NEW
├── components/WeeklyDigestCard.vue                 # NEW
├── components/OnboardingWizard.vue                 # Servers/Clients/Verify integration
├── views/Home.vue                                  # starter offer + digest card
├── stores/onboarding.ts, services/api.ts, types/api.ts
frontend/tests/unit/{starter-offer-card,second-client-section,weekly-digest-card,onboarding-wizard-starter}.spec.ts
native/macos/MCPProxy/MCPProxy/
├── API/APIClient.swift, API/Models.swift           # starter offer, digest, ActivitySummary catch-up
├── Views/HomeView.swift + Views/StarterOfferCard.swift (NEW)
├── Views/ConnectClientView.swift                   # success-state "Connect another client"
└── Menu/Glance/GlanceSection.swift                 # weekly line
native/macos/MCPProxy/MCPProxyTests/{StarterOfferTests,WeeklyLineTests,ConnectAnotherClientTests}.swift
oas/swagger.yaml; docs/** (see Constitution VI)
```

**Structure decision**: the existing layout, plus one new pure package, `internal/onboarding`. It keeps the eligibility, allowlist and bucket rules out of `httpapi`, so the Web and native surfaces share them through REST, and the rules are unit-testable without a server.

## Design Notes

### D1. Starter flow sequence (wizard)

```
Web/native                         core
GET /onboarding/starter  ───────▶  onboarding.Select(effective registries, PATH, servers, quarantine state)
                                   + record starter_eligibility / starter_launcher (first fresh check)
render card (command, isolation)
POST /onboarding/mark {starter_offered}
click "Add and inspect"
POST /onboarding/starter {kind, surface, shown_command} ─▶ requireServerOp(AddFromRegistry)
                                   entry := resolveRegistryEntry("reference", id)   // once
                                   validate entry (builtin/reference, audited cmd == shown_command) else 409 starter_changed
                                   addResolvedRegistryEntry(ctx, entry, {})          // the door's add step, unchanged
                                   record added/kind/surface/server_name/incarnation; wake facts loop
poll GET /servers/{name}/review (2 s, ≤90 s or 180 s docker; SSE servers.changed also triggers)
render captured tools; diff check vs expected_tools (FR-005 notice)
click "Approve and enable" → POST /servers/{name}/security/approve {expected_hashes, expected_incarnation}
                                   (Spec 117 path; 409 review_out_of_date → reload)
index commit → wake → onboardingFactsLoop reconcile → starter_live, first_tool_indexed_ever
Verify step: tool count + first prompt
```

### D2. Fact evidence, the facts loop and Spec 117 locks (research R4, R5, R5a)

**Producers** run at the commit sites and never touch storage:
- After `Runtime.batchIndexTools` (`absent_tool_removal.go:150-157`) succeeds, the discovery apply passes its own incarnation in, and the producer records `LoadOrStore((index, server, incarnation), now)`. Its callers are `lifecycle.go:1753,1946,1967,1982`, under the Spec 117 per-server approval lock.
- At capture completion (`lifecycle.go:~1066`, after both failure returns), it records `LoadOrStore((capture, server, incarnation), now)`.
- Either producer then makes a non-blocking send on a size-1 wake channel.

**`onboardingFactsLoop`** is the only fact writer. It wakes on the channel, after the starter POST, on a 10-minute tick and at startup. On each pass it:
- copies the evidence map;
- reads the connect service outside any transaction;
- applies one pure `UpdateOnboardingState` merge. Inside it, the starter association `(name, incarnation)` is compared with the evidence **in the transaction**;
- writes activation;
- deletes the evidence entries only after a successful write. On failure it retries on the next tick.

It runs whatever the telemetry state is and drains once on shutdown, bounded to 2 s.

**Starter identity** comes from the create commit's `CommitResult` incarnation, threaded out of `addResolvedRegistryEntry` (`config_cow.go:70-76` discards it today).

**Tests**:
- no storage write under the approval lock (`commit_testhooks.go`);
- index commit then purge before the loop runs: the fact still lands, at the commit time;
- evidence before registration;
- interleaved same-name replacement: no false live;
- partial capture failure: no capture evidence;
- write failure retries.

### D3. Digest single pass (R6-R8)

`DigestAccumulator.Add(rec)` is called inside the existing `for a := range StreamActivities(filter)` loop in `handleActivitySummary`. It keeps:
- per-client counts;
- a set of blocked policy-decision request IDs;
- the error tool_calls' request IDs, pending resolution;
- a `(server, tool)` set for hold entries;
- a legacy-hold flag;
- sensitive-data and blocked-class counts.

Brokered is `tool_call && metadata.dispatched == true` (research R6). A pre-dispatch blocked `policy_decision` counts in `blocked`. A `tool_call` with `dispatched == false` and no decision for its `request_id` counts once, classified by `BlockClass` on its own record, for example a sandbox profile refusal or a limiter shed. This needs a buffered ID set resolved in `Finish`. Post-dispatch blocks (`output_sanitisation`, `output_schema`) count in `responses_blocked`. `Finish(scope)` It also adds `currently_awaiting` from `Runtime.GetReviewQueue`, filtered by `filter.AllowedServers`, and coverage from the watermark. `extractSensitiveDataInfo` is exported as `SensitiveDataDetected(rec)`. `Finish` also fills `window`: nominal start, `effective_start = max(start, install anchor)`, and the label kind and days. Every surface renders the window label from this one source.

### D4. Scope rules (FR-023)

Scoped callers already have `filter.AllowedServers` applied to the stream (`internal/httpapi/activity.go:161-170`), so every stream-derived figure is in scope. `currently_awaiting` is filtered by the same allowed set. When `filter.Profile`, `filter.ClientID` or `filter.TokenName` is set, `held_for_review` is returned with null figures and `reason: not_attributable`. Test: a scoped token on servers {a, b} with holds on c gives an identical response with and without c's holds.

### D5. Web surfaces

- `StarterOfferCard.vue` has two modes. `wizard` is pre-selected, with Skip; `home` has an explicit button and a compact-link variant after a decline. It owns the add → poll review → bound approve state machine (states `idle`, `adding`, `capturing`, `review`, `approving`, `live`, `failed`).
- Wizard tab completion is **unchanged**. The Servers tab completes only on `hasUsableServer` (`OnboardingWizard.vue:975-981`, backend `onboarding.go:328-336`), so a quarantined or uncaptured starter never completes it. Only an approved, indexed starter does. A vitest covers this.
- `SecondClientSection.vue` reuses `ClientConnectList` filtered to `detected_unconnected_clients` in a new **`per-client` mode**. This mode hides the bulk-connect footer and its loop (`ClientConnectList.vue:470-486,1275-1286`), so every write needs that client's own preview confirmation (FR-014). The preview → write → undo flow and the diff and backup notice come from the existing component. A vitest asserts that no bulk action renders in this mode.
- `SecondClientSection` and `WeeklyDigestCard` render only in the personal edition, guarded on the `/api/v1/status` `edition`. The digest endpoint itself is edition-neutral.
- `WeeklyDigestCard.vue` sits on Home between `AttentionList` and `UsageSummaryStrip`. It sends `view=web` once per mount when visible, then refreshes without `view` on activity SSE.

### D6. Native surfaces

- `StarterOfferCard.swift` mirrors the Web card's home mode. A new `StarterFlowModel` runs the same state machine as the Web card: capture wait, inline captured review, audited-inventory mismatch notice, Retry/Remove and quarantine-off ready. It never offers the blind approval that the ordinary native review allows before capture (`ReviewQueueView.swift:244-254`). The full native review opens only on request. Today that screen sends `expected_hashes` but **no** `expected_incarnation` (`API/APIClient.swift:290-298`). PR-E adds incarnation decoding to the native review model and passes it from `securityApproveServer` (research R3). A remove and re-add before approval must return 409 `server_replaced`.
- `ConnectClientView` success state: the list comes from `GET /onboarding/state`'s `detected_unconnected_clients`, mapped through the existing connect client list. The success state sends the `second_client_offered`, `second_client_none_detected` and `second_client_skipped` marks through a new `markOnboarding` method on the data-source seam.
- `GlanceSection` weekly line: `activitySummary(period: "7d", view: nil)` is fetched on the existing glance refresh. `menuWillOpen` sends one `view=tray` request, debounced by the core.
- The `ActivitySummary` Swift model adds `call_count`, `call_error_count`, `rejected_count`, `other_count` and an optional `digest`. Older cores omit them, so they decode as optional.

### D7. PR boundaries and order

| PR | Contains | Depends on | Ships |
|---|---|---|---|
| PR-A | facts, facts loop, telemetry, docs (tasks.md Phases 1-2) | none | first, alone if the calendar allows (R12) |
| PR-B | starter core + Web (tasks.md Phase 3) | PR-A | next release |
| PR-C | second client Web (tasks.md Phase 4) | PR-A | with PR-B |
| PR-D | digest core + Web + CLI (tasks.md Phase 5) | PR-A for view counting; it otherwise stands alone | with PR-B |
| PR-E | native (tasks.md Phase 6) | PR-B, PR-C, PR-D | same release if ready, otherwise the next | Merge each PR on its own, with no stacking and at most two reviews in flight (memory: merge early, don't stack). Each PR gets the OpenCode Sol 6.1 review loop, with the 10-round cap.

### D8. Risks

| Risk | Mitigation |
|---|---|
| New `BlockReason` values break the Spec 108 refusal and label parity contracts or Activity filters | Update the contract JSON and labels in PR-D. Run the profile contract tests and the Activity view vitest. |
| A completion path forgets to set `dispatched` | A table test drives every `tool_call` completion exit in `mcp.go` (pre-dispatch validation, missing client, generation refusal, success, upstream error, both output blocks, strict output schema) and asserts the key. Legacy-only windows report `coverage: partial`. |
| An index or capture commit happens and its documents are purged before the loop runs | Commit-time evidence is retained in memory until it is persisted (research R4). A purge-before-consume test covers this. |
| The registry changes between the consent check and the add | One resolved entry snapshot is validated and passed to the add step (research R3). A swap-between-steps test covers it. |
| The native approval binds to the wrong server incarnation | PR-E sends `expected_incarnation` (research R3). A remove-and-re-add test covers it. |
| The launcher is resolvable but the first `uvx` download is slow or offline | 90 s/180 s capture timeout, Retry/Remove, and `capture_timeout` recorded (SC-003 diagnostic). |
| A starter package release adds a tool | FR-005 "differs from reviewed version" notice. The user approves what was captured (Spec 117 binding). |
| Users never approve (the approval click becomes the new drop-off) | `approval_abandoned` is measured. If it is high, O1/O4 and copy changes are the next experiment. Auto-approval is never added. |

## Complexity Tracking

| Violation | Why needed | Simpler alternative rejected because |
|---|---|---|
| Constitution IV says "stdio MCP servers MUST run in Docker containers by default". The product default is `docker_isolation.enabled: false` (`internal/config/config.go:1644-1646`, "for backward compatibility"), so the starter runs as a host process by default. | **Pre-existing deviation, not introduced here.** The starter follows the global isolation setting, exactly like every catalog add (FR-003). The consent card shows the isolation mode before anything runs (FR-004). Both candidates are audited as pure-computation tools (FR-002). | Forcing Docker for the starter alone would need Docker on a fresh machine. That defeats "no Docker needed" and adds a starter-specific trust path that FR-003 forbids. Flipping the global default is a product decision outside E3; it is filed as follow-up O5 in the PR body. |

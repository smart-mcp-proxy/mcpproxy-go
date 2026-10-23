# Research: Day-Two Activation (Spec 119)

Every decision below resolves a design unknown from spec.md. Code references are to `origin/main` at `9dfdb2f19`.

## R1. Which starter, and how it is resolved

**Decision**: a compiled-in allowlist `internal/onboarding/starter.go`:

| Order | Kind | Catalog ID | Audited command | Expected tools |
|---|---|---|---|---|
| 1 | `time` | `reference/time` | `uvx mcp-server-time` | `get_current_time`, `convert_time` |
| 2 | `sequentialthinking` | `reference/sequentialthinking` | `npx -y @modelcontextprotocol/server-sequential-thinking` | `sequentialthinking` |

Each entry also records `audited_version`, the package version the maintainer reviewed, and a fixed `first_prompt`.

A candidate is selected only if all of these hold:
1. The effective `reference` registry has protocol `builtin/reference`. A config entry with the same ID overrides it (`internal/registries/registry_data.go:9-14`).
2. `registries` resolves the catalog ID to exactly the audited command and args.
3. Its launcher resolves (R2).
4. No configured server uses its name.
5. No configured server already runs the same command.

**Rationale**: the reference catalog is compiled in (`internal/registries/reference.go:26-74`) and has no required inputs. Docker isolation is off by default (`internal/config/config.go:1644-1646`). Both candidates are pure computation: no file, environment or network access at call time.

**Alternatives rejected**:
- `everything`: its live inventory includes `get-env`, which returns all environment variables, plus LLM-sampling test tools.
- `fetch`: open-world URL fetching.
- `filesystem` and `git`: they read files.
- `memory`: it writes a file.
- An embedded in-process starter: deferred to O1, because it is a new server kind with its own lifecycle verbs (lesson MCP-2396).
- A version-pinned command: deferred to O4, because it would change the shared catalog entry.

## R2. Launcher resolution without spawning

**Decision**: a new `shellwrap.ResolveOnEffectivePATH(logger, name string) (string, bool)`. It runs an `exec.LookPath`-style search over `shellwrap.LoginShellPATH(logger)` (`internal/shellwrap/shellwrap.go:455`, cached) followed by `os.Getenv("PATH")`. On Windows it searches the process PATH only, matching what `LoginShellPATH` returns there. The result is cached for 60 s in the starter service.

**Rationale**: stdio spawning wraps non-Docker commands in the login shell (`internal/upstream/core/connection_stdio.go:456` → `WrapWithUserShell`), so the login-shell PATH is the ground truth. `hasCommand` (`connection_stdio.go:474`) uses only the process PATH, which is degraded for GUI launches on macOS.

**Alternatives rejected**:
- Spawning `uvx --version`: slow, has side effects, and counts as running code before consent.
- Process PATH only: wrong for Finder-launched cores.

## R3. Starter add door, authorization and consent binding

**Decision**:
- `GET /api/v1/onboarding/starter` is wrapped in `requireAdminRead`.
- `POST /api/v1/onboarding/starter` is wrapped in the **same** authorization as the catalog door: `requireServerOp(auth.ServerOpAddFromRegistry, …)` (`internal/httpapi/server.go:1258`; agent tokens are denied by `internal/auth/server_ops.go:59-79`).
- Routed agent-token tests assert 403 with **no** resolution, add or fact write (plan review r1 #1).

**Atomic consent binding** (plan review r1 #2). `AddServerFromRegistry` (`internal/server/add_from_registry.go:88`) is split into two steps:
1. `resolveRegistryEntry(registryID, serverID) (*registries.ServerEntry, provenance, error)`, the current `:93-99` logic;
2. `addResolvedRegistryEntry(ctx, entry, req)`, which runs the current `:100-141` build-and-add steps unchanged.

The public catalog door calls both in sequence, so its behaviour is unchanged. The starter handler calls `resolveRegistryEntry` **once**, validates that snapshot (`builtin/reference` provenance, audited command equal to `shown_command`), then passes the **same** entry to `addResolvedRegistryEntry`. No second resolution happens, so a registry hot-reload between the check and the add cannot change what runs. A test swaps the `reference` registry between the starter check and the add, through a test hook between the two calls, and asserts that the validated command is what gets configured.

- A mismatch with `shown_command` returns 409 `starter_changed` with a fresh offer, and nothing is added.
- `duplicate_name` from the add passes through as 400.

**Approval** uses the existing `POST /api/v1/servers/{id}/security/approve` with `expected_hashes` **and** `expected_incarnation` (`internal/httpapi/security_scanner.go:~377`), both taken from the displayed `GET /api/v1/servers/{id}/review` (`internal/httpapi/review.go:68`, `data.server.incarnation`).
- **Web** (`frontend/src/services/api.ts:1641`) already sends both.
- **Native does not**: `securityApproveServer` sends only the hashes (`native/macos/MCPProxy/MCPProxy/API/APIClient.swift:290-298`), and the core then binds to whatever incarnation is current at call time (`internal/httpapi/security_scanner.go:350-360`). PR-E therefore adds `expectedIncarnation` to `securityApproveServer` and to the native review model. It is captured with the displayed review and tested with a remove and re-add before approval, which must return 409 `server_replaced` (plan review r1 #3).

**Alternatives rejected**: clients calling `POST /registries/reference/servers/reference%2Ftime/add` directly. That loses the atomic consent check and the core-side fact recording.

## R4. "Starter live" and "first tool indexed": commit-time evidence, retained until persisted

**Decision**: "ever" facts come from **evidence captured at the commit that produced them**. The evidence is bound to the server incarnation and kept until it is persisted. A current-index read never derives them, apart from the one supplement noted below (plan review r1 #4/#5, r2 #15/#16).

- **Evidence producers** run at the commit site, which already knows the server and its incarnation:
  - **Index evidence**: after `Runtime.batchIndexTools` (`absent_tool_removal.go:150-157`) returns nil for a non-empty batch. The discovery apply that calls it (`lifecycle.go:1753,1946,1967,1982`) runs under the Spec 117 per-server approval lock, with Spec 116's incarnation-conditional writes. The apply passes its own incarnation into the hook.
  - **Capture evidence**: at the point where a quarantined review capture completes, right after `storeLastGoodLocked` on the review-capture path (`lifecycle.go:~1066-1068`). This is past both failure returns (`checkToolApprovalsHeld` and `pruneAbsentUndecidedApprovals`, `:1054-1063`). A partial capture that fails is never evidence (r2 #17).
- **Retention**: a `sync.Map` keyed by `(kind, server, incarnation)` keeps the **first** commit time per key. It uses `LoadOrStore`, so a later commit never moves the time. Storing is lock-free for producers, nothing is ever dropped, and there is no bbolt write under the approval lock. A non-blocking send on a size-1 wake channel then nudges the facts loop.
- **Consumer**: `onboardingFactsLoop` (R5a) drains a **copy** of the map and applies one `UpdateOnboardingState` callback plus one activation write. It deletes an entry only after the write succeeds. On a write failure the entry stays and is retried on the next tick. Inside the callback, which is the transaction-current state:
  - `first_tool_indexed_ever` and `first_tool_indexed_at` come from the **earliest** index evidence of any upstream server, at its commit time.
  - `starter_live` is set only if index evidence exists for exactly `(StarterServerName, StarterServerIncarnation)` as stored **in the same transaction**.
  - `starter_captured_at` is set from capture evidence for that same pair.
  - Evidence that matches no recorded starter is kept for 15 minutes, which covers evidence arriving before the starter POST records its registration (r1 #5). It is then discarded once `first_tool_indexed` has been persisted.
- **Supplement**: on an upgraded install with tools already indexed before this build, the loop sets `first_tool_indexed_ever = true` from the current index document count, read directly, with `first_tool_indexed_at` unknown. That gives `first_tool_indexed_after = ""`. `GetToolCount()` is never used, because of its cache and upstream fallbacks (`runtime.go:4061-4078`).
- **Creation identity** (r2 #16): `addResolvedRegistryEntry` returns the `CommitResult` incarnation of the create commit. Today `createServer` discards it (`internal/server/config_cow.go:70-76`), so it is threaded through an internal result type. The starter POST records that incarnation, so a post-add lookup can never pick up a replacement.
- **Shutdown**: one bounded final drain, at most 2 s. Evidence still in memory at a crash is lost. It is regenerated only if the server indexes again, which is accepted and documented.

**Tests (T007, T026)**:
- successful indexing, then a purge before the loop runs: the fact still lands, at the commit time;
- evidence before registration;
- interleaved remove → same-name re-add → index of B while A is the recorded starter: A is not live;
- a partial capture failure produces no capture evidence;
- a write failure retries and keeps the entry.

**Alternatives rejected**:
- A buffered event channel with drop-on-full: it loses facts, because discovery skips unchanged tools (`lifecycle.go:1807-1819`).
- Current-index reconciliation as the source: a purge loses history, and documents carry no incarnation (`internal/index/bleve.go:55-73`).
- Stamping in `index.Manager`: the index package has no storage dependency.

### R4a. Approval abandonment (plan review r1 #11, r2 #17)

`approval_abandoned` is set only when **all** of these hold:
- capture evidence for the recorded `(StarterServerName, StarterServerIncarnation)` has been persisted as `starter_captured_at`;
- `now - starter_captured_at >= 24h`;
- `!starter_live`;
- the starter, at the same incarnation, is still quarantined.

A starter that never completed a capture keeps its `connect_failed` / `capture_timeout` and is never reclassified.

## R5. Install anchor and client observation

**Decision (anchor, plan review r1 #6)**: `Storage.EnsureInstallAnchor(now)` runs **once at startup**, in a single `Update` transaction, **before** the HTTP server, the connect service or the MCP initialisation can write onboarding state.
- No record: it creates one with `InstallFirstRunAt = now`.
- A record exists without the field: the install is an upgrade, and the anchor stays zero forever.

As a safeguard, the "missing record" branch of `UpdateOnboardingState` (`internal/storage/bbolt.go:1283-1305`) also sets the anchor when it has to create the record. That covers tests and any writer that runs before startup init. The anchor is never back-filled on an existing anchorless record. Tests cover update-before-get, concurrent first writers, and upgrade.

**Decision (observation)**: `ClientFirstObservedAt map[string]time.Time` is updated by `observeConnectedClients(ids)`. The IDs are collected **outside** the transaction from `connect.Service.GetConnectedIDs` (`internal/connect/connect.go:371-393`), then merged inside the pure update callback, which never does I/O (`bbolt.go:1272-1276`). Callers:
- the connect and disconnect handlers (`internal/httpapi/connect.go:332-360`);
- `computeOnboardingState` (`internal/httpapi/onboarding.go:276`);
- the facts lifecycle tick (R5a), which does **not** depend on telemetry.

Only unseen IDs are added; existing entries are never overwritten. `second_client_connected` becomes true once there are at least two entries.

**Rationale**: `anonymous_id` rotates yearly (`internal/telemetry/telemetry.go:1864-1877`). Connect timestamps overwrite (`connect.go:332-360`). `connected_client_ids` is a live read.

**Caveat**: two clients observed at different times count as "connected" even if they were never configured at the same time. This is accepted, because the metric is "used a second client with MCPProxy".

### R5a. Telemetry-independent facts tick (plan review r1 #7)

A runtime background goroutine, `onboardingFactsLoop`, runs at startup (after `EnsureInstallAnchor`) and then every 10 minutes. Each tick runs client observation, `reconcileActivationFacts`, and the abandonment check (R4a). It runs whatever the telemetry state is: opt-out, non-semver dev builds, or before the first heartbeat (`internal/telemetry/telemetry.go:974-1025`). The heartbeat only **reads** the persisted facts.

## R6. Digest: brokered-call predicate (plan review r1 #8, #9)

**Decision**: persist an explicit **dispatch fact** and do not infer it from the fault domain.
- On every `tool_call` completion record, the server sets `Metadata["dispatched"] = true|false`. The value comes from the existing per-request dispatch evidence: `noteUpstreamDispatched` (`internal/server/mcp.go:3346`), and `callerr.Facts.Dispatched` from the transport recorder (`internal/callerr/classify.go:73-80`).
- Pre-dispatch exits record `dispatched=false`. Examples are argument validation (`mcp.go:3221-3226`), missing or disconnected clients (`:3246-3250,3319-3323`), and the review-not-recorded or generation refusals (`:3446-3475`).
- **Limiter sheds** (r2 #18) bypass the normal completion emitter (`mcp.go:3412-3417`). Their record is built in `ActivityService.RecordToolCallRejected` (`internal/runtime/activity_service.go:555-576`), which also stamps `dispatched=false`. Direct and sandbox sheds are both tested: each counts once in blocked (`other`), zero in brokered, and causes no legacy-coverage downgrade.
- **Sandbox sub-call refusals** (`internal/server/audit_funnel.go:517-526`, `mcp_code_execution.go:1289-1299`) record `status=blocked` with a typed profile `BlockReason` and `dispatched=false`.
- **Brokered call**: `Type == tool_call && Metadata.dispatched == true`, with any status.
- **Legacy records** without the key are counted with the old heuristic (`status == success`) and set `calls_brokered.coverage = partial` until the window holds only new-format records.
- **Sensitive data** counts brokered calls only.

**Post-dispatch blocks** (r1 #9). `emitActivityPolicyDecisionWithBlockReason` already distinguishes them (`isPostDispatchBlockKey(reasonKey)`, `internal/server/mcp.go:991-998`: `output_sanitisation`, `output_schema`). R7 persists `reasonKey` as the typed `BlockReason`, so the phase is derivable:
- a **pre-dispatch** blocked `policy_decision` counts in `blocked.total` and its class;
- a **post-dispatch** block counts in a new `blocked.responses_blocked` and **not** in `blocked.total`, but **only** when its `request_id` pairs with a `tool_call` that has `dispatched=true`. The call itself stays brokered. `prompts/get` emits the same `output_sanitisation` reason after an upstream prompt fetch (`internal/server/output_sanitisation.go:286-289`, `mcp.go:1124-1132`) with no `tool_call`, so it never pairs and never counts (plan review r4 #9). A blocked-prompt fixture asserts zero contribution to every digest figure.

The strict output-schema exit (`mcp.go:3610-3612,3653`) must also emit the canonical `tool_call` completion with `dispatched=true`; a test covers both output-block paths.

Blocked-total dedupe: a pre-dispatch refusal that left both a `tool_call` (`dispatched=false`) and a `policy_decision` counts once, via the `policy_decision`. A `tool_call` with `dispatched=false` and **no** paired decision (same `request_id`) counts once and is classified by `BlockClass(rec)` on its own record: a sandbox profile refusal is `profile`, and a limiter shed is `other`. It defaults to `other` only when the record has no classified reason (r2 #19).

**Alternatives rejected**: `FaultDomain`. Fault ownership is not dispatch: argument rejection is `client` and a missing client is `upstream` (`internal/callerr/classify.go:97-106`).

## R7. Digest: blocked reason classes

**Decision**: persist a typed reason on every refusal record.
- `emitActivityPolicyDecisionWithBlockReason` (`internal/server/mcp.go:989-1009`) writes `reasonKey` into `ActivityRecord.BlockReason` whenever its `blockReason` argument is empty. Today the key is dropped.
- **Sandbox quarantine and approval-lock refusals** (plan review r3 #11) compute `policyRefusalReasonKey(gate)` for the audit line but pass an empty reason to `emitSubCallRefused` (`internal/server/mcp_code_execution.go:958-965`). They now pass that key, so a sandbox quarantine refusal classifies as `quarantine` exactly as its direct-call twin does. Fixtures pair direct and sandbox refusals for server quarantine, pending approval and changed approval, and assert the same class. The keys come from `internal/telemetry/preflight_counters.go:45-56` plus the post-dispatch keys `output_sanitisation` and `output_schema`.

Mapping table (`internal/storage/activity_digest.go`, `BlockClass(rec)`):

| `BlockReason` / record | Class |
|---|---|
| `server_quarantined`, `tool_pending_approval`, `tool_changed_approval` | `quarantine` |
| `profile_*` (`internal/profile/contract.go:56-64`), `profile_scope` | `profile` |
| `token_scope`, `token_permission`, `tool_not_callable`, any other value, empty | `other` |
| `Status == rejected` (limiter) | `other` |
| `output_sanitisation`, `output_schema` (post-dispatch) | `responses_blocked`, which is not in the total |

**Rationale**: FR-020 forbids free-text matching (`Metadata["reason"]`).

**Risk**: the new `BlockReason` values become visible in the Activity UI and the Spec 108 contracts (`internal/profile/testdata/contract/{labels,refusals}.json`). PR-D adds labels and updates the parity tests in the same PR.

## R8. Digest: held-for-review entries (plan review r1 #10)

**Decision**: instrument the actual **not-held → held** transitions.
- `emitToolQuarantineEvent` (`internal/runtime/tool_quarantine.go:3363`) gains `enteredHold bool, holdKind string`, which become activity metadata. Each call site passes values computed from the record's **previous** and **new** status, plus whether quarantine is in effect for the server.

| Site | Values |
|---|---|
| New record saved as pending, quarantine in effect (`:917-954`) | `true`, `new` |
| A status transition approved → changed (the description-change sites), quarantine in effect | `true`, `changed` |
| The same transition while quarantine is off (`:1582-1594`) | `false` |
| `refreshHeldContractSnapshot` (`:380-386,451-458`), already changed | `false` |
| `adoptLegacyLockOrBaseline`, where it **creates** a pending hold in the failed-scan and manual branches (`:2150-2167`). Today these emit nothing; a new emit is added | `true`, `new` |
| Legacy lock carry-over (`:2177-2194,2211-2225`, including the `:2262` emit), which preserves an existing pending or changed lock | `false` |
| Toggle path that synthesises a pending record (`:2721-2765`) | adds `tool_discovered` with `true`, `new` when it creates the pending record |
| `tool_auto_approved`, `tool_approved`, `tool_blocked` | `false` |

- A table-driven test enumerates every `emitToolQuarantineEvent` call site by grep count, so a future site cannot be added without a decision.
- Fixtures cover both adoption helpers, carry-over, refresh of an already-changed tool, quarantine off, and enter → approve → re-enter.
- Digest coverage for this figure is `partial` while the window still contains `tool_quarantine_change` records without the field.

**Alternatives rejected**: deriving from `ToolApprovalRecord.PendingSince` / `DefinitionChangedAt`. These are current-state fields (`internal/storage/bbolt.go:479-501`).

## R9. Coverage watermark

**Scope** (plan review r4 #8): the watermark is installation-wide, so pruning of a disallowed server's history would leak through it. Only **admin** callers get `coverage.state` and `complete_since` from it. A scoped caller (`AllowedServers` set) gets `coverage: {state: "unavailable", complete_since: null}`, which never depends on records outside its scope. Test: identical A/B activity with different C history (including a prune of C and watermark initialisation from a C record) gives byte-identical digests for an A/B-scoped token.

**Decision**: a new key `activity_complete_since` in the existing activity meta area (`internal/storage/activity.go`). `PruneOldActivities` (`:342`), `PruneExcessActivities` (`:393`) and `PruneActivitiesToSize` (`:451`) each set it to the timestamp of the newest deleted record + 1 ns, inside their own delete transaction, and only ever move it forward. On first open by this build, if the key is absent, it is initialised to the oldest surviving record's timestamp, or to "now" for an empty log.

**Rationale**: FR-021. Pruning deletes oldest-first in every mechanism, so "newest deleted" is a correct completeness boundary.

## R10. Summary views and the `view` parameter

**Decision**:
- `GET /api/v1/activity/summary` accepts an optional `view=web|tray|cli`. Any other value is ignored, with no error, to stay compatible.
- The handler calls `telemetry.RecordSummaryView(surface)`. This is an in-memory counter with a per-surface last-counted time (1 h debounce), drained into `day_two.summary_views` on an accepted send, like other delta counters (`internal/telemetry/registry.go:449-621`).
- Web sends `view=web` only when the Home card mounts with `document.visibilityState === 'visible'`, not on its 5-minute refresh. The tray sends `view=tray` only from `menuWillOpen`. The CLI always sends `view=cli`.

**Rationale**: one aggregation path (A6). No client-side telemetry.

## R11. Telemetry schema 15

**Decision**:
- `SchemaVersion = 15` (`internal/telemetry/telemetry.go:163`), with a change-log paragraph.
- `ActivationState` gains `FirstToolIndexedEver` (`activationKeyFirstToolIndexedEver`) and a derived `FirstToolIndexedAfter` bucket. The latter is computed at payload build from the stored `first_tool_indexed_at` and the install anchor, and is never stored as a timestamp on the wire.
- A new `DayTwoSnapshot` provider, mirroring `SetOnboardingProvider` (`telemetry.go:863`), builds `HeartbeatPayload.DayTwo *DayTwoPayload` (personal edition only; nil on server). The `day_two` keys are omitted when nil.
- `ScanForPII` gains `scanDayTwo`, modelled on `scanTrustModeDistribution` (`internal/telemetry/anonymity.go:317-357`). It enforces a closed key set, closed enums, booleans only for flags, and a closed `summary_views` object. It also covers `activation.first_tool_indexed_after`.
- Every schema-14 pin moves to 15. The list is from `grep -rnE 'schema_version.{0,3}14|want 14' internal/telemetry docs`: `telemetry_test.go:315,323`, `payload_v2_test.go:91,138`, `payload_v7_test.go:11,20,81,112`, `payload_privacy_test.go:147`, `tpa_scanner_test.go:267`, `current_error_codes_test.go:268`, `tpa_funnel_v9_test.go:263`, `docs/features/telemetry.md:13,166-175`.

**Rationale**: this follows the house convention for every schema bump. The receiver stores payloads wholesale, so unknown fields are accepted (`docs/features/telemetry.md:~348`).

## R12. Release order: measure before exposing

**Decision**: PR-A, which carries telemetry and the onboarding facts, merges first and ships in a release before the starter UI. This gives one release of schema-15 baseline data for `clients_day0` and `first_tool_indexed_ever` (spec SC-004).

**Rationale**: SC-004's richer diagnostic has no pre-release baseline otherwise. The primary metrics (SC-001, SC-002) use v14-comparable instruments and do not depend on this.

**Risk accepted**: if the starter and PR-A must ship in the same release because of the strategy calendar ("E3 shipped in the next release", W5-W8), the diagnostic baseline comes from the first release cohort. The plan allows that order.

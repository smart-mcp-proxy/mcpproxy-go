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

## R3. Starter add door and consent binding

**Decision**: a new `POST /api/v1/onboarding/starter` with body `{kind, surface, shown_command}`. The handler:
1. re-runs candidate resolution (R1);
2. answers 409 `starter_changed` with a fresh offer if `shown_command` differs from the resolved command;
3. otherwise calls the existing `Server.AddServerFromRegistryRef(ctx, "reference", catalogID, "", nil, nil)` (`internal/server/add_from_registry.go:150`) with no extra fields;
4. records `starter_added`, `starter_kind`, `starter_surface`, `starter_added_at` and `starter_server_name`.

Approval uses the existing `POST /api/v1/servers/{id}/security/approve`, with `expected_hashes` and `expected_incarnation` (`internal/httpapi/security_scanner.go:~377`), taken from `GET /api/v1/servers/{id}/review` (`internal/httpapi/review.go:68`).

**Rationale**: FR-003 requires the same door, with no override. A dedicated endpoint wraps it so that the consent check (FR-003 re-resolve) and fact recording live in the core (FR-012), never in clients. Approval needs no new code: Spec 117 binding is already in Web (`frontend/src/services/api.ts:1641`) and native.

**Alternatives rejected**: clients calling `POST /registries/reference/servers/reference%2Ftime/add` directly. That loses the consent re-check and leaves each surface to record facts.

## R4. When is the starter "live" and when was a tool first indexed

**Decision**: the only runtime index write wrapper is `internal/runtime/absent_tool_removal.go:156` (`indexManager.BatchIndexTools`). After it returns nil with a non-empty batch, the runtime calls `recordToolsIndexed(serverNames)` **after the per-server approval lock is released** (deferred to the caller's unlock path or a non-blocking channel to the activation recorder). It does two things:
- marks `activation.first_tool_indexed_ever` and `first_tool_indexed_at`, once;
- sets `starter_live` when a batch contains a tool of `starter_server_name`.

**Rationale**: the spec forbids stamping from `GetToolCount()`, which has cache and upstream fallbacks (`internal/runtime/runtime.go:4061-4078`). Spec 117 PR-3 holds the per-server lock across index writes, so a bbolt write must not run inside it, to avoid lock-order growth. The activation bucket write follows `RecordRealToolCallForActivation` (`runtime.go:3946`).

**Alternatives rejected**:
- Polling the tool count in the heartbeat: it misses brief indexing between heartbeats and cannot attribute the starter.
- Stamping in `index.Manager`: the index package has no storage dependency.

## R5. Install anchor and client observation

**Decision**:
- `OnboardingState.InstallFirstRunAt` is set by the onboarding store's first `Get` that finds **no** `wizard_state` key (`internal/storage/bbolt.go:1221`), before the first save. If a record exists without the field, this is an upgraded install, and the anchor stays zero forever.
- `ClientFirstObservedAt map[string]time.Time` is updated by `observeConnectedClients(ids)`, which is called from three places:
  - `computeOnboardingState` (`internal/httpapi/onboarding.go:276`);
  - the connect and disconnect handlers (`internal/httpapi/connect.go:332-360`);
  - the onboarding telemetry provider (`internal/server/server.go:3234`).

  It adds unseen IDs only and never overwrites an existing entry. The four second-client booleans are derived on write: `second_client_connected` becomes true once `len(ClientFirstObservedAt) >= 2`.

**Rationale**: `anonymous_id` rotates yearly (`internal/telemetry/telemetry.go:1864-1877`). The connect timestamps overwrite (`connect.go:332-360`). `connected_client_ids` is a live read (`internal/connect/connect.go:371-393`), so persistence must happen at observation time.

**Caveat**: two clients observed at different times count as "connected" even if never configured at the same time. This is accepted, because the metric is "used a second client with MCPProxy", not concurrency.

## R6. Digest: brokered-call predicate

**Decision**: in one stream pass over the window, a record is a **brokered call** when all of these hold:
- `Type == tool_call`;
- `Status == success`, or (`Status == error` **and** `FaultDomain != "proxy"`);
- `RequestID` is not in the set of request IDs that have a `policy_decision` with `Status == blocked` in the window.

That set is collected in the same pass. Records are not sorted by type, so the pass buffers the error tool_calls' request IDs and resolves them at the end. The buffer is bounded by the error count.

Every refusal site that records a `tool_call` error without dispatch must set `FaultDomain = "proxy"`. An example is `internal/server/mcp.go:3446-3475`, `isReviewNotRecordedRefusal` / `ErrConnectionGenerationChanged`. A test enumerates the known refusal sites (task T034).

**Rationale**: `storage.CountsAsCall` (`internal/storage/activity_call_population.go:79`) counts blocked policy decisions as failed calls. That is right for the 24h strip but wrong for "brokered". `FaultDomain` already exists (`internal/storage/activity_models.go:223`).

**Alternatives rejected**: a new `dispatched` boolean on every tool_call. The existing fault domain plus the request-ID dedupe covers it without a schema change.

## R7. Digest: blocked reason classes

**Decision**: persist a typed reason on every refusal record:
- `emitActivityPolicyDecision` (`internal/server/mcp.go:989-1009`) today drops the telemetry block-reason key. It now writes it into `ActivityRecord.BlockReason` when that field is empty.
- The keys come from `internal/telemetry/preflight_counters.go:45-56`: `server_quarantined`, `tool_pending_approval`, `tool_changed_approval`, `tool_not_callable`, `profile_scope`, `token_scope`, `token_permission`.

Mapping table (`internal/storage/activity_digest.go`, `BlockClass(rec)`):

| `BlockReason` / record | Class |
|---|---|
| `server_quarantined`, `tool_pending_approval`, `tool_changed_approval` | `quarantine` |
| `profile_*` (`internal/profile/contract.go:56-64`), `profile_scope` | `profile` |
| `token_scope`, `token_permission`, `tool_not_callable`, any other value, empty | `other` |
| `Status == rejected` (limiter) | `other` |

**Rationale**: FR-020 forbids matching on free text (`Metadata["reason"]`).

**Risk**: new `BlockReason` values become visible in the Activity UI and the Spec 108 refusal and label contracts (`internal/profile/testdata/contract/{labels,refusals}.json`). The plan adds labels for the three quarantine values and `token_*`. Parity tests are updated in the same PR.

## R8. Digest: held-for-review entries

**Decision**: `emitToolQuarantineEvent` (`internal/runtime/tool_quarantine.go:3363`) gains two parameters, `enteredHold bool` and `holdKind string`, which become activity metadata.

| Emitting path | Values |
|---|---|
| New pending record when quarantine is in effect (`:917-954`) | `entered_hold=true`, `hold_kind=new` |
| Approved → changed transition (`:1591`, `:2131`, `:2262`) | `entered_hold=true`, `hold_kind=changed`, but only when quarantine is in effect for the server (`:1582-1594` emits even when it is off) |
| `refreshHeldContractSnapshot` (`:380-386,451-458`), already changed | `entered_hold=false` |
| The toggle path that synthesises a pending record (`:2721-2765`) | today emits only enable/disable. It emits an additional `tool_discovered` with `entered_hold=true`, `hold_kind=new`, when it creates a pending record |
| `tool_auto_approved`, `tool_approved`, `tool_blocked` | `entered_hold=false` |

Digest coverage for this figure is `partial` while the window still contains `tool_quarantine_change` records without the field.

**Alternatives rejected**:
- Deriving from `ToolApprovalRecord.PendingSince` / `DefinitionChangedAt`. These are current-state fields (`internal/storage/bbolt.go:479-501`) and lose history.

## R9. Coverage watermark

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

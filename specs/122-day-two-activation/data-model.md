# Data Model: Day-Two Activation (Spec 122)

All persisted additions are additive and `omitempty`. An older binary ignores them (downgrade-safe), and a newer binary treats their absence as "unknown" or "false".

## 1. `storage.OnboardingState` additions (bbolt bucket `onboarding`, key `wizard_state`)

Base struct: `internal/storage/models.go:102`.

| Field | JSON | Type | Written by | Rules |
|---|---|---|---|---|
| `InstallFirstRunAt` | `install_first_run_at` | `time.Time` | `EnsureInstallAnchor` at startup, before any other writer; also the missing-record branch of `UpdateOnboardingState` as a safeguard (research R5) | Set once and never changed. Zero means an upgraded install with no anchor; it is never back-filled. |
| `StarterEligibility` | `starter_eligibility` | `string` enum | the first starter eligibility evaluation on a fresh install | Set once. Values: `eligible`, `launcher_missing`, `name_taken`, `reference_overridden`, `already_configured`, `tools_present`, `held_tools`, `not_fresh`. |
| `StarterLauncher` | `starter_launcher` | `string` enum | every fresh-install evaluation | Values: `uvx`, `npx_only`, `none`. The first value is kept for telemetry (sticky). The live value is recomputed per request and not stored. |
| `StarterOffered` | `starter_offered` | `bool` | mark endpoint (`starter_offered: true`) | Sticky. Only false → true. |
| `StarterAdded` | `starter_added` | `bool` | `POST /onboarding/starter` success | Sticky. |
| `StarterLive` | `starter_live` | `bool` | facts loop from commit-time index evidence (research R4) | Sticky. Set only from index evidence for the recorded `(StarterServerName, StarterServerIncarnation)`, compared inside the update transaction. |
| `StarterDeclined` | `starter_declined` | `bool` | mark endpoint | Sticky. It stops pre-selection only. |
| `StarterLastFailure` | `starter_last_failure` | `string` enum | mark endpoint (`capture_timeout`, `connect_failed`) or core (`approval_abandoned`) | Overwritable, latest value. It never changes `StarterLive`. |
| `StarterKind` | `starter_kind` | `string` enum | first successful add | Set once. Values: `time`, `sequentialthinking`. |
| `StarterSurface` | `starter_surface` | `string` enum | first successful add | Set once. Values: `wizard`, `home_web`, `home_native`. |
| `StarterAddedAt` | `starter_added_at` | `time.Time` | first successful add | Set once. Local only, never sent. |
| `StarterServerName` | `starter_server_name` | `string` | each successful add | Local only, never sent. |
| `StarterServerIncarnation` | `starter_server_incarnation` | `int64` | each successful add, from the create commit's `CommitResult` (never a post-add lookup) | Local only. It binds the live and abandonment checks to the added server, so a same-name replacement never counts. |
| `StarterCapturedAt` | `starter_captured_at` | `time.Time` | facts loop from capture-completion evidence (research R4), at its commit time | Set once per starter incarnation. Local only. The abandonment clock (R4a). `definitions_captured` is **not** used, because it is true after a partial capture. |
| `SecondClientOffered` | `second_client_offered` | `bool` | mark endpoint | Sticky. |
| `SecondClientNoneDetected` | `second_client_none_detected` | `bool` | mark endpoint | Sticky. |
| `SecondClientSkipped` | `second_client_skipped` | `bool` | mark endpoint | Sticky. |
| `SecondClientConnected` | `second_client_connected` | `bool` | `observeConnectedClients` | Sticky. True once `len(ClientFirstObservedAt) >= 2`. |
| `ClientFirstObservedAt` | `client_first_observed_at` | `map[string]time.Time` | `observeConnectedClients` | Keys are restricted to registry client IDs (`internal/connect/clients.go`). Entries are added and never overwritten. Local only. |

### Derived values (not stored)

| Value | Derivation |
|---|---|
| `clients_day0` | Count of `ClientFirstObservedAt` entries whose time is in `[InstallFirstRunAt, +24h)`, bucketed `0 / 1 / 2 / 3_plus`. `""` when `InstallFirstRunAt` is zero. |
| `clients_day0_final` | `now >= InstallFirstRunAt + 24h`, when there is an anchor. |
| `approval_abandoned` | Checked on the telemetry-independent facts tick (R5a): `StarterCapturedAt` set (successful capture), `now - StarterCapturedAt >= 24h`, `!StarterLive`, and the starter (same incarnation) is still quarantined. Then `StarterLastFailure = approval_abandoned`. A starter that never captured keeps its capture or connect failure. |

## 2. `telemetry.ActivationState` additions (bbolt bucket `activation`)

Base: `internal/telemetry/activation.go:17-50`.

| Key / Field | Type | Rules |
|---|---|---|
| `first_tool_indexed_ever` | bool | Sticky. Written by `MarkFirstToolIndexed`. |
| `first_tool_indexed_at` | time, stored locally | Written once with the flag. **Never** on the wire. |
| `FirstToolIndexedAfter` (wire `first_tool_indexed_after`) | enum | Derived at payload build from `first_tool_indexed_at - InstallFirstRunAt`: `lt_10m`, `10m_1h`, `1h_24h`, `1d_7d`, `gt_7d`. `""` without an anchor or before the flag. |

## 2a. In-memory evidence (not persisted)

`factEvidence sync.Map[(kind ∈ {index, capture}, server, incarnation)] → first commit time`. Producers use `LoadOrStore`. The facts loop deletes an entry only after persisting it. Unmatched entries expire after 15 minutes (research R4).

## 3. Activity log additions

### 3a. `tool_quarantine_change` metadata (research R8)

| Key | Type | Values |
|---|---|---|
| `entered_hold` | bool | true only on a not-held → pending/changed transition with quarantine in effect |
| `hold_kind` | string | `new`, `changed`, or absent |

### 3b. `ActivityRecord.BlockReason` new values (research R7)

The new values are `server_quarantined`, `tool_pending_approval`, `tool_changed_approval`, `tool_not_callable`, `profile_scope`, `token_scope`, `token_permission`, `output_sanitisation` and `output_schema`. The last two are post-dispatch. They are the existing telemetry block-reason keys. Until now they were dropped by `emitActivityPolicyDecision`; they are now persisted when the record has no profile reason.

### 3c. Dispatch fact on `tool_call` completions

Every `tool_call` record carries `Metadata["dispatched"]` (bool), taken from the existing dispatch evidence (research R6). This covers the completion funnel, limiter sheds (`RecordToolCallRejected`) and sandbox sub-call refusals. Brokered = `dispatched == true`. Records without the key are legacy and count heuristically, with partial coverage.

### 3d. Coverage watermark

| Key | Location | Rules |
|---|---|---|
| `activity_complete_since` | activity meta | Moves forward only. Each prune advances it in its own delete transaction. Initialised on first open (research R9). |

## 4. Wire types

### 4a. `contracts.StarterOffer` (`GET /api/v1/onboarding/starter`)

All REST bodies use the standard `{"success": true, "data": …}` envelope (`internal/httpapi/server.go:1463`). The shapes below are the `data` member.

```json
{
  "eligible": true,
  "reason": "eligible",
  "preselect": true,
  "candidate": {
    "kind": "time",
    "catalog_id": "reference/time",
    "command": "uvx mcp-server-time",
    "isolation": "host",
    "expected_tools": ["get_current_time", "convert_time"],
    "first_prompt": "What time is it in Tokyo right now?"
  },
  "launcher": "uvx",
  "starter_server": null,
  "facts": {
    "offered": false, "added": false, "live": false,
    "declined": false, "last_failure": ""
  }
}
```

- `reason` is one of `eligible`, `launcher_missing`, `name_taken`, `reference_overridden`, `tools_present`, `held_tools`, `already_configured` or `server_edition`.
- `candidate` is null when no candidate passes.
- `isolation` is `host` or `docker`, taken from the effective isolation decision for that config.
- `starter_server` is `{name, quarantined}` once a starter is configured. Surfaces use it to resume the review step after a reload.

### 4b. `contracts.ActivityDigest` (`digest` in `ActivitySummaryResponse`)

```json
"digest": {
  "calls_brokered": {"total": 1284, "by_client": [{"client": "Claude Code", "count": 812}, {"client": "Codex", "count": 431}], "other": 41, "unknown": 0, "coverage": "complete"},
  "held_for_review": {"entered": 3, "new": 2, "changed": 1, "currently_awaiting": 2, "coverage": "complete", "reason": null},
  "blocked": {"total": 5, "quarantine": 4, "profile": 1, "other": 0, "responses_blocked": 1},
  "sensitive_data": {"calls_with_detections": 2},
  "coverage": {"state": "complete", "complete_since": "2026-09-01T00:00:00Z"},
  "window": {"start": "2026-10-13T12:00:00Z", "effective_start": "2026-10-17T09:30:00Z", "label": "since_install", "days": 3}
}
```

- `window` is the single shared source for the US4 scenario 6 label (plan review r3 #16). `start` is the nominal period start, the same value as the existing `start_time`. `effective_start = max(start, install anchor)`. `label` is `since_install` when `effective_start > start`, otherwise `period`. `days` is the whole number of days from `effective_start` to the end, rounded up. An anchorless (upgraded) install always gets `label = period`. Coverage is independent: a `period` window can still be `partial`.
- `blocked.responses_blocked` counts post-dispatch output blocks paired by `request_id` with a dispatched `tool_call`. Blocked prompt results never count. They are not in `blocked.total`, because those calls were brokered.
- `coverage` comes from the installation-wide watermark for admin callers only. Scoped callers get `{state: "unavailable", complete_since: null}`, so out-of-scope pruning cannot leak through it.
- `held_for_review` takes the form `{entered: null, ..., reason: "not_attributable"}` when an attribution filter is set (spec FR-023).
- `held_for_review.coverage` is `partial` while legacy records without `entered_hold` exist in the window.
- `new + changed` may exceed `entered`: a tool counts once in `entered` but may appear under both kinds if it entered twice with different kinds.

### 4c. `telemetry.DayTwoPayload` (`day_two` in the heartbeat, schema 17, personal edition only)

```json
"day_two": {
  "starter_eligibility": "eligible",
  "starter_launcher": "uvx",
  "starter_offered": true, "starter_added": true, "starter_live": true, "starter_declined": false,
  "starter_last_failure": "",
  "starter_kind": "time",
  "starter_surface": "wizard",
  "second_client_offered": true, "second_client_none_detected": false,
  "second_client_skipped": false, "second_client_connected": true,
  "clients_day0": "2",
  "clients_day0_final": false,
  "summary_views": {"web": "1", "tray": "0", "cli": "0"}
}
```

The enums are closed. The PII scan rejects unknown keys, unknown values, non-boolean flags, and any timestamp or name.

## 5. State transitions

### Starter (local)

```
not evaluated ──first fresh-install check──▶ eligibility recorded
offer rendered ──mark──▶ offered
"Add and inspect" ──POST /onboarding/starter──▶ added ──index hook──▶ live
                                                  │
                                                  ├─▶ last_failure = capture_timeout / connect_failed (UI mark), retry allowed
                                                  └─▶ last_failure = approval_abandoned (24 h, core)
Skip / uncheck ──mark──▶ declined (pre-selection off; Home shows a compact link)
```

All booleans are sticky. `live` dominates for success analysis, and failures are diagnostic only.

### Second client (local)

These four booleans are independent: `offered`, `none_detected`, `skipped` and `connected`. Only `connected` is derived by the core; the others are UI marks. None of them is ever cleared.

# Implementation Plan: Typed Pending-Call Outcome and Call Correlation

**Branch**: `114-pending-call-outcome` | **Date**: 2026-10-06 (revised after review rounds 1 and 2) | **Spec**: [spec.md](spec.md)
**Input**: Feature specification from `/specs/114-pending-call-outcome/spec.md`

## Summary

mcpproxy mints a `call_id` for every upstream `tools/call` and carries it in ctx with the principal and the gate tier (`pending.CallMeta`).

At `managed.Client.callTool`, after admission and the generation re-check, one registry operation runs. It checks the gate and creates a dispatch record (`CheckAndBegin`). This is the authoritative gate, and it covers every entry point, including activity replay.

For stdio, a transport decorator captures the typed JSON-RPC id and always runs the inner `SendRequest` detached, with a deadline fixed at dispatch. That way a response that arrives after the caller's deadline can still be observed.

When the caller's ctx ends after dispatch, the record becomes `unresolved`, unless a late response or an epoch bump has already decided it. The client's error keeps its legacy string and gains a typed `pending_call` block, with a per-surface envelope.

Each entry resolves in one of five ways:
- late completion (stdio only);
- per-entry reconciliation by a later read in the same gate scope, which must be explicitly `readOnlyHint:true`. It is implicit only for entries whose late completion is not observable; an entry with a live detached wait needs `_meta["io.mcpproxy/reconciles"]`;
- an explicit cancel, sent through the transport instance captured at dispatch (stdio); local-only on HTTP/SSE;
- an epoch bump (stdio only);
- the TTL expires.

Caller cancellation is never forwarded upstream in v1. Every new field is metadata layered on the 113-c taxonomy.

Round 2 changes, in one line each: authorization uses `auth.ScopedView`; records are only created at `CheckAndBegin`, so the global cap folds gating entries into per-scope overflow blockers and, as a last resort, refuses before dispatch; cancel is authorized against the admission tier; `local` is gated per MCP session; registry methods return transitions that the caller emits outside every lock; activity resolution is made reliable by a registry-driven resync; REST and replay get typed capture paths; `code_execution` extends its `{ok:false,error}` envelope; both refusal points are audited as `tool_call rejected`; mcp-go multi-round calls are tracked as an id list.

## Technical Context

**Language/Version**: Go 1.26, backend only. Also generated TS types and a read-only count in the Web UI.
**Primary Dependencies**: existing only: mcp-go v1.0.0, zap, bbolt, Cobra. **No new dependencies.**
**Storage**:
- The registry is in memory.
- Activity records gain five `omitempty` fields.
- A new bbolt index maps `call_id` to the activity id, written in the same transaction as the record (FR-024a).
- `UpstreamRecord` gains one `omitempty` field.

**Testing**:
- Per-package unit tests with `-race`.
- A stdio fixture in `cmd/mcpfixture`, `slow_tool`. It can sleep, emit progress, succeed late, return `isError` late, return a JSON-RPC error late, or return a malformed result late. It counts `tools/call` receipts and records any `notifications/cancelled` it receives, including the raw `requestId` JSON.
- goleak, `./scripts/test-api-e2e.sh`, and a real isolated instance.

**Target Platform**: all three desktop OSes, plus the server edition (shared code, no build tags).
**Project Type**: single Go module.
**Performance Goals** (measured by benchmarks in `internal/upstream/pending` and `core`):
- `CheckAndBegin`: O(gating entries for the server and principal) under the per-server mutex. That is at most `max_pending_calls_per_principal` (16). It makes one record allocation per call.
- Detached dispatch (stdio): each `tools/call` costs one goroutine, one buffered channel and one select. Budget: < 50 µs of added p50 latency and < 4 KB per in-flight call. This replaces the earlier claim of "no allocation / no added latency", which the always-detached design cannot meet.
- With the registry empty and the mode `off`, the HTTP/SSE path adds only `CheckAndBegin` and `Complete`.

**Constraints**:
- The signatures of `Manager.CallTool`, `Manager.CallToolOnEpoch` and `managed.Client.CallTool` stay frozen.
- No `call_tool_*` input schema changes.
- No reconnect on a config change.
- The legacy error string at each surface stays byte-identical in every mode (spec FR-003a). Success results are unchanged.

**Scale/Scope**: the bounds are those in spec FR-007a.

## Constitution Check

- **Security by default**: yes.
  - The gate is scoped per principal, with a stable token principal id (FR-020).
  - Visibility and cancel are re-checked against the current effective scope and the exact permission (FR-021).
  - SSE events are filtered per principal (FR-024b).
  - Non-disclosing `not_found`. Call ids grant nothing.
- **No new dependencies**: yes.
- **Backward compatible**: additive.
  - The legacy strings are preserved per surface.
  - The `call_tool_*` `troubleshooting` value changes for timeouts only (spec FR-003a).
  - REST adds a 409 for refusals, which happen only in `enforce` mode.
  - `upstream_servers` gains a documented schema delta (FR-033).
- **Both editions**: yes. The tenant allowlist gains one named must-refuse (`/servers/{id}/pending-calls`, FR-023); nothing is newly allowed.
- **Tests first**: every task in tasks.md (to be generated by `/speckit.tasks`) writes the failing test first.
- **Narrow blast radius**: yes. Sibling ctx values are used instead of signature changes. The authoritative gate lives at one seam.

## Research & Decisions

| # | Decision | Rationale | Rejected alternatives |
|---|---|---|---|
| R1 | Mint our own `call_id`; capture the typed `mcp.RequestId` in a `transport.Interface` decorator | mcp-go assigns the JSON-RPC id inside `client.Client` and does not return it. The decorator sees `SendRequest(ctx, req)` with `req.ID`. A string conversion would lose the JSON type (numeric `1` vs `"1"`) | Fork mcp-go; store the id as a string |
| R2 | **Always-detached** inner `SendRequest` for stdio `tools/call` with `CallMeta`: inner ctx = `WithDeadline(WithoutCancel(ctx), dispatch + remaining(ctx) + ttl)`, fixed at dispatch; ties go to the result | `Stdio.SendRequest` deletes the response channel on `ctx.Done()` (`stdio.go:~594-650`), so switching to waiting only after the deadline is impossible. `WithoutCancel` alone has no end, so the deadline is required | Detach only after the deadline (cannot work); `WithoutCancel` with no deadline (leaks until transport close) |
| R3 | No detached wait on HTTP/SSE | It would keep the HTTP request open past the deadline and change today's abort behavior | Same mechanism for all transports |
| R4 | Gate key: `(server, principal)`, plus the epoch for stdio. An epoch bump resolves **stdio** entries only | A stdio bump kills the child, so mcpproxy can no longer observe the call, but it does not prove the effect was undone (accepted ambiguity, Q5). An HTTP reconnect does not stop remote work | Resolve every transport on a bump (unsafe for HTTP); keep stdio entries across a bump (blocks until the TTL with no way to observe the call) |
| R5 | Gate tier = `contracts.AnnotationTier`, with `unannotated → write` and not-found → `destructive`; read only with an explicit `readOnlyHint:true` | `tierForAnnotations`/`DeriveCallWith` map unannotated tools to read, which would make the gate inert for most real servers. The caller picks the variant, so the gate uses the target tier | `tierForAnnotations`; reusing the Spec 108 profile `unannotated` policy (it is per profile, absent under legacy profiles, and its default is not conservative) |
| R6 | **Authoritative** gate = `CheckAndBegin` at `managed.Client.callTool` after the post-admission generation re-check, under the per-server registry mutex that also serializes `MarkUnresolved`. **Advisory** pre-admission check in `Manager.callTool` and the `code_execution` dispatch | A check before admission only lets a queued write slip through. The managed seam is the only place shared with activity replay (`runtime.go:1571`). The advisory check saves concurrency slots | Gate only in `Manager.callTool` + `code_execution` (misses replay and queued writes); gate per handler |
| R7 | Intent, principal and call id travel in ctx (`pending.WithCallMeta`). Missing meta → fail-closed (`destructive`) | Keeps the frozen signatures and fails safe for any future caller | New `CallToolGated` siblings |
| R8 | Never send `notifications/cancelled` on mcpproxy's own deadline or on a caller ctx end; only on `cancel_pending_call` | mcp-go's server cancels the handler ctx with a plain `WithCancel` and handles inbound `notifications/cancelled` before user handlers (`request_handler.go:111-120`, `server.go:2477-2486`). A client-side timeout, a script deadline, an SSE disconnect and a shutdown cannot be told apart from an explicit cancel. Forwarding would kill the #1317 consent | Forward caller cancels (undoes #1317); a configurable mode now (no attributable signal; deferred to Q4) |
| R9 | Inject `_meta.progressToken = call_id` (stdio, only when absent); progress window = 30s constant | The only protocol-level liveness signal. Harmless to servers that ignore it | Configurable window (no use case) |
| R10 | Typed outcome as metadata. The legacy error string is preserved per surface, `pending_call` is added, and only the timeout `troubleshooting` value changes | Clients reading text still see the existing string, and 113-c remains the single classification | Byte-identical whole JSON body (cannot add `pending_call`); `_meta`-only (agents reading text get no self-healing hint) |
| R11 | List and cancel on `upstream_servers` (MCP), not a new built-in tool; documented delta: 2 enum values + `call_id` | Avoids a new top-level tool. The cost is that the cancel action is unavailable where `upstream_servers` is hidden, so the refusal text computes availability per caller (FR-016) | New `pending_calls` built-in tool (BM25 index, goldens, direct surface) |
| R12 | REST refusal = `409 Conflict` | A state conflict that resolves later. Different from the Spec 093 429 "capacity" | 429; 423 |
| R13 | In-memory only | Persistence would not make a restart safe: stdio children die, and remote HTTP work is unobservable either way. Stated as accepted ambiguity | BBolt persistence |
| R14 | Principal id for agent tokens = 128-bit hash of length-prefixed `(UserID, Name, CreatedAt)`, stamped on `AuthContext.TokenPrincipalID` at authentication | No stable id exists today. Names are unique only per owner, so the owner is part of the key. `Name` alone conflates a deleted and recreated token. The prefix changes on regenerate. `UserID` alone conflates sibling tokens | Add a persisted random token id (needs a migration for existing tokens; could follow later); `(Name, CreatedAt)` without owner (collides across owners) |
| R15 | Cancel is sent through the transport instance captured at dispatch, with a bounded send outside every lock. HTTP/SSE cancel is local-only in v1 | Checking the epoch and then sending through the current client is a TOCTOU across a reconnect. HTTP transports are built separately and have no id capture | Epoch check + current client; holding `epochMu` across I/O; an HTTP decorator now (needs 113-e re-init safety tests first) |
| R16 | Authorization and principal derivation read `auth.ScopedView(ac, confinedAnonymous)`; confined anonymous callers are an `anonymous_profile` principal | `AnonymousContext` is `AuthTypeAdmin` on purpose, so raw `IsAdmin()` would make a profile-confined anonymous caller a global admin over token-owned entries | Raw `IsAdmin()`; treat confined anonymous as `local` |
| R17 | Global cap: evict terminal/read → fold the inserting scope's oldest gating entry → fold the largest holder's → refuse pre-dispatch (`pending_registry_full`) | Records are only created at `CheckAndBegin`, so that is the one decision point. Folding keeps every gate closed while bounding memory; refusal only when 4096 calls are in flight | Dispatch untracked (breaks SC-001); exceed the cap (unbounded); evict gating entries (opens gates) |
| R18 | Detached cap applies to post-deadline waits, with a dynamic fair share `max(1, floor(cap/N))` and preemption of the most-over-share principal's newest wait | In-flight waits are already bounded by `dispatched` records. A per-principal ceiling ("at most half") lets two principals take every slot. Preempting a post-deadline wait only cancels the inner ctx, which sends nothing upstream | Per-principal ceiling; static reservations (unbounded principals) |
| R19 | Cancel is authorized against the admission tier, not the gate tier | A read-only token can call an unannotated tool (`tierForAnnotations` → read) whose gate tier is write. Requiring `write` to cancel would strand it | Gate-tier permission; unconditional owner cancel (ignores later narrowing) |
| R20 | Gate scope = principal, except `local`, which splits per MCP session | Several clients share `local` in the personal edition. A shared gate gives cross-session false positives and lets one session's read reconcile another's write | Per-principal gate (false positives when stdio flips to `enforce`); per-session visibility (one human, A3) |
| R21 | Registry methods return `[]Transition`; callers emit after releasing `serverSet.mu` and `epochMu` | `epochMu` must never be held across foreign callbacks (`managed/client.go:151-158`), and SSE, bus, activity and logging are foreign callbacks | Emit inside the registry; emit asynchronously from a goroutine (reorders events) |
| R22 | Registry is the source of truth for activity resolution: tombstones carry `activity_synced`, a 30s resync applies and acks them, and orphaned `unresolved` rows become `untracked` | The bus drops events when a subscriber is full (`event_bus.go:205-218`) | Make the bus blocking (stalls every publisher); rely on the held map (ordering only) |
| R23 | Both refusal points audited as `tool_call outcome:rejected` | `authz allow` is written at Started, before the Manager and managed seams; `auditAuthz` is first-write-wins | `authz deny` at the advisory point (silently dropped); move the advisory check into each handler before Started (five sites, duplicating the Manager check) |
| R24 | Track an ordered list of JSON-RPC ids per call; classify an input-required late result as `input_required` | mcp-go's `multiRoundTrip` sends a new `tools/call` per round | One id per call (wrong id for cancel); treat input-required as `success` (opens the gate on an unfinished call) |

## Data Model

```go
// internal/upstream/pending (new package; stdlib + zap + mcp-go/mcp types only)
type State string  // dispatched | unresolved | completed_late | reconciled | cancelled | connection_reset | expired
type Reason string // deadline_exceeded | still_running | caller_deadline | caller_cancelled (waiting_human reserved, Q1)
type GateTier string // read | write | destructive
type LateOutcome string // success | tool_error | jsonrpc_error | malformed | input_required

type Principal struct{ Kind, ID string } // Kind: agent_token | anonymous_profile | local
type GateScope string // principal key, or local/session:<id>, or local/nosession (FR-020a)

type Record struct {
    CallID, Server, Tool, Transport string
    Epoch            int64
    Principal        Principal
    GateScope        GateScope
    GateTier         GateTier      // gate + reconcile (FR-015)
    AdmissionTier    GateTier      // cancel authorization (FR-021)
    JSONRPCIDs       []mcp.RequestId // typed, in round order (FR-002); empty for HTTP/SSE
    Canceller        Canceller       // the decorator instance captured at dispatch; always nil for HTTP/SSE in v1
    LateObservable   bool            // false for HTTP/SSE, abandoned, preempted or transport-closed waits
    activitySynced   bool            // FR-024a resync ack
    DispatchedAt, UnresolvedAt, ExpiresAt, LastProgressAt, ResolvedAt time.Time
    Reason           Reason
    State            State
    respondedLate    *LateOutcome // set by ResolveLate while State==dispatched (FR-010a)
    LateOutcome      LateOutcome
    ResolutionActor  string // principal kind
}

type Canceller interface {
    // CancelRequest sends notifications/cancelled for id on THIS transport
    // instance. It returns ErrTransportClosed when the instance is gone.
    CancelRequest(ctx context.Context, id mcp.RequestId, reason string) error
}

// Overflow blocker (FR-007a): one per (server, gate scope); gates like an entry.
type Blocker struct {
    ID                 string // pcb_...
    Server             string
    GateScope          GateScope
    Principal          Principal
    Count              int
    OldestUnresolvedAt time.Time
    MaxExpiresAt       time.Time
    MaxGateTier        GateTier
    MaxAdmissionTier   GateTier
}

type Transition struct{ Record Record; From, To State } // emitted by the caller, outside every lock (FR-009)

type Registry struct { /* map[server]*serverSet; serverSet{mu sync.Mutex; epochFloor int64; blockers map[GateScope]*Blocker; ...}; global count; caps; monotonic clock */ }

// All methods for one server take serverSet.mu. Nothing does I/O, logging at Info,
// event publication or callbacks under it; every mutator returns []Transition.
func (r *Registry) CheckAndBegin(m CallMeta, server, transport string, epoch int64, mode Mode) (Decision, *Record, []Transition, error) // error: *RefusedError | *CapacityError
func (r *Registry) Advisory(m CallMeta, server string, epoch int64, mode Mode) Decision   // never admits
func (r *Registry) Complete(callID string) []Transition                                  // dispatched -> dropped
func (r *Registry) CompleteRead(callID string, ok bool, reconciles []string) []Transition // FR-018 (implicit + explicit)
func (r *Registry) MarkUnresolved(callID string, reason Reason) (Record, []Transition)   // also decides the detached slot (FR-007a)
func (r *Registry) ResolveLate(callID string, out LateOutcome) []Transition
func (r *Registry) ResolveEpoch(server string, newEpoch int64) []Transition              // called under epochMu; emits nothing
func (r *Registry) Cancel(callID string, caller Caller) (Record, CancelPlan, []Transition, bool)
func (r *Registry) List(server string, caller Caller) []Record                           // FR-021 visibility; includes blockers
func (r *Registry) Touch(callID string)
func (r *Registry) UnsyncedTerminal(limit int) []Record                                  // FR-024a resync
func (r *Registry) AckSynced(callIDs []string)
func (r *Registry) Known(callID string) bool                                             // live, folded or tombstoned

type Caller interface { // adapter over auth.ScopedView(ac, confinedAnonymous), evaluated per request (FR-021)
    Principal() Principal
    IsAdmin() bool                     // IsAdmin() on the SCOPED view, never the raw context
    CanSeeServer(server string) bool   // CanAccessServer ∩ profile scope (Spec 105)
    HasPermission(tier GateTier) bool  // exact match; cancel checks the record's AdmissionTier
    ReadOnlyMode() bool
}

type CallMeta struct{ CallID string; Principal Principal; GateScope GateScope; Tier, AdmissionTier GateTier; Mode Mode; Source string; Reconciles []string }
func WithCallMeta(ctx context.Context, m CallMeta) context.Context
func CallMetaFrom(ctx context.Context) (CallMeta, bool)
var ErrPendingCallUnresolved = errors.New("pending call unresolved")
type RefusedError struct{ Blocking []Record; Blockers []Blocker; Actions []Action } // errors.Is(err, ErrPendingCallUnresolved)
var ErrRegistryFull = errors.New("pending registry full")
type CapacityError struct{ RetryAfter time.Duration } // errors.Is(err, ErrRegistryFull); handled like a Spec 093 shed
```

```go
// internal/upstream/core/transport_correlate.go (new)
type correlatingTransport struct {
    inner  transport.Interface
    reg    *pending.Registry
    closed atomic.Bool
    // detached-wait accounting per principal (FR-007a)
}
type bidiCorrelatingTransport struct{ *correlatingTransport } // + SetRequestHandler; chosen at construction iff inner implements BidirectionalInterface
func (t *correlatingTransport) CancelRequest(ctx context.Context, id mcp.RequestId, reason string) error
```

**Transition ordering** (all under `serverSet.mu`, in this order of precedence):

1. `CheckAndBegin` creates `dispatched`, or refuses.
2. `ResolveLate` on `dispatched` → `respondedLate`. On `unresolved` → `completed_late`. On a terminal record → no-op.
3. `MarkUnresolved`:
   - if `respondedLate` → `completed_late`;
   - else if `Epoch < epochFloor` (stdio) → `connection_reset`;
   - else → `unresolved`.
   - On a terminal record → no-op.
4. `ResolveEpoch`, called inside the `epochMu` section of `managed/client.go` (`:578-580`, `:669-671`): stdio records and blockers below the new epoch → `connection_reset`, and the floor is raised. This is the one place `serverSet.mu` is taken under `epochMu`. The order is always `epochMu` → `serverSet.mu`, never the reverse. It returns its transitions; the managed client emits them after it releases `epochMu` (spec FR-009).
5. A terminal record is a tombstone until it is evicted (FR-007a) and, for `pending_call_history`, until the FR-024a resync acknowledges it.
6. Global-cap handling inside `CheckAndBegin`: evict terminal/read → fold own scope → fold the largest holder → `CapacityError` (spec FR-007a). Folding moves a gating record into its scope's `Blocker` and never opens a gate.

Config:

```go
// internal/config/config.go
PendingCallGate             string   `json:"pending_call_gate,omitempty" mapstructure:"pending-call-gate"` // off|warn|enforce
PendingCallTTL              Duration `json:"pending_call_ttl,omitempty" mapstructure:"pending-call-ttl"`
PendingCallHistory          Duration `json:"pending_call_history,omitempty" mapstructure:"pending-call-history"`
MaxPendingCallsPerPrincipal int      `json:"max_pending_calls_per_principal,omitempty" mapstructure:"max-pending-calls-per-principal"`
MaxDetachedWaitsPerServer   *int     `json:"max_detached_waits_per_server,omitempty" mapstructure:"max-detached-waits-per-server"` // pointer: 0 is meaningful
// ServerConfig
PendingCallGate string `json:"pending_call_gate,omitempty" mapstructure:"pending_call_gate"`
```

Auth: `AuthContext.TokenPrincipalID string`, stamped by `AgentToken.AuthContext()` from the length-prefixed `(UserID, Name, CreatedAt)` hash.

Activity (`storage.ActivityRecord`, `contracts.ActivityRecord`): `CallID`, `PendingState` (registry states plus the activity-only `untracked`), `PendingReason`, `LateOutcome`, `ResolvedAt`, all `omitempty`. New bucket `activity_call_index` (`call_id → activity id`), pruned with its record. The resync's scan for old `unresolved` rows uses a secondary index on `pending_state`.

Replay (`storage.ToolCallRecord`, `contracts.ToolCallRecord`): `CallID`, `PendingState`, `PendingReason`, all `omitempty` (spec FR-024c).

## Contracts

**Timeout, `call_tool_*`** (`isError:true`; the `error` value is the existing Manager-wrapped string, unchanged):

```json
{
  "error": "<legacy string, byte-identical to the base-commit golden>",
  "server_name": "chrome-devtools",
  "tool_name": "navigate_page",
  "troubleshooting": "The upstream may still be executing this call. See pending_call.resolve.",
  "pending_call": {
    "call_id": "pc_4K7Q…",
    "server": "chrome-devtools",
    "tool": "navigate_page",
    "transport": "stdio",
    "state": "unresolved",
    "reason": "deadline_exceeded",
    "connection_generation": 42,
    "upstream_may_still_be_running": true,
    "late_completion_observable": true,
    "dispatched_at": "…", "unresolved_at": "…", "expires_at": "…",
    "resolve": [
      {"action": "wait", "until": "…"},
      {"action": "reconcile", "how": "call a tool with readOnlyHint:true on chrome-devtools"},
      {"action": "cancel", "how": "upstream_servers {operation:\"cancel_pending_call\", call_id:\"pc_4K7Q…\"}"}
    ]
  }
}
```

The `cancel` action appears only when FR-016's availability check passes. The same object is in `result._meta["io.mcpproxy/pending_call"]`.

**Timeout, other surfaces** (spec FR-003a):
- Direct surface: the legacy text plus one appended line, and the same `_meta`.
- `code_execution`: the host function returns `{ok:false, error:{code:<unchanged>, message:<unchanged>, pendingCall:{…}}}`; the run result has `pending_calls: [call_id…]`. A refusal returns `{ok:false, error:{code:"PENDING_CALL_UNRESOLVED", message:<FR-016 text>, pendingCalls:[…]}}`.
- REST: `500` with `{…existing envelope…, "pending_call": {…}}`. The envelope's `error` string is built from the legacy body held in the `withPendingCallCapture` box, so it is byte-identical to the base-commit golden.
- Replay: the record has `call_id`, `pending_state` and `pending_reason`. A refusal is a typed error → 409; a capacity refusal → 429.

**Gate refusal** (`isError:true`; REST `409`):

```json
{
  "error": "refused: chrome-devtools has 1 unresolved write call from you (pc_4K7Q…, navigate_page, 14s ago). The upstream may still apply it. <available actions, one sentence each>",
  "error_class": "proxy_policy", "fault_domain": "proxy",
  "pending_calls": [ { "call_id": "pc_4K7Q…", "tool": "navigate_page", "age_ms": 14000, "expires_at": "…" } ],
  "resolve": [ … only the available actions … ]
}
```

`error_class` / `fault_domain` are present only once 113-c is merged.

**Audit** (Spec 107; spec FR-030):
- Advisory and authoritative refusal: `authz allow` (already written at Started) + `tool_call outcome:rejected reason:pending_call_unresolved`.
- Capacity refusal: `tool_call outcome:rejected reason:pending_registry_full`.
- No `authz deny` is written, and the `authz` reason vocabulary is unchanged.
- Docs: `docs/` audit JSON Schema and the Spec 107 `contracts/audit-line-events.md` table each gain the two `rejected` reasons. `schema_version` stays 1.

**MCP** `upstream_servers`:
- New `operation` values `pending_calls` (optional `name`) and `cancel_pending_call` (required `call_id`).
- New string property `call_id`.
- The response entries omit `principal.id` for non-admin callers.

**REST**: `GET /api/v1/pending-calls?server=&state=`, `GET /api/v1/servers/{name}/pending-calls`, `POST /api/v1/pending-calls/{call_id}/cancel`.
- Added to `oas/swagger.yaml`.
- Tenant sessions: 403. `sessionGETAllowed` gains a named must-refuse for `/servers/{id}/pending-calls` (spec FR-023).

**CLI**: `mcpproxy upstream pending list [--server NAME] [--state STATE] [-o table|json|yaml]` and `mcpproxy upstream pending cancel CALL_ID`. Both support `--help-json`.

**SSE**: `pending_call.unresolved`, `pending_call.resolved`, `pending_call.refused`.
- Payload: `server`, `call_id`, `state`, `reason`, timestamps.
- An internal `_principal` is used for filtering and stripped before sending.
- The types are registered in `identityBearingEventTypes`.
- A principal-aware visibility check runs after `eventVisibleToCaller`.

## Project Structure

### Documentation (this feature)

```text
specs/114-pending-call-outcome/
├── spec.md
├── plan.md        # this file
└── tasks.md       # /speckit.tasks, after maintainer review of Q1-Q6
```

### Source Code (touched packages)

| Place | Change |
|---|---|
| `internal/upstream/pending/` (new) | Registry, states, ordering, caps, gate, `Caller`, ctx helpers, typed errors |
| `internal/upstream/managed/client.go` | `CheckAndBegin` after the post-admission re-check (`:1195-1204`). `MarkUnresolved` / `Complete` / `CompleteRead` on return. `ResolveEpoch` inside the two `epochMu` sections, with its transitions emitted after unlock. Mint the call id and fail-closed meta when ctx lacks `CallMeta` |
| `internal/upstream/core/client.go` | Inject the progress token (stdio). Classify `reason` via `context.Cause`. Extend `logCallInterrupted`. Return a typed error wrapping today's error and the record, so that `.Error()` is unchanged |
| `internal/upstream/core/connection_stdio.go` | Wrap `stdioTransport` in the correlating transport at `client.NewClient` (`:244`). `c.stderr` still comes from the concrete transport (`:282`) |
| `internal/upstream/core/transport_correlate.go` (new) | Ordered id capture per call, always-detached wait, tie rule, `input_required` detection via `NeedsInput()`, `CancelRequest` (latest id), interface-preserving variants, abandon/preempt of post-deadline waits |
| `internal/upstream/core/connection_lifecycle.go` | Route `notifications/progress` with a matching token to `Registry.Touch` |
| `internal/upstream/manager.go` | Owns the `Registry`. Advisory check before admission. Pass `RefusedError` through verbatim (like `LimitError`, `:1714-1718`) |
| `internal/server/mcp.go` | Set `CallMeta` (principal and gate scope from the scoped view and MCP session, gate tier and admission tier from annotations, `Reconciles` from `params._meta`) before `dispatchOnEpoch`. Timeout envelope in `createDetailedErrorResponse`. Refusal and capacity branches beside the shed/generation branches. `withPendingCallCapture` in `CallToolDirect`, with the legacy body. Activity fields. `upstream_servers` operations |
| `internal/server/mcp_routing.go` | `CallMeta`; direct-surface envelope |
| `internal/server/mcp_code_execution.go` | `CallMeta` (inheriting the parent session's gate scope) and the advisory check at the sub-call dispatch; pass `pendingCall` / refusal data to the envelope; run-level `pending_calls` |
| `internal/jsruntime` | `ErrorCodePendingCallUnresolved`; an `errorEnvelope` variant that carries additive fields (`pendingCall`, `pendingCalls`) |
| `internal/server/audit_funnel.go` | `tool_call` rejected reasons `pending_call_unresolved` and `pending_registry_full` (helper shaped like `auditToolCallShed`). No `authz` arm |
| `internal/server/server.go` | Replay audit switch (`:4164-4194`) gains the two rejected arms |
| `internal/runtime/runtime.go` | `ReplayToolCall` sets `CallMeta` (gate tier from stored annotations, principal from the scoped view); replay record fields; returns `RefusedError` / `CapacityError` typed instead of folding them into `record.Error` |
| `internal/runtime/activity_service.go`, `internal/storage` | `call_id` persistence and index, held-update map, update by `call_id`, 30s resync loop and start-up scan, `untracked`, `pending_state` index |
| `internal/httpapi/server.go`, `internal/httpapi/sse_scope.go` | `CallMeta` for `/tools/call`; 409/429/500 mapping from the capture box; replay 409/429; pending-calls endpoints; event visibility on the subscriber's scoped view |
| `internal/httpapi/session_principal.go` | Named must-refuse for `/servers/{id}/pending-calls` |
| `internal/auth` | `AuthContext.TokenPrincipalID` (owner-namespaced); `ServerOpPendingCalls`, `ServerOpCancelPendingCall` (not denied) |
| `internal/contracts`, `cmd/generate-types`, `frontend/src/types/contracts.ts`, `oas/` | Activity fields, `ActivityFilter.PendingState`, `UpstreamRecord.PendingCallGate`, server `pending_calls` count |
| `internal/telemetry` | `BlockReasonPendingCallUnresolved` |
| `internal/config` | Fields, defaults, `ValidateDetailed` (enum, ranges, TTL ≥ `call_tool_timeout`, `enforce` capability gate), Copy/Merge/DetectConfigChanges |
| `cmd/mcpproxy` | `upstream pending list|cancel`, `activity list --pending-state` |
| `cmd/mcpfixture` | `slow_tool` modes listed under Testing, plus a load-shedding mode (returns an input-required result with an empty request map N times) and a late input-required mode |
| `docs/` | `docs/features/pending-calls.md` (new), `docs/configuration.md`, `docs/api/rest-api.md`, `docs/cli-management-commands.md`, audit schema |

**Structure Decision**: single Go module. The new logic sits in one new leaf package, `internal/upstream/pending`, which does not import `internal/server`, `internal/httpapi` or `internal/auth`. Authorization reaches it through the `Caller` adapter.

## Phases

Each phase is one PR, landed at the merge bar from an updated `main`, with no stacking. Every commit uses `Related #1321`. The capability set compiled into each build (spec FR-034) drives the `resolve` actions, so no phase advertises an action that it does not ship.

1. **Phase 1: correlation, typed outcome, listing.**
   - The `pending` package (no gate decisions), including the transition-return emission rule.
   - `CallMeta` at all five entry points (scoped-view principal, gate scope, admission tier), plus fail-closed meta.
   - `CheckAndBegin` in allow-only form, `MarkUnresolved`, epoch resolution, TTL and history sweeper, bounds with folding and the capacity refusal.
   - Per-surface envelopes, including the REST capture box and the `jsruntime` envelope fields. Replay typed errors and record fields. Activity fields, index and resync. SSE events with principal filtering. Listing on MCP/REST/CLI. Tenant must-refuse. Logging fields.
   - The mcp-go pin tests (spec A5).
   - `resolve` lists `wait` (and `reconcile` as advice).
   - No behavior change for any call.
2. **Phase 2: late completion and cancel.**
   - The decorator with ordered id capture, always-detached wait, tie rule, fair-share preemption, and late outcomes (including `input_required`) with activity update.
   - `cancel_pending_call` through the captured instance (stdio); local-only on HTTP/SSE.
   - Progress token and `still_running`.
   - Goleak, memory-bound and benchmark tests.
   - `resolve` gains `cancel` where available.
3. **Phase 3: gate.**
   - The authoritative and advisory checks, and modes `off|warn|enforce` (`enforce` is now accepted by validation).
   - Refusal envelopes, 409, telemetry block reason, audit arms.
   - Per-entry reconciliation (implicit and explicit `_meta`), per gate scope.
   - Per-caller reconcile/cancel availability in the refusal text.
   - 113-d interplay test. Proxy_policy stamping needs 113-c; without it, the refusal is recorded like the Spec 093 shed.
4. **Phase 4 (optional)**: read-only Web UI badge and macOS tray count. A Tasks-based design spec for upstreams that advertise task support. Q4 forwarding mode, if mcp-go gains an attributable cause.

## Test Strategy

- **Unit (`pending`)**:
  - A transition table covering every pair in the spec FR-008 graph, tombstone idempotence, and the precedence order above.
  - A gate truth table: mode × gate tier × principal × epoch × transport.
  - `gateTier` truth table: nil annotations, empty hints, each hint combination, not found.
  - Bounds:
    - per-scope cap folds into the blocker and keeps the gate closed;
    - cross-principal flood never evicts a gating entry and never opens a gate;
    - 256 non-gating/terminal cap;
    - global cap: each step of the eviction → fold own → fold largest → `CapacityError` order, with a small forced cap; all-`dispatched` → refusal, 0 upstream receipts;
    - blocker gating, reconcile, cancel (`id_unknown`), `connection_reset` and TTL;
    - detached fair share with three principals (`floor(cap/3)` each), preemption of the most-over-share principal's newest wait, and self-abandon when over share.
  - Emission rule: every mutator returns transitions; an emitter probe asserts no registry lock (and, in `managed`, no `epochMu`) is held during emission.
  - Resync: unsynced tombstones applied and acked; a tombstone is kept past `pending_call_history` until acked; `Known()` covers live, folded and tombstoned ids.
  - Monotonic TTL with a fake clock.
  - Interleavings under `-race` with `GOMAXPROCS=1 -count=20`:
    - `CheckAndBegin` vs `MarkUnresolved` (queued write);
    - `ResolveLate` before `MarkUnresolved`;
    - `ResolveEpoch` between the deadline and `MarkUnresolved`.
  - Reconciliation ordering: a read dispatched before the entry became unresolved does not clear it.
  - Reconciliation rules: implicit only for non-observable entries without recent progress; explicit `Reconciles` clears an observable entry; same gate scope only (two `local` sessions); an explicit id naming another scope's entry is ignored without disclosure.
- **Unit (`core`)**:
  - The decorator captures ids only for `tools/call` with meta, in round order across load-shedding rounds; cancel uses the latest id.
  - Late `input_required` result → `completed_late` with `late_outcome: input_required`.
  - The reflection test checks the interface set (spec FR-002). The sampling test still passes.
  - The always-detached wait ends on response, on transport close and on its fixed deadline (goleak).
  - Tie: response and deadline ready together → the caller gets the result.
  - Late outcomes `success`, `tool_error`, `jsonrpc_error`, `malformed`. Late content passes through the sensitive-data detector, and no content reaches a client.
  - `CancelRequest` sends `notifications/cancelled` whose `requestId` raw JSON equals the request's, for numeric `1`, string `"1"` and an integer above 2^53.
  - `CancelRequest` on a closed instance → `ErrTransportClosed`, and it never touches a newer instance.
  - Progress with the matching token sets `still_running`.
  - `reason` from `context.Cause`: our timeout, a parent deadline, a parent cancel, and a script `WithTimeoutCause`.
  - Pin tests for every mcp-go behavior in spec A5: `SendRequest` cancellation, `multiRoundTrip` new id per round, `NeedsInput()`, the type-asserted interface set, and the cause-free handler ctx.
  - Benchmark of the detached path against the performance budget.
- **Unit (`managed`)**:
  - An epoch bump resolves stdio entries before any new-epoch call can be gated. HTTP entries survive a bump.
  - `ResolveEpoch` transitions are emitted after `epochMu` is released, at both reconnect sites.
  - The `reconnect_on_use` path.
  - A missing `CallMeta` is gated as destructive.
  - The existing `calltool_cancel_test.go` is unchanged.
- **Server / httpapi / runtime**:
  - Each of the five entry points (including replay) refuses in `enforce`, annotates in `warn`, and passes explicit reads.
  - The queued-write test (spec US2 AS7).
  - `call_tool_read` on a write-annotated tool: the intent validator allows it under `strict` true and false, and the gate refuses it.
  - Per-surface timeout and refusal envelope contract tests, and the legacy-string goldens captured from the base commit.
  - Authorization:
    - two-token tests for list, cancel, count, SSE (all three types, shared and disjoint servers) and activity pending fields;
    - confined anonymous (`anonymous_profile` set): sees and cancels only its own entries on profile-visible servers, never token-owned ones, and receives no token-owned SSE events; unconfined anonymous keeps admin-shaped behavior; a test asserts the handlers never call `IsAdmin()` on an unscoped context;
    - scope revocation, profile reassignment, destructive-only token;
    - read-only token that called an unannotated tool via `call_tool_read`: can cancel its own `write`-gate entry (admission tier `read`);
    - token regenerate keeps the principal; delete+recreate does not; equal name and forced equal `CreatedAt` under two owners → distinct principals;
    - `not_found` bodies byte-identical.
  - `cancel` availability in the refusal text under `disable_management`, `read_only_mode`, `max_tier: write` and a `management_tools` exclusion. `reconcile` availability with no read-only tool on the server, and with read-only tools hidden by profile rules. The wait-only refusal text.
  - `agentDeniedServerOps` pin test for the two new ops.
  - Tenant walk test and a direct `sessionGETAllowed` unit test: the three new routes return the fixed 403 for a tenant session, including `GET /servers/{id}/pending-calls`.
  - Audit: advisory and authoritative refusal → `authz allow` + `tool_call rejected reason:pending_call_unresolved`, and no `authz deny`; capacity refusal → `tool_call rejected reason:pending_registry_full`; count invariants hold on all five entry points, replay included; no line for `cancel_pending_call`.
  - REST: timeout → 500 whose `error` equals the base-commit golden and whose envelope has `pending_call`; refusal → 409; capacity → 429. Replay: refusal → 409, capacity → 429, timeout record fields.
  - `code_execution`: timeout envelope keeps `code`/`message` and adds `pendingCall`; refusal envelope has `PENDING_CALL_UNRESOLVED`.
  - Activity linkage: a late response arriving immediately after the timeout updates the record (held-update path); a dropped bus event and a held-map overflow are repaired by the resync; persisted `unresolved` rows become `untracked` after a restart.
  - MCP/REST/CLI parity test.
  - The toolsurface golden diff equals exactly spec FR-033.
- **Config (FR-028)**:
  - Each field round-trips through the file, BBolt, Copy/Merge and REST PATCH.
  - A hot-reload through the file watcher and through PATCH applies to the next call with no epoch bump.
  - Validation refuses a bad enum, out-of-range values, TTL < `call_tool_timeout`, and `enforce` in a build without the phase 2 capabilities.
- **113-d interplay** (once merged): a refusal is excluded from the failure rate, and a late completion adds no second outcome.
- **E2E**: `./scripts/test-api-e2e.sh`, with the CI `-skip` regex for `internal/server`.
- **Real instance** (SC-007):
  - Setup: isolated `HOME` / data dir, high port, `mcpfixture slow_tool` as a stdio server, `call_tool_timeout: 2s`, curl JSON-RPC through `/mcp`.
  - Steps: timeout → refused write → late response → `activity show` → a write passes.
  - Repeat with `cancel_pending_call` (check the fixture's recorded `requestId`) and with a reconcile read.
- **Lint**: `golangci-lint run --config .github/.golangci.yml ./...`, both bare and with `--build-tags server`.

## Rollout

- Phase 1 is invisible to clients that ignore the new fields, apart from the timeout `troubleshooting` value. It ships in the next minor release.
- Phase 2 adds late completion and cancel. There is still no gate.
- Phase 3 ships with `pending_call_gate` at the Q2 default (`warn` recommended).
  - The release notes explain `enforce`, the per-server override, the unannotated-tool rule (spec Known Limitation 9), and that `connection_reset` and a restart do not prove the work stopped.
  - The recommended setting for the #1317 user is `"pending_call_gate": "enforce"` on the `chrome-devtools` server.
- After one minor release of `warn`, review the opt-in telemetry: the `pending_call_warning` count per server type, and how often a warned write follows an entry that later became `completed_late`. Then flip stdio to `enforce` in a follow-up PR.
- Rollback: `pending_call_gate: off` (global or per server) disables refusals without a restart. `max_detached_waits_per_server: 0` disables detached waits.
- Close #1321 by hand after SC-007 passes on a release build.

## Risks

- **Decorator interface forwarding.** If mcp-go adds a newly type-asserted interface, the wrapper hides it. Mitigation: the reflection test, plus a test that fails on an mcp-go bump until the enumerated list in spec Context item 6 is re-checked.
- **Detached-wait cost and goroutines.** Bounded per server and per principal, ended by a fixed deadline or transport close; goleak and a benchmark cover them. Escape hatch: `max_detached_waits_per_server: 0`.
- **mcp-go `SendRequest` semantics change.** Pinned by the A5 test.
- **False positives in `enforce`.** These come from the conservative unannotated rule and from two agents sharing one agent token. `local` sessions are split by gate scope (R20). The self-healing text, the `warn` default and per-server override mitigate the rest.
- **Overflow folding hides detail.** A folded call loses its late completion and its upstream cancel. It only happens past 16 unresolved entries per scope or at the global cap, and the gate stays closed. Logged at Warn.
- **`_meta` reconcile hint is new protocol surface.** It is optional and stripped before forwarding; agents that ignore it fall back to implicit reconciliation where that is safe, or to cancel and TTL.
- **Lock ordering.** `epochMu → serverSet.mu` is the only nested acquisition, and nothing is emitted under either (R21). A test with `-race` and a lock-order assertion helper covers it.
- **113-c / 113-d drift.** Both are unmerged; citations are pinned to `d5de9320d`. If their final shape changes, phase 3 adapts the mapping only.

## Complexity Tracking

No constitution violations.
- The one new package is justified by R6: a shared leaf that the authoritative seam and the advisory sites all need.
- The always-detached design (R2) is the minimum that makes late completion possible without forking mcp-go.

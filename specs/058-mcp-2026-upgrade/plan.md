# Implementation Plan: MCP Protocol Upgrade to the 2026-07-28 Spec Revision

**Branch**: `058-mcp-2026-upgrade` | **Date**: 2026-09-03 | **Spec**: [spec.md](./spec.md)
**Research**: [research.md](./research.md) | **Tracker**: [#532](https://github.com/smart-mcp-proxy/mcpproxy-go/issues/532)

## Summary

Adopt `github.com/mark3labs/mcp-go` v1.0.0 and serve the MCP `2026-07-28` revision alongside `2025-11-25`, without changing what any existing client sees.

The shape of the work is set by one measured fact: **the library already implements the protocol, and the bump is nearly free in production code** — zero compile breaks on both editions, one test-only symbol move, two known profile-test failures. The engineering is therefore not "implement 2026-07-28"; it is *(a)* making the bump provably inert on the wire, *(b)* resolving the one place mcpproxy genuinely holds connection state, and *(c)* deciding what to do about requirements that turned out to be vacuous or unimplementable through the library's public API.

The rollout is deliberately staged so that each PR is independently safe:

1. **Bump, pinned both directions.** Library at v1.0.0, client-facing transports pinned legacy (FR-028), upstream-facing `initialize` pinned legacy too (D3 — otherwise the bump alone silently switches every upstream hop to the new protocol). Wire behaviour unchanged.
2. **Stateless-readiness.** Profile resolution, session-id-free attribution, and the cross-client cancellation guard — all landed and tested *under the pin*, using an era-pinned test harness so the assertions are not vacuous.
3. **Lift the client-facing pin**, then the upstream pin, each behind its own acceptance evidence.
4. **Additive features** (cache hints, deterministic ordering, trace context) once both eras are served.

## Technical Context

**Language/Version**: Go 1.25.5 (`go.mod` toolchain)
**Primary Dependencies**: `mark3labs/mcp-go` v0.57.0 → **v1.0.0** (the only dependency change); existing `santhosh-tekuri/jsonschema/v6`, `zap`, Cobra, BBolt, Bleve. **No new dependencies.**
**Storage**: BBolt (`config.db`) — one open risk around Spec-032 tool hashes, see Risks
**Testing**: `go test` + testify; `internal/server` integration harness; `scripts/test-api-e2e.sh`; `e2e/playwright`; mcp-go's own `server/servertest` and conformance fixtures
**Target Platform**: macOS / Linux / Windows, personal and server editions
**Project Type**: single Go backend (no frontend work in this feature)
**Performance Goals**: no regression in connect time or `retrieve_tools` latency; the `server/discover` probe against legacy upstreams is the one new cost, and D3's pin avoids it for now
**Constraints**: zero regressions for `2025-11-25` clients (FR-026); no wire-format change may ship in the same PR as the library bump
**Scale/Scope**: ~22 session call sites, 5 client constructor sites, 5 server transport sites, 6 test files for the `servertest` swap

## Constitution Check

| Principle | Verdict |
|---|---|
| I. Performance at Scale | **Pass.** No indexing or search change. The one new network cost (`server/discover` probe per upstream) is deferred by the D3 pin and must be measured before the pin lifts. |
| II. Actor-Based Concurrency | **Pass.** No new goroutines or shared state. Note the upgrade *removes* one background path for modern clients (roots fetch never spawns). |
| III. Configuration-Driven | **Pass with a caveat.** The era pins start as constants, not config. If operators need to opt into `2026-07-28` early, that becomes a config field and must go through the 4-point wiring checklist. Recorded in Complexity Tracking. |
| IV. Security by Default | **Attention required.** Two items: the cross-client cancellation collision (Risks R1) and the fact that quarantine/TPA scanning must keep working when tool schemas are re-decoded (R2). Neither is caused by this feature, both are exposed by it. |
| V. Test-Driven Development | **Pass.** Every task below names its failing-test-first gate. The era-pinned harness (D7) exists precisely so the tests cannot pass vacuously. |
| VI. Documentation Hygiene | **Pass.** Three false comments and two doc pages are corrected as part of the work, not after it. |

## Project Structure

### Documentation (this feature)

```text
specs/058-mcp-2026-upgrade/
├── spec.md              # exists; needs the two amendments below before implementation
├── plan.md              # this file
├── research.md          # Phase 0 output
├── data-model.md        # Phase 1 output
├── quickstart.md        # Phase 1 output
├── contracts/           # Phase 1 output
├── checklists/
│   └── requirements.md  # exists
└── tasks.md             # created by /speckit.tasks, not here
```

### Source Code (repository root)

```text
internal/
├── server/                     # the bulk of the work
│   ├── server.go               # 5 NewStreamableHTTPServer sites -> one options helper (FR-028)
│   ├── profile_resolver.go     # tier-3 era gate + resolveActiveProfileForList (D1)
│   ├── profile_tool.go         # set_profile era behaviour + slug-before-session validation
│   ├── mcp_direct_scope.go     # list filters switch to the list-only resolver
│   ├── content_forward.go      # stop copying ctr.Result wholesale (D2 sibling task)
│   ├── mcp_routing.go          # direct-surface twin of the above
│   └── mcp.go                  # modern-era client-name attribution (AfterInitialize replacement)
├── upstream/core/
│   ├── connection_lifecycle.go # upstream-facing protocol pin (D3)
│   └── client.go               # FR-009/010 candidate layer; Spec-032 hash input
├── transport/http.go           # 4 client constructor sites, if the pin is done client-side
└── runtime/tool_quarantine.go  # Spec-032 hash — R2

bench/, internal/server/, internal/upstream/{,core,managed}/   # servertest import swap (6 files)
docs/features/profiles.md, docs/architecture.md                # doc corrections
```

**Structure Decision**: no new packages. Every change lands in an existing file; the only new file is the era-pinned test helper.

## Phases

### Phase A — Ratify the spec amendments (blocking, human decision)

The research surfaced three requirements that cannot be met as written. **This is the one place the plan stops and asks**, because implementing around an unratified MUST is worse than pausing.

- **FR-015 / FR-016 (MRTR relay)** → detect-and-frame in this spec; relay deferred to a follow-up. `client.CallTool` is hard-wired to an internal round-trip loop and the single-shot entry points are unexported, so a proxy **cannot** relay MRTR through the v1.0.0 public API. Two of three design judges chose deferral.
- **FR-018 / SC-005 (resource-not-found translation)** → vacuous; mcpproxy proxies no resources.
- **FR-007 (notifications)** → re-scope to requests; the library provides no header path for client-originated notifications, and mcpproxy originates none.
- **SC-006** → downgrade to "hints present and valid" or fund a bench cell.

Cross-review the amendments before they land (repo convention for generated specs).

### Phase B — The bump (one PR, no wire change)

1. `go get github.com/mark3labs/mcp-go@v1.0.0`; swap 32 `NewTestStreamableHTTPServer` call sites to `server/servertest`.
2. Add `clientFacingStreamableOptions()` applying the FR-028 legacy pin at all five transport sites, as a constant.
3. Pin the upstream-facing `initialize` to `mcp.LATEST_LEGACY_PROTOCOL_VERSION` (D3).
4. Fix `forwardContentResult` and its direct twin to copy result fields explicitly (D2).
5. **Gate**: full suite green on both editions; the three frozen goldens byte-identical; a wire-capture test proving the negotiated version is unchanged in both directions.

### Phase C — Stateless readiness (under the pin)

6. Tier-3 era gate (`!server.IsModernRequest(ctx)`) plus `resolveActiveProfileForList`; fix the pre-existing `prompts/list` FR-012 violation.
7. `set_profile`: validate the slug before the session guard; return machine-actionable guidance naming `/mcp/p/<slug>` on modern requests. Description rewrite and golden regeneration deferred to the end-of-window PR.
8. Modern-era client attribution: read `RequestProtocolInfoFromContext(ctx).ClientInfo` in the `BeforeAny` hook so activation telemetry and client names survive the loss of `AfterInitialize`.
9. Cross-client cancellation guard (R1).
9a. Canonicalize schema `$ref` form in `NormalizeJSON` + the four-fixture hash-stability guard test (R2/D8). Land this **in Phase B with the bump**, since that is when the decode behaviour changes.
10. Era-pinned test harness; split the two profile tests into legacy and modern variants; add the missing server-edition modern-era entitlement test (FR-013).
11. Correct the three false comments and two doc pages.

### Phase D — Lift the pins, then adopt

12. Lift the client-facing pin; re-run everything with real modern clients.
13. Measure the `server/discover` probe cost against non-mcp-go upstreams, then lift the upstream pin.
14. FR-019 cache hints, FR-020 deterministic ordering, FR-021 schema dialect, FR-022 trace context, FR-025 `_meta` log level.

## Risks

**R1 — Cross-client request cancellation (security, verified).** `inflightKey` (`server/server.go:2499-2506`) keys in-flight cancels as `"<sessionID>:<requestID>"`, and every modern request has `SessionID() == ""`. Registration at `request_handler.go:117-121` is unconditional. Two clients that both use JSON-RPC id `1` — the common case — share the key `":1"`, so a `notifications/cancelled` from one **cancels the other's in-flight call**. The FR-028 pin masks this today. Before the pin lifts: report upstream, and add a mcpproxy-side guard or a per-request session id.

**R2 — Spec-032 tool-hash drift (measured, mitigation chosen).** v1.0.0 rewrites draft-07 `#/definitions/…` refs on decode, and mcpproxy hashes the *decoded* schema, so an affected tool would be reported as changed — a rug-pull signal — after a bump that changed nothing upstream. Measured against a real 1096-tool database: **zero exposure**, because no upstream in that sample emits draft-07 local refs. Mitigation is therefore a normalizer fix in `internal/hash/hash.go` plus a four-fixture guard test, not a migration (D8).

**R3 — Vacuous acceptance tests.** Under the pin, an unpinned client negotiates *down*, so `ProtocolVersion() == "2026-07-28"` assertions pass while proving nothing. Mitigated by the era-pinned harness, which is a Phase-C deliverable rather than an afterthought.

**R4 — CI skip-regex trap.** `release-qa-gate.yml` and `e2e-tests.yml` skip on bare `MCP`, so any new test with "MCP" in its name silently vanishes from the race suite. Naming convention enforced in review.

**R5 — OAuth churn.** v1.0.0's OAuth surface is byte-identical to v0.57.0, so the spec's flagged risk did not materialize *for this bump*. It remains live for the next library release, which names authorization hardening as its workstream.

## Complexity Tracking

| Deviation | Why needed | Simpler alternative rejected because |
|---|---|---|
| Two independent era pins (client-facing and upstream-facing) rather than one | The library couples them: one constant drives both `LATEST_PROTOCOL_VERSION` for outbound `initialize` and the server's advertised set | A single pin leaves the upstream hop silently upgraded by the bump, violating FR-027 |
| `set_profile` keeps a description that will be briefly untrue for modern clients | The text is byte-frozen in 7 golden files; regenerating them is a wire-format change | Rewriting in the bump PR mixes a behaviour change into a dependency bump |
| Detect-and-frame instead of MRTR relay | The public client API cannot express a single-shot call | Reimplementing the call path duplicates library internals for a capability mcpproxy does not proxy today |

# Feature Specification: MCP SDK v1 Migration (Tracking Spec)

**Feature Branch**: `076-mcp-sdk-v1-migration`
**Created**: 2026-06-24
**Status**: Draft — **Tracking spec only** (no code port in this ticket)
**Input**: GH #70 "MCP sdk v1" · Parent epic MCP-7 · Issue MCP-13 (D30-4)
**Owner**: CTO (tracking) → BackendEngineer (execution, once decomposed)

> **Scope guard for this ticket (MCP-13):** This document is a *tracking* spec.
> It defines scope, motivation, breakage surface, risk, rollout strategy, and a
> test plan. It deliberately does **not** port any code, change `go.mod`, or
> design the final API mapping in detail. Implementation is decomposed into
> follow-up child issues only after this spec and its rollout strategy are
> reviewed. See [Out of Scope](#out-of-scope-for-this-tracking-spec).

---

## Summary

MCPProxy currently depends on the community SDK
**`github.com/mark3labs/mcp-go v0.55.0`** for all Model Context Protocol wire
types, the downstream MCP **server** (the proxy face that AI agents connect to),
and the upstream **client/transport** layer (how the proxy talks to managed MCP
servers). GH #70 proposes migrating to the **official, Go-team-supported SDK
`github.com/modelcontextprotocol/go-sdk` v1.0.0**
(<https://github.com/modelcontextprotocol/go-sdk/releases/tag/v1.0.0>).

The official SDK is a *steady dependency* (maintained jointly by the MCP project
and Google's Go team), reaches API stability at v1, and is expected to track the
MCP spec more closely than community SDKs. The cost is a non-trivial, repo-wide
API migration: the two SDKs differ in package layout, tool registration model,
schema representation, and transport abstractions. mcp-go is used across
**67 Go files**.

This spec exists so the migration is approached as a *planned, gated, reversible*
program of work rather than an ad-hoc dependency swap.

---

## Motivation (Why migrate)

1. **Stewardship & longevity** — the official SDK is co-maintained by the Go
   team and the MCP project; long-term maintenance risk is lower than a
   single-maintainer community library.
2. **Spec fidelity** — the official SDK is the reference implementation of the
   MCP spec for Go; new spec features (elicitation, resource links, structured
   content, progress/cancellation semantics) land there first.
3. **Ecosystem alignment** — registry, server authors, and tooling increasingly
   target the official SDK; staying aligned reduces interop friction.
4. **API stability** — v1.0.0 carries a stability commitment, reducing churn
   from minor-version breaking changes we have absorbed historically from
   `mark3labs/mcp-go` (we are on v0.55.0 — 55 minor releases of a v0 API).

**Non-motivations:** This is not driven by a known bug or performance defect in
`mark3labs/mcp-go`. The current SDK works. Migration is a strategic dependency
decision, which is precisely why it must be deliberately gated rather than rushed.

---

## User Scenarios & Testing *(mandatory)*

Because this is an internal dependency migration, "users" are AI agents and MCP
clients connecting through the proxy, and operators running the proxy. The
migration is successful only if it is **behavior-preserving** at every external
boundary.

### User Story 1 - Agents see no behavior change (Priority: P1)

An AI agent connected to the proxy continues to call `retrieve_tools`,
`call_tool_read|write|destructive`, `upstream_servers`, `quarantine_security`,
and `code_execution`, and receives byte-equivalent results before and after the
migration.

**Why this priority**: The proxy's value proposition is its MCP surface. Any
regression here breaks every downstream agent. This is the migration's
acceptance gate.

**Independent Test**: Run the existing API/MCP E2E suite
(`./scripts/test-api-e2e.sh`, `internal/server/e2e_test.go`) against a build on
the official SDK; results, error shapes, and tool schemas must match the
mark3labs baseline.

**Acceptance Scenarios**:

1. **Given** a built proxy on the official SDK, **When** an agent calls
   `retrieve_tools`, **Then** the returned tool list, ordering, and BM25 scoring
   are identical to the mark3labs baseline.
2. **Given** an upstream tool error, **When** an agent invokes it via
   `call_tool_*`, **Then** the proxy returns an equivalent MCP error result
   (same `isError` semantics and text content).
3. **Given** a streamable-HTTP MCP client, **When** it initializes and lists
   tools, **Then** the handshake, capabilities, and notifications match the
   baseline.

### User Story 2 - Upstream servers connect unchanged (Priority: P1)

The proxy continues to connect to upstream MCP servers over stdio, streamable
HTTP, and SSE — including OAuth-authenticated upstreams, brokered-auth
(server-edition token brokering), header injection, Docker-isolated stdio, and
deprecated-endpoint detection.

**Why this priority**: The upstream transport layer is the most heavily
customized integration point (OAuth coordinator, `BrokeredAuth`, connection
source tagging, header injection, deprecated-endpoint handling). It carries the
highest migration risk.

**Independent Test**: OAuth E2E suite (`oauth-e2e-testing` skill / Playwright),
stdio + Docker isolation E2E, and per-protocol connection tests against live and
stub upstreams.

**Acceptance Scenarios**:

1. **Given** an OAuth upstream, **When** the proxy connects, **Then** the
   RFC 8252 + PKCE flow, dynamic port allocation, and automatic token refresh
   behave identically.
2. **Given** a stdio upstream launched via Docker isolation, **When** the proxy
   spawns it, **Then** runtime detection, image selection, env/secret redaction,
   and lifecycle/cleanup are unchanged.
3. **Given** an upstream that advertises a deprecated endpoint, **When** the
   proxy connects, **Then** the deprecated-endpoint error is detected and
   surfaced as today.

### User Story 3 - Operators see no regression (Priority: P2)

Operators running `mcpproxy serve`, the tray, the CLI, the REST API, and the Web
UI observe unchanged behavior, health fields, activity logs, request IDs, and
diagnostics.

**Why this priority**: Migration must not leak into the operator surface
(`/api/v1`, SSE `/events`, `mcpproxy doctor`, activity log). These consume MCP
types indirectly.

**Independent Test**: REST API E2E, `mcpproxy doctor`, activity-log assertions,
and the Web-UI Playwright sweep (`docs/development/web-ui-verification.md`).

**Acceptance Scenarios**:

1. **Given** any tool call, **When** it completes, **Then** the activity log,
   `X-Request-Id` correlation, and unified `health` field are unchanged.
2. **Given** the Web UI tool/server views, **When** rendered, **Then** tool
   schemas, annotations, and quarantine state display identically.

### Edge Cases

- **Tool annotations** (read-only / destructive / open-world / title hints):
  must round-trip; the proxy relies on these for Spec 018 intent variants and
  Spec 032 quarantine. (16+ call sites each for the hint helpers.)
- **Tool input schema fidelity**: the proxy forwards upstream JSON Schemas
  verbatim (see MCP-3167 / `inputSchema` converter); the new SDK's schema
  representation must not lossily re-encode schemas. This is a known historical
  fragility.
- **Output schema validation** (Spec 056) and **output sanitisation**
  (Spec 059) sit directly on top of `mcp-go/server` result types.
- **Max result size** handling (`mcp_max_result_size.go`) wraps server result
  construction.
- **Content types** beyond text (image, embedded resource, resource links):
  ensure the proxy's content forwarding (`e2e_content_forward_test.go`) covers
  every variant both SDKs support.
- **Protocol version negotiation**: the two SDKs may default to different MCP
  protocol revisions; negotiated version must be pinned/verified.
- **Notifications & progress**: tool-list-changed, progress, and cancellation
  semantics must be preserved.
- **Server-edition build tag** (`//go:build server`): broker/transport code
  under `internal/serveredition/` must migrate in lockstep and keep compiling
  under both build tags.

---

## Breakage Surface *(grounded inventory — the core of this tracking spec)*

mcp-go is imported in **67 `.go` files** across four subpackages. The migration
touches three architectural layers with very different difficulty.

### Layer A — Wire types (`mark3labs/mcp-go/mcp`) — 53 files

The pervasive, mechanical-but-wide layer. High-frequency symbols (call-site
counts, approximate):

| Symbol | ~Sites | Role |
|--------|-------:|------|
| `mcp.CallToolRequest` | 197 | inbound tool-call params |
| `mcp.NewToolResultError` | 152 | error result constructor |
| `mcp.CallToolResult` | 99 | tool-call result |
| `mcp.TextContent` | 78 | text content block |
| `mcp.Tool` | 63 | tool definition |
| `mcp.NewToolResultText` | 32 | text result constructor |
| `mcp.NewTool` + `mcp.With*` builders | ~28 + ~100 | tool builder DSL (`WithString`, `WithBoolean`, `WithDescription`, hint annotations) |
| `mcp.ToolInputSchema` / `mcp.CallToolParams` | ~42 | schema + params types |
| `mcp.Content` | 16 | content interface |

**Migration character**: wide but mostly mechanical. The official SDK replaces
the `With*` builder DSL with a `jsonschema`-based / struct-tag tool registration
model and typed handler signatures. Expect a near-1:1 but non-automatable
rewrite of every tool definition and result constructor. **A thin internal
adapter package** (`internal/mcpcompat` or similar) that re-exports proxy-native
type aliases is a candidate to shrink the blast radius (see Rollout).

### Layer B — Downstream MCP server (`mark3labs/mcp-go/server`) — 17 files

The proxy's server face that agents connect to. Symbols:
`server.NewMCPServer` (15), `server.MCPServer` (12), `server.ServerTool` (26),
`server.NewStreamableHTTPServer` (8), `server.Hooks` (6),
`server.ServerOption` (3), `server.ToolHandlerFunc` (1).

Key files: `internal/server/mcp.go`, `mcp_routing.go`, `mcp_code_execution.go`,
`mcp_max_result_size.go`, `output_sanitisation.go`, `server.go`, plus E2E stubs
(`e2e/stubs/*/main.go`) and tests.

**Migration character**: contained but architecturally significant. The official
SDK's server model (`mcp.Server` + `server.AddTool` typed handlers +
`StreamableHTTPHandler`) differs from `NewMCPServer`/`ServerTool`/`Hooks`. The
proxy registers tools dynamically and rewrites routing per request — the new
registration/handler model must support the proxy's dynamic, hook-driven
dispatch. **Highest-design-effort server-side question:** does the official SDK
expose equivalents to `server.Hooks` for the proxy's cross-cutting concerns
(activity logging, sanitisation, max-result-size, request-ID tagging)?

### Layer C — Upstream client + transport (`client` + `client/transport`) — 16 files

**The highest-risk layer.** mcpproxy has heavily customized the transport stack.
Symbols include: `transport.NewStreamableHTTP`, `DetermineTransportType`,
`CreateHTTPTransportConfig`, `CreateHTTPClient`, `BrokeredAuth`, `OAuthHandler`,
`NewStdioWithOptions`, `WithHTTPHeaders`, `WithCommandFunc`,
`TagConnectionContext` / `GetConnectionSource` / `ConnectionSource*`,
`IsEndpointDeprecatedError` / `ErrEndpointDeprecated`,
`CreateSSEClient`, `DialContext`, `ErrNoToken`, `JSONRPCError`.

Key files: `internal/upstream/core/client.go`, `connection_stdio.go`,
`connection_oauth.go`, `internal/transport/http.go`, `internal/oauth/config.go`,
`persistent_token_store.go`, `internal/appctx/adapters.go`.

**Migration character**: redesign, not rewrite. The proxy injects custom
behavior at points the official SDK exposes differently:

- **Brokered / OAuth auth** (`BrokeredAuth`, `OAuthHandler`, Spec 074 token
  brokering) — must map onto the official SDK's auth / `http.RoundTripper`
  extension points; if those are narrower, the proxy may need to wrap the
  transport's HTTP client.
- **Connection-source tagging** (`ConnectionSourceTray|TCP`,
  `TagConnectionContext`) — proxy-specific context propagation; verify the new
  SDK doesn't strip context.
- **Custom command spawning** (`WithCommandFunc`, `NewStdioWithOptions`) — the
  Docker-isolation spawn path depends on overriding how stdio subprocesses are
  launched. The official SDK must expose an equivalent `CommandTransport` hook,
  or the proxy keeps its own stdio transport.
- **Deprecated-endpoint detection** (`ErrEndpointDeprecated`) — proxy-defined
  error semantics layered on transport errors.
- **Persistent token store** — couples to the SDK's OAuth token-store interface.

> If the official SDK's transport extension points are insufficient for any of
> the above, the fallback is to keep a **proxy-owned transport** that satisfies
> the official client's transport interface — preserving customization while
> still adopting the official types/server. This option must be evaluated early
> (spike) because it materially changes the migration's size.

### Cross-cutting consumers (indirect)

`internal/index` (tool indexing/BM25), `internal/security` (TPA/quarantine on
tool descriptions), `internal/httpapi` (REST tool serialization, the
`inputSchema` converter), `internal/observability` (tool-call spans/metrics),
and the bench harness (`bench/`, full-schema token counts) all consume MCP types
indirectly and need regression coverage.

---

## Requirements *(mandatory)*

### Functional Requirements (of the migration program, not of this ticket)

- **FR-001**: The migrated proxy MUST be **behavior-preserving** at every
  external boundary (MCP surface, REST API, SSE, CLI, Web UI, activity log,
  health fields, `X-Request-Id`).
- **FR-002**: The migration MUST preserve tool **input/output schema fidelity**
  (no lossy re-encoding; honor the MCP-3167 `inputSchema` contract).
- **FR-003**: The migration MUST preserve tool **annotations** (read-only,
  destructive, open-world, title hints) that drive Spec 018 intent variants and
  Spec 032 quarantine.
- **FR-004**: The migration MUST preserve all **upstream transports** (stdio incl.
  Docker isolation, streamable HTTP, SSE) and **auth** (OAuth 2.1 + PKCE,
  Spec 074 brokered auth, header injection, persistent token store).
- **FR-005**: The migration MUST keep **both editions** (personal + server,
  `//go:build server`) compiling and passing their suites.
- **FR-006**: The migration MUST preserve **output sanitisation** (Spec 059),
  **output schema validation** (Spec 056), and **max-result-size** handling.
- **FR-007**: The migration MUST NOT introduce a second concurrent MCP SDK
  dependency on `main` for longer than a bounded, behind-a-build-flag transition
  window; the end state has exactly one SDK.
- **FR-008**: Each migration increment MUST land behind CI green + reviewer
  ACCEPT + QA PASS, consistent with the repo's merge gate (Spec 064).

### Non-Functional / Acceptance

- **NFR-001**: No measurable regression in tool-discovery latency or token
  counts versus the frozen bench baseline (`bench/`, Recall@k + latency).
- **NFR-002**: Dependency surface MUST NOT grow without justification (CLAUDE.md
  "avoid new dependencies"); migration should be net-neutral or net-negative on
  transitive deps where possible.

### Key Entities

- **MCP SDK (incumbent)**: `github.com/mark3labs/mcp-go v0.55.0`.
- **MCP SDK (target)**: `github.com/modelcontextprotocol/go-sdk v1.x`.
- **Compatibility adapter (proposed)**: an internal package re-exporting
  proxy-native aliases for MCP types/results to localize the blast radius.
- **Proxy-owned transport (fallback)**: a transport implementation the proxy
  controls, satisfying the official client's transport interface.

---

## Risk Assessment

| # | Risk | Likelihood | Impact | Mitigation |
|---|------|-----------|--------|------------|
| R1 | Transport extension points (auth/command/source-tagging) insufficient in official SDK | **Med-High** | **High** | Early spike on Layer C; fallback = proxy-owned transport satisfying the SDK interface |
| R2 | Schema fidelity regression (lossy re-encode) breaks downstream agents | Med | **High** | Golden-schema diff tests; reuse MCP-3167 `BaselineSchemasCounted` gate; bench full-schema baseline |
| R3 | Server hook model (activity/sanitisation/max-size/request-id) has no 1:1 equivalent | Med | High | Audit official server middleware/handler model in the spike before committing |
| R4 | Tool-annotation round-trip loss breaks Spec 018/032 | Low-Med | High | Annotation round-trip tests across both SDKs |
| R5 | Big-bang migration produces an unreviewable diff (67 files) | **High** if unmanaged | Med | Phased rollout with adapter layer; per-layer PRs; build flag for transition window |
| R6 | Protocol-version default mismatch changes negotiated MCP revision | Med | Med | Pin and assert negotiated protocol version in E2E |
| R7 | Server-edition (`//go:build server`) drift — broker/transport compiles only under one tag | Med | Med | CI matrix runs both tags every increment (`go test -tags server`) |
| R8 | Official SDK is young (v1.0.0); undiscovered gaps/bugs | Med | Med | Keep incumbent removable until final cutover; bounded transition window with rollback |
| R9 | Hidden behavior differences in error/content encoding | Med | Med | Byte-equivalence E2E against captured mark3labs baselines |
| R10 | Migration stalls half-done, leaving two SDKs on main indefinitely | Med | Med | FR-007 bound + a single tracking epic with a cutover-or-revert decision gate |

**Top risk is R1 (transport).** It alone can change the migration from "large but
mechanical" to "redesign." It must be resolved by a spike **before** committing
to a rollout shape or creating implementation child issues.

---

## Rollout Strategy

Phased, gated, reversible. Each phase is a candidate child issue (created only
after this spec is reviewed — see Out of Scope).

- **Phase 0 — Transport & server-model spike (de-risk R1/R3).**
  Time-boxed spike: stand up the official SDK against (a) one HTTP upstream with
  brokered/OAuth auth, (b) one stdio+Docker upstream, and (c) the proxy's
  downstream server face with one hook (activity logging). Decision output:
  *adapter-and-migrate* vs *proxy-owned-transport* vs *defer*. No production code.

- **Phase 1 — Compatibility adapter (`internal/mcpcompat`).**
  Introduce proxy-native type aliases / thin wrappers for the high-frequency
  `mcp.*` wire types and result constructors, so call sites depend on internal
  names. This localizes Layer A's 53-file blast radius and makes later phases
  reviewable. Still backed by `mark3labs/mcp-go`.

- **Phase 2 — Server face (Layer B).**
  Migrate `internal/server` to the official server model behind the adapter,
  preserving hooks (activity/sanitisation/max-size/request-id). Land with full
  MCP/REST E2E green.

- **Phase 3 — Upstream transport (Layer C).**
  Migrate `internal/upstream/core` + `internal/transport` + `internal/oauth`
  using the Phase-0 decision. Highest-risk PR; OAuth + Docker E2E gating.
  Server-edition broker migrates in lockstep under `//go:build server`.

- **Phase 4 — Wire-type cutover (Layer A) + dependency removal.**
  Repoint the adapter to the official `mcp` package, delete `mark3labs/mcp-go`
  from `go.mod`, run `go mod tidy`. Single-SDK end state (FR-007).

- **Phase 5 — Cleanup & docs.**
  Remove the adapter if it no longer earns its keep, update `docs/`, bench
  baselines, and the constitution/CLAUDE.md SDK references.

**Transition window**: between Phase 1 and Phase 4 both SDKs may coexist on
`main`, bounded and reviewable. A **cutover-or-revert decision gate** after
Phase 0 and again after Phase 3 prevents an indefinitely half-migrated tree (R10).

**Branch/flag note**: where coexistence is unavoidable in a single binary, gate
the new path behind a build flag or config until the cutover PR; never ship a
user-facing flag for this — it is an internal migration.

---

## Test Plan

The migration's safety net is **behavior equivalence against a captured
mark3labs baseline**. No phase merges without its slice of this matrix green.

1. **Baseline capture (pre-migration).** Record golden outputs from the current
   build: tool lists + full schemas (`bench/` full-schema baseline,
   MCP-3167 gate), `retrieve_tools` ordering/scores, representative
   `call_tool_*` results and error shapes, content-type forwarding samples,
   protocol handshake/capabilities.
2. **MCP/API E2E** — `./scripts/test-api-e2e.sh` + `internal/server/e2e_test.go`
   + `e2e_content_forward_test.go` + routing/max-size/sanitisation tests, run on
   each phase build and diffed against baseline (US1, US3).
3. **Upstream transport** — per-protocol connection tests (stdio, streamable
   HTTP, SSE) against `e2e/stubs/*`; Docker-isolation spawn/lifecycle tests;
   deprecated-endpoint detection (US2).
4. **OAuth E2E** — `oauth-e2e-testing` skill / Playwright: PKCE, dynamic port,
   token refresh, persistent token store; **server-edition brokered-auth**
   (Spec 074) under `//go:build server` (US2).
5. **Schema/annotation fidelity** — golden-schema diff + annotation round-trip
   tests (R2, R4); reuse `BaselineSchemasCounted`.
6. **Both editions** — `go test -race ./internal/...` and
   `go test -tags server ./internal/serveredition/... -race` every increment (R7).
7. **Bench regression** — offline + live bench (`bench/`) for token counts and
   Recall@k/latency vs frozen baseline (NFR-001).
8. **Lint** — golangci-lint v2 (`.github/.golangci.yml`) on every increment.
9. **Web-UI sweep** — Playwright verification when tool/schema rendering could be
   affected (`docs/development/web-ui-verification.md`).

**Definition of done for the *program* (not this ticket):** `mark3labs/mcp-go`
removed from `go.mod`; full suite + both-edition + bench green; behavior-equivalence
matrix green; docs updated.

---

## Out of Scope (for this tracking spec)

- Any change to `go.mod` / `go.sum` or addition of the official SDK dependency.
- Any code port (Layers A/B/C), adapter implementation, or transport redesign.
- The Phase-0 spike itself (it is a *named follow-up*, not done here).
- Final API mapping tables (mark3labs symbol → official symbol) — produced during
  Phase 0/1, not pre-committed here.
- Detailed `plan.md` / `tasks.md` — generated after this spec is reviewed.

---

## Open Questions / [NEEDS RESEARCH]

These are intentionally unresolved and are the explicit charter of the Phase-0
spike. They must be answered before implementation child issues are created.

- **Q1 (R1)**: Does `modelcontextprotocol/go-sdk` v1 expose transport/HTTP-client
  and auth extension points sufficient for `BrokeredAuth`, `OAuthHandler`,
  header injection, and connection-source tagging — or is a proxy-owned
  transport required?
- **Q2 (R3)**: Is there an equivalent to `server.Hooks` for cross-cutting server
  concerns (activity logging, sanitisation, max-result-size, request-ID)?
- **Q3 (R1)**: Does the SDK support overriding stdio subprocess spawning
  (today's `WithCommandFunc` / Docker isolation)?
- **Q4 (R2)**: How does the official SDK represent tool input/output JSON Schema,
  and can the proxy forward upstream schemas verbatim without lossy re-encoding?
- **Q5 (R6)**: What MCP protocol revision does v1 negotiate by default, and is it
  the same as our current build?
- **Q6**: Does v1 support every MCP content type the proxy currently forwards
  (text, image, embedded resource, resource links)?
- **Q7**: What is the transitive-dependency delta of adopting the official SDK
  (NFR-002)?

---

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: `github.com/mark3labs/mcp-go` no longer appears in `go.mod`/`go.sum`
  at program completion; exactly one MCP SDK remains.
- **SC-002**: 100% of the pre-migration behavior-equivalence baseline (tool
  lists, schemas, error shapes, content forwarding, protocol handshake) passes
  on the official-SDK build.
- **SC-003**: Both editions build and pass their full suites
  (`-race` and `-tags server`).
- **SC-004**: No regression beyond noise in bench token counts and
  Recall@k/latency vs the frozen baseline.
- **SC-005**: The migration lands across reviewable, individually-gated PRs (no
  single PR rewrites all three layers at once); each merges via CI green +
  reviewer ACCEPT + QA PASS.
- **SC-006**: This tracking spec is reviewed/approved before any implementation
  child issue is created (gate on the program, not the ticket).

## Commit Message Conventions *(mandatory)*

When committing changes for this feature, follow these guidelines:

- Reference the tracking issue and parent epic: `(MCP-13)` / parent `MCP-7`,
  GH `#70`.
- Tracking-spec commit: `docs(spec): add 076 MCP SDK v1 migration tracking spec (MCP-13)`.
- Implementation-phase commits (later children) should reference their own issue
  and the phase, e.g. `refactor(mcp): phase 1 mcpcompat adapter (MCP-13.1)`.
- Keep the spec change isolated; no code or `go.mod` changes ride with this spec.

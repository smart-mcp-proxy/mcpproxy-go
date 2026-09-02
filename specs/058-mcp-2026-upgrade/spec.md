# Feature Specification: MCP Protocol Upgrade to the 2026-07-28 Spec Revision

**Feature Branch**: `058-mcp-2026-upgrade`
**Created**: 2026-05-28
**Status**: Draft — READY FOR PLANNING (dependency gate CLEARED 2026-08-12; spec revised 2026-08-24)
**Input**: User description: "MCP protocol upgrade to the 2026-07-28 spec revision"

## Dependency Gate *(CLEARED 2026-08-12; STABLE 2026-09-02 — see the amendment note below)*

**The gate has cleared.** `github.com/mark3labs/mcp-go` **v1.0.0-beta.1** (released 2026-08-12, mark3labs/mcp-go#951) ships full `2026-07-28` support with per-request protocol-era detection: both protocol eras are served concurrently on one endpoint, modern (stateless) requests synthesize legacy session state so existing `ClientSessionFromContext`-based handlers still receive a session object — though no persistent session id is bound, so session-id-dependent behavior (e.g. Spec-057's session-scoped `set_profile`) still fails; see Cross-Spec Reconciliation — and client-side `initialize` is capped at `2025-11-25`, and a conformance suite pins legacy byte-compatibility. The stable v0.x line (latest v0.58.0, 2026-08-11) does **not** include this support. mcpproxy currently pins **v0.57.0** (an earlier revision of this paragraph said v0.54.0; the pin has moved since).

A compile probe against v1.0.0-beta.1 produced **zero compile errors** on both editions, a clean `go vet`, and exactly **2 unit-test failures** — `TestProfile_SetProfileSessionScoped` and `TestProfile_SetProfileUnknown` (`internal/server/profile_integration_test.go`) — which is precisely the Spec-057 tension flagged in the Cross-Spec Reconciliation section below. *(Independently reproduced 2026-08-26 on `main` @ `4d30624`: `go get github.com/mark3labs/mcp-go@v1.0.0-beta.1` → `go build ./...` exit 0, `go build -tags server ./cmd/mcpproxy` exit 0, `go vet ./internal/...` exit 0, `go test ./internal/server -run TestProfile_` → exactly those two failures and no others.)*

**Merge constraint:** a v1.0.0-beta.x library pin MUST NOT merge to main unless either (a) mcp-go v1.0.0 stable has shipped, or (b) the client-facing Streamable HTTP transport is pinned legacy-only per FR-028. (Gate history: opened 2026-05-28 against v0.54.x; tracked weekly on issue #532 until cleared.)

> **Amendment 2026-09-03 (v1.0.0 stable probe).** mcp-go **v1.0.0 stable** shipped 2026-09-02, so condition (a) is satisfied. Re-probing against stable corrected **two claims in the paragraphs above**, and both corrections change plan-level decisions:
>
> 1. **"client-side `initialize` is capped at `2025-11-25`" is FALSE for stable.** `client.Initialize` is *modern-first*: it probes `server/discover` and only falls back to a legacy handshake when that fails (`client/client.go:305-333`), and `mcp.LATEST_PROTOCOL_VERSION` is now `2026-07-28` (`mcp/version.go:35`). Because `internal/upstream/core/connection_lifecycle.go:19` sends that constant, **the library bump by itself switches the upstream-facing hop to `2026-07-28`** — FR-028's pin covers only the client-facing side. FR-027 is therefore amended below to name both sides explicitly.
> 2. **"zero compile errors" no longer holds for stable.** `server.NewTestStreamableHTTPServer`/`NewTestServer` moved to a new `server/servertest` package (32 call sites across 6 test files). Production code still compiles unchanged on both editions, and the **full** `internal/server` suite under stable fails on exactly `TestProfile_SetProfileSessionScoped` and `TestProfile_SetProfileUnknown` and nothing else.
>
> Evidence and the full delta: [research.md](./research.md). Amendments in this document were ratified under the repo's zero-interruption policy (`CLAUDE.md`) rather than by separate maintainer sign-off; each states its rationale inline so it can be reversed on review.

## Cross-Spec Reconciliation *(read at plan time)*

> **Flagged 2026-07-01 (cross-spec contradiction audit).** FR-012 forbids per-connection variation of `*/list` results; identity-scoped views must be driven by request-carried identity (token / headers / `_meta`), **not connection state**. US3 + FR-013 already reconcile **Spec 028 agent-token scoping** — token identity travels in the `Authorization` header, so it is request-carried and compatible.
>
> **The unresolved case is Spec 057 (In-Proxy Profiles), which is SHIPPED on main.** Spec 057 selects a filtered per-profile toolset by **URL path** (`/mcp/p/<slug>`), not by `_meta`/header identity. Under FR-012 this MUST be classified before implementation:
> - **Option A — treat the profile URL path as request-carried identity.** Each request line carries the slug, so arguably it is *not* connection state. But a client that opens a stream to `/mcp/p/<slug>` binds the profile to that endpoint, which is closer to connection state than to `_meta` identity, and FR-014 forbids relying on a long-lived GET stream — so profile routing must remain correct in a purely stateless, request-scoped model.
> - **Option B — move profile selection into `_meta`/a header** (e.g. a `profile` field in per-request `_meta`), demoting or deprecating the `/mcp/p/<slug>` path form for `2026-07-28` clients while keeping it for `2025-11-25` clients during the deprecation window.
>
> **Update 2026-08-24 — this decision is now the MANDATORY FIRST TASK.** Under mcp-go v1.0.0-beta.1 the upgraded client library defaults to the stateless `2026-07-28` era, so no `Mcp-Session-Id` is bound and session-scoped `set_profile` fails with "set_profile requires an active MCP session; no session id is bound to this request" (`internal/server/profile_tool.go`, session resolved via `ClientSessionFromContext` in `profile_resolver.go`). The A/B choice can no longer be deferred as a plan-time detail: it is the first implementation task, and its acceptance criteria are the two tests that currently fail under beta.1 — `TestProfile_SetProfileSessionScoped` and `TestProfile_SetProfileUnknown` in `internal/server/profile_integration_test.go`.
>
> The audit is broader than `set_profile`: every `ClientSessionFromContext` call site (~21 across `internal/server` — the `describe_tool` session cache, the `code_execution` session, output sanitisation, request routing, and profile resolution) must be reviewed for correctness under the stateless era, not just the profile path.
>
> **Action for plan.md:** pick A or B explicitly (A: treat the `/mcp/p/<slug>` URL path as request-carried identity; B: profile in `_meta`/header), add an acceptance scenario proving per-profile filtering (Spec 057) works under the stateless model without per-connection list variation, and confirm `GET`/`DELETE` on `/mcp/p/<slug>` follow FR-014. Do not implement 058 without resolving this — building FR-012 naively would break shipped per-profile routing. (028 needs no change; 057 does.)

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Connecting clients negotiate the new protocol without breakage (Priority: P1)

An AI agent or IDE that speaks MCP `2026-07-28` connects to mcpproxy and is served correctly, while clients still on `2025-11-25` continue to work unchanged during the deprecation window.

**Why this priority**: Protocol version negotiation is the foundation. If the handshake replacement (removal of `initialize`/`initialized`, addition of `server/discover` + per-request `_meta`) is wrong, nothing else functions. It must not break the large installed base of older clients.

**Independent Test**: Point a `2026-07-28` client and a `2025-11-25` client at the same mcpproxy instance; both can discover and call tools. Verify `server/discover` returns mcpproxy's supported versions/capabilities/identity, and that a `2026-07-28` request carrying `_meta` (protocolVersion, clientInfo, clientCapabilities) is honored.

**Acceptance Scenarios**:

1. **Given** a `2026-07-28` client, **When** it calls `server/discover`, **Then** mcpproxy returns the set of supported protocol versions (including `2026-07-28` and `2025-11-25`), its server capabilities, and its identity.
2. **Given** a `2026-07-28` client, **When** it sends a `tools/call` with version/identity in `_meta` instead of a prior `initialize` handshake, **Then** the call succeeds without requiring an `initialize` round-trip.
3. **Given** a legacy `2025-11-25` client that sends `initialize`, **When** it connects, **Then** mcpproxy still completes the legacy handshake (backward-compat fallback) and serves the client.
4. **Given** a client declaring an unsupported protocol version, **When** it sends a request, **Then** mcpproxy responds with an unsupported-protocol error rather than silently mis-serving.

---

### User Story 2 - Required routing headers are set, validated, and forwarded in both directions (Priority: P1)

mcpproxy, sitting between clients and upstreams, correctly populates and forwards the now-mandatory request metadata headers (`Mcp-Method`, `Mcp-Name`, `MCP-Protocol-Version`) on every hop, and rejects mismatches.

**Why this priority**: A proxy is uniquely responsible for these headers — it both receives them from clients and must re-emit consistent values to upstreams. Getting this wrong makes every proxied call fail. This is the most proxy-specific obligation in the new spec.

**Independent Test**: Issue a `tools/call` through mcpproxy and inspect the upstream-bound request: `Mcp-Method`, `Mcp-Name` (from the resolved tool name/uri), and `MCP-Protocol-Version` are present and match the request body's `_meta`. Send a request with a deliberately mismatched header and confirm a header-mismatch error is returned.

**Acceptance Scenarios**:

1. **Given** any proxied request, **When** mcpproxy forwards it upstream, **Then** `Mcp-Method` and `MCP-Protocol-Version` are set and `Mcp-Name` is set for `tools/call`/`resources/read`/`prompts/get`.
2. **Given** a client request whose `MCP-Protocol-Version` header disagrees with its body `_meta`, **When** mcpproxy validates it, **Then** mcpproxy returns the header-mismatch error and does not forward the call.
3. **Given** a tool whose input schema declares `x-mcp-header` param mirroring, **When** that tool is called, **Then** mcpproxy mirrors the designated params into `Mcp-Param-*` headers (Base64-encoding non-ASCII values) before forwarding upstream.
4. **Given** a tool advertising an invalid `x-mcp-header` declaration, **When** mcpproxy surfaces it, **Then** that tool is rejected/flagged rather than passed through.

---

### User Story 3 - Stateless operation coexists with per-identity tool curation (Priority: P1)

mcpproxy operates without server-side sessions (no `Mcp-Session-Id`) while preserving its agent-token scoping and multiuser tool filtering, honoring the new rule that `*/list` results must not vary per connection.

**Why this priority**: This is the central architectural tension of the upgrade. mcpproxy's value (token-scoped tools, per-user curation) depends on per-identity views, yet the new spec forbids per-connection list variation. Resolving it correctly is essential and high-risk.

**Independent Test**: Two agent tokens with different server scopes connect concurrently. Confirm each sees only its permitted tools when discovering/calling, that no `Mcp-Session-Id` is required or relied upon, and that the canonical `tools/list` facade itself is identical across connections (variation is driven by identity in `_meta`/headers/dynamic discovery, not by connection state).

**Acceptance Scenarios**:

1. **Given** the default proxy mode (static `retrieve_tools` + `call_tool_*` facade), **When** any two clients call `tools/list`, **Then** they receive the same canonical list (no per-connection variation).
2. **Given** two agent tokens with different scopes, **When** each performs identity-scoped discovery/calls, **Then** each is restricted to its permitted servers/tools, derived from request-carried identity rather than session state.
3. **Given** any client, **When** it relies on a session id or a long-lived GET stream, **Then** mcpproxy operates correctly without them (stateless), and a `GET`/`DELETE` to the MCP endpoint returns the spec-mandated method-not-allowed response.

---

### User Story 4 - Multi-round-trip input replaces server-initiated sampling/elicitation (Priority: P2)

When an upstream needs additional input mid-call (formerly via server-initiated sampling/elicitation over SSE), mcpproxy bridges the new Multi Round-Trip Request (MRTR) pattern between client and upstream.

**Why this priority**: mcpproxy does not proxy sampling/elicitation today, so this is forward-looking rather than fixing a current behavior. It matters for completeness and for future upstreams that require input, but it is not on the critical path for the core proxy function.

**Independent Test**: Drive a call against an upstream that returns an `input_required` result; confirm mcpproxy relays the `inputRequests` to the client and replays the original call with the client's `inputResponses` and echoed `requestState` under a fresh request id.

**Acceptance Scenarios**:

1. **Given** an upstream returning `resultType: "input_required"`, **When** mcpproxy receives it, **Then** it surfaces the `inputRequests` to the originating client.
2. **Given** a client supplying `inputResponses`, **When** mcpproxy continues the call, **Then** it forwards them with the echoed `requestState` under a new request id and returns the final result.

---

### User Story 5 - Adopt additive features that amplify mcpproxy's value (Priority: P2)

mcpproxy emits the new caching and ordering hints and modern schema defaults so connected clients get measurably better token efficiency and observability.

**Why this priority**: These are the upside of the upgrade — directly reinforcing mcpproxy's headline "massive token savings" promise — but they layer on top of the breaking-change work and can ship incrementally.

**Independent Test**: Inspect `tools/list`/`resources/read` responses for `ttlMs` + `cacheScope` (`CacheableResult`), confirm deterministic tool ordering across repeated calls, confirm schemas validate as JSON Schema 2020-12, and confirm trace context from request `_meta` appears in activity-log records.

**Acceptance Scenarios**:

1. **Given** a list/read response, **When** a client receives it, **Then** it carries `ttlMs` and a `cacheScope` of `public` or `private` appropriate to the content's identity-sensitivity.
2. **Given** repeated `tools/list` calls with unchanged state, **When** results are compared, **Then** tool ordering is deterministic.
3. **Given** a request carrying W3C/OTel trace context in `_meta`, **When** mcpproxy records the activity, **Then** the trace identifiers are captured and correlated across the proxy hop.
4. **Given** tool input/output schemas, **When** validated, **Then** they conform to JSON Schema 2020-12 as the default dialect.

---

### Edge Cases

- A client mixes a new-style `_meta` version with a legacy `initialize` in the same session — mcpproxy must pick one coherent path and not double-handshake.
- An upstream still speaks only `2025-11-25` while the connecting client speaks `2026-07-28` (or vice versa) — mcpproxy must translate version, headers, and error codes across the mismatch rather than forwarding incompatible framing.
- A resource-not-found condition must surface the new `-32602` (Invalid Params) code rather than the retired `-32002`, including when translating an upstream that still returns `-32002`.
- A `subscriptions/listen` request arrives for resource updates where mcpproxy previously relied on `resources/subscribe` — subscription routing must map to the new mechanism (or degrade gracefully if unsupported).
- Header values containing non-ASCII characters must be Base64-encoded per the `x-mcp-header` rules; malformed encodings must be rejected, not forwarded raw.
- A deprecated capability (roots/sampling/logging) or the deprecated HTTP+SSE 2024-11-05 transport is still in use — mcpproxy must continue to honor it for the deprecation window while not advertising it as preferred.

## Requirements *(mandatory)*

### Functional Requirements

> **Scope note (2026-08-24, post-gate):** mcp-go v1.0.0-beta.1 itself implements protocol-era detection, `server/discover`, `GET`/`DELETE` endpoint handling, MRTR **endpoints on both sides**, and legacy byte-compatibility (conformance-tested upstream). FR-001–FR-006 and FR-014 are therefore **adopt-and-verify** requirements — configure the library correctly and prove the behavior with mcpproxy-level tests — rather than build-from-scratch work. **FR-015/FR-016 are NOT adopt-and-verify.** What the library ships is MRTR *participation*, not MRTR *bridging*: the server side can RAISE an `input_required` result (`mcp.NewInputRequiredResult`, `InputRequiredResult`, `MultiRoundTripResult` embedded in `CallToolResult`/`GetPromptResult`/`ReadResourceResult`), and the client side can ANSWER one locally (`client/mrtr.go` gathers responses from handlers and re-sends them with the echoed `requestState`). A proxy must do neither: it has to RELAY an upstream's `input_required` outward without answering it, and carry the client's `inputResponses` back to the upstream under a new request id. No primitive in beta.1 does that, and `requestState` is opaque and upstream-scoped, so whether it can be handed to the client verbatim or must be wrapped in a proxy-side token is an open design question for plan.md. mcpproxy-specific engineering therefore concentrates on FR-007–FR-010 (proxy header forwarding on both hops), FR-012/FR-013 plus the Spec-057 Option A/B decision (see Cross-Spec Reconciliation), FR-015/FR-016 (MRTR relay + continuation), FR-019 (`WithCacheHints`/`WithMethodCacheHints` configuration), FR-022 (trace context → activity log), FR-025, and FR-028.

**Protocol negotiation & handshake**

- **FR-001**: mcpproxy MUST advertise `2026-07-28` as a supported protocol version to connecting clients and MUST continue to support `2025-11-25` during the deprecation window.
- **FR-002**: mcpproxy MUST implement the `server/discover` RPC, returning supported versions, server capabilities, and server identity.
- **FR-003**: mcpproxy MUST accept per-request identity/version metadata (`_meta`: protocolVersion, clientInfo, clientCapabilities) in lieu of an `initialize`/`initialized` handshake for `2026-07-28` clients.
- **FR-004**: mcpproxy MUST retain backward-compatible handling of the legacy `initialize`/`initialized` handshake for clients that still use it.
- **FR-005**: mcpproxy MUST negotiate protocol versions independently on its client-facing side and its upstream-facing side, translating between versions when they differ.
- **FR-006**: mcpproxy MUST return the spec-defined unsupported-protocol error (`-32022`, `UNSUPPORTED_PROTOCOL_VERSION`) when a client requests a version it cannot serve. *(Final spec renumbering: `2026-07-28` reserves `-32020`…`-32099` for MCP-defined errors — `-32020` header mismatch, `-32021` missing required client capability, `-32022` unsupported protocol version.)*

**Routing headers & metadata**

- **FR-007** *(amended 2026-09-03)*: mcpproxy MUST set `Mcp-Method` and `MCP-Protocol-Version` on every **request** it forwards upstream, and `Mcp-Name` on `tools/call`, `resources/read`, and `prompts/get`. **Notifications are excluded**: `StreamableHTTP.SendNotification` passes a nil header (`client/transport/streamable_http.go:915-925`), `mcp.JSONRPCNotification` has no header field, and mcpproxy originates no client-side notifications — so the original "request/notification" wording was unsatisfiable and untestable. Revisit if the library grows a notification header path.
- **FR-008**: mcpproxy MUST validate that incoming `MCP-Protocol-Version` (and related required headers) match the request body `_meta`, returning the header-mismatch error (`-32020`, `HEADER_MISMATCH` per SEP-2243 — the RC-era `-32001` was renumbered in the final spec) on disagreement and not forwarding the request.
- **FR-009**: mcpproxy MUST support `x-mcp-header` param-to-header mirroring, copying designated tool params into `Mcp-Param-*` headers and Base64-encoding non-ASCII values.
- **FR-010**: mcpproxy MUST reject or flag tools that declare an invalid `x-mcp-header` mapping rather than forwarding them.

**Stateless operation**

- **FR-011**: mcpproxy MUST operate without server-side sessions and MUST NOT require or rely on `Mcp-Session-Id`.
- **FR-012**: mcpproxy MUST ensure `tools/list`, `resources/list`, and `prompts/list` responses do not vary per connection; any identity-scoped view MUST be driven by request-carried identity (token/headers/`_meta`) or dynamic discovery, not connection state.
- **FR-013**: mcpproxy MUST preserve agent-token scoping and multiuser tool filtering under the stateless model.
- **FR-014**: mcpproxy MUST respond to `GET`/`DELETE` on the MCP endpoint with the spec-mandated method-not-allowed behavior and MUST NOT depend on a long-lived GET stream or `Last-Event-ID` resumption.

**MRTR (multi round-trip input)**

> **Amended 2026-09-03 — relay deferred, detect-and-frame adopted.** MRTR relay **cannot be built on mcp-go v1.0.0's public client API**. `client.CallTool` is hard-wired to an internal multi-round-trip loop (`client/client.go:631-643`, `client/mrtr.go:56-103`) that both swallows `input_required` results and overwrites any caller-supplied `inputResponses`/`requestState`; the single-shot entry points (`callToolOnce`, `sendRequest`) are unexported. A proxy must neither answer an input request itself nor let the library answer it, so there is no code path that satisfies the original FR-015/FR-016 short of reimplementing the call path. mcpproxy also proxies no elicitation/sampling/roots handlers today, so nothing regresses by deferring. Two of three independent design reviews chose deferral over a relay built on library internals.

- **FR-015** *(amended)*: mcpproxy MUST detect an upstream `input_required` result and return a **structured, machine-readable `isError` notice** to the originating client (error type, tool, input kind, hint), rather than forwarding a result the client cannot act on or silently dropping the marker. Upstream free text and upstream-authored input-request ids MUST NOT appear in the notice body.
- **FR-016** *(amended)*: mcpproxy MUST reject an unsolicited continuation — a `call_tool_*`, direct-surface, or legacy `call_tool` request carrying `inputResponses`/`requestState` — pre-dispatch, rather than forwarding it upstream. The guard applies to those surfaces only, so a future built-in may legitimately use MRTR.
- **FR-016a** *(deferred to a follow-up spec)*: true relay and continuation. Design of record: a proxy-owned sealed envelope (`mpx1.<b64url(payload)>.<b64url(tag)>`, HMAC-SHA256, tool binding, args fingerprint) rather than passing the upstream `requestState` verbatim, because a verbatim relay lets a client redirect a continuation to a different upstream. Gated on mcp-go exporting a single-shot `CallTool` that accepts `mcp.MultiRoundTripParams` plus a typed input-required error; file those upstream issues before starting.

**Sibling requirement surfaced by the same analysis:**

- **FR-016b** *(added 2026-09-03, belongs to the library bump)*: mcpproxy MUST NOT copy an upstream `CallToolResult`'s embedded result envelope wholesale into its own response. `forwardContentResult` (`internal/server/content_forward.go:130-131`) and its direct-surface twin (`mcp_routing.go:595-596`) do exactly that; under v1.0.0 the struct gains `resultType` and the MRTR fields, so an upstream's `input_required` marker would reach the client as though mcpproxy had produced it. Copy the intended fields explicitly.

**Subscriptions & error codes**

- **FR-017**: mcpproxy MUST route resource update subscriptions via `subscriptions/listen` instead of `resources/subscribe`/`unsubscribe`.
- **FR-018** *(amended 2026-09-03 — vacuous, no work in this spec)*: the requirement stands as written for any future resource proxying, but **mcpproxy proxies no resources today**, so there is nothing to translate. Verified: the only `AddResource` call in the tree is the JSON-schema compiler (`internal/server/mcp_input_validation.go:144`); grep for `-32002`/`RESOURCE_NOT_FOUND` across non-test code is empty; and the sole JSON-RPC-code seam (`classifyUpstreamInvalidParams`, `internal/server/mcp.go:5760-5778`) matches a type whose constructors have no callers. Implementing a translation layer now would produce untestable dead code. Re-open together with FR-017 if resource proxying is ever added.

**Additive feature adoption**

- **FR-019**: mcpproxy MUST include `ttlMs` and a `cacheScope` (`public`/`private`) on list/read responses, choosing `private` for identity-scoped content and `public` for shared content (configured via mcp-go's `WithCacheHints`/`WithMethodCacheHints` server options).
- **FR-020**: mcpproxy MUST produce deterministic ordering for `tools/list` results given unchanged state.
- **FR-021**: mcpproxy MUST treat tool input/output schemas as JSON Schema 2020-12 by default and MUST NOT auto-dereference external `$ref`s.
- **FR-022**: mcpproxy MUST capture W3C/OTel trace context (`traceparent`/`tracestate`/`baggage`) from request `_meta` into activity-log records for cross-hop correlation.
- **FR-023**: mcpproxy SHOULD expose the now-spec-blessed `serverName:toolName` naming as its canonical disambiguation format (already its convention).

**Deprecations**

- **FR-024**: mcpproxy MUST continue to honor deprecated capabilities (roots, sampling, logging) and the deprecated HTTP+SSE 2024-11-05 transport for the spec's deprecation window, while not advertising them as preferred.
- **FR-025**: mcpproxy MUST move per-request log level handling to the new `_meta` log-level mechanism for `2026-07-28` clients.

**Compatibility & rollout**

- **FR-026**: The personal edition's existing tool-discovery/curation behavior MUST remain functionally unchanged from the connecting agent's perspective after the upgrade.
- **FR-027** *(amended 2026-09-03 — now names both hops)*: the codebase MUST NOT switch **either** negotiated default — client-facing or upstream-facing — to `2026-07-28` until that support exists and is verified for that hop. This is a substantive change, not a clarification: the original wording said "its negotiated default" as though there were one, and `internal/upstream/core/connection_lifecycle.go:19` sends `mcp.LATEST_PROTOCOL_VERSION`, which v1.0.0 redefines to `2026-07-28`. Under the original reading, **the library bump alone would have violated this requirement** by silently upgrading every upstream hop. The library-bump PR MUST therefore pin the upstream `initialize` to `mcp.LATEST_LEGACY_PROTOCOL_VERSION` alongside FR-028's client-facing pin, and each pin MUST be lifted in its own change with its own acceptance evidence. *(Availability satisfied by mcp-go v1.0.0 stable as of 2026-09-02.)*
- **FR-028** *(added 2026-08-24 — safe intermediate state)*: Until the Spec-057 Option A/B reconciliation is implemented and its acceptance tests pass, the client-facing Streamable HTTP transport MUST be pinned to legacy protocol versions only via `server.WithStreamableHTTPProtocolVersions(mcp.LegacyProtocolVersions()...)`. A v1.0.0-beta.x pin of `mcp-go` MUST NOT merge to main without either mcp-go v1.0.0 stable or this pin in place.

### Key Entities

- **Protocol Version**: An identifier (e.g., `2026-07-28`, `2025-11-25`) negotiated separately on the client-facing and upstream-facing sides; mcpproxy may bridge two different versions on a single proxied call.
- **Request Metadata (`_meta`)**: Per-request envelope carrying protocol version, client identity/capabilities, log level, trace context, and (for MRTR) `requestState` — replacing the prior connection-level handshake.
- **Required Headers**: `Mcp-Method`, `Mcp-Name`, `MCP-Protocol-Version`, plus mirrored `Mcp-Param-*`; proxy-owned on every hop.
- **CacheableResult**: List/read result attributes (`ttlMs`, `cacheScope`) advising clients how long and how widely a result may be cached.
- **Input-Required Result**: An upstream response (`resultType: "input_required"`) carrying `inputRequests`. In this spec it is **detected and reported**, not resolved (FR-015/FR-016 as amended); the client follow-up with `inputResponses` + echoed `requestState` belongs to the deferred FR-016a.
- **Identity Scope**: The agent-token or user identity that determines which servers/tools a caller may see/use — now derived per-request rather than per-session.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: A `2026-07-28` client and a `2025-11-25` client can both connect to the same mcpproxy instance and successfully discover and call tools, with zero regressions in the legacy client's behavior.
- **SC-002**: 100% of requests forwarded upstream carry correct `Mcp-Method`, `MCP-Protocol-Version`, and (where applicable) `Mcp-Name` headers, verified across all proxied method types.
- **SC-003**: Header/body version mismatches are rejected in 100% of injected-mismatch test cases with the correct error, and never forwarded.
- **SC-004**: Under concurrent connections from differently-scoped agent tokens, each caller sees only its permitted tools while the canonical list facade is byte-identical across connections, with no reliance on session ids.
- **SC-005** *(amended 2026-09-03)*: **not applicable while mcpproxy proxies no resources** — see FR-018. There is no resource-not-found path to measure; a "100% of cases" figure over an empty set would be a false green.
- **SC-006** *(amended 2026-09-03)*: list/read responses carry valid cache hints (`ttlMs` plus a `cacheScope` appropriate to the content's identity-sensitivity), asserted directly on the responses. The original second clause — a measured reduction in repeated client-side metadata fetches — is **withdrawn as unmeasurable from mcpproxy's side**: the fetch reduction happens in the client's cache, there is no pre-upgrade baseline recorded, and no telemetry counts repeated `tools/list` calls per client. Re-add it only with a funded bench cell that drives a cache-honouring client, since a criterion nothing can evaluate is worse than no criterion.
- **SC-007**: Trace context provided by a client is present and correlated in the corresponding activity-log records 100% of the time.
- **SC-008**: The full existing test suite (unit, race, API E2E, OAuth E2E) passes on both editions after the upgrade, and the personal edition's negotiated default does not change until library support is confirmed.

## Assumptions

- **Dual-version support over hard cutover**: mcpproxy will support both `2026-07-28` and `2025-11-25` simultaneously during the deprecation window rather than dropping the old version immediately, because a proxy must not strand clients on either side. (Reasonable default; revisit at plan time if the maintenance cost proves too high.)
- **Library-first adoption**: The upgrade rides on `mcp-go` adding `2026-07-28`; mcpproxy will not hand-roll the protocol primitives unless the library stalls indefinitely. Hand-rolling is explicitly out of scope for this spec.
- **mcpproxy remains tool-centric**: Resources/prompts/sampling proxying stays as-is (largely unimplemented); MRTR and subscription work is scoped to correct framing, not to newly proxying capabilities mcpproxy doesn't proxy today.
- **`cacheScope` selection**: identity-scoped content defaults to `private`; globally shared content defaults to `public`.
- **RC may shift before final — RESOLVED (2026-08-24)**: The `2026-07-28` revision went FINAL on 2026-07-28. This spec's FRs have been re-validated against the final spec as implemented by mcp-go v1.0.0-beta.1; the RC→final deltas affecting this document were the error-code renumbering now encoded in FR-006/FR-008/FR-018.

## Risks & Watch Items *(added 2026-08-24)*

- **OAuth "authorization hardening" churn between beta.1 and v1.0.0**: mcp-go v1.0.0-beta.1 explicitly left the OAuth surface untouched, but names authorization hardening (issuer-keyed credentials, CIMD) as the next v1.x workstream. mcpproxy's OAuth integration is load-bearing there — `internal/oauth/persistent_token_store.go` (implements `client.TokenStore`), `internal/oauth/config.go` (`client.OAuthConfig`), and the three near-duplicate authorize-URL emission paths in `internal/upstream/core/connection_oauth.go`. Mitigation: keep the feature branch rebased onto each beta and re-run the `internal/oauth` test suite on every library bump.
- **Frozen tool-surface goldens**: in beta.1 `CallToolResult` gains `resultType` (via the embedded `mcp.Result`) and the MRTR fields (via the embedded `MultiRoundTripResult`) — but **not** `ttlMs`/`cacheScope`. Those two live on `mcp.CacheableResult`, which is embedded only by `PaginatedResult` (hence every `*/list`), `ReadResourceResult` and `DiscoverResult` — matching FR-019's "list/read responses" scope exactly. Legacy-era responses omit `resultType`. The three frozen tool-surface golden tests must be re-verified as part of the dependency bump.
- **Schema dialect shift**: Spec 056's output-schema validation must be re-verified under JSON Schema 2020-12 as the default dialect (FR-021).
- **Beta churn**: watch for v1.0.0-beta.2+ and v1.0.0 stable; re-run the compile/test probe on each bump before rebasing the branch.

## Out of Scope

- Newly implementing upstream resource/prompt/sampling proxying that mcpproxy does not currently provide.
- Adopting the Tasks and MCP Apps extensions (tracked separately as future opportunities, not part of the core protocol upgrade).
- Hand-rolling protocol primitives ahead of `mcp-go` support.
- Migrating clients off deprecated capabilities on their behalf (mcpproxy honors them for the window; client migration is the client's responsibility).

## Commit Message Conventions *(mandatory)*

When committing changes for this feature, follow these guidelines:

### Issue References
- ✅ **Use**: `Related #[issue-number]` - Links the commit to the issue without auto-closing
- ❌ **Do NOT use**: `Fixes #[issue-number]`, `Closes #[issue-number]`, `Resolves #[issue-number]`

**Rationale**: Issues should only be closed manually after verification and testing in production, not automatically on merge.

### Co-Authorship
- ❌ **Do NOT include**: `Co-Authored-By: Claude <noreply@anthropic.com>`
- ❌ **Do NOT include**: "🤖 Generated with [Claude Code](https://claude.com/claude-code)"

**Rationale**: Commit authorship should reflect the human contributors, not the AI tools used. (This repo also omits Claude attribution by project convention.)

### Example Commit Message
```
feat: implement server/discover and per-request _meta negotiation

Related #[issue-number]

Adds 2026-07-28 protocol negotiation alongside the legacy 2025-11-25 path.

## Changes
- server/discover RPC returning versions/capabilities/identity
- per-request _meta parsing for protocolVersion/clientInfo/clientCapabilities
- legacy initialize fallback retained

## Testing
- unit + race green on both editions
- API E2E green; legacy client regression suite green
```

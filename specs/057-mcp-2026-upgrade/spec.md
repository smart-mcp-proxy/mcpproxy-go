# Feature Specification: MCP Protocol Upgrade to the 2026-07-28 Spec Revision

**Feature Branch**: `057-mcp-2026-upgrade`
**Created**: 2026-05-28
**Status**: Draft (BLOCKED — see Dependency Gate)
**Input**: User description: "MCP protocol upgrade to the 2026-07-28 spec revision"

## Dependency Gate *(read first)*

This feature **cannot begin implementation** until the upstream MCP client/server library (`github.com/mark3labs/mcp-go`) ships support for protocol revision `2026-07-28`. As of 2026-05-28 the pinned library (v0.54.0) and the latest published release (v0.54.1) both target only `2025-11-25`; no `2026-07-28` constant exists in the library. This spec captures the full upgrade scope now so execution can start immediately once the gate clears. A scheduled tracking agent watches the library and the spec's RC→final status and notifies the maintainer when the gate opens.

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

**Protocol negotiation & handshake**

- **FR-001**: mcpproxy MUST advertise `2026-07-28` as a supported protocol version to connecting clients and MUST continue to support `2025-11-25` during the deprecation window.
- **FR-002**: mcpproxy MUST implement the `server/discover` RPC, returning supported versions, server capabilities, and server identity.
- **FR-003**: mcpproxy MUST accept per-request identity/version metadata (`_meta`: protocolVersion, clientInfo, clientCapabilities) in lieu of an `initialize`/`initialized` handshake for `2026-07-28` clients.
- **FR-004**: mcpproxy MUST retain backward-compatible handling of the legacy `initialize`/`initialized` handshake for clients that still use it.
- **FR-005**: mcpproxy MUST negotiate protocol versions independently on its client-facing side and its upstream-facing side, translating between versions when they differ.
- **FR-006**: mcpproxy MUST return the spec-defined unsupported-protocol error when a client requests a version it cannot serve.

**Routing headers & metadata**

- **FR-007**: mcpproxy MUST set `Mcp-Method` and `MCP-Protocol-Version` on every request/notification it forwards upstream, and `Mcp-Name` on `tools/call`, `resources/read`, and `prompts/get`.
- **FR-008**: mcpproxy MUST validate that incoming `MCP-Protocol-Version` (and related required headers) match the request body `_meta`, returning the header-mismatch error (`-32001`) on disagreement and not forwarding the request.
- **FR-009**: mcpproxy MUST support `x-mcp-header` param-to-header mirroring, copying designated tool params into `Mcp-Param-*` headers and Base64-encoding non-ASCII values.
- **FR-010**: mcpproxy MUST reject or flag tools that declare an invalid `x-mcp-header` mapping rather than forwarding them.

**Stateless operation**

- **FR-011**: mcpproxy MUST operate without server-side sessions and MUST NOT require or rely on `Mcp-Session-Id`.
- **FR-012**: mcpproxy MUST ensure `tools/list`, `resources/list`, and `prompts/list` responses do not vary per connection; any identity-scoped view MUST be driven by request-carried identity (token/headers/`_meta`) or dynamic discovery, not connection state.
- **FR-013**: mcpproxy MUST preserve agent-token scoping and multiuser tool filtering under the stateless model.
- **FR-014**: mcpproxy MUST respond to `GET`/`DELETE` on the MCP endpoint with the spec-mandated method-not-allowed behavior and MUST NOT depend on a long-lived GET stream or `Last-Event-ID` resumption.

**MRTR (multi round-trip input)**

- **FR-015**: mcpproxy MUST relay an upstream `input_required` result (with its `inputRequests`) to the originating client.
- **FR-016**: mcpproxy MUST continue a call by forwarding the client's `inputResponses` together with the echoed `requestState` under a new request id, returning the final result.

**Subscriptions & error codes**

- **FR-017**: mcpproxy MUST route resource update subscriptions via `subscriptions/listen` instead of `resources/subscribe`/`unsubscribe`.
- **FR-018**: mcpproxy MUST emit `-32602` for resource-not-found conditions and MUST translate an upstream's legacy `-32002` to `-32602` when bridging.

**Additive feature adoption**

- **FR-019**: mcpproxy MUST include `ttlMs` and a `cacheScope` (`public`/`private`) on list/read responses, choosing `private` for identity-scoped content and `public` for shared content.
- **FR-020**: mcpproxy MUST produce deterministic ordering for `tools/list` results given unchanged state.
- **FR-021**: mcpproxy MUST treat tool input/output schemas as JSON Schema 2020-12 by default and MUST NOT auto-dereference external `$ref`s.
- **FR-022**: mcpproxy MUST capture W3C/OTel trace context (`traceparent`/`tracestate`/`baggage`) from request `_meta` into activity-log records for cross-hop correlation.
- **FR-023**: mcpproxy SHOULD expose the now-spec-blessed `serverName:toolName` naming as its canonical disambiguation format (already its convention).

**Deprecations**

- **FR-024**: mcpproxy MUST continue to honor deprecated capabilities (roots, sampling, logging) and the deprecated HTTP+SSE 2024-11-05 transport for the spec's deprecation window, while not advertising them as preferred.
- **FR-025**: mcpproxy MUST move per-request log level handling to the new `_meta` log-level mechanism for `2026-07-28` clients.

**Compatibility & rollout**

- **FR-026**: The personal edition's existing tool-discovery/curation behavior MUST remain functionally unchanged from the connecting agent's perspective after the upgrade.
- **FR-027**: The upgrade MUST be gated behind the availability of `2026-07-28` support in the underlying MCP library; the codebase MUST NOT switch its negotiated default to `2026-07-28` until that support exists and is verified.

### Key Entities

- **Protocol Version**: An identifier (e.g., `2026-07-28`, `2025-11-25`) negotiated separately on the client-facing and upstream-facing sides; mcpproxy may bridge two different versions on a single proxied call.
- **Request Metadata (`_meta`)**: Per-request envelope carrying protocol version, client identity/capabilities, log level, trace context, and (for MRTR) `requestState` — replacing the prior connection-level handshake.
- **Required Headers**: `Mcp-Method`, `Mcp-Name`, `MCP-Protocol-Version`, plus mirrored `Mcp-Param-*`; proxy-owned on every hop.
- **CacheableResult**: List/read result attributes (`ttlMs`, `cacheScope`) advising clients how long and how widely a result may be cached.
- **Input-Required Result**: An upstream response (`resultType: "input_required"`) carrying `inputRequests`; resolved by a client follow-up with `inputResponses` + echoed `requestState`.
- **Identity Scope**: The agent-token or user identity that determines which servers/tools a caller may see/use — now derived per-request rather than per-session.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: A `2026-07-28` client and a `2025-11-25` client can both connect to the same mcpproxy instance and successfully discover and call tools, with zero regressions in the legacy client's behavior.
- **SC-002**: 100% of requests forwarded upstream carry correct `Mcp-Method`, `MCP-Protocol-Version`, and (where applicable) `Mcp-Name` headers, verified across all proxied method types.
- **SC-003**: Header/body version mismatches are rejected in 100% of injected-mismatch test cases with the correct error, and never forwarded.
- **SC-004**: Under concurrent connections from differently-scoped agent tokens, each caller sees only its permitted tools while the canonical list facade is byte-identical across connections, with no reliance on session ids.
- **SC-005**: Resource-not-found conditions surface the new error code in 100% of cases, including when bridging an upstream that returns the legacy code.
- **SC-006**: List/read responses carry valid cache hints, and clients observe a measurable reduction in repeated tool-metadata fetches versus the pre-upgrade baseline.
- **SC-007**: Trace context provided by a client is present and correlated in the corresponding activity-log records 100% of the time.
- **SC-008**: The full existing test suite (unit, race, API E2E, OAuth E2E) passes on both editions after the upgrade, and the personal edition's negotiated default does not change until library support is confirmed.

## Assumptions

- **Dual-version support over hard cutover**: mcpproxy will support both `2026-07-28` and `2025-11-25` simultaneously during the deprecation window rather than dropping the old version immediately, because a proxy must not strand clients on either side. (Reasonable default; revisit at plan time if the maintenance cost proves too high.)
- **Library-first adoption**: The upgrade rides on `mcp-go` adding `2026-07-28`; mcpproxy will not hand-roll the protocol primitives unless the library stalls indefinitely. Hand-rolling is explicitly out of scope for this spec.
- **mcpproxy remains tool-centric**: Resources/prompts/sampling proxying stays as-is (largely unimplemented); MRTR and subscription work is scoped to correct framing, not to newly proxying capabilities mcpproxy doesn't proxy today.
- **`cacheScope` selection**: identity-scoped content defaults to `private`; globally shared content defaults to `public`.
- **RC may shift before final**: The `2026-07-28` revision is a release candidate; details (field names, codes) may change before final. Plan/tasks should re-validate against the finalized spec when the gate clears.

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

# Feature Specification: In-Proxy Profiles + Permanent URLs

**Feature Branch**: `057-in-proxy-profiles`
**Created**: 2026-05-26
**Status**: Draft. Direction settled per maintainer review; awaiting implementation.
**Revision**: Incorporated review feedback from @Dumbris (2026-06-04): fixed `/mcp/all` factual error, added Implementation Design section, converted Open Questions to Resolved Design Decisions, tightened FR-011 metadata storage.
**Input**: Issue #55. Reporter @technicalpickles asked for two related capabilities: per-server `working_dir` (already shipped via `ServerConfig.WorkingDir`, related issue #333), and a way for different MCP clients to see different subsets of upstream servers from the same proxy instance. @Dumbris responded with the design "In-Proxy Profiles + Permanent URLs". @Melodeiro suggested an extension that mixes `server` and `server:tool` entries in profile lists.

> Scope note: this spec covers the **MVP** of profiles only. The MVP is a stateless URL-based selector. Active-profile switching, a tray selector, a `set_profile` MCP tool, and an indexable `profile` field are explicitly deferred (see [Out of Scope](#out-of-scope)). The deferred items depend on resolving where active-profile state lives (per-process, per-session, or per-token); that question is parked as out-of-scope for the MVP.

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Two clients, two profiles, one proxy (Priority: P1)

An operator uses a single MCPProxy instance to back several MCP clients on the same machine. Some clients work on a "research" set of servers (web search, scratch filesystem), others on a "deploy" set (GitHub, Kubernetes, internal CI). They want each client connected to a permanent URL that exposes only its profile's servers, rather than seeing the full union of every configured server.

**Why this priority**: This is the core ask in #55. Without it, every client sees every server, which leaks unrelated tools into each agent's context and makes BM25 retrieval less precise on a per-client basis. It is a pure addition with no impact on existing setups.

**Independent Test**: Configure two profiles (`research` and `deploy`) referencing distinct subsets of servers. Connect Client A to `/mcp/p/research` and Client B to `/mcp/p/deploy`. Confirm `retrieve_tools` on Client A only returns tools from `research`'s servers, and `call_tool_*` on Client A rejects calls into a `deploy`-only server. Confirm `/mcp` (no profile) still returns the full union.

**Acceptance Scenarios**:

1. **Given** a config with `profiles: [{name: "research", servers: ["web", "fs"]}, {name: "deploy", servers: ["github", "k8s"]}]`, **When** a client connects to `/mcp/p/research` and calls `retrieve_tools`, **Then** only tools from `web` and `fs` are returned.
2. **Given** the same config, **When** a client connects to `/mcp/p/research` and invokes `call_tool_read` for a tool on `github`, **Then** the call is rejected with a clear "server not in profile" error.
3. **Given** the same config, **When** a client connects to `/mcp` (no profile), **Then** `retrieve_tools` returns tools from all four servers (no behavioural change versus today).
4. **Given** a config with no `profiles` field at all, **When** a client connects to `/mcp` or to any `/mcp/p/<anything>` URL, **Then** `/mcp` behaves exactly as today and `/mcp/p/<slug>` returns 404 with a body indicating "no profiles configured".

---

### User Story 2 - Profile composes with agent token scope (Priority: P1)

An operator already issues scoped agent tokens (Spec 028) that limit which servers an agent may use. They want to layer profiles on top so a single agent token, used against different profile URLs, naturally narrows further by the URL it is presented at, without having to re-issue tokens. The agent's effective server set is the **intersection** of `AgentToken.AllowedServers` and the profile's `servers`.

**Why this priority**: Profiles and agent tokens are two independent scoping primitives the project already exposes. They MUST compose by intersection to remain predictable. Without this, operators have to choose between the two.

**Independent Test**: Issue an agent token with `allowed_servers: ["github", "fs", "web"]`. Define a profile `deploy` with `servers: ["github", "k8s"]`. Connect with the agent token to `/mcp/p/deploy` and confirm `retrieve_tools` returns only `github` tools (intersection: `{github, fs, web} ∩ {github, k8s} = {github}`). Confirm calls to `fs` and to `k8s` are both rejected, with errors that distinguish "out of token scope" from "out of profile scope".

**Acceptance Scenarios**:

1. **Given** an agent token with `allowed_servers=["github","fs","web"]` and a profile `deploy` with `servers=["github","k8s"]`, **When** the agent calls `retrieve_tools` against `/mcp/p/deploy`, **Then** only tools from `github` appear in the result.
2. **Given** the same token and profile, **When** the agent calls a tool on `fs` (in token scope, not in profile), **Then** the request is rejected and the error names the profile.
3. **Given** the same token and profile, **When** the agent calls a tool on `k8s` (in profile, not in token scope), **Then** the request is rejected and the error names the token.
4. **Given** an agent token with wildcard `allowed_servers=["*"]` and any profile `P`, **When** the agent connects to `/mcp/p/P`, **Then** the effective scope is exactly `P.servers` (the wildcard is fully constrained by the profile).

---

### User Story 3 - Per-tool curation inside a profile reuses existing controls (Priority: P2)

An operator wants finer-than-server granularity inside a profile. They already have `enabled_tools`/`disabled_tools` on each server entry from prior layered-config work. They want to reuse those without learning a second mechanism: a profile picks the servers, the existing per-server `enabled_tools`/`disabled_tools` filter the tools.

**Why this priority**: It avoids inventing a new tool-level field on `Profile` (e.g. `["server:tool"]` per @Melodeiro's comment) when the existing knobs already express it. It is a documentation/composition story rather than new mechanism, hence P2.

**Independent Test**: Configure a server `github` with `disabled_tools: ["delete_repo"]`. Add a profile referencing `github`. Connect to `/mcp/p/<profile>` and confirm `retrieve_tools` returns the rest of `github`'s tools but not `delete_repo`, and a direct call to `delete_repo` is rejected with the same error today's per-server denylist produces.

**Acceptance Scenarios**:

1. **Given** a server with `disabled_tools=["X"]` and a profile referencing that server, **When** a client lists tools via `retrieve_tools` at the profile URL, **Then** tool `X` is absent.
2. **Given** the same setup, **When** the client calls `X`, **Then** it is rejected by the existing per-server denylist (no profile-specific override).
3. **Given** a server with `enabled_tools=["X"]` (allowlist), **When** a profile references that server, **Then** the profile-scoped client sees only `X` (no implicit broadening).

---

### Edge Cases

- **Profile references an unknown server**: handled at config load as a validation **warning** (loaded, logged, server omitted from the profile's effective set), not a hard error, for parity with how unknown server references are handled in Spec 028 agent tokens. A config that stays loadable after a server rename is preferable to a hard break; the warning is visible enough to catch typos in practice.
- **Reserved or malformed slug**: a profile name that fails slug validation (see FR-007) is rejected at config load with a precise diagnostic. Reserved values: `all`, `code`, `call`, `p`. Reasoning: `all`, `code`, and `call` are already bound routing-mode subpaths under `/mcp/` (Spec 031, `internal/server/server.go:1670`); `p` is the profile prefix itself. These slugs are reserved to avoid operator confusion — note there is no actual path collision, since profiles live under the distinct `/mcp/p/` prefix.
- **Two profiles with the same name**: rejected at config load. Names are unique; the URL slug is derived directly from the name.
- **Profile referencing a quarantined server**: the server is excluded from the profile's effective set while it is quarantined, mirroring how agent tokens treat quarantined servers. Once unquarantined, it appears in the profile without re-reading the file.
- **Profile referencing a disabled server**: the server is excluded from the profile's effective set while disabled. This matches `/mcp` behaviour today and means a profile cannot "force-enable" a disabled server.
- **Empty `servers` list on a profile**: legal config (the URL exists but exposes zero tools) and useful as a "deny everything" placeholder, but emits a warning so the operator notices.
- **Config hot-reload changes a profile mid-connection**: existing client sessions keep their resolved profile snapshot until they reconnect (no live mutation of an in-flight session's allowed-server list), consistent with how the project already handles config hot-reload for active connections. New connections pick up the new profile.
- **Tool indexing**: BM25 search index is **not** partitioned per profile in the MVP. Filtering happens at `retrieve_tools` and `call_tool_*` time by intersecting the active profile's server set with the result. With server cardinality typically ≤ a few dozen, this filter is cheap and avoids an index-shape change.

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: System MUST accept an optional top-level `profiles` array in the config file, where each entry has a `name` (string) and a `servers` (string array) field. Absent / empty `profiles` MUST be a fully supported state (zero migration cost: behaviour unchanged from today).
- **FR-002**: System MUST expose, for each profile `P`, a stateless HTTP MCP endpoint at `/mcp/p/<P.name>` whose protocol surface is identical to `/mcp` except that the agent's effective server set is restricted to `P.servers`.
- **FR-003**: `/mcp/p/<slug>` MUST be a pinned, stateless selector. The proxy MUST NOT mutate any global "active profile" state when a request hits this URL. Two concurrent requests to two different profile URLs from the same client MUST each see only their own profile's servers.
- **FR-004**: `retrieve_tools`, `call_tool_read`, `call_tool_write`, `call_tool_destructive`, and the upstream-servers introspection tools MUST honour the active profile when the request enters via `/mcp/p/<slug>`. Tools the profile excludes MUST NOT appear in `retrieve_tools` results, and calls into excluded servers MUST be rejected with an error that names the profile.
- **FR-005**: When a request arrives via `/mcp/p/<slug>` and is also authenticated with an agent token (Spec 028), the effective allowed-servers set MUST be the intersection of `AgentToken.AllowedServers` and the profile's `servers`. A wildcard `["*"]` on the token MUST be fully constrained by the profile.
- **FR-006**: Per-server `enabled_tools` / `disabled_tools` (already in `ServerConfig`) MUST continue to apply inside a profile-scoped request. The profile MUST NOT introduce a parallel per-tool list; tool-level filtering remains a server-level concern.
- **FR-007**: Profile names MUST validate as URL-safe slugs: regex `^[a-z0-9][a-z0-9_-]{0,62}$` (lowercase, digits, hyphen, underscore; up to 63 chars; must start with a letter or digit). The slug is the profile name verbatim, with no transformation. Reserved slugs that MUST be rejected at load time: `all`, `code`, `call`, `p`.
- **FR-008**: When `profiles` is empty or absent, requests to any `/mcp/p/<anything>` MUST return HTTP 404 with a JSON body indicating no profiles are configured. This is to surface misconfiguration rather than silently fall back to `/mcp`.
- **FR-009**: When `profiles` is non-empty but a request targets a slug that does not match any profile, the response MUST be HTTP 404 with a JSON body listing the available profile names.
- **FR-010**: `/mcp` (no profile) MUST continue to expose the full union of configured servers, exactly as today, regardless of whether profiles are configured. Profiles do not implicitly redefine `/mcp`.
- **FR-011**: System MUST log, in the existing activity log, the effective profile slug on tool-call activity records originating from a `/mcp/p/<slug>` URL, so operators can correlate activity to a profile. The slug lands in the existing `ActivityRecord.Metadata` map as `metadata["profile"]`, not a new top-level field — no schema change needed, matching how Specs 018/026 attach context. Records from `/mcp` MUST continue to omit the field.
- **FR-012**: Errors caused by profile filtering MUST distinguish themselves from errors caused by agent-token scoping, so an operator can tell which scoping primitive blocked the call.
- **FR-013**: Behaviour MUST be identical across personal and server editions (no build-tag-specific code paths). In the server edition's multi-user mode, profiles compose with the per-user shared/personal server visibility (Spec 029) by intersection: a user only sees profile entries for servers they are entitled to.
- **FR-014**: Config validation MUST reject duplicate profile names with a clear diagnostic that points at both occurrences.
- **FR-015**: Config validation MUST emit a non-fatal warning when a profile references a server name that does not exist in the current `mcpServers` list, and MUST omit that server from the profile's effective set. Warn-and-skip is the settled decision (parity with Spec 028; config stays loadable after a server rename).

### Key Entities

- **Profile**: A named, stateless, server-scoped view of upstream MCP servers, addressable at `/mcp/p/<name>`. Fields: `name` (URL slug), `servers` (list of `mcpServers[].name` references). No tool-level fields in the MVP.
- **Effective Server Set**: At request time, the intersection of (a) the profile's `servers`, (b) the agent token's `allowed_servers` if a token is present, (c) servers that are not disabled and not quarantined, (d) the per-user visible server set in the server edition.
- **Profile-scoped MCP endpoint**: An HTTP route `/mcp/p/<slug>` that serves the existing MCP protocol, with the request's allowed-server filter pre-bound to the matched profile.

### Implementation Design

The spec says profiles are "wired into the existing scope hooks" but does not say *how*. This section closes that gap so an implementer does not pick a wrong seam.

#### Routing mechanism — middleware + context, not per-profile server instances

Routing modes (`/mcp/all`, `/mcp/code`, `/mcp/call`) register a *separate MCP server instance per path at startup* via `GetMCPServerForMode`. That pattern cannot be mirrored for N hot-reloadable profile slugs because `http.ServeMux` cannot de-register routes at runtime.

The framework-friendly fit is a single `/mcp/p/` prefix handler that:

1. Strips the slug from the URL path.
2. Looks up the profile in the current config.
3. Injects the resolved server set into the request context.

This mirrors how `mcpAuthMiddleware` injects `AuthContext` today. The existing MCP server instance is reused — no per-profile server construction. Middleware order: **auth → profile** (profile filtering runs after authentication so it can compose with agent-token scope via intersection).

#### Profile filtering MUST run independently of auth type (correctness-critical)

The existing server-scope filter in `retrieve_tools` (`mcp.go` ~1108) and `call_tool_*` (`mcp.go` ~1491) is gated on:

```go
enforceAgentScope := authCtx != nil && !authCtx.IsAdmin()
```

A default `/mcp/p/...` connection with **no token** is assigned `AdminContext()`, for which `enforceAgentScope` is `false` and `CanAccessServer` returns `true` unconditionally. Therefore profile filtering **cannot** ride the agent-scope gate or be implemented by stuffing the profile's servers into `AuthContext.AllowedServers`. It must be a *parallel* check that runs for every auth type:

```go
if profileScope != nil && !profileScope.Allows(serverName) {
    // hide from retrieve_tools / reject with profile error
}
if enforceAgentScope && !authCtx.CanAccessServer(serverName) {
    // existing token gate
}
```

Two independent checks yield the intersection (FR-005) and the two distinct error messages (FR-012) for free, with **no change to `AuthContext`, the `agent_tokens` bucket, or token validation**.

> **Regression test (mandatory)**: "An unauthenticated connection at `/mcp/p/<slug>` is still filtered to the profile's servers." This test MUST pass before the implementation PR merges.

#### Config store + reload

Top-level `profiles` array in the config file, hot-reloaded alongside `mcpServers`:

```go
Profiles []ProfileConfig `json:"profiles,omitempty"`
```

When the field is absent, byte-identical round-trip is preserved (SC-004 for free). Hot-reload updates the in-memory profile map atomically; existing connections keep their resolved snapshot until reconnect (consistent with how the project handles config hot-reload for active connections today).

#### Files touched (scope guard)

| Layer | File | Change |
|-------|------|--------|
| Config + validation | `internal/config/` (+ new `profiles.go`) | `ProfileConfig` struct, slug validation, duplicate detection, unknown-server warning |
| Request context | new `internal/profile/context.go` (~30 lines) | Mirrors `auth/context.go`; `ProfileScope` with `Allows(serverName)` predicate |
| Routing | `internal/server/server.go` | One route (`/mcp/p/`) + `profileMiddleware` (auth → profile order) |
| Filtering | `internal/server/mcp.go` | Two filter conditions in `retrieve_tools` and `call_tool_*` + metadata write |

Explicitly **no** storage, index, or token-model changes. Per-server `enabled_tools` / `disabled_tools` need zero changes — they already apply downstream of the server gate, so FR-006 / US3 works automatically.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: With two profiles configured, two MCP clients connected to two different profile URLs from the same proxy each see, via `retrieve_tools`, only their own profile's tools. Verified by an integration test.
- **SC-002**: Adding profiles to the config has zero measurable effect on the protocol behaviour of `/mcp`, `/mcp/code`, and `/mcp/call` for clients that do not opt in to a profile URL. Verified by re-running the existing E2E suite unchanged.
- **SC-003**: A request authenticated with an agent token against a profile URL is filtered by the intersection of token scope and profile scope. Both "blocked by token" and "blocked by profile" responses identify the responsible primitive in their error message. Verified by a unit test on the policy layer.
- **SC-004**: A config with `profiles` absent loads byte-for-byte identically to a config without the field (no spurious diff on round-trip via the config writer).
- **SC-005**: Profile name validation rejects every reserved slug (`all`, `code`, `call`, `p`), every non-conforming slug (uppercase, leading hyphen, longer than 63 chars), and every duplicate, with diagnostics that point at the offending profile entry.
- **SC-006**: Filtering overhead at `retrieve_tools` time is below the existing E2E latency budget for `retrieve_tools`. Profile filtering is an O(servers-in-profile) set lookup over an already-paginated result, so no new latency budget is introduced.

## Assumptions

- **Stateless first**. The MVP is intentionally stateless (URL-bound). This avoids deciding where "active profile" state lives (per-process, per-session, per-token) which is the open question that has stalled #55 for several months. URL-bound profiles let the feature ship without that decision.
- **Filter at the call site, do not partition the index**. With server cardinality typically in the dozens, a per-request server-set filter on `retrieve_tools` results is cheap. Adding a `profile` field to the BM25 documents and reshaping queries was considered and rejected for the MVP because it complicates indexing, hot reload, and migration without a measurable benefit at current scale (Constitution III: read state from the source of truth, do not duplicate it).
- **Reuse existing tool-level controls**. `enabled_tools` / `disabled_tools` on each `ServerConfig` already give per-tool granularity. Re-implementing it on `Profile` (e.g. accepting `["server:tool"]` strings as @Melodeiro proposed) would create a second mechanism with different precedence rules; this MVP keeps profiles purely server-level.
- **`/mcp` semantics stay**. Today `/mcp` is the union of all configured servers. This spec keeps that as a settled invariant — profiles are purely additive. `/mcp` always exposes the full union regardless of whether profiles exist.
- **`working_dir` is a separate concern.** Per-server `working_dir` (the other half of #55, related to #333) is already implemented and out of scope here.
- **Personal edition reads the file directly**, server edition resolves profiles after layering shared + personal server visibility (Spec 029). Both editions share one filter implementation; only the input "visible servers for this caller" differs.

## Resolved Design Decisions

The following points were initially left open for maintainer direction. Review feedback from @Dumbris settled all three.

1. **Unknown server reference → warn and skip (FR-015).** Promoted from "recommendation" to settled decision. Rationale: parity with Spec 028 agent tokens, and config must stay loadable after a server rename. Hard-error variant is not needed.
2. **`/mcp` semantics → always full union, purely additive.** Stated as a settled invariant: `/mcp` continues to expose all configured servers regardless of whether `profiles` is configured. Profiles are opt-in narrowing via the new `/mcp/p/<slug>` URL only. No breaking change for existing `/mcp` clients. A `strict` mode (requiring a profile) can be a later spec if anyone asks.
3. **Reserve `all` → yes**, for the corrected reason: `/mcp/all` is **already a live, bound endpoint** serving direct routing mode (Spec 031). The `all` slug is reserved to avoid operator confusion, not for a hypothetical future profile. There is no actual path collision since profiles live under the distinct `/mcp/p/` prefix.

## Out of Scope

The following items appeared in the #55 thread or in the broader profiles design space and are explicitly **deferred** to follow-up specs/PRs. Each has a concrete reason for not being in the MVP.

- **Active-profile switching at runtime** (per-process or per-session "current profile"). Defer reason: ownership of the active state is unresolved (process-global? per MCP session? per agent token?). The stateless `/mcp/p/<slug>` URL gives every benefit of profiles without picking a winner. Switching can be added later by introducing one of (a) a `set_profile` MCP tool that mutates a session-scoped variable, (b) a header/query selector on `/mcp`, or (c) a tray UI control. All three are non-trivial and orthogonal to the URL-based selector.
- **Tray selector / `set_profile` MCP tool**. Defer reason: directly depends on the active-profile state machine above.
- **Index-level `profile` field on tool documents**. Defer reason: with current server cardinality, a per-request set-intersection at the result step is sufficient. An index field would make hot reload, profile rename, and per-profile statistics significantly more complex without a present payoff.
- **`["server", "server:tool"]` mixed list on `Profile`** (per @Melodeiro). Defer reason: equivalent expressivity already exists via per-server `enabled_tools` / `disabled_tools`. Adding it on `Profile` introduces a second mechanism whose precedence ordering against the per-server lists would need to be specified and tested.
- **Per-profile API key / token binding** ("a token that auto-pins to profile X"). Defer reason: agent tokens (Spec 028) already pin scope. Coupling profile binding into the token would conflate two scoping primitives that compose cleanly today.
- **Per-profile activity-log filters in the web UI / CLI.** Defer reason: the activity log will already record the profile slug per FR-011, but UI filter affordances are a separate UX concern and ship better alongside Spec 019 (Activity Web UI) or Spec 017 (Activity CLI) extensions.

## Examples

### Example 1: minimal two-profile setup

```json
{
  "listen": "127.0.0.1:8080",
  "mcpServers": [
    { "name": "github", "url": "https://api.github.com/mcp", "protocol": "http" },
    { "name": "k8s",    "command": "kubectl-mcp",                "protocol": "stdio" },
    { "name": "fs",     "command": "fs-mcp",                     "protocol": "stdio" },
    { "name": "web",    "command": "web-search-mcp",             "protocol": "stdio" }
  ],
  "profiles": [
    { "name": "research", "servers": ["fs", "web"] },
    { "name": "deploy",   "servers": ["github", "k8s"] }
  ]
}
```

```bash
# Research client
curl -H "X-API-Key: $K" http://127.0.0.1:8080/mcp/p/research
# Deploy client
curl -H "X-API-Key: $K" http://127.0.0.1:8080/mcp/p/deploy
# Full union (unchanged)
curl -H "X-API-Key: $K" http://127.0.0.1:8080/mcp
```

### Example 2: profile composes with agent token

```json
{
  "profiles": [
    { "name": "deploy", "servers": ["github", "k8s"] }
  ]
}
```

```bash
# Token scoped to {github, fs, web}; profile scoped to {github, k8s}.
# Effective scope at /mcp/p/deploy is the intersection: {github}.
mcpproxy token create --name ci-bot \
  --servers github,fs,web \
  --permissions read,write \
  --expires 30d
# Use the printed token against the deploy profile
curl -H "Authorization: Bearer mcp_agt_..." \
     http://127.0.0.1:8080/mcp/p/deploy
```

### Example 3: profile + per-server tool denylist

```json
{
  "mcpServers": [
    {
      "name": "github",
      "url": "https://api.github.com/mcp",
      "protocol": "http",
      "disabled_tools": ["delete_repo", "force_push"]
    }
  ],
  "profiles": [
    { "name": "deploy", "servers": ["github"] }
  ]
}
```

A client at `/mcp/p/deploy` sees every `github` tool except `delete_repo` and `force_push`. The exclusion is enforced by the existing per-server denylist; the profile contributes server-level scoping only.

## Migration

There is no migration. The `profiles` field is optional and additive.

- A config without `profiles`: loaded identically to today. `/mcp`, `/mcp/code`, `/mcp/call` behave identically. `/mcp/p/<anything>` returns 404 with "no profiles configured".
- A config with `profiles`: `/mcp`, `/mcp/code`, `/mcp/call` behave identically (still full union). `/mcp/p/<slug>` is added.
- Removing a profile from the config while a client is connected to its URL: the client's in-flight session is allowed to drain (no abrupt disconnect); reconnect attempts return 404 listing the now-current profile names.

## Testing Strategy

- **Unit tests** (`internal/config`): slug validation (`^[a-z0-9][a-z0-9_-]{0,62}$` + reserved set), duplicate names, unknown server references warn-not-fail (FR-015), empty `servers` list warns, `profiles` round-trips through the writer with no diff.
- **Unit tests** (filter layer): given an effective server-set computed from (profile, agent token, quarantined, disabled), the policy decision matches table-driven expectations for every cell. Specifically asserts the two distinct error messages from FR-012 (token-blocked vs profile-blocked). **Named regression test**: "an unauthenticated connection at `/mcp/p/<slug>` is still filtered to the profile's servers" — this validates that profile filtering does not depend on the `enforceAgentScope` gate (see Implementation Design).
- **Integration tests** (`internal/server`): two profiles configured, an HTTP server stood up, and `retrieve_tools` / `call_tool_*` exercised against `/mcp`, `/mcp/p/research`, `/mcp/p/deploy`, `/mcp/p/unknown`, `/mcp/p/all` (reserved → 404 even if defined), `/mcp/p/Bad-Slug` (uppercase → not loaded).
- **E2E test** (`internal/server/e2e_test.go` style): a real proxy with two stub upstream servers, two profiles, and two MCP clients; verifies isolation in both directions plus activity-log records carrying the profile slug per FR-011.
- **Backward-compat E2E**: existing E2E suite passes unchanged when `profiles` is absent (SC-002).

## References

- Issue #55: original report (technicalpickles), design proposal (@Dumbris, "In-Proxy Profiles + Permanent URLs"), extension comment (@Melodeiro, mixed `server` / `server:tool` list).
- Issue #333: `working_dir` per server (related half of #55, already shipped via `ServerConfig.WorkingDir`).
- Spec 028 (`specs/028-agent-tokens/`): agent tokens; profile scope composes with `AgentToken.AllowedServers` by intersection (FR-005).
- Spec 029 (`specs/029-mcpproxy-teams/`): server edition multi-user; profiles compose with per-user visibility by intersection (FR-013).
- Spec 031 (`031-routing-modes` branch, merged in PR #327): `/mcp/{mode}` routing established `/mcp/all`, `/mcp/code`, and `/mcp/call` as live bound endpoints (`internal/server/server.go:1670`). This spec adds the orthogonal `/mcp/p/<slug>` axis. The reserved-slug list (`all`, `code`, `call`, `p`) is anchored on Spec 031's existing route prefixes and the profile prefix.
- Spec 049 (`specs/049-agent-discoverable-disabled-tools/`): established that per-server `enabled_tools` / `disabled_tools` are the canonical tool-level scoping mechanism. FR-006 reuses it rather than introducing a profile-level equivalent.
- PR #525 / Spec 056 (`specs/056-output-schema-validation/`): recent example of the project's spec-first pattern (spec PR merged separately from implementation PR). This spec follows the same pattern.

## Commit Message Conventions *(mandatory)*

- Use `Related #55` (never `Fixes/Closes/Resolves`).
- Do NOT include `Co-Authored-By: Claude` or "Generated with Claude Code" (per repo policy / memory `feedback_no_claude_git_attribution`).
- Conventional Commit prefixes enforced by commitlint (Spec 053 WP-C5): `docs(057): ...` for spec/plan, `feat(057): ...` for implementation, `test(057): ...` for tests.

### Example commit message

```
docs(spec): add spec 057 for in-proxy profiles

Related #55

Captures the design discussion from issue #55 into a reviewable
spec doc so the implementation can be reviewed in two stages
(spec first, code after), matching the pattern from spec 056.

Scope:
- profiles config + /mcp/p/{slug} pinned URL
- filter at retrieve_tools / call_tool_* hooks
- composes with agent-token AllowedServers (Spec 028) and
  per-user visibility (Spec 029) by intersection
- existing per-server enabled_tools / disabled_tools reused
  for tool-level filtering inside a profile

Out of scope (deferred to follow-ups):
- active profile switching (state ownership is an open question)
- tray UI / set_profile MCP tool
- index-level profile field
- mixed server / server:tool list per profile
```

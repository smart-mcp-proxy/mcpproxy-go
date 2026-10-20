# Feature Specification: Profile Pin as the Visibility Boundary for Agent Tokens

**Feature Branch**: `118-pin-visibility-boundary`
**Created**: 2026-10-20
**Status**: Draft
**Input**: GitHub issue [#1686](https://github.com/smart-mcp-proxy/mcpproxy-go/issues/1686) (profile-pinned agent tokens still enumerate out-of-pin servers via `/status`, `/events`, `/review`, `/attention`, `/profiles`, `/annotations/coverage`). Maintainer decision 2026-10-20: **Option A**, the operator-set pin is a visibility boundary on every read surface.

**Related**: Spec 028 (agent-token server scoping), Spec 057 (profiles, pin as highest resolver tier), Spec 107 T085 (profile reachability on `/profiles`), Spec 108 (profiles v3, client bindings, switchable mode), Spec 109-l (`?profile=` view-as), #1166/#1167 (one shared enumeration predicate for REST), #1437 (`/tools` held to the pin), #1682 / PR #1684 (`/servers` held to the pin), Spec 105 FR-005 (scoped counts never leak the fleet size).

All code references are to `origin/main` at `4e02c36a8` unless stated otherwise.

## Context and Motivation

An agent token created with `mcpproxy token create --profile <p>` takes its reach from the profile, so its server grant (`allowed_servers`) is normally `*`. On the MCP surface the pin is already the first and highest resolver tier: a pinned token's `retrieve_tools`, `describe_tool`, `call_tool_*`, `code_execution`, direct routing, `set_profile` and `upstream_servers list` are all bounded by the pin, and a pin whose profile was deleted resolves to a deny-all scope (`internal/server/profile_resolver.go:263-285`, `docs/features/agent-tokens.md` "Profile Pinning").

The REST surface answers a different question. Its one shared predicate, `canSeeServer` / `visibleServers` / `auth.CanEnumerateServer` (`internal/httpapi/scope.go:26-55`, `internal/auth/context.go:277-286`), consults only `allowed_servers`. The header comment of `internal/httpapi/scope_subtree.go` (lines 17-46) documents this as doctrine: the token grant is "the boundary", the profile is "ergonomic", and only preflight intersects the pin. Since #1437 and #1684, however, `GET /tools` and `GET /servers` do hold a pinned token to its pin. The codebase therefore gives two answers to "what may a pinned token learn exists?", and the per-endpoint patches leave every other read door open:

| # | Door | What a `*`-scoped pinned token learns today | Code |
|---|---|---|---|
| L1 | `GET /api/v1/status` | `upstream_stats.servers.<name>` for every configured server, plus `total_servers`, `connected_servers`, `total_tools` | `filterUpstreamStatsServers` (`internal/httpapi/scope.go:78-130`) narrows by `canSeeServer` only |
| L2 | `GET /events` (SSE) | the `servers.changed` embed (rows and stats), status frames, every server-named event (`server`, `server_name` fields) | `servers.changed` narrowing in `internal/httpapi/server.go:4661-4720`, `eventVisibleToCaller` and `isOutOfScopeIdentityField` (`internal/httpapi/sse_scope.go:263-320`) |
| L3 | `GET /api/v1/annotations/coverage` | server names with per-server tool counts | `handleAnnotationCoverage` (`internal/httpapi/server.go:7549`) |
| L4 | `GET /api/v1/review` | quarantined servers, servers with pending tools, the queue count | `internal/httpapi/review.go:47` |
| L5 | `GET /api/v1/attention` and the `attention.changed` SSE embed | attention items naming the server, the dashboard count | `internal/httpapi/attention.go:112` |
| L6 | `GET /api/v1/profiles`, `GET /api/v1/profiles/{name}`, `GET /api/v1/profiles/{name}/effective-tools`, `GET /api/v1/profiles/active` | other profiles' `servers` and `effective_servers`, server-qualified rules, `switchable_to`; per-server tool rows of any reachable profile; the server-level `active_profile` slug, which can name a profile outside the pin | `profileReachable` (`internal/httpapi/profiles.go:62-77`), `internal/httpapi/profiles_views.go:133-166`, `handleProfileEffectiveTools` (`internal/httpapi/profiles_crud.go:233`), `handleGetActiveProfile` (`internal/httpapi/profiles.go:31`, blanks the slug only for session principals) |
| L7 | per-server subtree routes (`/servers/{id}/...` incl. `/servers/{id}/diagnostics`, `/servers/{id}/review`, logs, tool history, and the security scan routes `/security/overview`, `/security/scans`, `/security/scans/{jobId}/report` in `internal/httpapi/security_scanner.go`) | existence confirmed by a non-404 answer, or a per-server row, for an out-of-pin server | every `canSeeServer` caller in `internal/httpapi/` (12 files) |
| L8 | list-shaped consumers that take the raw grant: `GET /api/v1/activity`, `/activity/summary`, `/activity/export`, `/activity/usage`, `GET /api/v1/tool-calls` | records for out-of-pin servers; the usage cache is keyed on the grant alone, so a pinned and an unpinned token with the same grant share an entry | `applyActivityScope` (`internal/httpapi/activity.go:161`), `parseUsageParams` and `usageParams.cacheKey` (`activity.go:1290`, `1244`), `/tool-calls` (`internal/httpapi/server.go:5426`), all via `scopeAllowedServers` (`scope_subtree.go:48-60`) |
| L9 | `GET /api/v1/diagnostics` and `/api/v1/doctor` | per-server errors, OAuth state and missing-secret findings for out-of-pin servers | `handleGetDiagnostics` (`internal/httpapi/server.go:1194-1195`) → `management.Doctor` → `auth.CanEnumerateServer` (`internal/management/diagnostics.go:139`), the one caller outside `internal/httpapi/` |
| L10 | `GET /api/v1/catalog/search` | `added` / `added_server_name` computed against every granted server | `getVisibleServersForCatalog` (`internal/httpapi/catalog.go:172-196`) |

**Reproduction** (from the issue): servers `a` and `b`, profile `prof` = `[a]`, `mcpproxy token create --profile prof`. `GET /api/v1/servers` with that token lists `a` only (after #1684); `GET /api/v1/status` and the `/events` stream still name `b`.

**Why it matters.** The pin is a restriction the operator applied. An agent holding a pinned token is, by the operator's intent, confined to one profile's servers. Learning the rest of the fleet (names, connection state, tool counts, quarantine state, other profiles' composition) is an inventory disclosure the operator did not grant, and it is exactly the disclosure #1166, Spec 105 FR-005 and Spec 107 T085 closed for `--servers` tokens. The two per-endpoint patches also mean the counts on one door (`/servers`) disagree with the counts on another (`/status`), so a token can diff them to learn how many servers are hidden.

## The Decision

The operator-set pin **is** a visibility boundary. What a pinned caller may learn exists is the intersection of its server grant **and** its pinned profile's effective server set, on every surface, computed in one place. The caller-selected `?profile=` view-as and the session `set_profile` selection remain ergonomic filters that can only narrow further. The MCP surface already behaves this way; this feature brings the REST and SSE surfaces to the same rule, and the CLI follows because it reads REST.

This supersedes the "scope vs profile" doctrine in the `scope_subtree.go` header. The doctrine's reason for not intersecting the profile was that the profile is caller-selectable. The pin is not: it is set by the operator at token creation, it cannot be changed by the token, and a token cannot even confirm it exists. Intersecting it can only narrow.

## Scope Boundary

| In scope | Out of scope (unchanged, or a follow-up) |
|---|---|
| One shared enumeration predicate that every REST read door, the SSE writer and every derived count answer from, now intersecting the caller's pin | The MCP-side resolver and `serverInScope` (`internal/server/mcp_visibility.go:292`). They already apply the pin; this feature adds a parity test, not a change |
| The ten door classes L1 to L10, including every SSE embed, every scalar count beside a filtered list, and the list-shaped consumers that take the grant as a filter | `allowed_servers` semantics for tokens with no pin. Nothing changes for `--servers` tokens, administrators, the tray socket or the Web UI session |
| Fail-closed behaviour for a pin naming a missing profile, an empty profile, or a profile with no overlap with the grant | The pin's effect on **calls** (`call_tool_*`, preflight tiering). Already enforced |
| `GET /profiles`, `/profiles/{name}`, `/profiles/{name}/effective-tools` and `/profiles/active` for a pinned caller: its own pin only, narrowed to the servers it may see, exactly as `set_profile` and `/mcp/p/<slug>` already treat a pinned token | Profile CRUD. Agent tokens cannot write profiles today and still cannot |
| Replacing the #1684 and #1437 per-endpoint filters with the shared predicate, or proving them equivalent | Client credentials (`mcp_cli_…`) on REST. REST rejects them with `403` today and keeps doing so |
| The decision recorded in `internal/httpapi/scope_subtree.go` and `docs/features/agent-tokens.md`; REST docs and swagger descriptions that describe scoped visibility | Server-edition multi-user entitlements (Spec 107). The pin intersects with them exactly as it intersects with `allowed_servers`; no edition-specific code |
| A regression test with a `*`-scoped pinned token across every door in the table, including the SSE `servers.changed` embed | The Web UI and macOS app. They authenticate as administrators and see no change |

**Editions.** Identical behaviour in both editions. A server-edition user whose entitlement and whose token pin both apply sees the intersection of all three (entitlement, grant, pin).

**Compatibility.** No wire format changes. Responses to pinned tokens shrink; responses to every other caller are byte-identical. A token minted before this change behaves as if minted after it. No config field is added.

## Definitions

- **Pin**: the profile name stored on an agent token at creation (`profile_pin`). Operator-set, immutable for the token's life, preserved across `token regenerate`. Not caller-selectable.
- **Pinned caller**: a non-administrator request whose credential carries a pin. Today only agent tokens created with `--profile` (`callerHasProfilePin`, `internal/httpapi/view_as.go:264`).
- **Grant**: the token's `allowed_servers` list; `*` means every server.
- **Reach of a pin**: the pinned profile's effective server set (`Profile.EffectiveServers(cfg)`), restricted to configured servers. A pin naming no configured profile has empty reach. A pin whose profile is empty, or names only unconfigured servers, has empty reach.
- **Visible set** of a caller: administrators, the tray socket and requests with no credential: every server. A non-administrator without a pin: the grant. A pinned caller: grant ∩ reach of the pin. This is the one boundary.
- **Ergonomic filter**: a further narrowing the caller or operator selects per request or per session: `?profile=` view-as on REST, `set_profile` and `/mcp/p/<slug>` on MCP. It never widens the visible set. For a pinned caller the only selectable profile is its own pin: every other slug is refused with the same non-disclosing answer an unknown slug gets, which is how `set_profile` and `/mcp/p/<slug>` already treat a pinned token (`docs/features/profiles.md`, "Which profile applies" and "404 responses").
- **Binding base**: for a switchable client credential, the profile it is bound to (`profile_pin` with `profile_mode: switchable`). Its reachable profiles are the base plus the base's `switchable_to`. Client credentials are rejected on REST (`rejectClientCredentialOnREST`, `internal/httpapi/server.go:716`), so the term matters only for the design note under FR-014.

## User Scenarios & Testing

### User Story 1: A pinned token learns nothing outside its pin, on any read door (Priority: P1)

An operator creates profile `prof` = `[a]` over a fleet `a`, `b`, `c` and mints a token with `--profile prof` for an agent. Whatever read door the agent uses over REST or SSE, it sees `a` and only `a`, and every count it is shown is computed over `a` alone.

**Why this priority**: this is the disclosure the issue reports and the whole point of Option A. Without it the pin is a routing hint, not a restriction.

**Independent Test**: start an isolated core with three stdio servers, one profile, one pinned token with grant `*`. For each door in the table, request as the token and assert that no response body, header or SSE frame contains `b` or `c`, and that every scalar count equals the count over `a`. Compare each body with the same request as an administrator to prove the administrator view is unchanged.

**Acceptance Scenarios**:

1. **Given** fleet `a,b,c`, profile `prof=[a]`, a `*`-scoped token pinned to `prof`, **When** the token calls `GET /api/v1/status`, **Then** `upstream_stats.servers` has the single key `a`, and `total_servers`, `connected_servers`, `connecting_servers`, `quarantined_servers`, `total_tools` are computed over `a` only.
2. **Given** the same, **When** the token subscribes to `/events` and a `servers.changed` event is published with an embed, **Then** the embed's rows and stats name `a` only; status frames name `a` only; an event whose identity field names `b` is not delivered; a `servers.changed` whose extras name `b` reaches the token with that field withheld.
3. **Given** the same, **When** the token calls `GET /api/v1/annotations/coverage`, `GET /api/v1/review`, `GET /api/v1/attention`, **Then** only `a` appears and every count (queue count, dashboard count, coverage totals) is computed over `a`.
4. **Given** the same and `b` quarantined with a pending tool, **When** the token calls `GET /api/v1/review`, **Then** the response neither lists `b` nor counts its pending tool.
5. **Given** the same and a second profile `wide=[a,b]`, **When** the token calls `GET /api/v1/profiles`, **Then** it sees `prof` only, with `servers`/`effective_servers` = `[a]` and rules naming `b` withheld; `wide` is not listed although it contains `a`, because a pinned token may select only its own pin. **When** it calls `GET /api/v1/profiles/wide`, `GET /api/v1/profiles/wide/effective-tools` or any other non-pin name, **Then** the answer is the same non-disclosing `404` an unknown profile gets. **When** it calls `GET /api/v1/profiles/active` while the server-level active profile is `wide`, **Then** it reads `""`, never `wide`.
6. **Given** the same, **When** the token requests any per-server subtree route for `b` (`/servers/b`, `/servers/b/tools`, `/servers/b/review`, `/servers/b/diagnostics`, logs, tool-call history, `/security/overview`, `/security/scans` and a scan report for `b`), **Then** the answer is indistinguishable from the answer for a server that does not exist.
7. **Given** the same, **When** the token calls `GET /servers` bare and `GET /servers?profile=prof`, **Then** the two bodies are equal, and both equal what the shared predicate admits.
8. **Given** the same and activity records for `a` and `b`, **When** the token calls `GET /api/v1/activity`, `/activity/summary`, `/activity/export`, `/activity/usage` and `GET /api/v1/tool-calls`, **Then** only records for `a` are returned, every total is computed over `a`, and the usage answer is not served from a cache entry built for an unpinned token with the same grant.
9. **Given** the same, **When** the token calls `GET /api/v1/diagnostics` (or `/doctor`) and `GET /api/v1/catalog/search`, **Then** no diagnosis, OAuth state, missing-secret finding or `added_server_name` names `b` or `c`.
10. **Given** an administrator, the tray socket, or a `--servers a` token with no pin, **When** they call every door above, **Then** every body is byte-identical to the body before this change.

---

### User Story 2: A pin that lost its profile, or its reach, hides everything (Priority: P1)

An operator deletes, empties or renames the profile a token is pinned to, or the token's grant stops overlapping the profile. The token must not fall back to its grant and must not be able to tell whether its profile still exists.

**Why this priority**: the pin is a restriction; losing the profile it names must never widen the token. The MCP surface already does this (`profile_resolver.go:276-285`); REST must agree, or deleting a profile hands the token the whole fleet over REST while MCP shows nothing.

**Independent Test**: mint a pinned token with grant `*`, then force-delete the profile. Every door in the table returns the empty-fleet shape: empty lists, zero counts, `404` on every subtree route, no server-named SSE frames.

**Acceptance Scenarios**:

1. **Given** a `*`-scoped token pinned to `prof` and `prof` deleted with `force`, **When** the token calls any door, **Then** it sees no server and every count is zero; `GET /profiles` returns an empty list; the response shape matches what a token with grant `[]` receives, so the token cannot distinguish "profile deleted" from "nothing granted".
2. **Given** `prof` = `[]` (empty) or `prof` naming only unconfigured servers, **Then** the same as scenario 1.
3. **Given** a token with grant `[a]` pinned to `prof` = `[b]` (no overlap), **Then** the same as scenario 1.
4. **Given** `prof` renamed to `prof2`, **Then** the token's pin follows the rename (existing Spec 108 behaviour) and visibility is unchanged.
5. **Given** the pinned profile is deleted while the token holds an open `/events` stream, **When** the next `servers.changed` or status frame is published, **Then** the frame reaches the token with no server named, and no frame after the deletion names any server.

---

### User Story 3: The view-as filter and the MCP surface agree with the boundary (Priority: P2)

An agent holding a pinned token asks for `?profile=other` on REST, or uses `set_profile` / `/mcp/p/<slug>` on MCP. Nothing it selects can show it a server outside its visible set, and what REST lists for a server equals what MCP's `upstream_servers list` and `retrieve_tools` admit for the same token.

**Why this priority**: the ergonomic filters exist to narrow; the boundary must hold whatever the caller selects. Cross-surface parity is how the Web UI, CLI and agents stay consistent (Spec 109 parity principle).

**Independent Test**: with the fixture from Story 1, call `GET /servers?profile=wide` (a profile containing `a` and `b`) as the pinned token and assert the same non-disclosing refusal as for `?profile=<nonexistent>`; call `GET /servers?profile=prof` and assert it equals the bare listing; call `upstream_servers list` over MCP and assert the same set as REST.

**Acceptance Scenarios**:

1. **Given** profiles `prof=[a]` and `wide=[a,b]`, a token pinned to `prof`, **When** it calls `GET /servers?profile=wide` or `GET /tools?profile=wide`, **Then** it receives the same non-disclosing refusal as for a profile that does not exist, exactly as `set_profile("wide")` and `/mcp/p/wide` refuse a pinned token today; **When** it calls `GET /servers?profile=prof`, **Then** the body equals the bare `GET /servers`.
2. **Given** the same, **When** it calls `GET /servers?profile=<missing>`, **Then** the refusal is the same as today (Spec 109-l), and the body and the code path are the same as for `wide`, so it does not reveal whether `wide` or `b` exists.
3. **Given** the same, **When** `upstream_servers list` runs over MCP for the token, **Then** the server set equals the REST visible set, and `retrieve_tools` returns tools from that set only, with `debug.total_indexed_tools` counted over it.
4. **Given** a server-edition user whose entitlement admits `[a,b]`, holding a token pinned to `prof=[a]`, **Then** every door lists `a` only.

---

### User Story 4: The rule is written down where the next reader looks (Priority: P3)

A maintainer or contributor adding a REST read door reads the package header and the agent-tokens feature page and finds one rule: answer from the shared predicate; the pin is part of the boundary.

**Why this priority**: #1684's author left the pin alone because the header said the pin was ergonomics. The next contributor must not have to guess.

**Independent Test**: the `scope_subtree.go` header and `docs/features/agent-tokens.md` state the decision, cite this spec and #1686, and the stale "profile is ergonomic" paragraph is gone or rewritten. The REST API docs and swagger descriptions that explain scoped visibility mention the pin.

**Acceptance Scenarios**:

1. **Given** the merged change, **When** a reader opens `internal/httpapi/scope_subtree.go`, **Then** the header describes the pin as part of the boundary, distinguishes it from the caller-selectable filters, and explains fail-closed on a missing profile.
2. **Given** `docs/features/agent-tokens.md` "Profile Pinning", **Then** it states that the pin bounds every REST and SSE read, not only dispatch, and lists the fail-closed cases.

### Edge Cases

- **Pin with a `*` grant versus a listed grant**: `*` ∩ reach = reach; `[a,c]` ∩ reach `[a,b]` = `[a]`. Both are one rule, no special case.
- **Grant `[]` (deny-all) with a pin**: visible set is empty regardless of the pin. Non-nil empty slices stay non-nil where downstream filters read nil as unrestricted (`scopeAllowedServers`, `internal/httpapi/scope_subtree.go:48-60`).
- **Profile that includes a server which later leaves the config**: reach is restricted to configured servers; the departed server disappears from every door at once.
- **Config hot reload that changes the pinned profile**: the visible set follows the live configuration on the next request and the next SSE frame; no restart, no cached grant.
- **Event with no server identity but of an identity-bearing type**: stays undecidable and fails closed exactly as today (`eventVisibleToCaller`); the pin changes nothing there.
- **The `servers.changed` notify-only path** (embed unavailable upstream): the `server` extra is withheld when it names a server outside the visible set, as today, now by the pinned predicate.
- **Counts beside filtered lists**: every scalar that today is recomputed for a `--servers` token (`upstreamStatsScalars`, review queue count, attention dashboard count, coverage totals, `tool_count` per server) is recomputed for a pinned token by the same code path. No count may be an oracle for hidden servers.
- **Aggregate documents that cannot be filtered** (`/api/v1/config`, `/api/v1/sessions`, the round-10 deployment-wide denials): still denied to every non-administrator; unchanged.
- **A pinned token's own profile shown in `/profiles`**: `used_by` and the anonymous-profile marker stay administrator-only, as today.
- **Refusal paths**: for a pinned caller, a profile slug lookup, a per-server subtree `404` and a `?profile=` refusal take the same code path whether the named profile or server exists outside the visible set or does not exist at all, so neither the body nor the timing distinguishes the two. (The reach itself is computed from `ProfileConfig.EffectiveServers`, `internal/config/profiles.go:487`, which walks the configured fleet; that cost is paid on every path alike and is not an oracle.)
- **Where the predicate gets the configuration**: `auth.CanEnumerateServer` (`internal/auth/context.go:277`) takes only a context and has no configuration, while the pin's reach comes from the live configuration (`callerProfileServers`, `view_as.go:270`). FR-003 fixes the behaviour (resolved per request and per SSE frame, never cached on the credential or connection); where the resolution happens (a per-request resolution stamped at authentication, a controller-backed predicate, or another seam) is a plan decision, recorded in `plan.md`. The SSE writer already refreshes its caller context per frame (`refreshCallerContext`, `internal/httpapi/server.go:4519`, `4572`), and `internal/management/diagnostics.go:139` is the one caller of the predicate outside `internal/httpapi/` that the seam must also serve.
- **List-shaped consumers and the usage cache**: the activity and tool-call doors pass the grant as a filter list rather than calling the predicate per row; the usage cache key is built from that list. A `*`-scoped pinned token with a deleted pin would otherwise still pass the storage filter as `*` and share a cache entry with an unpinned `*` token. FR-019 covers both.

## Requirements

### Functional Requirements: the boundary

- **FR-001**: The system MUST compute a non-administrator caller's visible server set as its grant intersected with the reach of its pin when it has one, and as its grant alone when it has none. This computation MUST live in one shared predicate that every REST read door, every SSE embed and frame filter, and every derived count read from.
- **FR-002**: A pin that names no configured profile, an empty profile, a profile whose effective servers are all unconfigured, or a profile with no overlap with the grant MUST yield an empty visible set. The response shape in that case MUST be the same as for a caller with an empty grant, so the caller cannot distinguish the cases.
- **FR-003**: The visible set MUST be derived from the live configuration on every request and for every SSE frame; it MUST NOT be cached on the credential or the connection beyond the lifetime of one request or one frame.
- **FR-004**: Administrators, the tray socket, requests with no credential, and non-administrator callers without a pin MUST receive byte-identical responses before and after this change on every door.
- **FR-005**: For a pinned caller, every refusal path (profile slug lookups on `/profiles/{name}`, `/profiles/{name}/effective-tools` and `?profile=`, and per-server subtree `404`s) MUST take the same code path and produce the same body for a profile or server that exists outside the visible set as for one that does not exist, so that neither the body nor the timing of a refusal reveals existence.

### Functional Requirements: doors

- **FR-006**: `GET /api/v1/status` MUST narrow `upstream_stats.servers` to the visible set and recompute every sibling scalar (`total_servers`, `connected_servers`, `connecting_servers`, `quarantined_servers`, `total_tools`) over the surviving entries.
- **FR-007**: The `/events` stream MUST, for a pinned caller, narrow the `servers.changed` embed (rows and stats), narrow status frames, drop any event whose identity field names a server outside the visible set, and withhold identity extras naming such a server on the events it keeps.
- **FR-008**: `GET /api/v1/annotations/coverage`, `GET /api/v1/review`, `GET /api/v1/attention` and the `attention.changed` embed MUST list only visible servers and compute every count over them.
- **FR-009**: For a pinned caller, `GET /api/v1/profiles` MUST list its own pin only (while the pin has reach), with `servers`, `effective_servers`, server-qualified rules and `switchable_to` narrowed to the visible set; `GET /api/v1/profiles/{name}` and `GET /api/v1/profiles/{name}/effective-tools` for any name other than the pin MUST answer with the same non-disclosing `404` an unknown profile receives; `GET /api/v1/profiles/active` MUST return `""` unless the server-level active profile is the caller's own pin. A non-pinned scoped caller keeps today's rule (Spec 107 T085: profiles whose effective servers intersect the grant, narrowed). This mirrors the MCP surface, where a pinned token may select only its own pin.
- **FR-010**: Every per-server subtree route MUST answer for an out-of-visible-set server exactly as for a server that does not exist (existing gate, now reading the pinned predicate).
- **FR-011**: `GET /servers` bare and `GET /tools` bare MUST produce the same result for a pinned caller as the shared predicate admits. The #1684 `limitServersToCallerPin` and the #1437 `/tools` pin filter MUST be replaced by the shared predicate or, if kept for the `tool_count` narrowing they also perform, MUST be shown by test to admit exactly the predicate's set.
- **FR-012**: For a pinned caller, `?profile=` MAY name only the caller's own pin, and the result then equals the bare listing; any other slug, existing or not, MUST receive the Spec 109-l non-disclosing refusal through the same code path. For a non-pinned scoped caller, `?profile=` keeps today's rule and only narrows (grant ∩ requested profile). `?profile=` never widens.

### Functional Requirements: parity and documentation

- **FR-013**: For one pinned token, the server set admitted by the REST predicate and the set admitted by the MCP surface (`upstream_servers list`, `retrieve_tools`, `describe_tool`, `set_profile` reported `servers`) MUST be equal. A test MUST assert this for a `*`-scoped pinned token, a listed-grant pinned token, and a deleted pin.
- **FR-014** (design note, non-normative): a switchable client credential's visibility boundary is the union of the effective servers of its binding base and of each profile in the base's `switchable_to`, intersected with the credential's grant; the active profile is the ergonomic filter on top. This has no observable effect today: REST rejects client credentials (`rejectClientCredentialOnREST`), and on MCP `serverInScope` applies only the active profile's scope, which is always a subset of that union. It is recorded so that a future REST admission of client credentials implements the right boundary. Note for that future change: `callerHasProfilePin` (`internal/httpapi/view_as.go:264`) keys on `profile_pin` alone and ignores `profile_mode`, so a switchable credential would be treated as locked unless the predicate keys on the mode. No test is required for this note.
- **FR-015**: The CLI commands that read these REST doors (`mcpproxy upstream list`, `status`, `review`, `attention`, `profile list`, `doctor`) MUST show a pinned token the same narrowed view with no CLI-side change; a test MUST exercise at least `upstream list` and `review` with a pinned token.
- **FR-016**: The `internal/httpapi/scope_subtree.go` header MUST be rewritten to state that the pin is part of the boundary, why intersecting it can only narrow, and the fail-closed cases, citing #1686 and this spec. The stale "profile is ergonomic, not intersected" paragraph MUST be removed.
- **FR-017**: `docs/features/agent-tokens.md` "Profile Pinning" and "Server Scoping", `docs/api/rest-api.md` where scoped visibility is described, and the swagger descriptions of the affected endpoints MUST state that the pin bounds every REST and SSE read.
- **FR-018**: A regression test MUST mint a `*`-scoped pinned token and exercise every door in the table (L1 to L10, including the SSE `servers.changed` embed and a server-named event, `/profiles/{name}/effective-tools`, `/profiles/active`, the activity and tool-call doors, `/diagnostics` and `/catalog/search`), asserting that no out-of-pin server name appears anywhere in the body or frames and that every count equals the in-pin count. The same test MUST run the administrator and a `--servers` token through the same doors and assert their bodies are unchanged.
- **FR-019**: The grant-as-filter seam (`scopeAllowedServers`) MUST return the explicit visible set for a pinned caller (the intersection, never the raw `*`), as a non-nil slice even when empty so that downstream storage filters do not read it as unrestricted; the usage cache key MUST incorporate the pin's reach (or the resolved visible set) so a pinned and an unpinned caller with the same grant never share an entry; FR-002 holds on these doors too.
- **FR-020**: The pin's reach MUST be resolved from the live configuration per request and per SSE frame (FR-003). `plan.md` MUST name the one seam through which the predicate obtains the configuration, and that seam MUST also serve the one caller outside `internal/httpapi/` (`internal/management/diagnostics.go:139`).

### Key Entities

- **Agent token**: a credential with a name, grant (`allowed_servers`), permission tiers and an optional pin (`profile_pin`). Pinned tokens are created with `--profile`.
- **Profile**: a named subset of servers with tool policy; has an effective server set derived from the live configuration; may name `switchable_to` profiles.
- **Visible set**: the servers a caller may learn exist; one per request, derived as above.
- **Read door**: any REST route, SSE frame or embed, or CLI command that lists servers, names a server, or reports a count derived from servers.

## Success Criteria

- **SC-001**: With a fleet of three servers and a one-server pin, a `*`-scoped pinned token receives zero mentions of the two out-of-pin servers across all ten door classes, measured by a body and frame scan in the regression test.
- **SC-002**: Every count shown to that token equals the count computed over its one server, on every door that reports a count.
- **SC-003**: Administrator, tray-socket and `--servers`-token responses on every door are byte-identical to the responses on `main` before the change.
- **SC-004**: After force-deleting the pinned profile, the token's responses on every door match those of a token with an empty grant.
- **SC-005**: For each of three pinned tokens (star grant, listed grant, deleted pin), the REST visible set equals the MCP visible set.
- **SC-006**: The number of places on REST that apply the pin to server existence is one (the shared predicate); a grep under `internal/httpapi/` for `ProfilePin` or `callerProfileServers` outside that predicate, the preflight door, the token/client views and tests finds none.
- **SC-007**: No new config field, no new dependency, no wire format change.

## Assumptions

- **A1**: Only agent tokens carry a pin on REST today. Client credentials are rejected on REST; the FR-014 design note records the boundary they would need if REST ever admits them.
- **A6**: A pinned agent token is locked to its pin on MCP today (`set_profile` to any other slug and `/mcp/p/<other>` are refused non-disclosingly). REST adopts the same rule for `?profile=` and the profile doors (FR-009, FR-012) rather than the intersection rule Spec 107 T085 uses for non-pinned scoped callers.
- **A2**: The #1437 `/tools` and #1684 `/servers` filters also narrow per-server `tool_count` by profile tool rules. That narrowing is a tool-level ergonomic filter and may stay, provided the server set it admits is the predicate's set (FR-011).
- **A3**: Server-edition entitlements compose with the pin by intersection, like the grant; no edition-specific rule is needed.
- **A4**: The pin is immutable for a token's life and follows profile renames; both are existing Spec 108 behaviour and are relied on, not changed.
- **A5**: `used_by`, `anonymous_profile` and the deployment-wide aggregate documents stay administrator-only; this feature does not widen anything.

## Out of Scope

- Changing `allowed_servers` semantics, the deny-all empty-grant rule, or the Spec 105 scoped-count rules for tokens without a pin.
- Changing how the MCP resolver orders pin, URL, session and binding; it already treats the pin as authoritative.
- Letting a pinned token reach a second profile. Pins stay locked.
- Web UI or macOS app changes. Both authenticate as administrators.
- A new `profile_mode` for agent tokens.

## Commit Message Conventions

When committing changes for this feature, follow these guidelines:

### Issue References
- Use `Related #1686` in commits. Do not use `Fixes`, `Closes` or `Resolves`; the issue is closed by hand after verification.

### Co-Authorship
- Do not include `Co-Authored-By: Claude …` or "Generated with Claude Code" lines. Commit authorship reflects the human contributors.

### Example Commit Message
```
fix(httpapi): intersect the profile pin into the shared enumeration predicate

Related #1686

A profile-pinned agent token could still learn out-of-pin server names and
counts from /status, /events, /review, /attention, /profiles and
/annotations/coverage. The shared predicate now intersects the pin's
effective servers into the caller's grant, failing closed on a missing
profile, so every door and every derived count agree.

## Changes
- ...

## Testing
- ...
```

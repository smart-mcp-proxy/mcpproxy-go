# Feature Specification: Server outcomes on tool search and tool lists

**Feature Branch**: `128-server-list-outcomes`
**Created**: 2026-11-19
**Status**: Draft
**Input**: Idea 3 of the LiteLLM MCP Gateway comparison (2026-11-19). LiteLLM returns a per-server list
outcome in the list result's `_meta`, so a client can tell a failed upstream from an empty one
(`litellm/proxy/mcp_server/faults/list_outcomes.py`). mcpproxy today gives an agent no signal at all: a
server that is down, connecting, waiting for a sign-in or quarantined simply contributes no matches.

## Why

An agent asks `retrieve_tools` for "create a jira issue", gets zero results and concludes that the
capability does not exist. It then falls back to a shell CLI, a raw HTTP call or tells the user "I can't
do that". The real cause was that the `jira` server was waiting for an OAuth sign-in. That is something
the user could fix in ten seconds if the agent had said so. The proxy instructions already tell agents to
"retry with different wording before concluding the tool does not exist". That advice is wasted when the
tool is missing because its server is unavailable, not because of the wording.

The operator surfaces (Web UI, tray, `mcpproxy upstream list`, doctor) already know each server's state
through the unified `health` field. This spec carries a small, fixed-vocabulary slice of that knowledge to
the agent, limited to the servers that agent may see.

## Scope Boundary

In scope:

- `retrieve_tools` responses on every surface that offers it.
- `tools/list` responses on the surfaces that list upstream tools: the direct surface (`/mcp/all`,
  `routing_mode: direct`), the single-server surface (`/mcp/s/<server>`), and profile URLs served by
  either of those.
- `describe_tool` not-found answers, for an id whose server is in the caller's reach.

Out of scope:

- Tool-level holds (pending or changed tools on a ready server). Spec 049's `disabled` / `notice`
  (`include_disabled`) already covers them, and this spec does not duplicate or change it.
- Any new operator surface. The Web UI, tray, CLI and REST keep their existing `health` field.
- `call_tool_*` errors for an unavailable server (they already fail with a connection error). Their
  wording may later reuse this vocabulary, but that is not required here.
- Letting the agent fix anything. The agent gets a status and a short instruction for the user. It
  never gets a login link, an approve action or a management call.

## Definitions

- **Reach**: the servers a caller may see on this request, as **that surface already resolves them**.
  Surfaces resolve the profile differently today. `retrieve_tools` resolves at dispatch time, including
  a session `set_profile`. The direct `tools/list` and single-server admission deliberately skip
  session selection. The single-server surface is confined to its one server. The outcome collector
  MUST NOT resolve a profile itself: each surface passes in the effective scope it already computed,
  combined with the existing `serverInScope` auth check (agent-token `AllowedServers` incl. the Spec 118
  pin bound).
- **Unavailable server**: a server in reach whose final display `Status`, from the shared health
  computation, is anything other than `ready`. Servers disabled by an administrator are therefore
  included. When several faults combine (e.g. quarantined and also waiting for a sign-in), the
  calculator's final `Status` is authoritative, and this spec adds no precedence of its own. In
  addition, a server whose `Status` is `ready` but whose first tool discovery and indexing for the
  current connection has not completed is reported as `connecting`. That uses an explicit
  discovery-readiness fact (see FR-011a), never an inference from the tool count. A ready, indexed
  server is never unavailable, whether it has zero tools or all of its tools are held.
- **Server outcome**: one entry describing one unavailable server: its name, one status from a fixed
  vocabulary, and one fixed sentence for the agent.

## User Scenarios & Testing *(mandatory)*

### User Story 1: The agent learns a server needs a sign-in (Priority: P1)

The user asks the agent to create a Jira issue. The `jira` server is in reach but waiting for an OAuth
sign-in. `retrieve_tools` returns no Jira tools. It does return an outcome naming `jira` with status
`sign_in_required` and the sentence "Ask the user to sign in to this server in MCPProxy." The agent tells
the user, the user signs in, and the next search finds the tool.

**Why this priority**: this is the most common avoidable dead end. OAuth sign-ins expire on their own,
so it recurs.

**Independent Test**: one OAuth fixture server with no token, one stdio fixture that is ready. Search for
a tool that only the OAuth server has. The response carries exactly one outcome, for the OAuth server,
with status `sign_in_required`.

**Acceptance Scenarios**:

1. **Given** server A ready and server B waiting for sign-in, both in reach, **When** the agent calls
   `retrieve_tools`, **Then** the response lists B (and only B) under the outcomes key with status
   `sign_in_required`.
2. **Given** every server in reach is ready, **When** the agent calls `retrieve_tools`, **Then** the
   outcomes key is absent and the response is byte-identical to today's for the same query and index.
3. **Given** B is unavailable and the search returns zero tools, **When** the agent reads the response,
   **Then** `usage_instructions` adds one sentence pointing at the outcomes. The wording does not tell
   the agent to try different words.

---

### User Story 2: A scoped agent never learns about servers outside its reach (Priority: P1)

An agent token allowed only `github` calls `retrieve_tools` while `jira` (outside its reach) is down and
`github` is connecting. The outcome lists `github` only. Nothing in the response reveals that `jira`
exists.

**Why this priority**: the outcome list is a new disclosure channel. Specs 108 and 118 made reach a hard
boundary on every door, and this one must not reopen it.

**Independent Test**: reuse the Spec 118 pinned-token fixture. With every out-of-reach server unavailable,
assert that no out-of-reach name, count or status appears anywhere in the response bytes.

**Acceptance Scenarios**:

1. **Given** a token whose reach is {github} and servers jira and github both unavailable, **When** it
   calls `retrieve_tools`, **Then** only `github` appears and no count of other servers is given.
2. **Given** a pinned token whose pin names a deleted profile (empty reach), **When** it calls
   `retrieve_tools`, **Then** no outcomes key appears.
3. **Given** a profile that hides server X from the caller, **When** X is unavailable, **Then** X is not
   listed. The existing `hidden_by_profile` count is unchanged.

---

### User Story 3: Direct-mode clients see the same outcomes on tools/list (Priority: P2)

A client on `/mcp/all` (no `retrieve_tools`) lists tools and gets the outcome list in the list result's
`_meta` under `app.mcpproxy/unavailable_servers`. A client that ignores `_meta` sees no difference.

**Why this priority**: direct mode is the surface for harnesses that defer MCP tools (see the
harness tool-search landscape). It has no search tool to carry the signal.

**Independent Test**: direct-surface `tools/list` with one connecting server in reach. Assert the `_meta`
entry and that the `tools` array is unchanged.

**Acceptance Scenarios**:

1. **Given** the single-server surface `/mcp/s/jira` with jira waiting for sign-in, **When** the client
   lists tools, **Then** `_meta` carries one outcome for jira and `tools` is empty.
2. **Given** all servers in reach are ready, **When** the client lists tools, **Then** the
   `app.mcpproxy/unavailable_servers` key is absent.
3. **Given** a paginated `tools/list`, **When** the client walks the pages, **Then** the outcomes appear
   on the first page only.

---

### User Story 4: describe_tool explains why a known id is missing (Priority: P3)

An agent holding a remembered id `jira:create_issue` calls `describe_tool` on the default surface while
jira is disconnected. Instead of "tool not found", the answer says that the server is unavailable, with
status `error` and the same fixed sentence. The answer describes the **server only**. It is identical
for every tool name on that server, so it never confirms or denies that a given tool exists.

**Why this priority**: agents increasingly cache ids across sessions. This is less frequent than search,
and lower risk.

**Acceptance Scenarios**:

1. **Given** jira in reach and disconnected, **When** the agent describes `jira:create_issue`, **Then**
   the error carries status `error` and the outcome sentence.
2. **Given** jira outside reach, **When** the agent describes `jira:create_issue`, **Then** the answer is
   the existing uniform not-found answer.
3. **Given** jira unavailable, **When** the agent describes `jira:create_issue` and `jira:no_such_tool`,
   **Then** both answers are byte-identical.
4. **Given** the direct surface, **When** the agent describes any id, **Then** the existing single
   answer for unknown, withheld and invisible ids is unchanged. FR-009 does not apply there.

### Edge Cases

- **A server is ready but still indexing** (first discovery not finished for the current connection):
  `connecting`, from the discovery-readiness fact. A ready server whose discovery finished with zero
  tools is not listed.
- **A quarantined server** in reach: whatever final `Status` the calculator returns. That is usually
  `needs_review`, but `sign_in_required`, `needs_config` or `error` when those faults dominate.
- **An admin-disabled server** in reach: `disabled`, with the sentence "This server is turned off in
  MCPProxy." Disabled plus quarantined gives whatever the calculator returns.
- **A missing secret or config error**: `needs_secret` / `needs_config`.
- **A flapping server** that changes state between the search and the response: the outcome reflects the
  state read once per request. Two calls may disagree, and that is acceptable.
- **Many unavailable servers**: at most 20 entries, ordered by the existing Spec 109 action priority and
  then by name. When more exist, one extra field gives the remaining count. That count is also limited
  to servers in reach.
- **Upstream error text**: never copied. An upstream's error message is untrusted (it may contain
  injection text or secrets), so the agent sees only the fixed vocabulary.
- **`retrieve_tools` from inside `code_execution`**: same rules, same reach.
- **Telemetry** carries only an aggregate count of responses that had any outcome, never server names.

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: `retrieve_tools` MUST include an `unavailable_servers` array when at least one server in the
  caller's reach is unavailable, and MUST omit the key otherwise. With the key omitted, the response MUST
  be identical to the pre-feature response.
- **FR-002**: Each entry MUST contain exactly `server` (name), `status` and `message`. `status` is one
  value of the existing display vocabulary: `connecting`, `sign_in_required`, `needs_review`,
  `needs_secret`, `needs_config`, `error`, `disabled`. `message` is a fixed sentence per status, from one
  table shared by every surface.
- **FR-003**: Entries MUST be derived from the same health computation the operator surfaces use, read
  once per request. They MUST NOT contain upstream-supplied text, URLs, error strings, token or account
  details, or a fix action the agent could invoke.
- **FR-004**: Only servers in the caller's reach, as the surface itself resolved it (Definitions), MAY
  appear. No count, total or other value MAY depend on servers outside it. Each surface MUST pass its
  own effective scope to the collector, preserving list-time versus dispatch-time profile resolution
  and single-server confinement. This MUST hold for agent tokens (incl. pinned and stale-pin tokens),
  profile-bound clients, URL profiles, session-selected profiles, anonymous callers and the
  single-server surface. Tests MUST include a session whose `set_profile` choice differs from its
  binding or URL profile, on both `retrieve_tools` and the direct `tools/list`.
- **FR-005**: The array MUST hold at most 20 entries, ordered by action priority then name. When more
  unavailable servers are in reach, `unavailable_servers_more` MUST give the number not listed.
- **FR-006**: When `retrieve_tools` returns zero tools and `unavailable_servers` is present,
  `usage_instructions` MUST add one sentence telling the agent that the tool may belong to an unavailable
  server and to report the listed status to the user rather than rephrase.
- **FR-007**: `tools/list` on the direct and single-server surfaces MUST carry the same entries in the
  result's `_meta` under `app.mcpproxy/unavailable_servers` (and `app.mcpproxy/unavailable_servers_more`),
  on the first page only, with the same reach and cap rules. The `tools` array MUST be unchanged.
- **FR-008**: The default `retrieve_tools`-mode surface's `tools/list` (built-in tools only) MUST NOT
  carry outcomes.
- **FR-009**: On the default surface (canonical `server:tool` ids) and the single-server surface (bare
  ids, confined to that server), `describe_tool` MUST return the server's status and message, instead of
  the not-found remediation, for an id that would otherwise be "not found" and whose owning server
  resolves unambiguously, through the existing collision-aware resolution, to an in-reach unavailable
  server. The answer MUST be identical for every tool name on that server. The existing visibility and
  tool-policy gates run first and keep their answers. The direct surface's describe answers MUST be
  unchanged. `check: true` behaviour MUST be unchanged. For an id whose server is outside reach, or does
  not resolve unambiguously, the existing answer MUST be returned unchanged.
- **FR-010**: The status vocabulary and message table MUST be one contract shared with the operator
  display vocabulary. A test MUST fail if a new display status is added without a message.
- **FR-011**: Building the outcome list MUST NOT trigger a connection attempt, discovery, token refresh,
  storage write or activity row. It is a read of already-computed state.
- **FR-011a**: The runtime MUST expose a per-server discovery-readiness fact: whether the first tool
  discovery and index update for the current connection epoch has completed. Outcomes MUST use it for the
  `connecting` overlay. Tests MUST separate empty-ready (discovery done, zero tools, not listed),
  all-held (not listed), discovery-pending (`connecting`) and indexing-pending (`connecting`).
- **FR-012**: The feature MUST be on by default. It MAY be turned off with one global setting
  (`agent_server_outcomes: false`, env `MCPPROXY_AGENT_SERVER_OUTCOMES=false`), which restores the
  pre-feature responses exactly.
- **FR-013**: The three `retrieve_tools` response goldens MUST stay unchanged for the all-ready case, and
  a new golden MUST pin the shape with outcomes present.

### Key Entities

- **Server outcome**: `{server, status, message}`. Ephemeral, computed per request, never stored.
- **Outcome message table**: status → fixed agent-facing sentence. Versioned with the display vocabulary.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: In a scripted agent trial (sign-in-required server, query for its tool), the agent reports
  the sign-in need to the user in at least 9 of 10 runs, versus a baseline where it reports "tool does
  not exist" or falls back to a CLI.
- **SC-002**: With every out-of-reach server unavailable, zero bytes of any response to a scoped caller
  differ from the response with those servers ready (byte comparison across the Spec 118 fixture matrix).
- **SC-003**: Each outcome entry adds no more than 40 tokens. A full list of 20 entries stays under
  800 tokens.
- **SC-004**: For an all-ready fleet, responses on every surface are byte-identical to the pre-feature
  release.
- **SC-005**: Computing outcomes adds no measurable latency to `retrieve_tools` on the 3,000-tool perf rig
  (within run-to-run noise, p95).

## Assumptions

- The health calculator's `Status` display vocabulary (Spec 109 FR-010) is stable enough to expose to
  agents. If it grows, FR-010's test forces a message for the new value.
- Agents read top-level keys of `retrieve_tools` JSON, as they already do for `notice` and
  `hidden_by_profile`. Clients that drop unknown `_meta` keys lose nothing.
- mcp-go v1.0.0's `ListToolsResult` carries a writable result `_meta` that an after-list hook can set
  (confirmed by the review against `mcp/tools.go`, `mcp/types.go` and `server/hooks.go`).
- A disabled server is worth naming to an agent in reach. An operator who disagrees can drop the server
  from the profile, which removes it from reach.

## Open Questions for the Plan

- Exact message wording per status (≤ 20 words each, imperative, user-directed).
- Whether `connecting` should carry a "retry in a few seconds" hint distinct from the others.
- Whether the outcome should be recorded on the existing `retrieve_tools` activity row (e.g. as a count)
  for the failure timeline (Spec 124). This spec does not require it.

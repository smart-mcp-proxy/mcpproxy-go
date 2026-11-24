# Feature Specification: `admin_action` audit events — who changed the rules

**Feature Branch**: `133-admin-action-audit`
**Created**: 2026-11-24
**Status**: Draft (spec only — no implementation in this PR)
**Input**: Issue #1698 phase two. Phase one (#1842, merged) put the caller on the activity rows (`actor_kind`, `actor_name`, `surface`, `request_id`), recorded REST server changes and token regenerate. This spec lifts that record onto the synchronous audit JSONL stream (`docs/features/audit-log.md`, schema v1: `authz`, `tool_call`, `auth_event`) as a fourth event kind, `admin_action`.

**Companion files**: [inventory.md](inventory.md) (every administrative write door and what it records today, with file:line evidence) · [contracts/admin-action-line.md](contracts/admin-action-line.md) (the line key by key, the action list, the exemptions) · [plan.md](plan.md) (design, PR slicing, tests) · [research.md](research.md) (decisions and the alternatives they beat).

## Background

The audit log answers "who called which tool and what happened". It does not answer "who changed the rules". The activity log now names the actor on most administrative rows (#1842), but an operator who ships the audit file to an external system and treats it as the record still has three gaps:

1. Activity rows are read back through REST/CLI. `config_change`, `quarantine_change`, `tool_quarantine_change` and `skill_quarantine_change` reach storage through the 256-slot event bus, which drops on overflow without a counter; only `profile_change` (tokens, clients, profiles) is written synchronously (inventory.md §0.2). None of it is the rotated JSONL stream a log shipper reads.
2. The audit file has no line at all for a token minted, regenerated or revoked, a server or tool approved, or a config setting changed.
3. Phase one left holes the audit stream must not inherit (inventory.md §6): tool approve/block/enable rows carry no actor (the funnel takes no context, `internal/httpapi/server.go:7346`); token hard delete, client-rotation staging, keyring secrets, bulk enable/disable, `POST /config/apply` / `PATCH /config` global edits, registries, scanners, TPA bundle operations, MCP `upstream_servers update|patch`, file-watcher reloads and every server-edition user/admin handler record nothing.

The reporter's priority order (issue thread): (1) who minted, regenerated or revoked a token — regenerate matters most; (2) who approved a server or a changed tool; (3) who changed config settings. "Config file edited on disk, no caller known" is acceptable for a change with no request behind it.

## Clarifications

### Session 2026-11-24

Answered by the spec author from the #1698 thread and the code, under the maintainer's instruction to work autonomously; the reporter can overturn any of them on the PR.

- Q: When a REST or MCP add lands a server in quarantine through the admission gate in the same commit, is that one line or two? → A: One `server.add` line by the caller, with `quarantined` in `changes`; a separate `server.quarantine` line with `caller.kind: internal`, `trigger: admission` is written only when the gate acts outside a caller's commit (file reload, startup).
- Q: Is turning the audit log itself off or changing its sink a recorded change? → A: Yes — `config.update` with `audit_log.*` fields, diffed on the committed desired configuration (the live one keeps the restart-pinned value, `internal/runtime/config_hotreload.go:529-541`) and written by the running sink if there is one. It records a pending change; the old sink keeps writing until the restart, so it is not necessarily the last line.
- Q: How large may one line get? → A: 16 KiB serialized, with deterministic truncation (name lists to 64, `changes` to 128 sorted by `field`, then trailing entries dropped) and full counts kept (`changes_count`, `target.count`).
- Q: What does `ts` mean and is file order commit order? → A: `ts` is the commit time of the effect. File order is not commit order, not even for one target: the commit lock is released before post-commit work (`internal/runtime/commit.go:483-505`). Consumers order by `ts`.
- Q: A request that changes several targets (config-to-secret, security approve, key revoke with logins, user disable) — one line or several? → A: One line per committed effect, all with the same `request_id`. Tool, prompt and skill decisions are grouped per batch key (request or pass × server × kind × action × policy).
- Q: What if the request fails after part of it committed? → A: The line follows the effect, not the response: what persisted is recorded, what did not is not.

## User Scenarios & Testing *(mandatory)*

### User Story 1 — Token lifecycle is on the audit stream (Priority: P1)

An operator ships `audit.jsonl` to a SIEM. After an incident they need to know who minted, regenerated or revoked an agent token or client credential, and when. Every such change writes an `admin_action` line naming the caller, the token by name and display prefix, and never the token value.

**Why this priority**: The reporter's first priority. A regenerated token is a new secret in circulation; before #1842 it left no record at all, and it still leaves none on the audit stream.

**Independent Test**: Enable `audit_log.path` and drive the surface × operation matrix of contract §P1 — REST, CLI and Web UI for every agent-token verb; MCP `credentials` for `create_token`, `create_client` and the three `revoke` branches; Connect for mint, rotation and undo. Each committed effect writes exactly the lines the contract names (`token.create|rotate|revoke|delete`, `client.*`, `login.*`), with the right `caller`, `target`, `request_id` and old/new `token_prefix`, and no secret-shaped string anywhere (the sanitizer hit counter stays at zero).

**Acceptance Scenarios**:

1. **Given** audit logging is on, **When** an administrator runs `mcpproxy token regenerate ci-bot`, **Then** one line is written with `action: token.rotate`, `caller.kind: socket` (CLI over the socket) or `api_key` (TCP), `target: {kind: agent_token, name: ci-bot}`, `changes` holding `token_prefix` before and after, and `request_id` equal to the matching activity row's.
2. **Given** the same setup, **When** a token is revoked, **Then** one `token.revoke` line is written, and revoking an already-revoked token writes no line.
3. **Given** client `cursor` has an API key and two browser logins, **When** MCP `credentials revoke {client: cursor}` runs, **Then** one `client.key_revoke` and two `login.revoke` lines are written with the same `request_id`.
4. **Given** the disk under `audit_log.path` is full, **When** a token is revoked, **Then** the revoke still succeeds, the write-failure counter increments and `mcpproxy doctor` reports it.

---

### User Story 2 — Approvals are on the audit stream (Priority: P2)

A reviewer needs to know who approved a quarantined server, a pending or changed tool, or a skill — and, when the proxy approved one by itself (trusted baseline, scan-clean auto-approval, `auto_approve_tool_changes`), which policy did it.

**Why this priority**: The reporter's second priority. In a deny-by-default setup an approval is the act that widens access.

**Independent Test**: Approve a quarantined server and a changed tool through the Web UI, CLI and the `quarantine_security` MCP tool; let a trusted server baseline its tools; let a scan-mode server auto-approve a clean change. Each writes the contract's lines with the right `action`, `caller` and, for automatic approvals, `caller.kind: internal` with `trigger: policy` and a closed `policy`.

**Acceptance Scenarios**:

1. **Given** server `github` is quarantined, **When** an administrator unquarantines it in the Web UI, **Then** one `server.unquarantine` line is written with `caller.kind: api_key` (or `session_admin` in the server edition) and `client.surface: web`.
2. **Given** a tool's description changed and is held, **When** it is approved with `mcpproxy upstream approve`, **Then** one `tool.approve` line names the server, the tool, and the approved and previous hash prefixes.
3. **Given** a security review approves 10 tools and blocks 2 on one server, **Then** one `server.review_approve`, one `tool.approve` (`count: 10`) and one `tool.block` (`count: 2`) line are written with one `request_id`.
4. **Given** a trusted server connects for the first time, **When** the proxy baselines its 40 tools after a clean scan, **Then** one `tool.approve` line is written with `caller.kind: internal`, `trigger: policy`, `policy: trusted_baseline`, `target.count: 40` and the names (capped at 64, `truncated: true` beyond).

---

### User Story 3 — Config and server changes are on the audit stream (Priority: P3)

A reviewer needs to know who added, removed, enabled or edited an upstream server, changed a profile or a client binding, or changed a global setting — and, for edits made on disk, that there was no caller.

**Why this priority**: The reporter's third priority; the activity rows already carry before/after values for most of it, so the gap is the stream.

**Independent Test**: Make each change through REST, CLI, Web UI and the `upstream_servers` / `profiles` MCP tools, then edit `mcp_config.json` by hand. Each committed effect writes one line; the hand edit writes `caller.kind: none`, `trigger: config_file`. Changed fields appear by name; booleans, enums, bounded numbers and name lists appear verbatim; every other value is withheld.

**Acceptance Scenarios**:

1. **Given** server `ast-grep` exists, **When** `PATCH /api/v1/servers/ast-grep` changes `enabled`, `args` and `env.GITHUB_TOKEN`, **Then** one `server.update` line lists `enabled` with `before: true, after: false`, `args` with `withheld: "value"` and `env.GITHUB_TOKEN` with `withheld: "secret"`, and no value or hash of either.
2. **Given** the config file is edited on disk to add a server, **When** the watcher reloads it, **Then** one `server.add` line is written with `caller: {kind: none}`, `trigger: config_file`, no `request_id`.
3. **Given** a PATCH that changes nothing, **Then** no line is written.

---

### Edge Cases

- **No caller**: a file edit, the admission gate at load, a startup sweep. The line carries `caller.kind: none` (observed, not made, by the proxy) or `internal` (the proxy decided by policy, admission or schedule) plus a closed `trigger`; it never invents an actor and never carries `request_id`.
- **Refused attempt** (403, 404, 409, validation error before any write): nothing committed, no line. Refused administrative attempts are a later event (Out of scope).
- **Partial failure**: a request that commits some effects and then fails writes the lines for the effects that persisted (config-to-secret: `secret.set` without `server.update`).
- **Reported success, nothing committed** (no-op toggle): no line.
- **Bulk change** (enable-all, bulk-assign, a reload touching 30 servers): one line per committed effect; tool, prompt and skill decisions per batch key.
- **Stateless issuance**: the server-edition `POST /auth/token` signs a JWT and stores nothing; it writes one `jwt.issue` line when the JWT is signed, before it is returned.
- **Audit sink off**: nothing is written, nothing is buffered, behaviour is unchanged.
- **Crash between commit and line**: the change is kept and its line is lost — documented like the existing `authz` crash window.
- **Bus saturated**: the line count is unaffected — the line never goes through the event bus.
- **Agent token acting**: recorded as `caller.kind: agent_token` with `token_name`, `token_prefix` and, when set, `profile_pin`; a Spec 132 browser login (`mcp_oat_`) the same way, as `authz` lines already do.
- **Credential-shaped names or request ids**: masked per field with the existing patterns plus the proxy's own token shapes (`mcp_agt_…`, `mcp_cli_…`, `mcp_oat_…`, `mcp_ort_…`), which PR-1 adds.

## Requirements *(mandatory)*

### Functional Requirements

**Event and compatibility**

- **FR-001**: The audit stream MUST gain a fourth event kind, `admin_action`, written to the same sink as `authz`, `tool_call` and `auth_event`.
- **FR-002**: `admin_action` MUST ship under `schema_version: 1` as an additive change, following the published rule "adding a key or an enum value is a minor change". The published schema `docs/schemas/audit-line-v1.schema.json` and its contract copy MUST be updated in place (same `$id`): root enums widened (`event`, `caller.kind`), new keys declared at the root, an event-scoped block for the required/forbidden sets, the other event blocks forbidding the new keys, an event-aware producer-strict test, and a test that every pre-133 example still validates unchanged. The audit-log change log MUST gain an entry.
- **FR-003**: The audit-log page and the schema's `description` MUST state the compatibility rule for log shippers: a shipper filtering on `schema_version == 1` receives `admin_action` lines with no change; a consumer MUST route or filter on `event` before validating, and ignore values it does not know; a consumer that validates against a cached copy of the v1 schema MUST refresh it, because `event` and `caller.kind` are closed enums. The current sentence that a strict consumer "keeps validating after a minor additive change" MUST be narrowed to new keys, the only case where it is true.
- **FR-004**: No existing key, value or count invariant of `authz`, `tool_call` or `auth_event` changes. (The credential-pattern extension of FR-016 masks more on those lines too; a line that matched none of the new patterns is byte-identical.)

**Shape**

- **FR-005**: Every `admin_action` line MUST carry the common keys (`schema_version`, `ts`, `event`, `origin`, `source`, `caller`) plus `action`, `target` and `outcome: success`; and `changes`, `changes_truncated`, `changes_count`, `trigger`, `policy`, `request_id`, `transport_request_id`, `client`, `profile` under the conditions of [contracts/admin-action-line.md](contracts/admin-action-line.md).
- **FR-006**: `action` MUST be one value of a closed `<area>.<verb>` vocabulary (contract §Actions). An `admin_action` line MUST NOT carry `surface`, `operation`, `decision`, `disclosed`, `reason`, `server`, `tool`, `args_sha256`, `args_bytes`, `error_class`, `duration_ms`, `request_bytes`, `response_bytes`, `flags` or `parent_id` — those keep their per-event meaning on the other kinds.
- **FR-007**: `target` MUST identify what changed with the per-kind keys of contract §Target, never a secret. A tenant-owned target (server edition) MUST carry `owner_user_id`, because token names are unique per owner only.

**Actor**

- **FR-008**: `caller` MUST be derived from the trusted request context exactly as for `authz` lines (AuthContext type, then the anonymous bit, then the connection source; never an argument, a header or the activity `actor_kind` string), so one caller index covers every event kind. Contract §Caller lines the #1842 vocabulary up with it; the caller-asserted #1842 `surface` (`web`, `macos`, `cli`, `mcp`, `api`) goes to `client.surface`, untrusted like `client.name`.
- **FR-009**: An effect with a request behind it MUST carry that request's proxy-side `request_id`, minted once at the admin ingress, carried in the mutation context, and equal to the `request_id` on the matching activity row when there is one. Doors that mint an id today without passing it to the mutation (MCP admin tools, Connect) MUST be changed to pass it.
- **FR-010**: An effect with no request behind it MUST carry `caller.kind: none` (the proxy observed a change it did not make: a config file edit) or `internal` (the proxy decided: policy, admission, startup reconcile, schedule) plus a `trigger` from a closed list, and MUST NOT carry `request_id` (omitted, never `""`) or any identity key. An absent actor means "no caller", never an unknown one — the #1842 rule.
- **FR-011**: An offline CLI command that changes the rules while the core is stopped MUST record `caller.kind: cli_offline` (FR-021). A server-edition tenant acting on their own servers, tokens or credentials MUST be recorded as `caller.kind: session_user`, which becomes legal on `admin_action` (it already is on `auth_event`). Native stdio administration MUST be recorded as `caller.kind: stdio`.

**What is recorded**

- **FR-012**: The closed action list, in the reporter's priority order, is: (P1) tokens and credentials — agent token create/rotate/revoke/delete, client credential create/bind/key-revoke/rotate (stage, finalize, rollback)/forget, browser-login approve/deny/revoke, keyring secret set/delete (name only), API key reset; (P2) approvals — server quarantine/unquarantine, security review approve/reject, tool approve/block/enable/disable (manual and automatic), prompt approve, skill approve/block/unblock (manual and automatic), TPA bundle rollback/unpin/reload/activate, scanner enable/disable/configure; (P3) config — server add/update/remove/enable/disable, admission quarantine outside a caller's commit, global config update (including `audit_log` and telemetry), registry add/update/remove, profile create/update/delete/rename/classify/set-anonymous/activate, client binding; and in the server edition tenant tokens, JWT issuance, user provision/role change/enable/disable, server sharing, tenant servers and tenant credentials. Contract §Actions maps every `action` to its doors and operation branches; every other mutating door is on contract §Exempt with a reason.
- **FR-013**: A line records one committed effect (contract §Unit of record), written after the effect persists and before the request returns. A request that commits nothing writes nothing; a request that fails after part of it committed writes the lines for what persisted.
- **FR-013a**: A request that commits several effects MUST write one line per effect, all with the request's `request_id`. A server the admission gate quarantines inside a caller's own add is one `server.add` line with `quarantined` in `changes`.
- **FR-014**: Tool, prompt and skill decisions MUST be grouped into one line per batch key — (request or automatic pass) × server × `target.kind` × `action` × `policy` — carrying `target.count` and up to 64 sorted names, with `target.truncated: true` beyond that. An automatic decision carries `policy` (`trusted_baseline` | `scan_clean` | `auto_approve_tool_changes` | `hash_restored` | `skills_auto` | `login_replaced`).
- **FR-015**: `changes` MUST list changed fields by name. A value MUST appear verbatim only when the field's type is on contract §Changes' literal list (booleans, closed enums, bounded integers, timestamps, display prefixes, public content-hash prefixes, lists of server or profile names). Every other field MUST appear as `withheld: "secret"` (secret-class paths) or `withheld: "value"` (everything else), with no value and no hash. An unknown path is withheld.
- **FR-015a**: A serialized line MUST NOT exceed 16 KiB; truncation follows contract §Size and keeps the full counts.
- **FR-016**: A line MUST NEVER contain a token value, API key, cookie, JWT, OAuth secret, key material, an env or header value, a free-text config value, or a hash of any of them. Values cannot enter through the builder's types (plan D-1); names, ids and other caller-influenced strings pass through the per-field credential mask, whose fixed patterns MUST be extended with the proxy's own token shapes; the whole-line sanitizer and its hit counter remain as defence in depth. The spec does not claim that pattern masking detects a secret with no recognisable shape; that is why no value is ever used as a label.
- **FR-016a**: A change to `audit_log` itself MUST be recorded as `config.update`, diffed on the committed desired configuration, by the running sink if one exists.
- **FR-016b**: `ts` MUST be the commit time. File order is not guaranteed, even for one target; consumers order by `ts`.

**Failure semantics**

- **FR-017**: A failed audit write MUST NOT refuse, roll back or undo the change. The write is synchronous and MAY delay the response by the time it takes; it follows the existing sink failure policy (`mcpproxy_audit_write_failures_total`, a `mcpproxy doctor` finding, a log line at most once a minute). There is still no `audit_log.strict` mode.
- **FR-018**: The line MUST be written synchronously through the sink, never through the activity event bus. The count invariant (contract §Count invariant: one line per committed effect, in batch-key units, when the enabled sink's write succeeds) MUST hold with the bus saturated.

**Readers, editions**

- **FR-019**: No REST, SSE, MCP, CLI or Web UI surface reads audit lines, before or after this feature; scoped (agent) tokens can read nothing new. The visibility of the matching activity rows is unchanged.
- **FR-020**: The feature is edition-neutral: lines are written whenever `audit_log` is enabled (personal default off, server default on). Server-edition-only doors write lines in the server edition only. No connector, shipper, Kubernetes or Loki integration is added; shipping remains the operator's log shipper, as today.
- **FR-021**: An offline CLI write that changes the rules (contract §Actions, `cli_offline`) MUST write its line only when `audit_log` is effectively enabled and has a `path`; the stdout sink is never used by an offline command. An offline open or write failure MUST print one warning to stderr and MUST NOT change the command's exit status (the daemon's doctor counters do not see an offline process).

**Guard rails**

- **FR-022**: A repository test MUST enumerate every administrative write door from the constructed routers and command trees — every route of the personal and `-tags server` HTTP routers (all methods; a `GET` that persists state is listed by name, never blanket-exempt), every accepted operation value (including aliases) of every built-in admin MCP tool, and every CLI command branch that writes without the daemon — and fail when one is neither mapped to an `action` nor exempted with a reason, or when a registry entry no longer exists. A new write door therefore cannot merge without a record.
- **FR-023**: For every mapped door, a table test MUST drive the door with a capturing sink and assert the **exact set** of lines the contract specifies for that invocation (including fan-out and partial failure), with the expected `action`, `caller.kind`, `target`, `request_id`, no forbidden key, and schema validity.

### Key Entities

- **Admin action line**: one JSON object per committed effect; `action`, `target`, `changes`, `caller`, `trigger`/`policy`, `request_id`.
- **Committed effect**: one durable transition of one target, observed where it persisted.
- **Door registry**: the checked-in list mapping each (door, operation branch) to its `action` or an exemption, read by FR-022's test.
- **Change entry**: `{field, before?, after?}` or `{field, withheld}`.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: 100% of the (door, branch) pairs in contract §Actions produce exactly the specified line set per invocation, shown by the door table tests (FR-023).
- **SC-002**: A reviewer can answer "who minted, regenerated or revoked token X, and when" from the audit file alone, for every token change made after upgrade.
- **SC-003**: With the event bus saturated, 2,000 token revocations (2,000 committed effects) produce 2,000 `admin_action` lines.
- **SC-004**: Zero values outside the literal list, zero hashes of any value, and zero sanitizer hits across the door table tests and a fuzzed config-change test.
- **SC-005**: A change made while the audit disk is full still succeeds in 100% of cases.
- **SC-006**: Adding a mutating route, MCP operation or offline CLI write without a registry entry fails CI.

## Assumptions

These were decided without asking, under the maintainer's instruction to work autonomously; each is recorded with its alternative in research.md.

- **A1 — Additive within v1, not v2.** The published versioning rule classes a new enum value as minor, and a v2 bump would break every shipper filter on `schema_version == 1` for the three existing kinds. Cost: a validator holding the old v1 copy rejects `admin_action` lines; FR-003 tells shippers so.
- **A2 — The audit `caller` object, not the #1842 key names.** The audit file already names callers with `caller.kind`; a second spelling (`actor_kind`) on one event kind would split every SIEM index. The caller-asserted `surface` goes under `client`, because the audit doc never puts a header-derived value in a trusted key.
- **A3 — A failed audit write never refuses the change.** Fail-closed would let a full disk block token revocation and quarantine — the safety actions. This matches the existing sink policy.
- **A4 — Committed effects only.** Refused administrative attempts are not recorded here; "who tried" is a different question with a different count invariant (plan.md Q2).
- **A5 — Names always; values only for typed, non-free-text fields; nothing (not even a hash) for the rest.** The issue suggested names only; the reporter's reviewers asked for before/after. This answers "enabled went from true to false" without ever putting a free-text value, or a digest of one, on the line.
- **A6 — Automatic approvals are recorded** (`caller.kind: internal`, `trigger: policy`), batched, because "who approved this tool" has the answer "the proxy, under this policy", and the reviewer needs to see it.

## Out of Scope

- Recording refused administrative attempts (A4).
- A read surface for audit lines, a connector, or shipping to Kubernetes/Loki (server-edition roadmap, unchanged).
- Changing the activity log beyond two by-products: PR-2 passes the request context to the tool-approval funnels, so `tool_quarantine_change` rows gain the #1842 actor keys; PR-1 carries one request id from the MCP admin tools and Connect into their mutations, so those rows' `request_id` matches the wrapper row. Both are additive. Nothing else about activity rows, keys or visibility changes.
- Child tokens and guardrails (later on the product order: audit → child tokens → guardrails).

## Commit Message Conventions *(mandatory)*

### Issue References
- ✅ **Use**: `Related #1698`
- ❌ **Do NOT use**: `Fixes #1698`, `Closes #1698`, `Resolves #1698`

**Rationale**: Issues should only be closed manually after verification and testing in production, not automatically on merge.

### Co-Authorship
- ❌ **Do NOT include**: `Co-Authored-By: Claude <noreply@anthropic.com>`
- ❌ **Do NOT include**: "🤖 Generated with [Claude Code](https://claude.com/claude-code)"

**Rationale**: Commit authorship should reflect the human contributors, not the AI tools used.

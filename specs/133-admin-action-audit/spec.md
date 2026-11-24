# Feature Specification: `admin_action` audit events — who changed the rules

**Feature Branch**: `133-admin-action-audit`
**Created**: 2026-11-24
**Status**: Draft (spec only — no implementation in this PR)
**Input**: Issue #1698 phase two. Phase one (#1842, merged) put the caller on the activity rows (`actor_kind`, `actor_name`, `surface`, `request_id`), recorded REST server changes and token regenerate. This spec lifts that record onto the synchronous audit JSONL stream (`docs/features/audit-log.md`, schema v1: `authz`, `tool_call`, `auth_event`) as a fourth event kind, `admin_action`.

**Companion files**: [inventory.md](inventory.md) (every administrative write door and what it records today, with file:line evidence) · [contracts/admin-action-line.md](contracts/admin-action-line.md) (the line, key by key) · [plan.md](plan.md) (design, PR slicing, tests) · [research.md](research.md) (decisions and the alternatives they beat).

## Background

The audit log answers "who called which tool and what happened". It does not answer "who changed the rules". The activity log now has an actor on every administrative row (#1842), but an operator who ships the audit file to an external system and treats it as the record has two remaining gaps:

1. Activity rows are read back through REST/CLI. `config_change`, `quarantine_change`, `tool_quarantine_change` and `skill_quarantine_change` reach storage through the 256-slot event bus, which drops on overflow without a counter; only `profile_change` (tokens, clients, profiles) is written synchronously (inventory.md §0.2). None of it is the rotated JSONL stream a log shipper reads.
2. The audit file has no line at all for a token minted, regenerated or revoked, a server or tool approved, or a config setting changed.
3. Phase one left holes the audit stream must not inherit (inventory.md §6): tool approvals, blocks and enable/disable rows carry no actor (the funnel takes no context, `server.go:7346`); token hard delete, client-rotation staging, keyring secrets, bulk enable/disable, `POST /config/apply` / `PATCH /config` global edits, registries, scanners, TPA bundle operations, MCP `upstream_servers update|patch`, file-watcher reloads and every server-edition user/admin handler record nothing.

The reporter's priority order (issue thread): (1) who minted, regenerated or revoked a token — regenerate matters most; (2) who approved a server or a changed tool; (3) who changed config settings. "Config file edited on disk, no caller known" is acceptable for a change with no request behind it.

## Clarifications

### Session 2026-11-24

Answered by the spec author from the #1698 thread and the code, under the maintainer's instruction to work autonomously; the reporter can overturn any of them on the PR.

- Q: When a REST or MCP add lands a server in quarantine through the admission gate in the same commit, is that one line or two? → A: One `server.add` line by the caller, with `quarantined` in `changes`; a separate `server.quarantine` line with `caller.kind: internal`, `trigger: admission` is written only when the gate acts outside a caller's commit (file reload, startup).
- Q: Is turning the audit log itself off or changing its sink a recorded change? → A: Yes — `config.update` with `audit_log.*` fields, written by the running sink before the restart that applies it (the sink is restart-pinned, `internal/runtime/config_hotreload.go:529-540`), so "audit was switched off" is the last line the old sink writes.
- Q: How large may one line get when an apply changes hundreds of fields? → A: At most 128 `changes` entries per line, then `changes_truncated: true` and `changes_count`; field order is sorted by `field`.
- Q: What does `ts` mean and is file order commit order? → A: `ts` is the commit time; lines for different targets written concurrently may appear in the file out of `ts` order, and consumers sort on `ts`. For one target the funnel's existing per-target lock keeps them in order.
- Q: A request that changes several targets (config-to-secret, security approve, user disable) — one line or several? → A: One line per target, all with the same `request_id` (e.g. `secret.set` + `server.update`; `server.review_approve` + `tool.approve`/`tool.block`; `user.disable` + one `token.revoke` per token).

## User Scenarios & Testing *(mandatory)*

### User Story 1 — Token lifecycle is on the audit stream (Priority: P1)

An operator ships `audit.jsonl` to a SIEM. After an incident they need to know who minted, regenerated or revoked an agent token or client credential, and when. Every such change writes one `admin_action` line naming the caller, the token by name and display prefix, and never the token value.

**Why this priority**: The reporter's first priority. A regenerated token is a new secret in circulation; before #1842 it left no record at all, and it still leaves none on the audit stream.

**Independent Test**: Enable `audit_log.path`, create, regenerate and revoke a token through REST, CLI and the MCP credentials door, and (for client credentials) through Connect. The file holds exactly one `admin_action` line per change with `action` `token.create` / `token.rotate` / `token.revoke`, the right `caller`, `target.name`, old/new `token_prefix`, and no secret-shaped string anywhere (the whole-line sanitizer counter stays at zero).

**Acceptance Scenarios**:

1. **Given** audit logging is on, **When** an administrator with the API key runs `mcpproxy token regenerate ci-bot`, **Then** one line is written with `event: admin_action`, `action: token.rotate`, `caller.kind: socket` (CLI over the socket) or `api_key` (TCP), `target: {kind: token, name: ci-bot}`, `changes` holding `token_prefix` before and after, and `request_id` equal to the matching activity row's `request_id`.
2. **Given** the same setup, **When** a token is revoked, **Then** one `token.revoke` line is written, and revoking an already-revoked token writes no line (nothing changed).
3. **Given** the disk under `audit_log.path` is full, **When** a token is revoked, **Then** the revoke still succeeds, the write-failure counter increments and `mcpproxy doctor` reports it.

---

### User Story 2 — Approvals are on the audit stream (Priority: P2)

A reviewer needs to know who approved a quarantined server, a pending or changed tool, or a skill — and when the proxy approved one by itself (trusted baseline, scan-clean auto-approval, `auto_approve_tool_changes`), which policy did it.

**Why this priority**: The reporter's second priority. In a deny-by-default setup an approval is the act that widens access.

**Independent Test**: Approve a quarantined server and a changed tool through the Web UI, CLI and the `quarantine_security` MCP tool; let a trusted server baseline its tools; let a scan-mode server auto-approve a clean change. Each writes `admin_action` lines with the right `action`, `caller` and, for automatic approvals, `caller.kind: internal` with `trigger: policy` and a closed `policy` reason.

**Acceptance Scenarios**:

1. **Given** server `github` is quarantined, **When** an administrator approves it in the Web UI, **Then** one `server.unquarantine` line is written with `caller.kind: api_key` (or `session_admin` in the server edition) and `client.surface: web`.
2. **Given** a tool's description changed and is held, **When** it is approved with `mcpproxy upstream approve`, **Then** one `tool.approve` line names the server, the tool, the approved hash prefix and the previous one.
3. **Given** a trusted server connects for the first time, **When** the proxy baselines its 40 tools after a clean scan, **Then** one `tool.approve` line per server and pass is written with `caller.kind: internal`, `trigger: policy`, `policy: trusted_baseline`, `target.tool_count: 40` and the tool names (capped, with `truncated: true` beyond the cap).

---

### User Story 3 — Config and server changes are on the audit stream (Priority: P3)

A reviewer needs to know who added, removed, enabled or edited an upstream server, changed a profile or a client binding, or changed a global setting — and, for edits made on disk, that there was no caller.

**Why this priority**: The reporter's third priority; the activity rows already carry before/after values, so the gap is the stream, not the data.

**Independent Test**: Make each change through REST, CLI, Web UI and the `upstream_servers` / `profiles` MCP tools, then edit `mcp_config.json` by hand. Each writes one line; the hand edit writes `caller.kind: none`, `trigger: config_file`. Changed fields appear by name; enumerated values appear verbatim; free-text values appear only as fingerprints; secret-bearing values appear as neither.

**Acceptance Scenarios**:

1. **Given** server `ast-grep` exists, **When** `PATCH /api/v1/servers/ast-grep` changes `enabled` and `env.GITHUB_TOKEN`, **Then** one `server.update` line lists `enabled` with `before: true, after: false` and `env.GITHUB_TOKEN` with `redacted: true` and no value or hash.
2. **Given** the config file is edited on disk to add a server, **When** the watcher reloads it, **Then** one `server.add` line is written with `caller: {kind: none}`, `trigger: config_file`, no `request_id`.
3. **Given** a PATCH that changes nothing, **Then** no line is written.

---

### Edge Cases

- **No caller**: a file edit, the admission gate at load, a startup sweep. The line carries `caller.kind: none` (observed, not made, by the proxy) or `internal` (the proxy decided by policy or schedule) plus a closed `trigger`; it never invents an actor and never carries `request_id`.
- **Refused or failed attempt** (403, 404, 409, validation error): nothing changed, no line. Refused administrative attempts are a separate, later event (Out of scope).
- **Bulk change** (approve-all, restore a profile set, a reload that touches 30 servers): one line per target, never one line for the batch — except automatic tool approvals, which are one line per server and pass (Story 2, scenario 3).
- **Audit sink off**: nothing is written, nothing is buffered, behaviour is unchanged.
- **Crash between commit and line**: the change is kept and its line is lost. This is the one gap, documented like the existing `authz` crash window.
- **Bus saturated**: the line count is unaffected — the line never goes through the event bus.
- **Agent token acting**: an agent token that reaches an administrative door is recorded as `caller.kind: agent_token` with `token_name`, `token_prefix` and `profile_pin`; a browser login (Spec 132, `mcp_oat_`) is recorded the same way, as the existing caller derivation already does.
- **Names that look like credentials**: a server, profile or token name shaped like a credential is masked per field, as on `authz` lines.

## Requirements *(mandatory)*

### Functional Requirements

**Event and compatibility**

- **FR-001**: The audit stream MUST gain a fourth event kind, `admin_action`, written to the same sink as `authz`, `tool_call` and `auth_event`.
- **FR-002**: `admin_action` MUST ship under `schema_version: 1` as an additive change (a new `event` value, new keys, new `caller.kind` values), following the published rule "adding a key or an enum value is a minor change". The published schema `docs/schemas/audit-line-v1.schema.json` and its contract copy MUST be updated in place (same `$id`) with an `admin_action` block, and the audit-log change log MUST gain an entry.
- **FR-003**: The audit-log page MUST state the compatibility rule for log shippers: a shipper filtering on `schema_version == 1` receives `admin_action` lines with no change; a consumer that switches on `event` MUST ignore values it does not know; a consumer that validates against a copy of the v1 schema taken before this change MUST refresh that copy, because `event` and `caller.kind` are closed enums. The page's sentence that a strict consumer "keeps validating after a minor additive change" MUST be narrowed to new keys, which is all it is true for.
- **FR-004**: No existing key, value or count invariant of `authz`, `tool_call` or `auth_event` changes.

**Shape**

- **FR-005**: Every `admin_action` line MUST carry the common keys (`schema_version`, `ts`, `event`, `origin`, `source`, `caller`) plus `action`, `target` and `outcome: success`; `changes`, `trigger`, `policy`, `request_id` and `client` as defined in [contracts/admin-action-line.md](contracts/admin-action-line.md).
- **FR-006**: `action` MUST be one value of a closed `<area>.<verb>` vocabulary (the contract's action list). An `admin_action` line MUST NOT carry `surface`, `operation`, `decision`, `disclosed`, `server`, `tool`, `args_sha256`, `args_bytes`, `error_class`, `duration_ms`, `flags` or `reason` — those keys keep their per-event meaning on the other three kinds.
- **FR-007**: `target` MUST name what changed (`kind` plus `name`, and `server` for a tool or skill), never a secret.

**Actor**

- **FR-008**: `caller` MUST be derived exactly as for `authz` lines (`auth.AuthContext` + credential kind + connection source; never an argument or a caller-supplied header), so one caller index covers every event kind. The #1842 activity vocabulary maps onto it: `actor_kind` → `caller.kind` (table in the contract), `actor_name` → `caller.token_name` / `caller.user_email`, `request_id` → `request_id`, and the caller-asserted `surface` (`web`, `macos`, `cli`, `mcp`, `api`) → `client.surface`, untrusted like `client.name`.
- **FR-009**: A change with a request behind it MUST carry that request's `request_id`, equal to the `request_id` on its activity row.
- **FR-010**: A change with no request behind it MUST carry `caller.kind: none` (the proxy observed a change it did not make: a config file edit) or `caller.kind: internal` (the proxy decided by itself: policy auto-approval, admission gate, startup sweep, scheduled refresh), plus a `trigger` from a closed list. Such a line MUST NOT carry `request_id` or any identity key. An absent actor means "no caller", never an unknown one — the #1842 rule.
- **FR-011**: An offline CLI command that changes the rules while the core is stopped MUST record `caller.kind: cli_offline` when it writes a line (see FR-021). A server-edition tenant acting on their own servers, tokens or credentials is recorded as `caller.kind: session_user`, which becomes legal on `admin_action` lines only.

**What is recorded**

- **FR-012**: The closed action list, in the reporter's priority order, is: (P1) tokens and credentials — agent token create/rotate/revoke/delete, client credential create/rotate (stage, finalize, rollback)/forget, browser-login approve/deny/revoke, keyring secret set/delete (name only); (P2) approvals — server quarantine/unquarantine, security review approve/reject, tool approve/block/enable/disable (manual and automatic), prompt approve, skill approve/block/unblock, TPA bundle rollback/unpin/reload/activate, scanner install/remove/configure/enable/disable; (P3) config — server add/update/remove/enable/disable, global config update, registry add/update/remove, profile create/update/delete/rename/classify/set-anonymous/activate, client binding, and in the server edition tenant tokens, JWT mint, user enable/disable, server sharing, tenant servers and tenant credentials. [contracts/admin-action-line.md](contracts/admin-action-line.md) §Actions maps every `action` to its doors; every other mutating door is on its exemption list with a reason.
- **FR-013**: Exactly one line MUST be written per committed change to one target, after the change commits and before the request returns. A refused, failed or no-op request MUST write no line.
- **FR-013a**: A request that changes several targets MUST write one line per target, all with the request's `request_id`. A server the admission gate quarantines inside a caller's own add is one `server.add` line with `quarantined` in `changes`.
- **FR-014**: Tool, prompt and skill decisions MUST be written as one line per server per request (manual) or per server per pass (automatic), carrying `target.tool_count` and up to 64 names, with `target.truncated: true` beyond that. An automatic decision carries `policy` (`trusted_baseline` | `scan_clean` | `auto_approve_tool_changes` | `hash_restored` | `skills_auto`).
- **FR-015**: `changes` MUST list changed fields by name. A value MUST appear verbatim only when the field is on the contract's closed literal list (booleans, enums, bounded integers, token prefixes, content-hash prefixes). Any other value MUST appear only as `before_sha256` / `after_sha256` (RFC 8785 canonical JSON, the existing `args_sha256` algorithm). A secret-bearing field (env values, header values, API keys, OAuth client secrets, encryption keys, any value resolved from `${env:}`/`${file:}`/`${keyring:}`) MUST appear as `redacted: true` with neither value nor hash.
- **FR-015a**: A line MUST hold at most 128 `changes` entries, sorted by `field`; beyond that it carries `changes_truncated: true` and `changes_count`.
- **FR-016**: A line MUST NEVER contain a token value, API key, cookie, JWT, OAuth secret, key material, an env or header value, or a hash of any of them. The guarantee is structural (the builder has no input through which such a value can arrive), backed by the existing whole-line sanitizer and its hit counter.

**Failure semantics**

- **FR-016a**: A change to `audit_log` itself MUST be recorded as `config.update` by the running sink before the restart that applies it.
- **FR-016b**: `ts` MUST be the commit time. File order across concurrent changes to different targets is not guaranteed; consumers order by `ts`.

- **FR-017**: A failed audit write MUST NOT refuse, roll back or delay the change. It follows the existing sink failure policy: `mcpproxy_audit_write_failures_total`, a `mcpproxy doctor` finding, a log line at most once a minute. There is still no `audit_log.strict` mode.
- **FR-018**: The line MUST be written synchronously through the sink, never through the activity event bus; the count invariant `#admin_action == #committed changes in the action list` MUST hold with the bus saturated.

**Readers, editions**

- **FR-019**: No REST, SSE, MCP, CLI or Web UI surface reads audit lines, before or after this feature; scoped (agent) tokens can read nothing new. The visibility of the matching activity rows is unchanged.
- **FR-020**: The feature is edition-neutral: lines are written whenever `audit_log` is enabled (personal default off, server default on). Server-edition-only doors (users, roles) write lines in the server edition only. No connector, shipper, Kubernetes or Loki integration is added; shipping remains the operator's log shipper, as today.
- **FR-021**: Offline CLI writes that change the rules (inventory.md §"Offline CLI") MUST write their line to `audit_log.path` when one is configured, through the same builder; the stdout sink is never used by an offline command.

**Guard rails**

- **FR-022**: A repository test MUST enumerate every administrative write door — every mutating REST route under `/api/v1` (personal and `-tags server`), every mutating operation of the built-in MCP admin tools, and every offline CLI write — and fail when one is neither mapped to an `action` nor listed as exempt with a reason. A new write door therefore cannot merge without a record.
- **FR-023**: For every mapped door, a table test MUST drive the door with a capturing sink and assert exactly one line with the expected `action`, `caller.kind`, `target` and no forbidden key; and every line MUST validate against the updated schema.

### Key Entities

- **Admin action line**: one JSON object per committed administrative change; `action`, `target`, `changes`, `caller`, `trigger`/`policy`, `request_id`.
- **Door registry**: the checked-in list mapping each write door to its `action` (or an exemption) that FR-022's test reads.
- **Change entry**: `{field, before?, after?, before_sha256?, after_sha256?, redacted?}`.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: 100% of the doors in inventory.md's action list write exactly one line per committed change, shown by the door table test (FR-023).
- **SC-002**: A reviewer can answer "who minted, regenerated or revoked token X, and when" from the audit file alone, without the activity log, for every token change made after upgrade.
- **SC-003**: With the event bus saturated (2,000 administrative changes in a burst), the file holds 2,000 `admin_action` lines.
- **SC-004**: Zero secret values or secret hashes on any line across the door table test and a fuzzed config-change test; the sanitizer hit counter stays at 0.
- **SC-005**: A change made while the audit disk is full still succeeds in 100% of cases.
- **SC-006**: Adding a mutating REST route without a registry entry fails CI.

## Assumptions

These were decided without asking, under the maintainer's instruction to work autonomously; each is recorded with its alternative in research.md.

- **A1 — Additive within v1, not v2.** The published versioning rule already classes a new enum value as minor, and a v2 bump would break every shipper filter on `schema_version == 1` for the existing three kinds. Cost: a validator pinned to the old v1 copy rejects `admin_action` lines; FR-003 tells shippers so.
- **A2 — The audit `caller` object, not the #1842 key names.** The audit file already names callers with `caller.kind`; a second spelling (`actor_kind`) on one event kind would split every SIEM index. The #1842 keys map one-to-one (FR-008); the caller-asserted `surface` goes under `client`, because the audit doc never puts a header-derived value in a trusted key.
- **A3 — A failed audit write never refuses the change.** Fail-closed would make a full disk block token revocation and quarantine — the safety actions. This matches the existing sink policy.
- **A4 — Committed changes only.** Refused or failed administrative attempts are not recorded in this feature; they are a candidate follow-up (`admin_authz`?), because "who tried" is a different question with a different count invariant.
- **A5 — Field names plus literal values for a closed list, fingerprints otherwise, nothing for secrets.** The issue suggested names only; the reporter's reviewers asked for before/after; this keeps the audit doc's structural redaction guarantee while answering "enabled went from true to false".
- **A6 — Automatic approvals are recorded** (`caller.kind: internal`, `trigger: policy`), batched per server and pass, because "who approved this tool" has the answer "the proxy, by this policy", and the reviewer needs to see it.

## Out of Scope

- Recording refused administrative attempts (A4).
- A read surface for audit lines, a connector, or shipping to Kubernetes/Loki (server-edition roadmap, unchanged).
- Changing the activity log beyond one by-product: PR-2 passes the request context to the tool-approval funnels, so `tool_quarantine_change` rows gain the #1842 actor keys (additive). Nothing else about activity rows, keys or visibility changes.
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

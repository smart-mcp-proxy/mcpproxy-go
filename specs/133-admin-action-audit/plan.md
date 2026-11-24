# Implementation Plan: `admin_action` audit events

**Branch**: `133-admin-action-audit` | **Date**: 2026-11-24 | **Spec**: [spec.md](spec.md)
**Input**: Feature specification from `specs/133-admin-action-audit/spec.md`; door inventory [inventory.md](inventory.md); line contract [contracts/admin-action-line.md](contracts/admin-action-line.md); decisions [research.md](research.md).

## Summary

Add a fourth event kind, `admin_action`, to the Spec 107 audit JSONL: one line per committed administrative effect, written synchronously where the effect persisted, from what persisted, with the caller derived the way `authz` lines derive it. The action list follows the reporter's priority order — tokens, then approvals, then config — and ships in five PRs, each mergeable alone. A door-registry test lands in the first PR, so no administrative write door can merge afterwards without either a line or a written exemption.

The unit of record is the committed effect, not the response (spec FR-013): the line is written where a change persisted, from what persisted.

## Technical Context

**Language/Version**: Go 1.26 (backend only; no Web UI, macOS or CLI output change beyond docs)
**Primary Dependencies**: none new. `internal/audit` (builder, `redact.go` masking, `sink.go`), `gopkg.in/natefinch/lumberjack.v2` (already used by the file sink), `chi.Walk` and the Cobra command tree for the registry test
**Storage**: the existing audit file or stdout stream. No BBolt bucket, no activity schema change (two additive by-products, spec Out of Scope)
**Testing**: `go test -race` on `internal/audit`, `internal/runtime`, `internal/server`, `internal/httpapi`, `internal/management`, `internal/security/scanner`, `cmd/mcpproxy`; `-tags server` for `internal/serveredition`; schema validation with the JSON Schema compiler `internal/audit/schema_test.go` already uses
**Target Platform**: personal (macOS, Windows, Linux) and server edition (Docker)
**Project Type**: single Go module
**Performance Goals**: one synchronous sink write per committed change (the same cost as one `authz` line); administrative changes are rare next to tool calls, so no batching beyond FR-014
**Constraints**: never through the event bus (FR-018); never refuses a change (FR-017); no secret, token value or hash of one on a line (FR-016)
**Scale/Scope**: about 90 mutating (door, branch) pairs (inventory.md §1–§5 plus the round-1 additions), ~55 actions, 5 implementation PRs

## Constitution Check

| Principle | Status | Note |
|---|---|---|
| I. Performance at Scale | Pass | One line per committed effect; tool decisions batched per batch key (FR-014) so a 3,000-tool baseline writes one line per server; lines capped at 16 KiB |
| II. Actor-Based Concurrency | Pass | The sink's own mutex serialises writes; lines are written after the commit, outside the config lock (the write must not extend the commit's critical section), so file order is not commit order (FR-016b) |
| III. Configuration-Driven | Pass | No new config key; gated by the existing `audit_log` block |
| IV. Security by Default | Pass | Structural redaction (contract §Changes); default-redact for unlisted fields; no new read surface |
| V. TDD | Pass | Every slice lands its door table tests red first; the registry test fails until each door is mapped |
| VI. Documentation Hygiene | Pass | `docs/features/audit-log.md` (event table, `admin_action` section, compatibility rule, change log), schema file, `CLAUDE.md` Recent Changes in the last slice |
| Event-Driven Updates ("state changes MUST propagate via the event bus") | Justified deviation | The bus still carries every SSE and activity event it carries today. The audit line is a *record*, not propagation, and the audit doc already requires the record to bypass the droppable bus (`authz`, `tool_call` do the same). |

## Design

### D-1. Builder (`internal/audit`)

`NewAdminAction(AdminActionInput) (Line, error)` beside `NewAuthz`/`NewToolCall`/`NewAuthEvent` in `line.go`. Input is a typed struct: `Ts`, `RequestID`, `TransportRequestID`, `Origin`, `Source`, `Caller`, `ClientName/Version/IP/Surface`, `Profile`, `Action` (typed constant), `Target` (typed per kind), `Changes []Change`, `Trigger`, `Policy`. A `Change` can be built only by `Literal(field, before, after)` — whose value parameters are a sealed `LiteralValue` interface implemented by `bool`, bounded `int64`, `time.Time`, the closed enum types, `TokenPrefix`, `HashPrefix` and `NameList` — or by `Withheld(field, WithheldSecret|WithheldValue)`. There is no constructor that takes a `string` or `any` value and no hashing path, so FR-016 is structural. `newBase` is not reused as is: it always sets `request_id`, which must be omitted (not `""`) on background lines. The builder enforces the contract's `iff` rules, the batch caps and the 16 KiB budget (contract §Size), and returns an error for a violation, which the auditor logs and counts like a builder rejection today (`internal/server/audit_funnel.go:334-345`).

The classifier — literal, withheld secret, withheld value — is one table keyed by field path in `internal/audit/admin_fields.go`; an unknown path is `WithheldValue`. PR-1 also adds `mcp_(agt|cli|oat|ort)_[A-Za-z0-9_-]{16,}` to `credentialPatterns` (`internal/audit/redact.go:38`), which every event kind and the whole-line sanitizer share.

### D-2. Seam (`internal/runtime`) and attribution

```go
// AdminAuditor writes admin_action lines. nil = audit off.
type AdminAuditor interface {
    AdminAction(ctx context.Context, rec AdminRecord)
}
```

`Runtime.SetAdminAuditor` is called by `internal/server` when it receives the sink (`server.go:504`, where `mcpProxy.auditSink` is set). The implementation derives `audit.Caller` with `auditCallerFromContext` (`audit_funnel.go:231`) — one derivation for every event kind — reads `origin`/`source`/`client.ip` from the metadata `TagRequestMeta` stores, maps `RequestActor(ctx).Surface` to `client.surface`, and writes through `p.auditSink`. `AdminRecord` carries an explicit attribution for background effects (`Background{Kind: none|internal|cli_offline, Trigger, Policy}`); when it is set the context is ignored, so a nil AuthContext on an admin funnel never becomes `anonymous`.

Attribution plumbing that PR-1 lands (round-1 findings, all verified):

- **One request id per admin ingress.** The MCP admin tools mint a local correlation id for their wrapper row and pass the original context to the mutation (`internal/server/mcp_credentials_tool.go:174-180,345-351`, `mcp.go:4797,4955`); they will install it with `reqcontext.WithRequestID` before dispatch. Connect records its mint and finalize with `context.Background()` (`internal/runtime/clients_service_connect.go:217-233,328-363`); it will receive the request context.
- **Server-edition mount.** The server-edition routes are a sibling group with their own auth middleware (`internal/serveredition/setup.go:343-350`), outside the core `/api/v1` chain that runs `TagRequestMeta` and `actorContextMiddleware` (`internal/httpapi/server.go:1100-1111`); an untagged context would make `auditSourceFromContext` default to `mcp` (`audit_funnel.go:198-207`). PR-4 installs both middlewares on that group.

### D-3. Hooks: where the committed effect is observed

The rule (spec FR-013): the line is written where the effect persisted, from what persisted. Concretely:

| Effect | Hook | Notes |
|---|---|---|
| Token / client / login lifecycle | `CredentialsService.writeLifecycle` (`credentials_service.go:485`), `ClientsService.writeChange` (`clients_service.go:605`), `OAuthLoginService` approve/deny/revoke (`oauth_login_consent.go:901,928`, `oauth_login_tokens.go:253`), Connect mint/finalize/undo (`clients_service_connect.go`) | beside the existing sync `profile_change` write (PR-1) |
| Token hard delete, rotation staging, keyring | `tokens.go:411`, `ClientsService.Rotate` and the Connect staging path, `resolver.Store`/`Delete` callers (`server.go:5183,5274,3303`, `secrets_cmd.go`, `upstream_cmd.go:1579-1601`), `status --reset-key` | new calls, each after its own write succeeds (PR-1) |
| Server config effects | the commit pipeline itself: `Runtime.Commit` returns a `CommitResult` with an immutable committed delta (servers added/removed/changed with field paths, global key paths, profile paths, desired-vs-published flag) and the commit timestamp. Every writer — `PatchServer` (production REST PATCH and config-to-secret, `internal/httpapi/server.go:3147-3162,3338-3347`), the MCP writer (`mcp_upstream_writer.go:67-95`), `MutateConfig`/`applyConfigWith` (`config_funnel.go:87-99`, `runtime.go:2273-2329`, which today discards the result), `ReloadConfiguration` incl. restart-only reloads (`lifecycle.go:2303-2318`) — hands that result to one emitter | PR-3. Diffing the candidate before the commit is wrong: admission can replace it before persistence |
| Admission quarantine | staged in the commit transaction and emitted after success, folded into the caller's `server.add` when there is a caller. Today it fires a goroutine before persistence (`config_load_admission_gate.go:253-270`) | PR-3 |
| Enable/disable | `EnableServerCtx` / `BulkEnableServers` from the commit result, so a no-op toggle writes nothing (today's activity row invents `previous = !enabled`, `lifecycle.go:2437`) | PR-3 |
| Server quarantine, review approve/reject | `QuarantineServerCtx`/`QuarantineServerAtCtx` (`lifecycle.go:2459,2474`), `FinishServerApprovalCtx` (`scanner/service.go:2607`), `RejectServer` (`:2641`) | PR-2 |
| Tool decisions | collected at each successful approval-record save in `tool_quarantine.go` and the reviewed path `tool_approval_review.go:76-187` (it bypasses the public `ApproveTools*` writers), incl. hash restores that save without an activity event (`tool_quarantine.go:1342-1377,1516-1545`); flushed as batch-key lines at the request or pass boundary. The public funnels gain `ctx` variants | PR-2 |
| Prompts, skills | `prompt_quarantine.go:372,421`; skills `mutateSkillsAt` and refresh transitions (`skills_catalog.go:587-632,686`) | PR-2 |
| Bundle | rollback/unpin/reload receive the request context (today none, `security_bundle.go:142-146,184,207`); `bundle.activate` is written once by the refresh cycle that activated, with its initiator (`tpa_refresh.go:537-589`), not by the activation observers (detached goroutines that also fire on rollback, `tpa_bundle_observer.go:75-104`) | PR-2 |
| Scanners | `InstallScanner`/`RemoveScanner`/`ConfigureScanner` (`scanner/service.go:961,1080,1148`) | PR-2 |
| Profiles, bindings, registries, active profile | `writeProfileChanges` (`profile_change_diff.go:222`) via the commit result; `SetBinding`/`BulkAssign`; registry commits (`add_registry_source.go:135`, `edit_…:104`, `remove_…:49`); `PUT /profiles/active` | PR-3 |
| Server edition | `user_handlers.go`, `admin_handlers.go`, `admin_tokens.go`, `auth_endpoints.go:156` (at signing), SSO provisioning (`oauth_handler.go:461-525`), the broker callback persist (`broker/oauth_connector.go:244-257`) | PR-4 |

## PR slicing

Each PR is mergeable alone, keeps every existing test green, and leaves the registry test passing. Dependencies are stated, not implied.

| PR | Scope | Depends on | Gate |
|---|---|---|---|
| **PR-0** (this) | Spec, inventory, contract, plan | — | Maintainer review |
| **PR-1 — seam + tokens (P1)** | Schema changes (contract preamble) + change log; `NewAdminAction`, classifier, credential-pattern extension; `AdminAuditor` and background attribution; request-id plumbing for MCP admin tools and Connect; **door registry + registry test** with the pending set frozen; all P1 lines incl. Connect's `client.bind` (it commits with the mint, so it ships with P1, not P3); docs section | — | P1 door table tests (exact line sets incl. the `credentials revoke` cascade and config-to-secret partial failure); schema tests; 2,000-revocation bus-saturation test |
| **PR-2 — approvals (P2)** | `ctx` variants for the tool-approval funnels (activity `tool_quarantine_change` rows gain the actor keys); tool decision collector + batch flush; `server.quarantine|unquarantine` (caller), `server.review_*`, `tool.*`, `prompt.approve`, `skill.*`, `bundle.*`, `scanner.*` | PR-1 | P2 door table tests; 3,000-tool baseline fixture → one line per server per batch key; mixed approve/block review → two lines |
| **PR-3 — config (P3, personal)** | `CommitResult` committed delta and one emitter for every writer; `server.*` incl. bulk and MCP update/patch; admission staged in the transaction and folded into `server.add` (also the admission-only `server.quarantine`); `config.update` (incl. `audit_log`, telemetry CLI); file reload (`none`); registries; `profile.*`; `client.bind` for bindings; offline `upstream` writes | PR-1 (independent of PR-2: the admission line is owned here, not in PR-2) | P3 door table tests; config fuzz test (random diffs: nothing outside the literal list, no hash); whole-config apply adding a gated server; pending `audit_log` edit and its reversal |
| **PR-4 — server edition** | Middleware on the server-edition group; tenant tokens (`owner_user_id`), `jwt.issue`, `user.*`, `server.share`, `user_server.*`, `user_credential.*` | PR-1; its admin enable/disable rows need PR-3's emitter and stay `pending` until PR-3 lands | `-tags server` door table tests |
| **PR-5 — strict** | Registry test turns `pending` into a failure; `CLAUDE.md` Recent Changes | PR-1..PR-4 | Registry test strict |

PR-2, PR-3 and PR-4 can be reviewed in parallel after PR-1.

## Tests that pin each door

1. **Door registry test** (D9 in research.md): enumerates doors from the constructed personal and `-tags server` routers (`chi.Walk`, all methods, the server-edition sibling group included), the running MCP server's admin tools and their accepted operation values plus aliases, and the constructed Cobra tree with flag-dependent branches. Each must be in the registry (`{door, branch, action | exempt reason, slice}`), each registry entry must still exist, the pending set may only shrink, and `GET` routes are exempt unless on the registry's named state-changing list.
2. **Door table test** per slice: for each `done` (door, branch), drive the real handler (`httptest` against the router with an API key, a socket context, an agent token, a browser-login token, a session admin and a session user in `-tags server`; the MCP tool handler; the CLI command against a temp `HOME`) with a capturing sink, and assert the **exact set** of lines: `action`, `caller.kind`, `target`, `request_id` equal to the activity row's where one exists, no forbidden key, schema-valid. The same harness asserts that a driven `GET` not on the state-changing list writes nothing to storage or config.
3. **No-op, refusal and partial-failure tests**: a no-op body and a refused caller (scoped token, 404, 409) write zero lines; an injected failure after the first of two effects writes exactly the first effect's line.
4. **Count invariant under a saturated bus**: fill the event bus, commit 2,000 effects, assert 2,000 lines (the Spec 107 pattern for `authz`).
5. **Failure semantics**: a sink whose `Write` always fails — every change still succeeds, `WriteFailures()` counts them, doctor reports it; an offline command with an unwritable path warns once on stderr and keeps its exit status.
6. **Redaction**: classifier table test (every literal path has a literal Go type; every path under `env`, `headers`, `oauth`, `secrets` is `WithheldSecret`; an unknown path is `WithheldValue`); a compile-level test that `Change` has no string/any value constructor; the config fuzz test of PR-3; a test that a `mcp_agt_…` value in a name or `X-Request-Id` is masked and counted; `SanitizerHits()` zero across all door tests.
7. **Schema**: `internal/audit/schema_test.go` gains `admin_action` examples and forbidden-key rejections, the event-aware producer-strict check, and the "pre-133 examples still validate" check; contract and published copies stay identical (the existing test).

## Open questions (left to review)

- **Q1**: `auth tokens decrypt` (offline) changes how upstream OAuth tokens are stored at rest. Exempt as maintenance, or `config.update` with `field: oauth_token_storage`? The contract exempts it; a reviewer may prefer a line.
- **Q2**: Recording refused administrative attempts (spec A4) — a separate `admin_authz` event, or `outcome: denied` on `admin_action`? Out of scope here; named so it is not lost.
- **Q3**: Upstream OAuth login/logout (`POST /servers/{id}/login|logout`) is exempt as "not an access rule". If reviewers count an upstream credential as a rule, it is a P1-shaped `secret.*`-like action.

## Project Structure

### Documentation (this feature)

```text
specs/133-admin-action-audit/
├── spec.md
├── plan.md                         # this file
├── research.md                     # decisions D1–D9
├── inventory.md                    # every write door, what it records today
├── contracts/admin-action-line.md  # the line, key by key; action list; exemptions
└── checklists/requirements.md
```

No `data-model.md` (no stored entity: the line is the contract) and no `quickstart.md` (the PR-1 docs section in `docs/features/audit-log.md` is the operator quickstart). No `tasks.md` yet: `/speckit.tasks` runs once the maintainer accepts the shape.

### Source Code (touched by PR-1..PR-5)

```text
internal/audit/        line.go (NewAdminAction), admin_fields.go, redact.go (patterns), admindoors.go (+ tests), schema_test.go
internal/runtime/      admin_auditor.go (seam), commit.go (CommitResult delta), credentials_service.go, clients_service*.go,
                       oauth_login_*.go, tool_quarantine.go, tool_approval_review.go, lifecycle.go, config_funnel.go,
                       config_load_admission_gate.go, skills_*.go, prompt_quarantine.go
internal/server/       admin_auditor.go (sink-backed implementation), server.go, mcp.go, mcp_credentials_tool.go, mcp_upstream_writer.go
internal/httpapi/      tokens.go (hard delete), server.go (secrets, PATCH), security_bundle.go (ctx)
internal/security/scanner/  service.go, tpa_refresh.go (review approve/reject, bundle, scanners)
internal/serveredition/     setup.go (middleware), api/*.go, auth/oauth_handler.go, broker/oauth_connector.go (PR-4)
cmd/mcpproxy/          secrets_cmd.go, status_cmd.go, telemetry_cmd.go, upstream_cmd.go, connect_credential.go
docs/                  features/audit-log.md, schemas/audit-line-v1.schema.json
specs/107-server-edition-sso-hardening/contracts/audit-line.schema.json
```

**Structure Decision**: single Go module; the line builder stays in the leaf `internal/audit`, the seam in `internal/runtime`, the sink-backed implementation in `internal/server` (the only package that holds the sink today).

## Complexity Tracking

| Violation | Why needed | Simpler alternative rejected because |
|---|---|---|
| Synchronous write outside the event bus | FR-018: the record must survive bus saturation | Consuming activity events from the bus drops them on overflow (inventory.md §0.2) |
| `CommitResult` committed delta | FR-013: the line must describe what persisted; today several writers diff the candidate or discard the result | Diffing at each door duplicates the logic and misses admission rewrites and restart-only reloads |
| `ctx` variants of the tool-approval funnels | FR-008: tool approvals must name the caller, and today the funnel receives only the string `"api"`/`"mcp"` | Passing the actor as a string would duplicate the caller derivation and lose `token_prefix`/`profile_pin` |

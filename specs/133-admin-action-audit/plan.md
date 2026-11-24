# Implementation Plan: `admin_action` audit events

**Branch**: `133-admin-action-audit` | **Date**: 2026-11-24 | **Spec**: [spec.md](spec.md)
**Input**: Feature specification from `specs/133-admin-action-audit/spec.md`; door inventory [inventory.md](inventory.md); line contract [contracts/admin-action-line.md](contracts/admin-action-line.md); decisions [research.md](research.md).

## Summary

Add a fourth event kind, `admin_action`, to the Spec 107 audit JSONL: one line per committed administrative change, written synchronously at the runtime funnel that commits it, from the commit's snapshot, with the caller derived the way `authz` lines derive it. The action list follows the reporter's priority order — tokens, then approvals, then config — and ships in four PRs, each mergeable alone. A door-registry test lands in the first PR, so no administrative write door can merge afterwards without either a line or a written exemption.

## Technical Context

**Language/Version**: Go 1.26 (backend only; no Web UI, macOS or CLI output change beyond docs)
**Primary Dependencies**: none new. `internal/audit` (builder, `canonical.go` RFC 8785 hash, `redact.go` masking, `sink.go`), `gopkg.in/natefinch/lumberjack.v2` (already used by the file sink), `go/ast` / `go/parser` for the registry test
**Storage**: the existing audit file or stdout stream. No BBolt bucket, no activity schema change (one additive by-product, see PR-2)
**Testing**: `go test -race` on `internal/audit`, `internal/runtime`, `internal/server`, `internal/httpapi`, `internal/management`, `internal/security/scanner`, `cmd/mcpproxy`; `-tags server` for `internal/serveredition`; schema validation with the JSON Schema compiler `internal/audit/schema_test.go` already uses
**Target Platform**: personal (macOS, Windows, Linux) and server edition (Docker)
**Project Type**: single Go module
**Performance Goals**: one synchronous sink write per committed change (the same cost as one `authz` line); administrative changes are rare next to tool calls, so no batching beyond FR-014
**Constraints**: never through the event bus (FR-018); never refuses a change (FR-017); no secret, token value or hash of one on a line (FR-016)
**Scale/Scope**: about 80 mutating doors (inventory.md §1–§5), ~45 actions, 4 implementation PRs

## Constitution Check

| Principle | Status | Note |
|---|---|---|
| I. Performance at Scale | Pass | One line per change; automatic tool approvals batched per server per pass (FR-014) so a 3,000-tool baseline writes one line per server |
| II. Actor-Based Concurrency | Pass | The sink's own mutex serialises writes; the funnel writes after its commit, outside the config lock (the write must not extend the commit's critical section) |
| III. Configuration-Driven | Pass | No new config key; gated by the existing `audit_log` block |
| IV. Security by Default | Pass | Structural redaction (contract §Changes); default-redact for unlisted fields; no new read surface |
| V. TDD | Pass | Every slice lands its door table tests red first; the registry test fails until each door is mapped |
| VI. Documentation Hygiene | Pass | `docs/features/audit-log.md` (event table, `admin_action` section, compatibility rule, change log), schema file, `CLAUDE.md` Recent Changes in the last slice |
| Event-Driven Updates ("state changes MUST propagate via the event bus") | Justified deviation | The bus still carries every SSE and activity event it carries today. The audit line is a *record*, not propagation, and the audit doc already requires the record to bypass the droppable bus (`authz`, `tool_call` do the same). |

## Design

### D-1. Builder (`internal/audit`)

`NewAdminAction(AdminActionInput) (Line, error)` beside `NewAuthz`/`NewToolCall`/`NewAuthEvent` in `line.go`. Input is a typed struct: `Ts`, `RequestID`, `TransportRequestID`, `Origin`, `Source`, `Caller`, `ClientName/Version/IP/Surface`, `Profile`, `Action` (typed constant), `Target` (typed), `Changes []Change`, `Trigger`, `Policy`. `Change` carries a field path and a `ChangeValue` that can only be built by three constructors — `Literal(field, before, after)` (accepts `bool`, `int64`, a typed enum, a prefix type), `Fingerprint(field, before, after any)` (hashes immediately via `canonical.go`, keeps only the hex), `Redacted(field)`. There is no constructor that stores an arbitrary string value, so FR-016 is structural. The builder enforces the contract's `iff` rules (request_id vs. no-caller kinds, trigger vs. kind, policy vs. trigger, the 128-entry cap, the 64-name cap) and returns an error for a violation, which the auditor logs and counts like a builder rejection today (`audit_funnel.go:334-345`).

The field classifier — which paths are literal, which are fingerprinted, which are redacted — lives in one table in `internal/audit/admin_fields.go`; an unknown path is `Redacted`.

### D-2. Seam (`internal/runtime`)

```go
// AdminAuditor writes admin_action lines. nil = audit off.
type AdminAuditor interface {
    AdminAction(ctx context.Context, rec AdminRecord)
}
```

`Runtime.SetAdminAuditor` is called by `internal/server` right after it receives the sink (`server.go:504`, where `mcpProxy.auditSink` is set). The server-side implementation turns `ctx` into `audit.Caller` with `auditCallerFromContext` (`audit_funnel.go:231`) — one caller derivation for every event kind (D2 in research.md) — reads `origin`/`source`/`client.ip` from the request metadata `TagRequestMeta` already stores, maps `RequestActor(ctx).Surface` to `client.surface`, and writes through `p.auditSink`. A context without a caller produces `none` or `internal` from `AdminRecord.Trigger`, never `anonymous` (the funnel's default for a nil AuthContext must not apply here: an empty context on an admin funnel means "no request", FR-010).

`AdminRecord` is built by the funnels, next to the existing `stampActor` / `writeChangeRecord` calls, from the same committed snapshot they already use (#1842). Funnels that have no context today take one (PR-2: tool approval; PR-3: MCP update/patch writer).

### D-3. Where each slice hooks

| Funnel (inventory.md) | Hook |
|---|---|
| `CredentialsService.writeLifecycle` (`credentials_service.go:485`), `ClientsService.writeChange` (`clients_service.go:605`), `OAuthLoginService` approve/deny/revoke (`oauth_login_consent.go:901,928`, `oauth_login_tokens.go:253`) | PR-1: beside the sync `profile_change` write |
| `tokens.go:411` hard delete, `ClientsService.Rotate` stage (`clients_service.go:836`), secrets (`server.go:5183,5274,5094,3219`, `secrets_cmd.go`) | PR-1: new calls (no row today) |
| `QuarantineServerCtx`/`QuarantineServerAtCtx` (`lifecycle.go:2459,2474`), `FinishServerApprovalCtx` (`scanner/service.go:2607`), `RejectServer` (`:2641`), admission gate (`config_load_admission_gate.go:146`) | PR-2 |
| `ApproveTools*`, `BlockTools*`, `SetToolEnabled*`, `SetAllToolsEnabled` (`tool_quarantine.go:2888,2895,3519,3525,3578,3682,3229,3285`) — gain `ctx` variants; automatic paths (`tool_quarantine.go:1219,1264,1468,2514,2645,2650`, `tool_quarantine_scan_approve.go:120`) | PR-2 |
| Prompts (`prompt_quarantine.go:372,421`), skills (`skills_approval.go` runtime `:74,:121`, `skills_catalog.go:686`), bundle (`scanner/service.go:488,493,499,506` + activation observer), scanners (`:961,1080,1148`) | PR-2 |
| `Server.AddServer`/`UpdateServer`/`RemoveServer` (`server.go:2350,2524,2606`), `EnableServerCtx` (`lifecycle.go:2418`), `BulkEnableServers` (`:2567`), MCP writer (`mcp_upstream_writer.go:71`), `Runtime.MutateConfig` (`config_funnel.go:65`, global diff), `ReloadConfiguration` (`lifecycle.go:2270`, `trigger: config_file`), registries, profiles (`profile_change_diff.go:222`), `PUT /profiles/active` | PR-3 |
| Offline CLI: `upstream` config mode, `connect` offline backend, `secrets` | PR-3 (PR-1 for `secrets`) via `audit.NewFileSink` on `audit_log.path` |
| Server edition: `user_handlers.go`, `admin_handlers.go`, `admin_tokens.go`, `auth_endpoints.go:156`, `credential_handlers.go` | PR-4 |

`config.update` (PR-3) needs the one genuinely new diff: global keys between the pre- and post-commit config inside `MutateConfig`/`ReloadConfiguration`, excluding `mcpServers` and `profiles` (they get their own lines). It reuses the server delta approach of #1842 (`serverConfigDelta`) at the top level, then classifies each path through D-1's table.

## PR slicing

Each PR is mergeable alone, keeps every existing test green, and leaves the registry test passing.

| PR | Scope | Lands | Gate |
|---|---|---|---|
| **PR-0** (this) | Spec, inventory, contract, plan | `specs/133-admin-action-audit/` | Maintainer review |
| **PR-1 — tokens (P1)** | Schema block + change log; `NewAdminAction`, field table; `AdminAuditor` seam and server install; **door registry + registry test** (every door from inventory.md listed: P1 doors `done`, the rest `pending: PR-2/3/4` or `exempt`); P1 lines: `token.*`, `client.*`, `login.*`, `secret.*` (personal edition); docs section | Reporter priority 1 complete | Door table test for P1; schema test; bus-saturation count test (2,000 token changes) |
| **PR-2 — approvals (P2)** | `ctx` variants for the tool-approval funnels (activity `tool_quarantine_change` rows gain the actor keys as a by-product); `server.*quarantine`, `server.review_*`, `tool.*` (manual + automatic), `prompt.approve`, `skill.*`, `bundle.*`, `scanner.*` | Reporter priority 2 | Door table test for P2; automatic-approval batch test (3,000-tool fixture → one line per server); admission-inside-commit test (Clarification Q1) |
| **PR-3 — config (P3, personal)** | `server.*` incl. bulk and MCP update/patch, `config.update` (global diff, `audit_log` self-change), file reload (`none`), registries, `profile.*`, `client.bind`, offline CLI writes | Reporter priority 3 | Door table test; config fuzz test (random config diffs: no value outside the literal list, no hash of a redacted path); reload test |
| **PR-4 — server edition + strict** | Tenant tokens, JWT mint, users, sharing, tenant servers and credentials (`session_user` legal); confirm the server-edition routers sit under the actor stamping (inventory.md §2 UNCONFIRMED); **registry test turns `pending` into a failure**; `CLAUDE.md` Recent Changes | Feature complete | `-tags server` door table test; registry test strict |

PR-2 and PR-3 touch disjoint funnels and can be reviewed in parallel after PR-1; PR-4 needs PR-1 only, except the strict switch, which merges last.

## Tests that pin each door

1. **Door registry test** (`internal/audit/admindoors_test.go` or under `internal/server`): parses the sources with `go/parser` and enumerates
   - every `r.Post|Put|Patch|Delete(...)` / `r.Method(...)` route registered in `internal/httpapi` (bare build and `-tags server` via build-constrained file sets) and in `internal/serveredition/api`, `internal/serveredition/auth`;
   - every `case "<op>":` in the operation switch of each built-in admin MCP tool (`upstream_servers`, `quarantine_security`, `profiles`, `credentials`) — read-only operations are listed exempt;
   - every Cobra command in `cmd/mcpproxy` whose `RunE` reaches a storage or config write without the daemon (`openTokensStorage`, `config.SaveConfig`, `resolver.Store`, the offline connect backend).
   Each enumerated door must appear in the checked-in registry (`admindoors.go`: `{door, action | exempt reason, slice}`), and each registry entry must still exist in the source. Until PR-4, `pending` entries are allowed and listed in the test output.
2. **Door table test** per slice: for each `done` door, drive it through its real handler (`httptest` against the router with an API key, a socket context, an agent token, a session admin in `-tags server`; the MCP tool handler; the CLI command against a temp `HOME`), with a capturing sink. Assert exactly one line per target, the expected `action`, `caller.kind`, `target`, `request_id` equal to the activity row's, no forbidden key, schema-valid.
3. **No-op and refusal test**: the same doors with a no-op body and with a refused caller (scoped token, 404, 409) write zero lines.
4. **Count invariant under a saturated bus**: fill the event bus, run 2,000 changes, assert 2,000 lines (the Spec 107 pattern for `authz`).
5. **Failure semantics**: a sink whose `Write` always fails — every change still succeeds, `WriteFailures()` counts them, doctor reports it.
6. **Redaction**: the field classifier table test (every literal path is bool/enum/int/prefix-typed; every path under `env`, `headers`, `oauth`, `secrets` is redacted) plus the config fuzz test of PR-3; `SanitizerHits()` stays zero across all door tests.
7. **Schema**: `internal/audit/schema_test.go` gains `admin_action` examples and forbidden-key rejections; contract and published copies stay identical (the existing test).

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

### Source Code (touched by PR-1..PR-4)

```text
internal/audit/        line.go (NewAdminAction), admin_fields.go, admindoors.go (+ tests), schema_test.go
internal/runtime/      admin_auditor.go (seam), credentials_service.go, clients_service.go, oauth_login_*.go,
                       tool_quarantine.go, lifecycle.go, config_funnel.go, config_load_admission_gate.go, skills_*.go
internal/server/       admin_auditor.go (sink-backed implementation), server.go, mcp.go, mcp_upstream_writer.go
internal/httpapi/      tokens.go (hard delete), server.go (secrets)
internal/security/scanner/  service.go (review approve/reject, bundle, scanners)
internal/serveredition/     api/*.go, auth/auth_endpoints.go (PR-4)
cmd/mcpproxy/          secrets_cmd.go, upstream_cmd.go (config mode), connect_credential.go
docs/                  features/audit-log.md, schemas/audit-line-v1.schema.json
specs/107-server-edition-sso-hardening/contracts/audit-line.schema.json
```

**Structure Decision**: single Go module; the line builder stays in the leaf `internal/audit`, the seam in `internal/runtime`, the sink-backed implementation in `internal/server` (the only package that holds the sink today).

## Complexity Tracking

| Violation | Why needed | Simpler alternative rejected because |
|---|---|---|
| Synchronous write outside the event bus | FR-018: the record must survive bus saturation | Consuming activity events from the bus drops them on overflow (inventory.md §0.2) |
| `ctx` variants of the tool-approval funnels | FR-008: tool approvals must name the caller, and today the funnel receives only the string `"api"`/`"mcp"` | Passing the actor as a string would duplicate the caller derivation and lose `token_prefix`/`profile_pin` |

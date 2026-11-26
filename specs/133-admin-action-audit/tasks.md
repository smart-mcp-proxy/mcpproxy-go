---
description: "Task list for Spec 133 — admin_action audit events"
---

# Tasks: `admin_action` audit events — who changed the rules

**Input**: `specs/133-admin-action-audit/` — [spec.md](spec.md), [plan.md](plan.md), [contracts/admin-action-line.md](contracts/admin-action-line.md), [inventory.md](inventory.md), [research.md](research.md)

**Tests**: required. The spec mandates a door registry test (FR-022), exact-effect door table tests (FR-023) and failing tests first (constitution V). Each slice writes its tests red before the implementation.

**Organization**: by user story (US1 tokens and credentials, US2 approvals, US3 config), sliced by the plan's PRs. A task's PR is named in brackets after the story label. Tasks marked `[X]` are implemented on the PR-1a branch.

## Format: `[ID] [P?] [Story] Description`

- **[P]**: can run in parallel (different files, no dependency on an unfinished task)
- **[Story]**: US1 / US2 / US3 from spec.md

---

## Phase 1: Setup

- [X] T001 Record the PR-1 → PR-1a / PR-1b split and its reason in specs/133-admin-action-audit/plan.md (PR slicing table) and the matching slice names in specs/133-admin-action-audit/contracts/admin-action-line.md

---

## Phase 2: Foundational — the event kind and the seam (PR-1a)

**Purpose**: everything every later line depends on. No slice can write a line before this phase.

### Tests first

- [X] T002 [P] Builder tests (contract example byte-exact, unknown action, trigger/policy iff rules, `none`/`cli_offline` never carry `request_id`, background callers carry no identity, masked credential-shaped `request_id` with `request_id_masked`, 256-character id kept whole, proxy token shapes masked, private-key label withheld, 64-rune cap after masking, literal type mismatch withheld, invalid prefix withheld, classifier secret/unknown paths, JSON Pointer escaping, 128-entry cap and 16 KiB budget, invalid session email omitted, sanitizer identity, timestamp literal) in internal/audit/admin_test.go
- [X] T003 [P] Schema tests: event-aware producer exact-key check replacing the recursive strictifier, `admin_action` positive and negative fixtures, action vocabulary equals the schema enum, legacy lines (frozen pre-133 examples and one line with colliding extension keys) still validate in internal/audit/schema_test.go and internal/audit/testdata/legacy_pre133_lines.jsonl
- [X] T004 [P] Sink boundary tests (torn JSON, lost newline, full-length errored write, refused append while the repair fails, reopen of a torn file, reopen of a clean file) in internal/audit/sink_boundary_test.go

### Implementation

- [X] T005 Widen the root enums (`event` + `admin_action`, `caller.kind` + `none`, `cli_offline`), scope the agent-token `token_name` and owned-token rules to the dispatch kinds, add the `admin_action` block (`$defs.admin_ref`, target per kind, changes shapes, forbidden keys, iff rules) and the two contract examples to docs/schemas/audit-line-v1.schema.json, and copy it byte-identical to specs/107-server-edition-sso-hardening/contracts/audit-line.schema.json
- [X] T006 [P] Sealed literal types, field paths, classifier in internal/audit/admin_fields.go
- [X] T007 `NewAdminAction`, `AgentTokenTarget`, the closed action/trigger/policy vocabularies, the label order (private-key check, mask, 64-rune cap) and the size budget in internal/audit/admin.go; `Caller.TokenNameWithheld` and `Caller.Ref` in internal/audit/attempt.go
- [X] T008 Extend `credentialPatterns` with `github_pat_…` and `mcp_(agt|cli|oat|ort)_…`, add the process-wide per-field mask-hit and build-failure counters in internal/audit/redact.go
- [X] T009 Record-boundary repair (lone newline before the next record, nothing appended while it fails, tail check on open) in internal/audit/sink.go
- [X] T010 `AdminAuditor`, `AdminRecord`, `Decider`, the late-bound holder and `Runtime.SetAdminAuditor` in internal/runtime/admin_auditor.go; the holder on `Runtime` in internal/runtime/runtime.go
- [X] T011 Sink-backed auditor (caller by the `authz` credential mapping without the `SourceInternal` shortcut, no caller → counted, never `anonymous`; source/origin/client from the ingress tag) in internal/server/admin_auditor.go; `auditCallerFromAuthContext` split out in internal/server/audit_funnel.go; install it and the two doctor warnings in internal/server/server.go
- [X] T012 Classify `client.surface` and `client.name` once at the REST ingress: `RequestMeta.Surface`/`ClientName` in internal/reqcontext/requestmeta.go, set by `TagRequestMeta` in internal/httpapi/middleware.go
- [X] T013 Attribution unit tests (socket, stdio, REST built-in dispatch keeps the REST surface, decided effect inside a request, observed edit, no caller) in internal/server/admin_auditor_test.go

### Door registry (FR-022)

- [X] T014 Registry rows for every personal REST route and method, runnable CLI command and built-in MCP tool, per branch, done/pending/exempt with slice and reason, plus the frozen pending ceiling in internal/audit/admindoors.go and internal/audit/admindoors_enum.go
- [X] T015 [P] Registry well-formedness and pending-only-shrinks tests in internal/audit/admindoors_test.go
- [X] T016 [P] chi.Walk enumeration test in internal/httpapi/admin_door_registry_test.go
- [X] T017 [P] Cobra enumeration test in cmd/mcpproxy/admin_door_registry_test.go
- [X] T018 [P] Built-in tool enumeration test (union of the routing surfaces, skills and prompts on) in internal/server/admin_door_registry_test.go

**Checkpoint**: the event kind exists, the sink is boundary-safe, every door is classified.

---

## Phase 3: User Story 1 — token lifecycle on the audit stream (Priority: P1) 🎯 MVP

**Goal**: who minted, regenerated, revoked or deleted a token, then client credentials, logins, secrets and Connect.

**Independent test**: enable `audit_log.path`, drive the contract §P1 matrix, assert exactly the named lines with caller, target, `request_id` and old/new `token_prefix`, and zero sanitizer hits.

### PR-1a — agent tokens

- [X] T019 [P] [US1] Storage tests: ref assigned at create and kept across rotation, rotation report prefixes, legacy record gets a persisted ref on first revoke, delete report in internal/storage/agent_tokens_audit_ref_test.go
- [X] T020 [P] [US1] Service tests: create/rotate/revoke/delete lines, no line for an already revoked token, a refused rotation, a second delete or a refused create; MCP create_token and revoke in internal/runtime/admin_auditor_tokens_test.go
- [X] T021 [P] [US1] Door table test against an in-process daemon: REST (`POST /tokens`, regenerate, revoke twice, regenerate revoked → 409, permanent delete twice, refused create, credential-shaped `X-Request-Id`), MCP `credentials create_token`/`revoke`/refused revoke, request-id join with the activity rows, saturated bus in internal/server/admin_action_tokens_e2e_test.go (`ServerOptions` on the test environment in internal/server/e2e_test.go)
- [X] T022 [US1] `AgentToken.AuditRef` and `NewAuditRef`/`ValidAuditRef`/`EnsureAuditRef` in internal/auth/agent_token.go and internal/auth/audit_ref.go
- [X] T023 [US1] `RegenerateAgentTokenReport`, `DeleteAgentTokenReport`, ref assignment at create and revoke in internal/storage/agent_tokens.go
- [X] T024 [US1] `token.*` records from the storage reports, `DeleteToken`, the old prefix read inside the rotating transaction in internal/runtime/credentials_service.go
- [X] T025 [US1] `DELETE /tokens/{name}/permanent` through `CredentialsService.DeleteToken` in internal/httpapi/tokens.go
- [X] T026 [US1] MCP `credentials`: reuse the context's request id, mint only when absent, install it for the mutation in internal/server/mcp_credentials_tool.go

### PR-1b — the rest of P1 and the shared plumbing

- [ ] T027 [P] [US1] Door table tests red first for `client.create|bind|key_revoke|rotate_*|forget`, the three `credentials revoke` branches, the legacy `DELETE /tokens/client-<id>` (key-only and client-wide), `token.delete` on a client record (`target.kind: client`), login-only clients in internal/server/admin_action_clients_e2e_test.go
- [ ] T028 [P] [US1] Door table tests red first for `login.register|approve|deny|issue|revoke|cancel` incl. the approve/switch-off race, the failed exchange and replay in internal/server/admin_action_logins_e2e_test.go
- [ ] T029 [P] [US1] Door table tests red first for Connect (mint, rotation stage, finalize, abort, undo, compensating restore; config-write, finalize and compensation failures) in internal/server/admin_action_connect_e2e_test.go
- [ ] T030 [P] [US1] Keyring tests red first (`secret.set` after a registry failure, late completion after timeout, absent delete writes nothing, same `ref` on set and delete, config-to-secret partial failure) in internal/secret/keyring_audit_test.go and internal/server/admin_action_secrets_e2e_test.go
- [ ] T031 [US1] Client-id origin record and client `ref` (keyed by client, covering its API-key credential), withheld for derived and legacy ids, in internal/storage/client_credentials.go and internal/runtime/clients_service.go
- [ ] T032 [US1] Client and login targets (`client`, `login`, `login_registration`, `secret`) and the name-list literal in internal/audit/admin.go and internal/audit/admin_fields.go
- [ ] T033 [US1] Client lines from the transaction's changed identities (`ClientsService.writeChange`, revoke cascade, Forget) in internal/runtime/clients_service.go and internal/runtime/credentials_service.go
- [ ] T034 [US1] Login lines at each store transition and the D-E cancellation capture under the request-store lock in internal/runtime/oauth_login_consent.go, internal/runtime/oauth_login_tokens.go and internal/mcplogin/requests.go
- [ ] T035 [US1] Connect: pass the REST context through every step, one line per persistence point in internal/runtime/clients_service_connect.go and internal/httpapi/connect.go
- [ ] T036 [US1] Keyring completion-path hook with a stored `ref` per name in internal/secret/keyring_provider.go
- [ ] T037 [US1] `CommitResult` committed delta (servers with field paths and gate-origin quarantine changes, globals, profiles, commit sequence number) and the shared emitter; `server.update` for `PATCH /servers/{id}` and config-to-secret in internal/runtime/commit.go and internal/httpapi/server.go
- [ ] T038 [US1] Sticky server-name origin record and server `ref` (legacy `unknown`, withheld) in internal/storage and internal/runtime/lifecycle.go
- [ ] T039 [US1] `login.cancel` from a browser-login switch-off on apply, `PATCH /config` and file reload in internal/runtime/runtime.go and internal/runtime/lifecycle.go
- [ ] T040 [US1] D-A: CLI and daemon ownership (config and audit-file locks, instance nonce verified on each rerouted mutation, keyring commands rerouted through `POST /secrets`, `cli_offline` sink otherwise, no config creation by the CLI loader) in cmd/mcpproxy/cli_config.go, cmd/mcpproxy/daemon_client.go, cmd/mcpproxy/secrets_cmd.go and internal/cliclient/client.go
- [ ] T041 [US1] Ingress tags for the server-edition sibling group, the public SSO callback, the public OAuth mux and stdio in internal/serveredition/setup.go and internal/server/server.go; request-id plumbing for the other MCP admin tools in internal/server/mcp.go
- [ ] T042 [US1] MCP admin-tool operation descriptors (move `TestActivityAttributedOps_MatchTheOperationSwitches` with them), the single outer-mux registrar with its enumeration, per-surface MCP enumeration and the `-tags server` router enumeration in internal/server/mcp.go, internal/server/server.go and internal/audit/admindoors.go; add the Cobra guard to the tagged CI job in .github/workflows/unit-tests.yml
- [ ] T043 [US1] Flip the PR-1b registry rows to done and lower `AdminDoorsPendingCeiling` in internal/audit/admindoors.go

**Checkpoint**: SC-002 holds for every token and credential change.

---

## Phase 4: User Story 2 — approvals on the audit stream (Priority: P2, PR-2)

**Goal**: who approved a server, a tool, a prompt or a skill, and which policy did when the proxy did.

**Independent test**: the contract §P2 scenarios (security review five-line set and every partial-failure prefix, legacy `/unquarantine`, changed-tool approve, trusted baseline, scan-clean auto-approve).

- [ ] T044 [P] [US2] Door table tests red first incl. the 3,000-tool baseline fixture (one line per server per batch key) and the mixed approve/block review in internal/server/admin_action_approvals_e2e_test.go
- [ ] T045 [US2] `ctx` variants of the tool-approval funnels (activity rows gain the actor keys) and the tool-decision collector with batch flush in internal/runtime/tool_quarantine.go and internal/runtime/tool_approval_review.go
- [ ] T046 [US2] Batch targets (`tool`, `prompt`, `skill` with `server_ref`, `count`, `names`, `truncated`, `names_withheld`) in internal/audit/admin.go
- [ ] T047 [US2] `server.quarantine|unquarantine|review_approve|baseline_*` from the commit result and the scanner approval adapters in internal/runtime/lifecycle.go and internal/security/scanner/service.go
- [ ] T048 [US2] `prompt.*`, `skill.*` incl. holds, with the rule-1a handoff from the request's commit result in internal/server/prompt_quarantine.go and internal/runtime/skills_catalog.go
- [ ] T049 [US2] D-D: `bundle.*` at each locked state transition, bundle application on sequence-ordered commit results in internal/security/scanner/tpa_refresh.go and internal/security/scanner/tpa_bundle_runtime.go
- [ ] T050 [US2] `scanner.*` incl. the pull completion and startup boundary in internal/security/scanner/service.go and internal/security/scanner/pull_manager.go
- [ ] T051 [US2] Flip the PR-2 registry rows and lower the ceiling in internal/audit/admindoors.go

---

## Phase 5: User Story 3 — config and server changes on the audit stream (Priority: P3)

**Goal**: who added, removed, enabled or edited a server, a profile, a binding or a global setting; `none` for a hand edit.

**Independent test**: contract §P3 through REST, CLI, Web UI, `upstream_servers` and `profiles`, then a hand edit of `mcp_config.json`.

### PR-3 — personal edition

- [ ] T052 [P] [US3] Door table tests red first, the config fuzz test (random diffs: nothing outside the literal list, no hash) and the whole-config apply with a gated server in internal/server/admin_action_config_e2e_test.go
- [ ] T053 [US3] D-B for every other writer: every config writer hands its `CommitResult` to the emitter; commit-delta partition into `server.*`, `registry.*`, `profile.*`, `config.update` in internal/runtime/config_funnel.go and internal/runtime/commit.go
- [ ] T054 [US3] Admission quarantine staged in the transaction and folded into `server.add` in internal/runtime/config_load_admission_gate.go
- [ ] T055 [US3] Watcher attribution (`none`, `config_file`) against the raw-file baseline under the commit lock in internal/runtime/config_watcher.go
- [ ] T056 [US3] Registry id origin (`explicit|derived`) and registry-added server-name provenance in internal/config/config.go and internal/httpapi/add_registry_source.go
- [ ] T057 [US3] `profile.*`, `token.repin`, `client.bind` for bindings and bulk assign in internal/runtime/profile_change_diff.go and internal/runtime/client_bindings.go
- [ ] T058 [US3] `config.update` for `status --reset-key`, telemetry and the startup API-key save owned by `main` in cmd/mcpproxy/main.go, cmd/mcpproxy/status_cmd.go and cmd/mcpproxy/telemetry_cmd.go
- [ ] T059 [US3] Flip the PR-3 registry rows and lower the ceiling in internal/audit/admindoors.go

### PR-4 — server edition

- [ ] T060 [P] [US3] `-tags server` door table tests red first in internal/serveredition/api/admin_action_test.go
- [ ] T061 [US3] Storage calls return changed identities (user disable's tokens, sharing) in internal/serveredition/users/store.go and internal/runtime/server_sharing.go
- [ ] T062 [US3] Tenant tokens (`owner_user_id`), `jwt.issue`, `user.*`, `server.share`, `user_server.*`, `user_credential.*`, SSO provisioning from the verified identity in internal/serveredition/api/user_handlers.go, internal/serveredition/api/admin_handlers.go, internal/serveredition/api/auth_endpoints.go and internal/serveredition/auth/oauth_handler.go
- [ ] T063 [US3] Flip the PR-4 registry rows and lower the ceiling in internal/audit/admindoors.go

---

## Phase 6: Polish — PR-5 strict

- [ ] T064 Registry test turns any remaining `pending` row into a failure in internal/audit/admindoors_test.go
- [ ] T065 [P] `CLAUDE.md` Recent Changes entry for Spec 133 in CLAUDE.md
- [ ] T066 [P] Final pass of docs/features/audit-log.md: the recorded-today table becomes the full action list

---

## Dependencies

- Phase 2 (PR-1a) blocks everything.
- PR-1b (T027–T043) depends on PR-1a. PR-2, PR-3 and PR-4 depend on PR-1b (the commit result, server `ref` and ingress tags) and can be reviewed in parallel after it.
- PR-5 depends on PR-1a..PR-4.

## Parallel opportunities

- PR-1a: T002–T004 and T015–T018 touch separate test files.
- PR-1b: T027–T030 (tests in separate files); T031/T036 in different packages.
- After PR-1b: PR-2, PR-3 and PR-4 in separate sessions.

## Implementation strategy

MVP is PR-1a: the event kind plus the reporter's first priority (agent tokens). Each later slice lands its tests red, flips its registry rows to done and lowers the pending ceiling, so the registry shows how far the feature is at any commit.

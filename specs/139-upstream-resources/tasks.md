# Tasks: Upstream Resources

**Input**: Design documents from `/specs/139-upstream-resources/`
**Prerequisites**: plan.md, spec.md, research.md, data-model.md, contracts/

**Tests**: Required (constitution V, TDD). Every implementation task is preceded by a failing test.

**Organization**: PR-1 (US1–US4) is detailed; PR-2 (US5 list part, US6) and PR-3 (US5 subscriptions) are phase-level and are broken down when they start.

## Format: `[ID] [P?] [Story] Description`

---

## Phase 1: Setup (PR-1)

- [ ] T001 Add `AggregateUpstreamResources bool` (`aggregate_upstream_resources`, default false) to `Config` and `ExposeResources *bool` (`expose_resources`) to `ServerConfig` in `internal/config/config.go`, with tests for JSON round trip and default in `internal/config/resources_config_test.go`
- [ ] T002 Add `expose_resources` tri-state patch handling (diff entry, nil = unchanged) in `internal/config/merge.go`, test in `internal/config/resources_config_test.go`
- [ ] T003 [P] Add `storage.ActivityTypeResourceRead = "resource_read"` in `internal/storage/activity_models.go`; test that `AttemptRoleOf` gives the zero role for it in `internal/storage/attempt_role_resource_test.go`

## Phase 2: Foundational (PR-1, blocks every story)

- [ ] T004 [P] Write table and fuzz tests for the address codec (round trip, canonical-only decode, every refusal case in contracts/resource-address.md, template composition) in `internal/resourceaddr/resourceaddr_test.go`
- [ ] T005 Implement `Encode`, `Decode`, `PrefixTemplate`, `IsSkillAddress`, `IsSkillTemplate` in `internal/resourceaddr/resourceaddr.go` (stdlib only)
- [ ] T006 [P] Write tests for `core.Client.ListResources` / `ListResourceTemplates` (no capability → nil,nil; `expose_resources:false` → nil,nil; pagination followed; page cap 50; item caps 500/100) and `ReadResource` (result returned; over-cap response → `*ResourceTooLargeError`) against an in-process mcp-go fixture in `internal/upstream/core/resources_test.go`
- [ ] T007 Implement `exposeResources` atomic + `SetExposeResources` in `internal/upstream/core/client.go` and the three methods in `internal/upstream/core/resources.go` (paged with `ListResourcesByPage`/`ListResourceTemplatesByPage`, SSE serialisation, `beginRequest`, `transport.WithResponseLimit(ctx, MaxResourceResponseBytes)` + post-read encoded-size check)
- [ ] T008 Pass-through `ListResources`, `ListResourceTemplates`, `ReadResource` with connection checks in `internal/upstream/managed/resources.go`; push `ExposeResources` to the core client wherever `SetExposePrompts` is pushed
- [ ] T009 [P] Write manager tests (disabled/quarantined/disconnected servers skipped; per-server and total caps logged; `ReadResource` refuses disabled/quarantined and uses reconnect-on-use) in `internal/upstream/manager_resources_test.go`
- [ ] T010 Implement `Manager.ListResources`, `Manager.ListResourceTemplates` (returning `[]ServerResource` / `[]ServerResourceTemplate`) and `Manager.ReadResource(ctx, server, uri)` in `internal/upstream/manager_resources.go`

**Checkpoint**: upstream layer lists and reads resources with every cap; nothing client-facing yet.

## Phase 3: User Story 1 - List and read from my client (P1) 🎯 MVP

**Goal**: `/mcp` (every routing mode) lists namespaced resources and templates and reads them.
**Independent Test**: two fixture servers with an identical URI → four distinct addresses, two templates, each read returns its own server's content; feature off → no capability, `resources/*` refused as today.

- [ ] T011 [P] [US1] Write end-to-end tests over the MCP client (in-process `MCPProxyServer` with two fixture upstreams): list, templates list, read of listed and template-produced addresses, identical URI on two servers, content-item URI rewrite (FR-009), feature off golden handshake (SC-002), runtime on→off clears lists in `internal/server/mcp_resources_test.go`
- [ ] T012 [US1] Implement `RefreshResources` (live config, snapshot publish, `SetResources`/`SetResourceTemplates` on all four routing-mode servers, no registration when off and never on) and the read handlers for resources and templates in `internal/server/mcp_resources.go`
- [ ] T013 [US1] Add `WithResourceCapabilities(false, false)` when the feature is on at construction in `internal/server/mcp.go` and `internal/server/mcp_routing.go` (routing-mode server options)
- [ ] T014 [US1] Call `RefreshResources` next to each `RefreshPrompts` call in `internal/server/server.go` (servers changed, config reload) and on start

## Phase 4: User Story 2 - Quarantine, reach and profiles (P1)

**Goal**: callers see and read exactly the servers they may reach.
**Independent Test**: admin / scoped token / pinned token / profile-bound client × healthy / quarantined / disabled / out-of-scope server matrix; refusals byte-identical to an unknown address and nothing sent upstream (counting fixture).

- [ ] T015 [P] [US2] Write the reach matrix tests, the "refusal equals unknown address" byte comparison for both protocol eras, the read-time re-check after a quarantine flip, and a test that no routing-mode server sets a pagination limit in `internal/server/mcp_resources_scope_test.go`
- [ ] T016 [US2] Implement after-list hooks for resources and templates (owner from snapshot, `promptServerAllowed` + `resolveActiveProfileForList`, fail closed on missing owner) and `authorizeResourceServer` + `resourceNotFound(ctx, uri)` in `internal/server/mcp_resources_scope.go`; register the hooks once on `p.hooks` in `internal/server/mcp.go`

## Phase 5: User Story 3 - Content checks and activity (P1)

**Goal**: output policy, size cap, metadata scan and one `resource_read` row per read.
**Independent Test**: fake AWS key + injection fixture under redact and block; oversize fixture; poisoned description fixture; row fields per contracts/activity-resource-read.md.

- [ ] T017 [P] [US3] Write output-policy tests (redact, strip, spotlight on text; block on critical text and on textual blob; non-textual blob unchanged; policy-decision companion row with same request id) in `internal/server/mcp_resources_output_test.go`
- [ ] T018 [US3] Implement `applyResourceResultSanitisation` in `internal/server/mcp_resources_output.go` reusing `p.sanitisationDetector`, `evaluateOutputSanitisation` and the prompt strip/spotlight helpers
- [ ] T019 [P] [US3] Write activity tests (one row per handled read: success, upstream error, too large, blocked, refused-by-gate; fields; scoped SSE visibility) in `internal/server/mcp_resources_activity_test.go` and `internal/httpapi/activity_resource_read_test.go`
- [ ] T020 [US3] Implement `Runtime.EmitActivityResourceReadAttributed` in `internal/runtime/event_bus_resources.go` and the emit in the read handler; accept the type in `internal/httpapi/activity.go` filters and `internal/httpapi/sse_scope.go` (as `prompt_get`)
- [ ] T021 [US3] Implement the metadata scan (name + title + description through `scanner.ScanToolMetadataVerdict`, drop "dangerous", keep withheld set, template reads of withheld URIs refused) in `internal/server/mcp_resources.go`, tests in `internal/server/mcp_resources_test.go`
- [ ] T022 [P] [US3] Add the type to CLI help in `cmd/mcpproxy/activity_cmd.go`, Web UI label/filter in `frontend/src/utils/activity.ts` and `frontend/src/types/api.ts`, OpenAPI enum in `oas/swagger.yaml` (`make swagger`)

## Phase 6: User Story 4 - Skills stay behind their gate (P1)

**Goal**: no skill file reachable through the plain resource path.
**Independent Test**: fixture with `skill://` resource, a skill-named `file://` resource and a `file:///{+path}` template, skills feature on and off.

- [ ] T023 [P] [US4] Write exclusion tests (list and read, template expansion onto a skill file, skills on/off, held and unusable skills) in `internal/server/mcp_resources_skills_test.go`
- [ ] T024 [US4] Add `Runtime` (skills catalog) method returning every skill and file URI a server's catalog holds, whatever the state, in `internal/runtime/skills_addresses.go` with a test; use it plus `resourceaddr.IsSkillAddress/IsSkillTemplate` at list and read time in `internal/server/mcp_resources.go`

## Phase 7: Polish (PR-1)

- [ ] T025 [P] Wire `expose_resources` into the `upstream_servers` tool schema/handlers (`internal/server/mcp.go`), the REST server payload and `contracts` types, with tests (config "5 wiring points")
- [ ] T026 [P] Write `docs/features/upstream-resources.md`, add both settings to `docs/configuration.md`, add a "Recent Changes" line to `CLAUDE.md` by hand
- [ ] T027 Run both golangci-lint v2 commands, `go test -race` for `internal/resourceaddr`, `internal/upstream/...`, `internal/config/...`, `internal/storage/...`, `internal/server/...` (skip regex), `internal/httpapi/...`, and `./scripts/test-api-e2e.sh`

## Phase 8: PR-2 — list changes and single-server endpoint (US5 list part, US6)

- [ ] T028 [US5] Handle `notifications/resources/list_changed` in `internal/upstream/core/connection_lifecycle.go` → callback chain core → managed → manager → server, debounced per server; rebuild with fingerprint, notify only on change; advertise `listChanged:true`
- [ ] T029 [US6] Register a server's resources with original URIs on `/mcp/s/<server>` in `internal/server/mcp_single_server.go` (new file for the resource part), merged capability with `nativeSkillsServerOptions`, native interceptor unchanged; tests for coexistence with Spec 125

## Phase 9: PR-3 — subscriptions (US5)

- [ ] T030 [US5] Core `Subscribe`/`Unsubscribe`, `notifications/resources/updated` dispatch in `internal/upstream/core/`
- [ ] T031 [US5] Session subscription registry implementing `SessionWithResourceSubscriptionsErr`, refcounted upstream subscriptions, reach re-check on delivery, reconnect re-subscribe, release on session unregister, both protocol eras; `subscribe:true`; `closes #1969` on this PR

---

## Dependencies & Execution Order

- Phase 1 → Phase 2 → Phase 3 (MVP) → Phases 4, 5, 6 (independent of each other, all need Phase 3's refresh/handler skeleton) → Phase 7. PR-1 ships Phases 1–7 together, since US2–US4 are the safety half of US1.
- PR-2 (Phase 8) needs PR-1 merged; PR-3 (Phase 9) needs PR-2.

## Parallel Opportunities

- T003, T004, T006, T009 (different packages) in parallel.
- T015, T017, T019, T023 test writing in parallel once T012 exists.
- T022, T025, T026 in parallel.

## Implementation Strategy

MVP = Phases 1–3 locally, but nothing ships until Phases 4–6 are in: the feature is off by default and PR-1 is reviewed as one safety unit.

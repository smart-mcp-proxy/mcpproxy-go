---
description: "Task list for Spec 057 — In-Proxy Profiles + Permanent URLs"
---

# Tasks: In-Proxy Profiles + Permanent URLs

**Input**: Design documents from `/specs/057-in-proxy-profiles/`
**Prerequisites**: plan.md, spec.md, research.md, data-model.md, contracts/
**Branch**: `057-in-proxy-profiles` | **Issue**: GH #55

**Tests**: INCLUDED — Constitution V (TDD) is binding and the spec's *Testing Strategy* enumerates required unit/integration/E2E tests. Write each test red-first.

**Commit conventions** (spec §Commit Message Conventions): `feat(057): …` / `test(057): …` / `docs(057): …`; footer `Related #55`; **no** `Co-Authored-By: Claude` / "Generated with Claude Code".

## Format: `[ID] [P?] [Story] Description`

- **[P]**: parallelizable (different files, no incomplete-task dependency)
- **[US#]**: user story this task serves (story phases only)

---

## Phase 1: Setup

- [ ] T001 Create new file skeletons with package decls and build-passing stubs: `internal/config/profiles.go` (package `config`) and `internal/profile/context.go` (package `profile`). Confirm `go build ./...` still green.

---

## Phase 2: Foundational (blocking — all stories depend on these)

**Config entity + validation**

- [ ] T002 [P] Write red unit tests in `internal/config/profiles_test.go` for slug validation (`^[a-z0-9][a-z0-9_-]{0,62}$`), reserved-slug rejection (`all`,`code`,`call`,`p`), duplicate-name rejection (FR-014), unknown-server warn-and-skip (FR-015), and empty-`servers` warning. Table-driven; assert diagnostics point at the offending entry (SC-005).
- [ ] T003 Define `ProfileConfig{Name string; Servers []string}` in `internal/config/profiles.go` with a `ValidateProfiles(cfg *Config) (warnings []string, err error)` helper implementing the rules from data-model.md. Make T002 pass.
- [ ] T004 Add `Profiles []ProfileConfig \`json:"profiles,omitempty"\`` to the `Config` struct in `internal/config/config.go` (immediately after `Servers`, ~L109). Wire `ValidateProfiles` into `Config.Validate()` in `internal/config/loader.go` (~L1521): surface warnings via the existing logger, return error on fatal rules.
- [ ] T005 [P] Add round-trip test in `internal/config/profiles_test.go`: a config with `profiles` absent marshals byte-identically through `SaveConfig`/`json.MarshalIndent` (SC-004), and a config with profiles round-trips losslessly.

**Request-scoped profile scope**

- [ ] T006 [P] Write red unit tests in `internal/profile/context_test.go`: `ProfileScope.Allows` membership (and nil-receiver ⇒ allow-all), and `WithProfileScope`/`ProfileScopeFromContext` round-trip; `ProfileScopeFromContext` returns nil on a bare context.
- [ ] T007 Implement `internal/profile/context.go` (~30 LOC, mirror `internal/auth/context.go`): `ProfileScope{Name string; servers map[string]struct{}}`, `Allows(string) bool` (nil receiver ⇒ true), `NewProfileScope(name string, servers []string)`, `WithProfileScope`/`ProfileScopeFromContext` using an unexported context key. Make T006 pass.

**Checkpoint**: config accepts/validates profiles; `ProfileScope` resolvable from context. No behaviour change yet.

---

## Phase 3: User Story 1 — Two clients, two profiles, one proxy (P1) 🎯 MVP

**Goal**: `/mcp/p/<slug>` exposes only the profile's servers; `/mcp` unchanged; bad slugs 404.
**Independent test**: connect to `/mcp/p/research` and `/mcp/p/deploy`; each `retrieve_tools` returns only its servers' tools; cross-profile `call_tool_*` rejected; `/mcp` still full union; `/mcp/p/<unknown>` and no-profiles cases 404.

- [ ] T008 [US1] Add `profileMiddleware(next http.Handler) http.Handler` in `internal/server/server.go`: match the `/mcp/p/` prefix, strip the slug, look up the profile in `s.runtime.Config().Profiles` (lock-free snapshot ⇒ hot-reload), build a `ProfileScope` from its effective servers, inject via `profile.WithProfileScope`, delegate to the existing retrieve_tools-mode handler. 404 JSON `{"error":"no profiles configured"}` when none configured (FR-008); 404 JSON `{"error":"unknown profile '<slug>'","available":[…]}` on miss (FR-009).
- [ ] T009 [US1] Register the `/mcp/p/` route near the existing mode routes (`internal/server/server.go` ~L1690), reusing `p.server` (retrieve_tools mode) wrapped as `mcpAuthMiddleware(profileMiddleware(loggingHandler(streamable)))` — **auth before profile** so token scope composes downstream. Handle both `/mcp/p/` and `/mcp/p` forms.
- [ ] T010 [US1] Add the parallel profile filter at the retrieve_tools site in `internal/server/mcp.go` (~L1113): before/independent of the `enforceAgentScope` gate, read `profile.ProfileScopeFromContext(ctx)`; if non-nil and `!scope.Allows(serverName)`, skip the result. MUST NOT depend on `enforceAgentScope`.
- [ ] T011 [US1] Add the parallel profile filter at the call_tool_* site in `internal/server/mcp.go` `handleCallToolVariant` (~L1529): if `profileScope != nil && !profileScope.Allows(serverName)`, reject with `"server '<s>' is not in profile '<name>'"` and emit a policy-decision activity record (mirror the existing token-scope rejection just below).
- [ ] T011a [US1] **(Codex #621 finding 1 — FR-004)** Add the parallel profile filter to the `upstream_servers` introspection path in `internal/server/mcp.go` (list path ~L2763, which today applies only the agent-token filter ~L2769/L2777 before returning server names/config ~L2800/L2810): exclude servers not in `ProfileScope` from the listing/get result so a profile URL cannot enumerate out-of-profile servers. Add an integration test asserting `upstream_servers(list)` at `/mcp/p/<slug>` returns only the profile's servers.
- [ ] T011b [US1] **(Codex #621 finding 2 — profile-boundary escape)** Intersect `ProfileScope` into `code_execution`. The `/mcp/p/` route reuses the retrieve-tools server, which registers `code_execution` when `enable_code_execution` is true (`mcp.go:588`/`611`). `handleCodeExecution` (`internal/server/mcp_code_execution.go:181`/`190`) passes only auth scope into the JS runtime, and the runtime treats an empty `allowed_servers` as ALL servers (`internal/jsruntime/runtime.go:18`/`141`/`303`), with the nested caller invoking the named upstream directly (`mcp_code_execution.go:410`/`421`). Pass the profile-intersected effective server set into the runtime (or reject `call_tool()` for servers outside the active profile). Add a test: `call_tool()` inside `code_execution` at `/mcp/p/<slug>` is rejected for an out-of-profile server (and allowed for an in-profile one).
- [ ] T012 [US1] **Mandatory regression test** (spec §Implementation Design): integration test in `internal/server/` asserting an **unauthenticated** connection at `/mcp/p/<slug>` is still filtered to the profile's servers (proves profile filtering does not ride `enforceAgentScope`/AdminContext).
- [ ] T013 [P] [US1] Integration tests in `internal/server/` (httptest server, two profiles): `retrieve_tools` at `/mcp/p/research` returns only research tools; `/mcp` returns the full union (SC-002 spot-check); `/mcp/p/unknown` → 404 with available list; `/mcp/p/all` (reserved, even if a profile tried to define it) never routes; no-profiles config → `/mcp/p/x` 404.
- [ ] T014 [P] [US1] E2E test (`internal/server/e2e_test.go` style): real proxy + two stub upstreams + two profiles + two MCP clients; verify bidirectional isolation via `retrieve_tools` and `call_tool_*` (SC-001).

**Checkpoint**: US1 independently shippable — the core #55 ask works.

---

## Phase 4: User Story 2 — Profile composes with agent token scope (P1)

**Goal**: effective scope = profile ∩ token; errors name the responsible primitive.
**Independent test**: token `{github,fs,web}` at `/mcp/p/deploy`(`{github,k8s}`) ⇒ only `github`; `fs`→profile error, `k8s`→token error; wildcard token fully constrained by profile.

- [ ] T015 [US2] Verify (and adjust if needed) that the US1 parallel checks already yield the intersection: with both a non-nil `ProfileScope` and an agent token present at the call_tool_* site, the profile check and the existing token check run **independently**, so a server must pass both. Ensure ordering produces the correct distinct message (profile check names the profile; token check names the token — FR-012).
- [ ] T016 [P] [US2] Table-driven policy unit test in `internal/server/` (or a small extracted helper) covering every cell of {in-profile?}×{in-token?}×{wildcard token} from data-model.md §3, asserting allow/deny **and** the exact error string identifying the blocking primitive (SC-003). Include the wildcard-token-fully-constrained-by-profile case (US2 acceptance #4).
- [ ] T017 [P] [US2] Extend the retrieve_tools integration test (T013) with a token-scoped client at a profile URL, asserting the listed tools are the intersection only.

**Checkpoint**: two scoping primitives compose predictably with attributable errors.

---

## Phase 5: User Story 3 — Per-tool curation reuses existing controls (P2)

**Goal**: per-server `enabled_tools`/`disabled_tools` keep applying inside a profile; no new profile-level tool field.
**Independent test**: server `github` with `disabled_tools:["delete_repo"]` referenced by a profile ⇒ `delete_repo` absent at the profile URL and rejected on direct call.

- [ ] T018 [P] [US3] Integration test in `internal/server/`: a profile referencing a server that has `disabled_tools:["X"]` — `retrieve_tools` at the profile URL omits `X`; direct `call_tool_*` of `X` is rejected by the existing per-server denylist (not a profile-specific path). Also assert an `enabled_tools` allowlist server shows only its allowlisted tool at the profile URL (US3 acceptance #1–3). No production code change expected (FR-006 works downstream of the server gate) — this phase is a guard test confirming it.

**Checkpoint**: tool-level granularity confirmed without a second mechanism.

---

## Phase 6: Polish & Cross-Cutting

- [ ] T019 [P] Activity metadata (FR-011): at the tool-call activity emit sites in `internal/server/mcp.go` (`emitActivityToolCallCompleted`/policy-decision paths), when `ProfileScopeFromContext(ctx)` is non-nil, set `metadata["profile"] = scope.Name` (via the existing `Metadata map[string]interface{}` / `UpdateActivityMetadata`). Records from `/mcp` MUST omit the field. Add a test asserting presence/absence.
- [ ] T020 [P] Backward-compat E2E (SC-002): run the existing `internal/server` E2E suite with a `profiles`-absent config and confirm `/mcp`, `/mcp/code`, `/mcp/call` behave unchanged.
- [ ] T021 [P] Hot-reload test: change `profiles` at runtime via the config service; assert a new connection sees the new profile while an in-flight session keeps its snapshot (data-model §State transitions).
- [ ] T022 [P] Docs (Constitution VI): update `CLAUDE.md` (MCP endpoints / routing note: add `/mcp/p/<slug>`), `README.md` (profiles section from quickstart.md), and add `docs/features/profiles.md`. Note: confirm `wc -c CLAUDE.md` stays < 40,000 (memory `project_claudemd_40k_gate`) — keep the CLAUDE.md delta to one line.
- [ ] T023 Final gate: `./scripts/run-linter.sh`, `go test ./internal/... -race`, `./scripts/test-api-e2e.sh`; confirm the named unauth-profile regression test (T012) passes. Verify dual-edition build: `go build ./cmd/mcpproxy` and `go build -tags server ./cmd/mcpproxy` (FR-013, no build-tag divergence).

---

## Dependencies & Execution Order

- **Phase 1 (T001)** → **Phase 2 (T002–T007)** → user stories.
- **US1 (T008–T014 + T011a/T011b)** is the MVP and must land first — it builds the routing + filter seam across ALL profile-boundary surfaces (retrieve_tools, call_tool_*, upstream_servers, code_execution). T011a/T011b are correctness-critical (Codex #621 findings) — a profile that filters retrieve_tools but leaks via upstream_servers or code_execution is incomplete.
- **US2 (T015–T017)** depends on US1's call_tool_* filter site (T011) and retrieve_tools test (T013).
- **US3 (T018)** depends on US1 routing (T008–T009); otherwise independent.
- **Phase 6** depends on US1; T019/T020/T021/T022 are mutually parallel.

### Parallel opportunities

- Foundational: T002‖T005‖T006 (tests), then T003/T004 and T007.
- US1: T013‖T014 after T008–T012.
- US2: T016‖T017. Polish: T019‖T020‖T021‖T022.

## Implementation Strategy

- **MVP = Phase 1+2+US1** (T001–T014): ships the core #55 capability, independently testable.
- **Increment 2 = US2** (token composition) — small, mostly tests since intersection falls out of the US1 design.
- **Increment 3 = US3 + Polish** (tool-curation guard test, activity metadata, docs, gates).

## Hand-off note (Paperclip)

These 23 tasks are sized for delegation to Paperclip engineers. Suggested split: one engineer takes Foundational+US1 (T001–T014, the keystone), a second picks up US2+US3+Polish (T015–T023) blocked on US1. All work is in `internal/{config,profile,server}` — **no `internal/teams` overlap** with the in-flight Server-edition rename / spec-074 OAuth cluster.

---
description: "Task list for Skills over MCP (spec 125)"
---

# Tasks: Skills over MCP

**Input**: Design documents from `/specs/125-skills-over-mcp/`
**Prerequisites**: plan.md, spec.md, research.md, data-model.md, contracts/

**Tests**: required. The constitution (V) and the project rules ask for a failing test before every implementation task; each implementation task below names the test task it turns green.

**Organization**: by pull request (plan.md, PR table), and inside each by user story. `enable_skills` stays default-off throughout. Nothing agent-facing (PR-3 onward) merges before PR-2.

## Format: `[ID] [P?] [Story] Description`

- **[P]**: can run in parallel (different files, no dependency on an unfinished task)
- **[Story]**: US1..US5 from spec.md; no label = shared groundwork

---

## PR-1: Read skills in, show them to an administrator (US1, part of US4)

### Phase 1: Leaf package (blocks everything)

- [x] T001 [P] Table tests for URI and path rules in `internal/skills/uri_test.go`: valid nested path, name ≠ last segment, other scheme or host, query, fragment, encoded separator, `..`, absolute, empty segment, control character, duplicate relative path (plan D1)
- [x] T002 [P] Tests for entry decoding in `internal/skills/entry_test.go`: array resources, `"dynamic"`, other types, unknown fields ignored, missing `SKILL.md` reference, over 512 files, sizes over 16 MiB
- [x] T003 [P] Tests for `VerifyFile`, `ParseFrontmatter`, `FrontmatterEqual` in `internal/skills/verify_test.go`: size mismatch, digest mismatch, non-sha256 digest, no frontmatter, frontmatter over 64 KiB, YAML vs JSON number and string normalisation
- [x] T004 [P] Tests for `ContentHash` in `internal/skills/hash_test.go`: stable under file order, changes when a file URI, relative path, digest, size or any frontmatter value changes, no collision between `("ab","c")` and `("a","bc")` (length prefixes), golden value pinned for `sk1-`
- [x] T005 Implement `internal/skills/{types.go,uri.go,verify.go,hash.go,limits.go}` until T001-T004 pass; imports limited to stdlib, `gopkg.in/yaml.v3`, `mcp-go/mcp` (guarded by a `go list` import test in `internal/skills/imports_test.go`)

### Phase 2: Fixture upstream

- [x] T006 Build `internal/skills/skillstest/fixture.go`: raw JSON-RPC `httptest` server answering `initialize` (with or without the extension), `tools/list`, `skills/list` (optional pagination), `skills/get`, `resources/read`; knobs for wrong digest, wrong size, frontmatter mismatch, dynamic skill, over-limit skill, binary file, oversized response, mutation between calls, failing listing; request log for "nothing was sent" assertions (research R9)
- [x] T007 Self-test of the fixture in `internal/skills/skillstest/fixture_test.go` (digest of served bytes equals the listing by default)

### Phase 3: Upstream layers (US1)

- [x] T008 [P] [US1] Tests in `internal/upstream/core/skills_test.go` against the fixture: `SupportsSkills` true only with extension **and** resources; `ListSkills` sends nothing without support or with `expose_skills` false; pagination; page and entry caps return `*skills.TruncatedError` with the partial list; string request ids; text and blob file reads; mismatched content URI refused; invalid UTF-8 text treated as binary
- [x] T009 [P] Tests for the response limit in `internal/transport/response_limit_test.go`: a request with `WithResponseLimit(ctx, n)` fails after `n` bytes, a request without it is untouched, limit `6×size+64KiB` accepts a fully escaped body
- [x] T010 [US1] Implement `internal/transport/response_limit.go` and add the link to the round-tripper chain (plan D2)
- [x] T011 [US1] Implement `internal/upstream/core/skills.go` (`SupportsSkills`, `ListSkills`, `GetSkill`, `ReadSkillFile`, `exposeSkills`) following `core/prompts.go`
- [x] T012 [US1] Tests then implementation of `internal/upstream/managed/skills.go` (`ListSkillsCaptured`, `ReadSkillFile` through `withSession`; capture stamped before the request) and `internal/upstream/manager_skills.go` (per-server access; refuses disabled and quarantined; never reconnects on use)

### Phase 4: Storage (US1)

- [x] T013 [P] [US1] Tests in `internal/storage/skills_test.go`: record CRUD, blob put/get by `server:digest`, list by server, key safety for a server name or URI containing `:`; extend `internal/storage/purgetest` (label, `Seed`, `Counts`) and `purge_test.go` so removing a server deletes both buckets' rows and leaves another server's
- [x] T014 [US1] Implement `SkillApprovalRecord`, `skill_approvals`, `skill_files` in `internal/storage/{models.go,bbolt.go,manager.go}`; add both to `initBuckets`, `purgeServerTx`, `Manager.PurgeSetCounts` (plan D4)

### Phase 5: Config (US4, the part PR-1 needs)

- [x] T015 [P] [US4] Tests: defaults (`enable_skills` false, `expose_skills` nil), merge and copy of `expose_skills`, hot-reload detection of both, contracts round trip, `upstream_servers` tool parsing
- [x] T016 [US4] Wire `enable_skills` and `expose_skills` at every point listed in plan D5; `make swagger`; regenerate `frontend/src/types/contracts.ts`; update the MCP tools-list goldens; `docs/configuration.md`

### Phase 6: Runtime catalog (US1)

- [x] T017 [US1] Tests in `internal/runtime/skills_catalog_test.go` with the fixture: feature off sends nothing (SC-005); 3 skills read in as `pending`; each unusable reason code from data-model.md; one bad skill does not stop the others; stored blob reused only after re-verification, damaged stored blob refetched; budgets (skill, refresh, server) with reservation released for binary; leftover staged blobs collected at the start of the next refresh; empty listing keeps records as `listed: false`; stale or retired capture writes no record; a failing listing leaves tools usable and sets `skills_error` (SC-007); server removal leaves nothing
- [x] T018 [US1] Implement `internal/runtime/skills_catalog.go` (plan D6 steps 1-6, `SkillAvailable`, single-flight per server, timeouts, `skills.changed` event) and hook the triggers: connect, sweep, config change, explicit refresh
- [x] T019 [US1] Race test: refresh concurrent with server removal and with a second refresh request (`-race`, `GOMAXPROCS=1 -count=20`)

### Phase 7: REST and CLI (US1)

- [x] T020 [P] [US1] Handler tests in `internal/httpapi/skills_test.go`: list, content with offset and `next_offset`, binary 415, unknown 404, refresh, feature off, and a scoped agent token gets 403/404 on all three (FR-019a)
- [x] T021 [US1] Implement `internal/httpapi/skills.go` + routes + swagger annotations; `make swagger`
- [x] T022 [P] [US1] CLI tests then `cmd/mcpproxy/skills_cmd.go`: `skills list [--server]`, `skills show <server>:<skill> [--file]`, `skills refresh <server>`, `-o json|yaml`, `--help-json`
- [x] T023 [US1] `docs/features/skills.md` (what is read, what is verified, limits, that nothing reaches agents yet), sidebar entry, REST and CLI reference sections, `CLAUDE.md` Recent Changes line by hand
- [x] T024 [US1] Manual check from quickstart.md against the public server with a scratch data dir and high port; record the count and one digest in the PR description

**Checkpoint PR-1**: an administrator can list and read every skill of a skills server; agents see nothing; feature off is byte-identical on the wire.

---

## PR-2: The gate (US2)

- [x] T025 [P] [US2] Table test for `decideSkill` in `internal/runtime/skills_quarantine_test.go`: every cell of the FR-013 table, blocked survives every transition, dynamic always unusable, content returning to the approved hash restores approved, illegal promotions refused
- [x] T026 [P] [US2] Tests for scan chunking in `internal/runtime/skills_scan_test.go`: file under 48 KiB is one chunk, newline split, no-newline hard split on a UTF-8 boundary, overlap ≤ 1 KiB and always advancing, `MaxScanBytesPerSkill` gives "incomplete", `coverageOK` false gives "incomplete", findings carry file and offset
- [x] T027 [US2] Implement `internal/runtime/skills_quarantine.go` (`decideSkill`, scan, apply inside the catalog's step 4)
- [x] T028 [P] [US2] Tests for `ApproveSkillsReviewedAt`, `BlockSkill`, `UnblockSkill`: stale review writes nothing (409 `review_out_of_date`), replaced server (409 `server_replaced`), unusable refused, approved copy retained when current changes later
- [x] T029 [US2] Implement them with the approval lock and incarnation check (plan D10); activity type `skill_quarantine_change` with a `…Ctx` emitter (constants, `ValidActivityTypes`, label parity test)
- [x] T030 [US2] REST `approve`, `block`, `unblock` (administrator only) + CLI `skills approve|block|unblock`, `skills show --diff`
- [ ] T031 [US2] Held and changed skills in the review queue and attention list as administrator-only entries; `skillVisibleTo` helper; test that walks every REST and SSE read door with a scoped credential and a held skill and finds it nowhere (FR-019a) — **partly done in PR-2**: administrator-only activity rows and SSE events for skills, and the walk over every REST read door with a scoped credential and a held skill (`internal/httpapi/skills_held_walk_test.go`). The review-queue and attention-list entries and `skillVisibleTo` move to their own PR (they change the Web and macOS parity contract).
- [x] T032 [US2] Fixture scenario test for the story's independent test: approve, change one byte upstream, refresh → held with diff, approve → new content
- [x] T033 [US2] Scan the public server's 25 skills manually and record verdicts in the PR description (plan Risks: false positives); docs section "Reviewing skills" in `docs/features/skills.md` and `docs/features/security-quarantine.md`

**Checkpoint PR-2**: skills can be approved, held on change and blocked; still nothing agent-facing.

---

## PR-3: Agent tools (US3, rest of US4)

- [x] T034 [P] [US3] Tests for the search index in `internal/skills/index/index_test.go`: ranking by name and description, rebuild, only available skills, 1,000 entries under 100 ms
- [x] T035 [US3] Implement `internal/skills/index` (memory-only Bleve)
- [x] T036 [P] [US3] Tool tests in `internal/server/mcp_skills_test.go`: search result shape and `total`, load with provenance header and manifest, offsets and `next_offset` over a multi-part file, file read, binary refused, path escape refused, and the single `skill not found` answer for pending, changed, blocked, unusable, not listed, excluded server, disabled server, quarantined server, out-of-scope server and unknown id (byte-identical answers)
- [x] T037 [US3] Implement `internal/server/mcp_skills.go`; content only from the approved copy, re-hashed before return; sanitisation and sensitive-data detection; activity type `skill_access`
- [x] T038 [US3] Scope tests with an agent token, a profile, a pinned profile and tool-deny rules (FR-023, FR-023a); upstream changed after approval → agent still gets the approved text (FR-008b)
- [x] T039 [US4] `RefreshSkillsAvailability`: per-surface tests (default registry, call-tool, code-execution, direct, single-server) for off → on → off; a built-in name can never resolve to an upstream tool; implement and call it from the reload listener (plan D5)
- [x] T040 [US3] Scoped REST answers (available skills only, approved copy only) replacing the PR-1 administrator-only gate on list and content
- [x] T041 [US3] E2E in `internal/server/e2e_test.go` style: real client session, search → load → read; `./scripts/test-api-e2e.sh`; token count of a 25-skill search answer under 2,000 (SC-003)
- [x] T042 [US3] Docs: agent usage, the three tools, profiles and tokens; `docs/features/agent-instructions.md` snippet

**Checkpoint PR-3**: an agent in any client finds and loads approved skills.

---

## PR-4: Web UI (US1, US2 on the Web)

- [x] T043 [P] Vitest specs under `frontend/tests/unit/` for the Skills tab: list with state chips, file list, text viewer with paging, diff, findings, approve bound to the shown hash, block, 409 handling
- [x] T044 Implement the Skills tab in `frontend/src/views/ServerDetail.vue` and its components; API client methods; `skills.changed` SSE handling
- [ ] T045 Review queue and Home attention rows for skills; terminology values in `internal/contracts/terminology.go` and fixtures — **PR-4b**: PR-4 was split; PR-4a (T043, T044, T046: the Skills tab, `cmd/skillsfixture`, `e2e/web-ui-sweep/skills-tab.spec.ts`) is done, this task and the rest of T031 (review-queue and attention entries, `skillVisibleTo`, macOS activity labels) are the next PR
- [x] T046 Playwright sweep and HTML report per `docs/development/web-ui-verification.md`; `docs/web-ui/` page

---

## PR-5: Native surface on the single-server endpoint (US5)

- [ ] T047 [P] [US5] Tests with a raw client against `/mcp/s/<server>`: extension announced only when the feature is on and the server not excluded; listing and single entry contain exactly the available skills with upstream addresses; file read byte-exact against the listed digest; held skill absent and not found; aggregated endpoints never announce; activity rows for list, get and read including not-found
- [ ] T048 [P] [US5] Parity tests: each extension method with a missing session id, a foreign one and an unsupported protocol version gets the refusal `tools/call` gets
- [ ] T049 [US5] Extract the transport's method, protocol-version and session checks for reuse; implement the interceptor inside `serveSingleServer` and the resource template in `internal/server/skills_native.go` (plan D14)
- [ ] T050 [US5] Endpoint lifecycle: per-endpoint lock, combined acquisition of instance, transport and generation, retirement, generation in session ids, upstream name bound to the instance; tests for off → on → off (old session refused, old stream closed, transports do not accumulate), the acquisition-versus-retirement race and the paused `initialize`
- [ ] T051 [US5] Manual check with a client that consumes the extension; docs section "Native skills on single-server endpoints"

---

## Dependencies & Execution Order

- T005 blocks everything else. T006-T007 block every test that needs an upstream.
- PR-1 phases 3, 4 and 5 are independent of each other after T005-T007 and can run in parallel; phase 6 needs all three; phase 7 needs phase 6.
- PR-2 needs PR-1. PR-3 needs PR-2. PR-4 needs PR-2 (and PR-3 for nothing). PR-5 needs PR-2 and T039.
- Every pull request: `go test -race` on the touched packages, both `golangci-lint` runs (bare and `--build-tags server`), `make swagger-verify`, `./scripts/test-api-e2e.sh`, cross-model review before merge.

## MVP

PR-1 + PR-2 + PR-3: skills from any upstream, reviewed once, usable from every client. PR-4 and PR-5 widen the audience and can follow in later releases.

# Tasks: Agent-Token Scope Hardening

**Input**: [plan.md](plan.md) · [research.md](research.md) D1–D11 · [data-model.md](data-model.md) · [contracts/](contracts/) · [gap-map.md](gap-map.md) §1 (gap ids), §4 (building blocks), §7 (tests to invert)
**Organization**: one phase per PR in the plan's merge order `A → (B ∥ D ∥ E ∥ H0) → C → F → G → H1`. Every phase: failing tests first (one per gap id) → implementation → inverted pinned tests → verification → astra round. Story labels map FRs to spec user stories: **US1** = FR-001…FR-007, FR-010, FR-012 (scoped token cannot learn about other servers); **US2** = FR-009 (cannot execute above tier); **US3** = FR-008 (publication identity).

**Format**: `- [ ] [ID] [P?] [Story] Description with file path`. `- [~]` = process step with no committed artifact (excluded from the completion ratio).

**Common verification (every PR, from quickstart.md)**: `go test -race -count=1 -skip 'E2E|Binary|MCPProtocol|TestInfoEndpoint|TestGracefulShutdownNoPanic|TestSocketInfoEndpoint' ./internal/server/...` + touched packages · frozen goldens unchanged: `git diff --stat origin/main -- internal/server/testdata/*.golden.json internal/server/testdata/toolslist_goldens` empty (H0 excepted, two description strings only; H1 may ADD `internal/server/testdata/scope_latency/` but must not touch the goldens) · `/opt/homebrew/bin/golangci-lint run --config .github/.golangci.yml ./...` · `./scripts/test-api-e2e.sh` · astra review via `.review-tmp/run-astra.sh` (≤10 rounds, `VERDICT:` line required, `permission requested` count 0).

---

## Phase 1: Setup (shared across PRs)

- [ ] T001 Add `agentCtx(allowed []string, perms []string, pin string) context.Context` and `adminCtx()` helpers with the `["*"]`-means-unrestricted rule documented, in `internal/server/scope_fixture_test.go` (new; consumed by every PR's tests; empty `AllowedServers` = deny-all)
- [ ] T002 [P] Add `startCountingUpstream(t, tools ...toolSpec) (*countingUpstream)` generalising `startCountingTargetTierUpstream` (`internal/server/mcp_call_tool_target_tier_test.go:335-411`) so any test can assert zero upstream calls, in `internal/server/scope_fixture_test.go`
- [ ] T003 [P] Amend spec text per plan §"Spec text amendments" (FR-008 empty-raw-name analogue; FR-003/004 D1 pinned zero-reach decision; SC-007 restated as the gate on FR-005/008/010 + inverted-assertion list; FR-009 unresolved identity refused for every caller per Edge Case `spec.md:107`; FR-002 anonymous callers; SC-005 two new named administrator exceptions — FR-005 profile-scoped admin on shared-index fallback (D3) and FR-001 caller-kind-first (D5)) in `specs/105-agent-scope-hardening/spec.md`

---

## Phase 2: PR A — `scope-target-identity-producers` (FR-009, gaps FR009-G1…G7)

**Goal**: exact raw-name identity is produced (approval records, index docs) and consumed (gate, callability, preflight); unresolved identity refuses scoped callers; no-record under active quarantine is pending.
**Independent test**: discover `[erase, ns:erase]` on a manual-trust server with `erase` approved → `ns:erase` is `pending` under its own name and `call_tool_read a:ns:erase` from a full-tier `a`-only token is refused with zero upstream calls.

### Failing tests (write first, confirm RED against the merge base)

- [ ] T004 [US2] FR009-G1: `setupQuarantineRuntime` manual trust, baseline `erase` approved, discover `[erase, ns:erase]` → expect `BlockedTools["ns:erase"]` and a pending record keyed `ns:erase`; full-tier `a`-only `call_tool_read a:ns:erase` → `TOOL_QUARANTINED`, zero upstream — `internal/runtime/tool_quarantine_identity_test.go` (new) + `internal/server/mcp_call_tool_target_tier_test.go`
- [ ] T005 [P] [US2] FR009-G2: `applyDifferentialToolUpdate` with `[erase, ns:erase]` → `GetToolsByServer` length 2; `SaveToolApproval(ns:erase, Disabled)` survives a rerun (HEAD deletes it) — `internal/runtime/lifecycle_identity_test.go` (new) + `internal/index/bleve_rawname_test.go` (new: distinct docIDs)
- [ ] T006 [P] [US2] FR009-G3: seed `{a, erase, pending}`, delete `ns:erase` record; full-tier ctx: `makeDirectModeHandler` entry `ns:erase` → IsError + zero upstream; `preflightApprovalReader.ToolApproval` → non-nil pending — `internal/server/mcp_direct_callability_test.go` + `internal/server/preflight_glue_test.go`
- [ ] T007 [P] [US2] FR009-G4: counting upstream `[erase]`; full-tier `a`-only AND admin `call_tool_read a:ghost` → `Permission denied`, zero upstream (D4: every caller; SC-005 named exception); sandbox `call_tool('a','ghost')` → `PERMISSION_DENIED` envelope; unknown-server branch unchanged (control) — `internal/server/mcp_call_tool_target_tier_test.go` + `internal/server/mcp_code_execution_scope_test.go` (new)
- [ ] T008 [P] [US2] FR009-G5: manual-trust server, quarantine on, StateView has `ns:erase`, storage has no record → refused; only `{a, erase, approved}` seeded → `a:ns:erase` refused; `quarantine_enabled=false` → unchanged — `internal/server/tool_gate_test.go`
- [ ] T009 [P] [US2] FR009-G6: generated tables at the spec shape (`spec.md:132`): retrieve **54 cells** = 3 permission sets × 3 target tiers × 3 `call_tool_*` variants × strict-intent on/off, pre-classified allowed / insufficient-permission / intent-mismatch; plus direct (permission set × target tier, driven through the registered handler in A — G's T104a re-drives the same table through `HandleMessage`, D15) and nested (via real sandbox, envelope asserted) tables; paired-name rows (`erase` approved, `ns:erase` config-denied via `disabled_tools` / unapproved); `{read,destructive}` rows; `auth.AdminContext()` control rows; counting oracle on every cell — `internal/server/scope_target_tier_matrix_test.go` (new)
- [ ] T010 [P] [US2] FR009-G7: regression cell `{read,destructive}` × write target refused on all three paths (passes today; pins the exact-match rule) — `internal/server/scope_target_tier_matrix_test.go`

### Implementation

- [ ] T011 [US2] Add `RawName` to `config.ToolMetadata` with `CanonicalToolName`/`RawToolName` helpers in `internal/config/tool_identity.go` (new) and populate it from upstream tool names in `internal/upstream/core/client.go:394-397`
- [ ] T012 [US2] Producers use `RawName`: `checkToolApprovals` keys records by raw name (`internal/runtime/tool_quarantine.go:443-616,1216-1229`), `applyDifferentialToolUpdate`/`newToolsMap` stop collapsing and stop deleting exact-name records (`internal/runtime/lifecycle.go:673-790,975-980`); legacy collapsed record approves only its own raw name (one-shot, documented in code comment)
- [ ] T013 [US2] Bleve docID = `server:` + raw name (`internal/index/bleve.go:158-183,356-372,433`), `RawName` derived on read from the docID (not the stored `tool_name` / `full_tool_name`, which stay byte-identical scored fields — SC-005). **No index rebuild trigger** (adversarial review, 2026-09-14): the raw-keyed differential update self-heals a pre-upgrade collapsed doc on the first discovery (`TestApplyDifferentialToolUpdate_HealsPreUpgradeCollapsedDoc`), so a global wipe gated on the storage schema counter was pure cost and fired on every start
- [ ] T014 [US2] Reader: `lookupToolApproval` exact-wins + no-record-under-active-gate ⇒ pending (`internal/server/tool_gate.go:96-227`); direct callability and preflight read through it (`internal/server/mcp_direct_callability.go:136,192-201,253`, `internal/server/preflight_glue.go:400-411`); `ClassifyTool` consults the gate before returning Ready on nil approval (`internal/preflight/classify.go:98-100`)
- [ ] T015 [US2] Unresolved identity on a known server refuses **every** caller on retrieve (`internal/server/mcp.go:2283-2323`) and nested (`internal/server/mcp_code_execution.go:1251-1268` → explicit unresolved sentinel in the `ToolAnnotationFunc` contract, `internal/jsruntime/runtime.go:401-409` refuses on it); the unknown-server branch (`mcp_code_execution.go:987-997`) is untouched (D4); seed StateView in fixtures that register an upstream without one (`mcp_call_tool_trim_test.go:57`)
- [ ] T016 [US2] Docs: "Target tool tier" paragraph (exact-match permissions, annotation-less → read, unresolved → refused for scoped callers) replacing the "destructive implies both" claim in `docs/features/agent-tokens.md:140-150`

### Inverted pinned tests (gap-map §7)

- [ ] T017 [US2] Invert `internal/server/mcp_call_tool_target_tier_test.go:210-223,244-253,474` (ghost tool now refused for scoped callers); rebuild `internal/server/mcp_routing_test.go:306-447` direct cells on the real fixture with the counting oracle; resolve `extractToolName` callers in the three test files that still use it

### Verification

- [ ] T018 [US2] Run the common verification set plus `go test -race ./internal/runtime/... ./internal/index/... ./internal/preflight/... ./internal/jsruntime/...`; `git diff --stat -- internal/server/testdata` empty
- [~] T019 [US2] Live check: real daemon (quickstart §Live check) with a stdio server exposing `erase` + `ns:erase`; approve `erase` only; scoped full-tier token call to `a:ns:erase` refused; record output in PR body
- [~] T020 [US2] Astra review rounds (≤10) on the PR diff against FR-009 + gaps FR009-G1…G7; quote the final `VERDICT:` line in the PR body

---

## Phase 3: PR B — `scope-cache-legacy-invalidation` (FR-001/002, gaps FR001-G1…G7) — parallel with D, E, H0

**Goal**: legacy/internal cache entries refused for every caller (legacy also invalidated durably); child pages inherit the parent's provenance; agent refusals non-disclosing on MCP and REST.
**Independent test**: a record written without `producer` is refused for admin and agent, absent after bbolt reopen; a registry key is refused for admin via `read_cache` yet still `Peek`-able.

### Failing tests

- [ ] T021 [US1] FR001-G1: store legacy (nil producer) record → every reader kind incl. admin/anonymous gets `ErrUnauthorizedRead`; `Peek` false; close+reopen bbolt → absent; server-level admin `handleReadCache` → IsError without `records` — `internal/cache/manager_legacy_test.go` (new) + `internal/server/mcp_read_cache_authz_test.go`
- [ ] T022 [P] [US1] FR001-G2: store `registry-servers:…` and `npm:…` keys via the runtime/guesser writers → admin `readCacheAs` IsError, no payload; agent live-vs-absent key identical text; entry still `Peek`-able (no eviction) — `internal/server/mcp_read_cache_authz_test.go`
- [ ] T023 [P] [US1] FR001-G3: store, rewrite `ExpiresAt` past, `Get` errs, `Peek` false, on-disk count == in-memory count — `internal/cache/manager_test.go` (`TestExpiredRecords` extended to assert absence)
- [ ] T024 [P] [US1] FR001-G4: agent W `["*"]` produces K1; admin pages K1 → K2; W reads K2 → success — `internal/server/mcp_read_cache_authz_test.go`
- [ ] T025 [P] [US1] FR001-G5: narrow ctx `[weather]`; live broad key vs nonexistent → identical text on `handleReadCache` and `CallToolDirect`; `cache key not found` substring kept — `internal/server/mcp_read_cache_authz_test.go` + `internal/server/mcp_call_tool_direct_test.go`
- [ ] T026 [P] [US1] FR001-G6: table — admin reader qualifies for ANY snapshot regardless of its profile (unscoped, narrower, wider, empty, deleted profile → true); agent reader vs admin-produced snapshot → false; pinned wildcard agent vs unpinned agent entry → false; empty-profile agent reader → false (deny-all guard for agents only) — `internal/cache/authorization_test.go` (D5 / FR-001 `spec.md:124`)
- [ ] T027 [P] [US1] FR001-G7: upgrade fixture (raw JSON record without `producer` and with `version:0`), fresh-internal-entry, recursive-child on MCP + REST, pinned-token REST (direct dispatch refused AND `read_cache` refused with nonexistent-key body), held-call narrowing (passes; regression) — `internal/server/scope_cache_fixtures_test.go` (new)

### Implementation

- [ ] T028 [US1] `Record.Version` + `Authorization.Kind = internal`; `GetRecordsAs`: nil/unknown version → refuse + delete inside the committed `Update` (tx fn returns nil; refusal surfaced outside), stats mutate on commit only; internal → refuse without eviction; **caller kind first** — admin reader qualifies for any snapshot, agent never for an admin snapshot, deny-all guard applies to agent readers only; rename `Unrestricted()` → `IsAdministrator()` (D2, D5) — `internal/cache/models.go:26-43`, `internal/cache/manager.go:108-245`, `internal/cache/authorization.go:52-95`
- [ ] T029 [P] [US1] Stamp internal writers `CallerKindInternal` in `internal/runtime/runtime.go:2226` and `internal/experiments/guesser.go:349`
- [ ] T030 [US1] `ReadCacheResponse.Producer` (`json:"-"`) carries the parent's authorization to child page stores; `handleReadCache` collapses unauthorized/not-found/expired into the `cache key not found` body for agent callers (real BBolt errors distinct; activity log keeps the real reason) — `internal/server/mcp.go:5613-5690`, `internal/server/cache_authz.go:54-86`, `internal/server/content_forward.go:256`

### Inverted pinned tests

- [ ] T031 [US1] Invert `internal/cache/authorization_test.go:65-72,93-121,123-154`, `internal/server/mcp_read_cache_authz_test.go:86,171`, `internal/server/mcp_call_tool_direct_test.go:35-48` (seed stamped records instead of unstamped)

### Verification

- [ ] T032 [US1] Common verification + `go test -race ./internal/cache/... ./internal/runtime/... ./internal/experiments/...`; `-tags server` build to `/dev/null`
- [~] T033 [US1] Live check: daemon with a pre-feature `config.db` copy; `read_cache` on an old key refused, key gone after restart
- [~] T034 [US1] Astra rounds on FR-001/002 + FR001-G1…G7; quote final `VERDICT:`

---

## Phase 4: PR D — `scope-selectable-profile-predicate` (FR-003/004, gaps FR003-G1…G8) — parallel with B, E, H0

**Goal**: `/mcp/p/<slug>`, `/mcp/p`, `/mcp/p/` and `set_profile` apply the selectable-profile predicate for every scoped caller; one refusal body across missing/deleted/not-selectable/pin-mismatch/no-profiles/zero-reach; URL precedence honoured by `set_profile`.
**Independent test**: unpinned `research-srv`-only token: `/mcp/p/deploy` ≡ `/mcp/p/nonexistent` ≡ `/mcp/p` (status+body), `/mcp/p/research` 200.

### Failing tests

- [x] T035 [US1] Generalise `mintPinnedToken` to `mintAgentToken(t, env, name, allowed, perms, pin)` in `internal/server/profile_integration_test.go:627` (H1 reuses it)
- [x] T036 [US1] FR003-G1/G2/G5: `a`-only unpinned token — `/mcp/p/deploy` (disjoint), `/mcp/p/nonexistent`, `/mcp/p`, `/mcp/p/`, deleted `deploy` → equal status+body, no `available`; `/mcp/p/research` positive control — `internal/server/profile_integration_test.go`
- [x] T037 [P] [US1] FR003-G3/G4: pinned `research` — `/mcp/p/deploy`, `/mcp/p/nope`, deleted-pin `/mcp/p/research` → identical; fleet `[deploy]` vs `nil` → identical per slug — `internal/server/profile_integration_test.go`
- [x] T038 [P] [US1] FR003-G6: ctx scoped `[research-srv, deploy-srv]` + `profile.WithProfileScope(research)`; `set_profile deploy` → `active_profile == "deploy"` (stored) but `servers == [research-srv]` (URL governs: URL profile ∩ token, `spec.md:112,126`); clear → `[research-srv]` — `internal/server/profile_tool_test.go`
- [x] T039 [P] [US1] FR003-G7: pinned `research`, `set_profile ""` → `active_profile == ""`, `servers == [research-srv]` — `internal/server/profile_pin_enforcement_test.go`
- [x] T040 [P] [US1] FR003-G8 (D1): pinned `empty`/ghost pin — `set_profile empty` → deleted-pin body, no mutation; `/mcp/p/empty` → uniform 404 — `internal/server/profile_tool_test.go` + `internal/server/profile_integration_test.go`

### Implementation

- [x] T041 [US1] `profileMiddleware` evaluates the selectable-profile rule for the requested slug ONLY (`profileIndex.selectable` over a per-snapshot slug index, keyed on `auth.IsScopedCaller`; never `selectableProfileNames`, whose cost is fleet-sized — codex round 2) for `/mcp/p/<slug>`, `/mcp/p`, `/mcp/p/`; ONE refusal constructor (`profileNotSelectable(w)`) for missing/deleted/not-selectable/pin-mismatch/no-profiles/zero-reach; no-profiles branch moved after the gate; admin/anonymous branches unchanged; every scoped refusal logs one operator-facing line (`profile URL refused for scoped caller`: agent_name, profile, remote_addr — critique round 1, S1) — `internal/server/server.go`
- [x] T042 [US1] `handleSetProfile`: pin branch requires reach (D1); for scoped callers `servers` = effective scope after the update via `resolveActiveProfileIn` (pin > URL > session, same config snapshot as the admission check) ∩ token, rendered in profile-declared order — on a URL-scoped endpoint that is the URL profile, not the stored selection; `active_profile` = stored selection; cleared pinned selection reports `active_profile == ""`. Administrators short-circuit to the pre-105 payload (selected profile's servers / all servers on clear) — SC-005 names no FR-003 exception, so the URL-precedence reporting is agent-only (critique round 1, A1/A2) — `internal/server/profile_tool.go`
- [x] T043 [P] [US1] Update the cleared-selection line in `docs/features/profiles.md:70-72`

### Inverted pinned tests

- [x] T044 [US1] Invert `internal/server/profile_integration_test.go:201-233,647-703,775-808`, `internal/server/profile_tool_test.go:344,356,397-411` (`TestHandleSetProfile_PinnedTokenSelectsDisjointPin` → refuses), `internal/server/profile_pin_enforcement_test.go:150,160`; keep `TestHandleSetProfile_AdminUnchanged`, `TestProfile_404UnknownSlug/404NoProfiles` as admin controls

### Verification

- [x] T045 [US1] Common verification + `go test -tags server -race ./internal/serveredition/...` (AuthTypeUser is scoped)
- [~] T046 [US1] Live check: daemon with two profiles; unpinned scoped token hits `/mcp/p/<disjoint>` and `/mcp/p/<missing>`; bodies diffed byte-equal
- [~] T047 [US1] Astra rounds on FR-003/004 + FR003-G1…G8 + D1; quote final `VERDICT:`

---

## Phase 5: PR E — `scope-log-attribution` (FR-007, gaps FR007-G1…G6) — parallel with B, D, H0

**Goal**: per-record log ownership; `tail_log` filters by owner before limiting; OAuth callback stop logs through the subject-bound logger; Docker cleanup touches only canonically owned containers.
**Independent test**: writers `a/b` and `a_b` share one file; `tail_log a_b` from an `a_b`-only token returns zero `a/b` lines and `lines_returned` equals the filtered count.

### Failing tests

- [ ] T048 [US1] FR007-G1: `internal/logs` writers `a/b`, `a_b`; sentinel in `a/b`; attributed tail(`a_b`, 50) → no sentinel; a child stderr line `left | right | {"server":"a_b"}` written through the REAL stderr path (`monitoring.go:214`, `zap.String("message", line)`) by `a/b` is attributed to `a/b` and never to `a_b`; same cases under the JSON encoder (`logger.go:152-155`); server-level differential with/without `a/b`; admin `tail_log` bytes unchanged vs pre-feature capture — `internal/logs/logger_attributed_test.go` (new) + `internal/server/mcp_tail_log_scope_test.go`
- [ ] T049 [P] [US1] FR007-G2: `O_APPEND` an unstamped `LEGACY_PLAIN_LINE`; attributed read excludes it; admin whole-file read includes it; historical subject-evidence records (`spec.md:130`): a pre-upgrade `Removing existing container` record stamped `server=a` with `container_name=mcpproxy-a-b-wxyz` (shape of `docker.go:555-558`), the same-sanitised-name case — `server=a/b` with `container_name=mcpproxy-a-b-wxyz` and no `container_owner` (indistinguishable from hidden `a-b`'s container) — an ID-only `container_id` record, and a callback-stop record stamped `server=a` naming server `b`'s port — all withheld from the scoped reader, all present for admin; a post-upgrade record with `container_owner=a/b` IS returned to the `a/b` reader — `internal/logs/logger_attributed_test.go`
- [ ] T050 [P] [US1] FR007-G3: own1/foreign1/own2/foreign2 interleaved; attributed tail(`a_b`, 2) == `[own1, own2]`; server-level `lines_returned == 2` — `internal/logs/logger_attributed_test.go` + `internal/server/mcp_tail_log_scope_test.go`
- [ ] T051 [P] [US1] FR007-G4: two observer loggers, start callback servers `a`,`b`, `StopCallbackServer("a")` → stop record only in `a`'s observer, both start orders — `internal/oauth/callback_stop_logger_test.go` (new)
- [ ] T052 [P] [US1] FR007-G5: fake docker (`SetWellKnownDockerPathsForTest` + `ResetDockerPathCacheForTest`) `ps` → `deadbeef1234 mcpproxy-a-b-wxyz` (label `a-b`) + `mcpproxy-a-wxyz` (label `a`); server `a` connect/disconnect → foreign never rm/stop/kill'd nor logged; own removed; SECOND fixture: no owned container, empty known container ID, one foreign container on the SAME image → the image-name fallback (`docker.go:243-248,296-329`) touches nothing; unit matcher table `a` vs `a-b` vs `a/b` vs `A` — `internal/upstream/core/docker_ownership_test.go` (new)
- [ ] T053 [P] [US1] FR007-G6: forced-rotation shared-history and case-only (`A`/`a`, branch on FS case sensitivity) fixtures; admin outcomes recorded — `internal/logs/logger_attributed_test.go`

### Implementation

- [ ] T054 [US1] **No new field** (D8): keep the existing `server=<raw>` zap field at `internal/logs/logger.go:385`; add `ReadUpstreamServerLogTailAttributed(name, n)`: console encoder → scan ` | {` boundaries left to right and accept the first whose suffix decodes as exactly one complete JSON object (no trailing bytes); JSON encoder → whole line; match `server` exactly AND apply the subject-evidence rule (a container record is attributable only with `container_owner` == requested server; withhold every container record lacking it and every callback record naming another server — D8 rule 3), filter before taking the last *n*, withhold lines with no accepted boundary; whole-file reader untouched (admin records byte-identical) — `internal/logs/logger.go:321-547`
- [ ] T054a [P] [US1] Producer audit test: every `upstreamLogger.{Info,Warn,Error,Debug}(` call in `internal/upstream/core` passes a constant message literal (child-controlled text only as field values) — `internal/upstream/core/upstream_logger_audit_test.go` (new)
- [ ] T055 [US1] `handleTailLog` uses the attributed reader for scoped callers, whole-file for admins; `lines_returned` = filtered length — `internal/server/mcp.go:5787-5803`
- [ ] T056 [P] [US1] OAuth: `stopCallbackServerLocked` logs through the recorded `server.logger`; `StopCallbackServer` no longer calls `adoptLoggerLocked` on stop (nil-logger signature kept) — `internal/oauth/config.go:1686-1735`
- [ ] T057 [P] [US1] Docker: `ensureNoExistingContainers`, the disconnect name-pattern fallback AND the image-name fallback all filter by label `com.mcpproxy.server=<raw>` AND `^mcpproxy-<san>-[a-z0-9]{4}$`; foreign matches neither logged nor removed; every housekeeping record that names a container adds `zap.String("container_owner", <label value>)` (D8 rule 3, SC-005 housekeeping-record change) — `internal/upstream/core/docker.go:243-248,296-329,349-431,501-582`

### Inverted pinned tests

- [ ] T058 [US1] Rewrite `internal/server/mcp_tail_log_scope_test.go:63-64,110-117` and `internal/server/mcp_secret_redaction_test.go:288` to use stamped writers (unstamped canary is now withheld); keep `TestTailLog_AdminUnchanged`; close returned `io.Closer`s in fixtures

### Verification

- [ ] T059 [US1] Common verification + `go test -race ./internal/logs/... ./internal/oauth/... ./internal/upstream/core/...`
- [~] T060 [US1] Live check: daemon with servers `a/b` and `a_b`; scoped `tail_log a_b` shows no `a/b` lines
- [~] T061 [US1] Astra rounds on FR-007 + FR007-G1…G6 + D8/D9; quote final `VERDICT:`

---

## Phase 6: PR H0 — `scope-regression-suite` part 1: FR-012 (gaps FR01x-G1…G3) — parallel with B, D, E

**Goal**: stored-script not-found no longer enumerates for agent callers; descriptions stop advertising discovery-by-failed-call; docs carry the invariant.
**Independent test**: agent `["*"]` calling `code_execution script=gamma` with `alpha-SENTINEL.js` present gets an error containing neither `SENTINEL` nor `Available scripts`; admin still enumerates.

### Failing tests

- [ ] T062 [US1] FR01x-G1: agent ctx `["*"]`, script `gamma`, `alpha-SENTINEL.js` present → IsError, no `SENTINEL`/`Available scripts`, byte-equal to empty-dir proxy; admin control kept; positive controls per `spec.md:116`: `a`-only token runs a stored script returning a constant (no upstream call) and gets the constant; a stored script calling `b` is refused at the nested call; scoped initialization publishes custom `instructions` mentioning `b:private_search` (documented) — `internal/server/mcp_code_scripts_test.go` + `internal/server/mcp_instructions_scope_test.go` (new)
- [ ] T063 [P] [US1] FR01x-G2: `TestCodeExecutionDescriptions_EnumerationIsAdminOnly` asserting `code_execution.description` and `script.description` no longer advertise enumeration, and a golden-delta assertion that only those two strings changed — `internal/server/toolslist_snapshot_test.go`

### Implementation

- [ ] T064 [US1] Caller-kind branch: enumeration only for non-scoped callers — `internal/server/mcp_code_execution.go:473-499` (or `internal/codescripts/codescripts.go:338-356` with a caller flag)
- [ ] T065 [US1] Reword `internal/server/mcp_code_execution.go:52-53,73-74`; regenerate goldens with `MCPPROXY_WRITE_TOOLSLIST_GOLDENS=testdata/toolslist_goldens go test -run TestToolsListSnapshot ./internal/server/` (the variable is the OUTPUT DIRECTORY, `toolslist_snapshot_test.go:151-158`), then rerun with it unset; diff limited to `internal/server/testdata/toolslist_goldens/{default_server,retrieve_tools_mode,code_execution_mode}.json`
- [ ] T066 [P] [US1] Docs: enumeration is admin-only in `docs/code_execution/overview.md:379-387`, `cookbook.md:141`, `troubleshooting.md:604-613`, `api-reference.md:591`; add invariant sentence, covered-surface list (`/mcp`, `/mcp/all`, `/mcp/code`, `/mcp/call`, `/mcp/p/<slug>`, aliases), retained-effects list and custom-instructions/stored-scripts secrets warning to `docs/features/agent-tokens.md` (FR01x-G3)

### Verification

- [ ] T067 [US1] Common verification; `git diff --stat -- internal/server/testdata` shows only the three goldens
- [~] T068 [US1] Astra rounds on FR-012 + FR01x-G1…G3; quote final `VERDICT:`

---

## Phase 7: PR C — `scope-retrieve-tools` (FR-005, gaps FR005-G1…G5) — after A

**Goal**: `retrieve_tools` computes search, `total`, indexed counts, usage ranking and session risk over the authorized population only; admin output byte-identical.
**Independent test**: `a`-only token, `{query:"rotate keys", limit:1}` with a higher-scoring hidden `b` tool → `[a:rotate_keys]`; admin → the `b` tool.

### Failing tests

- [ ] T069 [US1] FR005-G1: index `a:rotate_keys` + two `b` tools with repeated query terms; `a`-only `{query:"rotate keys", limit:1}` → `[a:rotate_keys]`, `total 1`; `["*"]` control → `b` tool; profile-scoped admin on the shared-index fallback → `[a:rotate_keys]` (D3, SC-005 named exception); unprofiled admin → `b` tool unchanged; `TestSearchScoped_ScoreIdenticalToExhaustiveFilter`: ordered `(id, score)` from `SearchScoped` == exhaustive `Search(Size=docCount)` filtered then cut, on the 527-tool snapshot and multi-term fixtures (D6) — `internal/server/mcp_retrieve_scope_test.go` (new) + `internal/index/manager_scoped_test.go` (new)
- [ ] T070 [P] [US1] FR005-G2: seed `IncrementToolUsage` `b:secret_sentinel_tool`×3, `a:echo`×1, `a:removed_tool`×1 (unindexed AND carrying a stale *approved* record, so it can only be excluded by population membership), `a:pending_tool`×2 (indexed but pending approval); `a`-only `include_stats` → `top_tools == [{a:echo,1}]` (current authorized population ∧ approved — `spec.md:128`); admin unchanged; `TestRetrieveToolsFullMode_GoldenByteIdentity/include_stats` still green — `internal/server/mcp_retrieve_scope_test.go`
- [ ] T071 [P] [US1] FR005-G3: `buildMCPProxyWithActivation`; StateView `a` (read-only) + `b` (nil annotations); `a`-only → `session_risk.level == low`, `lethal_trifecta == false`; delete `b` → deep-equal; admin keeps `high` — `internal/server/mcp_session_risk_test.go`
- [ ] T072 [P] [US1] FR005-G4: index `a:t1, b:t2, b:t3`; `a`-only `{debug:true}` → `total_indexed_tools == 1`; admin 3; profile-index path also 1 — `internal/server/mcp_retrieve_scope_test.go`
- [ ] T073 [P] [US1] FR005-G5: `TestRetrieveTools_ScopeOracle` table-driven over three-server vs `a`-only fixtures with `include_stats+debug+session_risk` (US1.5) — `internal/server/mcp_retrieve_scope_test.go`

### Implementation

- [ ] T074 [US1] `index.Manager.SearchScoped(query, limit, servers)` = conjunction(text query, boost-0 `server_name` disjunction) with exhaustive-then-filter as the ONLY fallback if the equality test fails (D6); `ScopedDocumentCount(servers)` via `server_name` facet terms — `internal/index/manager.go:105-143`, `internal/index/bleve.go:274,458-486`
- [ ] T075 [US1] Retrieve handler: route scoped callers (`auth.IsScopedCaller`) and profile-scoped administrators on the shared-index fallback through `SearchScoped` with the effective set from `serverInScope`/`resolveActiveProfile`; unprofiled administrators keep `Search` + post-filter byte-for-byte (D3); `debug.total_indexed_tools` from `ScopedDocumentCount`; `usage_summary.top_tools` filtered by current authorized-population membership (`lookupIndexedTool` / StateView presence) **and** approval (exact-tool callability predicate) then cut to 10 — both are required: callability alone never checks that the tool still exists (`mcp_direct_callability.go:168-229`), membership alone admits pending tools; ordinary discovery stays quarantine-blind (admin path untouched: `GetToolStats(10)`, non-nil `[]`) — `internal/server/mcp.go:1705-2043`, `internal/server/mcp_visibility.go` (`scopedIndexedToolCount`), `internal/storage/manager.go:424-456` (unlimited/filterable stats)
- [ ] T076 [US1] `analyzeSessionRiskScoped(snapshot, serverDiscoverable)`; admin keeps `analyzeSessionRisk` — `internal/server/mcp_annotations.go:29-40,98-104`, call site `internal/server/mcp.go:2007-2016`

### Verification

- [ ] T077 [US1] Common verification; `TestFilterDiagnostics_WindowUsesNormalizedLimit`, `TestSurfaceIsolation_RetrieveTools*`, `TestRetrieveTools_TruncatedPayloadReadableViaReadCache` and all four `retrieve_full_*` goldens green; `isToolCallable` untouched (quarantine-blind)
- [ ] T078 [US1] FR-011 pre-check: `scope_latency_test.go` prototype on the 527-tool snapshot (`loadDeferredLargeCorpus`) for `retrieve_tools`, scoped-vs-admin ≤ 20 ms, skipped under `-race`; local merge-base p95 table in the PR body — `internal/server/scope_latency_test.go` (new; H1 extends to four operations + CI job)
- [~] T079 [US1] Astra rounds on FR-005 + FR005-G1…G5 + D3/D6; quote final `VERDICT:`

---

## Phase 8: PR F — `scope-direct-publication` (FR-008, gaps FR008-G1…G7) — after A

**Goal**: every direct-surface definition is authorized by the identity of the publication that produced it, at every seam, both skew directions, full and deferred; built-ins positively identified; no-identity definitions withheld from everyone.
**Independent test**: `rebuildPaused` origin flip `{a, b__c}` → `{a__b, c}`; `a`-only `tools/list` during the pause never returns `a__b__c`'s definition, in full and deferred modes.

### Failing tests

- [ ] T080 [US3] FR008-G1: `newSkewFixture`, old `{a, b__c, S1}` → new `{a__b, c, S2}`; `a`-only `listed()` during pause → `a__b__c` absent, no `[a__b]` description, schema `S1`; both modes; `*` + admin controls — `internal/server/mcp_direct_skew_test.go`
- [ ] T081 [P] [US3] FR008-G2: `rebuildPaused` adding `__a__review` / `b__` with sentinel → not listed for `a`-only during seam; positive-identification test: `describe_tool` is Builtin via the explicit set, `__x__y` and a registry tool named `retrieve_tools` are NOT — `internal/server/mcp_direct_catalog_publish_test.go` + `internal/server/mcp_direct_catalog_test.go`
- [ ] T082 [P] [US3] FR008-G3: reverse flip `a__b → a` and plain addition `b__x`; `a`-only / `b`-only token sees the tool during the window — `internal/server/mcp_direct_skew_test.go`
- [ ] T083 [P] [US3] FR008-G4: `rebuildPaused` ReadOnly → Destructive; `{read}` token not listed during seam; handler IsError `Permission denied`; full + deferred — `internal/server/mcp_direct_skew_test.go`
- [ ] T084 [P] [US3] FR008-G5: inside `rebuildPaused`, `tools/call a__b__c` through `directServer.HandleMessage` as `a`-only → the WHOLE JSON-RPC envelope (code `-32602`, message, data) is byte-equal to the envelope for the same requested name `a__b__c` in the fixture where `a__b` is absent (the caller-supplied name is echoed in both); assert no owner metadata (`server`, `owner`, canonical id) appears anywhere in the envelope (D12) — `internal/server/mcp_direct_skew_test.go`
- [ ] T085 [P] [US3] FR008-G6: `__a` server fixture — steady state listed/describable/dispatchable for `*` + admin, withheld + `-32602` parity for `a`-only; seam variant via `rebuildPaused`; both modes — `internal/server/mcp_direct_underscore_test.go` (new)
- [ ] T086 [P] [US3] FR008-G7 + FR-006 consumer: `buildDirectCatalog([{a, ""}]).Len() == 0`; filter omits `a__` for admin and agent; prompt aggregation drops empty prompt names; an UNSTAMPED prompt (no accepted registration) is withheld from `prompts/list` and refused by `prompts/get` for admin AND agent (admin outcome recorded, SC-005) — `internal/server/mcp_direct_catalog_test.go` + `internal/server/mcp_prompt_scope_test.go`
- [ ] T086a [P] [US3] Protocol-level proof: `tools/list` + `tools/call` through `directServer.HandleMessage` show (a) no stamp on the wire for admin and agent, (b) the scope filter is re-evaluated at `tools/call` (hidden registered tool → `-32602`) — the mcp-go behaviour the plan relies on (D7) — `internal/server/mcp_direct_protocol_test.go` (new)

### Implementation

- [ ] T087 [US3] `directToolStamp{Owner, RawName, Tier}` private struct written into `mcp.Tool.Meta` by `renderDirectTools`; `builtinDirectToolNames` populated from the built-in constructors (`buildDescribeToolTool().Name` etc.) at `internal/server/mcp_routing.go:143-148,156-311`
- [ ] T088 [US3] Scope filter and callability filter read the stamp first (neither removes it), consult the catalog only for unstamped entries, withhold unstamped non-built-ins; a TERMINAL third `WithToolFilter` (`stripDirectToolStamp`, registered last) removes the stamp for EVERY caller (remove the admin early-return at `internal/server/mcp_direct_scope.go:48-50`); `filterAggregatedPromptsForAuth` withholds unstamped prompts regardless of `enforce` and `prompts/get` refuses them (FR-006) — `internal/server/mcp_direct_scope.go:40-143,185-212`, `internal/server/mcp_direct_callability.go:49-92`, `internal/server/mcp_routing.go:958-961`
- [ ] T089 [US3] Catalog: remove parse-failure → Builtin inference; withhold empty raw name; tier from the producing entry — `internal/server/mcp_direct_catalog.go:151-224,374-423`; direct handler in-seam scope refusal returns the error mcp-go maps to the SAME `-32602` envelope as an unregistered name (replace the `NewToolResultError` branches at `internal/server/mcp_routing.go:423-440`), never naming the captured owner (D12)
- [ ] T090 [P] [US3] Prompt aggregation drops empty prompt names (`internal/server/mcp_routing.go:1322-1326`) and `prompts/get` refuses them (`internal/server/mcp.go:900`)

### Inverted pinned tests

- [ ] T091 [US3] Invert `internal/server/mcp_direct_skew_test.go:195-224,316-332,459-489`, `internal/server/mcp_direct_catalog_publish_test.go:65-101,155`, `internal/server/mcp_direct_catalog_test.go:177-235`; `direct_full_prefeature.golden.json` and `TestDirectModes_SetIdentity`/`toolSetFingerprint` stay byte-exact

### Verification

- [ ] T092 [US3] Common verification; Spec 102 SC-007 listing/describe parity tests green
- [~] T093 [US3] Astra rounds on FR-008 + FR008-G1…G7 + D7; quote final `VERDICT:`

---

## Phase 9: PR G — `scope-refusal-shapes` (FR-010, gaps FR010-G1…G7) — after C and F

**Goal**: scope-first refusal precedence with hidden ≡ nonexistent on every surface; tier denials on authorized servers reach the handler; suggestions and alias resolution computed over the authorized corpus.
**Independent test**: `a`-only token, `call_tool_read a:t` when `a` has no live client → error text identical between the `{a,b,a__b}` and `{a}` fixtures.

### Failing tests

- [ ] T094 [US1] FR010-G1: fixture A `a` (no client) + `b`,`a__b` added after `PhaseReady`; `a`-only `call_tool_read a:t` → text identical across fixtures, no sentinel (order-insensitive) — `internal/server/mcp_auth_scope_test.go`
- [ ] T095 [P] [US1] FR010-G2: index `B:read` (+ hidden `b:read` in A); token `[B]`; `describe_tool b:read` definition → equal responses with `B:read` suggestion; `describe_plain_corpus_test.go` bytes unchanged — `internal/server/mcp_describe_tool_scope_test.go` (new)
- [ ] T096 [P] [US1] FR010-G3: catalog A `{x:y:z sentinel, x__y:z}` vs B `{x__y:z}`; token `[x__y]`; `describe_tool x__y:z` definition + check → identical, no sentinel; admin `not_found` control (`TestDescribeDirect_DisplayAndCanonicalNamespaceOverlap`) green — `internal/server/mcp_describe_direct_test.go`
- [ ] T097 [P] [US1] FR010-G4: catalog A hidden `B:read` + authorized `b:Read`; token `[b]`; `describe_tool b:read` → suggestion `b:Read` in both fixtures — `internal/server/mcp_describe_direct_test.go`
- [ ] T098 [P] [US1] FR010-G6: real proxy; ctx `[a]`,`{read}`; `p.directServer.HandleMessage` `tools/call a__write_tool` → isError `Permission denied … 'write'`, zero upstream (HEAD `-32602`) — `internal/server/mcp_direct_scope_test.go`
- [ ] T099 [P] [US1] FR010-G7: token `{[a], pin P={a,b}}`; `call_tool_read b:t` vs `zzz:t` → identical text; sandbox `call_tool('b')` vs `('zzz')` → identical envelope — `internal/server/mcp_auth_scope_test.go` + `internal/server/mcp_code_execution_scope_test.go`

### Implementation

- [ ] T100 [US1] `Available servers:` filtered through `serverInScope` for scoped callers — `internal/server/mcp.go:2473-2495`
- [ ] T101 [US1] Effective set = profile ∩ token evaluated once, one body for agent callers on `call_tool_*` (`internal/server/mcp.go:2262-2293`), sandbox allow-list (`internal/server/mcp_code_execution.go:1198-1229`) and nested refusal (`internal/jsruntime/runtime.go:383-410`); admins on `/mcp/p` keep today's text
- [ ] T102 [US1] describe_tool definition mode: `toolVisibleToSession` evaluates scope before index presence (`internal/server/mcp_visibility.go:51-72`); not-found + case-correction through `visibleCorpus.notFoundResult` over the authorized corpus (`internal/server/mcp_describe_tool.go:123-160`); direct case-correction `continue` on invisible match (`internal/server/mcp_describe_direct.go:133-155`)
- [ ] T103 [US1] Shadow canonical map in the direct catalog so an authorized canonical id resolves even when a hidden display entry collides — `internal/server/mcp_direct_catalog.go:227-268`, `internal/server/mcp_describe_direct.go:54-103`
- [ ] T104 [US1] Call-time `WithToolFilter` evaluates scope → tier → callability in spec order (D13): hidden → `-32602`; over-tier on an authorized server → passed through to the handler (insufficient-permission); in-scope within-tier but disabled/quarantined/pending/changed → `-32602` unchanged; `tools/list` predicate still withholds over-tier and non-callable — `internal/server/mcp_direct_scope.go:115-143`, `internal/server/mcp_routing.go:958-961`
- [ ] T104a [P] [US1] FR010 precedence regression through `HandleMessage`: disabled/quarantined/pending/changed alone → `-32602` unchanged; over-tier alone → insufficient-permission; over-tier + pending → insufficient-permission (tier-first, `spec.md:133`); re-drive the generated FR-009 direct table from T009 through `HandleMessage` (D15) — `internal/server/mcp_direct_scope_test.go`

### Verification

- [ ] T105 [US1] Common verification; `mcp_auth_scope_test.go:84` updated; admin controls green
- [~] T106 [US1] Astra rounds on FR-010 + FR010-G1…G7; quote final `VERDICT:`

---

## Phase 10: PR H1 — `scope-regression-suite` part 2 (FR-011/013/014, SC-007, gaps FR01x-G4…G7) — last

**Goal**: the two-fixture differential oracle, HTTP credential matrix and latency test exist, cover every user-story scenario by id, and prove no pinned-reversal test survived.
**Independent test**: `TestScopeCoverage_EveryUserStoryScenario` passes only when US1.1–1.8, US2.1–2.6, US3.1–3.4 are all registered.

- [ ] T107 [US1] `newScopeFixture(t, full)`, `runScopeScenario(t, usID, fn)`, `normalizeScopeResponse` (nondeterministic fields only), per-fixture retrieve-oracle derivation (`Search(Size=docCount)` filtered then cut) asserted for every SC-001-excluded field, coverage registry + `TestScopeCoverage_EveryUserStoryScenario` per contracts/differential-oracle.md — `internal/server/scope_differential_test.go` (new)
- [ ] T108 [US1] Register every US1.x scenario (retrieve oracle incl. `include_stats+debug+session_risk`, describe, call_tool refusal, read_cache, set_profile, profile URL, tail_log, stored scripts, prompts) against both fixtures — `internal/server/scope_differential_test.go`
- [ ] T108a [P] [US1] Retained-effect mode `runRetainedEffectScenario` + one named fixture per SC-001 exclusion (`spec.md:114,161`): shared-limiter contention (global capacity 1, no queue, held call on hidden `b`, call to `a` → "proxy-wide limit saturated"), cross-server scan admission (real discovery, baseline on `a`, same-name near-identical tool on hidden `b` under `trust_mode: scan` → `a`'s addition pending via shadowing), prompt-name collision, global prompt cap, direct display-name collision, shared prompt-refresh deadline, shared log rotation/retention — each asserting authorized ownership of everything returned, no hidden content/sentinel, and the documented outcome — `internal/server/scope_retained_effects_test.go` (new)
- [ ] T109 [P] [US2] Register US2.1–2.6 (retrieve 54-cell + direct + nested tables from T009, nested envelope through the real runtime, unresolved identity for every caller) — `internal/server/scope_differential_test.go`
- [ ] T110 [P] [US3] Register US3.1–3.4 (origin flip, unparseable names, tier change, refusal text) reusing `newSkewFixture` — `internal/server/scope_differential_test.go`
- [ ] T111 [US1] HTTP credential matrix: `mintAgentToken` (T035) × surfaces `{/mcp, /mcp/all, /mcp/code, /mcp/call, /mcp/p/<slug>, aliases}` × operations; `yes` cells run the differential runner, `n/a` cells assert `-32602` unregistered; no `E2E` in the name; one env per fixture — `internal/server/scope_http_matrix_test.go` (new)
- [ ] T112 [P] [US1] Finalise `scope_latency_test.go` (T078) over `retrieve_tools`, `read_cache` (frozen 10-page entry), `prompts/list` (frozen 50-prompt set), `tools/list`: 20 warm-up + 200 timed per caller, `p95(scoped) − p95(admin) ≤ 20ms` per operation, `-race` skip — `internal/server/scope_latency_test.go` + frozen fixtures under `internal/server/testdata/scope_latency/`
- [ ] T112a [P] [US1] Merge-base gate: `.github/workflows/scope-latency.yml` (non-race, reference runner, on PRs touching `internal/server`/`internal/index`) checks out merge-base into a second directory, copies head's `scope_latency_test.go` + `testdata/scope_latency/` over it (test-only, backward compatible: the test compiles against the pre-feature API), runs both in one job, fails on administrator p95 regression > max(10%, 5 ms) per operation AND fails if either side yields no measurement for any operation; comparison in Go, no new dependency (D10)
- [ ] T113 [US1] SC-007 inventory table (US id → test name → `-run` pattern) in the file header of `internal/server/scope_differential_test.go`; `grep` guard test that none of the four pinned-reversal assertions (gap-map §7) still exist in their original form
- [ ] T114 [P] [US1] Docs: final invariant wording + retained-effects list reconciled with what shipped in `docs/features/agent-tokens.md`
- [ ] T115 Common verification across all touched packages; roadmap: flip the eight `scope-*` tasks to `done` with PR numbers in `roadmap.yaml:430-469` and regenerate `ROADMAP.md` (`python3 scripts/gen-roadmap.py`); `python3 scripts/gen-roadmap.py --check-github`
- [~] T116 Astra rounds on FR-011/013/014 + FR01x-G4…G7; quote final `VERDICT:`

---

## Dependencies & Execution Order

```
Phase 1 (T001–T003)
   └─► A (T004–T020)  ─┬─► B  (T021–T034) ─┐
                       ├─► D  (T035–T047) ─┤
                       ├─► E  (T048–T061) ─┼─► C (T069–T079) ─► F (T080–T093) ─► G (T094–T106) ─► H1 (T107–T116)
                       └─► H0 (T062–T068) ─┘
```
- **A first**: data-migration event (raw-name approvals + index docIDs); C shares `bleve.go`, F/G share the callability reader.
- **B ∥ D ∥ E ∥ H0**: no shared **function** (B and E both edit `mcp.go` in disjoint regions, D14); H0 touches `docs/features/agent-tokens.md` after A's paragraph lands — rebase, no conflict expected.
- **C before G**: G's describe not-found policy uses C's scoped-count primitive. **F before G**: G moves tier out of the filter F rewrote.
- **H1 last**: consumes `mintAgentToken` (D), the generated tier tables (A), the skew fixture (F), and would fail on US1.5/1.8/3.x until C/F/G land. A–G ship standalone tests on the Phase-1 fixtures; nothing depends on H1 (D14).

## Parallel Execution Examples

- Within each PR, every `[P]` failing-test task runs concurrently (different files); implementation tasks are sequential per PR.
- B, D, E, H0 run as four worktree-isolated sessions after A merges; each carries its own astra rounds under the shared `/private/tmp/claude-501/opencode.lock` mutex (sequential reviews, parallel coding).
- H1's T109/T110/T112 are parallel with T108.

## Implementation Strategy

- **MVP = A + C + F + G** (the four roadmap P1 tasks: exact identity, scoped retrieve, publication identity, refusal shapes). B/D/E/H0 are P1/P2 leaves that can merge in any order once A is in; H1 is the proof.
- Never regenerate a golden outside H0. Never touch `isToolCallable`/`indexedToolVisible` semantics (quarantine-blind search predicate). Every scoped computation short-circuits `authCtx == nil || authCtx.IsAdmin()` with nil profile scope.
- Each PR body carries: gap ids closed, tests inverted (before → after), admin controls kept, research decision ids applied, astra model + final `VERDICT:` line, docs links as docs.mcpproxy.app URLs.

## Task Count

121 tasks: Setup 3 · A 17 · B 14 · D 13 · E 15 · H0 7 · C 11 · F 15 · G 14 · H1 12 (T086a, T104a, T112a added in round 1; T054a in round 2; T108a in round 3). Astra rounds 1–5 applied 2026-09-14. `[~]` process steps: 13 (excluded from the ratio). Failing-test tasks: 49 (one per gap id, FR008-G5≡FR010-G5 counted once, plus the protocol-level proof and the FR-010(3) regression). Astra plan-review round 1 (14 findings) applied 2026-09-14.

## Follow-ups (not in this spec)

- [ ] **REST replay tool gate** — `POST /api/v1/tool-calls/{id}/replay` dispatches with no tool gate, identity resolution or target-tier check (`internal/runtime/runtime.go` ~`:1350-1437` calls `client.CallTool` directly; `internal/httpapi/server.go` ~`:4978-4992` only checks `canSeeServer`). A read-only scoped token can replay a recorded destructive call; a pending/changed/disabled/config-denied/record-less tool re-executes. Fix in a separate spec/PR by routing replay through `resolveExactToolIdentity` + `evaluateExactToolGate` + `tierForAnnotations`/`HasPermission` (or through `handleCallToolVariant` with the recorded variant). Details: gap-map.md §8. Found by the PR A round-3 review (finding 3); pre-existing on `main`.

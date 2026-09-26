# Tasks: Agent-Token Scope Hardening

**Input**: [plan.md](plan.md) · [research.md](research.md) D1–D11 · [data-model.md](data-model.md) · [contracts/](contracts/) · [gap-map.md](gap-map.md) §1 (gap ids), §4 (building blocks), §7 (tests to invert)
**Organization**: one phase per PR in the plan's merge order `A → (B ∥ D ∥ E ∥ H0) → C → F → G → H1`. Every phase: failing tests first (one per gap id) → implementation → inverted pinned tests → verification → astra round. Story labels map FRs to spec user stories: **US1** = FR-001…FR-007, FR-010, FR-012 (scoped token cannot learn about other servers); **US2** = FR-009 (cannot execute above tier); **US3** = FR-008 (publication identity).

**Format**: `- [ ] [ID] [P?] [Story] Description with file path`. `- [~]` = process step with no committed artifact (excluded from the completion ratio).

**Common verification (every PR, from quickstart.md)**: `go test -race -count=1 -skip 'E2E|Binary|MCPProtocol|TestInfoEndpoint|TestGracefulShutdownNoPanic|TestSocketInfoEndpoint' ./internal/server/...` + touched packages · frozen goldens unchanged: `git diff --stat origin/main -- internal/server/testdata/*.golden.json internal/server/testdata/toolslist_goldens` empty (H0 excepted, two description strings only; H1 may ADD `internal/server/testdata/scope_latency/` but must not touch the goldens) · `/opt/homebrew/bin/golangci-lint run --config .github/.golangci.yml ./...` · `./scripts/test-api-e2e.sh` · astra review via `.review-tmp/run-astra.sh` (≤10 rounds, `VERDICT:` line required, `permission requested` count 0).

---

## Phase 1: Setup (shared across PRs)

- [x] T001 Add `agentCtx(allowed []string, perms []string, pin string) context.Context` and `adminCtx()` helpers with the `["*"]`-means-unrestricted rule documented, in `internal/server/scope_fixture_test.go` (new; consumed by every PR's tests; empty `AllowedServers` = deny-all)
- [x] T002 [P] Add `startCountingUpstream(t, tools ...toolSpec) (*countingUpstream)` generalising `startCountingTargetTierUpstream` (`internal/server/mcp_call_tool_target_tier_test.go:335-411`) so any test can assert zero upstream calls, in `internal/server/scope_fixture_test.go`
- [x] T003 [P] Amend spec text per plan §"Spec text amendments" (FR-008 empty-raw-name analogue; FR-003/004 D1 pinned zero-reach decision; SC-007 restated as the gate on FR-005/008/010 + inverted-assertion list; FR-009 unresolved identity refused for every caller per Edge Case `spec.md:107`; FR-002 anonymous callers; SC-005 two new named administrator exceptions — FR-005 profile-scoped admin on shared-index fallback (D3) and FR-001 caller-kind-first (D5)) in `specs/105-agent-scope-hardening/spec.md`

---

## Phase 2: PR A — `scope-target-identity-producers` (FR-009, gaps FR009-G1…G7)

**Goal**: exact raw-name identity is produced (approval records, index docs) and consumed (gate, callability, preflight); unresolved identity refuses scoped callers; no-record under active quarantine is pending.
**Independent test**: discover `[erase, ns:erase]` on a manual-trust server with `erase` approved → `ns:erase` is `pending` under its own name and `call_tool_read a:ns:erase` from a full-tier `a`-only token is refused with zero upstream calls.

### Failing tests (write first, confirm RED against the merge base)

- [x] T004 [US2] FR009-G1: `setupQuarantineRuntime` manual trust, baseline `erase` approved, discover `[erase, ns:erase]` → expect `BlockedTools["ns:erase"]` and a pending record keyed `ns:erase`; full-tier `a`-only `call_tool_read a:ns:erase` → `TOOL_QUARANTINED`, zero upstream — `internal/runtime/tool_quarantine_identity_test.go` (new) + `internal/server/mcp_call_tool_target_tier_test.go`
- [x] T005 [P] [US2] FR009-G2: `applyDifferentialToolUpdate` with `[erase, ns:erase]` → `GetToolsByServer` length 2; `SaveToolApproval(ns:erase, Disabled)` survives a rerun (HEAD deletes it) — `internal/runtime/lifecycle_identity_test.go` (new) + `internal/index/bleve_rawname_test.go` (new: distinct docIDs)
- [x] T006 [P] [US2] FR009-G3: seed `{a, erase, pending}`, delete `ns:erase` record; full-tier ctx: `makeDirectModeHandler` entry `ns:erase` → IsError + zero upstream; `preflightApprovalReader.ToolApproval` → non-nil pending — `internal/server/mcp_direct_callability_test.go` + `internal/server/preflight_glue_test.go`
- [x] T007 [P] [US2] FR009-G4: counting upstream `[erase]`; full-tier `a`-only AND admin `call_tool_read a:ghost` → `Permission denied`, zero upstream (D4: every caller; SC-005 named exception); sandbox `call_tool('a','ghost')` → `PERMISSION_DENIED` envelope; unknown-server branch unchanged (control) — `internal/server/mcp_call_tool_target_tier_test.go` + `internal/server/mcp_code_execution_scope_test.go` (new)
- [x] T008 [P] [US2] FR009-G5: manual-trust server, quarantine on, StateView has `ns:erase`, storage has no record → refused; only `{a, erase, approved}` seeded → `a:ns:erase` refused; `quarantine_enabled=false` → unchanged — `internal/server/tool_gate_test.go`
- [x] T009 [P] [US2] FR009-G6: generated tables at the spec shape (`spec.md:132`): retrieve **54 cells** = 3 permission sets × 3 target tiers × 3 `call_tool_*` variants × strict-intent on/off, pre-classified allowed / insufficient-permission / intent-mismatch; plus direct (permission set × target tier, driven through the registered handler in A — G's T104a re-drives the same table through `HandleMessage`, D15) and nested (via real sandbox, envelope asserted) tables; paired-name rows (`erase` approved, `ns:erase` config-denied via `disabled_tools` / unapproved); `{read,destructive}` rows; `auth.AdminContext()` control rows; counting oracle on every cell — `internal/server/scope_target_tier_matrix_test.go` (new)
- [x] T010 [P] [US2] FR009-G7: regression cell `{read,destructive}` × write target refused on all three paths (passes today; pins the exact-match rule) — `internal/server/scope_target_tier_matrix_test.go`

### Implementation

- [x] T011 [US2] Add `RawName` to `config.ToolMetadata` with `CanonicalToolName`/`RawToolName` helpers in `internal/config/tool_identity.go` (new) and populate it from upstream tool names in `internal/upstream/core/client.go:394-397`
- [x] T012 [US2] Producers use `RawName`: `checkToolApprovals` keys records by raw name (`internal/runtime/tool_quarantine.go:443-616,1216-1229`), `applyDifferentialToolUpdate`/`newToolsMap` stop collapsing and stop deleting exact-name records (`internal/runtime/lifecycle.go:673-790,975-980`); legacy collapsed record approves only its own raw name (one-shot, documented in code comment)
- [x] T013 [US2] Bleve docID = `server:` + raw name (`internal/index/bleve.go:158-183,356-372,433`), `RawName` derived on read from the docID (not the stored `tool_name` / `full_tool_name`, which stay byte-identical scored fields — SC-005). **No index rebuild trigger** (adversarial review, 2026-09-14): the raw-keyed differential update self-heals a pre-upgrade collapsed doc on the first discovery (`TestApplyDifferentialToolUpdate_HealsPreUpgradeCollapsedDoc`), so a global wipe gated on the storage schema counter was pure cost and fired on every start
- [x] T014 [US2] Reader: `lookupToolApproval` exact-wins + no-record-under-active-gate ⇒ pending (`internal/server/tool_gate.go:96-227`); direct callability and preflight read through it (`internal/server/mcp_direct_callability.go:136,192-201,253`, `internal/server/preflight_glue.go:400-411`); `ClassifyTool` consults the gate before returning Ready on nil approval (`internal/preflight/classify.go:98-100`)
- [x] T015 [US2] Unresolved identity on a known server refuses **every** caller on retrieve (`internal/server/mcp.go:2283-2323`) and nested (`internal/server/mcp_code_execution.go:1251-1268` → explicit unresolved sentinel in the `ToolAnnotationFunc` contract, `internal/jsruntime/runtime.go:401-409` refuses on it); the unknown-server branch (`mcp_code_execution.go:987-997`) is untouched (D4); seed StateView in fixtures that register an upstream without one (`mcp_call_tool_trim_test.go:57`)
- [x] T016 [US2] Docs: "Target tool tier" paragraph (exact-match permissions, annotation-less → read, unresolved → refused for scoped callers) replacing the "destructive implies both" claim in `docs/features/agent-tokens.md:140-150`

### Inverted pinned tests (gap-map §7)

- [x] T017 [US2] Invert `internal/server/mcp_call_tool_target_tier_test.go:210-223,244-253,474` (ghost tool now refused for scoped callers); rebuild `internal/server/mcp_routing_test.go:306-447` direct cells on the real fixture with the counting oracle; resolve `extractToolName` callers in the three test files that still use it

### Verification

- [ ] T018 [US2] Run the common verification set plus `go test -race ./internal/runtime/... ./internal/index/... ./internal/preflight/... ./internal/jsruntime/...`; `git diff --stat -- internal/server/testdata` empty
- [~] T019 [US2] Live check: real daemon (quickstart §Live check) with a stdio server exposing `erase` + `ns:erase`; approve `erase` only; scoped full-tier token call to `a:ns:erase` refused; record output in PR body
- [~] T020 [US2] Astra review rounds (≤10) on the PR diff against FR-009 + gaps FR009-G1…G7; quote the final `VERDICT:` line in the PR body

---

## Phase 3: PR B — `scope-cache-legacy-invalidation` (FR-001/002, gaps FR001-G1…G7) — parallel with D, E, H0

**Goal**: legacy/internal cache entries refused for every caller (legacy also invalidated durably); child pages inherit the parent's provenance; agent refusals non-disclosing on MCP and REST.
**Independent test**: a record written without `producer` is refused for admin and agent, absent after bbolt reopen; a registry key is refused for admin via `read_cache` yet still `Peek`-able.

### Failing tests

- [x] T021 [US1] FR001-G1: store legacy (nil producer) record → every reader kind incl. admin/anonymous gets `ErrUnauthorizedRead`; `Peek` false; close+reopen bbolt → absent; server-level admin `handleReadCache` → IsError without `records` — `internal/cache/manager_legacy_test.go` (new) + `internal/server/mcp_read_cache_authz_test.go`
- [x] T022 [P] [US1] FR001-G2: store `registry-servers:…` and `npm:…` keys via the runtime/guesser writers → admin `readCacheAs` IsError, no payload; agent live-vs-absent key identical text; entry still `Peek`-able (no eviction) — `internal/server/mcp_read_cache_authz_test.go`
- [x] T023 [P] [US1] FR001-G3: store, rewrite `ExpiresAt` past, `Get` errs, `Peek` false, on-disk count == in-memory count — `internal/cache/manager_test.go` (`TestExpiredRecords` extended to assert absence)
- [x] T024 [P] [US1] FR001-G4: agent W `["*"]` produces K1; admin pages K1 → K2; W reads K2 → success — `internal/server/mcp_read_cache_authz_test.go`
- [x] T025 [P] [US1] FR001-G5: narrow ctx `[weather]`; live broad key vs nonexistent → identical text on `handleReadCache` and `CallToolDirect`; `cache key not found` substring kept — `internal/server/mcp_read_cache_authz_test.go` + `internal/server/mcp_call_tool_direct_test.go`
- [x] T026 [P] [US1] FR001-G6: table — admin reader qualifies for ANY snapshot regardless of its profile (unscoped, narrower, wider, empty, deleted profile → true); agent reader vs admin-produced snapshot → false; pinned wildcard agent vs unpinned agent entry → false; empty-profile agent reader → false (deny-all guard for agents only) — `internal/cache/authorization_test.go` (D5 / FR-001 `spec.md:124`)
- [x] T027 [P] [US1] FR001-G7: upgrade fixture (raw JSON record without `producer` and with `version:0`), fresh-internal-entry, recursive-child on MCP + REST, pinned-token REST (direct dispatch refused AND `read_cache` refused with nonexistent-key body), held-call narrowing (passes; regression) — `internal/server/scope_cache_fixtures_test.go` (new)

### Implementation

- [x] T028 [US1] `Record.Version` + `Authorization.Kind = internal`; `GetRecordsAs`: nil/unknown version → refuse + delete inside the committed `Update` (tx fn returns nil; refusal surfaced outside), stats mutate on commit only; internal → refuse without eviction; **caller kind first** — admin reader qualifies for any snapshot, agent never for an admin snapshot, deny-all guard applies to agent readers only; rename `Unrestricted()` → `IsAdministrator()` (D2, D5) — `internal/cache/models.go:26-43`, `internal/cache/manager.go:108-245`, `internal/cache/authorization.go:52-95`
- [x] T029 [P] [US1] Stamp internal writers `CallerKindInternal` in `internal/runtime/runtime.go:2226` and `internal/experiments/guesser.go:349`
- [x] T030 [US1] `ReadCacheResponse.Producer` (`json:"-"`) carries the parent's authorization to child page stores; `handleReadCache` collapses unauthorized/not-found/expired into the `cache key not found` body for agent callers (real BBolt errors distinct; activity log keeps the real reason) — `internal/server/mcp.go:5613-5690`, `internal/server/cache_authz.go:54-86`, `internal/server/content_forward.go:256`

### Inverted pinned tests

- [x] T031 [US1] Invert `internal/cache/authorization_test.go:65-72,93-121,123-154`, `internal/server/mcp_read_cache_authz_test.go:86,171`, `internal/server/mcp_call_tool_direct_test.go:35-48` (seed stamped records instead of unstamped)

### Verification

- [x] T032 [US1] Common verification + `go test -race ./internal/cache/... ./internal/runtime/... ./internal/experiments/...`; `-tags server` build to `/dev/null`
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

- [x] T041 [US1] `profileMiddleware` evaluates the selectable-profile rule for the requested slug ONLY (`profileIndex.selectable` over a per-snapshot slug index with precomputed per-profile reach bitsets, keyed on `auth.IsScopedCaller`; never `selectableProfileNames`, whose cost is fleet-sized — codex round 2; reach costs the same for a missing, deleted or 4 096-server candidate — codex round 3; reach is O(|token grant|), one membership test per `allowed_servers` entry against the candidate's precomputed set, never a walk of the configured servers — codex round 4; the index is warmed at construction and on every config event, not by the first request — codex round 3) for `/mcp/p/<slug>`, `/mcp/p`, `/mcp/p/`; ONE refusal constructor (`profileNotSelectable(w)`) for missing/deleted/not-selectable/pin-mismatch/no-profiles/zero-reach; no-profiles branch moved after the gate; admin/anonymous branches unchanged; every scoped refusal logs one operator-facing line (`profile URL refused for scoped caller`: agent_name, profile, remote_addr — critique round 1, S1) — `internal/server/server.go`
- [x] T042 [US1] `handleSetProfile`: admission decides the requested slug alone through the same per-snapshot index (`profileIndex.selectable`, never the selectable list); a scoped caller's refusal is the list-free `unknown profile '<slug>'`, administrators keep the `available:` list (codex round 3); pin branch requires reach (D1); for scoped callers `servers` = effective scope after the update via `resolveActiveProfileIn` (pin > URL > session, same config snapshot as the admission check) ∩ token, rendered in profile-declared order — on a URL-scoped endpoint that is the URL profile, not the stored selection; `active_profile` = stored selection; cleared pinned selection reports `active_profile == ""`. Administrators short-circuit to the pre-105 payload (selected profile's servers / all servers on clear) — SC-005 names no FR-003 exception, so the URL-precedence reporting is agent-only (critique round 1, A1/A2) — `internal/server/profile_tool.go`
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

- [x] T048 [US1] FR007-G1: `internal/logs` writers `a/b`, `a_b`; sentinel in `a/b`; attributed tail(`a_b`, 50) → no sentinel; a child stderr line `left | right | {"server":"a_b"}` written through the REAL stderr path (`monitoring.go:214`, `zap.String("message", line)`) by `a/b` is attributed to `a/b` and never to `a_b`; same cases under the JSON encoder (`logger.go:152-155`); server-level differential with/without `a/b`; admin `tail_log` bytes unchanged vs pre-feature capture — `internal/logs/logger_attributed_test.go` (new) + `internal/server/mcp_tail_log_scope_test.go`
- [x] T049 [P] [US1] FR007-G2: `O_APPEND` an unstamped `LEGACY_PLAIN_LINE`; attributed read excludes it; admin whole-file read includes it; historical subject-evidence records (`spec.md:130`): a pre-upgrade `Removing existing container` record stamped `server=a` with `container_name=mcpproxy-a-b-wxyz` (shape of `docker.go:555-558`), the same-sanitised-name case — `server=a/b` with `container_name=mcpproxy-a-b-wxyz` and no `container_owner` (indistinguishable from hidden `a-b`'s container) — an ID-only `container_id` record, and a callback-stop record stamped `server=a` naming server `b`'s port — all withheld from the scoped reader, all present for admin; a post-upgrade record with `container_owner=a/b` IS returned to the `a/b` reader — `internal/logs/logger_attributed_test.go`
- [x] T050 [P] [US1] FR007-G3: own1/foreign1/own2/foreign2 interleaved; attributed tail(`a_b`, 2) == `[own1, own2]`; server-level `lines_returned == 2` — `internal/logs/logger_attributed_test.go` + `internal/server/mcp_tail_log_scope_test.go`
- [x] T051 [P] [US1] FR007-G4: two observer loggers, start callback servers `a`,`b`, `StopCallbackServer("a")` → stop record only in `a`'s observer, both start orders — `internal/oauth/callback_stop_logger_test.go` (new)
- [x] T052 [P] [US1] FR007-G5: fake docker (`SetWellKnownDockerPathsForTest` + `ResetDockerPathCacheForTest`) `ps` → `deadbeef1234 mcpproxy-a-b-wxyz` (label `a-b`) + `mcpproxy-a-wxyz` (label `a`); server `a` connect/disconnect → foreign never rm/stop/kill'd nor logged; own removed; SECOND fixture: no owned container, empty known container ID, one foreign container on the SAME image → the image-name fallback (`docker.go:243-248,296-329`) touches nothing; unit matcher table `a` vs `a-b` vs `a/b` vs `A` — `internal/upstream/core/docker_ownership_test.go` (new)
- [x] T053 [P] [US1] FR007-G6: forced-rotation shared-history and case-only (`A`/`a`, branch on FS case sensitivity) fixtures; admin outcomes recorded — `internal/logs/logger_attributed_test.go`

### Implementation

- [x] T054 [US1] **No new field** (D8): keep the existing `server=<raw>` zap field at `internal/logs/logger.go:385`; add `ReadUpstreamServerLogTailAttributed(name, n)`: console encoder → scan ` | {` boundaries left to right and accept the first whose suffix decodes as exactly one complete JSON object (no trailing bytes); JSON encoder → whole line; match `server` exactly AND apply the subject-evidence rule (a container record is attributable only with `container_owner` == requested server; withhold every container record lacking it and every callback record naming another server — D8 rule 3), filter before taking the last *n*, withhold lines with no accepted boundary; whole-file reader untouched (admin records byte-identical) — `internal/logs/logger.go:321-547`
- [x] T054a [P] [US1] Producer audit test: every `upstreamLogger.{Info,Warn,Error,Debug}(` call in `internal/upstream/core` passes a constant message literal (child-controlled text only as field values) — `internal/upstream/core/upstream_logger_audit_test.go` (new)
- [x] T055 [US1] `handleTailLog` uses the attributed reader for scoped callers, whole-file for admins; `lines_returned` = filtered length — `internal/server/mcp.go:5787-5803`
- [x] T056 [P] [US1] OAuth: `stopCallbackServerLocked` logs through the recorded `server.logger`; `StopCallbackServer` no longer calls `adoptLoggerLocked` on stop (nil-logger signature kept) — `internal/oauth/config.go:1686-1735`
- [x] T057 [P] [US1] Docker: `ensureNoExistingContainers`, the disconnect name-pattern fallback AND the image-name fallback all filter by label `com.mcpproxy.server=<raw>` AND `^mcpproxy-<san>-[a-z0-9]{4}$`; foreign matches neither logged nor removed; every housekeeping record that names a container adds `zap.String("container_owner", <label value>)` (D8 rule 3, SC-005 housekeeping-record change) — `internal/upstream/core/docker.go:243-248,296-329,349-431,501-582`

### Inverted pinned tests

- [x] T058 [US1] Rewrite `internal/server/mcp_tail_log_scope_test.go:63-64,110-117` and `internal/server/mcp_secret_redaction_test.go:288` to use stamped writers (unstamped canary is now withheld); keep `TestTailLog_AdminUnchanged`; close returned `io.Closer`s in fixtures

### Verification

- [x] T059 [US1] Common verification + `go test -race ./internal/logs/... ./internal/oauth/... ./internal/upstream/core/...`
- [~] T060 [US1] Live check: daemon with servers `a/b` and `a_b`; scoped `tail_log a_b` shows no `a/b` lines
- [~] T061 [US1] Astra rounds on FR-007 + FR007-G1…G6 + D8/D9; quote final `VERDICT:`

---

## Phase 6: PR H0 — `scope-regression-suite` part 1: FR-012 (gaps FR01x-G1…G3) — parallel with B, D, E

**Goal**: stored-script not-found no longer enumerates for agent callers; descriptions stop advertising discovery-by-failed-call; docs carry the invariant.
**Independent test**: agent `["*"]` calling `code_execution script=gamma` with `alpha-SENTINEL.js` present gets an error containing neither `SENTINEL` nor `Available scripts`; admin still enumerates.

### Failing tests

- [x] T062 [US1] FR01x-G1: agent ctx `["*"]`, script `gamma`, `alpha-SENTINEL.js` present → IsError, no `SENTINEL`/`Available scripts`, byte-equal to empty-dir proxy; admin control kept; positive controls per `spec.md:116`: `a`-only token runs a stored script returning a constant (no upstream call) and gets the constant; a stored script calling `b` is refused at the nested call; scoped initialization publishes custom `instructions` mentioning `b:private_search` (documented) — `internal/server/mcp_code_scripts_test.go` + `internal/server/mcp_instructions_scope_test.go` (new)
- [x] T063 [P] [US1] FR01x-G2: `TestCodeExecutionDescriptions_EnumerationIsAdminOnly` asserting `code_execution.description` and `script.description` no longer advertise enumeration, and a golden-delta assertion that only those two strings changed — `internal/server/toolslist_snapshot_test.go`

### Implementation

- [x] T064 [US1] Caller-kind branch: enumeration only for non-scoped callers — `internal/server/mcp_code_execution.go:473-499` (or `internal/codescripts/codescripts.go:338-356` with a caller flag). Critique r1: the scoped form (`codescripts.ResolveScoped`) never lists the directory and strips host paths / OS errors from the ambiguous and unusable refusals too; `GET /api/v1/code/scripts` is gated with `requireAdminRead` (403 for agent tokens)
- [x] T065 [US1] Reword `internal/server/mcp_code_execution.go:52-53,73-74`; regenerate goldens with `MCPPROXY_WRITE_TOOLSLIST_GOLDENS=testdata/toolslist_goldens go test -run TestToolsListSnapshot ./internal/server/` (the variable is the OUTPUT DIRECTORY, `toolslist_snapshot_test.go:151-158`), then rerun with it unset; diff limited to `internal/server/testdata/toolslist_goldens/{default_server,retrieve_tools_mode,code_execution_mode}.json`
- [x] T066 [P] [US1] Docs: enumeration is admin-only in `docs/code_execution/overview.md:379-387`, `cookbook.md:141`, `troubleshooting.md:604-613`, `api-reference.md:591`; add invariant sentence, covered-surface list (`/mcp`, `/mcp/all`, `/mcp/code`, `/mcp/call`, `/mcp/p/<slug>`, aliases), retained-effects list and custom-instructions/stored-scripts secrets warning to `docs/features/agent-tokens.md` (FR01x-G3)

### Verification

- [x] T067 [US1] Common verification; `git diff --stat -- internal/server/testdata` shows only the three live goldens plus their deliberately frozen pre-105 copies (`toolslist_goldens/pre105/*.json`, byte-identical to the merge base — the baseline the golden-delta assertion diffs against)
- [~] T068 [US1] Astra rounds on FR-012 + FR01x-G1…G3; quote final `VERDICT:`

---

## Phase 7: PR C — `scope-retrieve-tools` (FR-005, gaps FR005-G1…G5) — after A

**Goal**: `retrieve_tools` computes search, `total`, indexed counts, usage ranking and session risk over the authorized population only; admin output byte-identical.
**Independent test**: `a`-only token, `{query:"rotate keys", limit:1}` with a higher-scoring hidden `b` tool → `[a:rotate_keys]`; admin → the `b` tool.

### Failing tests

- [x] T069 [US1] FR005-G1: index `a:rotate_keys` + two `b` tools with repeated query terms; `a`-only `{query:"rotate keys", limit:1}` → `[a:rotate_keys]`, `total 1`; `["*"]` control → `b` tool; profile-scoped admin on the shared-index fallback → `[a:rotate_keys]` (D3, SC-005 named exception); unprofiled admin → `b` tool unchanged; `TestSearchScoped_ScoreIdenticalToExhaustiveFilter`: ordered `(id, score)` from `SearchScoped` == exhaustive `Search(Size=docCount)` filtered then cut, on the 527-tool snapshot and multi-term fixtures (D6) — `internal/server/mcp_retrieve_scope_test.go` (new) + `internal/index/manager_scoped_test.go` (new)
- [x] T070 [P] [US1] FR005-G2: seed `IncrementToolUsage` `b:secret_sentinel_tool`×3, `a:echo`×1, `a:removed_tool`×1 (unindexed AND carrying a stale *approved* record, so it can only be excluded by population membership), `a:pending_tool`×2 (indexed but pending approval); `a`-only `include_stats` → `top_tools == [{a:echo,1}]` (current authorized population ∧ approved — `spec.md:128`); admin unchanged; `TestRetrieveToolsFullMode_GoldenByteIdentity/include_stats` still green — `internal/server/mcp_retrieve_scope_test.go`
- [x] T071 [P] [US1] FR005-G3: `buildMCPProxyWithActivation`; StateView `a` (read-only) + `b` (nil annotations); `a`-only → `session_risk.level == low`, `lethal_trifecta == false`; delete `b` → deep-equal; admin keeps `high` — `internal/server/mcp_session_risk_test.go`
- [x] T072 [P] [US1] FR005-G4: index `a:t1, b:t2, b:t3`; `a`-only `{debug:true}` → `total_indexed_tools == 1`; admin 3; profile-index path also 1 — `internal/server/mcp_retrieve_scope_test.go`
- [x] T073 [P] [US1] FR005-G5: `TestRetrieveTools_ScopeOracle` table-driven over three-server vs `a`-only fixtures with `include_stats+debug+session_risk` (US1.5) — `internal/server/mcp_retrieve_scope_test.go`

### Implementation

- [x] T074 [US1] `index.Manager.SearchScoped(query, limit, servers)` = conjunction(text query, boost-0 `server_name` disjunction) with exhaustive-then-filter as the ONLY fallback if the equality test fails (D6); `ScopedDocumentCount(servers)` via `server_name` facet terms — `internal/index/manager.go:105-143`, `internal/index/bleve.go:274,458-486`
- [x] T075 [US1] Retrieve handler: route scoped callers (`auth.IsScopedCaller`) and profile-scoped administrators on the shared-index fallback through `SearchScoped` with the effective set from `serverInScope`/`resolveActiveProfile`; unprofiled administrators keep `Search` + post-filter byte-for-byte (D3); `debug.total_indexed_tools` from `ScopedDocumentCount`; `usage_summary.top_tools` filtered by current authorized-population membership (`lookupIndexedTool` / StateView presence) **and** approval (exact-tool callability predicate) then cut to 10 — both are required: callability alone never checks that the tool still exists (`mcp_direct_callability.go:168-229`), membership alone admits pending tools; ordinary discovery stays quarantine-blind (admin path untouched: `GetToolStats(10)`, non-nil `[]`) — `internal/server/mcp.go:1705-2043`, `internal/server/mcp_visibility.go` (`scopedIndexedToolCount`), `internal/storage/manager.go:424-456` (unlimited/filterable stats)
- [x] T076 [US1] `analyzeSessionRiskScoped(snapshot, serverDiscoverable)`; admin keeps `analyzeSessionRisk` — `internal/server/mcp_annotations.go:29-40,98-104`, call site `internal/server/mcp.go:2007-2016`

### Verification

- [x] T077 [US1] Common verification; `TestFilterDiagnostics_WindowUsesNormalizedLimit`, `TestSurfaceIsolation_RetrieveTools*`, `TestRetrieveTools_TruncatedPayloadReadableViaReadCache` and all four `retrieve_full_*` goldens green; `isToolCallable` untouched (quarantine-blind)
- [x] T078 [US1] FR-011 pre-check: `scope_latency_test.go` prototype on the 527-tool snapshot (`loadDeferredLargeCorpus`) for `retrieve_tools`, scoped-vs-admin ≤ 20 ms, skipped under `-race`; local merge-base p95 table in the PR body — `internal/server/scope_latency_test.go` (new; H1 extends to four operations + CI job)
- [x] T079 [US1] Cross-model review (opencode quota exhausted on both Terra and Sol — confirmed live before each round, per CLAUDE.md fallback rule; fell back to `codex exec --model gpt-5.6-sol`) on FR-005 + FR005-G1…G5 + D3/D6, 2 rounds. Round 1: 2 MUST-FIX (SearchToolsScoped silently skipped SearchTools' underscore-segment enhancement; ScopedDocumentCount's facet was capped at a fixed 10000 terms) — both fixed (shared `augmentedToolSearchQuery` helper; facet sized to `DocCount()`, a proven exact bound) with a new regression test. Round 2: `VERDICT: CLEAN`, no MUST-FIX.

---

## Phase 8: PR F — `scope-direct-publication` (FR-008, gaps FR008-G1…G7) — after A

**Goal**: every direct-surface definition is authorized by the identity of the publication that produced it, at every seam, both skew directions, full and deferred; built-ins positively identified; no-identity definitions withheld from everyone.
**Independent test**: `rebuildPaused` origin flip `{a, b__c}` → `{a__b, c}`; `a`-only `tools/list` during the pause never returns `a__b__c`'s definition, in full and deferred modes.

### Failing tests

- [x] T080 [US3] FR008-G1: `newSkewFixture`, old `{a, b__c, S1}` → new `{a__b, c, S2}`; `a`-only `listed()` during pause → `a__b__c` absent, no `[a__b]` description, schema `S1`; both modes; `*` + admin controls — `internal/server/mcp_direct_skew_test.go`, `internal/server/mcp_direct_publication_identity_test.go`
- [x] T081 [P] [US3] FR008-G2: `rebuildPaused` adding `__a__review` / `b__` with sentinel → not listed for `a`-only during seam; positive-identification test: `describe_tool` is Builtin via the explicit set, `__x__y` and a registry tool named `retrieve_tools` are NOT — `internal/server/mcp_direct_catalog_publish_test.go` + `internal/server/mcp_direct_catalog_test.go` + `internal/server/mcp_direct_publication_identity_test.go`
- [x] T082 [P] [US3] FR008-G3: reverse flip `a__b → a` and plain addition `b__x`; `a`-only / `b`-only token sees the tool during the window — `internal/server/mcp_direct_publication_identity_test.go`
- [x] T083 [P] [US3] FR008-G4: `rebuildPaused` ReadOnly → Destructive; `{read}` token not listed during seam; handler IsError `Permission denied`; full + deferred — `internal/server/mcp_direct_skew_test.go` (full) + `internal/server/mcp_direct_publication_identity_test.go` (deferred)
- [x] T084 [P] [US3] FR008-G5: inside `rebuildPaused`, `tools/call a__b__c` through `directServer.HandleMessage` as `a`-only → the WHOLE JSON-RPC envelope (code `-32602`, message, data) is byte-equal to the envelope for the same requested name `a__b__c` in the fixture where `a__b` is absent (the caller-supplied name is echoed in both); assert no owner metadata (`server`, `owner`, canonical id) appears anywhere in the envelope (D12) — `internal/server/mcp_direct_publication_identity_test.go`
- [x] T085 [P] [US3] FR008-G6: `__a` server fixture — steady state listed/describable/dispatchable for `*` + admin, withheld + `-32602` parity for `a`-only; seam variant via `rebuildPaused`; both modes — `internal/server/mcp_direct_underscore_test.go` (new)
- [x] T086 [P] [US3] FR008-G7 + FR-006 consumer: `buildDirectCatalog([{a, ""}]).Len() == 0`; filter omits `a__` for admin and agent; prompt aggregation drops empty prompt names; an UNSTAMPED prompt (no accepted registration) is withheld from `prompts/list` and refused by `prompts/get` for admin AND agent (admin outcome recorded, SC-005) — `internal/server/mcp_direct_catalog_test.go` + `internal/server/mcp_prompt_scope_test.go`
- [x] T086a [P] [US3] Protocol-level proof: `tools/list` + `tools/call` through `directServer.HandleMessage` show (a) no stamp on the wire for admin and agent, (b) the scope filter is re-evaluated at `tools/call` (hidden registered tool → `-32602`) — the mcp-go behaviour the plan relies on (D7) — `internal/server/mcp_direct_protocol_test.go` (new)

### Implementation

- [x] T087 [US3] `directToolStamp{Owner, RawName, Tier}` private struct written into `mcp.Tool.Meta` by `renderDirectTools`; `builtinDirectToolNames` populated from the built-in constructors (`buildDescribeToolTool().Name` etc.) at `internal/server/mcp_routing.go:143-148,156-311`
- [x] T088 [US3] Scope filter and callability filter read the stamp first (neither removes it), consult the catalog only for unstamped entries, withhold unstamped non-built-ins; a TERMINAL third `WithToolFilter` (`stripDirectToolStamp`, registered last) removes the stamp for EVERY caller (remove the admin early-return at `internal/server/mcp_direct_scope.go:48-50`); `filterAggregatedPromptsForAuth` withholds unstamped prompts regardless of `enforce` and `prompts/get` refuses them (FR-006) — `internal/server/mcp_direct_scope.go:40-143,185-212`, `internal/server/mcp_direct_callability.go:49-92`, `internal/server/mcp_routing.go:958-961`
- [x] T089 [US3] Catalog: remove parse-failure → Builtin inference; withhold empty raw name; tier from the producing entry — `internal/server/mcp_direct_catalog.go:151-224,374-423`; direct handler in-seam scope refusal returns the error mcp-go maps to the SAME `-32602` envelope as an unregistered name (replace the `NewToolResultError` branches at `internal/server/mcp_routing.go:423-440`), never naming the captured owner (D12)
- [x] T090 [P] [US3] Prompt aggregation drops empty prompt names (`internal/server/mcp_routing.go:1322-1326`) and `prompts/get` refuses them (via `filterAggregatedPromptsForAuth`'s unconditional stamp check, `internal/server/mcp_direct_scope.go`, which mcp-go re-evaluates on `prompts/get` too)

### Inverted pinned tests

- [x] T091 [US3] Invert `internal/server/mcp_direct_skew_test.go:195-224,316-332,459-489`, `internal/server/mcp_direct_catalog_publish_test.go:65-101,155`, `internal/server/mcp_direct_catalog_test.go:177-235`; `direct_full_prefeature.golden.json` and `TestDirectModes_SetIdentity`/`toolSetFingerprint` stay byte-exact. Also inverted, discovered red by the full suite run and not in the original enumeration above: `internal/server/mcp_routing_test.go` (`TestDirectModeHandler_ServerAccessDenied`, `TestFilterDirectModeToolsForAuth_KeepsNonDirectTools` → renamed `TestFilterDirectModeToolsForAuth_DropsNonBuiltinSeparatorlessNames`), `internal/server/profile_pin_enforcement_test.go` (`TestDirectModeHonorsTokenProfilePin`), `internal/server/mcp_prompt_scope_test.go` (`TestFilterAggregatedPromptsForAuth_UnstampedFailsClosed`) — all pinned the D12 disclosure or the `enforce`-gated prompt withholding this PR closes.

### Verification

- [x] T092 [US3] Common verification; Spec 102 SC-007 listing/describe parity tests green — `go build ./...`, `go vet ./...` clean; `go test ./internal/server/...` (full package, no `-skip` needed beyond the standard CI regex) green, `-race` green; `golangci-lint` (bare + `--build-tags server`) clean on every touched file
- [x] T093 [US3] Cross-model review — opencode (`gpt-5.6-terra`, then `gpt-5.6-sol`) reported quota exhaustion on both models (confirmed, not assumed); fell back to `codex exec -m gpt-5.6-sol --sandbox read-only` per the CLAUDE.md ladder. 3 rounds: round 1 found 1 SHOULD-FIX (an mcp-go `SessionWithTools` interaction with the unstamped-tool fallback path — verified unreachable in production via repo-wide grep, since mcpproxy-go registers no session-specific tools anywhere; documented as a residual with a code comment rather than a behavioral change, which round 2 confirmed as accurate and acceptable) + 2 NITs (stale doc comments describing pre-PR-F behavior); round 2 found 1 more NIT (a comment mixing up which cross-generation residual is the SC-007-forbidden direction); round 3: `VERDICT: clean`.

---

## Phase 9: PR G — `scope-refusal-shapes` (FR-010, gaps FR010-G1…G7) — after C and F

**Goal**: scope-first refusal precedence with hidden ≡ nonexistent on every surface; tier denials on authorized servers reach the handler; suggestions and alias resolution computed over the authorized corpus.
**Independent test**: `a`-only token, `call_tool_read a:t` when `a` has no live client → error text identical between the `{a,b,a__b}` and `{a}` fixtures.

### Failing tests

- [x] T094 [US1] FR010-G1: fixture A `a` (no client) + `b`,`a__b` added after `PhaseReady`; `a`-only `call_tool_read a:t` → text identical across fixtures, no sentinel (order-insensitive) — `internal/server/mcp_auth_scope_test.go`
- [x] T095 [P] [US1] FR010-G2: index `B:read` (+ hidden `b:read` in A); token `[B]`; `describe_tool b:read` definition → equal responses with `B:read` suggestion; `describe_plain_corpus_test.go` bytes unchanged — `internal/server/mcp_describe_tool_scope_test.go` (new)
- [x] T096 [P] [US1] FR010-G3: catalog A `{x:y:z sentinel, x__y:z}` vs B `{x__y:z}`; token `[x__y]`; `describe_tool x__y:z` definition + check → identical, no sentinel; admin `not_found` control (`TestDescribeDirect_DisplayAndCanonicalNamespaceOverlap`) green — `internal/server/mcp_describe_direct_test.go`
- [x] T097 [P] [US1] FR010-G4: catalog A hidden `B:read` + authorized `b:Read`; token `[b]`; `describe_tool b:read` → suggestion `b:Read` in both fixtures — `internal/server/mcp_describe_direct_test.go`
- [x] T098 [P] [US1] FR010-G6: real proxy; ctx `[a]`,`{read}`; `p.directServer.HandleMessage` `tools/call a__write_tool` → isError `Permission denied … 'write'`, zero upstream (HEAD `-32602`) — `internal/server/mcp_direct_scope_test.go`
- [x] T099 [P] [US1] FR010-G7: token `{[a], pin P={a,b}}`; `call_tool_read b:t` vs `zzz:t` → identical text; sandbox `call_tool('b')` vs `('zzz')` → identical envelope — `internal/server/mcp_auth_scope_test.go` + `internal/server/mcp_code_execution_scope_test.go`

### Implementation

- [x] T100 [US1] `Available servers:` filtered through `serverInScope` for scoped callers — `internal/server/mcp.go:2473-2495`
- [x] T101 [US1] Effective set = profile ∩ token evaluated once, one body for agent callers on `call_tool_*` (`internal/server/mcp.go:2262-2293`), sandbox allow-list (`internal/server/mcp_code_execution.go:1198-1229`) and nested refusal (`internal/jsruntime/runtime.go:383-410`); admins on `/mcp/p` keep today's text
- [x] T102 [US1] describe_tool definition mode: `toolVisibleToSession` evaluates scope before index presence (`internal/server/mcp_visibility.go:51-72`); not-found + case-correction through `visibleCorpus.notFoundResult` over the authorized corpus (`internal/server/mcp_describe_tool.go:123-160`); direct case-correction `continue` on invisible match (`internal/server/mcp_describe_direct.go:133-155`)
- [x] T103 [US1] Shadow canonical map in the direct catalog so an authorized canonical id resolves even when a hidden display entry collides — `internal/server/mcp_direct_catalog.go:227-268`, `internal/server/mcp_describe_direct.go:54-103`
- [x] T104 [US1] Call-time `WithToolFilter` evaluates scope → tier → callability in spec order (D13): hidden → `-32602`; over-tier on an authorized server → passed through to the handler (insufficient-permission); in-scope within-tier but disabled/quarantined/pending/changed → `-32602` unchanged; `tools/list` predicate still withholds over-tier and non-callable — `internal/server/mcp_direct_scope.go:115-143`, `internal/server/mcp_routing.go:958-961`
- [x] T104a [P] [US1] FR010 precedence regression through `HandleMessage`: disabled/quarantined/pending/changed alone → `-32602` unchanged; over-tier alone → insufficient-permission; over-tier + pending → insufficient-permission (tier-first, `spec.md:133`); re-drive the generated FR-009 direct table from T009 through `HandleMessage` (D15) — `internal/server/mcp_direct_scope_test.go`

### Verification

- [x] T105 [US1] Common verification; `mcp_auth_scope_test.go:84` updated; admin controls green
- [x] T106 [US1] Cross-model review on FR-010 + FR010-G1…G7 (codex exec, gpt-5.6-sol — opencode terra/sol/astra quota-exhausted, confirmed). 4 rounds: round 1 (3 MUST-FIX: jsruntime `authInfo != nil` misread as agent-only, indexed/direct describe_tool profile-scoped-admin regressions), round 2 (3 MUST-FIX: `AuthTypeUser` misclassified as admin across the direct-mode scope predicates, canonical-shadow predicate missing callability, a vacuous test assertion), round 3 (1 MUST-FIX: the `AuthTypeUser` fix's regression tests didn't exercise the G3/G4 branches they claimed to), round 4: `VERDICT: clean`. A related but out-of-FR-010's-mandate instance of the same `AuthTypeUser` pattern (prompt filtering, direct-mode callability hiding) was deliberately left unfixed per round-3's confirmation that the callability-hiding split is intentional design, not a bug, and flagged as a separate follow-up task.

---

## Phase 10: PR H1 — `scope-regression-suite` part 2 (FR-011/013/014, SC-007, gaps FR01x-G4…G7) — last

**Goal**: the two-fixture differential oracle, HTTP credential matrix and latency test exist, cover every user-story scenario by id, and prove no pinned-reversal test survived.
**Independent test**: `TestScopeCoverage_EveryUserStoryScenario` passes only when US1.1–1.8, US2.1–2.6, US3.1–3.4 are all registered.

- [x] T107 [US1] `newScopeFixture(t, full)`, `runScopeScenario(t, usID, fn)`, `normalizeScopeResponse` (nondeterministic fields only), per-fixture retrieve-oracle derivation (`Search(Size=docCount)` filtered then cut) asserted for every SC-001-excluded field, coverage registry + `TestScopeCoverage_EveryUserStoryScenario` per contracts/differential-oracle.md — `internal/server/scope_differential_test.go` (new)
- [x] T108 [US1] Register every US1.x scenario (retrieve oracle incl. `include_stats+debug+session_risk`, describe, call_tool refusal, read_cache, set_profile, profile URL, tail_log, stored scripts, prompts) against both fixtures — `internal/server/scope_differential_test.go`
- [x] T108a [P] [US1] Retained-effect mode `runRetainedEffectScenario` + one named fixture per SC-001 exclusion (`spec.md:114,161`): shared-limiter contention (global capacity 1, no queue, held call on hidden `b`, call to `a` → "proxy-wide limit saturated"), cross-server scan admission (real discovery — `startCountingUpstream` baseline pass then `up.serve`+`runRuntimeDiscovery` pass 2 — same-name, near-duplicate-description tool on hidden `b` under `trust_mode: scan` → `a`'s addition genuinely held PENDING via the shadowing.cross_server impersonation-clone check; verified non-vacuous by swapping in a dissimilar description and confirming the test goes red), prompt-name collision, global prompt cap (six hidden servers × 200 prompts each, so the fixture actually crosses the 1000-prompt aggregate ceiling rather than being clamped by the 200-per-server cap first), direct display-name collision, shared prompt-refresh deadline (named fixture at `internal/upstream/manager_prompts_deadline_test.go` — `TestManager_ListPrompts_SharedNotPerServerDeadline`, an artificially-short-deadline reproduction of the same sequential-shared-context mechanism the 30s production path uses, since the 30s deadline itself is hardcoded and not test-injectable), shared log rotation/retention (cited, cross-package) — each asserting authorized ownership of everything returned, no hidden content/sentinel, and the documented outcome — `internal/server/scope_retained_effects_test.go` (new). Cross-model review round 1 caught that the first versions of the cross-server-scan-admission and global-prompt-cap fixtures were vacuous (never created the actual shadowing condition; never crossed the aggregate cap) and that the prompt-refresh-deadline effect had only a comment, not a fixture — all three rebuilt for real. Round 2 caught two more: the deadline fixture (then one slow + one fast server) didn't distinguish a SHARED deadline from N independent per-server deadlines — rebuilt with four slow servers (enough that whichever is visited first alone exhausts the shared budget) asserting at-most-one-server's-prompts-survive and total elapsed stays near ONE deadline regardless of server count, never ~4x; and global-prompt-cap's sentinel check inspected prompt NAMES while the sentinel lives only in DESCRIPTIONS (vacuous) — fixed to fetch "a"'s own prompt content through its registered handler (the real `authorize()` path) when it survives the cap and check the sentinel is absent from the served text. Round 3: the single-regime deadline fixture (60ms budget vs 300ms delay) still could not distinguish shared-sequential from N independent-but-CONCURRENTLY-raced per-server deadlines of the same size (both designs bound elapsed to ~one deadline and admit only the fast server in that parameter regime) — added a SECOND, complementary fixture, `TestManager_ListPrompts_SharedBudgetCannotFitAllServersEvenWithGenerousDeadline`: five 100ms-delay servers under a single 150ms budget (fits ~1 server, not all 5) — an independent-concurrent design would let all 5 succeed in parallel (~100ms), an independent-sequential design would eventually seat all 5 (~500ms); only shared-sequential seats ~1. Verified non-vacuous (widened to 2s, got 5/5, reverted) and non-flaky (10x `-count=10`, consistently 1/5, ~151ms). Also strengthened `TestScopeHTTPMatrix_RetrieveToolsNonDisclosureOverRealHTTP` (new): a raw wire-text substring check over real HTTP proving hidden deploy-srv's tool identifiers never leak into a scoped token's retrieve_tools response (query deliberately chosen to match deploy-srv's content), with an unrelated positive-control query proving the same HTTP path returns real authorized content.
- [x] T109 [P] [US2] Register US2.1–2.6 (retrieve 54-cell + direct + nested tables from T009, nested envelope through the real runtime, unresolved identity for every caller) — `internal/server/scope_differential_test.go`
- [x] T110 [P] [US3] Register US3.1–3.4 (origin flip, unparseable names, tier change, refusal text) reusing `newSkewFixture` — `internal/server/scope_differential_test.go`
- [x] T111 [US1] HTTP credential matrix: real tokens minted via `mintProfileAgentToken` (profile_integration_test.go, T035 — `mintAgentToken` was already taken by an unrelated Spec 107 helper of a different signature, so H1 reuses the actual generalised minter under its real name) × surfaces `{/mcp, /mcp/all, /mcp/code, /mcp/call, /mcp/p/<slug>}` × operations; `yes` cells run through real HTTP with session continuity, `n/a` cells assert the actual JSON-RPC-level `-32602 tool ... not found` envelope (not any application-level `isError:true`, which a registered tool's own handler can also return for bad arguments — round-1 finding, fixed) against a definitely-nonexistent-name positive control; no `E2E` in the name; one env per fixture — `internal/server/scope_http_matrix_test.go` (new). Round 2: expanded to cover every FR-014 row at least once — prompts list/get on all three surfaces, describe_tool (yes on retrieve+direct, n/a confirmed on code), tail_log via the explicit operation (not just upstream_servers' default `list`), and the three call_tool_* execution-tier variants on retrieve. This expansion's tail_log cell FOUND A REAL BUG: `internal/logs.ReadUpstreamServerLogTail`/`ReadUpstreamServerLogTailAttributed` dereferenced a nil `*config.LogConfig` (a caller building `config.Config{}` directly rather than through `config.DefaultConfig()`, which always sets `Logging`) — mcp-go's panic recovery turned it into a confusing `-32603` instead of crashing, but it was a genuine defect affecting BOTH admin and scoped readers identically (not a scope/security issue, a robustness gap). Fixed with a TDD red/green pair (`internal/logs/nil_log_config_test.go`) plus the minimal nil-guard in both functions. Remaining scope note: still not every operation × surface × alias cell exhaustively (stored-script resolution and nested code-execution dispatch are proven in-process by mcp_code_scripts_test.go / scope_target_tier_matrix_test.go's nested table, not re-driven over HTTP here) — the FR-005/008/010 differential fixtures in scope_differential_test.go prove the underlying non-disclosure behavior in-process; this file's job is proving the SAME auth/session mechanics hold over real HTTP for every row at least once.
- [x] T112 [P] [US1] Finalise `scope_latency_test.go` (T078) over `retrieve_tools`, `read_cache` (frozen 10-page entry), `prompts/list` (frozen 50-prompt set), `tools/list`: 20 warm-up + 200 timed per caller, `p95(scoped) − p95(admin) ≤ 20ms` per operation, `-race` skip — `internal/server/scope_latency_test.go` + `internal/server/testdata/scope_latency/README.md` (the fixtures are generated deterministically in the test file itself, not as separate serialized files — see the README)
- [x] T112a [P] [US1] Merge-base gate: `.github/workflows/scope-latency.yml` (non-race, `ubuntu-latest` — no dedicated "reference runner" available to this repo) checks out merge-base into a second directory, copies head's `scope_latency_test.go` + `testdata/scope_latency/` over it (test-only, backward compatible: the test compiles against the pre-feature API — `scope_latency_test.go` was made deliberately self-contained, with its OWN local `connectLatencyPromptUpstream` helper, after round-1 review caught that an earlier version called `connectPromptUpstream` from `scope_retained_effects_test.go`, a file that does not exist at merge-base and so would have failed the copied file's compile), runs both in one job, fails on administrator p95 regression > max(10%, 5 ms) per operation AND fails if either side yields no measurement for any operation; comparison via new `cmd/scope-latency-compare` (Go, stdlib only, no new dependency, D10; unit-tested). `continue-on-error: true` — not a required check yet, per the CLAUDE.md flakiness lesson from PRs F/G. The full merge-base mechanism (archive checkout, file copy, build, run, produce results) was verified locally end-to-end against this PR's actual merge-base commit (75cfc27b7) before relying on the CI job to prove it.
- [x] T113 [US1] SC-007 inventory table (US id → test name → `-run` pattern) in the file header of `internal/server/scope_differential_test.go`; `grep` guard test (`TestScopePinnedReversalsStayInverted`, `internal/server/scope_pinned_reversal_guard_test.go`) that none of the four pinned-reversal assertions gap-map.md's FR01x-G7 entry names still exist in their original form
- [x] T114 [P] [US1] Docs: final invariant wording + retained-effects list reconciled with what shipped in `docs/features/agent-tokens.md`
- [x] T115 Common verification across all touched packages; roadmap: `scope-refusal-shapes` was still `status: todo` despite PR G (#1328) having merged — flipped to `done` with its PR number; the other seven `scope-*` tasks were already `done`; `scope-regression-suite` (this PR) intentionally left `status: todo` until merge; regenerated `ROADMAP.md` (`python3 scripts/gen-roadmap.py`); `python3 scripts/gen-roadmap.py --check-github` clean
- [x] T116 Cross-model review on FR-011/013/014 + FR01x-G4…G7 (opencode terra/sol/astra confirmed quota-exhausted live before round 1; fell back to `codex exec --model gpt-5.6-sol --sandbox read-only`, stdin closed, `gtimeout`-wrapped, brief split into a file the CLI reads itself). 4 rounds, all genuine findings verified against production/test code before fixing (no false-positive acceptances):
  - Round 1 (7 MUST-FIX/SHOULD-FIX across 8 review areas): US1.5's retrieve-oracle scenario was vacuous (no hidden-influencing usage data, byte-compared ranking fields the contract excludes) — fixed with real usage seeding + `stripRankingDependentFields`; the pinned-reversal guard was trivially bypassed — hardened with string-concat collapsing, whitespace-tolerant regex, and a structural (not text-label) table-row parse; `CrossServerScanAdmission` and `GlobalPromptCap` retained-effect fixtures were vacuous (never created the real shadowing condition; never crossed the aggregate cap) — rebuilt against the actual `shadowing.cross_server` impersonation-clone mechanism and a 6-hidden-server-×-200-prompt shape that genuinely exceeds `maxAggregatedPrompts`; the HTTP matrix's `unregisteredToolIsNA` accepted any application-level `isError` as proof of non-registration — fixed to require the actual `-32602` envelope; the `tools/list` latency fixture measured an empty direct catalog — fixed to publish the real 527-tool corpus through connected upstreams; the CI workflow's merge-base checkout couldn't compile the copied test file — fixed by making `scope_latency_test.go` fully self-contained; docs overclaimed "every FR has a three-server differential-oracle test" — reworded. Every fix verified non-vacuous by temporarily reverting the underlying guard/logic and confirming the test goes red (git diff clean afterward in every case).
  - Round 2 (2 MUST-FIX, 1 SHOULD-FIX): the shared-deadline fixture (1 slow + 1 fast server) didn't distinguish shared from independent-per-server deadlines — rebuilt with 4 slow servers; the HTTP matrix's new rows only proved tool *registration*, not FR-014's matrix — expanded to cover prompts/describe_tool/tail_log/execution-tier explicitly; this expansion's tail_log cell **found and fixed a genuine pre-existing nil-pointer panic** in `internal/logs.ReadUpstreamServerLogTail(Attributed)` (nil `*config.LogConfig` dereference, affecting admin and scoped readers identically — a robustness gap, not a scope bug), fixed with a TDD red→green pair; `GlobalPromptCap`'s sentinel check inspected prompt names while the sentinel lives only in descriptions — fixed to check served content through the real registered handler.
  - Round 3 (2 MUST-FIX, 1 NIT): the round-2 deadline fixture (60ms budget vs 300ms delay) still couldn't distinguish shared-sequential from independent-CONCURRENT per-server deadlines in that parameter regime — added a second, complementary fixture (5×100ms servers, one 150ms shared budget) whose parameters specifically defeat that alternative; the HTTP matrix still lacked real content-level non-disclosure proof over HTTP — added a raw-wire-text substring check on `retrieve_tools` with a query deliberately matching hidden content, plus an unrelated positive control; gofmt NIT fixed.
  - Round 4: both prior MUST-FIXes confirmed resolved ("the complementary 150ms/100ms fixture distinguishes shared-sequential behavior from both independent-concurrent and independent-sequential deadlines"; "the real-HTTP retrieve_tools test provides content-level non-disclosure through authentication middleware... further per-row content assertions have insufficient marginal value"). One NIT (a stale comment in the deadline test file, written before round 3's second fixture existed, overclaiming what the first fixture alone rules out) — fixed.
  - **`VERDICT: clean`** (round 4, quoted in full): *"Both prior MUST-FIX findings are resolved: The complementary 150 ms/100 ms fixture distinguishes shared-sequential behavior from both independent-concurrent and independent-sequential deadlines. The real-HTTP retrieve_tools test provides content-level non-disclosure through authentication middleware, with an authorized positive control. Combined with the differential suite and existing direct/profile HTTP checks, further per-row content assertions have insufficient marginal value."*
  - Full `-race` suite (internal/server, internal/upstream, internal/cache, internal/logs, cmd/scope-latency-compare) green after every round's fixes; one pre-existing, unrelated flaky allocation-count test (`TestProfileMiddleware_RefusalWorkIndependentOfFleet`, a PR-D test this PR does not touch) failed once under concurrent-session load on the shared build host and passed cleanly in isolation immediately after — not attributable to this PR. `go build`/`go vet` (bare + `-tags server`) clean throughout; `golangci-lint --new-from-rev=origin/main` (bare + `--build-tags server`) 0 issues at every checkpoint; `gofmt -l` clean on every touched/new file.

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

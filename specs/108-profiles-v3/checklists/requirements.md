# Specification Quality Checklist: Profiles v3 — Scoped Profiles, Client Binding and Scope-Aware Observability

**Purpose**: Validate specification completeness and quality before proceeding to implementation
**Created**: 2026-09-25
**Feature**: [spec.md](../spec.md)

## Content Quality

- [x] No implementation details in user stories and success criteria — surfaces, tools and fields are named; packages and types appear only in plan/data-model/contracts
- [x] Focused on user value (read-only really read-only; bind a client; answer "what did X do" and "why can't X")
- [x] Written for maintainers and reviewers; Definitions section binds every term
- [x] All mandatory sections completed (stories, edge cases, FRs, key entities, success criteria, commit conventions)

## Requirement Completeness

- [x] No [NEEDS CLARIFICATION] markers remain — every open choice is decided in research.md D1–D28
- [x] Requirements are testable and unambiguous — enforcement matrix and refusal texts are exact
- [x] Success criteria are measurable (SC-001…SC-010)
- [x] All six audit acceptance checks appear as acceptance scenarios (US1-1, US1-2 + US3-1, US1-3, US2-2, US2-4/5, US3-2)
- [x] Edge cases identified (dangling pins, stale classification, concurrent reassignment, expiry, pre-upgrade clients, REST use of client credentials, edition split)
- [x] Scope clearly bounded (excluded S1/O1/S3 and M1–M7 remainder; prompts tierless; stdio unscoped)
- [x] Dependencies and assumptions identified (Spec 057/105, PR #1323 seam, Spec 107 expiry cap, connect carriers)
- [x] Backward compatibility specified (legacy profiles, legacy pinned tokens, legacy activity records, deprecated `/profiles/active`)

## Cross-surface parity

- [x] Every capability has a row in the parity matrix with Web UI, macOS, CLI, MCP and REST columns
- [x] Every "—" cell carries a reason
- [x] One terminology table binds names across config, REST, MCP, CLI and UI labels
- [x] Shared enums + golden fixtures consumed by Go, vitest and Swift (FR-052)
- [x] Findings P1–P7 each map to FRs and PRs (P1: FR-039/044/048; P2: FR-034–037/040–041/047; P3: FR-001–005/010–015; P4: FR-021–025; P5: FR-020/029/033 + UI source labels; P6: FR-029–033/045; P7: FR-021/034 tokens/043/049)

## Feature Readiness

- [x] All functional requirements have acceptance coverage in stories or the enforcement matrix
- [x] Each PR (108-a…108-l) has its own test plan and live-verification recipe (tasks.md + quickstart.md §3)
- [x] Security defaults fail closed (unannotated deny, allow never widens, client credentials MCP-only, no admin key in client configs, anonymous confinement, `profiles` tool admin-kinds only)

## Notes

- Validation pass 1 (2026-09-25): all items pass.
- Cross-model review round 1 (zcode / GLM-5.3, read-only plan mode, 2026-09-25): verdict FINDINGS, 8 findings (1 high), all applied: stored `set_profile` selection re-validated per request and cleared on reassignment (FR-020/FR-026, high); `management_tools: true` never widens a credential and client credentials get a bounded `upstream_servers` op set (FR-016); "All servers" switchable credential keeps legacy switching (FR-022); anonymous tier covers the unrecognised-token branch (FR-008, D13a); refusal precedence equals the explainer chain; rename/delete rewrite `switchable_to`/`anonymous_profile` (D19); management-tool narrowing on reconnect stated (D5); `locked` requires a pin (FR-021).
- Cross-model review round 2 (zcode, 2026-09-25): verdict FINDINGS, 14 findings (0 high), all applied: bulk move, Settings `anonymous_profile`, macOS Dashboard/Servers filters now have tasks (T098, T098a, T116, T116a, T119a); dependency graph matches plan (k parallel after f); MCP `explain` gains `anonymous`; `client_name` and `profile anonymous` added to FR-031/FR-036; bulk `mode` on CLI/MCP; `--expires-in`; FR-030 change enum aligned; templates moved out of scope; tri-state wording in FR-001; golden paths corrected; 108-e surfaces annotated.
- Cross-model review round 3 (zcode, 2026-09-25): confirmed all round 1–2 fixes; verdict FINDINGS, 4 medium, all applied: Rename in Web UI/macOS editors (FR-041/047, T091/T115, recipe); `profile_change` type + binding-change records land in 108-c (T040/T032), 108-e adds filters (T063); FR-031 `token` limited to activity/sessions, `/servers` takes `profile` only; anonymous callers confined by `anonymous_profile` never keep legacy management tools (FR-016, D14, matrix row, T047).
- Cross-model review round 4 (zcode, 2026-09-25): confirmed all round-3 fixes; one medium (a 108-c test asserted management-tool hiding that lands in 108-d) — moved the assertion from T033 to T047. No other findings.
- Cross-model review round 5 (zcode, 2026-09-25): round-4 fix confirmed; full HIGH-severity scan (scope widening, PR self-testability) found nothing. **VERDICT: CLEAN.** 5 of the 10-round cap used.
- Independent review round 1 (2026-09-25, after the zcode CLEAN verdict): 15 findings (4 high, 7 medium, 4 low), each verified against `origin/main` code, **15 applied, 0 rejected**:
  - (high) FR-016 no longer grants client credentials `add`/`add_from_registry`/`patch`/`enable`/`disable`/`restart`/`refresh`. `patch` replaces a stdio server's `command`/`args` and `restart` re-launches it, so the grant was host code execution. Client credentials now keep the Spec 028 `AuthorizeServerOp` policy (`list`/`tail_log` only); T037a is now test-only. Also D14, D5 notice, mcp-tools table, enforcement matrix, T042, T047, quickstart 108-d.
  - (high) FR-020: a dangling switchable binding or `anonymous_profile` now resolves deny-all, the same as a dangling pin, and never falls through to `none`. Also data-model §4, refusals, matrix row, T029.
  - (high) Rename and `reassign_to` rewrite `profile_pin` on every token and client credential in one bbolt tx. Also US4-3, D19, data-model §3, T067, T072, quickstart 108-f.
  - (high) 108-d owns FR-013–016 and 018. The `profiles` rows and column move to 108-h T086, and T047/T048 exclude them. Also plan table, Phase 5 header, matrix note.
  - (medium) FR-027 profile edit/rename/delete now sends `list_changed`, triggered by a per-snapshot diff that also covers hand edits. New tasks T067a and T072a, plus mcp-tools Notifications and D10.
  - (medium) The bypass warning now covers switchable bindings too. It is renamed `binding_bypassable_without_auth`, with doctor check `profiles.binding_bypass`. Also US2-7, US5-4, D13, data-model §7, REST/CLI contracts, T083, T098a, T116a.
  - (medium) FR-022 / US2-4: naming the caller's own pin or bound profile is not a switch (keeps `selectable()` `slug == pin`, SC-003). Also D6, matrix row, T029.
  - (medium) FR-035 and data-model §7 now agree on a top-level `fixes[]` with a `fix actions` enum (FR-052, T011, T094).
  - (medium) `PUT /clients/{id}/binding` `mode` is optional: omitted keeps the current mode, and `profile:""` becomes switchable. Also the CLI `client set-profile` and MCP `assign` docs, T074, T086.
  - (medium) FR-021 client-token name collisions: the mint replaces the `kind=client` record, including a revoked one, because soft-revoked records keep reserving the name (`storage.CreateAgentToken`). A grandfathered `kind=agent` `client-<id>` token gets a `409` plus the `client_token_name_conflict` warning and is never touched. Also D8, data-model §3/§7, REST/CLI contracts, edge cases, T028, T031.
  - (low) The Web UI `anonymous_profile` Settings picker is now required by FR-042, so parity row 23 can be traced.
  - (low) FR-030 `change=anonymous`: `PATCH /config` routes `anonymous_profile` through `profiles_service.SetAnonymous`. Also rest-api, D13, T067, T072, T074.
  - (low) US1-4 names its fixture (`servers: [github, notion]`).
  - (low) FR-019 is now pinned by the prompts regression test T018a in 108-b.
- Independent review round 2 (2026-09-25): 16 findings (1 high, 8 medium/low-medium, 7 low), each verified against `origin/main` code, **16 applied, 0 rejected** (one applied in a different PR than suggested — see below):
  - (high) FR-016: a caller confined by `anonymous_profile` to a `management_tools: true` profile now gets exactly the client-credential `upstream_servers` op set (`list`/`tail_log`). Verified: `auth.AnonymousContext()` is `AuthTypeAdmin` (`internal/auth/context.go`) and `AuthorizeServerOp` exempts admin contexts from the whole denylist (`internal/auth/server_ops.go`), so without the clause a keyless process could `patch` + `restart`. Also D14, data-model §3, mcp-tools table, matrix row, refusals row, T047, new T052a, quickstart 108-d.
  - (medium) FR-015 "every dispatch path": REST `POST /api/v1/tools/call` (`handleCallTool` → `CallToolDirect`, verified at `internal/httpapi/server.go` / `internal/server/mcp.go`) bypasses `WithToolFilter`; the gates now live in the shared handlers, profile refusals map to `403`, hidden tools answer the route's unknown-tool error. New enforcement-matrix column, mcp-tools/rest-api sections, refusals rows, plan mechanism + risks, T052, new T046a, quickstart 108-d.
  - (medium) SC-002 now exempts tools admitted by a `tools.allow` rule that no deny rule matches (FR-010 step 3 order kept).
  - (medium) US2-2 asserts `profile_source=pin` (locked client credential, FR-020 tier 1, now stated explicitly); D25 records why client credentials are not special-cased to `binding`. T029.
  - (medium) `IsLegacy()` is defined over exactly the six policy fields; `title`/`description` never make a profile non-legacy. Definitions, data-model §1, mcp-tools, rest-api `is_legacy`, T004, T016.
  - (medium) Binding/anonymous discovery rows now have an owning task: **T044a in 108-d**, not 108-c as suggested — 108-b and 108-c merge in parallel, so 108-c cannot extend 108-b's test files; 108-d is the first PR with both. T016 text and the Dependencies note updated.
  - (low-medium) plan.md marks FR-044 transitional in 108-i and completed in 108-j (T107 switches the chip to `useScopeQuery`); phase headers updated.
  - (low-medium) plan.md 108-e scope says `profile_change` **filters** (the type lands in 108-c T040).
  - (low-medium) `GET /clients?profile=&client=` and `GET /tokens?profile=&token=` are server-side REST filters (current binding/pin); url-filter `client` row lists Clients; CLI `client list --profile`, `token list --profile`; MCP `list_clients {profile}`; parity rows 7/24; T068, T070, T074, T078, T086.
  - (low) FR-011: `hidden_by_profile` (+ `profile`) present — possibly `0` — for every non-legacy profile, absent for legacy/no profile; the `max_tier: destructive` edge case now says the tool **set** is identical to legacy, not the JSON; matrix `work-full` = present `0`.
  - (low) cli.md `doctor` `profiles.binding_bypass` carries the `anonymous_profile` qualifier and shares one condition function with the `GET /clients` warning (data-model §7, T083 test for the equal-narrowness case).
  - (low) `client_name` endpoint list is explicit: REST `GET /activity`, `/activity/export`; CLI `activity list|export|watch` (`watch` filters `/events` client-side). FR-031, FR-036, rest-api, cli, D11, T065.
  - (low) Matrix `/mcp/p/<slug>` cells for switchable and anonymous rows add "(or own base, FR-022)".
  - (low) Binding a client with `credential_state` ≠ `client` → `409 no_client_credential` on every surface, nothing minted (FR-025/FR-026 edge case); bulk moves report `skipped[]`; UIs disable the picker and offer the previewed upgrade. rest-api, refusals, cli, data-model §7, T032, T040, T092, T116, T117.
  - (low) `DELETE /profiles/{name}` of the `anonymous_profile` → distinct `409 profile_is_anonymous_profile` (`used_by.anonymous_profile: true`), not overridden by `force`. US4-3, D19, rest-api, cli, T067, T070.
  - (low) FR-009 pinned by a field-set reflection test in T004 (both build tags).
  - Incidental: US4-5 and the 108-d recipe now use the real `token create --name <n>` syntax (the CLI takes no positional name).
- Independent review round 3 (2026-09-25): 13 findings (6 medium, 7 low), each verified against `origin/main` code, **13 applied, 0 rejected**:
  - (medium) FR-032 "view as" access now uses the full enforcement chain (credential scope, FR-010, global gates, server state, tool approval), so `callable` equals a real call; `reason` = FR-010 reason or first failing explainer step. data-model §7 EffectiveTool, rest-api, T059.
  - (medium) US2-7 carries the US5-4 condition (`anonymous_profile` unset or wider); one shared condition function, no contradiction between the two scenarios.
  - (medium) Enforcement-matrix row `anonymous_profile=legacy`: `set_profile("legacy")` is the own base → `ok (own base, FR-022)`.
  - (medium) Unknown-tool parity on REST `/tools/call`: verified `internal/server/mcp.go` returns `fmt.Errorf("unknown tool: %s", toolName)`, and `handleCallTool` wraps it as `Failed to call tool: …`, so the body echoes the requested name. "Byte-identical" replaced by "same status and body shape from the same formatter, differing only in the echoed name and per-request identifiers" in FR-015, enforcement-matrix, mcp-tools, refusals, rest-api, quickstart, T046a (and T018's describe_tool comparison worded the same way).
  - (medium) FR-031 is split across 108-e (activity/sessions/tools/servers params) and 108-f (`/clients`, `/tokens` filters); FR-005's reporting surfaces are tagged on 108-f (effective-tools REST), 108-g (`profile show --effective`), 108-h (`effective_tools`), 108-i and 108-k (editors), with matching test clauses in T070, T078, T086, T091, T115.
  - (medium) T058 narrowed: `client_name` only on `/activity` and `/export`; `/summary`, `/usage`, `/sessions` reject it with `400` (decision: reject rather than ignore, so an aggregate is never silently unfiltered — matches cli.md "summary rejects it like the REST route"). FR-031, rest-api.
  - (low) Session base is not stored (it changes on reassign/rename); T072a derives it at notify time from `SessionInfo.TokenName` → current `profile_pin`, or `anonymous_profile` for tokenless sessions. data-model §6.
  - (low) ClientView gains `profile_source` (`pin` locked, `binding` switchable, empty without a client credential), backing the CLI `SOURCE` column. T070, T078.
  - (low) url-filter macOS section lists Dashboard usage summary and Servers (FR-049, parity rows 16/19).
  - (low) FR-046 client-row explainer: "Explain access…" row action in T092/T098 (Web UI) and T116 (macOS); link map rows for client row and blocked activity row "Why?" (T108); 108-j tagged FR-046 for the Tools/Activity entry points.
  - (low) FR-007 warning bucket lists rules/classifications naming a server outside `servers`; US4-4 separates fatal errors from warnings (which save the profile and ignore the entry). `switchable_to` naming an unknown profile also moved to the warning half, matching data-model §1.
  - (low) FR-011: a dangling base (FR-020 deny-all) always counts as non-legacy — `profile` = missing name, `hidden_by_profile: 0`. data-model §4, mcp-tools, T016, T044a.
- Independent review round 4 (2026-09-25): 10 findings (4 medium, 6 low), each verified against `origin/main` code, **10 applied, 0 rejected**, plus one same-class gap found while verifying:
  - (medium) FR-014/FR-015: `POST /api/v1/code/exec` dispatches `CallToolDirect("code_execution")` (verified `internal/httpapi/code_exec.go:182`), and `classifyCodeExecError` knew no profile refusal, so it would have answered `500 EXECUTION_FAILED`. The typed profile-hidden error maps to `403 PROFILE_BLOCKED` (the route's `CodeExecResponse` shape) and writes a `profile_code_execution` record. refusals row, rest-api/mcp-tools sections, enforcement-matrix REST column, plan, T046b, T052, quickstart 108-d, D26.
  - (found while verifying) `POST /api/v1/tool-calls/{id}/replay` calls the upstream client directly (`Server.ReplayToolCall` → `runtime.ReplayToolCall`), outside every built-in handler. It now runs the FR-010 decision before dispatch: `403` + refusals text + blocked record. FR-015, the same documents, T046b, T052.
  - (medium) FR-030 lifecycle records: T040 writes `rotate`/`forget` (and a mint as `assign` with empty `previous_profile`). T068 asserts exactly one record each, and none on a refused operation. FR-030 and data-model §5 name the writer.
  - (medium) macOS parity rows 11/20/24: T116 gains the "Other client…" sheet, and T118 the token list profile/token filters and the read-only legacy-scope display. New T119b generalises `AppState` to a `ScopeFilter` channel for the Clients-row, Profiles-card and Token-row links; the url-filter macOS section lists Token rows. T121 adds a UI capability walk (`parity-matrix.json` + vitest + `ParityMatrixTests.swift`), so an unimplemented ✓ cell fails CI.
  - (medium) FR-032 `reason`: follows the canonical chain order, and the enum is total (adds `server_in_scope`, plus `credential`/`profile`). The FR-010 reasons are reported by the `server_in_scope`/`tool_rule`/`tier_cap` steps. data-model §7, rest-api, cli `tools list`, T059.
  - (low) FR-032 authorization: `client=` view-as is admin-only on `GET /tools` and `effective-tools`. `profile=` follows profile reachability, and the caller's own scope filters rows first. rest-api, cli, T059.
  - (low) FR-007: the fatal bucket now includes the title > 80 / description > 500 length rule (data-model §1 already had it).
  - (low) US1 Independent Test fixture now sets `title: "Work · Read-only"`, the title scenario 2 quotes.
  - (low) FR-021 is binding for custom `client_id` (`^[a-z0-9][a-z0-9_-]{0,55}$`, not a supported-client id, `400 {error, field:"id"}`). FR-034, rest-api `POST /clients`, cli `client add`, data-model §3, D8, T068.
  - (low) `set_profile("")`: FR-018 states it is always admitted and falls through to the base. A new enforcement-matrix column exists for every caller row, and T029/T048 assert it.
  - (low) `/mcp/call`: the enforcement matrix explains the column, and T045 asserts on that mcp-go instance directly.
- Codex (gpt-6-sol) review round 1 (2026-09-25) + cross-spec ownership rule with Spec 109: 21 findings on Spec 108 (15 high, 6 medium), each verified against `638fa805a` code; **20 applied, 1 partially rejected** (provenance: the audit exists but is uncommitted — the citation now says so and the Context table is the traceable basis). Details and rejected alternatives: research D27.
  - (high) Binding guard FR-008a: API refusal `409 binding_bypassable_without_auth` + runtime anonymous deny-all (`anonymous_denied_by_binding_guard`) replace the advisory warning. US2-1/US2-7, US5-1/US5-4, data-model §4/§7, refusals, cli, enforcement matrix, T031, T033a, T040/T040a, T083, quickstart 108-c.
  - (high) Admin-key remediation FR-025: previewed bulk upgrade + admin-key rotation step; P4 claim narrowed; SC-008. rest-api, cli, T068, T074, T092, T116.
  - (high ×2) Fail-closed credential records + rollback: `mcp_cli_` secret prefix and per-authentication invariant check (FR-021). T027a, T030a, T034; edge cases.
  - (high) `locked ⇒ pin` scoped to `kind=client`; regular tokens have no mode. data-model §3, FR-020, D5, T027.
  - (high) Staged rotation FR-021a (pending secret, finalize, reconciler). data-model §3/§9, rest-api, cli, T028/T028a, T035.
  - (high) `GET /profiles` omission for every non-admin caller (FR-034, parity row 1, T070).
  - (high) Effective-tools / view-as: excluded rows admin-only; non-admin visible rows + counts (FR-032, data-model §7, rest-api, T059, T070, T104).
  - (high) Refusals never name the profile; `retrieve_tools.profile` only for url/session sources; `set_profile` reports only the caller's own selection (FR-011/013/014/018, refusals, mcp-tools, T016, T044, T048).
  - (high) Rollout gate FR-009a (`PolicyEnforcementReady` false until 108-d). T004a, T009, T055a, plan.
  - (high) Filter before limit via `SearchToolsAdmitted` (FR-011, plan mechanism 2, data-model §2, T016a, T022).
  - (high) T059 effective-tools sub-test moved to T070 (route lands in 108-f T074).
  - (high ×2) URL filter contract and ClientView vs Spec 109: this spec's url-filter-contract.md is now a pointer to Spec 109's; `ClientView` extends Spec 109's presence row additively (`kind` adds `custom`; `CONFIG PATH` kept); warning names shared.
  - (medium) `ToolTierGeneration` in the cache stamp (FR-027, T020, T024); `switchable_to` as `*[]string` (T004, T009); `profiles` tool registered outside `buildManagementTools()` (FR-017, mcp-tools, T086–T088); matrix fixture allow/deny overlap + out-of-servers allow rule (US1-4, T005); dangling-base canonical reason `profile` (refusals); `clientCtx` moved from T002 to T027b.
  - (ownership) Merged into Spec 109 as owner: `useScopeQuery` + link map (old 108-j T104/T106/T107 → Spec 109-k), Clients page shell and `GET /clients`/`client list|show` presence (→ Spec 109-h), macOS `ScopeFilter` (old T119b → Spec 109-k), sidebar placement (→ Spec 109-i). 108-j re-scoped to profile-specific UI; cross-spec edges f ← 109-h, i ← 109-h/109-i, j ← 109-k, k ← 109-h/109-k in plan.md and tasks.md.
- Codex (gpt-6-sol) cross-spec review round 2 (2026-09-25), Specs 108 + 109: 5 findings (1 high, 2 medium, 2 low), each verified against `638fa805a`; **5 applied, 0 rejected**. Details: research D28.
  - (high) FR-025a: `GET /connect`, `/connect/{client}`, `/connect/{client}/preview` become administrator-only in 108-c (they carry `credential_state`). US2-8, rest-api, data-model, T030b, T038, plan 108-c, quickstart 108-c.
  - (medium) Sessions REST mapping on macOS: T113 asserts `view=sessions` sends `profile`/`client`/`token` (contract fixed in Spec 109).
  - (medium) `--from/--to` removed from T065, plan 108-e and cli.md; Spec 109-k is the sole owner.
  - (low) Version skew: `400 unsupported_scope_filter` gate (Spec 109-k) fed by the one list 108-e fills; FR-031, rest-api, T058, T064, T070, T074, quickstart 108-e.
  - (low) Shared-file ordering: edge 108-k ← 109-i (+ T110a); "later merge keeps both" rules for `ClientStatus` (108-c ∥ 109-b) and the connect component (108-c ∥ 109-h, T042 covers both orders). plan, tasks Dependencies.
- Codex (gpt-6-sol) review round 3 (2026-09-25), Specs 108 + 109: 16 findings on Spec 108 (3 blocker, 9 major, 4 minor), each verified against `638fa805a`; **16 applied, 0 rejected** (108-i ← 109-k applied as a clarification: the edge already held transitively). Two propagations from Spec 109's round 3. Details: research D29.
  - (blocker) `mcp_cli_` in the `POST /clients` and connect-preview examples (rest-api; T031, T070 assert the literal prefix).
  - (blocker) FR-008a "wider" = reachable sets (incl. `switchable_to`) compared on servers, cap, unannotated handling, every admitted tool (`Decide`, deny/allow included) and capabilities; runtime guard re-evaluated per published pair. US2-1/US2-7, US5-4, data-model §4/§7, refusals (fix `target` only when P qualifies), cli doctor, enforcement matrix "wider" cases, T033a, T083, quickstart 108-f.
  - (blocker) SC-010 rollback guarantee only with `require_mcp_auth` on; downgrade precondition in docs/release notes (Edge Cases, plan, T124, T125).
  - (major) Confined anonymous: `auth.ScopedView` for every `IsAdmin()` branch in reachable handlers (tail_log reader + redaction, list filter, refusal shape). FR-016, data-model §3, T047, T052a.
  - (major) Replay: `server_not_in_profile` → existing non-disclosing `404`. FR-015, rest-api, refusals, enforcement matrix, T046b.
  - (major) `POST /config/apply`: FR-008a check + `profile_change` diff records. FR-008a, rest-api, T033a, T067, T074, quickstart 108-f.
  - (major) `SearchToolsAdmitted` annotation seam = `profile.EffectiveAnnotations`/`resolveExactToolIdentity`; no index-schema change. FR-011, data-model §2, plan, T013, T016a, T022.
  - (major) FR-015a REST discovery (`/index/search`, `/tools`, `/servers/{id}/tools` + export, diff) applies a pinned token's profile. US1-8, SC-002, rest-api, enforcement matrix, plan 108-d, T046c, T052b, quickstart 108-d.
  - (major) MCP `profiles` create/update take named `ProfileConfig` arguments; errors are the REST error body. mcp-tools, T086, T087.
  - (major) 108-i ← 109-k explicit (plan, tasks header and diagram).
  - (major) D8 one-tx replace scoped to revoked/expired records.
  - (minor) Server-edition per-user tokens cannot carry a profile yet (Editions, Edge Cases, D22).
  - (minor) Quickstart 108-c uses legacy profiles and only 108-c's operations; rotate/forget/warnings/`profile_source` checks moved to 108-e/f/g; T033a's route assertion moved to T070.
  - (minor) FR-030 names `actor_kind`/`actor_name`; T067/T068.
  - (minor) CLI enum values canonical `as_write`/`as_read` (kebab alias); bulk flags `--from-profile/--to-profile`. FR-001, terminology, parity row 9, cli, T078, T080.
  - (from Spec 109 r3) connect-read gate lands with whichever of 108-c/109-h merges first (FR-025a, rest-api, T030b, T038); quickstart §1 E2E gate runs only with no other core running until Spec 109-a T011a fixes the script's blanket `pkill`.
- Codex (gpt-6-sol) cross-spec review round 4 (2026-09-25), Specs 108 + 109: 7 findings on Spec 108 (2 high — one defect reported twice — 3 medium, 2 low) plus 3 propagated from Spec 109's round 4, each verified against `638fa805a`; **all applied, 0 rejected**. Details: research D30.
  - (high) Edge 108-a ← 109-a; `IntrinsicTier` = exhaustive adapter over `contracts.AnnotationTier` (int `profile.Tier`, string `contracts.Tier`, unknown → destructive); joint test T005a. Spec Tier definition, data-model §2, plan, tasks, quickstart 108-a.
  - (medium) T119a targets `HomeView.swift` (Spec 109-d rename); plan file list; quickstart 108-k.
  - (medium) Gate function travels with the list variable: whichever of 108-e / 109-k merges first creates both; T058 refuses `token` on `/tools`. FR-031, rest-api, pointer, T064.
  - (medium) 109-l ← 108-k (macOS ScopeFilter un-hiding). plan, tasks.
  - (low) `connect_cmd.go` / `ConnectClientView.swift` in the "later merge keeps both" list, pinned by `connect_profile_flags_test.go` + 109's goldens. plan, T041, T042, Dependencies.
  - (low) Pointer lists `activity watch` for `--from/--to`.
  - (propagated) `agent` gated except on `/activity` + `/export`; admin-key header on every recipe `curl`; `tool` URL value split into `server` + bare `tool` by Spec 109's `toRest()`.
- zcode (GLM-5.3) cross-spec review round 2 (2026-09-26): 3 high findings on Spec 108, all applied (research D32):
  - (high) FR-008a guard on `DELETE /profiles/{name}` (incl. `reassign_to`) and `POST /clients/upgrade-admin-key-holders` (`apply` + profile, whole request; preview `guard`), plus `POST /profiles`, rename and MCP `create`/`update`/`delete`/`rename`/`classify`. rest-api, refusals, cli, mcp-tools, T067, T068, T070, T070a (route coverage), T074, T080, T086, quickstart 108-f.
  - (high) FR-008a API refusal is `BindingGuardDelta` over whole candidate states, which covers narrowing/deleting/unlisting a member of P's `switchable_to` and classification changes. spec FR-008a/US2-7/US4-3, data-model §7, enforcement-matrix delta cases, T033a, T040, T072, plan.
  - (high) FR-009a override mechanism: `const` + `EnablePolicyForTest(tb)` gated by `testing.Testing()`, no env/config/flag/build tag; T004a AST + `go build` probe + built-binary exit-4 check; T055a deletes it. spec FR-009a, data-model §1, T002, T004a, T009, T055a, plan, quickstart 108-b/108-c.
- Spec commit omits the Claude co-author trailer per the repository constitution and the maintainer's standing rule for this repo.

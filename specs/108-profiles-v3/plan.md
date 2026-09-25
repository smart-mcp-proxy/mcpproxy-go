# Implementation Plan: Profiles v3 — Scoped Profiles, Client Binding and Scope-Aware Observability

**Branch**: `108-profiles-v3` | **Date**: 2026-09-25 | **Spec**: [spec.md](spec.md)
**Input**: [spec.md](spec.md) · [research.md](research.md) D1–D30 · Spec 109 (branch `109-ux-navigation-consistency`; ownership split, spec.md) · [data-model.md](data-model.md) · [contracts/](contracts/) · UX audit rev 2 (findings P1–P7, PV-A/B/C) · four surface inventories (Web UI, macOS, CLI, MCP/REST) at `638fa805a`

## Summary

Make the profile the single unit of scope. PV-A adds an optional tool policy to `ProfileConfig` (tier cap, fail-closed unannotated handling, allow/deny, classification, code execution, management tools, `switchable_to`) evaluated by **one predicate** inside the existing Spec 105 choke points, and records the effective profile, client and token on every activity record. PV-B gives clients an identity — a per-client agent token (kind `client`) minted at connect time and pinned to a profile — so reassignment is a server-side token update plus a targeted `tools/list_changed`, and connect stops writing the admin API key; it adds REST CRUD, CLI groups, the `profiles` MCP tool and the access explainer, all over one service. PV-C builds the Web UI (Profiles page and editor, the profile/credential additions to Spec 109's Clients page, token dialog, Viewing chip, view-as rows, explainer) and the macOS equivalents (Profiles, profile picker on Spec 109's Clients view, tray Clients submenu, token sheet, explainer); the URL filter contract, `useScopeQuery`, the link map and the macOS `ScopeFilter` are Spec 109's, which this spec's backend un-hides through `features.scope_filters`. Twelve PRs, dependency-ordered, each independently shippable: legacy behaviour is unchanged and no build admits a policy it cannot enforce (FR-009a rollout gate).

## Technical Context

**Language/Version**: Go 1.26 (core, both editions); TypeScript 5.9 / Vue 3.5 (`frontend/src`); Swift 5.9 / SwiftUI (`native/macos/MCPProxy`)
**Primary Dependencies**: existing only — `mark3labs/mcp-go` v1.0.0 (`WithToolFilter` re-run at `tools/call`; `SendNotificationToSpecificClient`), `bbolt`, `bleve`, `zap`, Cobra, chi; Vue Router, Pinia, Tailwind/DaisyUI; SwiftUI/AppKit. **No new dependencies.**
**Storage**: `mcp_config.json` (`profiles[]` extended, `anonymous_profile`); `config.db` bucket `agent_tokens` (fields added), activity and session buckets (fields added, no migration — omitempty, legacy records remain valid)
**Testing**: `go test -race` (+ `-tags server` for edition parity), the enforcement-matrix table test, Spec 105 two-fixture differential oracle and counting upstream, frozen tool-surface goldens (`internal/server/testdata/*.golden.json`, `internal/server/testdata/toolslist_goldens/`), `./scripts/test-api-e2e.sh`, vitest (`frontend/tests/unit/*.spec.ts`), Playwright `e2e/web-ui-sweep` (new `profiles-scope.spec.ts`), Swift `MCPProxyTests` (XCTest) + `mcpproxy-ui-test` MCP, CLI golden output tests (`cmd/mcpproxy/*_test.go`)
**Target Platform**: macOS/Linux/Windows daemon; Web UI embedded; macOS 13+ app
**Project Type**: web + native (Go backend, Vue frontend, Swift app, CLI)
**Performance Goals**: SC-007 ≤ 5 ms p95 added to `retrieve_tools` at 1,000 tools; constitution I (< 100 ms BM25) unchanged; policy evaluation O(1) map lookups + bounded glob matches per tool
**Constraints**: legacy profiles byte-identical (SC-003); Spec 105 non-disclosure and pair discipline preserved; no automatic client-config rewrites; every surface uses the same names (spec Terminology) and the shared contract fixtures
**Scale/Scope**: ~12 PRs; ~45 Go files, ~25 Vue/TS files, ~15 Swift files, 4 docs pages

## Constitution Check

*GATE: evaluated before Phase 0; re-checked after Phase 1 (bottom of file).*

| Principle | Status | Notes |
|---|---|---|
| I. Performance at Scale | PASS with measurement | Policy compiled once per config snapshot into the Spec 105 `profileIndex`; SC-007 measured with the Spec 105 scope-latency harness in 108-b. |
| II. Actor-Based Concurrency | PASS | No new mutexes. Compiled policy rides the immutable (index, snapshot) pair; binding changes flow through the event bus to the server's existing subscriber goroutine; session lookup uses `SessionStore`'s existing ownership. |
| III. Configuration-Driven | PASS with one justified exception | Profiles and `anonymous_profile` live in `mcp_config.json`, hot-reloaded. Client bindings live on the client credential in `config.db` — see Complexity Tracking. Tray holds no state (reads `/clients`, `/profiles`). |
| IV. Security by Default | PASS (primary driver) | Fail-closed unannotated default; allow never widens; client credentials MCP-only; admin API key never written to client configs; anonymous confinement; locked bindings; `profiles` tool admin-kinds only; cache stamps carry policy fingerprint. |
| V. TDD | PASS | Each PR lands failing tests first (matrix rows, CLI goldens, vitest, XCTest), then code. Spec 057/105 suites run unmodified. |
| VI. Documentation Hygiene | PASS | `docs/features/profiles.md` (v3), `agent-tokens.md` (kind client, `--profile`), `connect-clients.md` (client credentials, no admin key), `docs/cli-management-commands.md`, `docs/api/rest-api.md`, `oas/swagger.yaml`, `CLAUDE.md` Recent Changes line. |
| Core+Tray split | PASS | macOS app uses REST/SSE only; no tray-owned state. |
| Event-driven updates | PASS | `profiles.changed`, `client.binding_changed` SSE; no polling added. |
| DDD layering | PASS | Domain: `internal/profile/policy.go` (pure). Application: `internal/runtime/profiles_service.go`, `internal/runtime/clients_service.go`. Infra: storage fields, server enforcement. Presentation: `internal/httpapi/profiles.go`, `clients.go`, `access.go`. |

## Project Structure

### Documentation (this feature)

```text
specs/108-profiles-v3/
├── spec.md                         # contract: stories, FRs, parity matrix, terminology
├── research.md                     # D1–D30 decisions (autonomous clarifications, codex rounds 1–4, Spec 109 ownership)
├── plan.md                         # this file
├── data-model.md                   # config, token, activity, session, derived views
├── quickstart.md                   # per-PR verification + live-instance recipes
├── contracts/
│   ├── rest-api.md                 # routes, shapes, auth, errors
│   ├── mcp-tools.md                # retrieve_tools/set_profile changes, `profiles` tool
│   ├── cli.md                      # profile/client/access groups, changed flags
│   ├── url-filter-contract.md      # pointer: contract owned by Spec 109; backend semantics + availability signal here
│   ├── enforcement-matrix.md       # the matrix fixture and expected outcomes
│   └── refusals.md                 # exact refusal texts and block reasons
├── checklists/requirements.md
└── tasks.md
```

### Source Code (repository root)

```text
internal/config/profiles.go                  # ProfileConfig v3 fields, ValidateProfiles extended, AnonymousProfile
internal/profile/policy.go                   # NEW pure: IntrinsicTier, CompiledPolicy, Decide, glob matcher, Fingerprint
internal/profile/contract.go                 # NEW enums (tiers, reasons, sources, block reasons, steps, warnings)
internal/profile/testdata/contract/*.json    # NEW shared golden fixtures (Go, vitest, Swift)
internal/server/profile_tool.go (profileIndex)  # compiled policies inside the existing (index, snapshot) pair
internal/server/profile_resolver.go          # ProfileResolution with source/base; binding + anonymous tiers; switchable_to
internal/server/profile_tool.go              # also: set_profile admission (FR-022), profile_source in response
internal/server/mcp_visibility.go            # indexedToolVisible gains the policy check
internal/index/manager.go, bleve.go          # SearchToolsAdmitted: hit-level pre-limit predicate (canonical server:tool; annotations resolved by the caller's predicate, not stored), policy-rejected count (FR-011)
internal/server/mcp.go                       # retrieve_tools via SearchToolsAdmitted + hidden_by_profile; call_tool_* profile gate; `profiles` registered outside buildManagementTools
internal/server/mcp_describe_tool.go         # visibility via Decide
internal/server/mcp_direct_scope.go          # /mcp/all list+call filter
internal/server/mcp_code_execution.go        # tool hiding + nested gate
internal/server/mcp_routing.go               # WithToolFilter for management/code_execution/profiles on retrieve/call/code servers
internal/server/mcp_profiles_tool.go         # NEW `profiles` admin tool
internal/server/cache_authz.go               # PolicyFingerprint + ToolTierGeneration in the stamp
internal/server/session_store.go             # TokenName/ClientID/Profile per session; SessionsForToken
internal/server/profile_notify.go            # NEW binding/profile change → list_changed to owning mcp-go server
internal/auth/agent_token.go, context.go     # Kind, ClientID, ProfileMode, pending secret; `mcp_cli_` prefix; per-auth invariant check (fail closed); AuthContext fields
internal/runtime/binding_guard.go            # NEW BindingBypassable/BindingGuardActive (FR-008a), shared by API refusals, resolver, warnings, doctor
internal/storage/activity_models.go, manager.go, agent_tokens*.go   # fields + filters + client-credential mint/revoke tx + staged rotation (pending-hash index, finalize) + reconciler
internal/runtime/activity_service.go         # stamp profile/source/client/token/block_reason
internal/runtime/profiles_service.go         # NEW one service: CRUD, rename/delete w/ reassignment, classify, try, effective tools
internal/runtime/clients_service.go          # NEW one service: list/assign/bulk/rotate/forget/add, warnings
internal/runtime/access_explain.go           # NEW explainer over the enforcement predicates
internal/runtime/events.go                   # profiles.changed, client.binding_changed
internal/connect/connect.go, clients.go      # credential from minted client token; --keyless; credential_state
internal/httpapi/profiles.go (non-admin omission), client_bindings.go (NEW: binding/rotate/forget/add/bulk/upgrade routes + decorator for Spec 109-h's clients.go GET routes), access.go (NEW), tokens.go, activity.go, connect.go, status (`features.scope_filters`), server.go, middleware  # routes, client-credential 403
internal/httpapi/code_exec.go                # classifyCodeExecError: typed profile refusal → 403 PROFILE_BLOCKED (FR-014)
internal/httpapi/server.go (handleSearchTools, handleGetGlobalTools, handleGetServerTools, export, diff), handleApplyConfig  # pinned-token profile decision on REST discovery (FR-015a); FR-008a check + profile_change diff records on POST /config/apply
internal/auth/scoped_view.go                 # ScopedView: non-admin view of a confined anonymous caller for every IsAdmin() branch in the handlers it reaches (FR-016)
internal/server/server.go                    # ReplayToolCall: profile decision before the direct upstream call (FR-015)
oas/swagger.yaml                             # regenerated
cmd/mcpproxy/profile_cmd.go (NEW), client_cmd.go (NEW), access_cmd.go (NEW), connect_cmd.go, token_cmd.go, activity_cmd.go, tools_cmd.go, upstream_cmd.go, doctor checks
frontend/src/views/Profiles.vue (NEW), ProfileEditor.vue (NEW)
frontend/src/components/ViewingFilter.vue (NEW, replaces ProfileSwitcher.vue, placed in Spec 109's header slot), AccessExplainer.vue (NEW), ProfileChip.vue (NEW), ClientBindingControls.vue (NEW, mounted in Spec 109's Clients.vue rows)
frontend/src/views/{Clients (Spec 109-h's; 108 mounts its controls), AgentTokens,Activity,Tools}.vue, components/{TopHeader (remove switcher),ConnectModal or Spec 109's ClientConnectList}.vue, router/index.ts (profiles routes), stores/profiles.ts, stores/clientBindings.ts (NEW), services/api.ts, types/contracts.ts (generated by `go run ./cmd/generate-types`) — `useScopeQuery.ts`, the link map and the sidebar are Spec 109's and are only consumed
frontend/tests/unit/{profile-policy-editor,profiles-page,client-binding-controls,viewing-filter,access-explainer,token-dialog-profile,activity-attribution,tools-view-as}.spec.ts (NEW)
e2e/web-ui-sweep/profiles-scope.spec.ts (NEW)
native/macos/MCPProxy/MCPProxy/Views/{ProfilesView,ProfileEditorView,ClientBindingControls,AccessExplainerView}.swift (NEW; `ClientsView.swift` and `AppState.ScopeFilter` are Spec 109's and only extended)
native/macos/MCPProxy/MCPProxy/{Views/MainWindow,Views/HomeView (Spec 109-d's rename of DashboardView),Views/TokensView,Views/ActivityView,Views/ToolsView,MCPProxyApp,Menu/TrayPresentation,API/APIClient,API/Models,State/AppState}.swift
native/macos/MCPProxy/MCPProxyTests/{ProfilesV3ContractTests,ClientsTrayMenuTests,ParityMatrixTests}.swift (NEW; `ScopeFilterTests` is Spec 109's, extended with the profile/client/token un-hide case)
specs/108-profiles-v3/parity-matrix.json (NEW, 108-l T121: parity row → Web UI / macOS implementing identifiers)
docs/features/{profiles,agent-tokens,connect-clients}.md, docs/cli-management-commands.md, docs/api/rest-api.md
```

**Structure Decision**: existing layout; one new pure domain package file set under `internal/profile/` (the package already exists for `ProfileScope`), two runtime services that every presentation layer calls (FR-026 "one service method"), no new top-level directories.

## Delivery Structure — twelve PRs

Merge order: `a → (b ∥ c) → d → e → f → (g ∥ h ∥ i ∥ k) → j → l`, with these **cross-spec edges** (Spec 109 owns the shared artifacts, spec.md ownership split): a ← 109-a (`contracts.AnnotationTier`, which `profile.IntrinsicTier` delegates to through an explicit adapter — research D30; 109-a is Spec 109's first, dependency-free quick-wins PR, so the security backend waits on nothing larger); f ← 109-h (decorates 109's `GET /clients`/`client list|show`); i ← 109-h, 109-i, 109-k (Clients page shell, header slot, sidebar, and the `useScopeQuery()` the Viewing chip reads — FR-044; 109-k already precedes 109-i in Spec 109's order, so this edge is explicit rather than new); j ← 109-k (`useScopeQuery`, link map); k ← 109-h, 109-i, 109-k (macOS `ClientsView`, `MainWindow.swift` sidebar sections + `SettingsView.swift`, `ScopeFilter`). Spec 109-l in turn waits for 108-f/i/j/k (108-k un-hides the macOS `ScopeFilter` fields 109-l asserts). Shared files between PRs with **no** edge (108-b…e never wait on 109, and 108-a waits only on 109-a), each resolved by a "later merge keeps both and both tests stay green" rule (tasks.md Dependencies): `ClientStatus`/`ConnectResult` (108-c `credential_state` ∥ 109-b `display_path`/`reload_hint`), the Web connect component (108-c ∥ 109-h's `ConnectModal.vue` → `ClientConnectList.vue`, either order), `cmd/mcpproxy/connect_cmd.go` and macOS `Views/ConnectClientView.swift` (108-c `--profile/--lock/--switchable/--keyless` and the profile/mode fields ∥ 109-b `display_path`/`reload_hint` output), `internal/httpapi/scope_filters.go` (108-e ∥ 109-k: one supported-list variable **and** one gate function `rejectUnsupportedScopeFilters`, both created by whichever merges first, so no handler ever parses a scope parameter without the gate). Every PR keeps legacy profiles byte-identical and is independently revertable: FR-009a keeps v3 policy fields rejected until 108-d, so no build between a and d admits a policy it cannot enforce; client credentials use the `mcp_cli_` prefix, which a pre-108 binary never accepts as an agent token, so reverting past 108-c can never turn a client credential into an agent token; with `require_mcp_auth` off a pre-108 binary treats it like an omitted credential (unconfined anonymous, since it knows no `anonymous_profile`), so the documented downgrade procedure turns `require_mcp_auth` on first (SC-010, research D27/D29).

| PR | Branch | Scope | FRs | Depends on | Surfaces |
|---|---|---|---|---|---|
| 108-a | `108-a-profile-policy-model` | v3 fields (`switchable_to` as `*[]string`), validator, `anonymous_profile` field, pure `policy.Decide`, `IntrinsicTier`, fingerprint, contract enums + fixtures, compiled into `profileIndex`; **rollout gate** `profile.PolicyEnforcementReady=false` (v3 policy fields rejected outside tests); `IntrinsicTier` delegates to Spec 109-a's `contracts.AnnotationTier` through the explicit string → int adapter (data-model §2, T005a) | FR-001–009, FR-009a (gate introduced) (FR-005: stale detection in `policy`; its reporting surfaces are tagged on f, g, h, i, k), 052 | **109-a** (`contracts.AnnotationTier`) | Go |
| 108-b | `108-b-profile-discovery-enforcement` | index API `SearchToolsAdmitted` (hit-level pre-limit predicate) + `retrieve_tools` filter-before-limit + `hidden_by_profile` (and `profile` only for url/session sources), `describe_tool`, `/mcp/all` `tools/list`, cache fingerprint + `ToolTierGeneration`, SC-007 bench; policy active only under the test override (FR-009a) | FR-010–012, 015 (list), 019 (regression test), 027 (cache) | a | MCP |
| 108-c | `108-c-client-credentials` | token `Kind/ClientID/ProfileMode` (mode client-only), `mcp_cli_` secret prefix, per-authentication invariant check (fail closed), staged rotation (pending secret, finalize, reconciler), resolver tiers binding/anonymous + `ProfileResolution` + `switchable_to` admission (FR-022), **binding guard** (API refusal + runtime anonymous deny-all), client creds MCP-only (REST 403), connect mints creds + never writes admin key + `--keyless` + `credential_state` (and `GET /connect`, `/connect/{client}`, `/connect/{client}/preview` become administrator-only reads, FR-025a), session token index, live reassignment notify, `PUT /clients/{id}/binding` (in the 108-owned `client_bindings.go`; `GET /clients` is Spec 109-h's and is decorated in 108-f), Web UI/macOS connect dialogs gain profile + mode | FR-008a, FR-020–026, FR-021a, FR-025a, 028, FR-030 (binding changes), US5 | a | Go + connect |
| 108-d | `108-d-profile-execution-enforcement` | `call_tool_*` gate + refusals, `/mcp/all` call, `code_execution` hide + nested gate, management-tool filters, the pinned-token profile decision on the REST discovery routes (`GET /index/search`, `GET /tools`, `GET /servers/{id}/tools`, export, diff — FR-015a), every gate enforced inside the shared handlers so REST `POST /api/v1/tools/call` and `POST /api/v1/code/exec` (typed `PROFILE_BLOCKED` via `classifyCodeExecError`) match MCP, plus the profile decision before `POST /api/v1/tool-calls/{id}/replay`'s direct upstream call (`server_not_in_profile` → its existing non-disclosing `404`; policy reasons → `403`) (FR-014/FR-015), confined-anonymous non-admin view (`auth.ScopedView`) for `AuthorizeServerOp` **and** every other `IsAdmin()` branch in the handlers it reaches (`upstream_servers` list filter, `tail_log` reader and `last_error` redaction, call-path refusal shape), binding/anonymous/guard rows added to the 108-b discovery tests, `set_profile` response `profile_source` (own selection only) + uniform refusal reuse, non-naming refusal texts, activity `block_reason`; **flips `profile.PolicyEnforcementReady` to `true`** in the commit that lands the last execution gate | FR-009a (gate lifted), FR-013–016, FR-015a, 018 (FR-017 lands with the `profiles` tool in 108-h; 108-d's tests exclude its rows) | a, b, c | MCP |
| 108-e | `108-e-scope-attribution-filters` | activity/session fields, `profile_change` **filters** (the type constant and binding-change records already exist from 108-c T040), filters on activity/summary/usage/export/sessions/tools/servers, view-as on `/tools` (non-admin: visible rows + counts), `features.scope_filters` on `/api/v1/status` (un-hides Spec 109's hidden params; fills the one supported list that Spec 109-k's `unsupported_scope_filter` gate reads — and, if 109-k has not merged, creates that list **and** the gate function and calls it in every handler this PR touches), swagger | FR-029–030, FR-031 (activity/summary/usage/export/sessions/tools/servers params + availability signal), FR-032–033 (+ CLI `activity` `--profile/--client/--token` flags, T065; `--from/--to` are Spec 109-k's) | c, d | REST, CLI (activity flags) |
| 108-f | `108-f-profiles-clients-rest` | profiles service + CRUD/rename/delete/classify/try/effective-tools (non-admin: visible rows + counts), `GET /profiles` omission for every non-admin caller, remaining clients routes (custom add, rotate + finalize, forget, bulk, admin-key bulk upgrade), binding/credential decoration and `profile`/`client` filters on **Spec 109-h's** `GET /clients` and `GET /clients/{id}`, `GET /tokens?profile=&token=` filters, `no_client_credential` refusal, tokens `profile`, access explain, SSE events, `/profiles/active` deprecation, rename/reassign re-pinning of tokens and bindings, `anonymous_profile` via the service, `list_changed` on profile edits (snapshot diff) | FR-005 (effective-tools `classification_stale`), FR-025 (bulk upgrade), FR-027 (notify), 030, FR-031 (`/clients` and `/tokens` filters), 034–035, 038–039 | e, **109-h** | REST |
| 108-g | `108-g-cli-profiles-clients` | `profile`, `access` groups; binding subcommands (`set-profile`, `lock`, `unlock`, `add`, `forget`, `rotate`, `upgrade-admin-key-holders`) and binding columns/`--profile` on **Spec 109-h's** `client list|show`; `connect --profile`, `token create --profile`, activity/tools/upstream flags; doctor checks | FR-036, FR-005 (`profile show --effective`), US5-4 | f | CLI |
| 108-h | `108-h-mcp-profiles-tool` | `profiles` admin tool registered outside `buildManagementTools()` (read ops survive `read_only_mode`) + visibility filter + goldens | FR-017, 037, FR-005 (`effective_tools`) | f | MCP |
| 108-i | `108-i-webui-profiles-clients` | Profiles page + editor (Try it, classify, focus), profile/credential controls mounted in **Spec 109-h's** Clients page (chip, lock, credential state, other client, rotate, forget, bulk, admin-key upgrade, warnings banner), connect-flow profile fields, token dialog, explainer, remove ProfileSwitcher → Viewing chip in **Spec 109-i's** header slot on **Spec 109-k's** `useScopeQuery()`, `/profiles` routes (the sidebar entry is 109-i's) | FR-040–043, FR-044 (complete), 046 (explainer component + client-row entry point), FR-005 (editor stale marker) | f, **109-h, 109-i, 109-k** (109-k's `useScopeQuery()` for the Viewing chip, FR-044; transitively implied by 109-i) | Web UI |
| 108-j | `108-j-webui-profile-scope-ui` (re-scoped; its former `useScopeQuery` + link-map scope is merged into **Spec 109-k**, the owner) | profile-specific UI on pages whose filters Spec 109 already wires: Tools view-as rows (greyed excluded rows, reason, "Why?"), Activity attribution chips (client · profile · token) and blocked-row "Allow in profile…"/"Why?" actions, Sessions-view profile/client/source columns, Playwright `profiles-scope.spec.ts` (audit check 6 end to end through 109's links) | FR-045 (consumer), 032 (UI), 046 (Tools view-as and blocked Activity row entry points) | e, i, **109-k** | Web UI |
| 108-k | `108-k-macos-profiles-clients` | Profiles view + editor, profile/credential controls in **Spec 109-h's** `ClientsView`, tray Clients submenu (replaces Profile), token sheet, un-hiding the profile/client/token filters of **Spec 109-k's** `ScopeFilter` on Activity/Tools/Dashboard/Servers, attribution columns, explainer sheet, contract tests | FR-047–050, FR-005 (editor stale marker) | f, **109-h, 109-i, 109-k** | macOS |
| 108-l | `108-l-profiles-v3-parity-docs` | cross-surface parity test, Playwright `profiles-scope.spec.ts` in the release-gate sweep, docs, release notes, CLAUDE.md line | FR-051, SC-001, SC-006 | g, h, j, k | all |

## Design — the five mechanisms

### 1. One predicate, compiled per snapshot
`profile.CompiledPolicy.Decide(server, tool, intrinsic)` is the only place a profile excludes a tool. It is compiled by the same pre-publish observer that builds `profileIndex` (Spec 105 D17) and taken with its snapshot as one pair, so a request can never pair a decision with a different policy. Enforcement sites, `effective-tools`, "view as", "Try it" (compiles a throwaway policy from the draft) and the explainer call it — which is what makes SC-009 provable. Enforcement sites are the **shared handlers** (`handleCallToolVariant`, `handleCodeExecution`, `handleUpstreamServers`, `handleQuarantineSecurity`, `handleRetrieveTools`, `describe_tool`), not the mcp-go tool filters: `WithToolFilter` only keeps `tools/list` honest, because REST `POST /api/v1/tools/call` reaches the same handlers by name through `CallToolDirect` (FR-015).

### 2. Filter before limit
`retrieve_tools` already runs a scope-aware search (Spec 105 FR-005), but its pre-limit predicate only sees the server name (`SearchToolsScoped(query, limit, inScope func(serverName string) bool)`) and `indexedToolVisible` runs on the already-cut result, so adding the policy there would let a hidden tool displace an allowed one and undercount `hidden_by_profile` (codex round 1). 108-b extends the index API with `SearchToolsAdmitted(query, limit, admit func(Hit) Admission)`: the predicate receives the hit's canonical `server:tool` (the index stores no annotations and gets none — no schema change, no reindex), resolves its effective annotations through `profile.EffectiveAnnotations` = `resolveExactToolIdentity`, the StateView identity seam every dispatch path already uses (so discovery and execution classify from one source), evaluates server scope then `Decide`, and the index applies the limit to admitted hits only while counting policy-only rejections (never server-scope ones) over the exhaustive match set. `indexedToolVisible` keeps its post-cut defence-in-depth role.

### 3. Resolution returns provenance
`resolveActiveProfileWithIndex` returns `ProfileResolution{Name, Source, Scope, Policy, Base}` from the pair it already holds. Every consumer (enforcement, activity stamping, session update, `set_profile` admission, cache stamp) reads the one result — no second lookup (Spec 105 pair discipline).

### 4. Binding = credential; reassignment = token update + targeted notify
The token store transaction updates pin/mode; `clients_service` emits `client.binding_changed`; `profile_notify.go` (subscriber) resolves sessions for the token from `SessionStore` and calls `SendNotificationToSpecificClient` on the mcp-go server instance that owns each session. Correctness never depends on the notification: `mcpAuthMiddleware` re-validates the token from storage on every request.

### 5. One service per aggregate, many presenters
`profiles_service` and `clients_service` own validation, persistence, activity (`profile_change`) and events; REST handlers, the `profiles` MCP tool, and (through REST) the CLI, Web UI and macOS app are thin presenters. The parity test (108-l) walks `contracts/rest-api.md` field lists against CLI `--help-json`, the MCP input schema and the generated `contracts.ts`/Swift models.

## Risks

| Risk | Mitigation |
|---|---|
| Two tier mappings drift (108 `profile.Tier` int vs Spec 109 `contracts.Tier` string) | one mapping: `IntrinsicTier` calls `contracts.AnnotationTier` (edge a ← 109-a) through an exhaustive adapter, unknown values fail closed to destructive; joint test T005a pins `IntrinsicTier(a,true).String() == AnnotationTier(a)` for every annotation fixture |
| Hidden behaviour change for legacy profiles | D1 inheritance; Spec 057/105 suites run unmodified in every PR; `IsLegacy()` fast path |
| `hidden_by_profile` becomes an oracle | counted only inside effective server scope; differential-oracle assertion in 108-b |
| Frozen goldens drift | only `profiles` tool and `retrieve_tools_profile_v3` goldens added, enumerated in PR bodies |
| Client credential leaks REST access | FR-023 403 in middleware + test per route family |
| Rollback past 108-c turns client credentials into wildcard agent tokens | `mcp_cli_` prefix never accepted as an agent token by a pre-108 binary; rollback test (T030a) runs the pre-108 middlewares against a minted client secret |
| Malformed/half-migrated credential record authenticates as a regular token | invariants re-checked on every authentication, fail closed (FR-021, T027a) |
| Rotation crash leaves a client with an invalidated secret | staged rotation + reconciler (FR-021a, T028a) |
| Locked client escapes by omitting its credential | FR-008a API refusal (every path incl. `POST /config/apply`) + runtime anonymous deny-all re-evaluated per published pair; "wider" compares reachable sets on servers, cap, unannotated handling, admitted tools and capabilities (T033a) |
| Pinned token reads hidden tools over REST discovery | FR-015a: `GET /index/search`, `GET /tools`, `GET /servers/{id}/tools` (+ export, diff) apply the caller's own profile (T046c) |
| Rollback past 108-c with optional auth | documented downgrade precondition `require_mcp_auth: true` (SC-010, T124/T125); the rollback test pins `401` with auth on |
| Discovery hides a tool that execution still runs (partial rollout) | FR-009a gate until 108-d (T004a) |
| Parallel work with Spec 109 on shared files | ownership split (spec.md): 108 never edits `useScopeQuery.ts`, the link map, `ScopeFilter`, the Clients page shell or the sidebar/header layout; it mounts components and registers routes; cross-spec edges in the merge order |
| Profile gates bypassed via REST `POST /api/v1/tools/call`, `POST /api/v1/code/exec` or `POST /api/v1/tool-calls/{id}/replay` (no mcp-go filters on those paths; replay reaches the upstream client directly) | gates live in the shared handlers (FR-015), `classifyCodeExecError` maps the typed profile refusal (FR-014), replay runs the profile decision itself; enforcement-matrix REST column (T046a, T046b) |
| Confined anonymous caller inherits admin `AuthorizeServerOp` (anonymous context is admin-typed) | non-admin evaluation for confined anonymous (FR-016, D14); T047 row with counting upstream |
| Codex `?apikey=` token in URL | documented; same carrier already used for the admin key (strict improvement) |
| Notification sent via wrong mcp-go instance | session → server-instance map recorded at initialize; test per routing mode |
| Rename/delete partial failure | ordered steps that never widen (D19) |
| macOS drifting from Web UI | shared contract fixtures decoded in XCTest; parity test in 108-l |

## Complexity Tracking

| Violation | Why Needed | Simpler Alternative Rejected Because |
|---|---|---|
| Client binding stored in `config.db` (on the token), not `mcp_config.json` (constitution III) | The binding is inseparable from a secret-bearing, per-machine credential already stored in `config.db`; one bbolt transaction updates pin, mode and revocation atomically | Storing the binding in the config file splits one change across two stores (non-atomic, drift on hand edits) and would put client ids next to shareable config; storing the raw credential in the config file is unacceptable |
| Two new runtime services | FR-026 requires one operation shared by five presenters | Handler-local logic would fork validation and activity recording per surface — exactly the drift the maintainer forbade |

## Constitution Check — post-design

Re-evaluated after data-model and contracts: all gates PASS; the only deviation is the justified config.db binding above. No new dependencies, no new locks, no polling.

## Phase 2 hand-off

`tasks.md` lists tasks per PR in the merge order above, failing tests first, each PR with its test plan and live-verification recipe ([quickstart.md](quickstart.md)).

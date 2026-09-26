# Implementation Plan: Navigation, Scope Filters and Cross-Surface UX Consistency

**Branch**: `109-ux-navigation-consistency` | **Date**: 2026-09-25 | **Spec**: [spec.md](spec.md)
**Input**: [spec.md](spec.md) · [research.md](research.md) D1–D28 · [data-model.md](data-model.md) · [contracts/](contracts/) · UX audit rev 2 (O2–O6, N1–N8, H1–H4, S2, S4–S7, C1–C2, A1–A3, T1; M1–M7; quick wins) · four surface inventories at `638fa805a` · Spec 108 (branch `108-profiles-v3`)

## Summary

Give MCPProxy one information architecture and one vocabulary across the Web UI, the macOS app, the CLI and MCP. The work has four parts:

1. **Shared core computations** served over REST. Each has one Go function and one generated enum set:
   - the health `status`, `usable` and ordered `actions`;
   - the tool `tier`;
   - the needs-attention list;
   - the review payload with captured definitions;
   - client presence;
   - the ranked catalog.
2. **Presenters** on every surface that read those computations and never re-derive them. The Web UI adds Home, Clients, the Review queue, the server card, the header with ⌘K, and Add server with Catalog and Paste. The macOS app gets the same areas and names. The CLI gets `attention`, `review`, `client` and `catalog` plus flag changes. MCP gets field parity on existing tools.
3. **One URL filter contract**: `useScopeQuery` plus a deep-link map and the macOS `ScopeFilter`, owned here with **every** parameter — `profile`, `client` and `token` included, wired but hidden until Spec 108-e's `features.scope_filters` (ownership rule, research D25).
4. **A contradiction register**, where every surface disagreement is either closed by an FR or accepted with a reason.

The work is split into thirteen PRs. The quick wins come first and none of them depends on Spec 108. Only 109-l integrates with Spec 108; in the other direction, Spec 108 integrates into the artifacts this spec owns (108-a ← 109-a for `contracts.AnnotationTier`, 108-f ← 109-h, 108-i ← 109-h + 109-i, 108-j ← 109-k, 108-k ← 109-h + 109-i + 109-k).

## Technical Context

**Language/Version**: Go 1.26 (core, both editions); TypeScript 5.9 / Vue 3.5 (`frontend/src`); Swift 5.9 / SwiftUI + AppKit (`native/macos/MCPProxy`)
**Primary Dependencies**: existing only: chi, zap, bbolt, bleve, Cobra, `mark3labs/mcp-go` v1.0.0; Vue Router, Pinia, Tailwind/DaisyUI; SwiftUI/AppKit. The ⌘K palette, diff rendering and ranking are written in-repo. **No new dependencies.**
**Storage**: `config.db` `tool_approvals` records gain `current_annotations`/`previous_annotations` (`omitempty`, 109-f); the onboarding record gains a `client_connected_at` map (client id → last connect write, 109-b) and a `client_last_seen` map (`clientInfo.name` → last session start, written on every MCP `initialize` independent of telemetry, 109-h; the telemetry-bucket `mcp_clients_seen_ever` stays an unchanged `[]string`). No config-file schema change. Keyring secrets use the existing secrets API.
**Testing**: `go test -race` (+ `-tags server` for the edition parity of new routes); table tests for `health`, `attention.Compute`, `AnnotationTier`, `registries.Rank`, the review composer, and presence; `./scripts/test-api-e2e.sh` extended with `/attention`, `/review`, `/clients`, `/catalog/search`; frozen MCP goldens (only the declared `search_servers`/`list_registries` schema text in 109-j); vitest (`frontend/tests/unit/*.spec.ts`); Playwright `e2e/web-ui-sweep/navigation-consistency.spec.ts` (new) plus the existing `visual-a11y-sweep.spec.ts` at 1440/1100/900/390; Swift `MCPProxyTests` (XCTest) plus the `mcpproxy-ui-test` MCP; CLI goldens (`cmd/mcpproxy/testdata/cli109/`)
**Target Platform**: macOS/Linux/Windows daemon; embedded Web UI; macOS 13+ app
**Project Type**: web + native (Go backend, Vue frontend, Swift app, CLI)
**Performance Goals**: `/attention` ≤ 20 ms p95 at 100 servers / 1,000 tools (served from snapshot); ⌘K tool search reuses BM25 (< 100 ms, constitution I); catalog search bounded by a 5 s per-source timeout; no per-request activity-log scans (presence counts come from the in-memory usage aggregate)
**Constraints**: every existing URL keeps working (redirects, query kept); CLI output is backward compatible (new columns appended, old flags kept as aliases, default `activity list` unchanged, JSON output additive only) with two declared table-text changes, listed in the PR bodies and release notes: `upstream list` STATUS shows the status label instead of the free-text summary (the summary stays in `-o json` `health.summary`; ACTION keeps its CLI-hint form, now keyed on `actions[0]`), and the `status` section heading `MCP Endpoints` becomes `Endpoint & mode` (research D23); legacy JSON fields unchanged (`level`, `approval_status` values; `action` unchanged except the one FR-010 case, quarantined + sign-in → `login`); Spec 105/108 security predicates untouched; untrusted descriptions rendered inert
**Scale/Scope**: 13 PRs; ~25 Go files, ~40 Vue/TS files, ~20 Swift files, ~8 CLI files, 5 docs pages

## Constitution Check

*GATE: evaluated before Phase 0; re-checked after Phase 1 (bottom of file).*

| Principle | Status | Notes |
|---|---|---|
| I. Performance at Scale | PASS | Attention and presence are served from event-maintained snapshots. The palette uses the existing BM25 index. Catalog fan-out is concurrent and bounded. SC-011 measured in 109-d (T059, attention p95) and 109-j (T109a, catalog p95 with a hung source). |
| II. Actor-Based Concurrency | PASS | The attention subscriber is one goroutine on the event bus that publishes an immutable slice (atomic pointer swap). Catalog fan-out is goroutines + a buffered channel with a context deadline. No new mutexes. |
| III. Configuration-Driven | PASS | No new config keys. Settings moves (Scanners, Catalog sources) are presentation only. The tray keeps no state: attention, review and presence come from REST/SSE. |
| IV. Security by Default | PASS (improves) | Closes X2 (macOS approval bypassed the scan gate) and X12 (the Go tray's one-click unquarantine). New SSE types are filtered per subscriber (FR-006), never classified fail-open. Informed review keeps quarantine blocking and shows untrusted text inertly. Secrets go to the keyring at add time. Agents still cannot approve servers. New REST routes classify callers with `auth.IsScopedCaller` (`!IsAdmin()`, never `Type == AuthTypeAgent`): administrators get full reads, scoped callers (agent tokens, server-edition user sessions) filtered reads or `403` per route (FR-007), each tested with both caller kinds. |
| V. TDD | PASS | Each PR lands failing tests first: tables, goldens, vitest, XCTest, Playwright. The X11 CLI `--risk` suspicion is proven by a failing test before the fix. |
| VI. Documentation Hygiene | PASS | `docs/features/needs-attention.md` (new), `docs/features/security-quarantine.md` (review flow), `docs/features/connect-clients.md` (Clients page, reload hints), `docs/cli-management-commands.md`, `docs/api/rest-api.md`, `oas/swagger.yaml`, `docs/development/web-ui-verification.md` and `macos-tray.md` checklists, `CLAUDE.md` Recent Changes. |
| Core+Tray split | PASS | macOS changes are presenters over REST/SSE. |
| Event-Driven Updates | PASS | New SSE `attention.changed` and `review.changed`. No polling (the Web UI's `loadPendingTools` per-server loop is removed in favour of `/review`). |
| DDD layering | PASS | Pure domain functions (`health`, `contracts.AnnotationTier`, `registries.Rank`, `attention.Compute`); orchestration in `internal/runtime/`; presentation in `internal/httpapi/` and `cmd/mcpproxy/`. |

## Project Structure

### Documentation (this feature)

```text
specs/109-ux-navigation-consistency/
├── spec.md                       # stories, FRs, parity matrix, terminology, contradiction register
├── research.md                   # D1–D28 decisions (autonomous clarifications, 108 coordination, codex rounds 1–4)
├── plan.md                       # this file
├── data-model.md                 # additive fields, derived views, frontend/macOS state
├── quickstart.md                 # common gates, isolated instance, per-PR live recipes
├── contracts/
│   ├── rest-api.md               # attention, review, clients, catalog, connect/onboarding/import additions
│   ├── cli.md                    # attention, review, client, catalog groups; changed commands
│   ├── mcp-tools.md              # search_servers, quarantine_security inspect, upstream_servers health
│   ├── health-vocabulary.md      # status enum, derivation, labels, forbidden renderings
│   ├── url-filter-contract.md    # useScopeQuery, parameters, behaviour, link map, macOS ScopeFilter
│   └── navigation-map.md         # sidebar, routes/redirects, header, z-index scale, macOS sidebar/toolbar/tray
├── checklists/requirements.md
└── tasks.md
```

### Source Code (repository root)

```text
internal/health/calculator.go, constants.go            # status, usable, actions (FR-010–012)
internal/contracts/types.go, tier.go (NEW)             # HealthStatus fields; AnnotationTier (FR-028); ServerTokenMetrics.estimated
internal/runtime/attention.go, attention_contract.go (NEW)   # Compute (+ minimal AttentionClient input) + subscriber + SSE (FR-001–002)
internal/runtime/review.go, review_diff.go (NEW)       # review queue + server review composer (FR-021)
internal/runtime/clients_presence.go (NEW)             # presence join (FR-030); AttentionClient() feeds client_never_seen (109-h)
internal/runtime/tool_quarantine.go                    # write current/previous annotations (FR-020)
internal/runtime/events.go                             # attention.changed, review.changed
internal/storage/models.go                             # ToolApprovalRecord annotations (109-f); OnboardingState client_connected_at (109-b) + client_last_seen (109-h)
internal/storage/bbolt.go, manager.go                  # UpdateOnboardingState read-modify-write helper (109-b, T035)
internal/connect/clients.go, connect.go                # ClientInfoNames, ReloadHint, display_path (FR-037)
internal/configimport/detector.go, import.go           # url/command formats, summary/tags/secret_like (FR-040, FR-064)
internal/registries/catalog.go, rank.go (NEW)          # SearchAll, Rank (FR-060–061)
internal/security/scanner/service.go                   # ApproveServer block[] (FR-022)
internal/httpapi/attention.go, review.go, clients.go (NEW, personal-only route), catalog.go (NEW)
internal/httpapi/onboarding.go, connect.go, import.go, security_scanner.go, server.go (routes; per-subscriber attention.changed render), sse_scope.go (FR-006 classification), tokens metrics
internal/tray/managers.go, tray.go; cmd/mcpproxy-tray/internal/api/client.go, adapter.go   # Go tray: quarantine click → Web UI review, unquarantine path deleted (X12)
internal/server/mcp.go                                 # search_servers registry optional; quarantine_security inspect fields; initialize writes client_last_seen (109-h)
cmd/generate-types/main.go                             # new enums/labels → contracts.ts
cmd/mcpproxy/attention_cmd.go, review_cmd.go, client_cmd.go, catalog_cmd.go (NEW)
cmd/mcpproxy/status_cmd.go, doctor_cmd.go, upstream_cmd.go, tools_cmd.go, connect_cmd.go, activity_cmd.go, registry_cmd.go, security_cmd.go
oas/swagger.yaml
frontend/src/router/index.ts                           # routes + redirects (navigation-map)
frontend/src/composables/useScopeQuery.ts (NEW)
frontend/src/stores/attention.ts, clients.ts (NEW)
frontend/src/views/Home.vue (NEW; Dashboard.vue's Overview parts move here), Usage.vue (page), Clients.vue (NEW), Review.vue (NEW), AddServer.vue (NEW; replaces Repositories.vue + AddServerModal entry), Activity.vue, Tools.vue, Servers.vue, ServerDetail.vue, Settings.vue, AgentTokens.vue (becomes a Clients tab), Sessions.vue (becomes an Activity view)
frontend/src/components/{AttentionList,StatusPill,AddMenu,CommandPalette,ClientConnectList,ImportServers,ReviewScreen,ToolDefinitionText,CatalogSearch,PasteServer,SecretToggle}.vue (NEW)
frontend/src/components/{TopHeader,SidebarNav,ServerCard,OnboardingWizard,TelemetryBanner,ProfileSwitcher}.vue; ConnectModal.vue, ModeSwitcher.vue (removed/merged)
frontend/src/assets/z-index.css (NEW) or tailwind theme tokens
frontend/tests/unit/*.spec.ts (NEW specs listed in tasks.md)
e2e/web-ui-sweep/navigation-consistency.spec.ts (NEW); visual-a11y-sweep.spec.ts (widths 1100 added)
native/macos/MCPProxy/MCPProxy/Views/{HomeView (renamed DashboardView),ClientsView,ReviewQueueView,ReviewSheet,CatalogView}.swift (NEW/renamed)
native/macos/MCPProxy/MCPProxy/Views/{MainWindow,ServersView,ServerDetailView,ToolsView,ActivityView,AddServerView,ConnectClientView,TokensView,SettingsView,RegistriesView (removed → CatalogView)}.swift
native/macos/MCPProxy/MCPProxy/{MCPProxyApp,Menu/TrayPresentation,API/APIClient,API/Models,API/ConnectModels,State/AppState}.swift
native/macos/MCPProxy/MCPProxyTests/{HealthVocabularyTests,AttentionTests,ReviewPayloadTests,ScopeFilterTests,NavigationStructureTests,ApproveServerPathTests}.swift (NEW)
docs/features/needs-attention.md (NEW), security-quarantine.md, connect-clients.md, docs/cli-management-commands.md, docs/api/rest-api.md, docs/development/{web-ui-verification,macos-tray,release-gate}.md
```

**Structure Decision**: existing layout. New code consists of pure domain functions next to their owners (`internal/health`, `internal/contracts`, `internal/registries`) and runtime composers in `internal/runtime/`. The Web UI gets new views and components under the existing folders. No new top-level directories.

## Delivery Structure — thirteen PRs

Merge order: `(a ∥ b ∥ c) → (f ∥ j ∥ k) → (d ∥ e) → (g ∥ h) → i → m`, and `l` once Spec 108-f/i/j/k and 109-i are merged (108-k un-hides the macOS `ScopeFilter` fields 109-l asserts; codex round 4). Exact edges: d, e ← c, k (their Activity links need 109-k's query params); f ← a, c; g ← d, f, k (its macOS review sheet repoints the attention/Home openings 109-d creates); h ← b, d, j, k (presence compiles against 109-d's `AttentionClient` and subscriber, and `ImportServers` is extracted from 109-d's `Home.vue` and 109-j's `AddServer.vue`); i ← d, g, h, j, k; j, k ← a; m ← a..k. Every PR keeps all existing URLs, JSON fields and CLI flags working, and can be reverted on its own. No PR depends on a type or route from a later wave: 109-d defines its own `AttentionClient` (109-h feeds it), and screens that ship later are reached through interim targets (109-a's `/review` redirects, owned by the first PR that links to `/review`). PRs without an edge between them that share a file or field are listed in tasks.md Dependencies: `OnboardingWizard.vue` (g, h); macOS `ServersView.swift` (e, f, and e, g for the review action) and `DashboardView.swift` → `HomeView.swift` (d, f), both security-sensitive; and the `has_usable_server` definition (b, c: the second to merge applies `health.usable`). Cross-spec: Spec 108-a waits on 109-a (its `IntrinsicTier` calls `contracts.AnnotationTier` through an int ↔ string adapter; codex round 4); shared files without an edge (Spec 108-b…e never wait on 109), each with a "later merge keeps both, both tests green" rule: `ClientStatus`/`ConnectResult` (109-b ∥ 108-c), `cmd/mcpproxy/connect_cmd.go` and macOS `ConnectClientView.swift` (109-b ∥ 108-c), the Web connect component (109-h ∥ 108-c, both orders), `internal/httpapi/scope_filters.go` (109-k ∥ 108-e: one list variable and one gate function, both created by whichever merges first), the administrator-only gate on the three `GET /connect` reads (109-h ∥ 108-c, one middleware and message; FR-030a, codex round 3). The macOS `MainWindow.swift`/`SettingsView.swift` pair with Spec 108-k is ordered by the edge 108-k ← 109-i (codex review round 2).

| PR | Branch | Scope | FRs / findings | Depends on | Surfaces |
|---|---|---|---|---|---|
| 109-a | `109-a-quick-wins` | `/` defaults to Overview (interim N8); ProfileSwitcher hidden with no profiles (H2); z-index scale + modals in the top layer (H4); "Settings" naming, line icons, edition-gated Server Edition tab (N7); "Add to MCPProxy" (C2); tab↔URL sync on server detail/Settings (S6); Tools approval values (T1) + interim `/review` redirects to server views (T026a, removed by 109-g); `contracts.AnnotationTier` + `tier` on tool listings, Web "Tier", CLI `--tier` (`--risk` alias, X11); integer ticks + "Calls to unknown tools" (A2 part); macOS and CLI label parity for T1/C2/tier; `scripts/test-api-e2e.sh` cleanup stops only its own PIDs (T011a, the gate both specs run) | FR-016 (detail, Settings), 027, 028, 055–057, 063 (label), 074; W3, W5, X7, X11 | — | Web, macOS (labels), CLI (help, `--tier`), REST (`tier`) |
| 109-b | `109-b-onboarding-connect-hints` | `display_path`, `reload_hint`, `ClientInfoNames` on connect; `has_usable_server`, `usable_servers`, `client_connected_at`; import preview `summary`/`tags`; wizard O2/O3/O4 (completion rule, Verify hints/prompts)/O5/O6; CLI `connect` output; macOS ConnectClientView result + FirstRunDialog check | FR-037, 040–044; X8 | — | Go, Web, macOS, CLI |
| 109-c | `109-c-health-vocabulary` | `status`/`usable`/`actions` in the calculator; contracts regen; Web labels (card status line, detail Configuration → Health row); macOS rows + tray; CLI `upstream list` STATUS label, ACTION hint keyed on `actions[0]`, `--status`; MCP `upstream_servers list` data | FR-010–012, 014 (labels), 015; X5 | — | Go, Web, macOS, CLI, MCP |
| 109-d | `109-d-needs-attention` | `attention.Compute` (every kind, incl. the `client_never_seen` rule over its own minimal `AttentionClient` input; live client feed from 109-h) + subscriber + `GET /attention` + SSE rendered per subscriber (FR-006); Home (attention + topology + usage strip); header attention pill in the current header; sidebar Home badge; macOS tray group + Home section + badge reading the endpoint (the Dashboard → Home rename also removes the one-click `approveTools`, whatever the order with 109-f); CLI `attention`, `status` line, `doctor` first section | FR-001–007, 051; X1; N6, N8, A3 | c, k | all but MCP |
| 109-e | `109-e-server-card-next-action` | Server card layout (status line, one primary, ⋯ menu, stats line with Activity links, stable height, shield); macOS Servers row primary + context menu; `navigation-consistency.spec.ts` created and added to `scripts/run-web-smoke.sh` | FR-013, 014; S4 | c, k | Web, macOS |
| 109-f | `109-f-review-backend` | Annotations on approval records; `GET /review`, `GET /servers/{id}/review`, diff composer with the server summary through the shared secret redaction (`oauth.RedactServerSecretFields`, the fourth door); `security/approve` `block[]` written (as `BlockTools` does: `approved` + `disabled`) in the baseline transaction **before** the unquarantine, via the new `scanner.Storage.SaveIntegrityBaselineWithBlocks`; MCP `inspect_quarantined` keeps its live-inspection fallback; CLI `review` group + alias help; MCP `quarantine_security` inspect fields; SSE `review.changed` scoped (FR-006); **macOS Approve Server → `security/approve` + dangerous-verdict confirmation (X2)**; **Go tray quarantine click → Web UI review, its `/unquarantine` path deleted (X12)**; `FIXTURE_CALL_LOG` in the stdio test fixture so the live recipe can prove a blocked tool never reached the upstream | FR-006 (review), 007 (review), 020–022, 025, 026; X2, X3, X12, C4 | a, c | Go, CLI, MCP, macOS, Go tray (X12 only) + Web (approval path and dead `/unquarantine` callers only) |
| 109-g | `109-g-review-queue-ui` | Web Review queue + review screen (+ server detail Review tab), inert description rendering, Tools "Needs review" → `/review`, Security page → redirect, scanners → Settings → Security; wizard inline review list (O4); macOS Review Queue view + review sheet, tray "Review Queue…" | FR-023, 024, 027 (links); S2, N6, M2 | d, f, k | Web, macOS |
| 109-h | `109-h-clients-hub` | **Owner** of the Clients page shell (row/column/banner slots for Spec 108), `GET /clients`/`GET /clients/{id}` and the row shape Spec 108 decorates; `GET /clients` presence (administrator only; `connected` from connect-write/seen evidence, `connection_unverified` otherwise, the content read only on `GET /clients/{id}`); the existing `GET /connect`, `/connect/{client}`, `/connect/{client}/preview` made administrator-only in the same PR (FR-030a; shared gate with Spec 108-c, first to merge adds it); persisted `client_last_seen` map written on every MCP `initialize` (independent of telemetry) and `client_disconnected_at` stamped by disconnect (seen-before-disconnect is not connection evidence); `calls_24h` from a new per-client rolling counter in the usage aggregate; presence feeds attention `client_never_seen`; CLI `status` `Endpoint & mode` section; Clients page (Clients · Endpoint & mode · Agent tokens); `ClientConnectList` + `ImportServers` shared by wizard/Clients/Add/Home; combined bulk diff; ConnectModal and ModeSwitcher/endpoint dropdown moved; `/tokens` redirect; macOS Clients view (+ Endpoint & Mode, Agent Tokens tab); CLI `client list|show` | FR-007 (clients), 030–037, 030a; N1, N2, N4, H3; C1 | b, d, j, k | all but MCP |
| 109-i | `109-i-navigation-header` | Sidebar regroup (Web + macOS); header: ⌘K palette, status pill, "+ Add ▾", Viewing slot, responsive < 1100 px; macOS toolbar "+" menu; remove Dashboard/Sessions/Security/Repositories/Agent Tokens/Registries nav items | FR-050, 052–054, 058; N7 (nav), H1, X6 | d, g, h, j, k | Web, macOS |
| 109-j | `109-j-catalog-add-server` | `registries.SearchAll` + `Rank` over an internal `CatalogHit`; `GET /catalog/search` returning the distinct `CatalogResult` DTO (`toCatalogResult`; `ServerEntry` JSON untouched); `/add-server` (tabs Catalog · Paste · Import · Manual via `?tab=`; `?source=` stays the catalog-source filter); paste URL/command detection; secret toggle (per-kind keyring names `<server>-env-<name>`/`<server>-header-<name>`, never overwriting an existing entry; `secret_like` = registry flag OR name rule) + keyring availability; `/repositories` redirect; Settings → Catalog sources; macOS Add Server sheet with Catalog/Paste + toggle; CLI `catalog` group, `upstream add --secret-env/--secret-header`, `registry search|add` deprecation; MCP `search_servers` registry optional | FR-007 (catalog), 060–067; N3, C1, S7, X4 | a | all |
| 109-k | `109-k-activity-scope-filters` | `useScopeQuery` with **all** parameters (`profile`/`client`/`token` registered hidden until `features.scope_filters` — merged here from Spec 108's former 108-j), the full link map incl. its hidden 108-target rows; Activity views + `/sessions` redirect + folding + column hiding + block reason + Duration; Tools/Usage/Servers scope-aware (Review follows in 109-g); chart-bar and Tools-row deep links (server-card links are 109-e's, Home-strip links 109-d's); token-savings estimate; macOS `ScopeFilter` + Activity segments/filters; CLI `--view`, `--from/--to` (sole owner; Spec 108-e does not register them); backend `unsupported_scope_filter` gate for `profile`/`client`/`token` (and `agent` where no handler honours it) until Spec 108-e fills the supported list (`internal/httpapi/scope_filters.go`; list and gate function created by whichever of 109-k/108-e merges first); `tool` URL value split into REST `server` + bare `tool`; `session` routed by the `ws-` prefix | FR-016 (Activity), 070–075, 080–083, FR-080a; A1, A2, N5; M3, C3 | a | Web, macOS, CLI, REST |
| 109-m | `109-m-parity-docs` | Terminology parity test (Go enums → `contracts.ts` → Swift fixtures → CLI `--help-json`); SC-002 attention parity test; SC-003 forbidden-rendering test; SC-005 `unquarantine` grep test; contradiction-register check; SC-001 traceability check (T148a); FR-091 parity-matrix walk (T148b); Playwright `navigation-consistency.spec.ts` into the release-gate sweep; docs; live run of every story on one isolated instance | FR-090–092; SC-001–012 | a–k | all |
| 109-l | `109-l-profiles-integration` | 108 warnings as attention kinds (Spec 108's names); end-to-end un-hiding tests for the parameters, link rows, sidebar entry and header slot wired in 109-i/109-k; parity rows (the Viewing chip and Clients-row profile controls are Spec 108's) | FR-093 | 108-f, 108-i, 108-j, 108-k, 109-i | Web, macOS, Go (attention kinds) |

## Design — six mechanisms

### 1. Compute once, present everywhere
Each cross-surface judgement has one Go function: health `status`, tool `tier`, `attention.Compute`, the review composer, presence, and `registries.Rank`. Enums and labels are exported by `cmd/generate-types` to `contracts.ts`, and golden JSON fixtures are decoded by Go, vitest and XCTest. Surfaces render; they never re-derive. That is what makes SC-002, SC-003, SC-004 and SC-008 testable as equalities.

### 2. Event-maintained snapshots
The attention subscriber listens to `servers.changed`, tool-approval, connect and session events, recomputes (debounced 250 ms), swaps an atomic pointer, and emits `attention.changed` only when the id set changes. Because three conditions are time-based (60 s `connecting`/`error`, 5 min `client_never_seen`), each recompute also arms one timer for the earliest pending threshold crossing (≤ 30 s), so an item appears on time on an instance where no event fires (codex round 1). Presence counts come from SessionStore and the usage aggregate. No request scans the activity log.

### 3. One review flow, four verbs
The review composer reads approval records (with captured annotations) plus the latest scan. The four verbs map to existing routes. The one new capability, `security/approve` with `block[]`, makes "approve these 12, block those 2" atomic: the block records are written in the baseline transaction before the unquarantine, so no call can reach a `block` tool at any instant (codex round 1; race test T078b). The review composer passes the server summary through the shared secret redaction for every caller. The macOS approval path is corrected first (109-f) because it is a security divergence.

### 4. Shared components instead of parallel UIs
`ClientConnectList`, `ImportServers`, `ReviewScreen`, `AttentionList` and `CatalogSearch` are each used in every place the audit found a duplicate UI (wizard, modal, Overview, Add Server). macOS mirrors each with one SwiftUI view used from the tray, the window and the sheets (the pattern `ConnectClientPresentation` already uses).

### 5. URL is the state
`useScopeQuery` owns every contract parameter, `profile`/`client`/`token` included (hidden until `features.scope_filters`). Tabs and views use `?tab=`/`?view=`. Every old route redirects and keeps its query. macOS carries the same values in `ScopeFilter`. Spec 108 supplies the backend and the availability signal; it never registers parameters or links.

### 6. Ownership split with Spec 108
See spec.md "Coordination with Spec 108" and research D1/D25. Every shared artifact has exactly one owner; the other spec only integrates through the owner's extension points (slots, row decoration, the `scope_filters` signal). "First to merge creates" is withdrawn.

## Risks

| Risk | Mitigation |
|---|---|
| Parallel work with Spec 108 on the same files | single-owner table (spec.md Coordination, D25); the named extension points; cross-spec edges in tasks.md; each PR body states which side of the split it implements |
| Review payload leaks a secret baked into a command or URL | the composer uses the shared redaction path; T077b parity test for admin and scoped callers |
| Attention under-reports on a quiet instance | threshold timer (D2); T053 fake-clock test |
| Reversing #1044's landing page annoys returning users | The usage strip rises when nothing needs attention; `/usage` is one click away in Monitor |
| Redirects break bookmarked deep links | Redirect table with query preservation; a Playwright test per redirect |
| Rendering untrusted descriptions | Text-only rendering + an XSS-shaped vitest fixture + `Text(verbatim:)` on macOS |
| Catalog fan-out slows Add Server | 5 s per-source timeout, cached sources, sections served from cache; the UI streams nothing and shows unavailable sources |
| CLI output drift breaks scripts | Columns appended, flags aliased, `activity list` default unchanged, goldens |
| macOS approval change surprises users (new confirmation) | Same dialog text as the Web UI; release note |
| `status` enum misses a calculator branch | Exhaustive table test over every branch in `calculator.go` (all existing `calculator_test.go` inputs re-run with `status` assertions) |
| Attention count flaps during reconnect storms | 60 s threshold for `connecting`/`error`; debounce; id-set comparison before emitting |

## Complexity Tracking

| Violation | Why Needed | Simpler Alternative Rejected Because |
|---|---|---|
| Two new derived services (attention, presence) plus one composer (review) | FR-003 requires one count on four surfaces; X1 shows the per-surface predicates drift | Per-surface filtering is the defect being fixed |
| `security/approve` gains `block[]`, written in the baseline transaction before the unquarantine | Keeps "approve with exceptions" free of any window where excluded tools are approved and enabled (the earlier "after unquarantine in the same call" wording still had one — codex round 1) | Two calls, or block-after-unquarantine in one call: during the window, blocked tools would be callable; a lock across approval and dispatch would add a lock to the call path |

## Constitution Check — post-design

Re-evaluated after data-model and contracts: all gates pass, with no deviations. No new dependencies, no new locks, no polling, no config-schema change.

## Phase 2 hand-off

`tasks.md` lists the tasks per PR in merge order: failing tests first, then implementation, then the PR's test plan and live-verification recipe ([quickstart.md](quickstart.md)), then zcode review.

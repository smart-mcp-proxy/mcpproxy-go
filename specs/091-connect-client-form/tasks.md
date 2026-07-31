# Tasks: Native Connect Client Form

**Input**: Design documents from `/specs/091-connect-client-form/` (spec.md rev 2.2, plan.md, research.md D1–D10, data-model.md, contracts/api-deltas.md)
**Tests**: Strict TDD — every implementation task has a distinct preceding failing-test task (a test failing to compile against a not-yet-existing symbol counts as red).

Swift paths relative to `native/macos/MCPProxy/` (sources `MCPProxy/`, tests `MCPProxyTests/`); Go paths repo-relative. The Go track (Phase 2) and the Swift model work (T012+) are parallelizable — Swift tests synthesize JSON and never need a live core.

## Phase 1: Setup

- [ ] T001 Verify green baseline on branch 091-connect-client-form: `cd native/macos/MCPProxy && swift test` and `go test ./internal/connect/... ./internal/httpapi/... -count=1` pass before any change (record pre-existing failures in specs/091-connect-client-form/verification/baseline.md)

## Phase 2: Foundational — Go contract deltas (blocking for live use; parallel to Swift model tasks)

- [ ] T002 [P] Write failing tests in internal/connect/summary_test.go: `EntrySummary` built ONLY from non-secret projections (entry name — including the ADOPTED equivalent entry's actual key via the same lookup the write performs —, type, endpoint with query params AND userinfo stripped, command, header NAMES, env NAMES); plus secrecy cases in internal/connect/preview_test.go feeding entries with a rotated X-API-Key header value, `--header` arg keys, bearer/env secrets, `?apikey=` URLs, and `user:pass@` URLs, asserting none of those values appear anywhere in the serialized preview response (research D2, contracts §1)
- [ ] T003 Implement internal/connect/summary.go (`EntrySummary`, adoption-aware resolution) and wire `ExistingEntrySummary` into ConnectPreview in internal/connect/preview.go; T002 green
- [ ] T004 [P] Write failing tests in internal/connect/token_test.go: HMAC-SHA256 with injected key over canonical LENGTH-PREFIXED encoding; deterministic per key; distinct tokens for — file absent↔present, resolved entry absent↔present, any raw byte change of the resolved entry (incl. a masked credential value change and an ADOPTED-entry change under a different key), any pending-entry change (API-key rotation / require_mcp_auth toggle / listen-address change); different key ⇒ different token; token contains no substring of any secret (research D1)
- [ ] T005 Implement internal/connect/token.go (`DerivePreconditionToken(key, configPath, fileExists, resolvedEntryName, rawResolvedEntry, pendingEntry)`) with a per-core-instance random in-memory key owned by the connect service; wire `PreconditionToken` into the preview response in internal/connect/preview.go; T004 green
- [ ] T006 [P] Write failing tests: internal/connect/preview_test.go asserts `ConnectRefusal` is populated for a non-create-capable client (OpenCode) with an absent config using the SAME guard the write uses, and empty otherwise (research D8)
- [ ] T007 Implement the refusal wiring in internal/connect/preview.go (extract/reuse the write's absent-config guard from internal/connect/connect.go); T006 green
- [ ] T008 [P] Write failing tests: internal/connect/connect_test.go — write with valid token succeeds (create/add/replace incl. adopted-entry replace); stale token (each drift class) ⇒ discriminated conflict, file byte-identical after; `force=true` + stale token still refuses; absent token = legacy behavior; internal/httpapi/connect_test.go — POST body accepts `precondition_token`, 409 body carries `"action":"precondition_failed"` for drift vs `"already_exists"` for the legacy case, preview response serializes the three new fields (contracts §1–2)
- [ ] T009 Implement token validation in internal/connect/connect.go (resolve current raw state with the same adoption lookup + rebuild pending entry, compare, refuse-before-backup) and the request field + 409 discriminator + swagger annotations in internal/httpapi/connect.go; run `make swagger`; T008 green, `make swagger-verify` green
- [ ] T010 [P] Write failing SC-006 test in internal/server/connect_socket_e2e_test.go: start the server on a real Unix socket, POST /api/v1/connect/{client} over it, assert admin context reaches the gated write route end-to-end; and an agent-token TCP caller is rejected on the same route (research D10)
- [ ] T011 Make T010 green (test-only if the path already works; fix wiring if not); run `go test ./internal/connect/... ./internal/httpapi/... -count=1` and `go test ./internal/server -run ConnectSocket -count=1`

## Phase 3: User Story 1 — Connect a client from the tray (P1) 🎯 MVP

**Goal**: Menu item → native form → stat-only list → detail+preview on selection → token-guarded connect.

**Independent test**: Drive the model with synthesized API responses; verify the preview-before-write structural guarantee and the connect flow; then the live journey via the manual protocol.

- [ ] T012 [P] [US1] Write failing decode tests in MCPProxyTests/ConnectModelsDecodingTests.swift: extended ClientStatus (`icon`, `server_name`, `access_state`, `remediation` all optional; unknown client id decodes generically), ConnectPreviewModel (three new fields incl. `existing_entry_summary` object and `connect_refusal`), `changeKind` derivation for all six cases (refused / create / add / replace incl. adopted-name summary / blockedByAccess for unreadable and denied), safety-net statement derivation, credential notice flag (data-model)
- [ ] T013 [US1] Implement the models (extend ClientStatus decode in MCPProxy/API/Models.swift or a new MCPProxy/API/ConnectModels.swift; ConnectPreviewModel with changeKind) — T012 green
- [ ] T014 [P] [US1] Write failing tests in MCPProxyTests/APIClientConnectTests.swift: exact URLs/bodies for `clientDetail(id)`, `connectPreview(id, serverName)`, `connect(id, serverName, force, preconditionToken)`, `undoConnect(id, backupIdentity)`, `disconnect(id)`; `transportKind` reports `.unixSocket` vs `.tcp` from the configured endpoint; mutating calls use strict-socket mode — with the socket gone, the request FAILS instead of falling back to TCP (red: none of these symbols exist)
- [ ] T015 [US1] Implement the five APIClient methods + `transportKind` in MCPProxy/API/APIClient.swift and the strict-socket request option in MCPProxy/Core/SocketTransport.swift (research D6); T014 green
- [ ] T016 [P] [US1] Write failing state-machine tests in MCPProxyTests/ConnectClientModelTests.swift: list loading/loaded/coreUnreachable (2 s poll transitions with an injected clock); selection → detail+preview fetch; the Connect control EXISTS iff preview is `.resolved` for the current (selection, entryName) AND changeKind ∉ {refused, blockedByAccess} (SC-002); entry-name edit resets preview; replace flows send force=true WITH the token, never force alone; 409 `precondition_failed` → `.conflict` → automatic single re-preview (no loop on `already_exists`, which maps to `.failed`); action buttons disabled in-flight; off-socket ⇒ mutating controls disabled with explanation; core refusal (OpenCode absent) ⇒ Connect control absent + verbatim reason (data-model invariants)
- [ ] T017 [US1] Implement MCPProxy/Views/ConnectClientModel.swift (@MainActor reducer over the APIClient seam); T016 green
- [ ] T018 [US1] Write failing presentation tests (red = compile failure acceptable): MCPProxyTests/ConnectClientModelTests.swift additions asserting the menu-item action posts the shared presentation route, and the view exposes stable accessibility identifiers for list/preview/actions (FR-010)
- [ ] T019 [US1] Implement MCPProxy/Views/ConnectClientView.swift (thin SwiftUI over the model: list with state labels, preview pane — pending entry text, path, existing-entry summary, safety-net statement, credential notice, refusal — and action bar) plus the "Connect Client…" menu item beside "Add Server…" in MCPProxy/MCPProxyApp.swift with direct sheet presentation (no asyncAfter chains, research D5); T018 and full `swift test` green

**Checkpoint**: MVP — the full connect journey works natively.

## Phase 4: User Story 2 — Client configuration state at a glance (P2)

- [ ] T020 [P] [US2] Write failing tests in MCPProxyTests/ConnectClientModelTests.swift: list rows render "config present" / "no config found" / unsupported-disabled (+reason) from the stat-only aggregate WITHOUT any detail fetch; selecting resolves connected + entry name; after a connect/disconnect completes only the affected client's state refetches (FR-002, US2 scenarios)
- [ ] T021 [US2] Implement the list-state presentation + post-action refresh in MCPProxy/Views/ConnectClientModel.swift and ConnectClientView.swift; T020 green

## Phase 5: User Story 3 — Undo and disconnect safely (P2)

- [ ] T022 [P] [US3] Write failing tests in MCPProxyTests/ConnectClientModelTests.swift: undo appears ONLY after a `.succeeded` connect in this form instance (carrying that connect's backup identity; created-file case = removal), disappears on use and on form close; disconnect requires a confirmation naming config file + entry before the request fires (US3 scenarios)
- [ ] T023 [US3] Implement session-scoped undo + disconnect confirmation in MCPProxy/Views/ConnectClientModel.swift and ConnectClientView.swift; T022 green
- [ ] T024 [P] [US3] Write failing test: the dashboard's connect action routes into the shared Connect Client presentation path and the legacy preview-less sheet is gone (red: assert against the new routing symbol) in MCPProxyTests/ConnectClientModelTests.swift or a small MCPProxyTests/DashboardRoutingTests.swift
- [ ] T025 [US3] Route MCPProxy/Views/DashboardView.swift (~line 1275 legacy connect sheet) into the new form's presentation path, deleting the preview-less flow (FR-012); T024 and full `swift test` green

## Phase 6: Polish & Cross-Cutting

- [ ] T026 Gates + docs: run `./scripts/run-linter.sh`, `go test ./internal/...`, `cd native/macos/MCPProxy && swift test`, `make swagger-verify`; document the two connect-surface deltas in the connect feature docs (docs/features/, wherever connect is documented — create a short section if absent); validate specs/091-connect-client-form/quickstart.md commands; mark manual protocol status (NOT RUN unless actually run); verify all checkboxes T001–T025, then this one

## Dependencies & Execution Order

- Phase 1 first. Phase 2 (Go) and the Swift chain T012→T019 can run in PARALLEL (Swift tests synthesize JSON); T011 must complete before any LIVE (manual-protocol) verification, not before Swift unit work.
- Within Phase 2, [P] test tasks (T002/T004/T006/T008/T010) touch distinct test files and may be authored in parallel; implementation tasks are sequential where they share preview.go (T003 → T005 → T007) and connect.go/httpapi (T009 → T011).
- Swift: T012→T013→T014→T015→T016→T017→T018→T019 strictly sequential (shared files); US2 (T020–T021) and US3 (T022–T025) follow US1 and are sequential with each other (same model/view files).
- T026 last.

## Implementation Strategy

MVP = Phases 1–3. US2 → US3 → polish. Each pair ends green before the next; commits per pair, conventional format, no AI attribution. If the pre-commit hook demands ROADMAP.md regeneration on tasks.md checkbox changes, run `python3 scripts/gen-roadmap.py` and stage it.

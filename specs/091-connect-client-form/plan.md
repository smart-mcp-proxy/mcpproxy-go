# Implementation Plan: Native Connect Client Form

**Branch**: `091-connect-client-form` | **Date**: 2026-07-31 | **Spec**: [spec.md](./spec.md)
**Input**: Feature specification from `/specs/091-connect-client-form/spec.md`

## Summary

A native tray path for connecting AI clients: a "Connect Client…" menu item opens a SwiftUI form that lists registry clients (stat-only, TCC-safe), resolves detail + a no-write preview on selection, and performs connect / session-scoped undo / disconnect with the preview-before-write discipline. Two additive backend changes carry it: the preview response gains the sanitized existing-entry content plus an opaque precondition token, and the connect write validates that token (conflict on drift). The legacy preview-less dashboard connect sheet is routed into the new form.

## Technical Context

**Language/Version**: Swift 5.9 (SwiftUI sheet + AppKit menu) + Go 1.25 (core)
**Primary Dependencies**: existing `internal/connect` package (registry, preview, connect, undo, backup), `internal/httpapi/connect.go` routes; Swift `APIClient` over Unix socket (admin context)
**Storage**: none new — client config files remain core-managed; token is stateless (HMAC/hash, not persisted)
**Testing**: `go test ./internal/connect/... ./internal/httpapi/...`, `swift test` (form state model, API model decoding), fixture-free unit tests
**Target Platform**: macOS 13+ (SwiftUI form uses existing app baseline; no 14.4-only API needed)
**Project Type**: Native macOS app + Go backend
**Performance Goals**: list opens instantly from the stat-only aggregate; per-client detail+preview resolve in one round-trip each
**Constraints**: zero client-config reads in the tray process; no config-content reads core-side on list (TCC); mutating actions require the socket transport; preview-before-write enforced structurally; session-scoped undo only
**Scale/Scope**: 8 registry clients today; one new SwiftUI view + view model; 2 additive backend fields

## Constitution Check

*GATE: evaluated pre-Phase-0 and re-checked post-design — PASS, no violations.*

- **I. Performance at Scale**: N/A-scale feature (8 clients); stat-only list avoids per-client file reads. PASS.
- **II. Actor-Based Concurrency**: no new goroutines beyond request handlers; Swift view model is @MainActor with async API calls. PASS.
- **III. Configuration-Driven Architecture**: tray keeps zero state (session-scoped undo state is UI-session state, explicitly specced); all mutations via core REST. PASS.
- **IV. Security by Default**: preview sanitization gets explicit no-raw-secrets backend tests; the precondition token is a non-reversible hash; connect-write endpoints stay gated (agent tokens rejected); socket stays 0600. PASS — this feature strengthens the security posture (no silent overwrites).
- **V. TDD**: state-model tests for every list/detail/preview/action state; backend tests for sanitization, token, conflict. PASS.
- **VI. Documentation Hygiene**: connect feature docs updated with the two new fields; swagger regenerated. PASS.
- **Core+Tray split**: form renders; core owns files. PASS.

## Project Structure

### Documentation (this feature)

```text
specs/091-connect-client-form/
├── spec.md, plan.md, research.md, data-model.md, quickstart.md
├── contracts/api-deltas.md
├── verification/manual-protocol.md
└── tasks.md   # Phase 2 (/speckit.tasks)
```

### Source Code (repository root)

```text
# Go core (additive deltas)
internal/connect/preview.go             # existing sanitized entry + precondition token in ConnectPreview
internal/connect/preview_test.go        # sanitization (no raw secrets) + token derivation tests
internal/connect/connect.go             # Connect accepts optional precondition token → conflict on drift
internal/connect/connect_test.go        # token validation: create/add/replace drift cases, absent-token back-compat
internal/connect/token.go               # NEW: token derivation (hash over file existence + raw entry state)
internal/connect/token_test.go          # determinism, non-reversibility shape, drift detection incl. masked-value drift
internal/httpapi/connect.go             # request body gains precondition_token; 409 on mismatch; swagger annotations
internal/httpapi/connect_test.go        # endpoint-level: preview fields present, conflict status code
oas/swagger.yaml, oas/docs.go           # make swagger

# Swift app (native/macos/MCPProxy/MCPProxy)
Views/ConnectClientView.swift           # NEW: the form (list → detail+preview → actions)
Views/ConnectClientModel.swift          # NEW: @MainActor state model (pure-testable reducer around APIClient calls)
Views/DashboardView.swift               # legacy connect sheet routed into the new form (FR-012)
MCPProxyApp.swift                       # "Connect Client…" menu item next to "Add Server…"; window/sheet plumbing
API/APIClient.swift                     # clientDetail, connectPreview, connect(with token/server_name), undo, disconnect
API/Models.swift (or ConnectModels)     # richer ClientStatus + ConnectPreview/ConnectResult models

# Swift tests (native/macos/MCPProxy/MCPProxyTests)
ConnectClientModelTests.swift           # NEW: every state/action of the model (FR-009 states, preview gating, undo scoping)
ConnectModelsDecodingTests.swift        # NEW: decode fixtures incl. unknown client ids, new preview fields
APIClientConnectTests.swift             # NEW: exact URLs/bodies incl. precondition token passthrough
```

**Structure Decision**: form logic lives in a view-model (`ConnectClientModel`) so the preview-before-write gating, session-scoped undo, and error surfacing are unit-testable without SwiftUI rendering; the view stays thin, mirroring how AddServerView is structured but with the state machine extracted (AddServerView's inline state is the pattern's known weakness).

## Complexity Tracking

No constitution violations; table intentionally empty.

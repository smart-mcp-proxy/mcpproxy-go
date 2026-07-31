# Research — Native Connect Client Form (Spec 091)

Unknowns were resolved against the codebase during the spec's 4 Codex review
rounds. Decisions:

## D1. Precondition token: stateless hash, derived and validated core-side

- **Decision**: `internal/connect/token.go` derives
  `sha256(config_path | file_exists | entry_name | raw_entry_json)` (canonical
  serialization; raw pre-sanitization values), hex-encoded, returned by the
  preview and echoed by the write. Validation recomputes at write time and
  returns a conflict when different. Absent token → today's behavior
  (backward-compatible). Not persisted, not reversible, no server-side state.
- **Rationale**: detects every drift class the spec names (file appears after
  absent-preview, file disappears, raw entry changes — including changes that
  sanitization masks) without new storage or endpoints.
- **Alternatives**: mtime/size (misses same-size edits); storing a revision
  server-side (state + expiry semantics for no benefit).

## D2. Sanitized existing entry: reuse the pending-entry masking

- **Decision**: `preview.go` already masks credentials when rendering the
  *pending* entry (`maskAPIKey` path); the existing entry is rendered through
  the same masking routine into a new `existing_entry_text` field, only when
  `entry_exists`. Backend tests feed configs whose entries carry API keys,
  bearer headers, env secrets, and credential-bearing URLs and assert none of
  the raw values appear anywhere in the serialized preview response.
- **Rationale**: one masking implementation, provably applied to both texts.

## D3. Aggregate list stays stat-only; detail resolves on selection

- **Decision**: the form's list binds to `GET /api/v1/connect` exactly as-is
  (config-presence only, `access_state` left `unknown` — the endpoint's
  deliberate TCC-prompt-avoidance design, asserted by existing API tests).
  Selecting a client fires `GET /api/v1/connect/{client}` (detail: exists,
  connected, server_name, access_state) then
  `GET /api/v1/connect/{client}/preview?server_name=…`. No eager per-client
  fan-out.
- **Rationale**: spec FR-002/US2; one user-initiated content read per selection.

## D4. Swift shape: extracted @MainActor view model, thin SwiftUI view

- **Decision**: `ConnectClientModel` owns the state machine:
  `list(loading|loaded|coreUnreachable)` → per-client
  `detail(loading|resolved)` + `preview(loading|resolved|failed)` →
  `action(inFlight|conflict|failed|succeeded)` + `undoAvailable(backupIdentity)`.
  The Connect control is *derived* — it exists only in states where a resolved
  preview for the current (client, server_name) inputs is present (SC-002's
  structural guarantee). Input changes reset preview state. The view renders
  states; `AddServerView`'s inline-state pattern is deliberately not repeated.
- **Rationale**: SC-002/SC-005 demand unit-testing the gating without
  rendering; a reducer-style model is the testable seam.

## D5. Menu + window plumbing: follow the Add Server path, minus its timing hack

- **Decision**: menu item "Connect Client…" beside "Add Server…" in
  `MCPProxyApp.swift` (~line 745 block). It opens the form as a sheet on the
  MainWindow like AddServerView, but via a direct presentation call rather
  than the double `DispatchQueue.asyncAfter` (0.3s/0.8s) notification chain
  used by `showAddServer()` — that sequencing is a documented fragility we do
  not copy. The dashboard's legacy connect sheet (`DashboardView.swift` ~1275)
  is replaced by a call into the same presentation path (FR-012).
- **Rationale**: one preview-first implementation, no preview-less native path
  left behind.

## D6. Off-socket behavior

- **Decision**: the model consults the existing transport identity
  (`APIClient` knows whether it talks over the Unix socket); when not on
  socket, Connect/Undo/Disconnect controls render disabled with an explanatory
  string; list/detail/preview stay functional. Verified during spec review:
  socket connections get admin context server-side (`listener_unix.go` tagging
  → auth conversion), and write routes reject restricted agent tokens.
- **Rationale**: spec's non-socket edge case; avoids a confusing 403 path.

## D7. Reachability polling

- **Decision**: while the core is unreachable the model polls the list every
  2 s (spec FR-013) using a cancellable task tied to the form's lifetime; on
  success it transitions to `loaded` without user action.

## D8. OpenCode (non-create-capable) absent case

- **Decision**: no client-side capability table. The preview still renders;
  Connect stays enabled for absent-file clients; when the core refuses
  (OpenCode's guard), the model surfaces the refusal verbatim in the action
  error state, which the spec defines as the non-connectable presentation.
  The manual protocol (9d) checks it; a model unit test simulates the refusal.
- **Rationale**: capability knowledge stays core-side; no drift when the
  registry changes.

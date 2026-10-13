# Feature Specification: Connect step trust — preview, backup visibility, and one-click undo

**Feature Branch**: `078-connect-trust-preview`
**Created**: 2026-07-02
**Status**: Draft
**Input**: User description: "Make the onboarding Connect step trustworthy — show the exact change that will be written to each AI client's config before writing it, always create and visibly surface a timestamped backup, offer a one-click undo that reverts the change, and explain in plain language (including the macOS App-Data prompt) what mcpproxy is about to touch — so users stop skipping the highest-drop step in the onboarding funnel."

## Overview

The onboarding wizard's "Clients" step — where mcpproxy registers itself as an MCP
server inside the user's AI client config files (`~/.claude.json`, Claude Desktop
config, Cursor/VS Code settings, Codex `config.toml`, Gemini, OpenCode) — is the
biggest drop in the setup funnel. Telemetry (CI-filtered + deduped, 2026-07-02):
of wizard-engaged users, **72.4% skip** the connect step and only **13.8%
complete** it, yet completing it correlates with ~50% two-week retention versus
6% for non-engaged users. The overall funnel loses 28% between first-run (1,237)
and client-connected (854).

The working hypothesis is a **trust gap**: users are asked to let mcpproxy modify
config files that belong to other apps, but the step does not show *what* will be
written, does not show a diff, and does not make the automatic backup visible.
On macOS the write path can also trigger a "wants to access data from other apps"
privacy prompt with no forewarning inside the wizard.

This feature closes the trust gap at the exact place the user hesitates: preview
the change before writing, surface the backup path after writing, offer a visible
one-click undo, and use plain-language copy (including a pre-emptive macOS
permission explanation) so the user understands precisely what mcpproxy touches
and that nothing else in the file changes.

### Context (current behavior, verified)

- **Backups already exist but are invisible in the UI.** Every connect and
  disconnect writes a timestamped backup before modifying the file
  (`internal/connect/backup.go:18`, named `<config>.bak.<YYYYMMDD-HHMMSS>`,
  same directory, same file mode), returned as `ConnectResult.BackupPath`
  (`internal/connect/connect.go:31`, JSON `backup_path`). The **CLI** prints it
  (`cmd/mcpproxy/connect_cmd.go:255-256`), but the **Web UI does not**: both
  `ConnectModal.vue` and `OnboardingWizard.vue` render only
  `ConnectResult.Message` (the generic "MCPProxy registered in X as
  mcpproxy") and never show `backup_path`. There is **no retention/cleanup** of
  backups anywhere — they accumulate one-per-operation indefinitely and this is
  undocumented.
- **There is no preview/diff.** No API returns the exact JSON/TOML that *will*
  be written before the user confirms. The server entry is built server-side
  (`buildServerEntry` at `internal/connect/clients.go:185`, per-client shape:
  `{type:"http",url}` for claude-code/vscode, `{command:"npx",args:[...]}` bridge
  for Claude Desktop, `{url,type:"sse"}` for Cursor, etc.) and written
  immediately on `POST /api/v1/connect/{client}`. The user never sees the entry,
  the endpoint URL (which may embed `?apikey=`), or which key is added.
- **macOS TCC handling is reactive, not pre-emptive (Spec 075).**
  `GET /api/v1/connect` is stat-only and never prompts; per-client reads /
  connect / disconnect resolve `access_state` and a denial surfaces as `403` +
  remediation and an in-band `access_state="denied"` banner in `ConnectModal.vue`
  (denied-banner with copy-`tccutil` + re-check). But this only appears **after**
  the prompt has fired and been denied. The only pre-emptive hint anywhere is a
  tooltip on the ConnectModal "Check access" button ("may prompt on macOS",
  `ConnectModal.vue:88`). The **wizard** connect rows (`ClientRow` in
  `OnboardingWizard.vue:1114`) have **no** access-state handling and **no** TCC
  forewarning at all.
- **Disconnect is surgical but not surfaced as undo, and does not restore.**
  `Disconnect` deletes only the mcpproxy entry (`delete(serversMap, serverName)`,
  `internal/connect/connect.go:475,628`) and re-writes; it takes its own backup
  first but does **not** restore the pre-connect file. If Connect used
  `force=true` to overwrite a pre-existing entry, disconnect cannot bring the
  overwritten entry back. In the wizard, a connected client shows only a static
  "Connected" badge with **no** disconnect/undo control (`OnboardingWizard.vue`
  ClientRow); disconnect exists only in the separate `ConnectModal.vue`.
- The onboarding funnel steps and skip/complete telemetry are recorded by
  `onboarding.markConnectCompleted()` / `markConnectSkipped()`
  (`OnboardingWizard.vue:1035,1088`), the metrics this feature's success criteria
  are measured against.

## User Scenarios & Testing *(mandatory)*

### User Story 1 - See the exact change before it is written (Priority: P1)

A user on the wizard's Clients step is asked to connect an AI client. Before any
file is modified, they want to see precisely what mcpproxy will add: which file,
which key, and the exact entry (server name, endpoint URL, how the API key is
handled) — and confirm that nothing else in the file changes.

**Why this priority**: The absence of a visible change is the core of the trust
gap and the most direct lever on the 72.4% skip rate. A user who can see exactly
what will happen is far more likely to proceed than one asked to authorize a
blind edit to their AI client's config.

**Independent Test**: Request a connect preview for a client and confirm the
response returns the exact entry that a subsequent connect would write (same key,
same shape as `buildServerEntry`), scoped to that one file, without modifying the
file or creating a backup.

**Acceptance Scenarios**:

1. **Given** an installed, accessible client, **When** the user opens the connect
   step for it, **Then** a preview shows the target config path, the key that will
   be added, and the exact entry contents to be written, and the file is not
   modified.
2. **Given** the client config already contains other MCP servers, **When** the
   preview is shown, **Then** it makes clear that only the mcpproxy entry is
   added/updated and all existing entries are left untouched.
3. **Given** the endpoint URL embeds an API key, **When** the preview is shown,
   **Then** the API-key handling is represented honestly (e.g. shown or clearly
   masked) so the user knows a credential is being written.
4. **Given** the user confirms after previewing, **When** connect runs, **Then**
   the file written matches the previewed entry exactly.

---

### User Story 2 - Always-created backup is visible and understood (Priority: P1)

After connecting, the user wants to see that a backup was made and exactly where
it is, so they know the operation is reversible outside the app too.

**Why this priority**: The backup already exists but is invisible in the UI,
wasting the single strongest "you can undo this" signal. Surfacing it is low-cost
and directly reinforces trust at the moment of the edit.

**Independent Test**: Perform a connect via the Web UI and confirm the surfaced
result includes the backup path returned by the API; perform it against a
non-existent config (bridge client) and confirm the "no prior file to back up"
case is represented correctly.

**Acceptance Scenarios**:

1. **Given** a successful connect that modified an existing config, **When** the
   result is shown in the Web UI (wizard and standalone modal), **Then** the
   timestamped backup path is displayed to the user.
2. **Given** a connect that created a new config file (no prior file existed),
   **When** the result is shown, **Then** the UI states that there was no prior
   file to back up (rather than showing an empty/blank backup path).
3. **Given** repeated connect/disconnect operations, **When** the user reviews
   the documentation, **Then** the backup naming, location, accumulation, and
   retention behavior are documented.

---

### User Story 3 - One-click undo that reverts the change (Priority: P2)

After connecting, the user wants a single, visible action in the same place that
reverts what mcpproxy did — ideally restoring the file to its pre-connect state —
with the change shown again before it is undone.

**Why this priority**: A visible, trustworthy undo makes the connect decision
low-stakes ("I can take this back in one click"), which lowers the barrier to
trying it. Today the wizard offers no undo control at all and disconnect does not
restore the pre-connect file.

**Independent Test**: Connect a client, then invoke undo from the same surface;
confirm the mcpproxy entry is removed (or the pre-connect file restored) and the
client returns to a not-connected state, with a preview of what will be reverted
shown beforehand.

**Acceptance Scenarios**:

1. **Given** a client mcpproxy just connected, **When** the user triggers undo
   from the same connect surface (wizard row and standalone modal), **Then** the
   mcpproxy entry is removed and the client is reported not-connected.
2. **Given** connect overwrote a pre-existing entry of the same name, **When**
   undo runs, **Then** the pre-connect state is restored from the backup rather
   than leaving the overwritten entry lost.
3. **Given** the user requests undo, **When** the revert is about to run, **Then**
   the change to be reverted is shown (the same preview surface) before it is
   applied.
4. **Given** other MCP servers exist in the config, **When** undo runs, **Then**
   only mcpproxy's contribution is reverted and all other entries remain intact.

---

### User Story 4 - Plain-language trust copy, including the macOS prompt (Priority: P2)

A user reading the connect step wants a plain-language explanation of what will
happen — which file is touched, what is added, that nothing else changes, where
the backup goes — and, on macOS, an explanation of *why* the OS permission prompt
is about to appear, shown **before** it fires.

**Why this priority**: Even with a preview, users need the "why should I trust
this" narrative in words, and macOS users need the privacy prompt de-mystified
before it appears — an unexplained "wants to access data from other apps" dialog
is itself a reason to abandon the step.

**Independent Test**: Load the wizard connect step and confirm the trust copy is
present and names the file, the addition, the no-other-changes guarantee, and the
backup location; on macOS, confirm the App-Data prompt is explained before the
first read/write that could trigger it.

**Acceptance Scenarios**:

1. **Given** the wizard connect step, **When** it is shown, **Then** it states in
   plain language which file will be modified, what entry will be added, that
   nothing else in the file changes, and that a backup is created.
2. **Given** a macOS user about to connect (or check access on) a client whose
   config lives under another app's protected data, **When** the action is
   offered, **Then** the wizard explains why macOS may show a privacy prompt and
   what to choose, **before** the prompt fires.
3. **Given** a macOS user who has already been denied (Spec 075 `denied` state),
   **When** the step is shown, **Then** the existing remediation surface is
   preserved (this feature does not regress the Spec 075 denial banner).

---

### Edge Cases

- The client config already contains an entry named `mcpproxy` that the user
  created: the preview must show it as an **update/overwrite**, and undo must be
  able to restore the pre-connect entry (not silently discard it).
- A bridge client (e.g. Claude Desktop) with no existing config file: the preview
  shows the entry that would be created, and the "backup" result honestly states
  there was no prior file to back up.
- The config file is malformed/unparseable (Spec 075 `malformed`): the preview
  cannot be rendered from parsed content and must degrade to a clear message
  rather than a misleading empty diff.
- macOS App-Data denial (Spec 075 `denied`) while attempting a preview or undo:
  the same actionable remediation as connect/disconnect is surfaced; the preview
  is not shown as "no changes".
- TOML clients (Codex) vs JSON clients: the preview must render the correct
  format for the client being connected.
- The API key embedded in the endpoint URL must never be silently exposed in a
  way that surprises the user, nor hidden so thoroughly that they don't realize a
  credential is written — the preview represents it deliberately.
- Backups accumulate with no retention: repeated connect/disconnect must not
  break, and the accumulation behavior must be documented (and optionally bounded)
  rather than left as a silent surprise.

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: The system MUST provide a way to preview the exact change a connect
  would make to a given client — the target config path, the key that will be
  added or updated, and the exact entry contents — **without** modifying the file
  or creating a backup.
- **FR-002**: The preview MUST be derived from the same entry construction used by
  the actual write (`buildServerEntry`) so that what is previewed equals what is
  written for the same client and configuration.
- **FR-003**: The preview MUST make explicit that only the mcpproxy entry is
  added/updated and that all other content in the config file is left unchanged,
  distinguishing a "create" from an "overwrite of an existing same-named entry".
- **FR-004**: The preview MUST represent API-key handling honestly (shown or
  clearly indicated/masked) so the user is aware a credential is written into the
  client config, without accidentally leaking it in logs or telemetry.
- **FR-005**: Connect MUST continue to create a timestamped backup before
  modifying an existing config (preserving current behavior) and the operation
  result MUST carry the backup path.
- **FR-006**: The Web UI (both the onboarding wizard connect step and the
  standalone Connect modal) MUST display the backup path to the user after a
  successful connect, and MUST clearly represent the "no prior file to back up"
  case rather than showing a blank path.
- **FR-007**: The system MUST offer a one-click undo of a connect from the same
  surface where the connect was performed (wizard row and standalone modal),
  reverting mcpproxy's contribution and returning the client to a not-connected
  state.
- **FR-008**: Undo MUST, when connect overwrote a pre-existing same-named entry,
  restore the pre-connect state from the backup rather than leaving the prior
  entry lost; in the common case (mcpproxy entry newly added) it MUST remove only
  the mcpproxy entry and leave all other entries intact.
- **FR-009**: Undo MUST show the change to be reverted (the same preview surface)
  before applying it.
- **FR-010**: The wizard connect step MUST present plain-language trust copy
  naming the file to be modified, the entry to be added, the no-other-changes
  guarantee, and the backup location.
- **FR-011**: On macOS, the wizard MUST explain why an App-Data privacy prompt may
  appear **before** the first read/write that could trigger it, in addition to the
  existing post-denial remediation (Spec 075) which MUST be preserved.
- **FR-012**: The preview, backup-path, and undo behavior MUST degrade gracefully
  for the Spec 075 access states (`absent`, `malformed`, `denied`): a preview MUST
  NOT be presented as "no changes" when the config could not be read, and a denial
  MUST surface the existing remediation.
- **FR-013**: The preview and undo MUST support both JSON clients and the TOML
  client (Codex), rendering the correct format per client.
- **FR-014**: Backup retention/accumulation behavior MUST be documented; if a
  retention bound is introduced it MUST be conservative and MUST NOT delete a
  backup that a pending undo could still need.
- **FR-015**: The existing Connect REST/CLI surface MUST remain backward
  compatible; new capabilities MAY add endpoints/fields/flags but MUST NOT remove
  or repurpose existing ones (preserving Spec 075 additive compatibility).
- **FR-016**: The onboarding connect-step telemetry (complete/skip transitions)
  MUST continue to be recorded so the funnel effect of this feature is measurable.
- **FR-017**: All new behavior MUST be covered by automated tests, including
  preview-equals-write equivalence, backup-path surfacing, and undo restore paths
  (new-add and overwrite cases).

### Key Entities *(include if feature involves data)*

- **Connect preview**: For one client — the target config path, the config format
  (JSON/TOML), the key to be added/updated, the exact entry to be written, and an
  action classification (create vs overwrite-existing).
- **Connect result**: Existing outcome of a connect/disconnect — success, action,
  config path, and the **backup path** (now surfaced in the UI).
- **Backup record**: The timestamped copy created before a write — its path,
  origin config, and timestamp; the basis for restore-based undo and the subject
  of documented retention.
- **Undo request/result**: A revert of a prior connect — whether it removed the
  mcpproxy entry or restored from backup, and the resulting client state.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: The wizard `connect_step` completion rate among wizard-engaged users
  increases from the 13.8% baseline (2026-07-02) after this feature ships.
- **SC-002**: The wizard `connect_step` skip rate among wizard-engaged users
  decreases from the 72.4% baseline (2026-07-02).
- **SC-003**: The overall first-run → client-connected funnel loss decreases from
  the 28% baseline (1,237 → 854).
- **SC-004**: For every supported client, the previewed entry is byte-for-byte
  equal to the entry a subsequent connect writes (same key, same shape), verified
  by test across JSON and TOML clients.
- **SC-005**: 100% of successful Web UI connects that modified an existing config
  display the backup path to the user; the "no prior file" case is shown
  distinctly (never a blank path), verified by test.
- **SC-006**: Undo returns the client to not-connected in 100% of tested cases,
  and restores the pre-connect state when connect overwrote a pre-existing
  same-named entry, verified by test.
- **SC-007**: On macOS, the App-Data prompt explanation is presented before the
  first prompt-triggering action in the wizard, and the Spec 075 post-denial
  remediation continues to pass its existing tests (no regression).
- **SC-008**: The existing Connect REST/CLI contract and Spec 075 tests continue
  to pass with no breaking changes.

## Assumptions

- The exact entry that will be written can be produced deterministically from the
  current listen address, API key, and client id ahead of the write, because the
  live write already builds it deterministically via `buildServerEntry`.
- Reading a config to render an "overwrite vs create" preview is an explicit,
  user-initiated action and is therefore an acceptable place for the Spec 075
  on-demand read (and thus a macOS prompt), consistent with the existing
  per-client status/connect/disconnect reads.
- Surgical entry removal is the correct default for undo in the common case (a
  newly added mcpproxy entry); backup-based restore is reserved for the overwrite
  case where surgical removal would lose the user's prior entry.
- Telemetry for the connect step is already captured (complete/skip) and is the
  authoritative measure for the funnel success criteria.
- The API key written into client configs is a real credential and must be handled
  as sensitive in previews, logs, and telemetry.

## Out of Scope

- Adding new client adapters or extending the set of supported clients.
- Changing the connect protocol, endpoint URL scheme, or the per-client entry
  shapes produced by `buildServerEntry`.
- Changes to app signing, notarization, or entitlements, or automating the
  granting of any macOS privacy permission (Spec 075 boundary preserved).
- Server-edition multi-user connect flows.
- A general-purpose config-file version history beyond the connect/undo backups.

## Commit Message Conventions *(mandatory)*

### Issue References
- ✅ **Use**: `Related #NNN` - Links the commit to the issue without auto-closing
- ❌ **Do NOT use**: `Fixes #`, `Closes #`, `Resolves #`

### Co-Authorship
- ❌ **Do NOT include** `Co-Authored-By: Claude` or "Generated with Claude Code" trailers.

### Example Commit Message
```
feat(connect): preview change, surface backup path, one-click undo

Related #NNN

The onboarding connect step now shows the exact entry that will be written
to each client config before writing, displays the timestamped backup path
after connect, and offers a one-click undo that removes mcpproxy's entry
(or restores from backup when an existing entry was overwritten). Plain-
language trust copy explains what is touched and, on macOS, why the App-Data
prompt appears — closing the trust gap behind the funnel's biggest drop.

## Changes
- Connect preview (path + key + exact entry; create vs overwrite) — no write
- Web UI surfaces backup_path after connect (wizard + modal)
- One-click undo: surgical remove, backup restore on overwrite
- Pre-emptive macOS App-Data explanation; Spec 075 remediation preserved
- Backup retention documented

## Testing
- preview-equals-write equivalence (JSON + TOML clients)
- backup-path surfacing incl. no-prior-file case
- undo restore paths (new-add and overwrite)
- Spec 075 + existing Connect contract tests pass
```

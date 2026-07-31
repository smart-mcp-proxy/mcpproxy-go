# Feature Specification: Native Connect Client Form

**Feature Branch**: `091-connect-client-form`
**Created**: 2026-07-31
**Status**: Draft (revised after Codex review round 1)
**Input**: User description: "Add client must open macOS app form — a native tray menu item and form for connecting AI clients (Claude Code, Cursor, etc.) to the proxy, replacing the current Web-UI-only flow."

## Context

The proxy already knows how to connect AI client applications: a client registry (Claude Code, Claude Desktop, Cursor, Windsurf, VS Code, Codex, Gemini, OpenCode) with per-client status, a no-write preview, a config-writing connect action that backs up an existing client config first, plus undo and disconnect. Today the only preview-first UI is the Web UI's Connect modal; the native app has a legacy dashboard sheet that connects *without* preview. Field feedback: "Add server, Add client must open macOS app form."

A standing product rule (learned from earlier feedback on this exact flow) applies: any config-mutating connect flow must show the pending change and the backup consequences BEFORE the action button.

Two platform realities shape this design:

- The aggregate client list is deliberately cheap (file-existence checks only) so that merely opening a list does not trigger macOS data-access prompts for every client's container. Reading a config's *contents* — needed to know "connected or not" and to build a preview — happens only for a client the user explicitly selects.
- Undo depends on the backup name returned by the connect that created it; the core keeps no cross-session undo state. Undo is therefore an in-form affordance for the connect you just performed (identical to the Web UI's behavior).

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Connect a client from the tray (Priority: P1)

The user picks "Connect Client…" from the tray menu. A native window lists the known AI clients, showing which have a config file present. They select Claude Code; the form reads its state (connected or not) and shows a preview: the exact entry that will be written, the config file path, whether an existing entry is being replaced (and its current content), and what the safety net is — a timestamped backup for an existing file, or "a new file will be created; Undo removes it" when none exists. They press Connect, and the row flips to connected.

**Why this priority**: This is the requested capability — the entire feature exists so this journey never leaves the native app.

**Independent Test**: With a core running, open the form, select a client whose config exists, verify the preview renders the pending entry, path, and backup consequence before any write occurs, connect, and verify the client's status updates.

**Acceptance Scenarios**:

1. **Given** the tray menu is open, **When** the user chooses "Connect Client…", **Then** a native window opens listing every registry client with name, icon, and config-presence state — no browser is involved, and no client config *contents* are read yet.
2. **Given** the user selects a client, **When** its detailed state loads, **Then** the form shows whether it is currently connected and renders the preview: the entry text that will be written, the config path, and — when an entry already exists — the existing entry being replaced.
3. **Given** the preview is displayed, **When** the user presses Connect, **Then** the write happens (backup first when the file existed), and the form shows the refreshed state without being reopened.
4. **Given** the selected client's config file does not exist, **When** the preview renders, **Then** it states that a new file will be created at the path and that Undo will remove that file (no pre-existing content exists to back up).
5. **Given** the core is unreachable, **When** the form opens, **Then** it shows a clear "core not running" state, retries in the background, and populates when the core becomes reachable without reopening.
6. **Given** a client the registry marks unsupported on this platform, **When** the list renders, **Then** the client appears disabled with the reason, not hidden.

---

### User Story 2 - See client configuration state at a glance (Priority: P2)

The user opens the form to check which of their AI tools have a config the proxy recognizes. The list answers with the cheap truth (config present / no config found / unsupported), and the user drills into any client to see the authoritative state (connected to this proxy or not, and under which entry name) — an explicit, user-initiated read.

**Why this priority**: Complements the tray's Clients presence section (spec 090): presence says who *talked* recently; this form says who is *configured* to talk.

**Independent Test**: Seed config files for some clients and not others; open the form; verify list states come from existence checks only; select a client and verify the connected/not-connected resolution appears only then.

**Acceptance Scenarios**:

1. **Given** clients with and without config files, **When** the form opens, **Then** rows show "config present" / "no config found" respectively, and no config contents have been read.
2. **Given** a selected client whose config contains the proxy entry, **When** its detail loads, **Then** the row shows connected and the entry name in use.
3. **Given** the form is open, **When** a connect or disconnect completes, **Then** the affected client's state refreshes from the core — the tray process never reads or parses client config files itself.

---

### User Story 3 - Undo or disconnect safely (Priority: P2)

After connecting, the user changes their mind. While the form remains open, Undo reverses the connect they just performed: restoring the timestamped backup, or removing the file the connect created. Disconnect (for any connected client, any time) removes the proxy entry after a confirmation that names the file and the entry.

**Why this priority**: A config-writing feature without a visible way back teaches users not to trust it.

**Independent Test**: Connect a client, then undo; verify the config returns to its pre-connect content (or the created file is removed) and the row reflects it. Disconnect a connected client; verify the entry is removed after confirmation.

**Acceptance Scenarios**:

1. **Given** a connect just performed in this open form, **When** the user chooses Undo, **Then** the pre-connect state is restored (backup restored, or created file removed) and the row reflects it.
2. **Given** the form was closed and reopened after a connect, **When** the row renders, **Then** Undo is not offered (undo state is scoped to the connect performed in the open form) — Disconnect remains available.
3. **Given** a connected client, **When** the user chooses Disconnect, **Then** a confirmation names the config file and the entry to be removed before anything happens, and the entry is removed after.

---

### Edge Cases

- **Existing entry pointing elsewhere** (stale port/URL): the preview shows both the existing entry and the replacement; connecting requires the same explicit confirmation, no silent overwrite.
- **Credential embedding**: when the pending entry would contain the admin credential, the preview says so explicitly.
- **Malformed config**: a client whose config cannot be parsed shows a "config unreadable" state with the path; Connect is disabled (a write would fail) and the user is pointed at the file.
- **Access denied**: a client whose config the core cannot read shows "access not granted" with remediation guidance; Connect is disabled until access resolves.
- **Preview staleness**: changing any input (selected client, entry-name override) discards the preview and fetches a fresh one; the Connect button is bound to the currently rendered preview. Every preview carries an opaque, backend-generated precondition token derived from the raw pre-write state (file existence + target entry presence and raw value); the write echoes it, and the core rejects with a conflict when the state has drifted — including a file appearing after an absent-file preview or disappearing after an add/replace preview — upon which the form re-previews. The form never passes a force/overwrite flag without a valid token.
- **Client requiring an existing config** (e.g. OpenCode): when its config file is absent, the core refuses to create one; the form surfaces the core's refusal verbatim and this absent case is non-connectable. The generic create statement applies only to clients the core will create configs for.
- **Double-click protection**: action buttons disable while a request is in flight.
- **Unknown client** (core newer than app): rendered by name with a default icon, fully functional.
- **Non-socket transport**: when the app is not talking to the core over its private local socket (administrative transport), the mutating actions are disabled with an explanation; the list and previews remain available if the transport permits.

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: The tray menu MUST offer a "Connect Client…" item adjacent to the existing "Add Server…" item, opening a native form; no step of the journey may require a browser.
- **FR-002**: The form's list MUST come from the core's aggregate client registry (existence-only checks): name, icon, platform support, and config-presence. The tray process MUST NOT read or parse client config files, and opening the list MUST NOT cause client config *contents* to be read.
- **FR-003**: Selecting a client MUST fetch its detailed state (connected / not connected, entry name, access state) and a no-write preview, displayed before the Connect control exists: the entry text to be written, the config file path, the existing entry when one is present, and the safety-net statement — "a timestamped backup of this file will be created alongside it" for an existing file, or "this file does not exist; it will be created, and Undo removes it" otherwise.
- **FR-004**: When the pending entry would embed the admin credential into the client's config, the preview MUST say so.
- **FR-005**: Connect MUST perform the existing connect operation (backup when the file exists, then write), and the form MUST refresh the client's state afterward. The write request MUST echo the preview's opaque precondition token (derived backend-side from raw file existence + target entry presence and raw value, for all change kinds — create, add, replace); the core MUST reject the write with a conflict when the current state no longer matches the token, and the form re-runs the preview instead of retrying. The sanitized rendered entry is display-only and never used for comparison. The form never sends an overwrite flag without a valid token.
- **FR-006**: Undo MUST be offered exactly for a connect performed while the form has been open (using that connect's returned backup identity; for a created file, undo removes it) and MUST disappear once used or once the form closes. Disconnect MUST be offered for any connected client, preceded by a confirmation naming the config file and entry.
- **FR-007**: The proxy entry name defaults to the standard name; an advanced, collapsed-by-default field allows overriding it. Any change to it discards and refetches the preview.
- **FR-008**: Failures (unreachable core, rejected write, preview error) MUST present the core's message text unaltered in content (a status-code prefix is acceptable); action buttons disable while a request is in flight.
- **FR-009**: Client states beyond the happy path MUST render with defined labels and affordances: unsupported-on-platform (disabled + reason), config unreadable (Connect disabled + path), access not granted (Connect disabled + remediation), unknown client id (generic rendering, fully functional).
- **FR-010**: The form MUST be fully keyboard-navigable and announced correctly by VoiceOver: list navigation, preview content, and action buttons, with stable accessibility identifiers for testing.
- **FR-011**: Opening the form MUST NOT modify anything; the only mutations are the explicit Connect / Undo / Disconnect confirmations.
- **FR-012**: The legacy native dashboard connect control (which connects without preview) MUST be routed into this form, so no native path performs a connect without the preview step.
- **FR-013**: While the core is starting or unreachable, the form MUST show a waiting state and poll (every 2 seconds) until reachable, then populate without user action.

### Key Entities

- **Registry client (list row)**: identity (name, icon), platform support, config-presence — the cheap, prompt-free view.
- **Client detail (selected)**: connected state, entry name in use, access state (readable / absent / unreadable / denied) — the authoritative, user-initiated view.
- **Connect preview**: the entry text to be written, config path, existing-entry content when present (sanitized: credentials, tokens, secret-like values, and credential-bearing URLs masked, matching the preview's existing non-disclosure discipline), credential-embedding flag, an opaque precondition token binding the preview to the raw pre-write state, and the derived safety-net statement (backup vs. create-and-undo-removes). Derivation of "create vs add vs replace" is defined: file absent → create (or non-connectable for clients the core refuses to create configs for, surfacing the core's refusal); readable without entry → add; readable with entry → replace; unreadable/denied → no preview, Connect disabled.
- **Connect action result**: success (with backup identity for Undo) or the core's reason; drives the in-form Undo affordance.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: Following the manual protocol (`specs/091-connect-client-form/verification/manual-protocol.md`), a user goes from opening the tray menu to a connected client in under 30 seconds with no browser involved (stopwatch step in the protocol).
- **SC-002**: Every config write performed through the form is structurally preceded by a rendered preview for the exact (client, entry-name) pair being written — the Connect control does not exist before that preview has rendered, and input changes destroy it; verified by unit tests of the form's state model.
- **SC-003**: The undo round-trip test restores the client's config to its exact pre-connect content for the existing-file case, and removes the created file for the new-file case.
- **SC-004**: The tray process performs zero client-config file reads (all state via the core); opening the form's list causes zero config-content reads core-side (existence checks only).
- **SC-005**: Unit tests of the form's state model cover: all list states of FR-009, the detail resolution, preview derivation for all four access states, connect success/conflict/failure, session-scoped undo appearance and disappearance, and disconnect confirmation. Visual flow is covered by the manual protocol.
- **SC-006**: An integration test proves a request over the private local socket reaches the gated connect-write route as an administrative caller end-to-end (complementing the existing separate middleware tests).

## Assumptions

- Backend surface: existing endpoints suffice, with two additive changes and no new endpoints: (1) the preview response gains the existing entry's *sanitized* rendered content when an entry is present (secrets masked; backend tests must prove raw secrets never enter the response) plus an opaque precondition token derived from the raw pre-write state (file existence + target entry presence and raw value; the token is a hash/revision, never reversible to secrets), and (2) the connect write request gains an optional precondition-token field, rejected with a conflict when the current state differs (backward-compatible: absent token preserves today's behavior). Undo/disconnect behavior is unchanged.
- The tray's administrative transport is the private local socket; socket callers are treated as administrative by the core (verified: socket connections are tagged and granted admin context; the write gate rejects only restricted agent tokens). TCP fallback exists for reads, so mutating actions require the socket per the non-socket edge case.
- "No config found" is a statement about the config file, not about whether the application is installed; labels say "no config found", never "not installed".
- Disconnect has no exact-diff preview endpoint; a confirmation naming file and entry is the defined and sufficient disclosure.
- Bridge/binary installation for clients that need a bridge is out of scope; such clients surface whatever state the core reports.
- The form is a window/sheet of the existing native app, consistent with the Add Server form's presentation.

## Out of Scope

- Web UI changes (the Web UI Connect modal remains as is).
- Adding new clients to the registry or changing detection/config-writing logic in the core.
- Cross-session (persistent) undo — would require new backend state.
- Application-installation detection (beyond config presence).
- The "Add Server…" form (already native and shipped).
- Windows/Linux tray parity.
- Automatic connection health checks after connect (spec 090's presence section covers observed traffic).

## Commit Message Conventions *(mandatory)*

### Issue References
- ✅ **Use**: `Related #[issue-number]` - Links the commit to the issue without auto-closing
- ❌ **Do NOT use**: `Fixes #[issue-number]`, `Closes #[issue-number]`, `Resolves #[issue-number]` - These auto-close issues on merge

### Co-Authorship
- ❌ **Do NOT include**: `Co-Authored-By: Claude <noreply@anthropic.com>`
- ❌ **Do NOT include**: "🤖 Generated with [Claude Code](https://claude.com/claude-code)"

### Example Commit Message
```
feat(macos): native Connect Client form

Related #[issue-number]

Tray menu item and native form over the core connect surface: status
list, preview-before-write with backup notice, connect, session-scoped
undo, disconnect.
```

# Feature Specification: Native Connect Client Form

**Feature Branch**: `091-connect-client-form`
**Created**: 2026-07-31
**Status**: Draft
**Input**: User description: "Add client must open macOS app form — a native tray menu item and form for connecting AI clients (Claude Code, Cursor, etc.) to the proxy, replacing the current Web-UI-only flow."

## Context

The proxy already knows how to connect AI client applications: a client registry (Claude Code, Claude Desktop, Cursor, Windsurf, VS Code, Codex, Gemini, OpenCode) with per-client status, a no-write diff preview, a config-writing connect action that backs up the client's config first, plus undo and disconnect. Today this is reachable only through the Web UI's Connect modal. The macOS tray app has a native "Add Server…" form but nothing for clients — the user must leave the menu, open a browser, and authenticate, for what is conceptually a two-click action. Field feedback: "Add server, Add client must open macOS app form."

A standing product rule (learned from earlier feedback on this exact flow) applies: any config-mutating connect flow must show the diff preview and the backup notice BEFORE the action button — the user sees exactly what will be written to their client's config file, and where the backup will land, before anything happens.

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Connect a client from the tray (Priority: P1)

The user picks "Connect Client…" from the tray menu. A native window lists the known AI clients with their current state (connected to this proxy / not connected / not detected). They select Claude Code, immediately see a preview: the exact change that will be made to Claude Code's config file, and a notice that a timestamped backup will be created first. They press Connect, and the row flips to connected.

**Why this priority**: This is the requested capability — the entire feature exists so this journey never leaves the native app.

**Independent Test**: With a core running, open the form, select a client whose config exists, verify the preview renders the pending change and backup notice before any write occurs, connect, and verify the client's status updates.

**Acceptance Scenarios**:

1. **Given** the tray menu is open, **When** the user chooses "Connect Client…", **Then** a native window opens listing every registry client with name, icon, and current state — no browser is involved.
2. **Given** a selected client, **When** the preview loads, **Then** the user sees the change that would be written (added/modified entry, or new file creation when no config exists) and the backup notice, all before any action button is enabled.
3. **Given** the preview is displayed, **When** the user presses Connect, **Then** the client's config is updated, a backup is created, and the form shows the new connected state without the user re-opening it.
4. **Given** the core is unreachable, **When** the form opens, **Then** it shows a clear "core not running" state instead of an empty list.
5. **Given** a client the registry marks unsupported on this platform, **When** the list renders, **Then** the client appears disabled with the reason, not hidden.

---

### User Story 2 - See every client's connection state at a glance (Priority: P2)

The user opens the form just to check: which of my AI tools are actually routed through the proxy? The list answers immediately — each client shows connected / not connected / not detected, and for connected ones, which proxy entry name they use.

**Why this priority**: Complements the tray's Clients presence section (spec 090): presence says who *talked* recently; this form says who is *configured* to talk.

**Independent Test**: Seed config files for some clients and not others; open the form; verify the three states render correctly without any file access from the tray process itself (states come from the core).

**Acceptance Scenarios**:

1. **Given** one connected client, one installed-but-unconnected client, and one client with no config present, **When** the form opens, **Then** the three rows show connected / not connected / not detected respectively.
2. **Given** the form is open, **When** a connect or disconnect completes, **Then** all rows refresh from the core — the tray process never reads or parses client config files itself.

---

### User Story 3 - Undo or disconnect safely (Priority: P2)

After connecting, the user changes their mind. From the same form they can undo the last connect (restoring the backup) or disconnect (removing the proxy entry), with the same preview-first discipline: the form states what will happen before the destructive button is pressed.

**Why this priority**: A config-writing feature without a visible way back teaches users not to trust it.

**Independent Test**: Connect a client, then undo; verify the config returns to its backed-up state and the row returns to its prior state. Disconnect a connected client; verify the entry is removed.

**Acceptance Scenarios**:

1. **Given** a client just connected via the form, **When** the user chooses Undo, **Then** the backed-up config is restored and the row reflects it.
2. **Given** a connected client, **When** the user chooses Disconnect, **Then** the form states that the proxy entry will be removed from the client's config before the user confirms, and the entry is removed after.
3. **Given** a client with no backup available, **When** the row renders, **Then** Undo is not offered.

---

### Edge Cases

- The client's config file does not exist yet: the preview must present this as "a new file will be created at <path>", not a diff against nothing.
- The client already has an mcpproxy entry pointing elsewhere (stale port/URL): the preview shows a modification; connecting requires the same explicit confirmation, no silent overwrite.
- The connect action can embed the admin credential into the client's config file; the preview notice must say so when it applies.
- The core rejects the write (e.g. the tray is connected with a restricted token): the form surfaces the rejection reason verbatim rather than a generic failure.
- Two rapid connect clicks: the action button disables while a write is in flight.
- The form is opened while the core is starting: it renders a waiting state and populates when the core becomes reachable, without requiring reopen.
- A registry client unknown to this app version (server newer than tray): rendered by name with default icon, fully functional.

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: The tray menu MUST offer a "Connect Client…" item adjacent to the existing "Add Server…" item, opening a native form; no step of the journey may require a browser.
- **FR-002**: The form MUST list all clients reported by the core's client registry with name, icon, platform support, and current state (connected / not connected / not detected), sourced entirely from the core; the tray process MUST NOT read or parse client config files.
- **FR-003**: Selecting a client MUST fetch and display a no-write preview: the exact pending change to that client's config (or new-file creation with its path) and a notice that a timestamped backup is created before any write — both visible before the Connect button can be pressed.
- **FR-004**: When the pending change would embed an admin credential into the client's config, the preview MUST say so.
- **FR-005**: Connect MUST perform the same operation as the existing connect action (backup, then write), and the form MUST refresh all client states from the core afterward.
- **FR-006**: The form MUST offer Undo (restore the pre-connect backup) when the core reports one is available, and Disconnect (remove the proxy entry) for connected clients; each states its effect before a confirming press.
- **FR-007**: The proxy entry name defaults to the standard name; an advanced, collapsed-by-default field allows overriding it, feeding the same preview-then-connect flow.
- **FR-008**: Failures (unreachable core, rejected write, preview error) MUST surface the core's reason text; the action button MUST disable while a request is in flight.
- **FR-009**: An unsupported-on-this-platform client renders disabled with its reason; an unknown client renders generically and remains functional.
- **FR-010**: The form MUST be fully keyboard-navigable and announced correctly by VoiceOver: list navigation, preview content, and action buttons.
- **FR-011**: Opening the form MUST NOT modify anything; the only mutations are the explicit Connect / Undo / Disconnect confirmations.

### Key Entities

- **Registry client**: An AI client application the core knows how to configure: identity (name, icon), platform support, detection state, connection state, and — when connected — the proxy entry name in use.
- **Connect preview**: A no-write description of the pending change for one client: change kind (create / add / modify), affected file path, rendered change, backup destination, and whether a credential would be embedded.
- **Connect action result**: Outcome of connect/undo/disconnect: success or a reason, plus the refreshed client state.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: A user can go from opening the tray menu to a connected client in under 30 seconds with no browser involved.
- **SC-002**: 100% of config writes performed through the form are preceded by a rendered preview and backup notice for that exact client — enforced structurally (the Connect control does not exist before the preview has rendered), verified by unit tests of the form's state model.
- **SC-003**: Undo restores the client's config to its exact pre-connect content in the round-trip test.
- **SC-004**: The tray process performs zero client-config file reads (all state via the core), verifiable by the existing tray no-config-access discipline.
- **SC-005**: All three client states and all three actions are covered by unit tests of the form's state model; the visual flow is covered by a screenshot protocol in the spec's verification directory.

## Assumptions

- The core's existing connect surface (registry list, per-client status, preview, connect, undo, disconnect) is sufficient; no new backend endpoints are required. The tray talks to it with its existing administrative transport (local socket), which is not subject to the restricted-token gate.
- The form is a window/sheet of the existing native app, consistent with the Add Server form's presentation.
- Client detection semantics (what counts as "not detected") are whatever the core reports today; this feature does not redefine them.
- Bridge/binary installation for clients that need a bridge is out of scope; such clients surface whatever state the core reports.

## Out of Scope

- Web UI changes (the Web UI Connect modal remains as is).
- Adding new clients to the registry or changing detection/config-writing logic in the core.
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
list, preview-before-write with backup notice, connect, undo,
disconnect.
```

# Research: Add/Edit Server UX Improvements

## R1: PATCH /api/v1/servers/{name} Backend Pattern

**Decision**: Add PATCH handler to existing Chi router in `internal/httpapi/server.go`, reusing `AddServerRequest` struct for the request body. Only non-nil fields are applied.

**Rationale**: The existing `handleAddServer` (POST) already validates and creates servers. PATCH reuses the same request struct but applies partial updates to the existing config. This follows REST conventions and minimizes new code.

**Alternatives considered**:
- PUT (full replacement): Rejected — user shouldn't need to send all fields to change one.
- New UpdateServerRequest struct: Rejected — AddServerRequest already has omitempty tags on all fields, making it suitable for partial updates.

## R2: SwiftUI Inline Validation Pattern

**Decision**: Use `@FocusState` to track which field has focus. On focus change, validate the previously-focused field. Show red `Text` below the field with `.foregroundStyle(.red)` and `.font(.caption)`.

**Rationale**: Apple HIG recommends preventing errors (disabled button) AND showing inline feedback. SwiftUI's `@FocusState` + `.onChange(of:)` provides clean field-blur detection. This is the standard SwiftUI pattern for form validation on macOS.

**Alternatives considered**:
- Shake animation: Not a standard macOS pattern per Apple HIG research.
- Alert on submit: Too disruptive for simple validation.
- Only disabled button: Current approach — insufficient feedback per user complaints.

## R3: NSOpenPanel from SwiftUI

**Decision**: Use `NSOpenPanel` directly from a SwiftUI button action via `@MainActor` async call. NSOpenPanel.runModal() works from SwiftUI context on macOS.

**Rationale**: NSOpenPanel is the standard macOS file picker. It works directly from SwiftUI without needing a NSViewRepresentable wrapper. Simply call `panel.beginSheetModal(for: NSApp.keyWindow!)` from the button's async action.

**Alternatives considered**:
- fileImporter SwiftUI modifier: Limited customization, doesn't support accessory views.
- Drag and drop: Supplements but doesn't replace file picker (out of scope).

## R4: Connection Test Phased Feedback

**Decision**: After form submission, show inline status text that transitions through phases:
1. "Saving configuration..." (API POST call)
2. "Connecting to server..." (poll server status via SSE or 2s delay + status check)
3. Success: "Connected (N tools)" / Failure: actual error + Save Anyway/Retry

**Rationale**: The current flow calls `addServer()` which saves AND triggers connection. The API returns immediately after saving. Connection happens asynchronously. To show connection feedback, we poll the server status endpoint after a brief delay, or listen for the SSE event for the new server.

**Alternatives considered**:
- Synchronous connection test: Backend doesn't support this — connection is async.
- WebSocket for real-time feedback: Over-engineering for this use case.

## R5: Import Preview

**Decision**: Use the existing `?preview=true` query parameter on `POST /api/v1/servers/import/path`. This returns the list of servers that would be imported without actually importing them. Display these in a list with checkboxes, then submit the actual import with `server_names` filter for selected servers only.

**Rationale**: The preview API already exists in the backend. The Swift client just needs to call it and display results before confirming.

**Alternatives considered**:
- Client-side config parsing: Rejected — the backend already handles multiple config formats (Claude Desktop, Cursor, Codex TOML, etc.). Duplicating parsing logic in Swift would be fragile.

# Feature Specification: UX-Friendly Web UI Settings Page

**Feature Branch**: `060-settings-page`
**Created**: 2026-05-29
**Status**: Draft
**Input**: Make mcpproxy's many config options approachable from the Web UI — a sectioned, form-based settings experience instead of raw JSON.

## Context

mcpproxy has grown a large configuration surface (security toggles, Docker isolation, code execution, quarantine, detection, logging, retention, TLS, teams…). Today the Web UI exposes all of it only as a raw Monaco JSON editor — fine for power users, intimidating for everyone else and easy to break. This feature turns the most important options into a friendly, prioritized, form-based settings page while keeping the raw editor as an escape hatch.

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Manage security & access settings without editing JSON (Priority: P1)

As an operator, I want a prominent "Security & Access" section where I can see and change the settings that matter most — API key, MCP auth requirement, quarantine, Docker isolation, code execution, read-only mode, sensitive-data detection, secret-header reveal, and the listen address — using plain toggles and inputs, so I can secure my instance confidently without hand-editing config or risking a typo.

**Why this priority**: These are the highest-impact, highest-risk options; making them safe and obvious is the core value and the MVP.

**Independent Test**: Open Settings → Security & Access. Toggle quarantine off and save; confirm the change persists and the running server reflects it. Toggle a dangerous option (reveal secret headers) and confirm a confirmation prompt appears. Confirm the API key field is masked with show/regenerate, and that saving a security change does NOT wipe the API key or other secrets.

**Acceptance Scenarios**:

1. **Given** the Security section, **When** I toggle `quarantine_enabled` off and save, **Then** only that field is sent, a success toast appears, and a re-open shows the new value (persisted + hot-reloaded).
2. **Given** a dangerous toggle (reveal secret headers, disable management, non-loopback listen), **When** I change it, **Then** I must confirm in a dialog before it is applied.
3. **Given** the API key field, **When** I view it, **Then** it is masked with a show toggle and a regenerate action; saving an unrelated security change must not overwrite the stored key/secret headers.
4. **Given** a restart-only field (api_key, listen, data_dir, TLS), **When** I change it, **Then** the UI clearly flags that a restart is required and the apply response's restart indication is surfaced.

### User Story 2 - Adjust common behaviour in a General section (Priority: P2)

As an operator, I want a "General" section for everyday knobs — routing mode, tool limits, response limit, call timeout, log level, telemetry, prompts — with appropriate controls (selects, numbers, toggles), so I can tune normal behaviour quickly.

**Acceptance Scenarios**:

1. **Given** the General section, **When** I change `routing_mode` via a select and save, **Then** only that field is applied and a success toast appears.
2. **Given** a numeric field with bounds (e.g. tools limit), **When** I enter an out-of-range value, **Then** the UI prevents saving and explains the valid range.

### User Story 3 - Reach every option via Advanced accordions and the raw editor (Priority: P3)

As a power user, I want the remaining subsystems (code execution, Docker isolation detail, sensitive-data detection categories/patterns, output validation, output sanitisation, activity retention, logging, TLS, tokenizer, intent, environment, scanner) grouped into collapsible "Advanced" accordions, plus a "Raw JSON" tab that preserves the full editor, so nothing becomes unreachable and I can still edit anything directly.

**Acceptance Scenarios**:

1. **Given** the Advanced tab, **When** I expand a subsystem accordion and change a field, **Then** I can save just that subsystem's changes.
2. **Given** the Raw JSON tab, **When** I edit and apply, **Then** behaviour matches today's editor (validate + apply), and validation errors are shown inline.
3. **Given** the server (multi-user) edition, **When** Teams config is present, **Then** a Teams section is shown; in the personal edition it is absent.

### Edge Cases

- Saving must never clobber secrets: because the config read masks the API key and secret headers, a save sends only the fields the user changed (partial update), not the whole masked config.
- Deprecated/internal options (`top_k`, `enable_tray`, `features`/`enable_web_ui`, telemetry bookkeeping) are not shown.
- Options managed elsewhere (upstream servers, registries) are not duplicated; the page links to those pages instead.
- A change that requires a restart is applied/persisted but clearly marked as taking effect after restart.
- Invalid input is caught before save with a clear message; a failed apply surfaces the server's validation errors.

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: The settings page MUST present configuration in prioritized sections: Security & Access (top), General, Advanced (grouped accordions), Raw JSON (full editor), and Teams (server edition only).
- **FR-002**: The Security & Access section MUST include, at minimum: API key (masked, show, regenerate), require MCP auth, quarantine enabled, global Docker isolation enabled, code execution enabled, read-only mode, sensitive-data detection enabled, reveal secret headers, and listen address.
- **FR-003**: Saving a section MUST persist only the fields the user changed (partial update), so masked secrets are never overwritten by a save of unrelated fields.
- **FR-004**: After a save, the system MUST persist the change, apply it live where possible, and clearly indicate when a restart is required (and which fields).
- **FR-005**: Changing a dangerous option (reveal secret headers, disabling management, binding to a non-loopback address, disabling quarantine) MUST require explicit confirmation.
- **FR-006**: Each control MUST use the appropriate input type (toggle, select/enum, number with bounds, text, duration, list) and validate input before allowing save.
- **FR-007**: Restart-only fields (api_key, listen, data_dir, TLS) MUST be visually flagged as requiring a restart.
- **FR-008**: Deprecated and internal-only fields MUST be hidden; options owned by other pages (servers, registries) MUST NOT be duplicated and SHOULD be cross-linked.
- **FR-009**: The Raw JSON editor MUST remain available with validate + apply behaviour equivalent to today's.
- **FR-010**: The Teams/Server section MUST appear only in the server edition and MUST NOT affect the personal edition.
- **FR-011**: Save outcomes (success, validation error, restart-required) MUST be communicated via clear feedback (toast + inline messaging).
- **FR-012**: All interactive controls MUST carry stable test identifiers to support automated UI verification.

### Key Entities

- **Settings section**: a grouped set of related options with its own save action; partial save scope.
- **Setting field**: one configurable option — key, label, help text, control type, value, validation bounds, danger flag, restart-required flag.
- **Apply result**: outcome of a save — applied-immediately vs requires-restart, the list of changed fields, and any validation errors.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: An operator can change each of the nine Security & Access settings and the General settings entirely through form controls, with zero JSON editing.
- **SC-002**: Saving any single setting persists only that field; 100% of the time, unrelated secrets (API key, secret headers) are preserved across the save.
- **SC-003**: 100% of dangerous changes require an explicit confirmation step before taking effect.
- **SC-004**: Every non-deprecated, non-duplicated config option is reachable from the page (a friendly control in Security/General/Advanced, or via the Raw JSON tab).
- **SC-005**: After any save, the UI correctly reports whether the change is live or needs a restart, matching the server's response.
- **SC-006**: The personal edition shows no Teams section and behaves identically to before for users who never open the new sections.
- **SC-007**: Each section and key control is verifiable via the Chrome extension (stable test identifiers + captured screenshots).

## Commit Message Conventions *(mandatory)*

### Issue References

Reference this spec (060) in commit bodies.

### Co-Authorship

Per repo policy: no Claude/Co-Authored-By attribution in mcpproxy-go commits.

### Example Commit Message

```
feat(060): form-based settings page with partial-update PATCH /config

Redesign Settings.vue into Security/General/Advanced/Raw-JSON tabs;
add PATCH /api/v1/config (partial deep-merge) so saving a section
preserves masked secrets. Verified via Chrome extension.
```

## Testing

- Backend: unit/integration tests for the partial-merge config update (only changed fields applied; secrets preserved; restart-required reported).
- Frontend: the page renders sections, validates inputs, confirms dangerous changes, and saves partial updates.
- Verification (mandatory): drive the Web UI via the Chrome extension — capture Security, General, Advanced, and Raw JSON sections; exercise a toggle save and a dangerous-toggle confirm. (QA report/screenshots are produced for review but NOT committed, per project policy.)

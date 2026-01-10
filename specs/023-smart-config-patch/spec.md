# Feature Specification: Smart Config Patching

**Feature Branch**: `023-smart-config-patch`
**Created**: 2026-01-10
**Status**: Draft
**Related Issues**: #239, #240

## Problem Statement

Configuration update operations in MCPProxy currently overwrite entire server configuration objects instead of intelligently merging changes. This causes critical data loss when operations like unquarantine, enable/disable, or patch only intend to modify a single field.

**Specific Issues:**
- Unquarantining a server via Web UI/System Tray removes the `isolation` configuration block (#240)
- Using `upstream_servers` tool with `patch` or `update` operation removes `isolation` config (#239)
- Any operation that updates a server risks losing user-configured nested objects (OAuth, isolation, headers, env)

**Root Cause:**
When config is read, modified for a single field, and saved back, the operation does not preserve fields that were not explicitly loaded or considered. The current implementation treats updates as full replacements rather than partial patches.

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Preserve Isolation Config During Unquarantine (Priority: P1)

As a user with custom Docker isolation settings, when I unquarantine a server, my Docker volume mappings, custom image, network_mode, and working_dir settings must be preserved.

**Why this priority**: This is the exact bug reported in #240. Users are losing critical Docker configuration, breaking their workflows immediately after unquarantining.

**Independent Test**: Can be fully tested by adding a server with isolation config, letting it auto-quarantine, then unquarantining via System Tray or Web UI - the isolation block should remain intact.

**Acceptance Scenarios**:

1. **Given** a server with isolation config `{"enabled": true, "image": "python:3.11", "extra_args": ["-v", "/path:/mount"]}` that is quarantined, **When** I unquarantine the server via Web UI, **Then** the server config in `mcp_config.json` retains the complete isolation block unchanged.

2. **Given** a server with isolation config that is quarantined, **When** I unquarantine via System Tray menu, **Then** only the `quarantined` field changes from `true` to `false`, all other fields remain unchanged.

3. **Given** a server with complex isolation config including network_mode, working_dir, and multiple extra_args, **When** I toggle quarantine status multiple times, **Then** the isolation config remains identical after each toggle.

---

### User Story 2 - Preserve Config During MCP Tool Patch Operations (Priority: P1)

As an AI agent using the `upstream_servers` tool, when I patch a server to change its enabled state, all other configuration including isolation, OAuth, env, and headers must be preserved.

**Why this priority**: This is the exact bug reported in #239. AI agents frequently enable/disable servers, and losing config on each operation breaks user workflows.

**Independent Test**: Can be fully tested by calling `upstream_servers(operation="patch", name="server", enabled=true)` on a server with isolation config and verifying the isolation block remains.

**Acceptance Scenarios**:

1. **Given** a server with isolation config, **When** I call `upstream_servers` with `operation="patch"` and only `enabled=true`, **Then** only the `enabled` field changes, all other fields including `isolation` remain unchanged.

2. **Given** a server with OAuth config and headers, **When** I patch just the `url` field, **Then** OAuth config and headers remain unchanged.

3. **Given** a server with env variables containing secrets (keyring references), **When** I patch `working_dir`, **Then** all env variables remain unchanged.

---

### User Story 3 - Preserve Config During Enable/Disable Operations (Priority: P2)

As a user toggling servers on/off via Web UI or CLI, my server configurations must remain intact regardless of how many times I enable or disable them.

**Why this priority**: Enable/disable is the most common server management operation. Config loss here would affect every user.

**Independent Test**: Can be tested by enabling/disabling a fully-configured server 10 times and comparing config before and after.

**Acceptance Scenarios**:

1. **Given** a server with full configuration (isolation, OAuth, env, headers, args), **When** I disable then re-enable the server via Web UI, **Then** all configuration fields remain identical.

2. **Given** a server with full configuration, **When** I run `mcpproxy upstream enable/disable` commands 5 times, **Then** `mcp_config.json` content for that server remains identical except for `enabled` field and `updated` timestamp.

---

### User Story 4 - Deep Merge for Nested Object Updates (Priority: P2)

As a user updating nested configuration objects like OAuth or isolation, only the fields I specify should change while preserving other nested fields I didn't mention.

**Why this priority**: Power users often need to update specific nested settings without reconstructing the entire object.

**Independent Test**: Can be tested by patching a single field within a nested object and verifying other nested fields remain.

**Acceptance Scenarios**:

1. **Given** a server with `isolation: {enabled: true, image: "python:3.11", network_mode: "bridge"}`, **When** I patch with `isolation: {image: "python:3.12"}`, **Then** result is `isolation: {enabled: true, image: "python:3.12", network_mode: "bridge"}`.

2. **Given** a server with `env: {API_KEY: "xxx", DEBUG: "true"}`, **When** I patch with `env: {DEBUG: "false"}`, **Then** result is `env: {API_KEY: "xxx", DEBUG: "false"}`.

3. **Given** a server with existing headers, **When** I add a new header via patch, **Then** existing headers remain and new header is added.

---

### User Story 5 - Explicit Field Removal (Priority: P3)

As a user who needs to remove a specific configuration field, I should be able to explicitly delete it without affecting other fields.

**Why this priority**: While preservation is the default, users must still be able to intentionally remove config when needed.

**Independent Test**: Can be tested by explicitly removing a field using null/remove directive and verifying only that field is removed.

**Acceptance Scenarios**:

1. **Given** a server with isolation config, **When** I explicitly set `isolation: null` in a patch operation, **Then** only the isolation field is removed, all other config remains.

2. **Given** a server with OAuth config, **When** I use a remove directive for OAuth, **Then** OAuth is removed but isolation, env, and other fields remain.

---

### Edge Cases

- What happens when patching a field that doesn't exist? (Should add it)
- How does system handle patching with invalid JSON structure? (Should reject with clear error)
- What happens when patching a server that was deleted between read and write? (Should fail gracefully with clear error message)
- How does deep merge handle arrays (args, extra_args)? (Arrays should be replaced, not merged element-wise)
- What happens when config file is externally modified during operation? (Database is source of truth; JSON file changes sync on next save)

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: System MUST preserve all unmodified fields when updating any single field on a server configuration
- **FR-002**: System MUST implement deep merge for nested objects (isolation, OAuth, env, headers) during patch operations
- **FR-003**: System MUST only modify the specific fields provided in a patch request, leaving all other fields unchanged
- **FR-004**: System MUST treat arrays (args, extra_args) as atomic values that are replaced entirely, not merged element-wise
- **FR-005**: System MUST support explicit field removal using null values
- **FR-006**: System MUST log configuration changes with before/after diff for auditability
- **FR-007**: System MUST validate merged configuration before persisting to ensure schema compliance
- **FR-008**: System MUST apply smart patching to all config update paths: MCP tool, REST API, Web UI, System Tray, CLI
- **FR-009**: System MUST preserve field order in JSON output for consistent diffs and version control
- **FR-010**: System MUST handle concurrent config modifications safely without data loss

### Key Entities

- **ServerConfig**: Represents an upstream MCP server with nested configuration objects (isolation, OAuth, env, headers)
- **PatchOperation**: A partial configuration update specifying only the fields to modify
- **MergeStrategy**: Rules for how different field types (scalar, object, array) are merged:
  - Scalars: replaced
  - Objects: deep merged recursively
  - Arrays: replaced entirely (not element-wise merged)
  - Null values: remove the field

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: After implementing this fix, unquarantining a server preserves 100% of original configuration fields
- **SC-002**: Patch operations modifying a single field result in exactly one field change (plus updated timestamp)
- **SC-003**: All existing E2E tests continue to pass with the new merge behavior
- **SC-004**: Configuration round-trip (read-modify-write) results in identical JSON except for modified fields and timestamps
- **SC-005**: No user reports of lost configuration data after enabling this feature
- **SC-006**: Configuration diff logs are available for all update operations, enabling audit trails

## Assumptions

- Arrays (args, extra_args) should be treated as atomic values and replaced entirely during merge, not merged element-wise (this follows industry standard Strategic Merge Patch semantics)
- The `updated` timestamp field will always change on any modification (expected behavior)
- Field order in JSON should be preserved for consistent version control diffs
- Deep merge applies to nested objects (isolation, OAuth, env, headers) but not to arrays
- Explicit null values in patch requests indicate field removal intent
- The database (BBolt) is the source of truth; JSON file is a persistence layer

## Out of Scope

- Three-way merge with conflict detection (future enhancement)
- Config file format migration or versioning
- Undo/rollback capability for config changes
- Real-time config file watching for external changes
- Support for YAML config format (currently JSON only)

## Supporting Documents

- **[examples.md](./examples.md)** - Comprehensive examples of Deep Merge + Strategic Merge Patch behavior, MCP tool descriptions for LLMs, and edge case handling

## Commit Message Conventions *(mandatory)*

When committing changes for this feature, follow these guidelines:

### Issue References
- **Use**: `Related #239, Related #240` - Links the commit to the issues without auto-closing
- **Do NOT use**: `Fixes #239`, `Closes #240` - These auto-close issues on merge

**Rationale**: Issues should only be closed manually after verification and testing in production, not automatically on merge.

### Co-Authorship
- **Do NOT include**: `Co-Authored-By: Claude <noreply@anthropic.com>`
- **Do NOT include**: "Generated with Claude Code"

### Example Commit Message
```
fix: preserve config fields during patch operations

Related #239, Related #240

Implements deep merge for server configuration updates to prevent
data loss when modifying single fields. Previously, patch operations
would overwrite entire config objects, losing nested fields like
isolation settings.

## Changes
- Add deep merge utility for nested config objects
- Update patch handlers to merge instead of replace
- Add config diff logging for auditability
- Add tests for merge behavior

## Testing
- All existing E2E tests pass
- New tests verify isolation preservation during unquarantine
- New tests verify patch operations preserve unmodified fields
```

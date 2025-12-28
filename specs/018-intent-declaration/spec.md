# Feature Specification: Intent Declaration with Tool Split

**Feature Branch**: `018-intent-declaration`
**Created**: 2025-12-28
**Updated**: 2025-12-28
**Status**: Draft
**Input**: User description: "Implement Phase 2 (Intent Declaration) from RFC-003 Activity Log proposal with enhanced UX: split call_tool into call_tool_read, call_tool_write, call_tool_destructive for granular IDE permission control."

## Overview

This feature introduces **intent-based tool splitting** - replacing the single `call_tool` with three operation-specific tools that enable granular permission control in IDEs like Cursor, Claude Desktop, and similar AI coding assistants.

### Key Innovation: Two-Key Security Model

Agents must declare intent in **two places** that must match:
1. **Tool selection**: `call_tool_read` / `call_tool_write` / `call_tool_destructive`
2. **Intent parameter**: `intent.operation_type` must match tool variant

```
call_tool_destructive(
  name: "github:delete_repo",
  args_json: "{}",
  intent: {
    operation_type: "destructive",  ← MUST match tool variant
    data_sensitivity: "private",
    reason: "User requested repo cleanup"
  }
)
```

**Validation chain:**
1. Tool variant declares intent → `call_tool_destructive` expects "destructive"
2. `intent.operation_type` → MUST be "destructive"
3. Mismatch → **REJECT** (agent confusion or attack attempt)
4. Server annotation check → validate against `destructiveHint`/`readOnlyHint`

This enables IDE permission settings:
```
MCPProxy Tools:
  [x] call_tool_read        → Auto-approve
  [ ] call_tool_write       → Ask each time
  [ ] call_tool_destructive → Always ask + confirm
```

### Breaking Change: Remove Legacy call_tool

The original `call_tool` is **removed entirely** (not deprecated). This is a clean break that:
- Simplifies the tool surface
- Eliminates ambiguity
- Forces explicit intent declaration
- Reduces security attack surface

## User Scenarios & Testing *(mandatory)*

### User Story 1 - IDE Configures Per-Tool-Type Permissions (Priority: P1)

Users configure their IDE (Cursor, Claude Desktop) to automatically approve read operations while requiring confirmation for write and destructive operations. This provides security without friction for safe operations.

**Why this priority**: This is the core UX improvement - enabling granular control that wasn't possible with a single call_tool.

**Independent Test**: Can be tested by configuring IDE to auto-approve call_tool_read, then verifying read operations proceed without prompts while write operations require approval.

**Acceptance Scenarios**:

1. **Given** a user has configured "auto-approve call_tool_read" in their IDE, **When** an agent calls `call_tool_read` with matching intent, **Then** the operation proceeds without user prompt
2. **Given** a user has configured "ask for call_tool_write" in their IDE, **When** an agent calls `call_tool_write` with matching intent, **Then** the IDE prompts for user approval
3. **Given** a user has configured "always ask for call_tool_destructive", **When** an agent calls `call_tool_destructive` with matching intent, **Then** the IDE prompts for approval with clear warning

---

### User Story 2 - Agent Declares Matching Intent (Priority: P1)

AI agents must provide intent.operation_type that matches the tool variant they're calling. Mismatches are rejected to prevent confusion or malicious behavior.

**Why this priority**: Core security feature - the two-key model prevents intent spoofing.

**Independent Test**: Can be tested by calling call_tool_read with intent.operation_type="write" and verifying rejection.

**Acceptance Scenarios**:

1. **Given** an agent calls `call_tool_read` with `intent.operation_type: "read"`, **Then** the call proceeds (matching intent)
2. **Given** an agent calls `call_tool_read` with `intent.operation_type: "write"`, **Then** the call is rejected with error "Intent mismatch: tool is call_tool_read but intent declares write"
3. **Given** an agent calls `call_tool_destructive` with `intent.operation_type: "read"`, **Then** the call is rejected with error "Intent mismatch: tool is call_tool_destructive but intent declares read"
4. **Given** an agent calls any tool variant without `intent.operation_type`, **Then** the call is rejected with error "intent.operation_type is required"

---

### User Story 3 - MCPProxy Validates Against Server Annotations (Priority: P1)

MCPProxy validates that the agent's tool choice matches server-provided annotations, rejecting mismatches where agent claims "read" but server marks tool as "destructive".

**Why this priority**: Critical security feature - prevents agents from sneaking destructive operations through auto-approved read channel.

**Independent Test**: Can be tested by calling call_tool_read on a tool marked with destructiveHint=true and verifying rejection.

**Acceptance Scenarios**:

1. **Given** a tool has `destructiveHint: true` annotation, **When** agent calls `call_tool_read` with matching intent, **Then** the call is rejected with error "Tool 'X' is marked destructive by server, use call_tool_destructive"
2. **Given** a tool has `destructiveHint: true` annotation, **When** agent calls `call_tool_write` with matching intent, **Then** the call is rejected with same error
3. **Given** a tool has `readOnlyHint: true` annotation, **When** agent calls `call_tool_destructive` with matching intent, **Then** the call succeeds (overly cautious is allowed)
4. **Given** a tool has no annotations, **When** agent calls any tool variant with matching intent, **Then** the call proceeds (trust agent)

---

### User Story 4 - retrieve_tools Returns Annotations and Guidance (Priority: P1)

The retrieve_tools response includes server annotations and recommends which call_tool variant to use for each tool, enabling agents to make correct choices.

**Why this priority**: Agents need this information to select the correct tool variant.

**Independent Test**: Can be tested by calling retrieve_tools and verifying annotations and call_with fields are present.

**Acceptance Scenarios**:

1. **Given** a server provides `destructiveHint: true` for a tool, **When** agent calls `retrieve_tools`, **Then** the tool entry includes `annotations.destructiveHint: true` and `call_with: "call_tool_destructive"`
2. **Given** a server provides `readOnlyHint: true` for a tool, **When** agent calls `retrieve_tools`, **Then** the tool entry includes `annotations.readOnlyHint: true` and `call_with: "call_tool_read"`
3. **Given** a server provides no annotations, **When** agent calls `retrieve_tools`, **Then** the tool entry includes `call_with: "call_tool_write"` (safe default)
4. **Given** agent calls `retrieve_tools`, **Then** response includes `usage_instructions` explaining the three tool variants

---

### User Story 5 - CLI Tool Call Commands (Priority: P2)

Users can invoke tools via CLI with explicit intent using separate commands for each operation type.

**Why this priority**: CLI parity with MCP interface.

**Independent Test**: Can be tested by running `mcpproxy call tool-read` and verifying it works.

**Acceptance Scenarios**:

1. **Given** a user runs `mcpproxy call tool-read github:list_repos --args '{}'`, **Then** the tool is called via call_tool_read with operation_type "read"
2. **Given** a user runs `mcpproxy call tool-write github:create_issue --args '{"title":"Bug"}'`, **Then** the tool is called via call_tool_write with operation_type "write"
3. **Given** a user runs `mcpproxy call tool-destructive github:delete_repo --args '{"repo":"test"}'`, **Then** the tool is called via call_tool_destructive with operation_type "destructive"
4. **Given** a user runs `mcpproxy call tool-read` on a destructive tool, **Then** the command fails with clear error message

---

### User Story 6 - View Intent in Activity List CLI (Priority: P2)

Users monitoring agent activity via CLI can see which tool variant was used for each operation.

**Why this priority**: Visibility into agent behavior patterns.

**Independent Test**: Can be tested by running `mcpproxy activity list` after tool calls.

**Acceptance Scenarios**:

1. **Given** tool calls via different variants exist, **When** a user runs `mcpproxy activity list`, **Then** an Intent column shows operation type with icon (read/write/destructive)
2. **Given** tool calls exist, **When** a user runs `mcpproxy activity list -o json`, **Then** intent objects include operation_type and metadata

---

### User Story 7 - Agent Provides Additional Intent Metadata (Priority: P3)

Agents provide data sensitivity classification and reason alongside the required operation_type.

**Why this priority**: Compliance and audit requirements, secondary to core classification.

**Independent Test**: Can be tested by calling call_tool_write with full intent and verifying storage.

**Acceptance Scenarios**:

1. **Given** an agent provides `intent: {operation_type: "write", data_sensitivity: "private", reason: "Creating user record"}`, **Then** all fields are stored in the activity record
2. **Given** an agent omits optional fields (data_sensitivity, reason), **Then** the call succeeds with only operation_type stored

---

### User Story 8 - Filter Activity by Operation Type (Priority: P3)

Users can filter activity to see only destructive operations for security review.

**Why this priority**: Useful for auditing but not core functionality.

**Independent Test**: Can be tested by running `mcpproxy activity list --intent-type destructive`.

**Acceptance Scenarios**:

1. **Given** various tool calls exist, **When** user runs `mcpproxy activity list --intent-type destructive`, **Then** only destructive operations are shown
2. **Given** various tool calls exist, **When** user calls `GET /api/v1/activity?intent_type=read`, **Then** only read operations are returned

---

### Edge Cases

- What happens when intent.operation_type doesn't match tool variant? (REJECT - primary validation)
- What happens when agent omits intent entirely? (REJECT - intent is required)
- What happens when server has both readOnlyHint and destructiveHint? (Treat as destructive - more restrictive wins)
- What happens during retrieve_tools for tools without annotations? (Default to call_with: "call_tool_write")
- What if server changes annotations after agent discovers tools? (Re-validate at call time)
- What happens if agent calls old call_tool? (Tool not found - it's removed)

## Requirements *(mandatory)*

### Functional Requirements

**Tool Split (Replaces call_tool)**:
- **FR-001**: System MUST provide `call_tool_read` for read-only operations
- **FR-002**: System MUST provide `call_tool_write` for state-modifying operations
- **FR-003**: System MUST provide `call_tool_destructive` for destructive/irreversible operations
- **FR-004**: System MUST remove legacy `call_tool` from MCP interface
- **FR-005**: All three tools MUST accept `name` (required), `args_json` (optional), and `intent` (required) parameters

**Intent Parameter (Required)**:
- **FR-006**: The `intent` object MUST be required on all call_tool_* variants
- **FR-007**: The `intent.operation_type` field MUST be required with values: "read", "write", "destructive"
- **FR-008**: The `intent.operation_type` MUST match the tool variant used
- **FR-009**: The `intent.data_sensitivity` field MUST be optional with values: "public", "internal", "private", "unknown"
- **FR-010**: The `intent.reason` field MUST be optional (max 1000 characters)

**Two-Key Validation**:
- **FR-011**: System MUST reject calls where intent.operation_type doesn't match tool variant
- **FR-012**: System MUST reject calls without intent parameter
- **FR-013**: System MUST reject calls without intent.operation_type
- **FR-014**: Error messages MUST clearly indicate the mismatch

**Server Annotation Validation**:
- **FR-015**: System MUST check server's `destructiveHint` annotation before allowing call_tool_read
- **FR-016**: System MUST check server's `destructiveHint` annotation before allowing call_tool_write
- **FR-017**: System MUST reject call_tool_read if tool has `destructiveHint: true`
- **FR-018**: System MUST reject call_tool_write if tool has `destructiveHint: true`
- **FR-019**: System MUST allow call_tool_destructive regardless of annotations (most permissive)
- **FR-020**: System MUST allow calls when server provides no annotations (trust agent)

**retrieve_tools Enhancement**:
- **FR-021**: retrieve_tools response MUST include `annotations` object for each tool
- **FR-022**: Annotations MUST include `readOnlyHint` and `destructiveHint` when provided by server
- **FR-023**: retrieve_tools response MUST include `call_with` field recommending tool variant
- **FR-024**: retrieve_tools response MUST include `usage_instructions` explaining tool variants
- **FR-025**: Tool descriptions MUST reference the correct call_tool variant to use

**CLI Commands**:
- **FR-026**: System MUST provide `mcpproxy call tool-read <server:tool> [--args JSON]` command
- **FR-027**: System MUST provide `mcpproxy call tool-write <server:tool> [--args JSON]` command
- **FR-028**: System MUST provide `mcpproxy call tool-destructive <server:tool> [--args JSON]` command
- **FR-029**: CLI commands MUST auto-populate intent.operation_type based on command used
- **FR-030**: CLI commands MUST support `--reason` and `--sensitivity` flags for optional intent fields

**Intent Storage**:
- **FR-031**: System MUST store complete intent object in activity record
- **FR-032**: System MUST store which tool variant was used (call_tool_read/write/destructive)
- **FR-033**: Activity records MUST be queryable by operation_type

**CLI Display**:
- **FR-034**: `activity list` MUST display operation type column by default
- **FR-035**: Operation type MUST display with visual indicator (read/write/destructive icons)
- **FR-036**: `activity show` MUST display complete intent section
- **FR-037**: JSON/YAML output MUST include complete intent information

**REST API**:
- **FR-038**: `GET /api/v1/activity` responses MUST include intent with operation_type
- **FR-039**: `GET /api/v1/activity` MUST support `intent_type` filter parameter
- **FR-040**: Validation errors MUST return clear error messages

**Configuration**:
- **FR-041**: System MUST support `intent_declaration.strict_server_validation` config (default: true)
- **FR-042**: When strict_server_validation is false, server annotation mismatches log warning but allow call

### Key Entities

- **IntentDeclaration**: Agent's declared intent for a tool call
  - operation_type: Required, must match tool variant (read/write/destructive)
  - data_sensitivity: Optional classification (public/internal/private/unknown)
  - reason: Optional explanation (max 1000 chars)

- **ToolWithAnnotations**: Tool info returned by retrieve_tools
  - name: Tool identifier (server:tool format)
  - description: Tool description
  - inputSchema: JSON schema for arguments
  - annotations: Server-provided hints (readOnlyHint, destructiveHint)
  - call_with: Recommended tool variant to use

- **ActivityRecord** (existing): Extended to include intent field

### Validation Matrix

| Tool Variant | intent.operation_type | Server Annotation | Result |
|--------------|----------------------|-------------------|--------|
| call_tool_read | "read" | readOnlyHint=true | ALLOW |
| call_tool_read | "read" | destructiveHint=true | REJECT (server mismatch) |
| call_tool_read | "read" | no annotation | ALLOW (trust agent) |
| call_tool_read | "write" | any | REJECT (intent mismatch) |
| call_tool_write | "write" | readOnlyHint=true | WARN + ALLOW (overly cautious) |
| call_tool_write | "write" | destructiveHint=true | REJECT (server mismatch) |
| call_tool_write | "write" | no annotation | ALLOW |
| call_tool_write | "destructive" | any | REJECT (intent mismatch) |
| call_tool_destructive | "destructive" | any | ALLOW (most permissive) |
| call_tool_destructive | "read" | any | REJECT (intent mismatch) |
| any | missing | any | REJECT (intent required) |

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: Users can configure IDE permissions per tool variant, enabling auto-approve for reads
- **SC-002**: 100% of tool calls require matching intent.operation_type (two-key model)
- **SC-003**: System rejects 100% of intent mismatches (tool variant vs intent.operation_type)
- **SC-004**: System rejects 100% of server annotation mismatches in strict mode
- **SC-005**: retrieve_tools returns annotations and call_with guidance for all tools
- **SC-006**: Users can invoke tools via CLI with `mcpproxy call tool-read/write/destructive`
- **SC-007**: System processes tool calls with validation in under 10ms overhead
- **SC-008**: Activity filtering by intent_type returns accurate results

## Assumptions

- MCP servers may or may not provide tool annotations (readOnlyHint, destructiveHint)
- IDEs like Cursor support per-tool permission configuration
- The ActivityRecord structure from Spec 016 is implemented
- The activity CLI commands from Spec 017 are implemented
- Legacy call_tool removal is acceptable (breaking change)

## Tool Description Updates

The following built-in tools need description updates:

### retrieve_tools
```
Current: "Search for tools across all upstream servers..."
Updated: "Search for tools across all upstream servers. Results include annotations
(readOnlyHint, destructiveHint) and recommended call_with variant. Use call_tool_read
for read-only operations, call_tool_write for modifications, call_tool_destructive
for deletions. Intent must match tool variant."
```

### call_tool_read (new)
```
"Execute a read-only tool discovered via retrieve_tools. Use for operations that
query data without modifying state. Requires intent.operation_type='read'. Will be
rejected if server marks tool as destructive."
```

### call_tool_write (new)
```
"Execute a state-modifying tool discovered via retrieve_tools. Use for operations
that create or update resources. Requires intent.operation_type='write'. Will be
rejected if server marks tool as destructive."
```

### call_tool_destructive (new)
```
"Execute a destructive tool discovered via retrieve_tools. Use for operations that
delete or permanently modify resources. Requires intent.operation_type='destructive'.
Most permissive - allowed regardless of server annotations."
```

## Commit Message Conventions *(mandatory)*

When committing changes for this feature, follow these guidelines:

### Issue References
- Use: `Related #[issue-number]` - Links the commit to the issue without auto-closing
- Do NOT use: `Fixes #[issue-number]`, `Closes #[issue-number]`, `Resolves #[issue-number]`

### Example Commit Message
```
feat: add intent-based tool split (call_tool_read/write/destructive)

Related #[issue-number]

Replace call_tool with three operation-specific variants enabling granular
IDE permission control. Implements two-key security model requiring both
tool variant and intent.operation_type to match.

## Changes
- Add call_tool_read, call_tool_write, call_tool_destructive tools
- Remove legacy call_tool
- Implement two-key validation (tool variant + intent.operation_type)
- Validate against server destructiveHint/readOnlyHint
- Update retrieve_tools to include annotations and call_with guidance
- Add CLI commands: mcpproxy call tool-read/write/destructive

## Testing
- Unit tests for validation matrix
- E2E tests for IDE permission scenarios
- E2E tests for retrieve_tools annotations
```

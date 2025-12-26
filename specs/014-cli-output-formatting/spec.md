# Feature Specification: CLI Output Formatting System

**Feature Branch**: `014-cli-output-formatting`
**Created**: 2025-12-26
**Status**: Draft
**Input**: User description: "Implement CLI Output Formatting system (RFC-001 foundation)"

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Machine-Readable Output for AI Agents (Priority: P1)

AI agents (Claude Code, Cursor, Goose) need to parse CLI output programmatically. When an agent runs `mcpproxy upstream list`, it needs structured JSON output instead of human-formatted tables to reliably extract server information for decision-making.

**Why this priority**: This is the core value proposition of the feature. AI agents are primary users of mcpproxy CLI, and they cannot reliably parse table output. Without JSON output, agents resort to brittle regex parsing.

**Independent Test**: Can be fully tested by running `mcpproxy upstream list -o json` and verifying the output parses as valid JSON with expected schema.

**Acceptance Scenarios**:

1. **Given** the daemon is running with 3 upstream servers, **When** an agent runs `mcpproxy upstream list -o json`, **Then** the output is valid JSON array with server objects containing name, status, and health fields
2. **Given** the daemon is running, **When** an agent runs `mcpproxy upstream list --json`, **Then** the output is identical to `-o json` (alias works)
3. **Given** MCPPROXY_OUTPUT=json is set, **When** an agent runs `mcpproxy upstream list`, **Then** JSON output is used by default without explicit flag
4. **Given** an agent runs any CLI command with `-o json`, **When** an error occurs, **Then** the error is returned as structured JSON with error code, message, and recovery_command fields

---

### User Story 2 - Human-Readable Table Output (Priority: P2)

Developers debugging mcpproxy need clean, formatted table output that's easy to read in a terminal. The default output should be a well-formatted table with aligned columns.

**Why this priority**: Human developers need good UX when debugging, but they can fall back to web UI. AI agents have no alternative.

**Independent Test**: Can be tested by running `mcpproxy upstream list` and verifying output displays as aligned table with headers.

**Acceptance Scenarios**:

1. **Given** the daemon is running, **When** a user runs `mcpproxy upstream list` without flags, **Then** output displays as a formatted table with headers (NAME, STATUS, HEALTH, etc.)
2. **Given** a table has columns of varying widths, **When** the table is rendered, **Then** columns are aligned and readable
3. **Given** NO_COLOR=1 is set, **When** table output is rendered, **Then** no ANSI color codes are included

---

### User Story 3 - Hierarchical Command Discovery (Priority: P3)

AI agents need to discover available commands and their options without loading full documentation. The `--help-json` flag provides machine-readable command metadata for progressive discovery.

**Why this priority**: Reduces token usage for agents by allowing them to discover commands incrementally instead of loading full documentation.

**Independent Test**: Can be tested by running `mcpproxy --help-json` and verifying structured command tree is returned.

**Acceptance Scenarios**:

1. **Given** an agent runs `mcpproxy --help-json`, **Then** output contains JSON with commands array listing all top-level commands with name and description
2. **Given** an agent runs `mcpproxy upstream --help-json`, **Then** output contains subcommands array for upstream command with their options
3. **Given** an agent runs `mcpproxy upstream add --help-json`, **Then** output contains flags array with flag name, type, description, and required status

---

### User Story 4 - YAML Output for Configuration (Priority: P4)

Some users prefer YAML format for configuration export/import scenarios. YAML is more readable than JSON for complex nested structures.

**Why this priority**: Nice-to-have format option. JSON covers most use cases.

**Independent Test**: Can be tested by running `mcpproxy upstream list -o yaml` and verifying valid YAML output.

**Acceptance Scenarios**:

1. **Given** the daemon is running, **When** a user runs `mcpproxy upstream list -o yaml`, **Then** output is valid YAML matching JSON structure

---

### Edge Cases

- What happens when `-o json` and `--json` are both provided? (Should be handled gracefully - use JSON, don't error)
- What happens with `-o invalid`? (Return error with list of valid formats)
- How does output work when stdout is not a TTY? (Auto-detect and simplify table formatting)
- What happens when output data is empty? (Return empty array `[]` for JSON, "No results" for table)

## Requirements *(mandatory)*

### Functional Requirements

**Output Format Selection**:
- **FR-001**: System MUST support `-o/--output` flag with values: table, json, yaml
- **FR-002**: System MUST support `--json` as alias for `-o json`
- **FR-003**: System MUST respect `MCPPROXY_OUTPUT` environment variable as default format
- **FR-004**: Default output format MUST be "table" when no flag or environment variable is set
- **FR-005**: `-o/--output` and `--json` flags MUST be mutually exclusive (error if both specified with different values)

**JSON Output**:
- **FR-006**: JSON output MUST be valid, parseable JSON
- **FR-007**: JSON output MUST use snake_case for all field names
- **FR-008**: JSON output MUST NOT include ANSI color codes or formatting characters
- **FR-009**: Empty results MUST return empty array `[]` not null

**Table Output**:
- **FR-010**: Table output MUST include column headers
- **FR-011**: Table columns MUST be aligned for readability
- **FR-012**: Table output MUST respect `NO_COLOR=1` environment variable
- **FR-013**: Table output SHOULD auto-detect TTY and simplify formatting for non-TTY

**Help JSON**:
- **FR-014**: System MUST support `--help-json` flag on all commands
- **FR-015**: Help JSON MUST include command name, description, subcommands, and flags
- **FR-016**: Flags in help JSON MUST include name, shorthand, type, description, default, and required status

**Error Output**:
- **FR-017**: When `-o json` is used, errors MUST be returned as structured JSON
- **FR-018**: Error JSON MUST include fields: code, message, guidance, recovery_command
- **FR-019**: Error JSON SHOULD include context object with relevant state information

**Output Formatter Abstraction**:
- **FR-020**: All CLI commands MUST use shared output formatter instead of direct printing
- **FR-021**: Output formatter MUST be injectable for testing
- **FR-022**: Output formatter MUST handle both success and error cases

### Key Entities

- **OutputFormatter**: Abstraction that formats data for output. Implementations include TableFormatter, JSONFormatter, YAMLFormatter.

- **StructuredError**: Error representation with code, message, guidance, recovery_command, and context fields for machine-readable error handling.

- **HelpInfo**: Machine-readable command help with name, description, subcommands, and flags arrays.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: All CLI commands that produce output support `-o json` flag and produce valid JSON (verifiable by running each command and parsing output)
- **SC-002**: AI agents using mcp-eval scenarios achieve 95%+ trajectory similarity with JSON output vs baseline (verifiable by running mcp-eval test suite)
- **SC-003**: Table output renders correctly in terminals of 80+ character width (verifiable by manual testing)
- **SC-004**: `--help-json` returns valid JSON for all commands in under 100ms (verifiable by timing command execution)
- **SC-005**: Error output with `-o json` always returns parseable JSON with required fields (verifiable by triggering various error conditions)
- **SC-006**: Zero breaking changes to existing CLI output structure for JSON format (verifiable by comparing before/after output)

## Assumptions

- The current `-o json` implementation in some commands (like `upstream list`) can be extended rather than replaced
- Cobra framework's built-in help system can be extended to support `--help-json`
- Performance impact of formatter abstraction is negligible (simple delegation)
- YAML format uses standard Go yaml.v3 library encoding

## Dependencies

- **Existing Components**:
  - `cmd/mcpproxy/*.go`: CLI commands to be updated
  - Cobra CLI framework (already used)

- **New Components**:
  - `internal/cli/output/` package (to be created)

- **External**:
  - gopkg.in/yaml.v3 for YAML output

## Out of Scope

- CSV output format (planned for activity export in separate spec)
- JSONL streaming format (planned for activity watch in separate spec)
- Colorized JSON output (jq-style)
- Custom output templates
- Pager integration (less, more)

## Commit Message Conventions *(mandatory)*

When committing changes for this feature, follow these guidelines:

### Issue References
- Use: `Related #[issue-number]` - Links the commit to the issue without auto-closing
- Do NOT use: `Fixes #[issue-number]`, `Closes #[issue-number]`, `Resolves #[issue-number]` - These auto-close issues on merge

**Rationale**: Issues should only be closed manually after verification and testing in production, not automatically on merge.

### Co-Authorship
- Do NOT include: `Co-Authored-By: Claude <noreply@anthropic.com>`
- Do NOT include: "Generated with [Claude Code](https://claude.com/claude-code)"

**Rationale**: Commit authorship should reflect the human contributors, not the AI tools used.

### Example Commit Message
```
feat(cli): add output formatting system with -o/--output flag

Related #[issue-number]

Implement unified output formatting for CLI commands following RFC-001.
Adds TableFormatter, JSONFormatter, and YAMLFormatter implementations.

## Changes
- Add internal/cli/output package with OutputFormatter interface
- Implement TableFormatter with column alignment
- Implement JSONFormatter with snake_case fields
- Implement YAMLFormatter using yaml.v3
- Add --help-json flag support to all commands
- Add structured error output for -o json
- Update upstream list command to use new formatter

## Testing
- Unit tests for all formatters
- E2E tests for upstream list -o json
- mcp-eval scenarios updated for JSON output
```

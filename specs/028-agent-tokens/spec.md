# Feature Specification: Agent Tokens

**Feature Branch**: `028-agent-tokens`
**Created**: 2026-03-06
**Status**: Draft
**Input**: User description: "Scoped agent tokens for autonomous AI agents - users create API credentials limited to specific upstream servers, permission tiers, and with automatic expiry"

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Create a Scoped Agent Token (Priority: P1)

A user wants to run an autonomous AI agent (e.g., OpenClaw, Devin, or a custom CI pipeline) that needs programmatic access to MCPProxy. The user creates a scoped agent token via the CLI, specifying which upstream servers the agent can access, which permission tiers it can use (read/write/destructive), and when the token expires. The token is displayed once and the user copies it into the agent's configuration.

**Why this priority**: This is the core value proposition. Without token creation, nothing else works. It directly solves the problem of sharing a single global API key with multiple agents.

**Independent Test**: Can be fully tested by running `mcpproxy token create` with server and permission flags, verifying the token is returned, and confirming it appears in `mcpproxy token list`.

**Acceptance Scenarios**:

1. **Given** MCPProxy is running with upstream servers configured, **When** the user runs `mcpproxy token create --name "my-agent" --servers github,filesystem --permissions read,write --expires 30d`, **Then** a unique token with `mcp_agt_` prefix is displayed once, the token appears in `mcpproxy token list`, and the token has the specified server scope, permissions, and expiry date.
2. **Given** the user attempts to create a token with a name that already exists, **When** the command is run, **Then** the system rejects the request with a clear error message.
3. **Given** the user specifies a server name that does not exist in the current configuration, **When** the token is created, **Then** the system rejects the request and lists available servers.
4. **Given** the user does not specify an expiry, **When** the token is created, **Then** the system uses a default expiry (30 days) and informs the user.

---

### User Story 2 - Agent Uses Token to Access MCP (Priority: P1)

An autonomous agent connects to MCPProxy using a scoped agent token. The agent can only discover and call tools from the servers allowed by its token, and only using the permitted tool call tiers. Requests to out-of-scope servers or disallowed permission tiers are rejected.

**Why this priority**: This is the enforcement side of the core feature. Without scoping enforcement, tokens provide no security benefit over the global API key.

**Independent Test**: Can be tested by creating a token scoped to specific servers, connecting as that agent, and verifying that `retrieve_tools` only returns tools from allowed servers and `call_tool_*` rejects out-of-scope requests.

**Acceptance Scenarios**:

1. **Given** an agent token scoped to servers `[github, filesystem]`, **When** the agent calls `retrieve_tools`, **Then** only tools from `github` and `filesystem` servers are returned.
2. **Given** an agent token with permissions `[read, write]`, **When** the agent calls `call_tool_destructive`, **Then** the request is rejected with a clear error indicating insufficient permissions.
3. **Given** an agent token scoped to `[github]`, **When** the agent calls `call_tool_read` for a tool on the `jira` server, **Then** the request is rejected with a "server not in scope" error.
4. **Given** a valid agent token, **When** the agent sends a request via `Authorization: Bearer mcp_agt_...` or `X-API-Key: mcp_agt_...`, **Then** the request is authenticated and scoped correctly using either header format.
5. **Given** an expired agent token, **When** the agent attempts any request, **Then** the request is rejected with a "token expired" error.
6. **Given** a revoked agent token, **When** the agent attempts any request, **Then** the request is rejected with a "token revoked" error.

---

### User Story 3 - Manage Agent Tokens via REST API (Priority: P1)

An admin or automation script manages agent tokens programmatically through the REST API. Tokens can be created, listed, revoked, and regenerated. Token secrets are never returned in list responses.

**Why this priority**: REST API management is essential for programmatic agent provisioning — agents and CI pipelines need to create tokens for sub-agents without CLI access.

**Independent Test**: Can be tested by making HTTP requests to token CRUD endpoints and verifying correct responses and behavior.

**Acceptance Scenarios**:

1. **Given** a valid global API key, **When** `POST /api/v1/tokens` is called with name, servers, permissions, and expiry, **Then** a 201 response is returned containing the token secret (shown once) and token metadata.
2. **Given** tokens exist, **When** `GET /api/v1/tokens` is called, **Then** a list of all tokens is returned with metadata but without token secrets.
3. **Given** a token exists, **When** `DELETE /api/v1/tokens/{name}` is called, **Then** the token is revoked and subsequent use of that token is rejected.
4. **Given** a token exists, **When** `POST /api/v1/tokens/{name}/regenerate` is called, **Then** a new secret is generated for the same token configuration, the old secret stops working, and the new secret is returned once.
5. **Given** an agent token is used (not the global API key), **When** token management endpoints are called, **Then** the request is rejected — only the global API key can manage tokens.

---

### User Story 4 - Manage Agent Tokens via CLI (Priority: P2)

A user manages their agent tokens through CLI commands: create, list, revoke, and regenerate. The CLI provides clear output including token details and usage instructions.

**Why this priority**: CLI is the primary interface for developers setting up agents locally. Important but REST API can serve as the sole management interface in the MVP.

**Independent Test**: Can be tested by running CLI commands and verifying output format and behavior.

**Acceptance Scenarios**:

1. **Given** MCPProxy is running, **When** the user runs `mcpproxy token list`, **Then** a table is displayed showing all tokens with name, allowed servers, permissions, expiry date, and last used time.
2. **Given** a token exists, **When** the user runs `mcpproxy token revoke <name>`, **Then** the token is revoked and confirmation is displayed.
3. **Given** a token exists, **When** the user runs `mcpproxy token regenerate <name>`, **Then** a new secret is generated and displayed once, and the old secret stops working.
4. **Given** the user runs any `mcpproxy token` command with `-o json`, **Then** the output is formatted as JSON for scripting.

---

### User Story 5 - Activity Log with Agent Identity (Priority: P2)

All MCP requests made using agent tokens are recorded in the activity log with the agent's identity (token name and prefix). Users can filter activity by agent name to audit what each agent did.

**Why this priority**: Auditing is essential for security and debugging but builds on top of the existing activity log infrastructure.

**Independent Test**: Can be tested by making requests with an agent token and verifying the activity log entries contain agent identity fields.

**Acceptance Scenarios**:

1. **Given** an agent makes a tool call using its token, **When** the activity log is queried, **Then** the activity record includes the agent token name and token prefix (first 12 characters).
2. **Given** multiple agents have made requests, **When** the user filters by agent name, **Then** only activities from that specific agent are shown.
3. **Given** agent activity exists, **When** the user filters by auth type "agent_token", **Then** only activities from agent tokens (not the global API key) are shown.

---

### User Story 6 - Manage Agent Tokens via Web UI (Priority: P3)

Users manage agent tokens through the MCPProxy web dashboard. The UI provides a dedicated tab for listing, creating, revoking tokens with a visual interface including server selection checkboxes and permission pickers.

**Why this priority**: Web UI provides the best user experience but is not required for core functionality. CLI and REST API cover all management needs.

**Independent Test**: Can be tested by navigating to the Agent Tokens tab, creating a token via the dialog, and verifying it appears in the list.

**Acceptance Scenarios**:

1. **Given** the user navigates to the Agent Tokens tab in the web UI, **When** the page loads, **Then** a table of all tokens is displayed with name, servers, permissions, expiry, and last used.
2. **Given** the user clicks "Create Token", **When** they fill in name, select servers via checkboxes, choose a permission tier, set an expiry, and submit, **Then** the token is created and the secret is shown once in a copyable modal.
3. **Given** a token exists in the list, **When** the user clicks the revoke button, **Then** the token is revoked after confirmation and removed from the active list.

---

### Edge Cases

- What happens when an agent token references a server that is later removed from the configuration? The token continues to exist but requests to the removed server return "server not found" errors.
- What happens when an agent token references a server that is quarantined? Quarantined servers are excluded from the agent's scope — tool calls to quarantined servers are rejected.
- What happens when the maximum token count is reached? The system enforces a maximum of 100 agent tokens per instance and rejects creation requests with a clear error.
- What happens when a token with `allowed_servers: ["*"]` is used after new servers are added? The wildcard includes all non-quarantined servers dynamically — new servers are automatically accessible.
- What happens when the token's expiry is set beyond the maximum (365 days)? The system rejects the request and informs the user of the maximum allowed expiry.

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: System MUST allow users to create agent tokens with a unique name, a list of allowed upstream servers, a permission tier (read / read+write / read+write+destructive), and an expiry duration.
- **FR-002**: System MUST generate cryptographically random tokens (256 bits) with a distinguishable `mcp_agt_` prefix.
- **FR-003**: System MUST display the token secret exactly once at creation time and never again — list operations MUST NOT return the secret.
- **FR-004**: System MUST store token secrets as secure one-way hashes for lookup without exposing the original value.
- **FR-005**: System MUST validate agent tokens on every request by checking: valid hash, not expired, not revoked.
- **FR-006**: System MUST enforce server scoping on `retrieve_tools` — only returning tools from the token's allowed servers.
- **FR-007**: System MUST enforce server scoping on all tool call operations — rejecting calls to servers not in the token's scope.
- **FR-008**: System MUST enforce permission scoping — rejecting write operations if the token only has read permissions, and destructive operations if the token lacks destructive permission.
- **FR-009**: System MUST reject all administrative operations when authenticated with an agent token — including server management, quarantine management, token management, and configuration changes.
- **FR-010**: System MUST provide a REST API for token lifecycle management (create, list, revoke, regenerate) accessible only via the global API key.
- **FR-011**: System MUST provide CLI commands for token lifecycle management (create, list, revoke, regenerate).
- **FR-012**: System MUST record agent identity (token name and prefix) in all activity log entries for requests made with agent tokens.
- **FR-013**: System MUST support token revocation with immediate effect — revoked tokens MUST be rejected on the very next request.
- **FR-014**: System MUST support token regeneration — generating a new secret for the same token configuration while invalidating the old secret.
- **FR-015**: System MUST enforce a mandatory expiry on all agent tokens with a configurable maximum.
- **FR-016**: System MUST support a wildcard server scope to mean "all non-quarantined servers."
- **FR-017**: System MUST maintain full backward compatibility — the global API key MUST continue to work exactly as before, and agent tokens are purely additive.
- **FR-018**: System MUST support agent token authentication via both `Authorization: Bearer` and `X-API-Key` headers.
- **FR-019**: System MUST provide a web UI for token management including creation with server selection, permission picker, expiry setting, and revocation.
- **FR-020**: System MUST support activity log filtering by agent name and authentication type.

### Key Entities

- **Agent Token**: A scoped credential with name, hashed secret, prefix, allowed server list, permission list, expiry timestamp, creation timestamp, last-used timestamp, and revocation status.
- **Auth Context**: The resolved authentication identity for a request — includes auth type (admin/agent), agent name (if applicable), allowed servers, and permissions.
- **Token Scope**: The combination of allowed servers and permitted tool call tiers that define what an agent can do.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: Users can create a scoped agent token and have an autonomous agent making MCP requests within 5 minutes of starting.
- **SC-002**: Agent token validation adds negligible latency per request compared to global API key authentication.
- **SC-003**: All out-of-scope requests (wrong server, wrong permission tier, expired, revoked) are rejected with clear error messages that identify the specific reason for rejection.
- **SC-004**: Activity log entries for agent requests include sufficient identity information to determine which agent performed which action.
- **SC-005**: Zero breaking changes for existing users — all existing API key authentication and MCP functionality works identically after the feature is deployed.
- **SC-006**: Token secrets cannot be recovered from storage — only the one-time display at creation reveals the secret.

## Assumptions

- Agent token counts per instance will be small (under 100), making hash-based lookup performant without additional indexing.
- The existing database has sufficient capacity for agent token storage alongside existing data.
- Autonomous agents support standard HTTP authentication headers (`Authorization: Bearer` or `X-API-Key`).
- The existing activity log infrastructure can be extended with additional metadata fields without schema migration.
- Default expiry of 30 days is appropriate for most agent use cases, with a maximum of 365 days covering long-running CI pipelines.

## Commit Message Conventions *(mandatory)*

When committing changes for this feature, follow these guidelines:

### Issue References
- Use: `Related #[issue-number]` - Links the commit to the issue without auto-closing
- Do NOT use: `Fixes #[issue-number]`, `Closes #[issue-number]`, `Resolves #[issue-number]` - These auto-close issues on merge

**Rationale**: Issues should only be closed manually after verification and testing in production, not automatically on merge.

### Co-Authorship
- Do NOT include: `Co-Authored-By: Claude <noreply@anthropic.com>`
- Do NOT include: "Generated with Claude Code"

**Rationale**: Commit authorship should reflect the human contributors, not the AI tools used.

### Example Commit Message
```
feat(auth): add agent token creation with server and permission scoping

Related #028

Implement scoped agent tokens that allow autonomous AI agents to access
MCPProxy with restricted server access and permission tiers.

## Changes
- Add agent token CRUD in storage layer
- Add secure token hashing with prefix indexing
- Add token validation middleware with scope enforcement

## Testing
- Unit tests for token CRUD and validation
- Integration tests for scoped MCP requests
- E2E tests for CLI token management
```

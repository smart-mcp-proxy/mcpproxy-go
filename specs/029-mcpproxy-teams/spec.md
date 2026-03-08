# Feature Specification: MCPProxy Teams

**Feature Branch**: `029-mcpproxy-teams`
**Created**: 2026-03-06
**Status**: Draft
**Input**: Multi-user MCP proxy for teams of 2-50 people with identity provider authentication, per-user workspaces, credential isolation, and admin management.

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Team Admin Sets Up Multi-User Mode (Priority: P1)

A team administrator configures MCPProxy to operate in team mode by selecting an identity provider (Google, GitHub, Microsoft, or a generic provider). The admin specifies who can access the proxy via email allowlists, domain restrictions, or organization membership. Once configured, the proxy requires all users to authenticate through the chosen identity provider before accessing any MCP tools.

**Why this priority**: Without authentication infrastructure, no other team feature can function. This is the foundation that enables all multi-user capabilities.

**Independent Test**: Can be tested by configuring team mode with a test identity provider, then verifying that unauthenticated requests are rejected and authenticated users receive valid session tokens.

**Acceptance Scenarios**:

1. **Given** the proxy is configured in team mode with Google as the identity provider, **When** a user navigates to the MCP endpoint, **Then** they are redirected to Google sign-in and returned to the proxy after successful authentication.
2. **Given** the admin has set allowed domains to "company.com", **When** a user with a personal email attempts to log in, **Then** access is denied with a clear error message.
3. **Given** team mode is configured with Microsoft and the organization enforces MFA, **When** a user logs in, **Then** the identity provider handles MFA transparently and the proxy receives the authenticated identity after completion.
4. **Given** no team mode is configured (default), **When** the proxy starts, **Then** it operates in personal mode with existing single-user behavior unchanged.
5. **Given** the admin configures GitHub as the identity provider with an allowed organization, **When** a GitHub user who is not a member of that org attempts to log in, **Then** access is denied.
6. **Given** a generic identity provider is configured (e.g., Keycloak or Okta), **When** a user authenticates, **Then** the proxy extracts their identity from standard claims and grants access.

---

### User Story 2 - Team Members Access Shared MCP Servers (Priority: P2)

After authenticating, team members can immediately use all MCP servers that the admin has configured for the team. Each user sees the same set of team servers and can invoke tools through them. The system identifies which user is making each request, enabling per-user audit trails.

**Why this priority**: Shared server access is the primary value proposition — the reason teams deploy a shared proxy instead of individual ones.

**Independent Test**: Can be tested by having two authenticated users invoke the same team MCP server and verifying both succeed with their identity recorded in the activity log.

**Acceptance Scenarios**:

1. **Given** the admin has added "Jira" and "GitHub" as team servers, **When** an authenticated user lists available tools, **Then** they see tools from both servers.
2. **Given** two users are authenticated, **When** both invoke a tool on the same team server, **Then** both requests succeed and each activity record shows the respective user's identity.
3. **Given** the admin disables a team server, **When** a user tries to invoke tools from that server, **Then** the tools are unavailable.

---

### User Story 3 - Users Manage Personal Servers and Credentials (Priority: P3)

Each team member can add their own personal MCP servers beyond the shared team servers. Users securely store their own credentials (e.g., personal access tokens for Jira, API keys for third-party services) that are encrypted and isolated from other users and even admins. When a personal server has the same name as a team server, the personal configuration takes precedence for that user.

**Why this priority**: Per-user credential isolation solves the core security problem — shared service accounts giving all users access to restricted resources. This was the triggering use case (Jira/Confluence access pulled due to shared credentials).

**Independent Test**: Can be tested by having a user add a personal server, store credentials, invoke a tool that uses those credentials, and verifying another user cannot see or use the first user's credentials.

**Acceptance Scenarios**:

1. **Given** an authenticated user, **When** they add a personal MCP server with their Jira personal access token, **Then** the server is available only to that user.
2. **Given** a user has stored credentials for an upstream service, **When** the admin queries the system, **Then** the admin cannot read the user's stored credentials.
3. **Given** a user has a personal server named "jira" and a team server also named "jira", **When** the user invokes a Jira tool, **Then** the personal server configuration is used.
4. **Given** a user stores an upstream credential, **When** the proxy restarts, **Then** the credential is still available (persisted and encrypted at rest).

---

### User Story 4 - Per-User Server Isolation for Local Processes (Priority: P4)

When MCP servers run as local processes (e.g., filesystem access, database tools, Slack bots), each user gets their own isolated instance. One user's server process cannot access another user's data or credentials. Idle server processes are automatically cleaned up to conserve resources.

**Why this priority**: Without process isolation, a local MCP server started for one user could leak data to another. This is essential for security in a multi-user environment.

**Independent Test**: Can be tested by having two users each connect to a filesystem MCP server and verifying each user's server process runs independently with its own credentials and cannot access the other's working directory.

**Acceptance Scenarios**:

1. **Given** two users both use a filesystem MCP server, **When** each invokes a tool, **Then** each request is handled by a separate isolated process with that user's credentials.
2. **Given** a user's server process crashes, **When** another user invokes the same server type, **Then** the other user's process is unaffected.
3. **Given** a user has not used a local server process for longer than the configured idle timeout, **When** the timeout expires, **Then** the process is automatically stopped and resources reclaimed.
4. **Given** a user connects to a remote HTTP-based MCP server (e.g., Jira cloud), **When** they invoke a tool, **Then** no local process is spawned — the proxy connects directly with the user's credentials in the request headers.

---

### User Story 5 - Admin Manages Team Servers and Templates (Priority: P5)

The team admin configures shared MCP servers that are available to all team members. To simplify setup, the system provides built-in templates for popular services (Jira, Confluence, GitHub, GitLab, Sentry, Linear, Notion, Slack, PostgreSQL, Filesystem). Admins can also create custom templates. Users set up personal servers from templates by filling in their own credentials.

**Why this priority**: Templates dramatically reduce the friction of onboarding new team members — instead of configuring each server from scratch, users pick a template and enter their personal token.

**Independent Test**: Can be tested by an admin creating a team server from a template, then a user creating a personal server from the same template with their own credentials, and verifying both work independently.

**Acceptance Scenarios**:

1. **Given** the admin selects the "Jira" template, **When** they fill in the company domain, **Then** a team Jira server is created and available to all users.
2. **Given** a user views available templates, **When** they select "GitHub" and enter their personal access token, **Then** a personal GitHub server is created in their workspace.
3. **Given** the admin creates a custom template with a URL pattern and auth configuration, **When** users browse templates, **Then** the custom template appears alongside built-in ones.
4. **Given** a non-admin user, **When** they attempt to manage team-wide servers, **Then** the action is denied.

---

### User Story 6 - Users Create Scoped Agent Tokens (Priority: P6)

Each authenticated user can create agent tokens that are scoped to their own workspace. An agent token created by a user can only access servers that user has access to (team + personal). Agent tokens carry the creating user's identity for activity logging purposes.

**Why this priority**: Agent tokens enable unattended AI agent access without requiring interactive login. Per-user scoping ensures agents operate within the creating user's permissions.

**Independent Test**: Can be tested by a user creating an agent token restricted to specific servers, then using that token to invoke tools and verifying it only accesses allowed servers with the user's identity in the audit log.

**Acceptance Scenarios**:

1. **Given** an authenticated user, **When** they create an agent token with access restricted to "github" and "filesystem" servers, **Then** the token can only invoke tools on those two servers.
2. **Given** an agent token created by Alice, **When** the token is used to invoke a tool, **Then** the activity log records both the agent token name and Alice's user identity.
3. **Given** an agent token created by Bob with read-only permissions, **When** the token attempts a write operation, **Then** the operation is denied.
4. **Given** a user has created the maximum allowed number of agent tokens, **When** they attempt to create another, **Then** the request is rejected with a clear limit message.

---

### User Story 7 - Activity Log with User Identity (Priority: P7)

Every MCP tool invocation, server change, and administrative action is recorded with the identity of the user who performed it. Admins can view and filter activity across all users. Regular users can view only their own activity. Activity can be filtered by user, auth type (interactive login vs. agent token), agent name, server, tool, time range, and sensitivity level.

**Why this priority**: Per-user audit trails are essential for security compliance, incident investigation, and understanding team usage patterns.

**Independent Test**: Can be tested by having multiple users perform actions, then verifying the admin sees all activity with user attribution while each user sees only their own.

**Acceptance Scenarios**:

1. **Given** multiple users have performed tool invocations, **When** the admin views the activity log, **Then** each entry shows which user performed the action.
2. **Given** a regular user views the activity log, **When** they apply no filters, **Then** they see only their own activity.
3. **Given** activity from both interactive logins and agent tokens, **When** the admin filters by auth type "agent", **Then** only agent-token-initiated activity is shown.
4. **Given** the admin filters activity by a specific user, **When** results are displayed, **Then** only that user's activity appears.

---

### User Story 8 - Web Interface for Login, Dashboard, and Administration (Priority: P8)

The proxy provides a web interface where users log in via their identity provider, view their personal dashboard (available servers, agent tokens, recent activity), and manage their workspace. Admins access an additional panel for team server management, template configuration, and user overview.

**Why this priority**: A web interface makes the system accessible to non-technical team members and provides admins with a visual management surface.

**Independent Test**: Can be tested by logging into the web interface, viewing the dashboard, managing a personal server, and (as admin) managing team servers — all through the browser.

**Acceptance Scenarios**:

1. **Given** a user navigates to the web interface, **When** they are not authenticated, **Then** they see a login page with the configured identity provider option.
2. **Given** an authenticated user, **When** they access the dashboard, **Then** they see their available servers (team + personal), agent tokens, and recent activity.
3. **Given** an admin user, **When** they access the admin panel, **Then** they can manage team servers, view templates, see all users, and browse team-wide activity.
4. **Given** a non-admin user, **When** they attempt to access admin functionality, **Then** the admin panel is not visible or accessible.

---

### Edge Cases

- What happens when an identity provider is temporarily unavailable? Users with valid existing sessions continue working; new logins fail with a descriptive error suggesting retry.
- What happens when a user is removed from the allowed list while they have an active session? Their session tokens are invalidated at next refresh (within 1 hour). Active requests complete normally.
- What happens when a user's upstream credential (stored in their vault) expires or is revoked? The upstream server returns an auth error, which is surfaced to the user with guidance to update their credential.
- What happens when the admin switches identity providers? Existing user sessions are invalidated. Users must re-authenticate with the new provider. User data is preserved if the email matches.
- What happens when two users create personal servers with the same name? Each user's namespace is independent — name collisions between users are impossible.
- What happens when the system reaches the maximum number of concurrent local server processes? New process creation is queued or rejected with a clear capacity message. Existing processes are unaffected.
- What happens when a user who created agent tokens is removed from the team? All their agent tokens are automatically revoked. Existing in-flight requests complete, but new requests with those tokens are rejected.

## Requirements *(mandatory)*

### Functional Requirements

**Authentication & Identity**

- **FR-001**: System MUST support team mode activation via configuration, separate from the existing personal mode.
- **FR-002**: System MUST authenticate users via external identity providers: Google, GitHub, Microsoft Entra ID, and any standards-compliant generic provider.
- **FR-003**: System MUST issue its own session tokens after successful identity provider authentication — identity provider tokens are never exposed to MCP clients.
- **FR-004**: System MUST support access control via email allowlists, domain restrictions, and organization membership depending on the identity provider.
- **FR-005**: System MUST support multi-factor authentication transparently through the identity provider's own MFA enforcement.
- **FR-006**: System MUST support automatic discovery of authentication requirements by MCP clients per the MCP specification.
- **FR-007**: System MUST support token refresh without requiring the user to re-authenticate interactively (within the refresh token lifetime).

**User Workspaces**

- **FR-008**: System MUST provide each authenticated user with a workspace combining team-wide servers and their personal servers.
- **FR-009**: System MUST allow personal server configurations to override team servers of the same name for that user only.
- **FR-010**: System MUST allow users to add, edit, and remove their own personal MCP servers.
- **FR-011**: System MUST keep each user's workspace isolated — one user cannot see or modify another user's personal servers.

**Credential Management**

- **FR-012**: System MUST encrypt all user-stored upstream credentials at rest.
- **FR-013**: System MUST ensure that no user (including admins) can read another user's stored upstream credentials.
- **FR-014**: System MUST persist encrypted credentials across proxy restarts.

**Server Process Isolation**

- **FR-015**: System MUST run local (stdio-based) MCP servers as separate isolated processes per user.
- **FR-016**: System MUST inject per-user credentials into isolated server processes at startup.
- **FR-017**: System MUST connect to remote (HTTP-based) MCP servers using per-user credentials in request headers without spawning local processes.
- **FR-018**: System MUST automatically clean up idle local server processes after a configurable timeout.

**Admin Management**

- **FR-019**: System MUST support an admin role determined by email match or identity provider claim.
- **FR-020**: Admins MUST be able to manage team-wide MCP server configurations.
- **FR-021**: Admins MUST be able to create, edit, and remove server templates.
- **FR-022**: Admins MUST be able to view all users' activity logs.
- **FR-023**: Non-admin users MUST NOT be able to perform admin actions (team server management, viewing other users' activity, user management).

**Server Templates**

- **FR-024**: System MUST provide built-in templates for popular services: Jira, Confluence, GitHub, GitLab, Sentry, Linear, Notion, Slack, PostgreSQL, and Filesystem.
- **FR-025**: Each template MUST define a URL pattern, authentication type, and required user-provided variables (e.g., domain, personal access token).
- **FR-026**: Admins MUST be able to create custom templates beyond the built-in set.
- **FR-027**: Users MUST be able to create personal servers from templates by providing their own credentials.

**Agent Tokens**

- **FR-028**: Each user MUST be able to create agent tokens scoped to their workspace.
- **FR-029**: Agent tokens MUST only access servers within the creating user's workspace (team + personal).
- **FR-030**: Agent token activity MUST be attributed to both the token and the creating user in the activity log.

**Activity & Audit**

- **FR-031**: All MCP tool invocations MUST be recorded with the identity of the user who performed them.
- **FR-032**: Admins MUST be able to filter activity by user, auth type, agent name, server, tool, time range, and sensitivity level.
- **FR-033**: Non-admin users MUST only see their own activity.

**Web Interface**

- **FR-034**: System MUST provide a login page that redirects to the configured identity provider.
- **FR-035**: System MUST provide a user dashboard showing available servers, agent tokens, and recent activity.
- **FR-036**: System MUST provide an admin panel for team server management, template configuration, user overview, and team-wide activity.

**Backward Compatibility**

- **FR-037**: When operating in personal mode (default), the system MUST behave identically to the current single-user version with no team features active.
- **FR-038**: The system MUST be built as a separate binary from the personal edition using Go build tags (`-tags teams`), from the same repository and `cmd/mcpproxy` entry point.

**Build & Distribution**

- **FR-039**: The personal edition MUST be the default build output (`go build ./cmd/mcpproxy`). The teams edition MUST require an explicit build tag (`go build -tags teams ./cmd/mcpproxy`).
- **FR-040**: The teams edition MUST be distributed as a Docker image (`ghcr.io/smart-mcp-proxy/mcpproxy-teams`), a `.deb` package for Ubuntu/Debian, and a Linux binary tarball.
- **FR-041**: The personal edition MUST be distributed as macOS DMG installer (with native Swift tray app), Windows MSI/EXE installer (with native C# tray app), Linux binary tarball, and via Homebrew.
- **FR-042**: Both editions MUST be released under a single GitHub release tag (e.g., `v0.21.0`) with assets clearly named by edition (`mcpproxy-*` for personal, `mcpproxy-teams-*` for teams).
- **FR-043**: The teams Docker image MUST bind to `0.0.0.0:8080` by default (not localhost) and MUST NOT include tray/GUI components.
- **FR-044**: Each binary MUST self-identify its edition (personal or teams) in version output, startup logs, and the `/api/v1/status` endpoint.

### Key Entities

- **User**: An authenticated team member identified by email from an identity provider. Has a role (admin or member), a workspace, stored credentials, and agent tokens.
- **Workspace**: The effective set of MCP servers available to a user — the union of team servers and the user's personal servers, with personal overriding team on name conflict.
- **Team Server**: An MCP server configured by an admin, available to all authenticated team members.
- **Personal Server**: An MCP server configured by an individual user, available only to that user.
- **Credential Vault Entry**: An encrypted upstream service credential (e.g., Jira PAT, GitHub token) belonging to a specific user.
- **Server Template**: A reusable server configuration pattern with variable placeholders (e.g., domain, token) that users fill in to create servers quickly.
- **Agent Token**: A scoped access token created by a user for unattended AI agent access, restricted to a subset of that user's workspace.
- **Activity Record**: A log entry for an MCP operation, attributing it to a user and optionally an agent token.
- **Identity Provider Configuration**: The admin-defined settings for connecting to an external authentication service (Google, GitHub, Microsoft, or generic).

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: An admin can configure team mode and have the first user authenticate in under 10 minutes using the simplest identity provider setup path.
- **SC-002**: A new team member can log in, browse available tools, and successfully invoke their first MCP tool within 3 minutes of receiving access.
- **SC-003**: System supports 50 concurrent authenticated users, each with up to 10 personal servers, without noticeable degradation in tool invocation response time.
- **SC-004**: Per-user credential isolation is absolute — no pathway exists for one user to read another user's stored upstream credentials, verified by security testing.
- **SC-005**: Activity logs for any user or time range can be retrieved in under 2 seconds.
- **SC-006**: Admin can onboard a new team member (grant access) without restarting the proxy.
- **SC-007**: A user setting up a personal server from a template completes the process in under 1 minute.
- **SC-008**: Existing personal mode deployments experience zero behavior change when upgrading to the version that includes team capabilities.
- **SC-009**: Idle local server processes are cleaned up within 1 minute of the configured timeout, with no orphaned processes after 24 hours of operation.
- **SC-010**: All four identity providers (Google, GitHub, Microsoft, generic) pass authentication end-to-end tests with test accounts.

## Assumptions

- The deployment environment has outbound internet access to reach identity providers (Google, GitHub, Microsoft endpoints).
- The host machine supports running isolated local processes (container runtime available for stdio server isolation).
- Teams of 2-50 people represent the target scale. Scaling beyond 50 users is explicitly out of scope for v1.
- Users have existing accounts with the configured identity provider (the system does not provision identity provider accounts).
- Session token lifetime defaults to 1 hour with 7-day refresh tokens — standard web application session management.
- Agent token maximum per user follows the same limit as the personal edition (10 tokens per user).
- Admin designation is static per configuration (email list or IdP claim) — there is no in-app role assignment UI in v1.
- Server template definitions are static (shipped with the binary + admin-defined in config) — there is no template marketplace or community sharing.
- Both editions are built from the same repository (`mcpproxy-go`) using Go build tags. Teams-only code lives in `internal/teams/` with `//go:build teams` guards. No `pkg/` migration is needed.
- Data retention for activity logs follows the same policy as the personal edition — configurable, with no default auto-purge.

## Scope Boundaries

### In Scope (v1)

- Team mode with identity-provider-based authentication
- Four identity providers: Google, GitHub, Microsoft Entra ID, Generic (covering Keycloak, Okta, Auth0)
- Per-user workspaces with team + personal servers
- Encrypted per-user credential vault
- Per-user isolated local server processes for stdio-based servers
- Per-user credential injection for HTTP-based remote servers
- Admin and member roles
- Server templates (10 built-in + custom)
- Per-user agent tokens
- Activity log with user identity and filtering
- Web UI: login, dashboard, admin panel
- Backward-compatible personal mode
- Same-repo build tag architecture (`-tags teams`)
- Single GitHub release with labeled assets per edition
- Docker image and deb package for teams distribution
- Native tray apps: Swift (macOS), C# (Windows) for personal distribution

### Out of Scope (v1)

- OpenTelemetry metrics/tracing export
- Direct LDAP authentication (supported via IdP federation with Keycloak/Okta)
- Policy engine integration (OPA, Cedar)
- Multi-instance high-availability deployment
- SCIM user provisioning
- Per-tool role-based access control (access is per-server, not per-tool)
- License key enforcement or seat management
- Template marketplace or community sharing
- In-app role assignment (admin is config-driven)
- Docker image for personal edition (it's a desktop app)
- macOS DMG / Windows installer for teams edition (it's a server product)
- Kubernetes Helm chart (v1 — Docker Compose is the recommended multi-container setup)
- Separate repositories for native tray apps (Swift/C# live in `native/` within the same repo)

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

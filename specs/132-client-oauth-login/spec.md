# Feature Specification: Browser login for MCP clients (opt-in OAuth), next to API keys

**Feature Branch**: `claude/oauth-api-key-client-role-eb4179`
**Created**: 2026-11-22
**Status**: Draft
**Input**: Issue [#1971](https://github.com/smart-mcp-proxy/mcpproxy-go/issues/1971): let MCPProxy act as the OAuth authorization server for its own MCP endpoint so a client needs only a URL. Keep API keys; let the user choose. Decide how a client is identified after login.

**Related**: [design.md](./design.md) is the approved and cross-reviewed design; it holds the decisions, the rejected alternatives and the protocol detail this spec refers to. [Spec 108](../108-profiles-v3/) (client credentials and bindings), [Spec 115](../115-mcp-credential-lifecycle/) (credential lifecycle), [Spec 089](../089-remote-access-tunnel/spec.md) (tunnel, later reuse).

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Updating changes nothing (Priority: P1)

A user who updates MCPProxy and never asked for browser login sees no difference. Every client keeps working with the credential it has. No prompt, no new screen, no new entry in any menu.

**Why this priority**: Algis's condition for shipping the first release. The feature must not cost existing users anything.

**Independent Test**: Run the existing end-to-end suites and a route-table comparison against the previous release with the feature at its default. Everything is identical.

**Acceptance Scenarios**:

1. **Given** a default configuration after an update, **When** any existing client connects with its API key, client credential or agent token, **Then** it behaves exactly as before the update.
2. **Given** the default configuration, **When** a request is sent to any browser-login address or discovery document, **Then** the answer is the same as in the previous release.
3. **Given** the default configuration, **When** the user opens Connect, the Clients hub, Settings or the tray, **Then** nothing about browser login is shown except one switch on the Settings page, off and marked "Experimental".
4. **Given** the default configuration, **When** MCPProxy starts, **Then** it creates no new stored data and shows no notice about this feature.

---

### User Story 2 - Sign a known client in with the browser (Priority: P1)

A user turns browser login on, chooses "Sign in with browser" for Cursor in Connect, and restarts Cursor. The browser opens a page that says "Sign-in request for Cursor". The user clicks Approve. Cursor is connected with Cursor's profile, and no secret was written to Cursor's config file.

**Why this priority**: This is the feature. It also answers the identity question: Connect gave Cursor its own address, so MCPProxy knows the request is for Cursor without guessing.

**Independent Test**: With the feature on, configure a scripted client with a per-client address, complete the flow with one approval, call a tool, and confirm the activity row names that client and its profile.

**Acceptance Scenarios**:

1. **Given** the feature is on and Cursor already has a profile binding, **When** a sign-in request arrives on Cursor's address, **Then** the consent page shows Cursor as the client, not selectable, with its current profile, and one click on Approve completes the login.
2. **Given** an approved login, **When** the client calls tools, **Then** it reaches exactly what Cursor's profile allows, and activity is attributed to Cursor.
3. **Given** a client that is new to MCPProxy and profiles exist, **When** its sign-in request is shown, **Then** no profile is pre-selected and Approve is disabled until the user picks one.
4. **Given** no profiles are configured, **When** a new client's sign-in request is shown, **Then** "All servers" is the pre-filled access.
5. **Given** a sign-in request, **When** the user clicks Deny, **Then** the client is told access was denied and nothing is stored for it.
6. **Given** the consent page shows a name the app reported about itself, **Then** that name is labelled as unverified and the page states in words whose access is being handed over.

---

### User Story 3 - Manage and revoke logins (Priority: P1)

The user opens the Clients hub. Cursor's row lists its credentials: an API key and two browser logins. The user revokes one login; the other and the API key keep working. Changing Cursor's profile changes access for all of them.

**Why this priority**: Without revoke and a single place for access, browser login would be a second, weaker client model.

**Independent Test**: Create a client with an API key and two logins, revoke one login, reassign the profile, and check each credential's access after each step.

**Acceptance Scenarios**:

1. **Given** a client with several credentials, **When** one browser login is revoked, **Then** it stops working on its next request, including on an open session, and the client's other credentials keep working.
2. **Given** a client, **When** the client itself is revoked, **Then** its API key and every browser login stop working.
3. **Given** a client, **When** its profile is changed, **Then** every credential of that client uses the new profile on its next request.
4. **Given** a client is forgotten, **Then** its browser logins are deleted with it.
5. **Given** a browser login unused for 30 days, **Then** it no longer works and the client must sign in again.

---

### User Story 4 - Approve when the browser is not signed in to MCPProxy (Priority: P2)

The client opens the user's default browser, which has never held MCPProxy's admin key. The page cannot show the request. It tells the user to open the request from the MCPProxy tray or to paste the API key. The tray shows a notification "Approve sign-in for Cursor…" that opens the request ready to approve.

**Why this priority**: Common on a fresh browser profile. Without it the first login fails with no way forward.

**Independent Test**: Open a sign-in request in a browser profile with empty storage; confirm the guidance, then approve through the tray entry.

**Acceptance Scenarios**:

1. **Given** a browser without the admin key, **When** the consent address opens, **Then** no detail of the request, of clients or of profiles is shown, only the two ways to continue.
2. **Given** a pending sign-in request on macOS, **When** the user picks the tray entry, **Then** the same request opens with the client and profile shown and can be approved.
3. **Given** the user pastes a valid API key, **Then** the request is shown and can be approved.

---

### User Story 5 - A client MCPProxy has never seen (Priority: P2)

The user types the generic browser-login address into a client that Connect does not know. The consent page opens with "New client" selected and the name filled in; the user keeps or edits the name, picks a profile, and approves. When the name the app reports matches exactly one client MCPProxy already knows, that client is selected instead and the user confirms or changes it. The client appears in the Clients hub.

**Why this priority**: Keeps the feature useful beyond the supported-client list, while the reported name only suggests and never decides.

**Independent Test**: Run the flow on the generic address with an unknown client name; confirm the new client row, its profile and its activity attribution.

**Acceptance Scenarios**:

1. **Given** a sign-in request on the generic address whose reported name matches no known client, or more than one, **Then** "New client" is selected with the name filled from the reported name, and the user can pick an existing client instead.
2. **Given** a sign-in request on the generic address whose reported name matches exactly one known client, **Then** that client is selected, the page says the selection comes from an unverified name, and the user can change it before approving.
3. **Given** a suggested client that has a profile, **When** the user approves without changing anything and the app completes the sign-in, **Then** the stored login belongs to the suggested client with that client's profile.
4. **Given** a suggested client, **When** the user picks another existing client, approves, and the app completes the sign-in, **Then** the stored login belongs to the client the user picked.
5. **Given** a suggested client, **When** the user picks "New client", approves, and the app completes the sign-in, **Then** a new client is created and the suggested client is unchanged.
6. **Given** an existing client is selected, by suggestion or by the user, **Then** the page states that the app receives that client's access before Approve is enabled.
7. **Given** an address that names a client id that does not exist, **Then** every step answers "not found" and no client is created.

---

### User Story 6 - Choose the credential type in Connect (Priority: P3)

With the feature on, Connect offers "API key" (default) and "Sign in with browser" for clients that support it. The preview shows that the browser option writes only an address, no secret.

**Why this priority**: Convenience. The flow already works when the address is typed by hand.

**Independent Test**: Connect a supported client with each option and compare the written config with the preview.

**Acceptance Scenarios**:

1. **Given** the feature is on and the client supports browser login, **When** the user opens Connect, **Then** both options are offered and "API key" is selected.
2. **Given** "Sign in with browser", **When** the change is applied, **Then** the client's config holds its own address and no credential, and the usual preview and backup notice were shown first.
3. **Given** the feature is off, **Then** Connect shows no choice.

---

### User Story 7 - Turn it off again (Priority: P2)

The user tried browser login and turns it off. Browser-login clients stop working at once and do not quietly continue as anonymous callers. Turning it on again restores them without a new approval.

**Why this priority**: An experimental switch must be safe to flip back.

**Independent Test**: Sign a client in, turn the feature off, confirm its requests are refused, turn it on, confirm it works again.

**Acceptance Scenarios**:

1. **Given** the feature was used and is turned off, **When** a browser-login client sends a request to its address, **Then** the request is refused, whatever `require_mcp_auth` is.
2. **Given** the feature was used, the last browser-login client was forgotten, and the feature is turned off, **Then** browser-login addresses are still refused.
3. **Given** the feature is turned on again, **Then** existing logins work without a new approval, and the Clients hub no longer says "browser login disabled".
4. **Given** pending sign-in requests, **When** the feature is turned off, **Then** they are cancelled.

### Edge Cases

- Two browser tabs approve the same request: one succeeds, the other is told it was already decided. Approve after Deny is refused.
- The client's profile changes while the consent page is open: approval is refused with "this changed, review again" and the page reloads.
- The chosen profile is a named one while `require_mcp_auth` is off: approval would let the client escape its profile by dropping its credential. The page shows the refusal and the fixes before the button can be pressed.
- A refresh credential that was already used comes back: that login is revoked. A made-up or malformed one changes nothing, even if it names a real login.
- A login approved for one client's address is presented on another address, or on `localhost` instead of `127.0.0.1`: refused.
- Many registrations that are never approved: they are capped and expire.
- A sign-in request left open for more than 10 minutes: expired; the client starts again.
- MCPProxy restarts during a sign-in: the pending request is gone; the client starts again. Approved logins survive.
- The request arrives from another machine on the network, or MCPProxy is the server edition: browser login answers "not found".
- The name a client sends once connected differs from what its row expects: an advisory warning on the row, nothing blocked.
- Downgrade to a release without the feature: API-key clients keep their correct profile; the docs tell the user to turn `require_mcp_auth` on first so browser-login clients do not become anonymous callers.

## Requirements *(mandatory)*

### Functional Requirements

**Default off, nothing changes**

- **FR-001**: Browser login MUST be off by default and controlled by one setting (config, environment variable, Settings switch marked "Experimental"). Turning it on or off MUST NOT need a restart.
- **FR-002**: While the feature is off and was never used, every address and document it would serve MUST answer as in the previous release, no stored data may be created for it, and no surface other than the Settings switch may mention it. This MUST be pinned by a test that compares against the previous behaviour.
- **FR-003**: API keys, client credentials and agent tokens MUST keep working unchanged whether the feature is on or off.

**Login flow**

- **FR-004**: With the feature on, a client given only an address MUST be able to complete the MCP authorization flow: discovery, self-registration, authorization with proof of possession, and renewal without the browser. The protocol shapes are those in design.md "Flow" and "Metadata".
- **FR-005**: Only public clients are accepted. A registration asking for a client secret or another kind of grant MUST be refused.
- **FR-006**: Return addresses MUST be loopback addresses or secure web addresses, matched exactly apart from the loopback port.
- **FR-007**: A login MUST be bound to the exact address and origin it was approved for. It MUST be refused on any other address or origin, and a different target presented at exchange or renewal MUST be refused.
- **FR-008**: The client's own round-trip value MUST be returned unchanged on both the approve and the deny answer.
- **FR-009**: Renewal MUST replace the renewal credential every time. Reuse of a genuine, already used renewal credential MUST revoke that login. A malformed, forged or unknown one MUST get one indistinguishable refusal and MUST change nothing.
- **FR-010**: Version 1 MUST serve browser login only to requests from the same machine, and MUST answer "not found" in the server edition.
- **FR-011**: Registration, authorization and exchange MUST be rate limited and size limited; never-approved registrations MUST be capped and expire (limits in design.md "Limits and abuse").

**Which client it is**

- **FR-012**: A per-client address MUST identify the client. On such a request the consent page MUST show that client as fixed.
- **FR-013**: On the generic address the page MUST offer "New client" and every existing client, and the user MUST be able to change the selection before approving. An existing client may be pre-selected only when the reported name matches exactly one known client (id, display name or known client name, compared without regard to case); the page MUST then say that the selection comes from an unverified name. Otherwise "New client" is selected.
- **FR-014**: The name an app reports about itself MUST be shown as unverified and MUST NOT grant a client, a profile or any access by itself: a login is stored only for the client the administrator approved. It may pre-fill the name field of a new client and, on the generic address, pre-select a client under FR-013. On a per-client address it MUST NOT affect the selection.
- **FR-015**: A per-client address whose id is neither a supported client nor an existing custom client MUST answer "not found" at every step. Typing an address MUST NOT create a client.
- **FR-016**: When the same registration signs in again, the page MUST say so and offer to replace its earlier login. Otherwise a new login is added beside existing ones.

**Consent**

- **FR-017**: Every login MUST be approved by the administrator on the consent page, every time, including a repeat login. No login may be approved silently or from the command line in version 1.
- **FR-018**: The consent page MUST show: the client, the reported name as unverified, where the browser returns to, the profile, and a sentence stating whose access the app receives.
- **FR-019**: For a client with no binding: with no profiles configured the access is "All servers"; with profiles configured the user MUST pick one and none is pre-selected. A named profile defaults to locked.
- **FR-020**: A sign-in request MUST be decided at most once (approved, denied, expired or cancelled) and MUST yield at most one login.
- **FR-021**: Approval MUST be refused, with a request to review again, when the client's binding differs from what the page displayed.
- **FR-022**: Approval that would create a binding a client can escape while `require_mcp_auth` is off MUST be refused by the existing binding guard, and the page MUST show that refusal and the guard's fixes before the user approves.
- **FR-023**: In a browser without the admin key the page MUST reveal nothing about the request and MUST offer: open from the tray, or paste the API key. The macOS tray MUST announce a pending request and open it. (The Windows/Linux tray follows in a later change.)
- **FR-024**: The consent page MUST NOT be embeddable in another page, and approval MUST NOT be possible by a cross-site request.

**One client, one binding**

- **FR-025**: A client MUST be one row in the Clients hub with at most one API key and any number of browser logins.
- **FR-026**: A client MUST have exactly one profile binding, used by all its credentials and changed in one place. Reassign, rename, profile deletion rules and the binding guard MUST treat a client that has only browser logins exactly like one with an API key.
- **FR-027**: A browser-login request MUST be authorized, scoped and attributed exactly like the same client's API-key request, with the credential kind and the login recorded so two logins can be told apart.
- **FR-028**: A downgrade to an earlier release MUST NOT give an API-key client a broader profile than its current one.

**Manage**

- **FR-029**: The Clients hub, the REST API, the CLI and the `credentials` administrator tool MUST list a client's browser logins (no secrets) and revoke them. None of them may create a login.
- **FR-030**: Revoking a client MUST revoke its API key and all its logins. Revoking a single credential MUST leave the client's others working. Forgetting a client MUST delete its logins.
- **FR-031**: Revocation MUST take effect on the next request, including on an open session.
- **FR-032**: A login MUST lapse after 30 days without use.
- **FR-033**: Issue, approve, deny, replay-revocation and revoke MUST each produce an activity record naming the acting administrator.

**Turn off**

- **FR-034**: Once any login has ever been stored, turning the feature off MUST make browser-login addresses refuse requests instead of serving them as anonymous, MUST cancel pending requests, and MUST keep logins and bindings so that turning it on restores them. "Ever used" MUST survive forgetting every client and restarts.

**Connect and docs**

- **FR-035**: While the feature is on, Connect MUST offer "API key" (default) and "Sign in with browser" for clients marked as supporting it, with the existing preview and backup notice. The browser option MUST write only the client's own address.
- **FR-036**: Documentation MUST cover: enabling, the two kinds of address, the consent page, managing logins, turning off, downgrade, and the fact that version 1 serves plain HTTP on the local machine unless local TLS is on, with the list of clients verified to work.

### Key Entities

- **Client**: the identity in the Clients hub. Has one profile binding and its credentials.
- **Registration**: what an app declared about itself when it registered: reported name, return addresses, state.
- **Sign-in request**: one pending approval. Short-lived, decided once.
- **Browser login (grant)**: one approved login of one registration for one client and one address. Can be revoked; lapses when unused.
- **Binding**: the profile and mode (locked or switchable) of a client, shared by all its credentials.
- **Ever-used marker**: remembers that browser login has been used on this installation.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: With the feature at its default, 100% of the existing test suites pass unchanged and the comparison against the previous release shows no difference on any address.
- **SC-002**: A user connects a supported client by browser login in under one minute and with one click on the consent page when the client already has a profile.
- **SC-003**: No secret is written to a client's config file in the browser-login path.
- **SC-004**: In every tested flow the client shown on the consent page is the one named by the address, or the one selected on the page when the user approved. On a per-client address zero cases depend on the reported name; on the generic address the reported name changes only what is pre-selected, never the stored result of an approval.
- **SC-005**: Each client on the supported list completes the whole flow, including renewal, in a recorded real-client check before that client is offered in Connect: at least Claude Code, Cursor, VS Code and MCP Inspector are attempted, and only those that pass are listed.
- **SC-006**: A revoked login is refused on its next request in 100% of tested cases, including on an open session.
- **SC-007**: After the feature was used and turned off, zero requests to a browser-login address are served.
- **SC-008**: Every abuse case in the edge-case list (double approval, changed binding, replayed renewal, forged renewal, wrong address, wrong origin, remote request) has an automated test that passes.

## Assumptions

- Personal edition only. Server edition and the tunnel are later work and reuse these parts.
- The administrator is whoever holds the admin API key in the browser or opens the request from the tray. There is no separate user login in the personal edition.
- Token lifetimes (one hour for access, 30 days sliding for renewal) and the numeric limits are as in design.md and may be tuned during planning without changing this spec.
- MCP clients follow discovery for an address below the MCP root. SC-005 verifies this per client before the client is offered.
- No new dependencies.

## Out of Scope

- Replacing or deprecating API keys and agent tokens.
- Browser login on the existing MCP addresses or routing-mode addresses.
- Clients with secrets, hosted client identity documents, approval from the CLI, custom-scheme return addresses.
- Server edition behaviour, the tunnel, the Windows/Linux tray notification (tracked as a follow-up change).

## Commit Message Conventions *(mandatory)*

When committing changes for this feature, follow these guidelines:

### Issue References
- ✅ **Use**: `Related #1971` - Links the commit to the issue without auto-closing
- ❌ **Do NOT use**: `Fixes #1971`, `Closes #1971`, `Resolves #1971` - These auto-close issues on merge

**Rationale**: Issues should only be closed manually after verification and testing in production, not automatically on merge.

### Co-Authorship
- ❌ **Do NOT include**: `Co-Authored-By: Claude <noreply@anthropic.com>`
- ❌ **Do NOT include**: "🤖 Generated with [Claude Code](https://claude.com/claude-code)"

**Rationale**: Commit authorship should reflect the human contributors, not the AI tools used.

### Example Commit Message
```
feat(oauth): browser login for MCP clients, backend (Spec 132 PR-1)

Related #1971

Opt-in authorization server for the MCP endpoint, off by default.

## Changes
- Discovery, registration, authorization and exchange endpoints
- Per-client and generic browser-login addresses

## Testing
- Default-off comparison against the previous release
- Scripted public client end to end
```

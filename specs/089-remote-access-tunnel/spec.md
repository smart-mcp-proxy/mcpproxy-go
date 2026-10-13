# Feature Specification: Remote Access Tunnel (MVP, feature-flagged)

**Feature Branch**: `089-remote-access-tunnel`
**Created**: 2026-07-29
**Status**: Draft
**Input**: User description: "Remote Access Tunnel (MVP, feature-flagged): expose the local mcpproxy /mcp endpoint to the public internet through an external tunnel binary (cloudflared quick tunnel first; Tailscale Funnel as second provider) so cloud AI clients — primarily Claude custom connectors (claude.ai web + iOS/Android via web-side setup) — can reach local MCP servers like Obsidian MCP. One button in the Web UI opens/closes the tunnel; mandatory OAuth 2.1 gate; per-server exposure allowlist; feature flag, off by default."

**Research basis**: [docs/research/remote-access-tunnel-research-2026-07-29.html](../../docs/research/remote-access-tunnel-research-2026-07-29.html) — deep-research run (25/25 claims adversarially verified). Key verified facts: Claude custom connectors are available on **all plan tiers including Free** and sync to iOS/Android, but Anthropic's **cloud** (not the user's device) originates the connection, so the MCP endpoint must be publicly reachable over HTTPS with Streamable HTTP; local stdio servers are unavailable in claude.ai web/mobile. NSA and Trend Micro guidance mandates gateway-layer authentication with proxy-managed token lifecycle for any internet-exposed MCP endpoint (thousands of unauthenticated MCP servers are already exposed and being scanned). Docker MCP Gateway — the closest competitor — has no such feature despite open user demand.

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Expose my local MCP servers to Claude mobile in a few clicks (Priority: P1)

A user runs mcpproxy on their desktop with local MCP servers configured (e.g., Obsidian MCP over their notes vault). They want to ask Claude on their phone questions that use those local tools. Today this is impossible: Claude's cloud cannot reach `127.0.0.1`. With this feature, the user enables the remote-access feature flag in config, opens the Web UI, picks which servers to expose, presses **"Open remote access"**, and gets a public HTTPS URL (plus QR code and step-by-step instructions for adding it as a custom connector on claude.ai web). After the one-time web-side setup, the connector appears in the Claude iOS/Android app and tools from the exposed local servers work from the phone.

**Why this priority**: This is the entire point of the feature — the verified, unmet use case (Claude connectors work on every plan tier, but only against a public endpoint). Without this journey working end-to-end there is no feature.

**Independent Test**: With the flag enabled, press the button in the Web UI, confirm a public URL is issued, add it as a custom connector on claude.ai, and successfully call an exposed server's tool from the Claude mobile app. Delivers the full "local files from my phone" value on its own.

**Acceptance Scenarios**:

1. **Given** the feature flag is enabled and at least one upstream server is marked as exposed, **When** the user presses "Open remote access" in the Web UI, **Then** within 30 seconds the UI shows a public HTTPS URL, a QR code, and connector-setup instructions, and the tunnel state is "active".
2. **Given** an active tunnel, **When** a Claude client completes the authorization flow and calls `retrieve_tools`/`call_tool_*`, **Then** only tools from exposed servers are visible/callable and the calls succeed end-to-end.
3. **Given** an active tunnel, **When** the user presses "Close remote access" (or shuts down mcpproxy), **Then** the tunnel process terminates, the public URL stops being served by the proxy (requests to it no longer reach mcpproxy), and the UI/tray state returns to "inactive".
4. **Given** the feature flag is disabled (default), **When** the user opens the Web UI or queries the API, **Then** no remote-access UI, endpoints, or tunnel behavior are present — behavior is identical to today.

---

### User Story 2 - Remote endpoint is locked by mandatory authentication (Priority: P1)

A security-conscious user (and the project itself) must be guaranteed that opening the tunnel never exposes an unauthenticated MCP endpoint to the internet. The tunneled endpoint requires every client to complete an OAuth 2.1 authorization flow (with PKCE and Dynamic Client Registration so Claude's zero-config connector setup works); unauthenticated requests through the tunnel are rejected. The user authorizes the connector by logging in with a local credential shown/configured in the Web UI. Tokens expire, can be rotated, and can be revoked from the Web UI ("disconnect this client").

**Why this priority**: Non-negotiable safety requirement. Research documented thousands of unauthenticated MCP servers already exposed and scanned (including shell-exec tools and leaked personal records); NSA/Trend Micro guidance converges on a mandatory gateway-layer auth with proxy-managed token lifecycle. Shipping a bare tunnel would be a reputational disaster for a security-focused proxy.

**Independent Test**: With an active tunnel, issue unauthenticated MCP requests to the public URL and confirm rejection; complete the OAuth flow and confirm access; revoke the client and confirm subsequent requests are rejected. Testable without any Claude client (any MCP-capable HTTP client suffices).

**Acceptance Scenarios**:

1. **Given** an active tunnel, **When** an MCP request arrives through the tunnel without valid authorization, **Then** it is rejected with a standards-compliant auth challenge and no MCP payload is served (no tool listing, no tool calls, no server metadata).
2. **Given** a Claude connector performing zero-config setup, **When** it registers dynamically and completes the authorization flow (with the user approving via local login), **Then** it receives a token accepted only through the tunnel origin, scoped to exposed servers.
3. **Given** an authorized remote client, **When** the user revokes its access in the Web UI, **Then** its tokens stop working within one minute without restarting mcpproxy — including for already-established sessions: the next message on any open session is refused.
4. **Given** an issued token past its expiry, **When** it is used, **Then** the request is rejected and the client must re-authorize (or refresh, if a refresh path is granted).
5. **Given** any configuration attempt to disable authentication on the tunneled endpoint, **Then** no such configuration exists — there is no supported way to run the tunnel without the auth gate (local `/mcp` behavior on localhost remains unchanged).

---

### User Story 3 - Least-privilege exposure: I choose exactly which servers go remote (Priority: P2)

The user has 15 upstream servers configured but only wants Obsidian MCP and a read-only weather server reachable remotely. In the Web UI they toggle "exposed via remote access" per server (default: none exposed). Remote clients see only the exposed subset; local clients continue to see everything. Quarantined servers cannot be exposed.

**Why this priority**: Core least-privilege control that makes the feature compatible with mcpproxy's local-first, security-first identity. P2 only because the tunnel (US1) and the lock (US2) must exist first; exposure control is what makes it safe to use daily.

**Independent Test**: Expose one of two servers; through the tunnel confirm search/list/call only reaches the exposed one while the local endpoint still reaches both.

**Acceptance Scenarios**:

1. **Given** servers A (exposed) and B (not exposed), **When** a remote client searches or lists tools, **Then** only A's tools appear; **When** it attempts to call a B tool by name, **Then** the call is refused.
2. **Given** a quarantined server, **When** the user tries to mark it exposed, **Then** the action is blocked with an explanation.
3. **Given** no servers exposed, **When** the user tries to open the tunnel, **Then** opening is blocked with an explanation (no public surface is created when nothing can be served).
4. **Given** a server is un-exposed (or becomes quarantined) while the tunnel is active, **Then** remote visibility of its capabilities ends without restart, including for already-established sessions — the next remote message touching it is refused.

---

### User Story 4 - Full visibility of remote activity (Priority: P2)

Every authenticated MCP request that arrives through the tunnel is recorded in the activity log marked with a "remote" origin, and passes through the same quarantine and sensitive-data detection pipeline as local traffic; rejected/unauthenticated probes are counted in bounded aggregate form rather than logged per request. The Web UI and tray clearly show when the tunnel is active (persistent indicator/warning), so the user can always tell their proxy is reachable from the internet, review what remote clients did, and spot anomalies.

**Why this priority**: Transparency is the project's stated pillar and the compensating control that security guidance requires for internet-exposed endpoints. P2 because it hardens and audits journeys delivered by US1/US2.

**Independent Test**: Make remote and local calls; verify the activity log distinguishes remote origin, sensitive-data detection fires on remote responses, and Web UI + tray show the active-tunnel indicator while open and clear it when closed.

**Acceptance Scenarios**:

1. **Given** an active tunnel, **When** a remote client calls a tool, **Then** the activity log entry carries a remote-origin marker (distinguishable and filterable) alongside the usual request correlation data.
2. **Given** a remote tool response containing detectable sensitive data, **When** it flows through the proxy, **Then** the sensitive-data detection behaves exactly as for local traffic (same detections, same logging).
3. **Given** the tunnel is active, **Then** the Web UI shows a persistent warning banner and the tray shows a distinct "remote access active" state; both clear when the tunnel closes.
4. **Given** mcpproxy restarts, **Then** the tunnel does NOT auto-restart; remote access requires a fresh explicit user action (the exposure allowlist and flag persist, the live tunnel does not).

---

### Edge Cases

- **Tunnel binary missing**: cloudflared (or the chosen provider binary) is not installed → the UI detects this before attempting to start, explains what is missing, and links/offers guidance to install it; no partial "active" state.
- **Tunnel process dies unexpectedly** (network loss, binary crash, provider outage): state transitions to "error/closed" within seconds, UI/tray indicators update, an activity/log entry records the termination; no silent zombie "active" display.
- **Provider URL churn**: quick tunnels issue a new random URL each start → instructions must warn that closing/reopening changes the URL and the Claude connector must be updated; the UI surfaces the current URL as the single source of truth.
- **Claude callback domain changes**: the authorization redirect allowlist covers both documented Anthropic callback domains, and the research notes Anthropic reserves the right to change them → allowlist must be maintainable via configuration without a release.
- **Clock skew / long-lived sessions**: token expiry validation must tolerate reasonable clock skew while still enforcing expiry.
- **Flag disabled while tunnel active**: config hot-reload turns the feature flag off → tunnel is torn down immediately, remote tokens stop being accepted.
- **Concurrent open attempts**: pressing "Open" twice, or opening from two browser tabs → exactly one tunnel process; second request returns the existing state.
- **Local API surface**: the tunnel must forward only the MCP endpoint plus the enumerated authorization surface (discovery metadata, DCR, authorize/consent, token) — the REST management API, Web UI, and events stream must NOT be reachable through the tunnel.
- **Rate abuse**: a remote client hammering the endpoint (or an internet scanner probing the public URL) must not exhaust local resources — throttling applies to tunnel-origin traffic, and auth challenges are cheap (no upstream work before authorization).

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001 (Feature flag)**: The entire capability MUST be gated behind a configuration feature flag, default **off**. With the flag off: no remote-access UI elements, no additional API surface, no tunnel processes, no OAuth-server endpoints — observable behavior identical to current releases.
- **FR-002 (Explicit start, never auto-start)**: Remote access MUST start only on explicit user action and MUST NOT persist across proxy restarts. Stopping mcpproxy MUST terminate the tunnel.
- **FR-003 (External tunnel orchestration)**: The system MUST establish public reachability by orchestrating an external tunnel provider binary; MVP acceptance is defined against Cloudflare quick tunnel only. Provider-specific behavior (availability detection, process launch/supervision/termination, public-URL discovery) MUST sit behind a single internal provider contract so a second provider (e.g. Tailscale Funnel) is an additive implementation of that contract; the contract itself is the testable artifact (MVP tests exercise it via the Cloudflare implementation).
- **FR-004 (One-button UX)**: The Web UI MUST provide a single control to open/close remote access, displaying: current state (inactive / starting / active / error), the public URL, a QR code of the URL, and step-by-step instructions for adding the URL as a Claude custom connector (including the web-only setup + mobile sync caveat).
- **FR-005 (Mandatory auth gate)**: Every MCP request arriving via the tunnel MUST require successful authorization before any MCP payload is served or any upstream work occurs. The gate implements an OAuth 2.1 authorization server with PKCE (S256 only) and Dynamic Client Registration compatible with the MCP authorization specifications supported by Claude clients (2025-03-26, 2025-06-18, 2025-11-25). Redirect-URI validation MUST use exact-match against registered URIs (no wildcard, suffix, or prefix matching), HTTPS-only (loopback excepted per OAuth 2.1), pre-seeded with both documented Anthropic callback URIs and maintainable via configuration. Authorization codes MUST be single-use and bound to the requesting client, redirect URI, and PKCE verifier. There MUST be no configuration that disables this gate while the tunnel is active.
- **FR-006 (User-approved authorization ceremony)**: Completing an authorization grant MUST require explicit user approval on a consent page that identifies the requesting client and the currently exposed servers, protected by a proxy-managed credential. The credential MUST be generated (never a default), MUST meet a minimum entropy bar, MUST be rotatable from the Web UI, and the consent flow MUST have brute-force throttling and CSRF protection. The consent page MAY be served through the tunnel (that is how a cloud-initiated redirect reaches the user's browser), but approval is impossible without the credential. Silent/anonymous grants are prohibited.
- **FR-007 (Token lifecycle & binding)**: The proxy MUST manage token lifecycle itself: bounded token lifetime, refresh path, per-client revocation from the Web UI effective within one minute, and persistence of user approvals across restarts. Two layers are distinct: (a) the **user approval/grant** (client registration + consent) persists across tunnel sessions and restarts; (b) **access tokens** are audience-bound to the canonical resource URI of the tunnel session that issued them (per the MCP authorization specs' resource-indicator requirement) and MUST NOT be accepted for a different resource URI — after the tunnel reopens under a new URL, clients MUST obtain new tokens for the new resource URI (re-authorization MAY proceed without a fresh consent ceremony because the approval persists, provided client identity is unchanged and the exposure set is re-evaluated at dispatch per FR-008). Closing the tunnel or disabling the feature flag MUST suspend acceptance of all remote credentials (they resume only when remote access is explicitly reopened); revocation is permanent.
- **FR-008 (Per-server exposure allowlist — full MCP surface)**: Exposure MUST be opt-in per upstream server, default none. The allowlist governs the entire MCP capability surface — tools, resources, prompts, completions, subscriptions, and notifications — not only tool discovery/calls. Authorization MUST be evaluated against the current allowlist at the moment of each dispatch (effective access = granted client ∩ currently exposed servers), so expose/un-expose/quarantine transitions apply immediately, including to established sessions. Quarantined servers MUST be non-exposable. Changes take effect without restart.
- **FR-009 (Scope of exposure)**: Tunnel-origin requests MAY reach only: (a) the MCP protocol endpoint (post-authorization, with pre-authorization requests answered by a 401 challenge carrying `resource_metadata` discovery information), and (b) the minimal authorization surface required to obtain access — OAuth protected-resource metadata (`.well-known/oauth-protected-resource`), authorization-server discovery metadata, DCR, authorization/consent, and token endpoints — plus nothing else. The 401-challenge and metadata-discovery bootstrap required by the supported MCP authorization specs MUST be covered by tests. REST management API, Web UI assets (other than the consent page of FR-006), and event streams MUST NOT be served to tunnel-origin requests. The pre-authorization surface MUST be explicitly enumerated in the design and covered by tests.
- **FR-010 (Remote-origin activity, redacted & bounded)**: All tunnel-origin MCP activity MUST be recorded in the activity log with a distinct, filterable remote-origin marker, and MUST pass through existing quarantine and sensitive-data detection pipelines unchanged. Authorization material — authorization headers, codes, PKCE verifiers, tokens, cookies, credentials — MUST never be recorded. Rejected/unauthenticated probe traffic MUST be logged in bounded, aggregated form (count/rate, not per-request records) so scanners cannot exhaust storage.
- **FR-011 (Status surfacing)**: Tunnel state MUST be visible in the Web UI (persistent warning banner while active) and in the tray (distinct state/indicator), driven by the same state source, updating within seconds of state changes.
- **FR-012 (Failure honesty)**: The system MUST actively verify tunnel health (process liveness plus a periodic end-to-end reachability probe of the public URL) and MUST transition to a non-active state within one probe interval (≤30 seconds) of the tunnel process exiting or the public URL becoming unreachable, surfacing the reason and logging the event. No stale "active" indications.
- **FR-013 (Abuse resistance, per endpoint)**: Tunnel-origin traffic MUST be rate-limited independently of local traffic, and unauthenticated probes MUST be rejected before any upstream server interaction occurs. The pre-authorization surface MUST have endpoint-specific protections: DCR registration quotas with automatic cleanup of never-approved registrations, per-endpoint rate limits on authorization/token requests, and request-size caps — each with defined limits verified by tests.
- **FR-014 (Unspoofable ingress boundary)**: Tunnel-origin traffic MUST enter through a dedicated ingress (e.g., a separate listener used exclusively by the tunnel process) whose classification as "remote" cannot be forged or bypassed via request headers, host names, or paths. Remote traffic MUST never be able to reach the local endpoint's authentication semantics (API-key or socket bypass), and tests MUST prove that tunnel-origin requests cannot enter the local path.
- **FR-015 (Documentation)**: User-facing documentation MUST cover: what the feature does and its risks, the security model (auth gate, allowlist, logging), provider prerequisites, the Claude connector setup walkthrough, and the URL-churn caveat of quick tunnels.

### Key Entities

- **Tunnel session**: The lifecycle of one remote-access activation — provider, public URL, state (inactive/starting/active/error/closed), start/stop timestamps, terminating reason.
- **Remote client grant**: A dynamically registered client plus its user-approved authorization — client identity, token metadata (issue/expiry/rotation), revocation/suspension state. Effective access at any moment is this grant intersected with the *current* exposure allowlist (FR-008), never a snapshot.
- **Exposure allowlist**: Per-upstream-server boolean "exposed via remote access", persisted in configuration; interacts with quarantine state (quarantined ⇒ non-exposable).
- **Remote activity record**: Existing activity-log record extended with an origin marker (local vs remote) for filtering and audit.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: A user with the flag enabled can go from "no tunnel" to "working Claude custom connector on their phone" in under 10 minutes, with at most one external tool installation and no manual config-file editing.
- **SC-002**: 100% of unauthenticated MCP requests through the tunnel are rejected without reaching any upstream server, in every shipped configuration (verified by automated tests — there is no reachable unauthenticated state).
- **SC-003**: With the feature flag off, a full regression pass shows zero behavioral or API differences versus the prior release.
- **SC-004**: Remote calls appear in the activity log with correct remote-origin marking in 100% of cases; sensitive-data detection parity between local and remote traffic is verified by tests.
- **SC-005**: Tunnel state shown in Web UI and tray matches the real process state within 5 seconds across open/close/crash scenarios.
- **SC-006**: Revoking a remote client renders its credentials unusable within 60 seconds without a proxy restart.
- **SC-007**: Within 10 seconds of closing the tunnel (or stopping mcpproxy), requests to the public URL no longer reach the proxy (HTTP reachability, not DNS resolution, is the criterion).

## Assumptions

- **Provider choice**: Cloudflare quick tunnel is the first provider because it is free, requires no account for ephemeral URLs, and issues HTTPS URLs immediately; the research confirms free external tunnels undercut any hosted-relay approach for an MVP. Tailscale Funnel is the planned second provider, not required for MVP acceptance.
- **Ephemeral URLs are acceptable for MVP**: quick-tunnel URLs change on every start; the UX mitigates via visible current-URL + instructions. Stable/named tunnels (user-owned Cloudflare account or domain) are a future enhancement, not MVP.
- **Claude is the target client**: ChatGPT custom MCP apps are web-only and enterprise-gated today (verified 2026-07-29), so acceptance is defined against Claude connectors; nothing in the design may preclude other MCP-over-HTTP clients that implement the same auth specs.
- **Auth reuse**: The personal edition currently has OAuth *client* code and the server edition has an OAuth login stack (Spec 024). The MVP brings a spec-compliant authorization-server role to the personal edition; how much is reused is a planning-phase decision, not a spec constraint.
- **Local behavior unchanged**: localhost `/mcp` auth semantics (`require_mcp_auth`, socket bypass) are out of scope and unchanged; the mandatory gate applies to tunnel-origin traffic.
- **Roadmap placement**: normal priority (P2), sequenced after TPA DB, security-scanner work, macOS tray redesign, and usage-graphs-as-dashboard (per product owner, 2026-07-29).

## Out of Scope (MVP)

- Own relay backend / hosted infrastructure, payments, legal entity — revisit only if opt-in telemetry shows sustained tunnel usage.
- P2P/WebRTC transport (cannot serve Claude/ChatGPT: their clouds originate connections and require a public HTTPS endpoint).
- Embedding tunnel libraries in-process (zrok SDK, tsnet) — future phase after MVP validates demand.
- ChatGPT-specific support, stable custom domains, multi-user remote access (server edition already covers team scenarios).
- Exposing the Web UI or REST API remotely.

## Commit Message Conventions *(mandatory)*

### Issue References
- ✅ **Use**: `Related #[issue-number]` - Links the commit to the issue without auto-closing
- ❌ **Do NOT use**: `Fixes #[issue-number]`, `Closes #[issue-number]`, `Resolves #[issue-number]` - These auto-close issues on merge

**Rationale**: Issues should only be closed manually after verification and testing in production, not automatically on merge.

### Co-Authorship
- ❌ **Do NOT include**: `Co-Authored-By: Claude <noreply@anthropic.com>`
- ❌ **Do NOT include**: "🤖 Generated with [Claude Code](https://claude.com/claude-code)"

**Rationale**: Commit authorship should reflect the human contributors, not the AI tools used.

### Example Commit Message
```
feat(tunnel): [brief description of change]

Related #[issue-number]

## Changes
- [Bulleted list of key changes]

## Testing
- [Test results summary]
```

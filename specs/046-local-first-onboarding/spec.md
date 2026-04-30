# Feature Specification: Adaptive First-Run Onboarding Wizard & Extended Client Connect

**Feature Branch**: `046-local-first-onboarding`
**Created**: 2026-04-28
**Status**: Draft (v2 amendment 2026-04-30)

> **v2 amendment** (2026-04-30): see "v2 — Wizard Surface Redesign" at the end of this document.
> The original v1 spec text below describes the shipped first cut (PR #433). Where v2
> contradicts v1, v2 wins. v1 is preserved verbatim for review traceability.
**Input**: User description: "Adaptive onboarding wizard that adjusts to the user's state — connect-a-client step shown only if no clients are connected, add-a-server step shown only if no upstream servers are configured (with a quarantine explainer), both shown back-to-back if neither exists. Plus extending the existing /api/v1/connect endpoint (Spec 039) with more clients (mcp-manager-style coverage)."

## Background

Spec 039 (Connect Clients & Dashboard) is shipped: `GET/POST/DELETE /api/v1/connect/{client}` endpoint, `mcpproxy connect|disconnect` CLI, hub dashboard. Seven clients currently supported.

Spec 032 (tool-level quarantine) and Spec 026 (sensitive-data detection) provide the trust boundary: every newly added server enters quarantine until the user explicitly approves it. Tool changes are hash-tracked (rug-pull detection). These are the existing safety net.

This feature adds three things on top:

1. An **adaptive first-run onboarding wizard** that detects what the user is missing — clients, servers, or both — and walks them through only the steps that apply, in the right order.
2. **Extended client coverage** behind the existing connect endpoint, growing the adapter table from 7 to ~20 clients.
3. **Onboarding telemetry** — connected-client state plus wizard funnel events — so we can measure whether the wizard moves retention and iterate from data.

### Baseline funnel (snapshot 2026-03-23 → 2026-04-28, n=1,491 unique installs)

Pulled from the production telemetry D1 (`mcpproxy-telemetry`, opt-out daily heartbeats).

| Stage | Installs | % of total |
|---|---:|---:|
| Installed (any heartbeat) | 1,491 | 100.0% |
| Ever had ≥1 server configured | 1,460 | 97.9% |
| Ever had ≥1 server connected | 1,389 | 93.2% |
| Ever had ≥3 servers | 1,375 | 92.2% |
| Ever had any tool indexed | 543 | 36.4% |
| Ever ran mcpproxy 24+ h continuously | 160 | 10.7% |
| Came back for day 2 | 176 | **11.8%** |
| Came back across 7+ days | 77 | 5.2% |

**What the data implies, and how it shaped this spec:**

- *Adding servers is not the dominant friction.* 97.9% already have servers, almost certainly via the import-from-existing-client-configs path on first launch. The wizard's "no servers" step targets the ~2% remainder.
- *Day-2 retention (11.8%) is the actual problem.* The most likely cause is users never wiring mcpproxy into an AI client, so on day 2 nothing draws them back. Connecting a client is therefore the wizard's high-leverage primary path.
- *We have no telemetry today on connected-client state.* The thing we most need to measure is invisible. This feature adds the missing telemetry as a first-class requirement (US3), gated by the same opt-out/anonymous heartbeat infrastructure and privacy posture as Spec 042 / Spec 044.

## User Scenarios & Testing *(mandatory)*

### User Story 1 — Adaptive Onboarding Wizard (Priority: P1)

A developer launches mcpproxy. The Web UI inspects the user's state — are any AI clients currently connected? are any upstream MCP servers configured? — and presents a wizard with **only the steps that are missing**:

- **Neither clients nor servers** → A two-step wizard: "Step 1: Connect an AI client" → "Step 2: Add an MCP server (and how quarantine works)".
- **No clients, has servers** → A one-step wizard: "Connect an AI client" only.
- **Has clients, no servers** → A one-step wizard: "Add an MCP server" only, including a short quarantine explainer.
- **Has clients and servers** → No wizard. The user lands directly on the dashboard.

The wizard is the user's first impression. Every step has a clear primary action, a "Skip for now" escape hatch, and never advances on its own. The two-step path frames clients as the precondition — "once mcpproxy is plugged into your AI client, your assistant can help you add servers" — and the server step explains quarantine plainly: "every server you add starts in quarantine until you approve it; that's mcpproxy's safety net."

**Why this priority**: This is the user's first 60 seconds with mcpproxy. Getting them from "what is this?" to "I have it connected and serving at least one server" is the single highest-leverage onboarding investment. Adapting to state means we don't drag through steps the user has already done (e.g. installing mcpproxy alongside an existing config that already has servers, or connecting the second machine of an experienced user).

**Independent Test**: Can be fully tested by running mcpproxy from a fresh state and from each of the three pre-existing states (no clients / no servers, has clients / no servers, no clients / has servers, both), confirming that (a) the wizard auto-shows in the first three cases and not the fourth, (b) only the missing steps appear, (c) each step's primary action completes the corresponding goal-state, (d) Skip on any step lands on the dashboard without partial commits, and (e) the wizard does not auto-show on subsequent launches once completed or skipped.

**Acceptance Scenarios**:

1. **Given** a fresh mcpproxy install with no upstream servers configured and no AI clients connected, **When** the user opens the Web UI, **Then** the wizard auto-appears with two visible steps in order: "Connect an AI client" then "Add an MCP server".
2. **Given** the wizard's "Connect an AI client" step is shown and Claude Code, Cursor, and VS Code are detected, **When** the step renders, **Then** detected clients are listed with status badges and the user can click "Connect to all detected" or per-client Connect, with a diff preview before any file is written.
3. **Given** the user completes the "Connect an AI client" step, **When** they advance, **Then** the "Add an MCP server" step is shown next.
4. **Given** the "Add an MCP server" step is shown, **When** it renders, **Then** the user sees a short, plain-language explainer of how quarantine works — every newly added server starts in quarantine, the user reviews and approves it before any AI client can call its tools — alongside the existing add-server controls (registry browse, JSON paste, manual entry).
5. **Given** the user adds a server via the wizard, **When** the server is added, **Then** it enters quarantine as it would through any other entry point, and the wizard's success summary explicitly says "Your server is in quarantine — review and approve it on the Servers page."
6. **Given** mcpproxy already has at least one upstream server but no AI client is connected, **When** the user opens the Web UI for the first time, **Then** the wizard appears with only the "Connect an AI client" step.
7. **Given** mcpproxy has at least one connected AI client but no upstream servers, **When** the user opens the Web UI for the first time, **Then** the wizard appears with only the "Add an MCP server" step (including the quarantine explainer).
8. **Given** mcpproxy has at least one connected AI client and at least one upstream server, **When** the user opens the Web UI for the first time, **Then** the wizard does not auto-show; the user lands on the dashboard.
9. **Given** the user clicks "Skip for now" on any step, **When** they confirm, **Then** they land on the dashboard without any partial writes from that step, and the wizard remembers having been skipped (does not re-show automatically).
10. **Given** the wizard is not auto-showing because the user previously completed or skipped it, **When** the user wants to re-run it (e.g. after uninstalling all clients), **Then** they can re-open it from a "Run setup wizard" link in the Clients section of the dashboard.
11. **Given** no AI clients are detected on the machine during the connect step, **When** the step renders, **Then** the user sees a friendly explanation, links to install instructions for supported clients, and a "Skip for now" option.
12. **Given** the wizard advances between steps, **When** progress is tracked, **Then** the user sees a small step indicator (e.g. "Step 1 of 2") so they know how much remains.
13. **Given** the user completes the final step (or completes the only step in a one-step variant), **When** they finish, **Then** they land on the dashboard with a contextual hint suited to what they just did — e.g. "Now ask your AI assistant to use mcpproxy" after the connect step, or "Review and approve your new server" after the add-server step.
14. **Given** the user opens mcpproxy via the tray app rather than directly visiting the Web UI, **When** the tray launches the Web UI for the first time, **Then** the wizard auto-show logic behaves identically to a direct browser visit.

---

### User Story 2 — Extended Client Coverage (Priority: P2)

The existing connect endpoint supports 7 clients. mcp-manager (a comparable tool we researched) supports ~20. A developer using one of the additional clients — for example Zed, Antigravity, Amazon Q, OpenCode, LM Studio, Cline, Roo Code, Junie, Copilot CLI, Copilot JetBrains, Kiro, Pi, or Amp — should be able to use mcpproxy's existing one-click connect with the same UX. The connect endpoint, the CLI, and the wizard all gain these adapters; nothing else changes.

**Why this priority**: Direct extension of an already-shipped feature. Pure breadth, no new UX surfaces. Each adapter is small, isolated work; not all need to land at once. Tier-1 (Spec 039) is already done.

**Independent Test**: Can be fully tested per-client by installing the target client in its default location, calling `GET /api/v1/connect`, confirming it appears with `exists: true`, calling `POST /api/v1/connect/{id}`, opening the resulting config file, verifying mcpproxy is correctly registered in that client's native shape (preserving existing keys), and confirming the client successfully connects to mcpproxy on its next start.

**Acceptance Scenarios**:

1. **Given** the user has a tier-2 client installed (e.g. Zed, Antigravity, Amazon Q, OpenCode, LM Studio), **When** they call `GET /api/v1/connect`, **Then** that client appears in the response with `exists: true` and the correct `config_path`.
2. **Given** the user calls `POST /api/v1/connect/{id}` for a tier-2 client, **When** the request succeeds, **Then** mcpproxy is added to that client's native config using the client's exact native shape (correct top-level key, correct entry shape, correct URL/auth field name), all other entries are preserved, and a timestamped backup exists.
3. **Given** a tier-2 client uses a non-standard shape (e.g. Zed's `context_servers` with `command: {path, args}` object, Amp's dotted `amp.mcpServers`, OpenCode's `mcp` with command-as-array, TOML formats), **When** mcpproxy registers itself, **Then** the entry exactly matches the client's documented schema and the client launches mcpproxy on next start without manual intervention.
4. **Given** a tier-2 client requires CLI-only registration (e.g. `<tool> mcp add-json …`), **When** the user calls `POST /api/v1/connect/{id}`, **Then** mcpproxy invokes the client's own CLI rather than editing a config file directly, and reports the outcome with the same response schema as file-based clients.
5. **Given** the user calls `DELETE /api/v1/connect/{id}` for a tier-2 client they previously connected, **When** the request succeeds, **Then** only mcpproxy's entry is removed; everything else in the client's config is byte-equivalent to before connect.
6. **Given** a tier-2 client is installed in a non-default location, **When** detection runs, **Then** detection either finds it via well-known fallback paths (per-client expanded list) or reports it as not detected without producing a false positive.
7. **Given** the wizard runs, **When** any tier-2 clients are detected during the connect step, **Then** they appear with the same UX as tier-1 clients.

---

### User Story 3 — Onboarding Telemetry for Data-Driven Iteration (Priority: P2)

To know whether the wizard actually solves the problem we think it solves (low day-2 retention driven by no AI client ever being connected), the daily heartbeat needs two new opt-out, anonymous fields and a small set of wizard funnel events. Without this, we ship the wizard blind and have no way to compare a treated cohort against the 11.8% day-2 baseline. With it, we can validate within a few weeks of release.

The heartbeat schema gains: (a) the count of AI clients currently connected to this mcpproxy, and (b) the set of supported-client identifiers currently connected (a fixed enum like `claude-code`, `cursor`, `vscode` — never user-entered values). The wizard records, in the same heartbeat, whether the wizard was ever shown to this installation, which steps were shown, which were completed via primary action, and which were skipped.

All telemetry is governed by mcpproxy's existing privacy posture (Spec 042 / Spec 044): opt-out, anonymous, off-by-default in CI/test, no upstream-server names, no tool descriptions, no user-entered strings.

**Why this priority**: Same priority as the wizard itself (P2) because shipping the wizard without the means to measure it leaves us in the exact spot we're already in — guessing at causes of the retention cliff. This is paired with US1 and should land together.

**Independent Test**: Can be fully tested by enabling telemetry in a dev install, running the wizard through each of the four state combinations (none / clients-only / servers-only / both), and verifying that (a) the next heartbeat carries the new fields with values matching observed behaviour, (b) opting out via the existing telemetry switch suppresses the new fields exactly as it suppresses everything else, and (c) the privacy section of the website's `/telemetry` page accurately describes the new fields.

**Acceptance Scenarios**:

1. **Given** mcpproxy has 2 AI clients currently connected (e.g. Claude Code and Cursor), **When** the next heartbeat is sent, **Then** the heartbeat includes a connected-client count of 2 and a connected-client identifier set containing the two client IDs.
2. **Given** mcpproxy has zero AI clients currently connected, **When** the next heartbeat is sent, **Then** the heartbeat carries a connected-client count of 0 and an empty identifier set, distinguishable from an opted-out heartbeat.
3. **Given** the wizard has been shown to this installation and the user completed the connect step but skipped the add-server step, **When** the next heartbeat is sent, **Then** the heartbeat carries a wizard-funnel record indicating wizard shown, connect step completed, server step skipped.
4. **Given** the wizard was never shown (because both predicates were satisfied at first run), **When** heartbeats are sent over time, **Then** the wizard-funnel record indicates "not shown — preconditions met".
5. **Given** the user has disabled telemetry (`mcpproxy telemetry disable` or `MCPPROXY_TELEMETRY=false`), **When** any heartbeat would be sent, **Then** no heartbeat is sent, including the new fields, with the same behaviour as before this feature.
6. **Given** the new telemetry is in production for at least 14 days post-release, **When** we query the telemetry D1, **Then** we can compute (a) the share of installs whose first heartbeat shows zero connected clients, (b) the share of installs that complete the connect step via the wizard, (c) the day-2 retention of installs whose first heartbeat shows ≥1 connected client vs zero connected clients, and (d) the day-2 retention of installs that completed the wizard's connect step vs those that skipped it.
7. **Given** the new fields are documented on the public telemetry page, **When** a user reads `/telemetry` on the website, **Then** the page accurately lists the connected-client count, the connected-client identifier enum, and the wizard funnel record alongside the existing field list, with the same explanatory tone.
8. **Given** an end-to-end privacy test asserts heartbeat content, **When** the test runs the wizard end-to-end, **Then** the test verifies that the heartbeat never contains user-entered strings such as upstream server names, custom client paths, free-text input, or any field outside the documented enum.

---

### Edge Cases

- **Wizard is showing the connect step but user has zero clients installed**: Step renders with installation links to supported clients and a clear "Skip for now". No misleading progress.
- **Wizard is showing the add-server step and the user closes the browser mid-flow**: Next time the user opens the Web UI, the wizard re-evaluates state. If the user partially configured a server but never confirmed, no half-server is left behind. If the user did add one, the wizard considers the server step satisfied and skips it.
- **User is in the wizard's add-server step and chooses "Skip for now"**: They land on the dashboard. No server is added. The wizard remembers having been skipped.
- **mcpproxy was previously installed with servers configured but they are all currently disconnected/disabled**: Wizard considers a configured server (regardless of current connection health) as "has servers" and skips that step. Health is the dashboard's job.
- **User's only "connected" client is Claude Desktop (stdio-only, currently unsupported by connect)**: The wizard considers connected = at least one client whose config currently contains a working mcpproxy entry. Detected-but-unsupported clients (like Claude Desktop) are shown with a clear "stdio-only — connect not supported" status and do not satisfy the precondition on their own.
- **Wizard auto-show suppression marker exists but the user re-installs mcpproxy from scratch**: A fresh install produces a fresh marker absence and the wizard re-evaluates from scratch.
- **The user invokes "Run setup wizard" manually after first run**: Wizard re-evaluates state and shows whichever steps are currently missing, even if they finish in zero steps ("You're all set — nothing to do here").
- **Tier-2 client install location is non-default**: Adapter falls back to documented well-known fallback paths; if all fail, it reports "not detected" rather than guessing.
- **Tier-2 client config file is malformed**: Connect refuses, points the user at the exact file path and parse error, never partially writes.
- **Tier-2 client's CLI registration command fails (CLI-only adapters)**: The connect endpoint returns the client CLI's stderr verbatim along with the same error envelope as file-based clients.
- **User upgrades mcpproxy and a new tier-2 adapter is added**: No silent rewrite of existing client configs. The newly supported client simply appears in the Clients section as available to connect; the wizard does not re-trigger.
- **Telemetry is opted out**: New connected-client and wizard-funnel fields are simply not transmitted, exactly like every existing telemetry field.
- **Heartbeat is sent before the user has finished the wizard**: The wizard funnel record reflects current state ("shown, in progress, no step yet completed"). Subsequent heartbeats reflect updated state. We never wait for wizard completion to send a heartbeat.
- **User connects then disconnects a client between heartbeats**: The heartbeat reflects the state at heartbeat time. We do not retain transient transitions. (Day-over-day comparisons in D1 capture the trend.)

## Requirements *(mandatory)*

### Functional Requirements

#### Adaptive Onboarding Wizard

- **FR-001**: System MUST evaluate, on first Web UI load, two state predicates: (a) does mcpproxy currently have at least one AI client connected to it (i.e. mcpproxy is registered in at least one client's config), and (b) does mcpproxy currently have at least one upstream MCP server configured.
- **FR-002**: System MUST show the wizard only when at least one of the two predicates is false. When both are true, no wizard is auto-shown and the user lands directly on the dashboard.
- **FR-003**: Wizard MUST render only the steps corresponding to false predicates: a "Connect an AI client" step when (a) is false, and an "Add an MCP server" step when (b) is false. Steps appear in this order when both apply.
- **FR-004**: Wizard MUST display a step indicator clarifying current and total steps so the user knows how much remains.
- **FR-005**: "Connect an AI client" step MUST detect locally installed AI clients using the same detection used by `GET /api/v1/connect` (extended per US2), present them with status badges, and offer a primary "Connect to all detected" action plus per-client Connect actions, all routed through the existing connect endpoint(s) so file writes, backups, and diff previews behave identically to manual use of the Clients section.
- **FR-006**: "Connect an AI client" step MUST require explicit user confirmation of a diff preview before any client config is modified. The combined-diff preview lists every file that will be touched.
- **FR-007**: "Connect an AI client" step MUST handle the "no detected clients" case gracefully with installation hints for supported clients and a "Skip for now" option.
- **FR-008**: "Add an MCP server" step MUST include a short, plain-language explainer of how quarantine works — every newly added server starts in quarantine until the user explicitly approves it, and the AI client cannot call its tools until that approval. The explainer is visible before the user adds anything.
- **FR-009**: "Add an MCP server" step MUST offer the same add-server controls as the existing add-server flow (registry browse, JSON paste, manual entry) and route through the same endpoints. No separate or duplicate add-server backend.
- **FR-010**: When a user adds a server through the wizard, the server MUST enter quarantine in the same way as any other entry point. The wizard's success summary MUST tell the user the server is quarantined and link to the Servers page where they can review and approve.
- **FR-011**: Each step MUST offer a "Skip for now" action that lands the user on the dashboard without partial writes.
- **FR-012**: System MUST persist a per-installation marker recording wizard completion or skip so the wizard does not auto-appear again. The marker is independent from the predicates in FR-001 — once the user has explicitly engaged with (or dismissed) the wizard, we trust that decision even if state later regresses.
- **FR-013**: System MUST expose a "Run setup wizard" link from the Clients section of the dashboard so the wizard can be re-run on demand. Re-running the wizard re-evaluates state from scratch.
- **FR-014**: When the user finishes any step via its primary action (not Skip), the System MUST land them on a contextual next-step hint matched to what they just did — e.g. "Now ask your AI assistant to use mcpproxy" after the connect step, or "Review and approve your new server" after the add-server step.
- **FR-015**: Wizard auto-show behaviour MUST be identical whether the user reaches the Web UI directly in a browser or via the tray application launching the Web UI.

#### Extended Client Connect Coverage

- **FR-016**: Existing `GET /api/v1/connect` MUST list all supported clients including the new tier-2 entries; each entry follows the existing response shape (`id`, `name`, `config_path`, `exists`, `connected`, `icon`, plus a `supported` flag where applicable).
- **FR-017**: Existing `POST /api/v1/connect/{client}` MUST accept tier-2 client identifiers and write the client's native config shape correctly. The response shape is unchanged from Spec 039.
- **FR-018**: Existing `DELETE /api/v1/connect/{client}` MUST accept tier-2 client identifiers and remove only mcpproxy's entry, preserving everything else.
- **FR-019**: Each new client adapter MUST be defined as data — detection paths, config path, file format, server-collection key, entry shape, naming sanitization rules — so adding further clients later is a configuration change and not new code in the connect engine.
- **FR-020**: Each new client adapter MUST preserve other entries, comments, and as much formatting as the file format allows. JSON adapters preserve key order and other top-level keys; TOML adapters preserve comments and key order; dotted-key formats preserve siblings; CLI-only adapters defer to the client's own CLI.
- **FR-021**: Each new client adapter MUST sanitize the registered server name to satisfy that client's naming constraints.
- **FR-022**: Existing CLI commands `mcpproxy connect <client>`, `mcpproxy connect --list`, `mcpproxy connect --all`, `mcpproxy disconnect <client>` MUST accept the new client identifiers without further changes to the CLI surface.
- **FR-023**: Tier-2 coverage MUST include adapters for at least: Antigravity, Zed, OpenCode, Amazon Q, Kiro, LM Studio, Cline, Roo Code, Junie, Copilot CLI, Copilot JetBrains, Pi, Amp. Each adapter is independently shippable.

#### Onboarding Telemetry

- **FR-024**: Daily heartbeat MUST include a connected-client count: the number of supported AI clients in which mcpproxy is currently registered (i.e. the count of `connected: true` entries returned by `GET /api/v1/connect`). The field is an integer.
- **FR-025**: Daily heartbeat MUST include a connected-client identifier set: the list of supported-client IDs currently connected, drawn exclusively from the fixed enum of supported client identifiers (e.g. `claude-code`, `cursor`, `vscode`, `windsurf`, `codex`, `gemini`, plus tier-2 IDs added by US2). User-entered values, paths, and arbitrary strings MUST NEVER appear in this field.
- **FR-026**: Daily heartbeat MUST include a wizard funnel record summarising the wizard's lifecycle for this installation. The record's possible states are: `not_shown_preconditions_met`, `shown_in_progress`, `connect_completed`, `server_completed`, `connect_skipped`, `server_skipped`, plus a flag for `auto_show_suppressed`. Each state is a fixed enum value, not free text.
- **FR-027**: Telemetry behaviour for the new fields MUST inherit, without exception, mcpproxy's existing telemetry posture: opt-out, anonymous, off-by-default in CI/test, no transmission when telemetry is disabled, and audit-able from `internal/telemetry/`.
- **FR-028**: The new fields MUST be documented on the public website's telemetry page alongside existing fields, with the same level of detail (what each field contains, why it is collected, the enum values). The documentation update is part of this feature.
- **FR-029**: The new fields MUST be supported by an automated privacy test that asserts no user-entered strings, custom client paths, upstream server names, free-text input, or values outside the documented enums ever appear in the heartbeat payload.
- **FR-030**: The telemetry backend (Cloudflare Worker + D1) MUST accept and persist the new fields without breaking older clients. Older heartbeats (without the new fields) continue to be accepted unchanged.
- **FR-031**: The telemetry D1 schema MUST be queryable to derive at least: (a) share of installs with zero connected clients on day 1, (b) share of installs that completed the wizard's connect step, (c) day-2 retention split by connected-clients-on-day-1, and (d) day-2 retention split by wizard-connect-step-completed. These queries are part of the planning artifacts for this feature.

### Key Entities *(include if feature involves data)*

- **Onboarding State**: A small per-installation marker recording whether the first-run wizard has been completed or skipped, plus optionally which steps were taken vs skipped. Used solely to suppress the auto-show after first engagement.
- **Wizard Step Definition**: One step in the wizard — currently "Connect an AI client" and "Add an MCP server". Each has a predicate function (is this step needed?), a UI surface, a primary completion action, a Skip action, and a contextual completion hint.
- **Client Adapter (extension of Spec 039)**: One supported AI client, captured as data: identifier, display name, icon, detection paths and binaries, config file path(s), file format, top-level key, entry shape, naming sanitization, registration mechanism (file edit vs CLI invocation), supported flag for stdio-only-and-thus-skipped cases.
- **Heartbeat Onboarding Fields**: Three additions to the daily heartbeat payload — connected-client count (integer), connected-client identifier set (list of fixed enum values), wizard funnel record (fixed enum state). All three are anonymous, opt-out, and inherit Spec 042 / Spec 044 privacy posture. Persisted in the existing telemetry D1 alongside existing heartbeat fields.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: A first-run user with no clients and no servers reaches "one client connected and one server configured" in under 2 minutes end-to-end through the wizard, on a machine with at least one supported client installed and a basic upstream server available.
- **SC-002**: A first-run user with no clients but existing servers reaches "one client connected" in under 60 seconds through the wizard.
- **SC-003**: A first-run user with existing clients but no servers reaches "one server configured (in quarantine)" in under 90 seconds through the wizard, including reading the quarantine explainer.
- **SC-004**: 80% of first-run users with at least one supported client installed complete the connect step (not skip it), measured against a representative cohort.
- **SC-005**: 100% of users who add a server through the wizard see the quarantine explainer before the server is added, and 100% of resulting servers are placed in quarantine, verified end-to-end.
- **SC-006**: Tier-2 client coverage extends `GET /api/v1/connect` from 7 entries to at least 20 entries; round-trip tests (connect then disconnect) leave each client's config functionally identical to its pre-connect state for every supported tier-2 client.
- **SC-007**: 95% of installations of any tier-2 client in its default location are detected correctly by `GET /api/v1/connect` on the four target operating systems where that client runs.
- **SC-008**: For every supported client (tier-1 and tier-2), connect-then-disconnect leaves the config file's other entries byte-equivalent to the pre-connect state, verified by automated round-trip tests.
- **SC-009**: The wizard does not auto-appear after first completion or skip in 100% of test runs across browser-restart, mcpproxy-restart, and upgrade scenarios.
- **SC-010**: The wizard auto-shows only the missing steps in 100% of test runs across the four state combinations (none / clients-only / servers-only / both).
- **SC-011**: 14 days post-release, the telemetry D1 returns non-null connected-client counts and wizard funnel records for ≥90% of incoming heartbeats from versions that include this feature, verifying end-to-end pipeline health.
- **SC-012**: 30 days post-release, day-2 retention of installs whose first heartbeat reports ≥1 connected client is materially higher than the 11.8% pre-feature baseline. The exact target is set at plan time once we have at least 7 days of post-release data to estimate variance, but the feature is considered to have moved the needle only if the difference is statistically significant.
- **SC-013**: 30 days post-release, day-2 retention of installs that completed the wizard's connect step is measurably higher than installs that skipped it on the same version, with at least 50 installs in each arm to support comparison.

## Assumptions

- Spec 039 (`/api/v1/connect/{client}` endpoint, `mcpproxy connect|disconnect` CLI, hub dashboard) is shipped and stable. This feature extends 039's adapter table and adds the wizard surface; it does not modify 039's HTTP/CLI contracts.
- Spec 032 (tool-level quarantine with hash-based change detection) is shipped. Quarantine is the authoritative trust boundary; the wizard's add-server step references it but does not change it.
- The existing add-server flow (registry browse, JSON paste, manual entry, tool-add) continues to work exactly as today. The wizard's add-server step embeds or links to the same UI, not a parallel one.
- Tier-1 clients (Claude Code, Claude Desktop, Cursor, Windsurf, VS Code, Codex, Gemini) are already connected via Spec 039 and do not need rework here.
- Tier-2 client list is best-effort coverage; not every adapter has to land at once. Each is independently shippable behind the same connect endpoint.
- "Connected client" means at least one client config file currently contains a working mcpproxy entry pointing at this mcpproxy's listen URL. Detection uses the same logic as `GET /api/v1/connect` returning `connected: true`.
- "Configured server" means at least one upstream server present in mcpproxy's configuration, regardless of current connection health.
- The wizard is Web-UI only in this scope. CLI users start mcpproxy and use `mcpproxy connect --all` or the existing add-server tools directly; the wizard is for the Web-UI / tray-launching path.
- "First-run" is detected by the absence of the wizard-engagement marker, not by a config-version stamp. Re-installing mcpproxy after manually connecting a client and adding servers does not trigger the wizard if both predicates are now true; if state regresses (e.g. user disconnects all clients), the wizard does not re-trigger automatically because the user has already engaged with it once.
- The 11.8% day-2 retention baseline used for SC-012/SC-013 was measured 2026-03-23 → 2026-04-28 across 1,491 unique installs from the production telemetry D1. The full baseline funnel is in Background. Post-release comparison must be apples-to-apples — same telemetry sampling, same heartbeat dedup rule, same install cohort definition.
- Telemetry remains opt-out and anonymous. The new fields fit the existing privacy framework (Spec 042 / Spec 044): no upstream-server names, no tool descriptions, no user-entered strings, only fixed enum identifiers and counts. Documentation on the public `/telemetry` page must be updated as part of this feature.

## Commit Message Conventions *(mandatory)*

When committing changes for this feature, follow these guidelines:

### Issue References
- ✅ **Use**: `Related #[issue-number]` — Links the commit to the issue without auto-closing
- ❌ **Do NOT use**: `Fixes #[issue-number]`, `Closes #[issue-number]`, `Resolves #[issue-number]` — These auto-close issues on merge

**Rationale**: Issues should only be closed manually after verification and testing in production, not automatically on merge.

### Co-Authorship
- ❌ **Do NOT include**: `Co-Authored-By: Claude <noreply@anthropic.com>`
- ❌ **Do NOT include**: "🤖 Generated with [Claude Code](https://claude.com/claude-code)"

**Rationale**: Commit authorship should reflect the human contributors, not the AI tools used.

### Example Commit Message
```
feat(046): adaptive onboarding wizard with state-based steps

Related #[issue-number]

Adds the adaptive first-run wizard. Detects whether the user has any
clients connected and any servers configured, and renders only the
steps that are missing, in order. Reuses Spec 039 for connect and
the existing add-server flow for servers. No new HTTP contract.

## Changes
- Web UI wizard component with predicate-driven step list
- Per-installation wizard-engagement marker
- Quarantine explainer in the add-server step
- Dashboard "Run setup wizard" entry point

## Testing
- Wizard auto-shows correct steps in all four state combinations
- Skip on any step lands on dashboard with no partial commits
- Wizard suppressed after first engagement across restart scenarios
```

---

# v2 — Wizard Surface Redesign (2026-04-30)

## Why v2

After PR #433 (the v1 wizard) shipped, telemetry from `gemini-home` (SynapBus #27345, 2026-04-30) confirmed that the activation funnel cliff is **Step 3 → Step 4 (78.2% → 11.7%)**: users add upstream servers but fail to wire mcpproxy into their AI IDE. The v1 wizard does help by surfacing the connect step, but it has three weaknesses against this signal:

1. **Linear, one-shot flow.** The user can't easily revisit "did my client actually connect?" without manually digging into Settings.
2. **No proof of round-trip.** Completing the connect step writes a config file, but the user has no in-product confirmation that their AI agent actually talked to mcpproxy. The 11.7% cliff is the gap between "config written" and "first successful MCP request".
3. **Hidden re-entry.** The "Run setup wizard" link buried in the Dashboard sidebar is unreachable from other pages, so users who get distracted mid-flow never come back.

v2 reframes the wizard as a **persistent, idempotent setup surface** that lives in the left sidebar and stays visible until the user has clients connected, servers configured, *and* a verified first round-trip from at least one client.

## v2 Goals (in priority order)

1. **Move the cliff signal in-product.** Surface "did your AI agent actually call mcpproxy?" as the third tab, gated by passive detection of the existing Spec 044 `FirstMCPClientEver` flag. No active ping; no fragile prompt injection.
2. **Always-available re-entry.** Replace the dashboard sidebar link with a top-pinned "Setup" entry that lives above Dashboard in the left nav, with an animated badge counting incomplete tabs. One source of truth: `incomplete_tab_count`.
3. **Idempotent navigation.** All three tabs are clickable at any time. Each tab is a stateful summary, not a one-shot ritual: revisiting Clients shows currently-connected clients with the option to add more; revisiting Servers shows current servers and the add form. The wizard becomes both first-run flow *and* setup overview.
4. **Reduce friction at Step 1.** Expose the safe defaults (localhost bind, auto-generated API key) inline as a `Show security settings ▾` expander on the Clients tab — no dedicated config screen ahead of the bottleneck step.
5. **Keep funnel telemetry honest.** Add the verify-step status to the existing US3 telemetry (PR #434). The five-field schema becomes six.

## v2 Non-Goals

- We do **not** add a configuration screen as a separate step. Bind-LAN and `require_mcp_auth` stay accessible from Settings; the wizard exposes them as an inline expander only.
- We do **not** ship active connection probing (e.g. spawning an IDE process and injecting a prompt). The Verify tab waits for a real, user-driven round-trip.
- We do **not** change the `/api/v1/connect` adapter table or the existing add-server flow. v2 is wizard surface only.

## v2 User Story 1 — Three-Tab Wizard with Verify

A first-run user opens mcpproxy. The wizard auto-pops as a 3-tab modal: **Clients | Servers | Verify**. The user can move freely between tabs with no gating: tabs are persistent state views, not steps to march through.

- **Clients tab** lists supported AI clients (detected first, then the always-pinned trio Claude Code / Codex / Gemini CLI, then a `Show N more clients ▾` collapsed list of the rest). Each row has a Connect button. A `Show security settings ▾` expander at the bottom exposes "Bind to LAN" and "Require API key on /mcp" toggles with safe defaults pre-selected.
- **Servers tab** shows currently configured servers and an "Add a server" button. Two compact toggles above the button: **Docker isolation** (per-server, default on for stdio when Docker is available; gracefully off otherwise) and **Quarantine new tools** (global `quarantine_enabled`, default on). Below the toggles, a one-paragraph quarantine explainer.
- **Verify tab** shows a passive status: "Open your AI agent and ask it to call `retrieve_tools` — we'll detect it live" until at least one supported client has successfully completed its first MCP `initialize` round-trip. Once detected, the tab flips to a green check with the recognized client name(s) and a "Tools indexed: N" counter sourced from existing index stats.

The wizard's badge in the sidebar is the count of tabs whose state is incomplete:
- Clients tab is "incomplete" iff `connected_client_count == 0`
- Servers tab is "incomplete" iff `configured_server_count == 0`
- Verify tab is "incomplete" iff `FirstMCPClientEver == false`

Auto-popup once on first run when `incomplete_tab_count > 0`. Sidebar badge stays live forever — once the user has engaged at least once, we never auto-popup again, but the badge remains so they know they have unfinished setup.

**Acceptance scenarios:**

1. Fresh install, no clients, no servers, no MCP requests received → wizard auto-pops; badge shows `3`.
2. User clicks Servers tab while on Clients tab → tab switches, no state change to either side.
3. User clicks Connect on Claude Code → adapter writes config, badge drops to `2`, Clients tab shows "Connected" badge for that client.
4. User opens Claude Code and asks it to call `retrieve_tools` (without leaving mcpproxy in another window) → Verify tab flips to green via SSE within 2s of the MCP `initialize` hook firing; badge drops accordingly.
5. User dismisses the wizard with badge at `1` → wizard closes; sidebar Setup entry shows badge `1`; clicking it re-opens the wizard at the first incomplete tab.
6. User connects all three (clients, servers, verified) → badge becomes `0`, sidebar entry collapses to a quiet `✓ Setup` styling, wizard does not auto-pop.
7. User completes everything, then disconnects all clients (regresses to 1 incomplete) → badge re-appears with count `1`. Auto-popup does **not** fire because `Engaged == true`. Sidebar entry pulses subtly to draw attention.

## v2 Functional Requirements (additive — does not remove FR-001..FR-031)

- **FR-V01**: Wizard MUST render exactly three tabs: Clients, Servers, Verify. Tabs are clickable bidirectionally with no gating.
- **FR-V02**: Wizard MUST auto-popup once on first Web UI load when `Engaged == false` AND `incomplete_tab_count > 0`. After engagement, only the sidebar Setup entry surfaces unfinished state.
- **FR-V03**: Sidebar MUST contain a top-pinned "Setup" entry above Dashboard, always visible to personal-edition users (Server edition is unaffected — see Out of Scope). Server edition layout (My Workspace / Administration sections) does not include the Setup entry. The entry has two visual states: active (badge with count > 0, pulse animation) and complete (no badge, muted color, optional `✓` glyph).
- **FR-V04**: Setup entry's badge MUST equal `incomplete_tab_count`, where:
  - +1 if `connected_client_count == 0`
  - +1 if `configured_server_count == 0`
  - +1 if `first_mcp_client_ever == false`
- **FR-V05**: Clients tab MUST sort the client list as: detected (any order acceptable, but by adapter table order is fine) → always-pinned trio (Claude Code, Codex, Gemini CLI) when not already in the detected list → collapsed `Show N more clients ▾` for the rest. The trio's order is fixed.
- **FR-V06**: Clients tab MUST include a `Show security settings ▾` expander that exposes:
  - `Bind interface` (read-only display of `listen` config — the wizard does not write this, only surfaces it; full edit lives in Settings → Configuration)
  - `Require API key on /mcp` toggle (binds to `require_mcp_auth` in config)
  - Both default to safe values; the toggle persists via existing `/api/v1/config` endpoints.
- **FR-V07**: Servers tab MUST surface two toggles above the existing add-server flow: `Docker isolation default` (binds to a config-level default that newly added stdio servers inherit) and `Quarantine new tools` (binds to `quarantine_enabled`).
- **FR-V08**: Verify tab MUST be passive: it polls the onboarding endpoint (or subscribes via SSE) and flips to "green" iff `first_mcp_client_ever == true`. The tab MUST NOT include any "test connection" button; it shows live recognized client names from `mcp_clients_seen_ever` and a tools-indexed count.
- **FR-V09**: `/api/v1/onboarding/state` response MUST be extended with three new top-level fields: `first_mcp_client_ever` (bool), `mcp_clients_seen_ever` ([]string, fixed enum from adapter table), and `incomplete_tab_count` (int, derived). The existing `should_show_wizard` flag's semantics change to: `!Engaged && incomplete_tab_count > 0`.
- **FR-V10**: Wizard modal MUST size to `min(960px, 90vw) × min(640px, 85vh)`, single column, with tabs across the top.
- **FR-V11**: Dashboard "Run setup wizard" link from v1 MUST be removed; the sidebar Setup entry replaces it. Existing references to that button in tests are updated.

## v2 Telemetry Delta (additive to PR #434)

The five v3 fields shipped in PR #434 stay. v2 adds **one more** to the heartbeat:

- `wizard_verify_step_state` (string, one of `""`, `"pending"`, `"verified"`):
  - `""` when wizard never shown
  - `"pending"` when wizard shown but `first_mcp_client_ever == false`
  - `"verified"` when `first_mcp_client_ever == true` (the cliff has been crossed)

This bumps `schema_version` 4 → 5 in `internal/telemetry/telemetry.go`. v3/v4 consumers ignore the new field. Privacy posture unchanged: tri-state enum, never user-entered.

> v2 telemetry can ship in a separate PR after the wizard surface lands. The wizard surface alone is mergeable independently of the heartbeat field — the field only determines whether we can *measure* the cliff close after release.

## v2 Out of Scope

- **Server edition** (Spec 024 multi-user): the Setup entry is personal-edition only. Server edition's left nav has its own structure (My Workspace / Administration); we do not add Setup there in this scope. Server-edition admins can drive setup via the existing admin tools.
- **Tier-2 client adapters** (US2 in v1): independently shippable as before. v2 changes nothing here.
- **CLI surface**: no `mcpproxy wizard` command. The wizard is Web-UI / tray only.

## v2 Implementation Notes

- Backend reuse: `internal/telemetry/activation.go` already exposes `FirstMCPClientEver` and `MCPClientsSeenEver` via the `ActivationStore` interface. The onboarding endpoint just needs to read `ActivationState` (via `runtime.TelemetryService().ActivationStore().Load(db)`) and surface the two fields. No new BBolt bucket, no new hook into MCP — the `AfterInitialize` hook in `internal/server/mcp.go:155` already calls `RecordMCPClientForActivation`.
- Frontend reuse: `OnboardingWizard.vue` is rewritten in place from a 1-2 step linear flow to a 3-tab idempotent surface. The Pinia store gains `verifiedFirstClient`, `mcpClientsSeenEver`, and `incompleteTabCount` computeds.
- Sidebar: `SidebarNav.vue` gains a top-pinned `<router-link>` to a synthetic route `/?wizard=open` that opens the wizard via the store. Badge binds to `onboarding.incompleteTabCount`.
- The existing v1 `should_show_wizard` derivation is replaced (semantically widened) but the field name is preserved so the frontend doesn't break.
- Live updates on the Verify tab: piggyback on the existing `/events` SSE stream — emit a `runtime.activation_changed` event when `MarkFirstMCPClient` flips. Frontend listens, refetches `/api/v1/onboarding/state`. Falls back to a 5s poll while the wizard is open.

## v2 Risks & Mitigations

- **Risk**: The MCP `initialize` hook fires for *any* MCP client, including curl/test scripts, so a verified state could be a false positive (user ran `mcpinspect` once but never wired up their real IDE). **Mitigation**: `MCPClientsSeenEver` records the client name; the Verify tab shows the recognized names so the user can see whether it's a real IDE or a test client. We don't gate on identity, but we surface it.
- **Risk**: Sidebar badge churn is annoying if it bounces between values during a quick connect. **Mitigation**: store fetches are debounced 500ms; SSE is the primary update path; the badge only re-renders when the count actually changes.
- **Risk**: Server-edition users see no Setup entry but expect parity. **Mitigation**: explicit non-goal in v2; we can revisit per multi-user UX in a follow-up.

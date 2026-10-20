# Feature Specification: Day-Two Activation (Experiment E3)

**Feature Branch**: `119-day-two-activation` (PR branch `claude/spec-day-two-activation-1fad2a`)
**Created**: 2026-10-20
**Status**: Draft (design only; implementation not started)
**Input**: Growth experiment E3 from the Q4 2026 promotion strategy (maintainer-approved). Three product changes: (1) first run always ends with a working tool indexed, (2) the connect wizard offers a second client, (3) a weekly "what MCPProxy did" summary gives users a reason to come back. A fourth part adds the telemetry needed to judge the experiment.

**Related**: Spec 075 (connect payload, `access_state`), Spec 108 (profiles, blocked-by-profile decisions), Spec 109 (Home needs-attention list, catalog-first Add, Clients hub), Spec 116 (`Runtime.Commit`), Spec 117 (review-bound approvals, `expected_hashes`), Spec 032 (tool-level quarantine), MCP-1072 (catalog provenance is informational only).

All code references are to `origin/main` at `9dfdb2f19`.

## Context and Motivation

First-party telemetry (non-CI installs, 2026-03-23 to 2026-10-20) shows that retention, not setup, is the binding constraint:

| Fact | Value | Source |
|---|---|---|
| New installs seen on one day only | 42% (machine key), 51% (IP key) | `telemetry_data_retention.csv` |
| Day-7 return | 23.9% (machine key), 14.9% (IP key), flat across Jun-Sep cohorts | same |
| Aug-Oct cohort: configured a server / connected a recognised client | 94.9% / 78.9% | `telemetry_data_funnel.csv` |
| Aug-Oct cohort: ever had a tool indexed (`tool_count > 0`) | 71.5% | same |
| One-and-done rate, tools indexed vs never indexed | 35.6% vs 65.9% | same |
| Machines running 2+ recognised clients | about half; Claude Code 44%, Codex 30%, OpenCode 26%, Cursor 20% of 30-day actives | `telemetry_data_clients_30d.csv` |

What the product does today:

| Area | Today on main | Code |
|---|---|---|
| Web onboarding wizard | Modal with Clients, Servers and Verify tabs. The Clients tab is complete once **any** client is connected. The wizard never auto-opens again after the first dismissal. | `frontend/src/components/OnboardingWizard.vue` (tabs ~:967, `pickInitialTab` ~:1146), `internal/httpapi/onboarding.go:276,342` |
| Starter server | None. A fresh install with zero servers sees a "Get started" card on Home. | `frontend/src/views/Home.vue:10-40` |
| Catalog | Curated reference servers are compiled in: filesystem, memory, everything, sequentialthinking, fetch, git, time. They need no credentials. Runtime is implicit in the install command (`uvx` or `npx -y`). | `internal/registries/reference.go:26` |
| Quarantine on add | Every catalog add is quarantined unless quarantine is globally off. Trust mode defaults to `manual`. Provenance never bypasses quarantine. A new quarantined server gets a 1-hour inspection exemption, so it connects and its tool definitions are captured for review. | `internal/config/config.go:2060,2173`, `internal/server/add_from_registry.go:~120-131`, `internal/server/server.go:2059`, `internal/server/review_capture.go:28` |
| Docker isolation | Off by default. | `internal/config/config.go:1644-1646` |
| "Tools indexed" | `tool_count` = search-index document count, sampled when the heartbeat is built. Quarantined or held tools are not in the index. | `internal/runtime/runtime.go:~4026`, `internal/telemetry/telemetry.go:189` |
| Activity summary | `GET /api/v1/activity/summary?period=1h|24h|7d|30d` and `mcpproxy activity summary --period` exist. They return totals, status counts, top servers and tools, and per-server counts. They have no held-for-review, blocked-by-reason, by-client or sensitive-data breakdown. | `internal/httpapi/activity.go:~886`, `internal/contracts/activity.go:132`, `cmd/mcpproxy/activity_cmd.go` |
| Tray | The AppKit menu has a glance block with a 24h activity line and an "Open Activity…" row. There is no weekly view. The native app has no onboarding wizard and never calls `/onboarding/*`. | `native/macos/MCPProxy/MCPProxy/MCPProxyApp.swift:~1041-1450`, `Menu/Glance/GlanceSection.swift` |
| Telemetry | Schema 14. It reports wizard fields (`wizard_engaged`, `wizard_connect_step`, `wizard_server_step`, `wizard_shown`), `connected_client_count` / `connected_client_ids` and `activation.*` first-ever flags. It has nothing for a starter offer, a second-client step, summary views, or a sticky "a tool was ever indexed" flag. | `internal/telemetry/telemetry.go:163-190`, `internal/telemetry/activation.go:42` |

The experiment's success bar comes from the strategy report:
- at least 82% of post-release new installs reach "tools indexed";
- day-7 return on the IP key reaches at least 18%.

The kill rule: no movement beyond noise after two releases and four weeks of cohort data.

## Clarifications

### Session 2026-10-20

Answered by the spec author under the zero-interruption policy; each answer is the recommended option, recorded as an assumption.

- Q: Does "held for review" count events or tools? → A: Distinct `(server, tool)` pairs that actually entered `pending` or `changed` (from a non-held state, with quarantine in effect) during the window; a tool that flips twice counts once. (Revised after spec review r2 #3.)
- Q: Which records count as "calls brokered"? → A: Upstream `tool_call` records that were actually dispatched to an upstream (success, or an error after dispatch). A call refused before dispatch counts under "blocked" only, never twice; `rejected` (limiter) calls count under blocked/`other`. (Revised after spec review r1 #8.)
- Q: What if a server named like the starter (`time`) already exists with a different command? → A: That candidate is skipped with reason `name_taken` and the next candidate is tried; the starter never renames or overwrites a user server.
- Q: When the wizard opens with a client already connected (connected before or outside the wizard), is the second-client section shown? → A: Yes, immediately, listing the remaining detected clients.
- Q: What counts as a summary "view" for telemetry? → A: A summary request that names its surface (`view=web|tray|cli`): Web when the Home card renders in a visible tab, tray when the menu opens with the line shown, CLI per `activity summary` run. The core debounces to at most one view per surface per hour.

## Scope Boundary

| In scope | Out of scope (unchanged, or a follow-up) |
|---|---|
| A **starter offer** in the Web wizard's Servers step: one safe catalog server, pre-selected, added through the normal catalog door with normal quarantine. When a review is needed, the user approves it in place. | Any new trust shortcut. A starter is never auto-approved and never quarantine-exempt by virtue of being a starter. |
| The same starter offer, not pre-selected, on the Web Home "Get started" card and the native macOS Home empty state, whenever the install has zero indexed tools. | A starter on the CLI. `mcpproxy catalog add reference/time` already exists. |
| A **"Connect another client"** step after the first client connects, in the Web wizard and the native Connect Client sheet. It lists detected, supported, unconnected clients from the Spec 075 registry. | Auto-writing any client config. Every write stays an explicit, previewed action. |
| A **weekly "what MCPProxy did" summary**: a Web Home card, a tray glance line, and the CLI `mcpproxy activity summary --since 7d`. It is computed locally from the activity log and the current review queue. | Push notifications or e-mail digests (follow-up O2). |
| Additive **telemetry**: schema 15, with bucketed counters and closed enums that show starter offer and outcome, second-client outcome, clients connected on day 0, a sticky first-tool-indexed flag, and summary views. | New identifiers, server/tool/client names beyond the existing closed `connected_client_ids` list, and receiver or dashboard changes. Those live in the telemetry repo; quickstart.md gives the queries. |
| Personal edition. | Server edition. Its onboarding, starter and summary surfaces stay as they are. The REST additions are available in both editions under the existing activity auth rules. |

## Definitions

- **Fresh install**: the persisted onboarding state was never engaged (`OnboardingState.engaged == false`). This covers new installs and upgraded installs that never dismissed the wizard.
- **Install anchor**: a persisted timestamp written once, on the first core start that finds no onboarding record (`OnboardingState.install_first_run_at`). It never rotates; the telemetry `anonymous_id` rotates yearly. Installs upgraded from a build without the field have no anchor. Day-0 metrics are then reported as unavailable (`""`), never guessed.
- **Day 0**: the first 24 hours after the install anchor.
- **Indexed tool**: a tool document in the shared search index. A held (pending/changed) or quarantined tool is not indexed. The sticky flag of FR-023 is stamped when an index commit for an upstream server leaves at least one document. It is never stamped from a cached or fallback tool count.
- **Held tool**: a tool approval record in `pending` or `changed` state, or any captured tool of a quarantined server.
- **Starter candidate**: an entry in a short, fixed, compiled-in allowlist. Each entry names a catalog reference server that was audited against the starter criteria (FR-002), together with the expected tool set recorded at audit time. The list is ordered. The **selected starter** is the first candidate that passes all checks:
  - its launcher (`uvx` or `npx`) resolves on the PATH the stdio spawner uses;
  - its name is not taken;
  - the **effective** `reference` registry is still the compiled `builtin/reference` source, and the catalog entry resolves to exactly the candidate's audited command.

  A config entry can override the `reference` registry by ID (`internal/registries/registry_data.go:9-14`). The check fails closed in that case.
- **Detected client**: a Spec 075 client registry entry that is `supported`, whose config location exists on this machine (`exists == true` in `GET /api/v1/connect`), and that is not currently in `connected_client_ids`.
- **Observed client**: a registry client ID that appeared in `connected_client_ids` at any observation. Observations happen on every onboarding read, every connect or disconnect action, and every heartbeat build, starting at the first start of a build with this feature. On an upgraded install, that first start records every currently connected client. The first-observed time per ID is persisted once and never overwritten. Observation does not depend on the install anchor, which is used only to bucket day-0 metrics. This covers clients connected outside the wizard and by hand-edited configs.
- **Brokered call**: an upstream `tool_call` that MCPProxy actually dispatched to an upstream server. It succeeded, or it failed after dispatch. A call refused before dispatch, for example by quarantine, a profile, a stale connection generation or the limiter, is never brokered.
- **Summary window**: the last 7 × 24 hours ending at request time. If the install is younger, the window starts at the install anchor.
- **Coverage watermark**: a persisted "activity log complete since T" timestamp. Every pruning pass (age, record count, byte size) advances it in the same transaction as the delete.

## User Scenarios & Testing *(mandatory)*

### User Story 1: First run ends with a working tool (Priority: P1)

A developer installs MCPProxy, opens the Web UI, connects Claude Code in the wizard and reaches the Servers step without having added a server. The step shows a pre-selected starter card:

> Start with **time**: tells your agent the current time in any timezone. No account or API key needed. MCPProxy will download and run `uvx mcp-server-time` on this machine to list its tools; nothing can call them until you approve.

They click "Add and inspect". MCPProxy adds the server through the normal catalog door, so it arrives quarantined like any catalog add, connects it for inspection and shows the captured tools with their risk tiers. They click "Approve and enable". The Verify step then says "1 server, 2 tools ready" and suggests a first prompt for the connected client ("Ask Claude Code: what time is it in Tokyo?").

**Why this priority**: tools indexed is the funnel step most strongly linked to one-and-done (36% vs 66%). It is the experiment's primary metric.

**Independent Test**: on a scratch instance with an empty config and `uv` on PATH, complete the wizard with defaults. `GET /api/v1/status` reports `tool_count >= 1`. The activity log has the user's approval of `time`. No approval record has `approved_by` of `auto-baseline`.

**Acceptance Scenarios**:

1. **Given** a fresh install with zero indexed tools and `uvx` resolvable, **When** the user reaches the Servers step, **Then** the starter card for `time` is shown and pre-selected. It shows the exact command it will run, the isolation mode (host process, or Docker when isolation applies), "no credentials needed", and the expected tools.
2. **Given** the card is selected, **When** the user clicks "Add and inspect", **Then** the server is added through the catalog door, quarantined under the default config. The step shows "Connecting…" until definitions are captured (at most 90 s), then lists the **captured** tools with tiers and an "Approve and enable" button.
3. **Given** the captured review, **When** the user clicks "Approve and enable", **Then** the approval is sent bound to the shown definitions and the server incarnation (Spec 117 `expected_hashes` and `expected_incarnation`). The server leaves quarantine, its tools are indexed, and the Verify step reports the indexed tool count.
4. **Given** the captured tool set differs from the candidate's audited tool set (a tool added, removed or renamed by a newer package release), **Then** the step drops the starter copy ("tells your agent the time…") and shows a notice: "These tools differ from the version we reviewed." This is the normal review, with no starter framing. The approval still requires the same click.
5. **Given** the user unchecks the card or clicks "Skip", **When** they continue, **Then** nothing is added or run. The decline is remembered, and the wizard never pre-selects the starter again.
6. **Given** the user closes the wizard while the card is selected, before clicking "Add and inspect", **Then** nothing is added or run.
7. **Given** a definition changes between capture and approval, **When** the user clicks "Approve and enable", **Then** the 409 `review_out_of_date` response reloads the tool list and nothing is approved. This is Spec 117 behaviour, surfaced in place.
8. **Given** `quarantine_enabled: false`, **When** the starter is added, **Then** its tools are indexed without an approval step, exactly as for any other catalog add, and the step skips straight to "ready".
9. **Given** no candidate's launcher resolves (neither `uvx` nor `npx`), **Then** the card is replaced with "To try a starter server, install uv or Node.js", with links. The eligibility result is recorded as `launcher_missing`.

---

### User Story 2: Users who skipped the wizard still get one path to a working tool (Priority: P2)

A user dismissed the wizard, or configured a server that failed, and now has zero indexed tools. Home's "Get started" card, and a new empty-state card on the native macOS Home, offer the same starter, not pre-selected, with the same add → inspect → approve flow.

**Why this priority**: the wizard's connect step wrote a client config for only 55% of the Aug-Oct cohort (`connected_client_ids` non-empty), so many installs leave the wizard early or never see it. macOS tray users may also never open the Web UI.

**Independent Test**: with an engaged onboarding state and zero indexed tools, Home shows the starter offer. Accepting it from Home reaches the indexed state through the same review.

**Acceptance Scenarios**:

1. **Given** zero indexed tools, no held tools awaiting review and the starter not yet configured, **When** Home loads (Web or native), **Then** the starter offer is shown with an explicit "Add and inspect time" button, the command and isolation mode. Nothing is pre-selected.
2. **Given** the install has a server whose tools are held for review, **Then** Home shows the existing review call-to-action ("Review N tools from <server> to start using them") instead of the starter offer.
3. **Given** the user previously declined the starter, **Then** Home shows the offer as a compact link rather than a card. A decline only stops pre-selection.
4. **Given** the native app, **When** the user accepts, **Then** the review step opens in the native review screen, and the approval is bound in the same way.

---

### User Story 3: Connect a second client right after the first (Priority: P2)

Once a client is connected, the wizard's Clients step shows "Connect another client" and lists the other agents it detected on this machine (Codex, OpenCode, Cursor…). Each row uses the existing preview-then-write flow, with the diff and backup notice shown before the Connect button. "Done" moves on.

**Why this priority**: half of active machines run 2+ clients. A shared tool hub across agents is the product's core pitch, and a second connected client adds a second daily touchpoint.

**Independent Test**: on a machine with Claude Code and Codex configs present, connect Claude Code in the wizard. Codex appears under "Connect another client". Connecting it adds `codex` to `connected_client_ids`, and the onboarding state records `second_client_connected = true`.

**Acceptance Scenarios**:

1. **Given** at least one client connected and at least one other detected client, **Then** the Clients step shows "Connect another client" with the detected clients in the registry's fixed order. Each has a Connect action that opens the existing preview.
2. **Given** no other detected client, **Then** the section shows one line, "Using another agent? See all clients", linking to the Clients hub, and `second_client_none_detected` is recorded.
3. **Given** the user clicks "Done" or moves to another tab without connecting a second client, **Then** `second_client_skipped` is recorded.
4. **Given** a client the access check reports as `denied` (Spec 075), **Then** its row shows the remediation instead of a Connect action.
5. **Given** the native Connect Client sheet, **When** a connect succeeds, **Then** the success state offers the same list ("Connect another client"), built from the same core payload.
6. **Given** the user skipped, or no other client was detected, **When** a second distinct client is later observed (from any surface, or by hand-editing a config), **Then** `second_client_connected` becomes true. Connected dominates the other facts, and nothing is ever cleared.
7. **Given** two clients were already connected before the wizard opened, **Then** `second_client_connected` is true. `second_client_offered` reflects only whether the section was shown.

---

### User Story 4: A weekly reason to open MCPProxy (Priority: P3)

Once a week, a user glances at the tray, or opens the Web UI, and sees what MCPProxy did for them:
- "This week: 1,284 calls brokered (Claude Code 812, Codex 431, other 41)";
- "3 tool changes held for review";
- "5 calls blocked (4 quarantine, 1 profile)";
- "2 calls with secrets detected".

Each figure links to the filtered Activity or Review view.

**Why this priority**: it is the habit part of the experiment and the least certain lever. It is independent of US1-US3.

**Independent Test**: seed an activity log with known records over 10 days. The Web card, the tray line, `mcpproxy activity summary --since 7d -o json` and `GET /api/v1/activity/summary?period=7d` report identical figures for the 7-day window.

**Acceptance Scenarios**:

1. **Given** brokered calls from two clients in the window, **Then** "calls brokered" equals the number of brokered calls (Definitions), split by client display name (top 3 plus "other"). Records with no client are counted as "unknown client".
2. **Given** a call refused before dispatch that left both a `tool_call` error record and a `policy_decision` blocked record for the same request, **Then** it counts once, under "blocked", and not under "calls brokered".
3. **Given** two tools that went pending and one approved tool that went changed in the window, **Then** "held for review" reports 3 distinct `(server, tool)` pairs. A tool held before the window and changed again inside it is not counted. A tool that flips twice counts once in the total. The per-kind breakdown (new / changed) may then sum to more than the total, and the response says so. The number currently awaiting review is shown next to it, with a link to the Review queue.
4. **Given** blocked or rejected calls, **Then** "blocked" reports the total and a split by reason class: `quarantine`, `profile` or `other`. Each refused attempt counts once.
5. **Given** brokered calls carrying sensitive-data detections, **Then** "sensitive data" reports how many calls had at least one detection, with a link to Activity filtered `sensitive_data=true`.
6. **Given** an install 3 days old, **Then** the summary is labelled "since <date> (3 days)" rather than "this week".
7. **Given** all counts are zero, **Then** the card says "Quiet week" with one next step (connect a client, or try the suggested first prompt), never an empty grid.
8. **Given** the coverage watermark is later than the window start, because pruning removed records inside the window, **Then** the summary is marked `partial` and states the date the log is complete from.
9. **Given** an agent-token caller scoped to servers A and B, and held tools on server C, **Then** C's holds do not affect any figure in that caller's response.

---

### User Story 5: The maintainer can judge E3 from telemetry (Priority: P1, ships with US1)

The maintainer queries the telemetry store for post-release cohorts. Without any new identifier, for each install they can tell:
- whether the starter was eligible, offered, added and made live, or failed and why;
- whether a second client was offered, skipped or connected;
- how many distinct clients were observed on day 0, including for installs that stopped before day 0 ended;
- whether a tool was ever indexed, and how soon;
- whether the summary was viewed.

**Why this priority**: without it the experiment cannot be judged or killed.

**Independent Test**: schema-15 payload tests assert the new fields' presence, enum closure, bucket edges and stickiness. The PII scan rejects unknown keys and values in every new object.

**Acceptance Scenarios**:

1. **Given** a starter accepted and approved, **Then** later heartbeats carry `day_two.starter_live = true` and `day_two.starter_kind = "time"`, and `activation.first_tool_indexed_ever = true` from then on, even if the tool count later drops to 0.
2. **Given** a heartbeat built while the index is still loading at startup, **Then** `first_tool_indexed_ever` still reflects the persisted history, while `tool_count` keeps today's sampled semantics.
3. **Given** two clients observed 1 hour after the install anchor and the core stopping 2 hours after it, **Then** the heartbeats sent before the stop carry `clients_day0 = "2"` with `clients_day0_final = false`.
4. **Given** three clients observed within day 0 and a fourth on day 3, **Then** heartbeats after day 0 carry `clients_day0 = "3_plus"` and `clients_day0_final = true`, and the value never changes again.
5. **Given** telemetry is disabled, **Then** nothing new is sent. The local onboarding state still records every fact, so the UI behaves the same.

---

### Edge Cases

- The starter is already configured, under any name, with the same command and args. The offer is not shown.
- A different server already uses the candidate's name (a user's own `time`). That candidate is skipped with reason `name_taken` and the next one is tried. The starter never renames or overwrites a user server.
- The user removes the starter later. The offer may reappear on Home only, as a compact link. The sticky telemetry facts stay true.
- Capture times out after 90 s, for example because the first `uvx` download is slow or the machine is offline. The step shows "Still starting… Retry / Remove" and records `starter_last_failure = capture_timeout`. The server stays configured and quarantined, and nothing is approved.
- The starter connects but reports zero tools. This is treated as `connect_failed`, and the step offers Remove.
- The GUI-launched core has a reduced PATH (macOS Finder launch). Launcher resolution searches the same login-shell PATH that stdio spawning uses, so the offer never promises a launcher the spawner cannot find.
- Docker isolation is enabled globally. The starter follows the normal isolation rules. The card says "runs in Docker", and the capture timeout is 180 s instead of 90 s.
- A config `registries` entry with ID `reference` overrides the built-in source. If the effective source is no longer `builtin/reference`, or `reference/time` resolves to a command other than the audited one, that candidate fails closed with reason `reference_overridden`, and the next candidate is checked the same way.
- The resolved command changes between showing the card and clicking "Add and inspect" (a config hot-reload). The add re-resolves the entry. If the command differs from the one shown, nothing is added and the card re-renders with the new state, so consent must be given again.
- The wizard opens with a client already connected. The second-client section is shown immediately.
- The activity log holds 100k records. The summary must still answer within the SC-006 budget.
- Clock skew or a timezone change does not matter: windows are computed in UTC from record timestamps, and display uses local time.
- A tool approval goes pending only through a toggle that synthesises a record. It is counted in the window only if that path writes a record with `entered_hold = true` (FR-019 requires every hold entry to do so). It always counts in "currently awaiting".
- Sensitive-data detection runs asynchronously, so a call made seconds before the request may not yet show its detection. This is acceptable; there is no consistency guarantee within a minute.
- Server edition: the starter and second-client surfaces are not rendered, and `day_two` is omitted from the payload.

## Requirements *(mandatory)*

### Functional Requirements: starter (US1, US2)

- **FR-001**: The system MUST offer a starter server in the Web wizard's Servers step when all of these hold:
  - the install is fresh;
  - zero tools are indexed;
  - no held tools await review;
  - no configured server matches a starter candidate's command;
  - a candidate passes the launcher and name checks.

  The offer MUST be pre-selected unless the user previously declined it. Pre-selection covers only the add-and-inspect action, never an approval.
- **FR-002**: Starter candidates MUST come from a compiled-in allowlist. Each entry MUST be audited by a maintainer against these criteria: no required inputs (credentials, env or headers), launched as stdio by `uvx` or `npx -y`, no Docker needed, and, in the audited version, no tool that reads files, environment variables or arbitrary network resources. Each entry MUST record its audited package version and expected tool names. A unit test MUST check what is checkable statically: the entry exists in the reference catalog, it declares no required inputs, its launcher is `uvx` or `npx`, and it is not on the exclusion list. The safety claim is limited to the audited version. At runtime the captured inventory is authoritative (US1 scenario 4).

  The v1 list, in order:
  1. `reference/time` (`uvx mcp-server-time`; expected tools `get_current_time`, `convert_time`);
  2. `reference/sequentialthinking` (`npx -y @modelcontextprotocol/server-sequential-thinking`; expected tool `sequentialthinking`).

  `fetch` (arbitrary URL fetching), `everything` (it ships `get-env`, which returns all environment variables, plus LLM-sampling test tools), `filesystem`, `memory` and `git` MUST NOT be candidates.
- **FR-003**: Accepting the offer MUST add the server through the same catalog add door as a normal catalog add, with no request fields beyond what that door already accepts. Immediately before the add, the core MUST re-resolve the catalog entry and abort, adding nothing, if the resolved command differs from the command shown to the user (Definitions: starter candidate). Quarantine default, trust mode (catalog adds resolve to `manual`), inspection exemption, admission scan and definition capture MUST be applied exactly as for that door. The starter MUST NOT pass any quarantine, trust-mode or approval override.
- **FR-004**: Before the add, the offer MUST show the exact command, the isolation mode and the statement that it will be downloaded and run for inspection. The button MUST name that action ("Add and inspect"), not a generic Continue.
- **FR-005**: When the added starter is quarantined, the offer surface MUST show the **captured** tools with their review tiers. It MUST require an explicit "Approve and enable" action, bound to the shown definitions and the server incarnation (Spec 117). No approval MUST happen without that click. If the captured tool names differ from the audited set, the starter copy MUST be replaced by the plain review with a "differs from the reviewed version" notice.
- **FR-006**: The surface MUST skip the review and go straight to the ready state only when the unchanged catalog door produced an unquarantined server, which today means quarantine is globally off.
- **FR-007**: Closing the wizard, unchecking the card or clicking Skip MUST add and run nothing. Skip and uncheck MUST persist `starter_declined` in the onboarding state.
- **FR-008**: The Web Home "Get started" card and a new native Home empty-state card MUST offer the starter, never pre-selected, when zero tools are indexed, no held tools await review and no starter is configured. If held tools await review, they MUST show the review call-to-action instead.
- **FR-009**: Launcher resolution MUST search the PATH that stdio spawning effectively uses: the login-shell PATH (`shellwrap.LoginShellPATH`) plus the process PATH. It MUST NOT spawn the launcher. Results MAY be cached for 60 s.
- **FR-010**: Capture MUST time out after 90 s, or 180 s when Docker isolation applies to the starter. On timeout or failure, the surface MUST offer Retry and Remove and MUST record the failure category. Remove MUST use the normal server-removal path.
- **FR-011**: After the starter becomes live, the wizard's Verify step MUST show the indexed tool count and one suggested first prompt per connected client. The prompt text MUST be fixed per starter kind.
- **FR-012**: The core MUST expose the offer decision through one read endpoint: eligibility and reason, the selected candidate with command, isolation mode and audited tool names, the pre-select flag, and the starter facts. Web and native MUST NOT re-implement the eligibility rules. The core MUST record starter facts, the add included, through the onboarding state, whichever surface acted.

### Functional Requirements: second client (US3)

- **FR-013**: Once at least one client is connected, the wizard's Clients step MUST show "Connect another client" listing every detected client in the registry's fixed order. This applies whether the client was connected in this session, earlier or outside the wizard.
- **FR-014**: Each listed client MUST use the existing preview → write → undo flow. The diff and backup notice MUST be visible before the write button. No client config MUST be written without a per-client click.
- **FR-015**: A client whose on-demand access check is `denied` MUST show the Spec 075 remediation instead of a Connect action. `malformed` MUST show the existing malformed-config message.
- **FR-016**: The onboarding state MUST record four independent sticky booleans, set from false to true and never cleared:
  - `second_client_offered`: the section was rendered with at least one detected client;
  - `second_client_none_detected`: the section was rendered with no detected client;
  - `second_client_skipped`: the user left the section without connecting a second client;
  - `second_client_connected`: two or more distinct observed clients, on any install, upgraded ones included (no anchor needed).

  It MUST also record each observed client's first-observed time, written once.
- **FR-017**: The native Connect Client sheet's success state MUST offer "Connect another client" from the same core list. Client observation MUST happen in the core (Definitions: observed client), so every fact is recorded no matter which surface or tool connected the client.

### Functional Requirements: weekly summary (US4)

- **FR-018**: The activity summary MUST gain an additive `digest` section for any period:

  | Field | Contents |
  |---|---|
  | `calls_brokered` | Total brokered calls, plus per-client counts (top 3 + other + unknown) |
  | `held_for_review` | Distinct `(server, tool)` pairs that entered pending or changed in the window, with per-kind counts; `currently_awaiting` from the review queue |
  | `blocked` | Total refused-before-dispatch attempts, including `rejected`, each counted once, plus per reason class `quarantine`, `profile`, `other`; separately `responses_blocked` (post-dispatch output blocks, whose calls stay brokered and are not in the total) |
  | `sensitive_data` | Brokered calls with at least one detection |
  | `coverage` | `complete` or `partial`, plus `complete_since` (the coverage watermark) |

  Existing response fields MUST be unchanged.
- **FR-019**: "Entered pending or changed" MUST count actual entries into a held state. Every `tool_quarantine_change` record MUST carry two new metadata fields:
  - `entered_hold`: true only when the record's status moved from not-held (none, approved, or approved-and-disabled) to `pending` or `changed`, **and** the hold is in effect, meaning quarantine is enabled for the server;
  - `hold_kind`: `new` or `changed`.

  The digest counts distinct `(server, tool)` pairs with `entered_hold = true` in the window. A further change to an already-held tool (`refreshHeldContractSnapshot`) and a change recorded while quarantine is off never count. Records written before this field existed cannot be classified: until the window contains only new-format records, `held_for_review` carries `coverage: partial`. The digest MUST NOT reconstruct history from current approval state. Fixtures: an already-held tool changing inside the window (not counted), a change with quarantine off (not counted), a tool that enters, is approved and re-enters (counted once).
- **FR-020**: Reason classes MUST come from a fixed mapping table (see plan). `block_reason` values with the `profile_` prefix and the profile-scope refusal map to `profile`. Server-quarantine, tool-pending and tool-changed refusals map to `quarantine`. Everything else maps to `other`. Refusal sites that today write an untyped reason MUST persist a typed reason so that the mapping never matches on free text.
- **FR-021**: Every pruning mechanism (age, record count, byte size) MUST advance the coverage watermark in the same storage transaction as its delete. On the first start of a build with this feature, the watermark MUST be initialised to the earliest surviving record's timestamp. This is conservative: an earlier idle period may then show as partial.
- **FR-022**: All digest figures MUST be computed locally. No digest figure MUST leave the machine. Telemetry carries only view counts (FR-028).
- **FR-023**: Authorization MUST follow the existing summary rules, extended to every digest figure:
  - for a scoped caller, every figure is computed only over servers in the caller's allowed set, including `held_for_review` and `currently_awaiting`;
  - when an attribution filter is set (`profile`, `client`, `token`), the server-scoped review figures (`held_for_review`, `currently_awaiting`) MUST be returned as `null` with `reason: "not_attributable"`, because approvals carry no client or profile attribution.

  Tests MUST prove that holds on a disallowed server cannot change a scoped caller's response.
- **FR-024**: The Web Home MUST show a "This week" card with the four figures. Each links to its view:
  - Activity filtered by the window;
  - the Review queue;
  - Activity filtered `status=blocked`;
  - Activity filtered `sensitive_data=true`.

  It MUST follow the window label rules (US4 scenarios 6-8).
- **FR-025**: The tray glance block MUST show one line, "This week: N calls · H held · B blocked", that opens the native Activity view. The line MUST be hidden when the core is unreachable.
- **FR-026**: The CLI MUST accept `mcpproxy activity summary --since 7d` as an alias of `--period 7d`, with the same values. The table output MUST print the digest, and `-o json|yaml` MUST include it additively.

### Functional Requirements: telemetry (US5)

- **FR-027**: The heartbeat MUST bump to schema 15 and add:
  - `activation.first_tool_indexed_ever`: bool, sticky and persisted, stamped per the Definitions;
  - `activation.first_tool_indexed_after`: enum `"" | lt_10m | 10m_1h | 1h_24h | 1d_7d | gt_7d`, measured from the install anchor and set once; `""` when there is no anchor.
- **FR-028**: The heartbeat MUST add a `day_two` object, in the personal edition only. It contains only booleans, closed enums and buckets:

  | Field | Values |
  |---|---|
  | `starter_eligibility` | Result of the first eligibility check on a fresh install: `"" | eligible | launcher_missing | name_taken | reference_overridden | tools_present | held_tools | not_fresh` |
  | `starter_launcher` | Launcher availability, evaluated on every fresh-install eligibility check whatever the other outcomes: `"" (never checked) | uvx | npx_only | none` |
  | `starter_offered`, `starter_added`, `starter_live`, `starter_declined` | bool, sticky |
  | `starter_last_failure` | `"" | connect_failed | capture_timeout | approval_abandoned` |
  | `starter_kind` | `"" | time | sequentialthinking` (kind of the first add) |
  | `starter_surface` | `"" | wizard | home_web | home_native` (surface of the first add) |
  | `second_client_offered`, `second_client_none_detected`, `second_client_skipped`, `second_client_connected` | bool, sticky |
  | `clients_day0` | `"" | 0 | 1 | 2 | 3_plus`: distinct observed clients whose first observation falls in day 0; provisional during day 0, `""` without an anchor |
  | `clients_day0_final` | bool, true once day 0 has ended (the value is then frozen) |
  | `summary_views` | `{web, tray, cli}`, each `0 | 1 | 2_5 | 6_plus`: views since the last accepted send, the same reset-on-accept window as other delta counters, kept in memory (a restart loses unsent views) |

- **FR-029**: The new fields MUST NOT carry server names, tool names, paths, timestamps, free text or new identifiers. `ScanForPII` MUST reject unknown keys inside `day_two` and `summary_views`, out-of-enum values, non-boolean flags and any serialized map or timestamp in these objects, following the closed-object precedent of `scanTrustModeDistribution`. The telemetry docs MUST list each field under "What is collected".
- **FR-030**: `approval_abandoned` MUST be recorded when a starter stays captured but unapproved for 24 h. It is an observation only and MUST NOT trigger any action.
- **FR-031**: The local onboarding state MUST record all facts whatever the telemetry opt-out setting. Opt-out gates only sending.
- **FR-032**: A summary view MUST be counted in the core when a summary request carries `view=web|tray|cli`, at most once per surface per hour. Surfaces MUST pass `view` only for user-visible renders, never for background refreshes.

### Key Entities

- **StarterCandidate**: compiled in. `{kind, catalog_id, launcher, audited_version, expected_tools[], first_prompt}`.
- **StarterOffer**: derived per request. `{eligible, reason, candidate{kind, command, isolation, expected_tools}, preselect, facts}`.
- **OnboardingState additions**: persisted in the existing bbolt record, all `omitempty`:
  - `install_first_run_at`;
  - starter facts: `starter_eligibility`, `starter_offered`, `starter_added`, `starter_live`, `starter_declined`, `starter_last_failure`, `starter_kind`, `starter_surface`, `starter_added_at`, `starter_server_name`;
  - second-client facts: the four second-client booleans;
  - `client_first_observed_at{client_id → time}`.
- **ActivationState addition**: `first_tool_indexed_ever` and `first_tool_indexed_at` in the existing `activation` bucket.
- **CoverageWatermark**: one persisted timestamp next to the activity bucket.
- **ActivityDigest**: derived. The additive summary section of FR-018.
- **SummaryViewCounter**: an in-memory delta counter per surface, with per-surface last-counted time for the hourly debounce. Drained on an accepted send.

## Success Criteria *(mandatory)*

Cohort definition for SC-001 to SC-004:
- non-CI installs per the dashboard CI filter;
- personal edition;
- continuity identity key (`cont`), with the IP key reported alongside;
- first seen on a build that includes this feature, with at least 14 days of observation.

The release-to-release comparison is pre/post. It is associative, not causal: release composition, autostart behaviour and heartbeat availability are confounds and are reported with the result.

### Measurable Outcomes

- **SC-001 (primary)**: at least **82%** of the cohort have `tool_count > 0` on at least one heartbeat. This is the same heartbeat-sampled instrument as the 71.5% Aug-Oct 2026 baseline. The sticky `first_tool_indexed_ever` rate is reported separately and never substituted. A movement counts as real if it exceeds the larger of 3 points and the 95% interval of the previous four monthly cohorts.
- **SC-002 (primary)**: day-7 return on the IP key is **at least 18%** for the cohort, against 14.9% pooled Jun-Sep. The machine key is reported alongside, with the same noise rule as SC-001.
- **SC-003 (diagnostic)**:
  - Launcher availability: `starter_launcher = none` is below 15% of installs with a non-empty `starter_launcher`. That field is evaluated independently of every other eligibility outcome, so `tools_present` or `name_taken` installs never dilute it. Above 15%, follow-up O1 (an embedded starter) is triggered.
  - Conversion: at least 60% of installs with `starter_offered` reach `starter_live`.
- **SC-004 (diagnostic)**: the share of cohort installs with at least one connected client whose day-0 heartbeats show at least two distinct ids rises by at least 8 points over the pre-release baseline. Both sides use the same instrument: the union of `connected_client_ids` over heartbeats received within 26 h of the identity's first heartbeat. Today that is the startup heartbeat at about 5 minutes and the first daily one. The baseline uses v14 cohorts; the comparison uses schema-15 cohorts. The richer `clients_day0` / `clients_day0_final` fields are a separate diagnostic with no pre-release baseline. Their first release cohort becomes the baseline for later releases.
- **SC-005 (safety)**: no code path makes a starter tool callable without an explicit user approval when a normal catalog add of the same server would be quarantined. A test asserts this for the wizard, Web Home and native doors. No starter code runs before the user clicks "Add and inspect".
- **SC-006 (performance)**: the 7-day summary with digest returns in under 1.5 s on a 100k-record activity log, measured with a new benchmark next to the summary handler tests. The Home card renders its figures within 2 s of Home load.
- **SC-007 (privacy)**: the schema-15 privacy tests pass. There are no new identifier fields. Every new field is enum-closed, and unknown keys are rejected.
- **Kill rule**: if SC-001 and SC-002 show no movement beyond the noise rule after two releases and four weeks of cohort data, the starter pre-selection and the summary card revert to opt-in. The telemetry stays.

## Assumptions

- **A1**: The starter is `time` when `uvx` resolves, otherwise `sequentialthinking` when `npx` resolves. `time` is preferred because it is useful in a real prompt and makes no network calls after the first package download. The other reference servers are excluded (FR-002).
- **A2**: "Lands in quarantine-exempt trusted state only if the catalog marks it trusted": no catalog source marks a server quarantine-exempt today (MCP-1072: provenance is informational), and this spec adds no such marker. So the starter is quarantined under the default config, and the in-place review (FR-005) is what gets its tools indexed. The security model is unchanged, at the cost of one extra click.
- **A3**: Pre-selecting the add is acceptable only with informed consent. The add starts the 1-hour inspection exemption, which downloads and runs the package as a host process (Docker isolation is off by default) before any approval. This is true of every catalog add today. So the card states the command and isolation mode, the button names the action (FR-004), and nothing runs on close or Skip. Tool *calls* stay blocked until approval by the existing tool gate.
- **A4**: The experiment is judged pre/post on release cohorts, not with a randomised holdout. Weekly new installs (about 75-230, depending on the key) make a 10% holdout too small to read within the kill window. Cohort retention has been flat for four months, which gives a stable baseline.
- **A5**: Day-7 return is measured by heartbeat presence, which needs the core running. The summary helps D7 only through users who keep MCPProxy running or reopen it. This is why US4 is P3 and US1 carries the primary metric.
- **A6**: The summary reuses `GET /activity/summary` and `activity summary`, with an additive `digest`, a `--since` alias and a `view` parameter, instead of a new endpoint or command. That keeps one aggregation path.
- **A7**: "Calls brokered" counts dispatched upstream calls only, not internal built-in calls such as `retrieve_tools`. Users understand calls to their tools; built-ins are an implementation detail.
- **A8**: The second-client list uses `exists == true` from the stat-only connect list as "detected". It never opens a client config just to decide whether to list it (Spec 075's no-content-read rule).
- **A9**: Server edition is out of scope for the starter, second-client and Home card surfaces. The additive summary fields are edition-neutral.
- **A10**: Installs upgraded from an earlier build have no install anchor. Their day-0 and time-to-first-tool fields are `""`, and they are excluded from the SC-004 diagnostic. All other facts, including client observation and `second_client_connected`, work on upgraded installs. This is acceptable because the cohorts are new installs.

## Open Questions (the design proceeds with the stated default)

- **O1**: Embedded starter. If `launcher_missing` exceeds 15% (SC-003), add a compiled-in, in-process read-only starter that needs no runtime. Default: not in v1. It adds a new server kind with its own lifecycle verbs (see the in-process scanner trap, MCP-2396).
- **O2**: A weekly OS notification ("MCPProxy this week: …"). Default: not in v1. The tray line and Home card come first; a notification needs its own opt-in UX.
- **O3**: Should a decline also hide the Home offer entirely? Default: no. It becomes a compact link (US2 scenario 3).
- **O4**: Pin the audited starter version in the install command (`uvx mcp-server-time==X`) instead of relying on the captured-inventory check. Default: no. Pinning changes the shared reference catalog entry for every user and needs a release-time bump process. The captured inventory plus a bound approval already guarantees that the user approves what actually runs.

## Out of Scope

- Any change to quarantine, trust modes, scan policy or approval semantics.
- Telemetry receiver and dashboard changes. quickstart.md gives the queries only.
- A CLI starter flow.
- Server edition onboarding.
- Notifications and e-mail digests.

## Commit Message Conventions *(mandatory)*

Commits use `Related #<issue>` once an issue exists. `Closes #...` appears only in the PR that completes the work, with one keyword per issue. Commits carry no co-author trailers and no AI attribution.

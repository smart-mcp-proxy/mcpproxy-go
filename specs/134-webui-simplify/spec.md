# Feature Specification: Web UI simplification

**Feature Branch**: `134-webui-simplify`
**Created**: 2026-11-25
**Status**: Draft
**Input**: Make the MCPProxy Web UI simpler and more solid for a developer who runs many MCP servers, without losing any function. Based on a 22-finding audit of v0.75, a design system and mockups.

## Background

The audit ran the personal edition against three data sets (fresh install, a seeded install, a 34-server fleet), in light and dark themes, at 1440 px and 390 px, with the keyboard, and through the repository's Playwright sweeps.

- Audit (22 findings, referred to below as A1–A22): https://claude.ai/artifact/DME4fgSCRrZbWAX57JvQ33
- Design system (tokens, content rules, components): https://claude.ai/artifact/71AA18hShTq1ibDmMB5Tzt
- Mockups: https://claude.ai/artifact/ME3Bv5Jmbg9jyGUwLNWe1Q

Skills over MCP (Specs 125, 130, 131) shipped after the audit data was seeded, so the audit has no skills finding. Its screens are covered by FR-036 to FR-039.

This spec builds on Spec 109 (`specs/109-ux-navigation-consistency`), which already gives the Web UI one health vocabulary (`health.status`, eight values, label table generated from `internal/health/constants.go`), one needs-attention list (`GET /api/v1/attention`), one review queue (`/review`), ordered `health.actions` and `?tab=` deep links. This spec does not replace any of that. It changes how the Web UI lays it out.

## Governing rules

These three rules rank above every requirement below.

- **R1 — No function is lost.** Everything a user can do or read in the Web UI today stays possible after this work, with the same result, at desktop and phone widths and by keyboard. This spec moves, regroups and restyles. It removes nothing.
- **R2 — Unnamed means unchanged.** A requirement below describes what a page shows by default. Any action, link, menu item, toggle, field, shortcut, URL parameter, state (loading, error, empty, refused) or piece of information that exists today and is not named below stays, with its present behaviour. If a requirement and an existing function cannot both hold, the function wins and the requirement is corrected.
- **R3 — The inventory is the contract.** [function-inventory.md](function-inventory.md) lists, page by page, functions that reviews of this spec found at risk. Each pull request extends the list for the pages it touches before changing them (FR-065). A function may move one level down (a row menu, an expanded row, a "Manage" panel, a "More filters" popover, a collapsed section). It may not disappear.

**The mockups are a direction, not a contract.** They show the default layout and leave out much of the depth the pages have. Where they differ from this document, this document wins:

| Mockup (first version) | This spec | Why |
|---|---|---|
| Five status words | The eight Spec 109 labels in five colour tones (FR-003) | The label table is shared with the macOS app, the tray and the CLI. |
| No topology diagram on Home | Kept, lower on the page, collapsed (FR-040) | R1 |
| One button per row, no menus; fewer columns, filters and tabs | Everything else moves one level down | R1 |
| "Viewing" in the top bar | "View as" in the filter row of the pages it affects, with a chip when set (FR-016, maintainer decision 2026-11-25) | A global filter in the top bar is easy to forget. |
| "Add server" button only | "Add server" plus the existing Add menu (FR-011) | R1 |
| One save row in Settings, "Save and restart" | Per-section save as today, placed so it covers nothing (FR-052) | R1; the Web UI has no call that restarts the core. |
| Invented counts, rows, field names | Existing data and fields | Mockup data is illustrative. |

## User Scenarios & Testing *(mandatory)*

### User Story 1 - See what is broken in a large fleet and fix it (Priority: P1)

A developer with 30 or more servers opens Servers. Today the page is a grid of large cards in configuration order, each with several badges and a raw error string, so broken servers are spread through the page (A2, A14). They want broken servers first, one line each, with the one thing to do.

**Why this priority**: Servers is the most used page and the one that degrades most as the fleet grows.

**Independent Test**: Start the core with the 34-server fixture fleet: 28 usable, 6 enabled but not usable, none disabled. Open `/ui/servers` at 1440×900.

**Acceptance Scenarios**:

1. **Given** the fixture and the "All" chip, **When** Servers opens, **Then** the 6 unusable servers are the first rows, each row shows name, status pill, a one-line reason, tool count, last call with the 24 h error link, its primary action when it has one and the "more" menu, and at least 14 rows are visible without scrolling.
2. **Given** a server whose command is not found, **When** its row renders, **Then** the reason is a short sentence on one line, the server name opens server detail where the full diagnostic is in the first screen, and the button is the label of `health.actions[0]`.
3. **Given** the chips "Not online", "All", "Online", "Disabled", **When** the user selects one, **Then** the list shows that group, the chip shows its server count, and the choice is in the URL, including an explicit "All".
4. **Given** "More filters", **When** it is opened, **Then** Connected, Enabled and Quarantined are selectable with their counts, as today.
5. **Given** an old link with any `?status=` value, **When** it is opened, **Then** it filters as it does today.
6. **Given** no profile filter, **When** the user compares header, Home and Servers, **Then** the online count is the same in all three (A15).

---

### User Story 2 - One look in both themes (Priority: P1)

Every page uses the same status pill, button styles, type sizes and spacing, in light and dark. Today the accent colour and badge colours change between themes, emoji are used as icons in places, and pages use their own badge styles (A1, A11, A17).

**Why this priority**: Every other story uses these parts. It ships first.

**Independent Test**: Open each top-level page in light and dark and compare against the design system; run the contrast sweep.

**Acceptance Scenarios**:

1. **Given** a server with a known `health.status`, **When** any page shows its status as text, **Then** it is the Spec 109 label, rendered by the one status pill component.
2. **Given** light and dark themes, **When** the same page is opened in both, **Then** the primary button uses the brand blue in both and each status tone means the same thing in both.
3. **Given** any page, **When** its chrome is inspected (navigation, headings, buttons, badges, empty states, toasts), **Then** it contains no emoji; icons are inline SVG from one set.
4. **Given** both themes, **When** the contrast sweep runs, **Then** every text and control meets WCAG AA.

---

### User Story 3 - Review a quarantined server without approving blind (Priority: P1)

A developer adds a server. It is quarantined. Today, while its tool definitions are not captured, the most prominent button approves the server without showing its tools (A4). Server detail has a Review tab and a Security tab that overlap, and its header repeats facts (A3).

**Why this priority**: This is the product's security promise.

**Independent Test**: Add a quarantined fixture server and walk from the sidebar to an approval; repeat while its definitions are not captured.

**Acceptance Scenarios**:

1. **Given** a quarantined server with `definitions_captured` true, **When** its review screen opens, **Then** the screen is as today: tools and scan result above the buttons, the counted approve button primary.
2. **Given** a quarantined server with `definitions_captured` false, **When** its review screen opens, **Then** the primary button is the existing action that tries to fetch the definitions, and approving without them is a secondary, clearly named button that asks for a confirmation naming the server.
3. **Given** the fetch attempt fails, finds no tools, or the server is disabled, **When** the screen reloads, **Then** it reports the outcome of the attempt, and neither enables nor approves the server. Whether the screen is in the captured or the not-captured state depends only on the reloaded `definitions_captured` value.
4. **Given** a trusted server with changed tools, **When** its review screen opens, **Then** its buttons and rules are exactly as today.
5. **Given** server detail, **When** it opens, **Then** its tabs are Tools, Review and scans, Skills (when available), Logs, Configuration; `?tab=review` lands on the review part and `?tab=security` lands on this server's scan part; and the persistent summary (name, status, reason, primary action) appears once.

---

### User Story 4 - Home answers "is anything wrong?" (Priority: P2)

Home today shows notices, then the attention list or the Get started prompt, then a topology diagram that takes most of the first screen and does not scale past a few servers (A6). The user wants the answer, the numbers and recent activity in the first screen.

**Independent Test**: Open `/ui/` with the fleet fixture at 1440×900 and 390×844.

**Acceptance Scenarios**:

1. **Given** attention items, **When** Home opens, **Then** after any notices the first screen shows the attention list with its fix buttons, the usage figures and recent activity; the topology diagram is below them, collapsed, and opens in place with everything it has today.
2. **Given** more than five attention items, **When** the user selects "See all", **Then** the full ordered list is shown and each item's fix is one activation away.
3. **Given** the attention list is loaded and empty, and neither the Get started branch nor the present hold on the all-clear message applies, **When** Home opens, **Then** one line says nothing needs attention. While it is loading or failed, the page says that instead.
4. **Given** the present Get started condition (no configured server and no attention item), **When** Home opens, **Then** Get started is shown as today.

---

### User Story 5 - Read Activity calls without scrolling sideways (Priority: P2)

The calls table has up to ten columns (time, type, server, tool, scope, details, sensitive, intent, status, duration) and overflows at common widths; opening the filters pushes the table down the page (A7, A19).

**Independent Test**: Seed 50 calls including failures and repeats; open `/ui/activity?view=calls` at 1280 px.

**Acceptance Scenarios**:

1. **Given** the calls view at 1280 px, **When** it renders, **Then** it shows five data columns (time, tool, result, client, duration) and one action column holding the row-open button, and has no horizontal scrollbar.
2. **Given** a row, **When** the user opens it, **Then** type, server, scope, details, sensitive and intent are in the detail, with the links and "Why" actions the row has today.
3. **Given** the Sort control, **When** it is opened, **Then** all five present sort keys (time, type, server, status, duration) are selectable in both directions.
4. **Given** the filter popover, **When** it is opened, **Then** every filter that can be set today can be set there, and it opens over the page without moving the results.
5. **Given** many active filters at 390 px, **When** they do not fit one row, **Then** an overflow control lists all of them and each can be removed.
6. **Given** "Group repeats", **When** Activity opens for a user who never changed it, **Then** it is on; a stored choice is respected; grouped rows keep their expansion, run summary and paging by runs.

---

### User Story 6 - Scan Tools and Clients (Priority: P2)

Tools is one flat list with five badges on every row; the Clients table has six columns and the connect list packs several controls into one cell (A8, A9, A16).

**Independent Test**: Fleet fixture; open `/ui/tools` and `/ui/clients`.

**Acceptance Scenarios**:

1. **Given** tools from many servers, **When** Tools opens, **Then** tools are grouped under their server in sections that collapse; a "Flat list" switch gives today's list with all seven sort keys.
2. **Given** grouped mode and a tool that is read-only, approved and enabled, **When** its row renders, **Then** it has no label. Labels appear only for exceptions (FR-056).
3. **Given** a tool row, **When** the user opens its details, **Then** everything the row and its detail surface show today is there.
4. **Given** selected tools, **When** some are in collapsed sections, **Then** the batch bar states how many are selected in total, and each action applies to the selected tools that are eligible for it today, whether or not their section is collapsed.
5. **Given** Clients, **When** it opens, **Then** every client listed today is one row with name, state, reach and one button; everything else the row has today is in a "Manage" side panel with grouped sections, destructive actions last and closed by default; the page toolbar, warnings and bulk connect flow are as today.

---

### User Story 7 - Use it from the keyboard and on a phone (Priority: P2)

On an ordinary page load the first Tab press opens the command palette; some controls are click-only; one link leads to a not-found page; at 390 px several pages overflow sideways or hide their main action (A12, A13).

**Independent Test**: Keyboard walk from page load; 390×844 viewport.

**Acceptance Scenarios**:

1. **Given** an ordinary page load (no dialog, no deep-link focus target), **When** the user presses Tab once, **Then** focus lands on "Skip to content", no dialog opens, and activating it moves focus into the main content.
2. **Given** a load that intentionally focuses something (first-run Setup, `?create=1`, `?focus=`, `?tool=`), **When** it opens, **Then** that focus is kept as today.
3. **Given** the phone menu, **When** it is used by keyboard, **Then** it opens, closes on Escape and returns focus; when closed, nothing inside it is a tab stop. Theme choices are operable by keyboard.
4. **Given** a 390 px viewport, **When** any page this feature changes is opened, **Then** the document does not scroll sideways and no container does except log output, code and JSON blocks, diff views and filter chip rows, and every button in the page body is at least 44 px tall.
5. **Given** any action in the inventory, **When** its test runs, **Then** it activates the action by keyboard and at 390 px and asserts its result.

---

### User Story 8 - Change a setting without hunting (Priority: P3)

Settings opens on its densest section, descriptions run to several lines, and the save bar covers the last fields; the profile editor has the same overlap (A10, A20).

**Independent Test**: Open `/ui/settings`, change one field in General, save; open it with `?focus=` for a field in another section.

**Acceptance Scenarios**:

1. **Given** Settings opened with no `?tab=` or `?focus=` that selects a section, **When** it renders, **Then** General is shown and sections are a list at the side (a select at phone width).
2. **Given** `?focus=<field>` or `?tab=<id>`, **When** Settings opens, **Then** it opens where it does today, including a server-edition section that becomes valid after edition discovery.
3. **Given** a field with long help, **When** it renders, **Then** a short summary is visible and the full text is one activation away on the page, whether or not a docs page exists.
4. **Given** unsaved changes in a section, **When** the user scrolls to its end, **Then** its Save and Discard are below its last field and cover nothing.

---

### Edge Cases

- A server name or error of 200 characters: the row truncates and the full text is on server detail, not only in a hover title; the row height does not change.
- 0 servers, 1 server, 300 servers: the empty state with its present actions, a one-row table, and a windowed table that still reaches every server.
- A core older than Spec 109 that sends no `health.status`: the present fallback labels, counts and actions apply unchanged.
- A status the UI does not know: the pill shows the raw value in the neutral tone and sorts after the error and review tones and before `connecting`.
- Two servers with the same display title: order is by canonical id.
- Scoped callers and the server edition: they see only what they see today. No count, group header or label may reveal a server outside their reach; group headers are built from returned data only.
- Partial results, refused requests, stale lists: the present warnings, Retry and Clear actions stay.
- SSE disconnected: counts keep their last value until the next successful refresh (Refresh and Retry keep working), and the existing connection indicator shows the state.
- Reduced motion: collapsing and expanding do not animate.

## Requirements *(mandatory)*

### Functional Requirements

**Visual system**

- **FR-001**: The Web UI MUST define the design-system tokens (brand, on-brand, brand-text, brand-soft, border, five status tones each with a soft background, focus) for light and dark in one stylesheet, as overrides of the two existing daisyUI themes. No theme is added or removed and the theme menu keeps working.
- **FR-002**: The primary button colour MUST be the brand blue in both themes. Destructive actions use the error tone. Counting units are: the page header, a card or panel header, a form's action row, a dialog footer, and each repeated item (table row, list item). Each unit has at most one primary button; other actions keep their place with secondary styling.
- **FR-003**: One component MUST render server status text when `health.status` is present: it prints the Spec 109 label. Tone: `ready` → ok; `needs_review` → review; `connecting` → busy; `disabled` → off; `sign_in_required`, `needs_secret`, `needs_config`, `error` → error; unknown → neutral, raw value. Without `health.status` the present fallback is used as is. The existing header component named `StatusPill` is renamed so the name is free.
- **FR-004**: A view MUST NOT print `health.status` with words of its own. Words for other facts (transport connected, administrative state, quarantine, client connection) keep their present wording. A unit test covers the eight known values on every surface that prints them. This includes the two server-edition status cells (`/my/servers`, `/admin/servers`): they print the label and keep `health.summary` as separate detail. Beyond that, those pages change only as far as the shell and SC-005 and SC-007 require (control size, keyboard operation).
- **FR-005**: UI chrome MUST NOT contain emoji. Icons are inline SVG, 24 px grid, stroke 2, from the set the sidebar uses. Every icon that carried meaning keeps its accessible text.
- **FR-006**: Type and spacing MUST follow the design-system scale (page title 30/36, section title 20/28, body 16/24, small 14/20, caption 12/16; spacing 4, 8, 12, 16, 24, 32; radius 4). Text smaller than 12 px is not used.
- **FR-007**: Both themes MUST keep WCAG AA contrast. The contrast sweep is extended to every page this feature changes, in both themes.

**Shell**

- **FR-010**: The personal-edition sidebar MUST list, in this order: Home, Servers, Tools, Clients, Profiles; group "Protect": Review, Secrets; group "Monitor": Activity, Usage; then Settings. Every badge the sidebar shows today (Home attention, Review with its active/deferred explanation, Servers, Tools, Clients, Secrets) stays on its item. The logo link, collapse/expand, Setup entry, Feedback, theme menu and every other present sidebar control stay. The server-edition menus, identity and sign-out are unchanged.
- **FR-011**: The header MUST show, each under its present authorization gate (a tenant sees neither the fleet indicator nor the Add controls): the search button; the online indicator linking to Servers with its present loading, unavailable, awaiting-review and offline states; "N need attention" (hidden when zero) opening the attention popover, in which every item's fix is selectable; "Add server" as a direct button next to the existing Add menu with all its entries. Usable-tool count and routing mode, including the pending-mode notice, are shown on Home, and leave the header only in the pull request that adds them to Home.
- **FR-012**: "Online" MUST mean `health.usable`. "N need attention" MUST be the Spec 109 attention item count in the header, the Home list and the Home sidebar badge; it counts items, not servers. Servers chips count servers. With no `?profile=`, the online count on Servers equals the header's; when a profile filter is actually applied (not merely present, as with `?profile=-`), Servers counts are for that profile and say so, as today.
- **FR-013**: On an ordinary load "Skip to content" MUST be the first tab stop and MUST move focus to the main content. The command palette MUST open only on its existing shortcuts (Cmd/Ctrl+K, and `/` outside editable elements) or its button, never on Tab or focus; its present keyboard behaviour is unchanged. Intentional focus on entry is unchanged. Every interactive element is keyboard-operable and shows a focus ring, including the phone menu and the theme choices.
- **FR-014**: Below 768 px the sidebar MUST be an overlay with a keyboard-operable menu button, Escape to close and focus return; closed, its content is not focusable. The header keeps menu, logo, search and, under the same gates as on desktop, the compact online link, the attention count and the Add controls. The project links and version of the page footer stay reachable below 768 px in a compact disclosure, also on pages without the sidebar.
- **FR-016**: The Viewing filter (view as a profile and/or a client) MUST move from the header to the filter row of every page or tab that registers a scope today (Servers, Tools, Activity, Usage, and Clients with its Tokens tab), as a "View as" control shown under its present visibility rule, at desktop and phone widths. Its choices, URL parameters, carry-over between pages, clear action, conflict and orphaned-scope recovery are unchanged. A value that is applied on the current page is shown as a removable chip "Viewing as <name>" and the page subtitle states the narrowing. A value that is carried but not applied on the current page (for example `client` on Servers, `profile=-`, or any value on a page with no scope) is shown as a removable chip marked "not applied on this page", so it can always be seen and cleared. Setting a new value needs a page that has the control; this is an accepted cost (maintainer decision 2026-11-25). Header counts stay fleet-wide.
- **FR-015**: No link or button may lead to a route that renders the not-found page. The Activity scan drawer's "Security & scan reports" link, which does today, is corrected.

**Servers**

- **FR-020**: Servers MUST be a table with fixed row height: name, status pill, reason, tool count, last call with the 24 h error count linking to filtered Activity, an action cell (FR-021) and the "more" menu with every secondary action the card has today, each behaving as it does today: commands run in place, confirmations open in place, links navigate. The row has no inline expansion and no separate "Details" link: the server name opens server detail, and everything else the card shows (display title versus id, trust mode and its shield, scan verdict and warnings, pending and changed counts, the diagnostic) MUST be reachable from the first screen of server detail, whichever tab is open: the summary facts are visible there, and a labelled control in the first screen opens the complete, untruncated diagnostic and raw error. Transport stays under the name. Reason and last call never wrap; a long reason is cut with an ellipsis. This replaces the card layout of Spec 109 FR-013; its content rules stay.
- **FR-021**: The action cell MUST show a button only when `health.actions` is not empty; it is `health.actions[0]` with its Spec 109 label and behaviour; the stale-login guard and the old-core fallback stay. Only this button counts toward "one button per row".
- **FR-022**: The reason MUST be one line from a pure function, in this order: a known failure pattern in the diagnostic (command not found, connection refused, sign-in expired, missing secret, timeout, invalid configuration) as a short sentence; else the structured diagnostic or short last-error cause the card shows today; else `health.summary`; else the status label.
- **FR-023**: Chips "Not online" (enabled and not usable), "All", "Online" (usable), "Disabled" MUST show server counts and write `?filter=not-online|all|online|disabled`. On a bare visit (no `filter`, `status`, `q` or `profile` in the URL) the default is "All" at 768 px and wider, and "Not online" below that when it is not empty; a link that carries only `q` or `profile` defaults to "All" at every width. Selecting a chip clears `status` and keeps `q` and `profile`. The present Connected, Enabled and Quarantined filters stay selectable with counts under "More filters", and every `?status=` value keeps its meaning. The words "needs attention" are used only for the attention list.
- **FR-024**: Default order MUST be total: statuses in error tone, then review, then unknown, then busy, then usable, then disabled; then display title; then canonical id.
- **FR-025**: The page toolbar and states MUST stay: Refresh, Scan All under its present gate and scope, profile picker, text search, enabled and available-tool totals, stale-list Retry, missing-profile Clear, first-run Add/Browse/Import, no-match recovery, CLI hints.
- **FR-026**: With a 300-server fixture a Playwright test MUST show the first row within 2 s of navigation on the CI runner and MUST collect, by scripted scroll, a set of server ids equal to the fixture's.

**Server detail and review**

- **FR-030**: Server detail MUST show its persistent summary (name, status, reason, primary action) once, in its header, with the present Actions (restart or reconnect, enable or disable, sign in again, discover tools, quarantine or review, refresh). Distinct measures and evidence on tabs are not duplicates and stay. Tabs: Tools, Review and scans, Skills, Logs, Configuration.
- **FR-031**: "Review and scans" MUST contain two parts with their own anchors: the review screen as today, and this server's scan part with everything the Security tab has today (start and cancel, per-scanner progress, skipped and failed scanners, findings, job id, full report). `?tab=review` lands on the first, `?tab=security` on the second, unless a focus parameter that today selects another tab (such as `?tool=`) is also present; then today's precedence holds. `?tool=`, `?focus=`, the Skills state filter and every other present query behaviour work on direct load and on in-page navigation.
- **FR-032**: For a quarantined server, an approving button MUST be primary only when `definitions_captured` is true; then buttons, labels and rules are today's. When it is false, the primary button is the existing fetch attempt, and approving without definitions is a secondary button with a confirmation naming the server. The outcome of a fetch attempt is reported separately; `definitions_captured` in the latest successfully loaded review chooses between the captured branch and the not-captured branch; inside the captured branch every button appears and hides by today's rules. Trusted-server review, Reject, force confirmation, filters, paging, bulk selection and stale-decision handling are unchanged.
- **FR-033**: This feature MUST NOT add an approve control for servers or tools outside the review screen, and MUST NOT remove any existing control: the Tools batch bar with all its actions, server detail's immediate Block, Block All, enable and disable, and skill approval, block and unblock on the Skills tab all stay.
- **FR-034**: Destinations MUST stay by kind: sidebar Review opens the queue with its triage functions; a server's Review action opens its review screen; a skill-only item opens Skills; sign-in comes before review where `health.actions` says so.
- **FR-035**: When a page has something to act on below the first screen (a held tool, a flagged or failed scan, a health action), a summary line in the first screen MUST link to it, and following the link reveals and focuses it even when filters would hide it (A18).

**Skills over MCP (Specs 125, 130, 131)**

- **FR-036**: The Skills tab of server detail MUST keep everything it has today and take the visual system of this spec. The list becomes a table: skill, description, state, scan result, file count, one action ("Review", "Review changes", "View", "Unblock"). The filter (All skills, Needs review, `?state=needs-review`), Refresh, last-refreshed and last-error notes, the truncated notice, and the empty states (turned off, not supported, this server lists no skills, nothing to review) stay. A server excluded from agents (`expose_skills: false`) keeps its badge next to the rows, as today. "Load more" stays in the file viewer, where it is today. `enable_skills` and `expose_skills` remain configuration settings; this feature adds no switch for them.
- **FR-037**: Skill states MUST keep their five labels and use the status tones: "Approved" → ok; "Awaiting review" and "Changed" → review; "Blocked" → error; "Unusable" → off, with its reason. They are rendered by the same tag component as other state labels, never by the server status pill.
- **FR-038**: The skill review card MUST keep the order and content of Spec 131: scan verdict, coverage notices and findings before the decision buttons; "Read SKILL.md"; changed, added and removed files with the line diff and approved-copy actions; "Unchanged files" and "File hashes" collapsed; the file viewer with copy and focus return; the stale-version notice and "Open the new version"; Approve or Approve changes as the one primary button when the skill is eligible for it, and exactly one of Block or Unblock by the skill's blocked state, under today's guards. Review-bound approval, refusal messages and authorization are unchanged.
- **FR-039**: Skills MUST stay visible everywhere they are today: review queue rows (skill-only rows open the Skills tab; rows with tools and skills offer both), the `skill_review` attention item on Home and in the header popover, the Review count, and the Activity types "Skill Access" and "Skill Approval Change" (their emoji icons are replaced by SVG under FR-005). Tool review and skill review stay separate decisions on separate screens. Scoped callers see no skill review surface, as today.

**Home**

- **FR-040**: After any notices and warnings Home shows today, Home MUST show in order: the attention list (five items, then "See all" revealing the rest in place), the usage strip with its three figures, their present filters, an accurate window label and the View usage link, recent activity (five rows, link to Activity), a line with usable-tool count and routing mode, the present shortcuts (Connect clients, Import from client configs, Recent sessions, Add server, Browse catalog), the security-scan summary, Token savings with its breakdown, and the topology diagram in a section that is collapsed by default and remembers its state. No count is introduced that Home cannot compute today.
- **FR-041**: Get started, first-run, loading, failed and tenant branches of Home MUST keep their present conditions and content.

**Activity**

- **FR-045**: In the calls view the table MUST show five data columns (time, tool, result, client, duration) and one action column holding the keyboard-operable row-open button. Tool-less and unattributed records show their present fallback text. A success keeps a quiet indication with accessible text; every other status keeps its own label. Other Activity views keep their columns.
- **FR-046**: A Sort control MUST offer all present sort keys in both directions. Grouping applies only in time order, as today.
- **FR-047**: Active filters MUST be removable chips in one row with an overflow control when they do not fit. One popover MUST let the user set every filter that can be set today. Summary tiles, legends, Clear all, URL and stored state, and view-specific rules are unchanged. Opening the popover does not move the results; the boundary between filters and results has a fixed position whether results, an empty state or an error is shown.
- **FR-048**: "Group repeats" MUST default to on when no choice is stored. Its key, opt-out, run summary, expansion of all members and paging by runs are unchanged. Record totals and export never change; run and page totals change with grouping, as today.
- **FR-049**: The row detail MUST show every column removed from the table, with the links and actions those cells have today.

**Settings and forms**

- **FR-050**: General MUST be the fallback section when neither `?tab=` nor `?focus=` selects one. Links inside the Web UI that rely on today's default (the setup wizard's "change these in Settings" link, in both its forms) MUST be changed to name their section (`?tab=security`) in the same pull request. Section labels: General, Access and keys (today "Security & Access"), Catalog sources, Advanced, Edit as JSON (today "Raw JSON"), plus the server-edition section. Tab ids, focus and prefill behaviour, search across sections, Reload and every field control are unchanged.
- **FR-051**: Each field MUST show a short summary that may wrap, with its full help one activation away on the page. "Learn more" links to docs.mcpproxy.app are added only where the page exists.
- **FR-052**: Save and Discard MUST keep their present scope, which is the mounted form: a section, an Advanced accordion or an ongoing search; JSON Validate and Apply and the separate binding, scanner and catalog actions stay separate. Each MUST sit in normal flow after the fields it applies to, covering nothing. "Needs restart" marking and the after-save notice stay. In the profile editor one action row MUST sit after both the policy form and the tool rules, keeping Enter submission and the separate rename, delete, assign and Try actions.
- **FR-053**: The setup wizard's verify step MUST show distinct text for each state it can know today: unknown, no handshake, handshake only, recorded upstream success. It MUST NOT refer to a prompt that is not on screen. Its tabs and its hints disclosure become keyboard-operable; its passive hint text stays as rendered text (A22).

**Tools**

- **FR-055**: Tools MUST default to rows grouped by server in collapsible sections, built only from the tools returned to this caller, and MUST offer a "Flat list" mode equal to today's table, including its rows, labels and all seven sort keys in both directions. The choice is stored. Statistics, their links (including Needs review → `/review`), View as (FR-016) and its controls, partial-result warnings, paging and page size are unchanged.
- **FR-056**: In grouped mode a tool row MUST show name, one-line description, last used, and exception labels only: "Writes", "Destructive", "Unannotated" (the returned tier, as today), "New, needs review" or "Changed, needs review" (Spec 109 wording, linking to the server's review), "Disabled", "Denied by config". A named Details action opens everything the row and its detail surface show today. Visible filters are search, server and "Only exceptions" (any label above); Status, Tier, Approval and callable filters stay under "More filters" with their URL parameters and the `?risk=` alias.
- **FR-057**: Selection MUST work across collapsed sections; the batch bar states the total selected; each action keeps its present eligibility rule (for example Reject skips tools that are already approved); select-all keeps its present per-page meaning and says so.

**Clients and tokens**

- **FR-060**: The Clients table MUST show, for every client it lists today, name, state, reach and one button. State and reach keep every distinction and wording the page has today, including unknown reach; "All servers" is shown only where the page shows unrestricted access today. "Manage" opens a side panel (the pattern Activity uses for call details), not an inline expansion. The panel groups everything else a row has today under four headings in this order: what it can use (profile, lock, explain access), how it connects (config path, sign-in method, check and verify, undo), key (rotate and finalize, one-time credential display), and a closed "Disconnect or remove" section (disconnect, revocations, forget). The panel closes on Escape and returns focus to the row's Manage button, or to the Clients toolbar when the row no longer exists; result dialogs (for example after Forget, or a one-time credential) stay until dismissed, as today; `?focus=<id>` opens it. It includes last seen, sessions, config path, the four scoped links, check and verify actions, profile, lock, explain access, forget, rotate and finalize, revocations, one-time credential display, and browser sign-in. Connect and Undo keep preview-before-write and their present guards. Only supported apps that are positively not detected fold into one expandable line; when the Clients tab is active, a focused client that is in the roster returned to this caller is always revealed (an explicit Tokens or Endpoint tab in the URL keeps its present behaviour); a focus outside that roster reveals nothing and requests nothing, as today. The page toolbar, warning banner, bulk connect flow, other-client snippet, and the Tokens and Endpoint tabs are unchanged.
- **FR-061**: The agent-tokens table MUST show every present column or move it to a row disclosure, with no clipped content at 1280 px, and all row actions. The badge text "Legacy scope" MUST be replaced by wording that describes the token's configured limits; it makes no claim about effective access. Token creation, including manual server and permission selection, migration and `/tokens` redirects, is unchanged (A21).

**Usage**

- **FR-062**: Usage MUST keep its four charts (calls per tool, error rate, response size ranking, timeline) with their present data, click-through links and accessible text, and keep the time-window selector, status filter, sort, scope chips, freshness note, token-savings tiles and every empty, loading, error and conflict state. The charts change only in tokens, type and spacing (FR-001 to FR-006); the filter row changes only as FR-016 says. Chart colours come from the brand and status tokens and meet AA in both themes.

**Compatibility and process**

- **FR-065**: Each pull request MUST begin by extending [function-inventory.md](function-inventory.md) for the pages it touches, from the code as it is: every action, link, menu item, toggle, field, shortcut, URL parameter, state and displayed fact, with its place before and after. A row with no place after blocks the merge. Each action row has a test that activates it in the new layout, by keyboard and at 390 px, and asserts its result. Each row for a passive fact or state has a test that produces the condition and asserts what is rendered, at 1440 px and 390 px. Removing a function needs a separate, explicit maintainer decision and is not part of this feature.
- **FR-070**: No REST route, payload or backend behaviour changes. If a requirement cannot be met without one, the pull request stops and records it.
- **FR-071**: Every existing route, redirect, `?tab=`, `?view=`, filter and focus parameter MUST keep resolving to the same function.
- **FR-072**: No new runtime dependency is added.
- **FR-073**: `data-test` attributes are kept where the element still exists; where it moved, the test moves with it in the same pull request. No test is deleted without a replacement that reaches the same function.
- **FR-074**: Scoped callers and the server edition see no more than today.

### Key Entities

- **Status tone**: ok, review, error, busy, off or neutral. Derived from `health.status` by FR-003. Colour only.
- **Server row**: name (link to server detail), transport, status, reason, tool count, last call and errors, primary action, more menu.
- **Exception label**: a tool-row label for a state that differs from read-only, approved, enabled, annotated.
- **Function inventory row**: page, function, place before, place after, test.
- **Design token**: a named colour, type style, spacing step, radius or shadow with a light and a dark value.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: With the 34-server fixture at 1440×900 and the "All" chip, Servers shows at least 14 rows in the first screen (today: 6 cards) and all 6 unusable servers are among them.
- **SC-002**: From any page with the shell visible and no blocking dialog open, at 1440 px and 390 px, any attention item's fix is reached in at most two activations (attention count, then the fix), including the sixth item.
- **SC-003**: On every surface that prints `health.status`, 100% of the eight known values are printed with their Spec 109 label; unknown and missing values are tested separately. Chrome contains 0 emoji.
- **SC-004**: For every fixture data set with no profile filter, the online count is equal on header, Home and Servers, and the attention item count is equal on header, Home list and Home sidebar badge.
- **SC-005**: At 390 px, on every page this feature changes, including its tabs, panels and dialogs, the document does not scroll sideways, and no container does except the permitted ones (log output, code and JSON blocks, diff views, filter chip rows); every body button is at least 44 px tall.
- **SC-006**: At 1280 px the Activity calls table and the tokens table have no horizontal scrollbar and no clipped content.
- **SC-007**: On an ordinary load the first Tab opens no dialog; every inventory action is operable by keyboard; 0 links lead to the not-found page.
- **SC-008**: The contrast sweep passes at AA on every changed page in light and dark.
- **SC-009**: For a quarantined server with `definitions_captured` false, the primary button never approves.
- **SC-010**: The existing Playwright sweeps and frontend unit tests pass on every pull request, with no test removed without replacement.
- **SC-011**: Home with the fleet fixture shows the attention list without scrolling at 1440×900 and at 390×844.
- **SC-012**: Every inventory row has a place after the change and a passing test. 0 functions are removed.

## Delivery

Independent pull requests, each mergeable and shippable on its own, in this order. Each starts with its inventory (FR-065), writes failing tests first (vitest under `frontend/tests/unit/`, Playwright under `e2e/web-ui-sweep/`) and attaches the sweep report described in `docs/development/web-ui-verification.md`. Design changes are cross-reviewed against R1 before merge.

| PR | Content | Requirements | Findings |
|---|---|---|---|
| 1 | Tokens, status pill, emoji removal, shell (sidebar order, header, skip link, keyboard, phone overlay, footer disclosure) | FR-001–007, FR-010–015 | A1, A5, A11, A13, A15, A17 |
| 2 | Servers table | FR-020–026 | A2, A12, A14 |
| 3 | Server detail tabs, review primary action, Skills tab and skill card | FR-030–039 | A3, A4, A18 |
| 4 | Home; tool count and routing mode move from header to Home | FR-040–041, FR-011 | A6 |
| 5 | Activity calls view | FR-045–049 | A7, A19 |
| 6 | Tools | FR-055–057 | A9, A16 |
| 7 | Clients and tokens; Usage restyle | FR-060–062 | A8, A21 |
| 8 | Settings, profile editor action row, wizard verify step | FR-050–053 | A10, A20, A22 |

## Assumptions

- The Spec 109 label table stays as it is.
- The topology diagram is kept and collapsed by default. Whether to retire it is a separate decision.
- A function that today is visible at once and after this work is one activation away (menu, disclosure, popover, panel) counts as kept.
- The mockups stay the visual reference for default layouts only.
- The macOS app and tray are not changed.

## Out of Scope

- Backend, REST, SSE, CLI, macOS app and tray changes.
- Removing any function.
- Redesign of Profiles, Secrets, Usage, Add server, the setup wizard and the scan report beyond the fixes named above.
- Server-edition pages (`/my/*`, `/admin/*`) beyond fitting the shell.
- New features, new themes, localisation.

## Commit Message Conventions *(mandatory)*

When committing changes for this feature, follow these guidelines:

### Issue References
- Use: `Related #[issue-number]` - links the commit to the issue without auto-closing
- Do NOT use: `Fixes #[issue-number]`, `Closes #[issue-number]`, `Resolves #[issue-number]` - these auto-close issues on merge

**Rationale**: Issues should only be closed manually after verification and testing in production, not automatically on merge.

### Co-Authorship
- Do NOT include: `Co-Authored-By: Claude <noreply@anthropic.com>`
- Do NOT include: "Generated with Claude Code"

**Rationale**: Commit authorship should reflect the human contributors, not the AI tools used.

### Example Commit Message
```
feat(web): servers table sorted problems-first

Related #[issue-number]

Replace the server card grid with a fixed-height table that lists
unusable servers first. Every card function keeps a place.

## Changes
- Servers.vue: table, filter chips, More filters
- utils/serverReason.ts: one-line reason
- specs/134-webui-simplify/function-inventory.md: Servers rows

## Testing
- vitest: serverReason, server ordering
- Playwright sweep: servers at 1440 px and 390 px, light and dark, keyboard
```

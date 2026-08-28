# macOS Tray UX + Settings-Parity Audit — 2026-08

**Epic**: `ux-audit` · **Task**: `ux-audit-macos-sweep`
**Audited build**: running production tray `com.smartmcpproxy.mcpproxy` **v0.60.0** (pid 14593), against repo `main` at tag `v0.60.0` — no version skew *at the time of the walk*, so every finding was reproducible from source **as audited**.
**Method**: full accessibility-tree walk of the live status-bar menu (`mcpproxy-ui-test` → `list_menu_items`, `read_status_bar`, `check_accessibility`), read-only. No binary swap, no destructive clicks. Settings and window surfaces compared statically: `native/macos/MCPProxy/MCPProxy/**` vs `frontend/src/**`.
**Live proxy state during the walk**: 29 servers (13 connected), 942 tools, 5 needing attention, 2 profiles, 1 idle client.

> **Status — all 16 findings are FIXED on `main`.** This document is the audit record, not an open backlog. [#1055](https://github.com/smart-mcp-proxy/mcpproxy-go/pull/1055) addresses **F1–F16** in the sequence §5 suggests. Read every finding below in the past tense: it describes v0.60.0. Do not re-file from this document — check `native/macos/MCPProxy/` at current `main` first.

Screenshots are deliberately absent: `screenshot_status_bar_menu` returns an all-black PNG on this machine (known Screen-Recording/TCC limitation), so the accessibility tree is the authoritative record. The verbatim dump is in the appendix.

---

## 1. Surface inventory

### 1.1 Tray status-bar menu (as walked)

```
MCPProxy v0.60.0                       (disabled, status dot)
13/29 servers, 942 tools               (disabled)
No calls in the last 24h               (disabled — glance/histogram)
Open Activity…
Clients                                (disabled section header)
  claude-code — 0 calls · seen 23h
Needs Attention (5)                    ▸ 5 rows (click = silent action + navigate)
Servers (29)                           ▸ Add Server… ⌘N + 29 server submenus
    <server> ▸ status · Protocol: … · [Sign in] · Stop|Disable / Start|Enable · Restart · View Logs
Profile: All servers                   ▸ All servers ✓ · research (0 tools) · deploy (0 tools)
Connect Client…
Open MCPProxy...
Settings...                            ⌘,
Open Web UI
Run at Startup                         ✓
Check for Updates
Stop MCPProxy Core
Documentation
About MCPProxy
Quit MCPProxy                          ⌘Q
```

### 1.2 Tray native windows

| Window | Sections |
|---|---|
| Main window (`MainWindow.swift`) | Dashboard · Servers (→ Server Detail: Tools / Logs / Config) · Registries (→ Browse) · Activity Log · Secrets |
| Settings (`SettingsView.swift`) | App · Security · General · Advanced · Raw |
| Modal flows | Add Server · Connect Client · First-run dialog · Update-failure dialog |

### 1.3 Web UI routes (personal edition)

Dashboard · Servers · Server Detail · Repositories · Search · Tools · Activity · Security (+ Scan Report) · Sessions · Secrets · Agent Tokens · Settings (Configuration) · Feedback. (Usage is a Dashboard tab, not a route, in v0.60.0.)

---

## 2. Parity matrix

### 2.1 Feature surfaces

| Surface | Tray | Web UI | Verdict |
|---|---|---|---|
| Dashboard (stats, token savings, agents) | ✅ native | ✅ | **both** |
| Server list + detail (tools / logs / config) | ✅ native | ✅ | **both** |
| Per-server tool approval / quarantine review | ✅ in Server Detail only | ✅ | **inconsistent** — no menu path (F8) |
| Registries / browse & install | ✅ native | ✅ Repositories | **both** (name differs: "Registries" vs "Repositories") |
| Activity log (+ code-exec parent↔child) | ✅ native | ✅ | **both** |
| Secrets (keyring) | ✅ native | ✅ | **both** |
| MCP sessions / clients | ✅ Dashboard + glance | ✅ Sessions | **both** |
| Connect a client (wizard) | ✅ native form | ✅ modal | **both** |
| Add server | ✅ native form | ✅ | **both** |
| Configuration editor | ✅ 5 tabs incl. Raw | ✅ | **inconsistent** — 3 fields missing (F6) |
| **Agent tokens** | ❌ view exists but unreachable | ✅ `/tokens` | **web-only** (F5) |
| **Security: deep scan, scan reports, TPA report** | ❌ | ✅ `/security`, `/security/scans/:id` | **web-only** (F8) |
| **Global tool search / all-tools list** | ❌ | ✅ `/search`, `/tools` | **web-only** (F16) |
| **Send feedback / report an issue** | ❌ (`ProjectLinks.issues` unused) | ✅ `/feedback` | **web-only** (F14) |
| Usage report | ⚠️ savings % on Dashboard | ✅ Dashboard → Usage tab (no standalone route in v0.60.0) | **inconsistent** |
| Core lifecycle (start / stop / restart core) | ✅ | ❌ | **tray-only** (correct) |
| Launch at login / start core on launch | ✅ | ❌ | **tray-only** (correct) |
| App updates (Sparkle) | ✅ | ❌ | **tray-only** (correct) |
| Interface text size | ✅ | ❌ (browser zoom) | **tray-only** (correct) |
| Profile switcher | ✅ menu | ✅ header | **inconsistent** — tray drops server count (F11) |

### 2.2 Settings fields (`SettingsCatalog.swift` = 56 fields vs `frontend/src/views/settings/fields.ts`)

The Swift file's own header says it "mirrors the Web UI 1:1". It does not:

| Key | Web UI | Tray | Note |
|---|---|---|---|
| `security.deep_scan.enabled` | ✅ Security | ❌ | Docker scanner opt-in — unreachable outside the Raw tab |
| `instructions` | ✅ Advanced → "MCP server instructions" (whole accordion) | ❌ | Web UI also prefills the live built-in default as placeholder |
| `code_execution_max_parallel` | ✅ Advanced → Code execution | ❌ | Spec 096 field, added web-side only |
| `server_edition.*` (3) | ✅ (server edition) | ❌ | **N/A** — server edition is not shipped in the personal tray |
| everything else (53 keys) | ✅ | ✅ | keys, labels, ranges, danger dialogs all match |

Cosmetic drift inside matching fields (F13): tray drops the `512m` / `1.0` / `docker.io` placeholders on the Docker fields, drops `step: 0.1` on `entropy_threshold` and `step: 0.5` on `oauth_expiry_warning_hours`, and its telemetry help text predates the Web UI's "sends a single anonymous opt-out signal" wording.

Tray-only settings — all legitimately app-owned: *Launch MCPProxy at login*, *Start MCPProxy Core when the app opens*, *Interface text size*, About/version block.

### 2.3 Per-server actions

| Action | Tray menu | Tray Server Detail | Web UI | Verdict |
|---|---|---|---|---|
| Enable / disable | ✅ (two different verb pairs — F7) | ✅ | ✅ | **inconsistent** |
| Restart | ✅ | ✅ | ✅ | both |
| OAuth sign-in | ✅ | ✅ | ✅ | both |
| View logs | ✅ | ✅ | ✅ | both |
| Edit config JSON | ❌ | ✅ | ✅ | both (not in menu) |
| Approve server / quarantine | ❌ | ✅ | ✅ | **inconsistent** (F8) |
| Approve tool / view tool diff | ❌ | ✅ | ✅ | **inconsistent** (F8) |
| Convert value → keyring secret | ❌ | ✅ | ✅ | both |
| Delete server | ❌ | ❌ | ✅ | **web-only** |
| Deep scan (single / all) | ❌ | ❌ | ✅ | **web-only** |
| Failure feedback on action | ❌ silent (F3) | ✅ inline error | ✅ toast | **inconsistent** |

---

## 3. House UX preferences — compliance

Checked against the stated tray/activity preferences.

| # | Preference | Status |
|---|---|---|
| 1 | Group consecutive same-tool calls | ✅ `GlanceSection.recentRuns` emits `label ×N` |
| 2 | Use `metadata.intent.reason` as the context line | ✅ `GlanceSection` reason budget + tooltip |
| 3 | No success checkmarks — mark only failures | ✅ transparent placeholder glyph keeps titles aligned |
| 4 | Activity at top level, above recent calls | ✅ "Open Activity…" is row 4 |
| 5 | Idle clients, not live-TCP counting | ✅ "claude-code — 0 calls · seen 23h" |
| 6 | Add Server / Add Client = native form | ✅ `AddServerView`, `ConnectClientView` |
| 7 | Split runs on status change | ✅ status participates in run identity |
| 8 | Histogram must match the call log | ✅ same `recentRuns` source; not exercisable live (0 calls in 24h) |
| 9 | code_execution sub-calls first-class, parent↔child navigable | ✅ in Activity view; ⚠️ **partially** in the menu — glance rows carry a session id but open the log unfiltered (F10) |

The glance block is the strongest part of the tray. The findings below are concentrated everywhere else.

---

## 4. Findings (ranked)

**P0 — none.** Nothing in the tray destroys data, misreports security state, or blocks a core workflow.

### P1

**F1 · The menu-bar icon never shows server health.** `Menu/TrayIcon.swift` — the SwiftUI view carrying the Spec 044 red/orange severity badge — is **never instantiated**; `grep -rn TrayIcon` finds only its own declaration. The live icon comes from `MCPProxyApp.updateStatusIcon()`, which badges only *core stopped* (`⏹`) and *core error* (`⚠`). With 5 servers needing attention the menu bar looked identical to all-healthy, and `AppState.healthLevel` / `worstDiagnosticSeverity` have no other consumer — both are dead. This defeats the tray's entire "glance" premise: attention state is only discoverable by opening the menu.
*Fix*: draw the severity dot in `updateStatusIcon()` (compose over the template image), or adopt `TrayIcon` as the `MenuBarExtra` label and delete the AppKit path. Add a test asserting the icon changes when `serversNeedingAttention` is non-empty. **Effort: S (½ day).**

**F2 · The status-bar item is unlabelled for VoiceOver.** `read_status_bar` returns `title: ""` with no `AXDescription` and no tooltip — a screen-reader user hears "status menu". `updateStatusIcon()` loads `icon-mono-44.png` via `NSImage(contentsOfFile:)`, which never sets `accessibilityDescription` (only the unreachable SF-symbol fallback does), and nothing sets `button.toolTip` or an accessibility label. The state glyphs `⏹`/`⚠` are also glyph-only with no text alternative.
*Fix*: on every `updateStatusIcon()` pass set `base.accessibilityDescription`, `button.toolTip = appState.statusSummary`, and `button.setAccessibilityLabel("MCPProxy — <summary>")`. **Effort: XS (< 1 h).** Highest value-per-line change in this audit.

**F3 · Tray server actions fail silently.** `enableServer`, `disableServer`, `restartServer`, `loginServer` and `handleAttentionAction` all call the API as `try? await …` and discard the error (`MCPProxyApp.swift:1553-1581`). A restart that 500s is indistinguishable from one that worked — the menu simply closes. The Web UI raises a toast (`systemStore.addToast`) and Server Detail shows an inline error, so the tray is the only surface that lies by omission.
*Fix*: route failures through the existing `NotificationService` (or an `NSAlert` for user-initiated actions); one shared `perform(_:)` helper covers all five call sites. **Effort: M (1 day).**

**F4 · "Needs Attention" rows mutate state on a click that reads as navigation.** `handleAttentionAction` runs `health.action` before navigating: clicking `demo-filesystem — failed to connect: server…` **restarts** the server; an `enable`-actioned row **enables** a server; a `login` row opens a browser for OAuth. None of that is in the row's label, none of it is confirmable, and (per F3) none of it reports failure. Enabling a server is a security-relevant state change and should never be an unlabelled side effect of what looks like a disclosure row.
*Fix*: either name the action in the row (`cloudflare-graphql — Sign in`) or make the row navigate-only and expose the action in a submenu, matching the explicit verbs already used under `Servers ▸`. **Effort: S (½ day).**

**F5 · Agent tokens are unreachable from the tray.** `Views/TokensView.swift` is a complete 444-line create/list/revoke UI that nothing instantiates — no `SidebarItem`, no menu item, no window. Agent tokens are a headline security feature reachable from the Web UI (`/tokens`) and CLI (`mcpproxy token`) but not the app the user actually has open.
*Fix*: add `case tokens = "Agent Tokens"` to `SidebarItem` (icon `key.horizontal`) and wire the switch in `MainWindow`. The view is already written and API-complete. **Effort: XS (< 1 h).**

### P2

**F6 · Settings drift with no parity gate.** Three non-deprecated Web UI settings are missing from the tray catalogue — `security.deep_scan.enabled`, `instructions`, `code_execution_max_parallel` — while `SettingsCatalog.swift` claims to mirror `fields.ts` 1:1 and `SettingsView.swift` claims "every backend setting is edited here". They are only reachable via the Raw tab, which is exactly the escape hatch the catalogue exists to avoid. The only test touching the catalogue (`SettingsDiscoveryFieldsTests`) checks duration validation, not key coverage — nothing prevents the next Web-UI-first field from drifting too.
*Fix*: add the three fields, then add a parity test — a small CI step that extracts `key:` literals from `fields.ts` (minus `server_edition.*`) and diffs them against a Swift-side dump of `SettingsCatalog` keys. **Effort: M (1 day incl. the gate).**

**F7 · One operation, two verb pairs, contradicting the status line.** `MCPProxyApp.swift:1152-1163` labels the same `enabled` flag `Stop`/`Start` for stdio servers and `Disable`/`Enable` for everything else. The result inside a single submenu: status line **"Disabled"**, action **"Start"** — two different mental models (transient process control vs. persistent admin state) for one config write. The Web UI says Enable/Disable everywhere; the tray's own Server Detail says Enable/Disable too.
*Fix*: use `Enable`/`Disable` unconditionally; if transient process control is genuinely wanted for stdio, it needs its own API, not a relabel. **Effort: XS (< 1 h).**

**F8 · Quarantine and the whole security surface have no tray path.** Three servers were quarantined during the walk. Their submenus offered only `Disable · Restart · View Logs` — no "Review", no "Approve". The `Needs Attention` row for a quarantined server has no `health.action`, so it silently falls through to plain navigation (the one case where the F4 side effect is absent, by luck rather than design). Deep scan, scan reports and TPA findings have no native home at all, and the setting that turns deep scan on is the one missing from the tray catalogue (F6).
*Fix (staged)*: (a) add `Review quarantine…` to the per-server submenu, deep-linking to Server Detail → Tools; (b) add a `Security` sidebar item wrapping scans/reports. **Effort: S for (a), L for (b).**

**F9 · Duplicated and ambiguous startup / open controls.** *Run at Startup* (menu) and *Launch MCPProxy at login* (Settings → App) are the same `AutoStartService` setting under two names. *Start MCPProxy Core when the app opens* lives only in Settings, one row from the menu's *Start/Stop MCPProxy Core* which means something different (right now, not at launch) — a genuinely confusable pair with no cross-reference. And *Open MCPProxy...* vs *Open Web UI* never says one is a native window and the other a browser tab.
*Fix*: one label for the login item ("Launch at Login" both places); rename to *Open MCPProxy Window* / *Open Web UI in Browser*; add a one-line footnote linking the two core-start controls. **Effort: XS.**

**F10 · Glance rows lose their filter on click.** `openActivityForSession` receives a session id in `representedObject` and throws it away — "the native log currently opens unfiltered", per its own comment. `ActivityView` already supports `parent_id` filtering and a filter chip, so the plumbing exists; only the hand-off is missing. This is the one place preference #9 (parent↔child navigable) is not honoured.
*Fix*: extend `.switchToSidebarTab` to carry a filter payload and seed `ActivityView`'s session filter. **Effort: S.**

### P3

**F11 · Empty profiles are offered silently.** The menu listed `research (0 tools)` and `deploy (0 tools)`; both reference servers (`github`, `gitlab`) that are not in the config, so `EffectiveServers` resolves to nothing. Switching to either would scope every agent to zero servers with no warning. The Web UI's `ProfileSwitcher` at least shows `N servers · M tools`; the tray shows only the tool count.
*Fix*: mirror the Web UI's "N servers · M tools", and flag a profile whose effective set is empty ("research — no servers"). **Effort: S.**

**F12 · Raw protocol strings leak into the menu.** `Protocol: streamable-http` sits beside `Protocol: http` and `Protocol: sse` for what users experience as one transport family. Display-normalise (`HTTP (streamable)` / `HTTP (SSE)` / `stdio`). **Effort: XS.**

**F13 · Copy and control drift inside matching settings fields.** Missing placeholders (`512m`, `1.0`, `docker.io`), missing `step` on two numeric fields, telemetry help text one revision behind the Web UI. Folds naturally into F6's parity work. **Effort: XS.**

**F14 · Dead links and no feedback path.** `ProjectLinks.issues` and `.discussions` are declared and never used; `openConfigFile()` is an unreferenced `@objc` selector. Meanwhile the Web UI has a `/feedback` page and the CLI has `mcpproxy feedback`, and the tray — the surface a Homebrew user actually sees — offers no way to report anything.
*Fix*: add *Report an Issue* (or *Send Feedback*) near *Documentation* using the already-declared link; either wire or delete `openConfigFile`. **Effort: XS.**

**F15 · The Servers submenu does not scale.** 29 entries, flat and alphabetical, with 13 disabled and 3 quarantined interleaved among the connected ones; only the dot colour distinguishes them, and the submenu is taller than most screens allow comfortably.
*Fix*: sort or group by state (needs attention → connected → disabled), or fold disabled servers into a `Disabled (13) ▸` sub-submenu. **Effort: S.**

**F16 · No global tool search in the tray.** `/search` and `/tools` — the BM25 discovery surface that is the product's headline feature — have no native equivalent; per-server tools are visible only inside Server Detail. A tray-first user cannot answer "which of my 942 tools does X?" without opening a browser.
*Fix*: a `Tools` sidebar item over `/api/v1/index/search` (note: that endpoint returns bare tool names, not `server:tool`). **Effort: M–L.**

---

## 5. Suggested sequencing

1. **One afternoon, high visibility**: F2, F5, F7, F9, F12, F14 — all XS, all user-visible, no architecture involved.
2. **Next**: F1 + F3 + F4 — the three that decide whether the tray can be trusted at a glance.
3. **Then**: F6 with its parity gate (stops the drift recurring), F10, F11, F15.
4. **Roadmap-sized**: F8(b) security surface, F16 tool search.

## 6. Resolution

**All 16 findings (F1–F16) were fixed by [#1055](https://github.com/smart-mcp-proxy/mcpproxy-go/pull/1055)** (`fix(tray): close the 16 findings of the macOS tray UX + settings-parity audit`), with [#1056](https://github.com/smart-mcp-proxy/mcpproxy-go/pull/1056) as a catalogue follow-up, in the order §5 suggests. Every finding above therefore describes v0.60.0 in the past tense. Nothing here is an open backlog item; check `native/macos/MCPProxy/` at current `main` before re-filing any of it.

Two fixes are worth recording because the obvious implementation is wrong:

- **F1** — the severity dot is drawn by `updateStatusIcon()` as an attributed-title glyph, *not* as a composite image. The status-bar image must stay `isTemplate` so it follows the menu bar, and a template image is re-rendered monochrome, which would erase a coloured badge composited into it.
- **F6** — the missing keys (`security.deep_scan.enabled`, `instructions`, `code_execution_max_parallel`) were added to `SettingsCatalog.swift` *and* backed by a parity gate, so the next Web-UI-first field cannot drift in silently.

The §1.1 menu capture is a v0.60.0 snapshot and three of its labels have since changed as part of F9:
`Open MCPProxy...` → **Open MCPProxy Window**, `Open Web UI` → **Open Web UI in Browser**, and
`Run at Startup` → **Launch at Login** (`MCPProxyApp.swift`).

Two claims in §1 were also wrong at the time of the walk, independent of the fixes, and are corrected in
place above: the Settings tab is named **Raw**, not "Raw JSON" (`Views/SettingsView.swift` at `v0.60.0`),
and the Web UI had **no `/usage` route** in v0.60.0 — Usage was a Dashboard tab, and `/usage` only became
a real route later, in #1044. The parity matrix's "✅ `/usage`" therefore overstated the Web UI side of
that row.

**Re-walked 2026-08-26 — and the findings hold, because the installed tray is still v0.60.0.**
`mcpproxy-ui-test` was initially unusable (macOS Accessibility revoked; `check_accessibility` →
`trusted: false`); once granted, a fresh `list_menu_items` walk reproduced this audit almost verbatim:

- Header still `MCPProxy v0.60.0`, with `Update 0.61.0 — ready to restart?` sitting in the menu — the
  #1055 fixes are downloaded but **not running**, so this walk cannot verify them.
- **F2** — `read_status_bar` still returns `title: ""` with no description and no tooltip.
- **F5** — no Agent Tokens entry anywhere in the menu.
- **F7** — `ElevenLabs` (stdio) offers **Stop** while `cloudflare-graphql` (http) offers **Disable**; and
  `memory` shows status **Disabled** above the action **Start**, exactly the contradiction F7 names.
- **F8** — all three quarantined servers (`everything`, `io.github.GreatQuestion/mcp`, `playwright`)
  offer only Disable/Stop · Restart · View Logs. No Review, no Approve.
- **F11** — `research (0 tools)` and `deploy (0 tools)` are still offered.
- **F12** — `Protocol: streamable-http` sits beside `Protocol: http` and `Protocol: sse`.
- **F15** — 29 flat entries, disabled and quarantined interleaved.
- **F16** — no tool search.
- Menu labels are still `Open MCPProxy...` / `Open Web UI` / `Run at Startup`, i.e. the pre-#1055 strings
  in §1.1 above.
- One drift from the original walk: the Clients block now reads `No clients in the last 24h` rather than
  listing an idle client — the empty state H5 exists to avoid.

So: the audit is accurate as a description of v0.60.0, and #1055's fixes remain **unverified on a running
tray**. Verifying them needs the pending 0.61.0 restart (or the dev bundle-swap procedure), which this
pass deliberately did not do — restarting the tray would have killed the core it manages.

---

## Appendix A — verbatim menu tree

Captured 2026-08-25 via `list_menu_items(bundle_id: "com.smartmcpproxy.mcpproxy")`. Server submenu shape, one representative of each state:

```
ElevenLabs               ▸ Connected (27 tools) · Protocol: stdio           · Stop    · Restart · View Logs
cloudflare-graphql       ▸ Sign-in required     · Protocol: http            · Sign in · Disable · Restart · View Logs
cloudflare-observability ▸ Disabled             · Protocol: http            · Enable  · Restart · View Logs
defillama2               ▸ Disabled             · Protocol: stdio           · Start   · Restart · View Logs
demo-filesystem          ▸ failed to connect…   · Protocol: stdio           · Stop    · Restart · View Logs
everything               ▸ Quarantined for review · Protocol: streamable-http · Disable · Restart · View Logs
```

Note the four rows in the middle: identical admin state, four different verbs (F7), and no review action on the quarantined one (F8).

## Appendix B — audit tooling notes

- `check_accessibility` → granted; the accessibility tree is complete and reliable.
- `screenshot_status_bar_menu` → all-black PNG (Screen-Recording/TCC). Use `list_menu_items` as the authoritative check for tray work.
- `read_status_bar` defaults to bundle id `com.smartmcpproxy.mcpproxy.dev`; the production tray is `com.smartmcpproxy.mcpproxy` and must be passed explicitly.

---
title: "macOS Tray Development"
sidebar_label: "macOS Tray"
description: "Build, replace, and verify the Swift macOS tray app in native/macos/."
---

# macOS Tray App development (`native/macos/`)

## Building the Tray App
```bash
cd native/macos/MCPProxy
SDK=$(xcrun --sdk macosx --show-sdk-path)
swiftc -target arm64-apple-macosx13.0 -sdk "$SDK" -module-name MCPProxy -emit-executable -O \
  -o /tmp/MCPProxy-new \
  $(find MCPProxy -name "*.swift" -not -path "*/Tests/*" | sort | tr '\n' ' ')
# Replace in .app bundle:
cp /tmp/MCPProxy-new /tmp/MCPProxy.app/Contents/MacOS/MCPProxy
```

## Building the UI Test Tool
```bash
cd native/macos/MCPProxyUITest
SDK=$(xcrun --sdk macosx --show-sdk-path)
swiftc -target arm64-apple-macosx13.0 -sdk "$SDK" -O -o /tmp/mcpproxy-ui-test Sources/main.swift
```

## Testing with mcpproxy-ui-test (MCP Server)

The `mcpproxy-ui-test` MCP server provides 7 tools for automated UI verification:

| Tool | Description |
|------|-------------|
| `check_accessibility` | Verify Accessibility API permissions |
| `list_running_apps` | List running macOS apps with bundle IDs |
| `list_menu_items` | Read status bar menu tree |
| `click_menu_item` | Click menu items by path |
| `read_status_bar` | Read status bar item info |
| `screenshot_window` | Capture app window or full screen (CGWindowListCreateImage) |
| `screenshot_status_bar_menu` | Open tray menu, capture screenshot, close menu |

**After every macOS tray code change, verify by:**
1. Build the tray binary (see above)
2. Replace in `/tmp/MCPProxy.app/Contents/MacOS/MCPProxy` and restart
3. Use `screenshot_window` to capture the window and visually verify
4. Use `click_menu_item` + `list_menu_items` to verify tray menu behavior
5. Use `screenshot_status_bar_menu` for tray menu visual verification

## Verification checklist (Spec 109 navigation)

After a change to `MainWindow.swift`, `ClientsView.swift`, `TokensView.swift` or the tray menu, check with `screenshot_window` and `list_menu_items`:

1. **Sidebar.** Sections read Home; Connect (Clients, Profiles, Servers, Tools); Protect (Review Queue, Secrets); Monitor (Activity). Home shows the needs-attention count and Review Queue the review count when they are above zero.
2. **Toolbar "+".** The `toolbar-add-menu` button offers Server, Client, Token and Profile. Server opens the Add Server sheet on Catalog, Client opens the Connect sheet, Token opens Clients, Agent Tokens with the create sheet, Profile opens the Profiles editor on a new profile. Try each from a section other than its own, and once from a window that was just opened.
3. **Clients.** Three tabs: Clients, Endpoint & Mode, Agent Tokens.
4. **Review Queue** opens its own view.
5. **Tray items** via `list_menu_items`: "Needs Attention (N)", "Review Queue… (N)", "Connect Client…"; "Open Activity…" lands on Activity.
6. **Shortcuts.** ⌘1 to ⌘8 follow the visual order (Home, Clients, Profiles, Servers, Tools, Review Queue, Secrets, Activity). Adding Profiles in third place **shifted** the shortcuts after it: Servers is now ⌘4 (it was ⌘3), Tools ⌘5, Review Queue ⌘6, Secrets ⌘7 and Activity ⌘8. The accessibility ids are `sidebar-<Name>` (for example `sidebar-Activity`), `sidebar-badge-<Name>`, `toolbar-add-menu` and `toolbar-add-<Server|Client|Token|Profile>` (capitalised, as 109-i named them).

## Verification checklist (Spec 108 profiles & clients)

After a change under `Views/Profiles*`, `Views/Client*`, `Views/AccessExplainer*`, `Views/TokensView.swift`, `Views/AnonymousProfileSection.swift`, `Menu/TrayClientsMenu.swift` or the profile/client models, run a **scratch core** (a high port and a scratch data directory and `HOME`, never the core your own tray manages) with two profiles, for example `work-readonly` and `work-full`, and one client wired with a client credential. Point the dev build at that core, then check with `screenshot_window`, `list_menu_items`, `click_menu_item` and `check_accessibility`:

1. **Profiles (⌘3).** The "All servers" card comes first, then one card per profile with its tier, tool counts, "Used by" and calls in 24 h. The Tools, Activity, Clients and Tokens links open the destination filtered by that profile, and the ✕ on the chip clears it. Toolbar "+ > Profile" opens the editor on a new profile.
2. **Editor.** Create `work-ro-mac` (servers, max tier Read, a title). A write tool reads "Above tier cap" and an unannotated one "Unannotated — hidden". Classify an unannotated tool as Read and Save: the row becomes visible. Classify an annotated tool: the stale marker "classification ignored — tool is now annotated" appears with a Remove button. "Try it" with an unsaved edit shows "Hidden by profile: N" and leaves the stored profile unchanged. A 400 lands on its field, and a 409 binding guard shows the guard view with two fixes that only open Settings.
3. **Rename and delete** list what they move (clients with their mode, tokens, profiles that list it, anonymous callers) and require a target when anything is listed.
4. **Clients.** A row shows `Profile · locked` and a credential badge. The detail has the profile Picker, the Locked toggle, credential state, "Blocked (24h)", Rotate, Finalize rotation, Forget and Explain access. A client with no active client credential has no working Picker; it shows "Upgrade to client credential…" or "Reconnect…". Changing the Picker from a terminal (`PUT /clients/<id>/binding`) updates the row and the tray within a second (SSE `client.binding_changed`).
5. **Sheets.** "Move Clients…" shows `skipped` clients. "Upgrade Admin-Key Clients…" shows the combined preview; with a named profile and authentication off the guard disables Apply, and "Require authentication…" opens Settings → Security scrolled to and highlighting `require_mcp_auth`. "Other Client…" shows the credential once with Copy; reopening shows nothing.
6. **Connect sheet.** With a profile chosen the lock is on, the preview shows `Credential: mcp_cli_•••• (client credential)`, and the notice "This client can no longer add, change or restart servers…" shows unless the profile sets `management_tools`. There is no API-key notice. The result line reads `Credential: mcp_cli_•••• (token client-<id>, profile <name>, locked)`.
7. **Tray.** `list_menu_items` has a "Clients" submenu and **no** item starting "Profile:". Rows read `name — profile 🔒`, the current profile is checked, Lock is disabled on All servers, and a client without a credential offers only "Upgrade to client credential…".
8. **Tokens.** Create Token asks for a Profile first and an Expiry (default 30 days); the legacy servers and permissions sit under "Legacy scope (advanced)". Legacy tokens show "Legacy scope" and "Migrate to a profile"; client-credential rows have no Revoke. The Profile and token-name filters send `GET /tokens?profile=&token=`.
9. **Settings → Security → Anonymous callers** saves `anonymous_profile`; a refused change shows the guard view, and a fix button preselects the picker without saving.
10. **Scoped views.** Tools "View as Client… / Profile…" greys rows the subject cannot use, with the reason and a "Why?" button that opens the explainer. Activity shows a Caller column and Profile, Client and Token pickers. Home filters usage and sessions by the same three and shows Profile and Source columns. Servers filters by profile.
11. **Width and accessibility.** At a 900 pt window nothing is clipped on Profiles, the editor and Clients; run `check_accessibility`. The accessibility ids are listed in the plan for 108-k (K22): `profile-card-<name>`, `profile-editor`, `profile-editor-save`, `client-profile-picker-<id>`, `client-lock-toggle-<id>`, `clients-warnings-banner`, `access-explainer`, `guard-refusal`, `settings-anonymous-profile`, `connect-profile-picker`, `connect-lock-toggle`, `connect-mgmt-notice`.

**Driving the window without screenshots.** `screenshot_window` needs Screen Recording; when it fails (`Failed to capture window`), drive and read the window through the accessibility tree instead (`AXPress`, `AXValue`, `AXFocused` on the `profile-*`, `client-*`, `token-*`, `connect-*` ids above), keystroke into focused fields with System Events, and check a 900 pt window by listing elements whose frame leaves the window. Navigation shortcuts: ⌘1 Home, ⌘2 Clients, ⌘3 Profiles, ⌘4 Servers, ⌘5 Tools. A tray attached to an already-running core (`MCPPROXY_TRAY_SKIP_CORE=1`) needs the core's `MCPPROXY_SOCKET_PATH`; the SSE stream authenticates with the key it reads from `/api/v1/info`.

**MCP config** (in Claude Code settings or `~/.claude/settings.json`):
```json
{
  "mcpServers": {
    "mcpproxy-ui-test": {
      "command": "/tmp/mcpproxy-ui-test",
      "args": ["--bundle-id", "com.smartmcpproxy.mcpproxy.dev"]
    }
  }
}
```

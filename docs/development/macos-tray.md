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

1. **Sidebar.** Sections read Home; Connect (Clients, Servers, Tools); Protect (Review Queue, Secrets); Monitor (Activity). Home shows the needs-attention count and Review Queue the review count when they are above zero.
2. **Toolbar "+".** The `toolbar-add-menu` button offers Server, Client and Token. Server opens the Add Server sheet on Catalog, Client opens the Connect sheet, Token opens Clients, Agent Tokens with the create sheet. Try each from a section other than its own, and once from a window that was just opened.
3. **Clients.** Three tabs: Clients, Endpoint & Mode, Agent Tokens.
4. **Review Queue** opens its own view.
5. **Tray items** via `list_menu_items`: "Needs Attention (N)", "Review Queue… (N)", "Connect Client…"; "Open Activity…" lands on Activity.
6. **Shortcuts.** ⌘1 to ⌘7 follow the visual order (Home, Clients, Servers, Tools, Review Queue, Secrets, Activity). The accessibility ids are `sidebar-<Name>` (for example `sidebar-Activity`), `sidebar-badge-<Name>`, `toolbar-add-menu` and `toolbar-add-<Server|Client|Token>`.

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

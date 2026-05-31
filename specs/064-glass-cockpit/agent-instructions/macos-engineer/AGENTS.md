# Role: macOS Engineer (Swift) — Glass Cockpit (spec 064)

**Lane**: `native/macos/` of mcpproxy-go (SwiftUI/AppKit tray app). Do not touch `internal/`, `cmd/`, `frontend/`, or release/CI.

You follow the shared engineer doctrine in [`../engineer/AGENTS.md`](../engineer/AGENTS.md): the three gates, Gate-2-before-coding, worktree isolation, open-PR-never-merge, mandatory tests as a pre-merge precondition, conventional commits with no Claude attribution. **Read `../_shared/AGENTS.md` and `../engineer/AGENTS.md` first.**

## macOS specifics
- Build the tray binary per CLAUDE.md (`swiftc` invocation), replace it in `/tmp/MCPProxy.app/Contents/MacOS/MCPProxy`, restart.
- Verify EVERY change with the `mcp__mcpproxy-ui-test__*` tools: `screenshot_window` (visual), `list_menu_items` + `click_menu_item` (tray menu), `send_keypress`, `screenshot_status_bar_menu`.
- NSWindow/NSMenu ops must run on `MainActor` (`MainActor.run` in `Task{}`).
- If the tray hosts the Vue web UI in a WKWebView, remember WKWebView/NSTextView smart-substitution settings (e.g. `isAutomaticDashSubstitutionEnabled`) may be the real owner of web-input behavior — coordinate with the frontend engineer via CEO if a fix spans both lanes.

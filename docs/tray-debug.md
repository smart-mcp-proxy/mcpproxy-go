# Tray Debugging Guide

This guide explains how to control the mcpproxy tray during development and automated testing using environment variables. The variables below let you attach the tray to a pre-launched core, skip automatic OAuth helpers, and keep instrumentation deterministic.

## Quick Reference

| Variable | Scope | Default | Purpose |
|----------|-------|---------|---------|
| `MCPPROXY_TRAY_SKIP_CORE` | Tray | unset | Prevents the tray from launching the core binary. |
| `MCPPROXY_CORE_URL` | Tray | `http://localhost:8080` | Overrides the core API endpoint the tray connects to. |
| `MCPPROXY_DISABLE_OAUTH` | Core | unset | Disables OAuth popups and tray-driven login prompts. |

## Use Cases

### Debugging the Core and Tray Separately

When you want to attach two debuggers (one for the core binary, another for the tray) or restart the core without bouncing the tray:

```bash
# terminal 1: start the core with verbose logging
MCPPROXY_DISABLE_OAUTH=true \
go run ./cmd/mcpproxy serve --listen :8085 --tray=false --log-level=debug

# terminal 2: build + run the tray without auto-spawning the core
MCPPROXY_TRAY_SKIP_CORE=1 \
MCPPROXY_CORE_URL=http://localhost:8085 \
go run ./cmd/mcpproxy-tray
```

**What happens**
- The tray icon appears immediately and connects to `:8085` once the core is ready.
- Because `MCPPROXY_TRAY_SKIP_CORE` is set, the tray never forks a new `mcpproxy` process. This lets you rebuild or restart the core freely.
- `MCPPROXY_DISABLE_OAUTH=true` ensures no OAuth browser windows are spawned during debugging.

### VS Code Compound Debugging

Add the following launch configurations to `.vscode/launch.json` (already included in the repo’s example setup):

```jsonc
{
  "name": "Debug mcpproxy (.tree/next)",
  "type": "go",
  "request": "launch",
  "mode": "exec",
  "program": "${workspaceFolder}/.tree/next/mcpproxy",
  "args": ["serve", "--listen", ":8085", "--tray", "false"],
  "env": {
    "CGO_ENABLED": "1",
    "MCPPROXY_DISABLE_OAUTH": "true"
  }
},
{
  "name": "Debug mcpproxy-tray (.tree/next)",
  "type": "go",
  "request": "launch",
  "mode": "exec",
  "program": "${workspaceFolder}/.tree/next/mcpproxy-tray",
  "env": {
    "CGO_ENABLED": "1",
    "MCPPROXY_TRAY_SKIP_CORE": "1",
    "MCPPROXY_CORE_URL": "http://localhost:8085"
  }
}
```

With a compound configuration that launches both entries, pressing F5 will:
1. Start the core under the debugger without tray UI.
2. Attach the tray to the already debugging core.

### Automated UI Testing

For Playwright, scripted tray checks, or MCP automation harnesses:

```bash
# Start the core in headless mode
MCPPROXY_DISABLE_OAUTH=true \
MCPPROXY_CORE_URL=http://localhost:18080 \
mcpproxy serve --listen :18080 --tray=false &

# Launch the tray with instrumentation enabled
MCPPROXY_TRAY_SKIP_CORE=true \
MCPPROXY_CORE_URL=http://localhost:18080 \
MCPPROXY_TRAY_INSPECT_ADDR=127.0.0.1:8765 \
go run -tags traydebug ./cmd/mcpproxy-tray
```

The `traydebug` build tag exposes an HTTP inspector (see `/state`, `/action`) so automated tests can query the tray menu without needing Accessibility permissions.

## Tips & Troubleshooting

- If the tray still spawns a core instance, confirm `MCPPROXY_TRAY_SKIP_CORE` is set to `1` or `true` in the tray process environment.
- The core URL must include the protocol (e.g. `http://`); otherwise the Go HTTP client rejects it.
- Combine `MCPPROXY_DISABLE_OAUTH` with test configs to avoid OAuth popups in CI or when running unit tests.
- When running against non-default ports, update your MCP clients (Cursor, VS Code, etc.) to use the same port.

### Resolving Port Conflicts

If another process already uses the configured listen port, the tray now surfaces a **Resolve port conflict** sub-menu directly beneath the status indicator. From there you can:

- Retry the existing port once you have freed it.
- Automatically switch to the next available port (the tray persists the new value and restarts the core for you).
- Copy the MCP connection URL to the clipboard for quick use in clients.
- Jump straight to the configuration directory if you prefer manual edits.

For scripted verification on macOS you can drive the new menu via `osascript`:

```applescript
osascript <<'EOF'
tell application "System Events"
  tell process "mcpproxy-tray"
    click menu bar item 1 of menu bar 1
    click menu item "Resolve port conflict" of menu 1 of menu bar item 1 of menu bar 1
    delay 0.2
    click menu item "Use available port" of menu 1 of menu item "Resolve port conflict" of menu bar item 1 of menu bar 1
  end tell
end tell
EOF
```

Adjust the inner menu titles if you localise the app; the defaults above match the English build.

## Further Reading

- [docs/setup.md](./setup.md) – full installation and configuration walkthrough.
- [MANUAL_TESTING.md](../MANUAL_TESTING.md) – manual smoke scenarios that benefit from the environment flags above.
- [Playwright MCP server README](../.playwright-mcp/README.md) – pattern for automating UI flows; the tray inspector mirrors that approach.

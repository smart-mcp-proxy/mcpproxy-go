# Tray Debugging Guide

This guide explains how to control the mcpproxy tray during development and automated testing using environment variables. The variables below let you attach the tray to a pre-launched core, skip automatic OAuth helpers, and keep instrumentation deterministic.

## Quick Reference

| Variable | Scope | Default | Purpose |
|----------|-------|---------|---------|
| `MCPPROXY_HOME` | macOS tray | `~/.mcpproxy` | Relocates the whole instance root: socket, config, database, autostart sidecar, lifecycle journal. |
| `MCPPROXY_SOCKET_PATH` | macOS tray | `<root>/mcpproxy.sock` | Points the tray at one specific core's socket, wherever the root is. |
| `MCPPROXY_TRAY_SKIP_CORE` | Tray | unset | Prevents the tray from launching the core binary. |
| `MCPPROXY_CORE_URL` | Tray | `http://localhost:8080` | Overrides the core API endpoint the tray connects to. |
| `MCPPROXY_DISABLE_OAUTH` | Core | unset | Disables OAuth popups and tray-driven login prompts. |

## Running a Second (Dev/QA) macOS Tray Instance

The native macOS tray resolves its paths through `homeDirectoryForCurrentUser`, which **ignores `$HOME`**. Without an override, a tray built from a branch and run out of a scratch bundle still reads and writes the real `~/.mcpproxy` — which means QA runs cannot go in parallel (they contend for one socket) and the autostart sidecar overwrites the user's real login-item state on every launch.

`MCPPROXY_HOME` moves the whole instance in one step. It is unset in normal use, and with it unset every path resolves exactly where it always did.

```bash
# Keep the root SHORT: sockaddr_un caps the socket path at 103 bytes, and a
# deep scratch directory silently fails to bind. The tray logs a warning if
# the resolved path is over the limit.
export MCPPROXY_HOME=/tmp/mcpproxy-qa
/path/to/dev/mcpproxy.app/Contents/MacOS/MCPProxy
```

With it set:

| File | Default | With `MCPPROXY_HOME=/tmp/mcpproxy-qa` |
|------|---------|----------------------------------------|
| Core socket | `~/.mcpproxy/mcpproxy.sock` | `/tmp/mcpproxy-qa/mcpproxy.sock` |
| Config opened by **Open Config File** | `~/.mcpproxy/mcp_config.json` | `/tmp/mcpproxy-qa/mcp_config.json` |
| Autostart sidecar | `~/.mcpproxy/tray-autostart.json` | `/tmp/mcpproxy-qa/tray-autostart.json` |
| Lifecycle journal | `~/.mcpproxy/tray-lifecycle.jsonl` | `/tmp/mcpproxy-qa/tray-lifecycle.jsonl` |
| Spawned core | `mcpproxy serve` | `mcpproxy serve --data-dir /tmp/mcpproxy-qa --config /tmp/mcpproxy-qa/mcp_config.json` |

Notes and limits:

- `MCPPROXY_SOCKET_PATH` still wins over the root. Use it to attach to a core somebody else started; use `MCPPROXY_HOME` to own a whole instance.
- **Not** relocated: `~/Library/Logs/mcpproxy` (the core's log directory, which follows the core's own rules) and the app's preferences domain — `cfprefsd` honours neither `$HOME` nor this variable, so give a dev bundle a distinct bundle id if you need separate defaults.
- The first-run dialog is a `UserDefaults` flag, so it lives in the preferences domain rather than the instance root; a fresh bundle id will show it again. Its "Launch at login" checkbox is pre-checked, so clicking through it in a dev instance registers a real login item.

## Attributing a Tray or Core Exit

Every start and stop is recorded in `<instance root>/tray-lifecycle.jsonl`, one JSON object per line, and mirrored to the macOS unified log at Notice level (`log show --predicate 'subsystem == "com.smartmcpproxy.mcpproxy"'`) — Notice because the Info and Debug tiers are purged within hours, which is precisely why an earlier silent exit could not be attributed.

```bash
tail -5 ~/.mcpproxy/tray-lifecycle.jsonl | python3 -m json.tool --json-lines
```

Recorded events: `appLaunched`, `appTerminating`, `signalReceived`, `coreLaunched`, `coreTerminated`, `coreExited`, `updateCheck`. Each carries a free-text reason, the uptime of the tray process, and a pid.

The rules worth knowing when reading one:

- Every shutdown records **who asked**. A shutdown nobody claimed is written as `unattributed (no initiator claimed this shutdown)` rather than as something plausible.
- `SIGTERM`, `SIGINT` and `SIGHUP` are caught, recorded, and then routed through the normal quit path. `SIGKILL` and a jetsam kill cannot be caught by anyone.
- Which is why the **next** launch reports an unaccounted-for previous run: if the last record before a launch is not an `appTerminating`, the new `appLaunched` record says so and names the last thing the dead run did. That marker is the signature of a SIGKILL-class death (jetsam, `pkill`, power loss) or a crash outside the app's handlers.
- The journal is trimmed to its newest 500 records at launch.

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

### Launcher Configuration

The tray launches `mcpproxy serve` when it detects that no core is running. You can steer that subprocess with the following environment variables before starting the tray:

- `MCPPROXY_CORE_URL` – full base URL the tray should connect to (e.g. `http://localhost:8085`). This also controls the health checks.
- `MCPPROXY_CORE_PATH` – custom path to the mcpproxy core binary (defaults to bundled binary or PATH lookup).
- `MCPPROXY_TRAY_LISTEN` / `MCPPROXY_TRAY_PORT` – override the port passed to `--listen` when the tray launches the core (formats accepted: `:8085` or `8085`).
- `MCPPROXY_TRAY_CONFIG_PATH` – absolute path to the `mcp_config.json` the tray should hand to the core via `--config`.
- `MCPPROXY_TRAY_EXTRA_ARGS` – optional additional CLI arguments (whitespace separated) appended after `serve`.
- `MCPPROXY_TRAY_SKIP_CORE` – set to `1` to prevent the tray from launching the core automatically (useful when attaching to an external instance).
- `MCPPROXY_TRAY_CORE_TIMEOUT` – timeout in seconds for core server startup (default: 30).
- `MCPPROXY_TRAY_RETRY_DELAY` – retry delay in milliseconds for core server connection (default: 1000).
- `MCPPROXY_TRAY_STATE_DEBUG` – set to `1` to enable state machine debug logging.

The tray’s status tooltip reflects the active listen address; when you change any of the variables above, restart the tray so it relaunches the core with the new settings.

### Building a DMG with Both Binaries

Use the updated packaging script to bundle the tray and core into a single notarizable DMG:

```bash
GOOS=darwin GOARCH=arm64 go build -o dist/mcpproxy-tray ./cmd/mcpproxy-tray
GOOS=darwin GOARCH=arm64 go build -o dist/mcpproxy ./cmd/mcpproxy
./scripts/create-dmg.sh dist/mcpproxy-tray dist/mcpproxy v1.0.0 arm64
```

The resulting `mcpproxy.app` contains:

- `Contents/MacOS/mcpproxy` – the tray executable.
- `Contents/Resources/bin/mcpproxy` – the CLI core binary that the tray stages at runtime.

When the DMG is mounted the user only needs to drag the app bundle to `/Applications`; the tray will manage the core automatically from that embedded location.

## Further Reading

- [docs/setup.md](./setup.md) – full installation and configuration walkthrough.
- [Playwright MCP server README](../.playwright-mcp/README.md) – pattern for automating UI flows; the tray inspector mirrors that approach.

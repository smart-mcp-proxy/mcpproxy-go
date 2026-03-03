# Quickstart: Status Command

## Build & Test

```bash
# Build mcpproxy binary
make build

# Run unit tests for status command
go test ./cmd/mcpproxy/ -run TestStatus -v

# Run with daemon
./mcpproxy serve &
./mcpproxy status
./mcpproxy status --show-key
./mcpproxy status --web-url
./mcpproxy status --reset-key
./mcpproxy status -o json

# Run without daemon (config-only mode)
kill %1  # stop daemon
./mcpproxy status
./mcpproxy status --show-key -o json
```

## Key Files

| File | Purpose |
|------|---------|
| `cmd/mcpproxy/status_cmd.go` | Command implementation |
| `cmd/mcpproxy/status_cmd_test.go` | Unit tests |
| `internal/cliclient/client.go` | `GetStatus()` method |
| `docs/cli/status-command.md` | Docusaurus documentation |
| `website/sidebars.js` | Sidebar navigation |

## Verify

```bash
# Quick check - command exists and shows help
./mcpproxy status --help

# Config-only mode
./mcpproxy status

# Open Web UI
open $(./mcpproxy status --web-url)

# JSON output
./mcpproxy status -o json | jq .
```

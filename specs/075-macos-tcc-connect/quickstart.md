# Quickstart: macOS TCC-safe Connect wizard & App-Data denial diagnostics

## What changes for a user

- Opening the Connect page on macOS **no longer triggers** a "wants to access data from other apps" prompt for every installed MCP client. Status shows which clients are installed (file present) without reading their contents.
- A client's "connected to mcpproxy?" detail is computed **when you act on that client** (open its detail / connect / disconnect). That is the only moment macOS may prompt — for that one app, in context.
- If macOS has blocked access, you get a clear message with the exact fix instead of a silent "not connected."

## Recover a wrong "Don't Allow"

```bash
# Reset the App Data decision for mcpproxy (release build):
tccutil reset SystemPolicyAppData com.smartmcpproxy.mcpproxy
# Dev build:
tccutil reset SystemPolicyAppData com.smartmcpproxy.mcpproxy.dev
# Or: System Settings ▸ Privacy & Security ▸ App Data ▸ enable mcpproxy
```

## Verify (developer)

```bash
# Unit tests — status does no content reads; on-demand read; denied surfacing; classifier.
go test ./internal/connect/ -run 'Status|Access|Denied|Connect' -v

# Doctor check (macOS-only logic; no-op build on Linux/Windows).
go test ./internal/diagnostics/... -run 'TCC|AppData' -v   # (package pinned in tasks)

# Doctor end-to-end:
go build -o mcpproxy ./cmd/mcpproxy
./mcpproxy doctor        # On macOS with a denial present: warns + prints tccutil command.

# Existing Connect REST contract still green:
./scripts/test-api-e2e.sh
```

## Manual macOS check (optional)

1. With several MCP clients installed, open the Connect page → confirm NO privacy prompt appears.
2. Click a single client's connect → a prompt may appear for that one app; allow it → connects.
3. Deny it → UI shows the remediation banner; `./mcpproxy doctor` flags it.

## Acceptance mapping

- US1/SC-001/SC-002 → `internal/connect` status test asserts zero content reads.
- US2/SC-003/SC-004 → classifier + surfacing tests (accessible/absent/denied/malformed).
- US3/SC-005 → doctor check tests (warn/pass on darwin, no-op elsewhere).
- SC-006 → existing Connect REST tests + `test-api-e2e.sh`.

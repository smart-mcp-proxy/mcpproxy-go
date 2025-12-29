# CLI Upstream QA Report (2025-12-28)

## Environment
- Repo: /Users/user/repos/mcpproxy-go
- mcpproxy version: v0.11.5
- Daemon: running (socket mode)

## Scope
Upstream and tools management: `upstream` and `tools` commands.
Docs referenced: docs/cli/management-commands.md, docs/cli/command-reference.md.

## Test Cases

### TC1: Upstream list
Command:
```bash
./mcpproxy upstream list
```
Expected (docs/cli/management-commands.md):
- Table listing servers with protocol, tool count, health status, and suggested action.
Actual:
- Table rendered with server name, protocol, tool count, status, and action.
- Status includes icon glyphs and short text (example: token expired, connected, quarantined).
Result: Pass (format), but see Issues below for status consistency.

### TC2: Upstream logs (tail)
Command:
```bash
./mcpproxy upstream logs obsidian-pilot --tail=5
```
Expected (docs/cli/management-commands.md):
- Last N log lines for the server.
Actual:
- Log lines returned successfully.
- Output includes full JSON-RPC tool payloads and responses (redacted here due to sensitive content).
Result: Pass (functionality).

UX notes:
- Logs include full tool response content by default; this can expose private user data. Consider a `--redact` or `--no-payload` mode.

### TC3: Tools list (server)
Command:
```bash
./mcpproxy tools list --server=obsidian-pilot
```
Expected (docs/cli/command-reference.md):
- Table of tools for a server.
Actual:
- Output is a long-form list with full per-tool descriptions and guidance blocks.
- Output includes ANSI color codes and warning icons in the tool descriptions.
Result: Pass (functionality), but output is very verbose for large toolsets.

UX notes:
- Consider a compact mode (name + short description) and/or `--limit` or `--no-details` for large servers.

## Issues / Observations
1) Health status inconsistency: `doctor` reports OAuth errors for some servers, while `upstream list` shows "Connected" for the same servers. This makes it hard to trust status at a glance.
2) `tools list` output is not easily machine-readable in default mode; doc says `-o json` is available, but default output is too verbose for quick scanning.
3) Logs expose full tool responses by default; consider masking or payload truncation flags.

## References
- docs/cli/management-commands.md
- docs/cli/command-reference.md

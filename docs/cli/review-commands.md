---
id: review-commands
title: Review Commands
sidebar_label: Review Commands
sidebar_position: 7
description: "CLI commands for reviewing quarantined servers and new or changed tools: list, show, approve and reject through the scan gate"
keywords: [review, quarantine, approve, reject, tools, scan, cli]
---

# Review Commands

`mcpproxy review` is the one CLI entry point for the review decisions that the Web UI **Review queue** and the macOS **Review Queue** also offer. It reads the same review payload as `GET /api/v1/review` and `GET /api/v1/servers/{id}/review`, and every decision goes through the scan gate: it never calls the legacy `unquarantine` endpoint.

For the concepts behind quarantine and the four review verbs see [Security Quarantine](/features/security-quarantine#review-verbs). For scanner setup and scan reports see [Security Scanner Commands](/cli/security-commands).

## Overview

```
mcpproxy review
├── list                        Servers that need review
├── show <server> [--full]      Captured tool definitions and scan verdicts
├── approve <server> [flags]    Approve a server, or approve tools on a trusted server
└── reject <server> [flags]     Keep a server quarantined, or block tools
```

All subcommands accept the global `-o table|json|yaml` flag. With `-o json` the output is the `data` object of the REST response.

## review list

```bash
mcpproxy review list
```

```
SERVER      KIND           QUARANTINED  PENDING  CHANGED  TIERS               SCAN
filesystem  server_review  true         0        0        map[destructive:1]  map[verdict:clean]
```

## review show

```bash
mcpproxy review show filesystem --full
```

```
Server: filesystem
TOOL      TIER         APPROVAL  SCAN   DESCRIPTION
delete_0  destructive  pending   clean  from the server, not verified:
Delete a file
This cannot be undone
Input schema:
{"type":"object"}
```

When the review payload carries scan coverage, a `Scan:` line follows `Server:`. It says whether the baseline scan describes the definitions shown, in the same words as the Web UI and the macOS app:

```
Server: notes
Scan: out of date (1 tool changed or added after the last scan: notes); run: mcpproxy security rescan notes
```

A scan that covers every captured tool reads `Scan: clean · risk 0/100 · covers all 5 tools`. When definitions have not been captured, the line says so and points to **Fetch tool definitions** on the Web or macOS review screen; there is no CLI command for that capture.

Descriptions and schemas come from the upstream server and are shown as plain text; they are not verified. Without `--full` only the first line of each description is shown and the schemas are left out.

## review approve

```bash
mcpproxy review approve <server> [--all | --tools a,b] [--except a,b] [--force] [--yes]
```

On a quarantined server the command allows the same default selection as the Web and macOS review screens: only read-only tools with a clean scan (`default_allowed` in the review payload). Every other tool is blocked, meaning approved and disabled, until you enable it on the Tools tab. A core that does not send `default_allowed` blocks every tool.

| Flag | Meaning |
|------|---------|
| `--all` | Approve every pending or changed tool (the Web and macOS "Approve all"); tools blocked earlier on a re-quarantined server stay blocked. On a trusted server this is the default and changes nothing |
| `--tools a,b` | Quarantined server: allow exactly these tools and block the rest. Trusted server: approve only these pending or changed tools |
| `--except a,b` | Quarantined server only: block these tools as well, whichever base was chosen |
| `--force` | Approve although the scan verdict is dangerous (use only after reading the findings) |
| `--yes` | Skip the confirmation prompt |

The command picks the right endpoint for you:

| Server state | Endpoint | Notes |
|--------------|----------|-------|
| Quarantined | `POST /api/v1/servers/{id}/security/approve` | `block` is every tool outside the selection (default, `--all` or `--tools`, minus `--except`); `--force` is sent as `force` |
| Trusted (not quarantined) | `POST /api/v1/servers/{id}/tools/approve` | `--tools` selects tools; without it every pending or changed tool is approved |

Using the wrong flag for the state fails with exit code 1 instead of doing something else:

- `--all` together with `--tools`: `--all cannot be combined with --tools`
- A tool name that is not in the review (`--tools` or `--except`): `unknown tool 'x' for server 's'`; nothing is written
- `--tools` or `--except` while no tool definitions are captured: `no tool definitions captured for server 's'; fetch them first ...`; nothing is written
- `--except` on a trusted server: `--except applies only while approving a quarantined server`

The confirmation prompt reads the review first and names the exact count, for example `Approve server 'memory' with 3 of 9 tools? Blocked: a, b, c.`, `Approve server 'memory' with all 9 tools?` or `Approve server 'memory' without seeing tools?` when nothing is captured. In table output one line precedes the result: `Allowing 3 of 9 tools; blocking 6: a, b, ...`; with `--all` it reads `Allowing all pending or changed tools; previously blocked tools stay blocked`. JSON and YAML output stay the REST data object.

`mcpproxy review approve <server> --yes` used to allow every tool. It now allows the default selection, so it matches the review screens; add `--all` to approve every tool.

```bash
mcpproxy review approve filesystem --except delete_0 --force --yes
```

```
Allowing 0 of 1 tool; blocking 1: delete_0
Approved server filesystem
```

```bash
mcpproxy review approve trusted --tools write_0 --yes
```

```
Approved 1 tool for server trusted
```

## review reject

```bash
mcpproxy review reject <server> [--tools a,b] [--yes]
```

Without `--tools` the server stays quarantined (`POST /api/v1/servers/{id}/security/reject`). With `--tools` the listed pending or changed tools are blocked (`POST /api/v1/servers/{id}/tools/block`).

```bash
mcpproxy review reject filesystem --yes
```

```
Rejected server filesystem
```

```bash
mcpproxy review reject trusted --tools write_0 --yes
```

```
Blocked 1 tool for server trusted
```

## Older commands

These keep working and name the matching `mcpproxy review` command in their help:

| Command | Same as |
|---------|---------|
| `mcpproxy upstream approve <server> [tools...]` | `mcpproxy review approve <server> --tools ...` for a trusted server |
| `mcpproxy tools approve <server:tool>...` | `mcpproxy review approve <server> --tools ...` |
| `mcpproxy tools reject <server:tool>...` | `mcpproxy review reject <server> --tools ...` |
| `mcpproxy security approve <server>` | `mcpproxy review approve <server>` |
| `mcpproxy security reject <server>` | `mcpproxy review reject <server>` |

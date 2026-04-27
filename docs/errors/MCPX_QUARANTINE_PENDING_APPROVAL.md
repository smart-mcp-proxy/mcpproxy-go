---
id: MCPX_QUARANTINE_PENDING_APPROVAL
title: MCPX_QUARANTINE_PENDING_APPROVAL
sidebar_label: PENDING_APPROVAL
description: This server has tools awaiting security approval; they will not run until approved.
---

# `MCPX_QUARANTINE_PENDING_APPROVAL`

**Severity:** warn
**Domain:** Quarantine

## What happened

mcpproxy quarantines newly added MCP servers (and newly added tools on existing
servers) by default. The quarantine is mcpproxy's defense against Tool Poisoning
Attacks (TPA) — the user must review tool descriptions and JSON schemas before
they're routed to AI clients.

This warning means the server connected and reported its tools, but at least
one tool is pending your approval.

## How to fix

### Approve via the web UI

Open the server's detail page → **Quarantine** panel → review the tool
descriptions / schemas → click **Approve all** or per-tool **Approve**.

### Approve via the CLI

```bash
mcpproxy upstream inspect <server-name>          # see what's pending
mcpproxy upstream approve <server-name>          # approve all pending tools
mcpproxy upstream approve <server-name> --tool foo  # approve a single tool
```

### Skip quarantine for trusted servers

For servers you fully trust (e.g. self-written, or vendor-signed) you can opt
out of quarantine on a per-server basis:

```json
{ "skip_quarantine": true }
```

Or globally (not recommended on shared dev machines):

```json
{ "quarantine_enabled": false }
```

## Related

- [Security Quarantine](../features/security-quarantine.md)
- [`MCPX_QUARANTINE_TOOL_CHANGED`](MCPX_QUARANTINE_TOOL_CHANGED.md) — rug-pull detection

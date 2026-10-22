---
title: Review a tool before exposing it to your agent
sidebar_label: Review a tool before use
description: Inspect and approve a first server deliberately, then handle a changed tool definition without bypassing the review hold.
---

# Review a tool before exposing it to your agent

An upstream tool description or schema is supplied by the server that runs the tool. Review it before allowing an agent to call it, and review a later captured contract change before accepting it. This guide covers both the first approval of a quarantined server and a changed tool on an already trusted server. You are done when only your chosen tool is callable and a real call succeeds.

## Before you start

Use **MCPProxy v0.71.1 or later** and an upstream whose source and requested access you can evaluate. Run operator commands on the machine hosting the proxy. The examples use `notes` and its `read_note` tool; replace both names with your exact server and tool. If the daemon uses a custom config or data directory, pass its `--config` and `--data-dir` options to every CLI command.

The default `manual` trust mode holds a new server for review and holds later substantive changes to a captured tool contract. Equivalent input-schema key order and `required` order do not count as a change; older approval records have [one-time migration exceptions](/features/security-quarantine#what-quarantine-does-and-does-not-cover). The guard cannot detect an implementation or result change when the definition stays the same. `scan` can admit a new server or new tool after a clean offline scan, but **holds a changed captured contract**. `auto`, global `quarantine_enabled: false`, and the legacy `auto_approve_tool_changes: true` option can remove these holds. Confirm the effective [trust mode](/features/security-quarantine#trust-modes-auto--scan--manual) before relying on a review queue.

## 1. Capture the first review

Add a new server with your real transport details, using `manual` trust. For example, an HTTP upstream:

```bash
mcpproxy upstream add notes https://YOUR-SERVER.example/mcp --trust-mode manual
mcpproxy review list
mcpproxy review fetch notes
mcpproxy review show notes --full
```

**Expected:** `notes` is quarantined. `review fetch` captures its definitions without admitting it. If an existing `notes` entry already exists, inspect that entry instead of adding a duplicate. If capture fails, check the server URL, authentication and `mcpproxy upstream logs notes`; do not approve unseen tools.

Read the source URL or command, every tool you plan to allow, descriptions, input and output schemas, and safety hints. A read-only annotation is the upstream's claim; verify the operation and where its data goes. Treat instructions inside a tool description as untrusted content, especially requests to change the agent's priorities, reveal secrets or send data elsewhere.

## 2. Scan, decide, and approve only the intended tool

```bash
mcpproxy security scan notes
mcpproxy security report notes
mcpproxy review show notes --full
```

The offline scan supplies findings and a verdict for the captured definitions. Check whether the review says the scan **covers** what is currently shown. A missing, failed, dangerous or out-of-date scan needs investigation or a fresh scan; a clean scan is evidence, not proof that a tool is safe or suitable for your task.

After accepting `read_note` and its access, run:

```bash
mcpproxy review approve notes --tools read_note
mcpproxy tools preflight notes:read_note --wait 10s
```

Read the approval prompt and the resulting counts. On a quarantined server, `--tools read_note` allows that tool and **blocks the others**; selecting a tool does not imply approval of the full catalog. Blocked tools cannot execute. In v0.71.1, the direct `/mcp/all` list can still include a blocked sibling's definition after server approval; use on-demand discovery if you also need to keep unselected definitions out of the initial client list. Preflight should report `ready` (exit `0`) after startup. It reads local state and does not make the upstream call. If the review changed during approval, MCPProxy refuses the stale decision; fetch/show the current definitions and decide again. See [Review commands](/cli/review-commands) for saved, fingerprint-bound reviews and the Web UI equivalent.

## 3. Review a later tool change

When a trusted server changes a tool's description, input/output schema or safety hints, it may appear in the Review queue as `changed`. Do not approve from its name alone:

```bash
mcpproxy review list
mcpproxy review show notes --full
mcpproxy tools preflight notes:read_note
mcpproxy security report notes
```

**Expected for a held change:** `review show` displays the previous and current definition, including the diff; preflight reports `tool_changed` (usually exit `11`). Check what the new arguments permit, whether the operation still matches the name and annotations, and whether the scan covers the **current** definition. If the report is stale, run `mcpproxy security rescan notes`, then inspect it again. A clean result does **not** release a changed contract under `manual` or `scan` trust.

If you accept the new contract, approve that one tool and verify it:

```bash
mcpproxy review approve notes --tools read_note
mcpproxy tools preflight notes:read_note --wait 10s
```

If you do not accept it, leave it held or use `mcpproxy review reject notes --tools read_note` to acknowledge and block it. Blocking disables the tool; later enabling it is a separate decision. Avoid `--force`, turning off quarantine or switching to `auto` to resolve a review hold.

## 4. Prove the agent's access and call

In your intended client, start a fresh session and ask it to find and call `notes:read_note` with harmless arguments. In `retrieve_tools` mode, it should search first and use the returned `call_with` route; in direct mode it should call `notes__read_note`. Then inspect:

```bash
mcpproxy activity list --server notes --tool read_note --type tool_call --from=-15m
```

**Completion check:** the chosen tool is visible to that client, the real call succeeds with the expected arguments, and Activity records it. If operator preflight says `ready` but the client cannot see it, check that client's profile and credential. If the call fails, inspect its Activity result and follow [Find out why an agent cannot use a tool](/guides/diagnose-unavailable-tool). A scan result, approval message or preflight result alone does not prove the agent completed the task.

*Validation: 22 October 2026, MCPProxy v0.71.1 on macOS. A controlled local MCP fixture was quarantined, its 24 definitions captured and scanned, and only one tool approved. After that tool's benign description changed, `review show` displayed the diff and preflight returned `tool_changed`. A fresh scan reported 0 findings and risk 0 while the tool remained held. The fixture was a protocol test, not an external client UI test. Maintained by the MCPProxy maintainers.*

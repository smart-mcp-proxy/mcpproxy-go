---
title: Reuse one MCP setup in two AI clients
sidebar_label: One setup, two clients
description: Connect Claude Code and Codex to one local MCPProxy instance, review a documentation upstream once, and verify a real tool call from each client.
---

# Reuse one MCP setup in two AI clients

Configure a documentation server once in MCPProxy, then use it from **Claude Code** and **Codex CLI** on the same computer. You are done when both clients have called the same upstream tool and you can find both successful calls in Activity.

## Before you start

- Install **MCPProxy v0.71.1 or later** and both clients. Sign in to each client as required by its provider.
- Start one local MCPProxy instance using the [installation instructions](/getting-started/installation) or the tray app. If it is already running, do not start another.
- Open its Web UI from the tray menu. Terminal users can run `mcpproxy status --web-url` and open the returned URL privately: it contains an administrator credential.
- Use the default **`retrieve_tools` routing mode**. Check **Clients → Endpoint & mode**; see [routing modes](/features/routing-modes) if your instance uses another mode.
- This guide uses the remote [Context7 documentation server](https://context7.com/docs/resources/all-clients). It receives your library query. Use only public example text; access requirements and rate limits are controlled by Context7.

Keep the listener on loopback (`127.0.0.1`). This is one person's local setup, not a shared team deployment. Sharing upstream configuration does **not** create per-user isolation. For team access, follow [Deploying for a team](/operations/deploying-for-a-team).

## 1. Add the upstream once

In a terminal on the machine running MCPProxy:

```bash
mcpproxy upstream add context7 https://mcp.context7.com/mcp --trust-mode manual
mcpproxy review fetch context7
mcpproxy review show context7 --full
```

**Expected:** the server is quarantined and its captured definitions include `resolve-library-id` and `query-docs`. Fetching definitions does not approve them. If `context7` already exists, inspect that entry instead of adding a duplicate.

Inspect the URL, descriptions, input schemas and read-only annotations. Tool definitions are supplied by the upstream; a clean scan is evidence for your decision, not a guarantee of safety.

```bash
mcpproxy security report context7
```

If a scan is still running, wait and read the report again. If no report exists, run `mcpproxy security scan context7`, then read the report. Resolve failed scans or findings before continuing; do not bypass the gate with `--force`.

After you have reviewed and accepted those two tools:

```bash
mcpproxy review approve context7 --tools resolve-library-id,query-docs
mcpproxy tools preflight context7:resolve-library-id --wait 10s
```

Read the approval prompt before confirming it. **Expected:** both selected tools are approved, then preflight reports `ready` (exit `0`). A brief `server_initializing` state after approval is normal; retry preflight once connection completes. If approval reports a stale review, inspect the new definitions before trying again.

If Context7 requires a key or returns a quota error, follow its current access instructions and store the credential on the **upstream entry in MCPProxy**, not separately in both AI clients. See [upstream authentication](/configuration/upstream-servers). The upstream's credential is different from a client's credential for MCPProxy.

## 2. Connect Claude Code

```bash
mcpproxy connect claude-code
```

This writes a per-client credential into Claude Code's configuration and backs up an existing file. It does not give the client the instance administrator API key. If an entry already exists, use **Clients → Connect** in the Web UI to inspect the replacement preview before confirming it; do not blindly add `--force`.

Restart Claude Code and open `/mcp`. Confirm that **mcpproxy** is connected. Project-level settings can override a user-level entry; if the wrong endpoint appears, check the client's [configuration scopes](https://code.claude.com/docs/en/mcp#mcp-installation-scopes).

**Expected:** Claude Code connects to MCPProxy's `/mcp` endpoint. It does not need its own direct Context7 entry.

## 3. Connect Codex CLI

```bash
mcpproxy connect codex
```

Restart Codex CLI, then open `/mcp` to check the connection. The Connect flow writes to `~/.codex/config.toml`; see [Codex MCP configuration](https://learn.chatgpt.com/docs/extend/mcp?surface=cli).

:::note Keep the generated credential private
For Codex, Connect currently puts the per-client credential in the endpoint URL's `apikey` query parameter. Do not paste the generated URL into screenshots, issues or shared configuration. The [Connect Clients reference](/features/connect-clients#codex-credential-limitation) describes a manual environment-header alternative.
:::

New client credentials default to **All servers, switchable**. Existing clients retain their binding when reconnected. If you need a narrower profile, use the [Connect Clients profile options](/features/connect-clients#using-client-credentials). Enable `require_mcp_auth` before relying on a restricted profile so an anonymous connection cannot bypass it.

**Expected:** both clients point to the same proxy, with separate client credentials. No second copy of the upstream configuration is needed.

## 4. Make one real call from each client

Start a fresh conversation in Claude Code and send:

> Use MCPProxy, not a shell command or web search. First use retrieve_tools to find context7's library ID resolver. Then call context7:resolve-library-id for libraryName "Vue Router" and query "How do I configure a basic Vue Router route?". Show the returned library ID. Do not change any files.

Approve the client's tool-use prompt if it asks. Repeat the same request in Codex.

**Expected tool sequence in each client:**

1. `retrieve_tools` finds `context7:resolve-library-id` and its current argument schema.
2. The agent uses the returned `call_with` tool (normally `call_tool_read`) to invoke that exact name with `libraryName` and `query`.
3. Context7 returns actual library matches. Their IDs and contents can change; do not compare against a hard-coded ID.

In discovery mode, seeing only MCPProxy's small set of routing tools in `/mcp` is normal. The upstream tools appear through retrieval. If the agent chooses another route, use the [agent-instructions snippet](/features/agent-instructions) and repeat the explicit request.

## 5. Verify both calls in Activity

```bash
mcpproxy activity list --server context7 --tool resolve-library-id --type tool_call --from=-15m
```

Open the two matching rows in the Web UI's **Activity** view, or run `mcpproxy activity show <id> --include-response` with each returned ID. Check the timestamp, arguments, successful result, and client/session attribution. With Connect credentials, the client identifiers should distinguish `claude-code` from `codex`; self-reported client names alone are advisory.

**Completion check:** one successful upstream call from each client, using the same `context7` entry. A connected badge or a plausible answer without the corresponding tool activity does not prove this workflow succeeded.

## If it does not work

| Symptom | Next check |
| --- | --- |
| Client cannot connect, or gets `401` | Confirm MCPProxy is running and the client's endpoint matches it. Reconnect that client to replace an expired/revoked credential; do not disable authentication as a fix. |
| Connected, but the resolver is missing | Run preflight again; inspect quarantine, tool approval and the client's profile. |
| Resolver appears, but the call fails | Inspect its Activity entry for invalid arguments, upstream authentication or rate limits. |

Follow [Find out why an agent cannot use a tool](/guides/diagnose-unavailable-tool) for the full diagnosis path.

## Next steps and cleanup

- Choose a [routing mode](/features/routing-modes) once the two-client baseline works.
- Disconnect only the client entry you no longer want with `mcpproxy disconnect claude-code` or `mcpproxy disconnect codex`. See [client credential lifecycle](/features/connect-clients#using-client-credentials) for revocation; removing a config entry and revoking its credential are distinct actions.
- To stop using the example upstream while keeping its configuration, run `mcpproxy upstream disable context7`.

*Validation: 22 October 2026, MCPProxy v0.71.1 on macOS. Upstream review, discovery, calls from two independent MCP sessions and Activity were checked against the released daemon. Client connection instructions were checked against Connect's configuration tests and the linked client references; this is not a claim of an end-to-end UI test of every client release. Maintained by the MCPProxy maintainers.*

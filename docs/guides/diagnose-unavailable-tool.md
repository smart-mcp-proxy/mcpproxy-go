---
title: Find out why an agent cannot use a tool
sidebar_label: Diagnose an unavailable tool
description: Trace a missing or failing MCP tool through client connection, server state, review, access policy, discovery and Activity, then verify the fix.
---

# Find out why an agent cannot use a tool

Your client may be connected while a particular tool is unavailable. Work from the first failing check below, change only the cause you identify, and repeat the original call. You are done when the agent's retry succeeds and Activity records that result.

## Before you start

Use **MCPProxy v0.71.1 or later** and run operator commands on the machine running the proxy. Commands below use `context7:resolve-library-id` as an example; replace it with your exact `server:tool` ID. If you use a custom data directory, pass the same `--config` and `--data-dir` options as your daemon.

Keep the original error text and approximate time. Do not share administrator URLs, credentials, private tool arguments or full diagnostic dumps in public issues.

## 1. Can the client reach the right proxy?

```bash
mcpproxy status
```

**Expected:** `Running`, the intended listen address, and the routing mode you expect. Treat the Web UI URL printed by diagnostics as private. If the proxy is stopped, start it using your normal tray/service setup; do not start a second instance on an occupied port.

Open the client's MCP status view (`/mcp` in Claude Code or Codex) and compare its endpoint with **Clients → Endpoint & mode** in MCPProxy.

| What you see | What to do |
| --- | --- |
| Connection refused | Check the running instance and port. On another computer or container, `localhost` refers to that environment, not your proxy's host. |
| `401` from MCPProxy | Check the client's credential. Use [Connect Clients](/features/connect-clients) to reconnect an expired/revoked client; keep `require_mcp_auth` enabled if you rely on it. |
| Connected to the wrong proxy | Correct the client entry and restart the client. Check project-level overrides as well as user-level configuration. |
| Connected, only routing tools visible | This is expected in `retrieve_tools` mode. Continue below. In direct or code-execution mode, follow that mode's [calling pattern](/features/routing-modes). |

An upstream `401` during a tool call is a different problem: fix that server's authentication in step 4, not the client's MCPProxy credential.

## 2. Ask for an exact availability diagnosis

```bash
mcpproxy upstream list
mcpproxy tools preflight context7:resolve-library-id
```

Preflight reads local proxy state; it does **not** contact the upstream or guarantee that a later call will succeed. A nonzero exit code is a diagnostic result, not a reason to reset configuration.

| Result / reason | Next action |
| --- | --- |
| `ready` (exit `0`) | Continue to discovery and the real call in step 4. |
| `server_initializing` or another retryable reason (exit `10`) | Read the action/detail; allow a short retry with `--wait 10s`. If it persists, inspect the server's connection error and logs. |
| `server_quarantined`, `tool_pending_approval`, `tool_changed` (usually exit `11`) | Inspect the held definitions in step 3. |
| Disabled server or tool; configuration policy denial | Confirm whether the restriction is intentional. Use the server/Tools view to identify it before enabling anything. |
| `oauth_required` or an upstream authentication problem | Authenticate the upstream through its server page. See [OAuth authentication](/features/oauth-authentication). |
| `not_found` (exit `12`) | Verify the exact name and server entry. An agent-visible `not_found` can also mean its access scope hides the tool. |

For connection errors, inspect the server page and `mcpproxy upstream logs context7`. See [preflight reason and exit-code details](/features/tools-preflight).

### Operator-ready does not always mean client-ready

An operator's default preflight sees more than a restricted client. Inspect the client's actual access:

```bash
mcpproxy tools list --client claude-code
```

Replace `claude-code` with the connected client's ID. Find the row with server `context7` and name `resolve-library-id` and read the **ACCESS** and **REASON** columns. The client view applies to the global list; do not combine it with `--server`. Check the client's binding in **Clients**, its profile's server/tool policy, and any token restrictions. If the restriction is intentional, use an authorized client/profile; do not widen access just to make the error disappear.

A tool outside a restricted agent's scope can appear as `not_found` without disclosing that it exists. See [Profiles](/features/profiles) and [Agent tokens](/features/agent-tokens).

## 3. Resolve a review hold deliberately

```bash
mcpproxy review list
mcpproxy review show context7 --full
```

If definitions have not been captured, use `mcpproxy review fetch context7`, then inspect them. Read `mcpproxy security report context7`; if no scan exists, run `mcpproxy security scan context7` first.

- **New server:** review the server source and the tools you intend to allow. Approval can leave unselected tools blocked.
- **Changed tool:** inspect the definition diff, including schemas and safety hints. A clean scan does not approve the changed contract.
- **Failed or dangerous scan:** investigate the report. Do not use `--force`, disable quarantine, or switch to automatic trust as a troubleshooting shortcut.

Only after accepting the specific tool, run:

```bash
mcpproxy review approve context7 --tools resolve-library-id
```

Read the confirmation prompt. On a quarantined server, this allows that selected tool and blocks the others; on an already trusted server it approves the selected pending/changed tool. If the reviewed definitions changed in the meantime, fetch and inspect the new review before trying again.

If a tool was deliberately blocked earlier, approving it again is not the same as enabling it. Inspect its state in the Tools view and enable only what your policy permits. See [Review commands](/cli/review-commands).

Repeat preflight. If the reason changes, follow the new reason instead of repeating approval.

## 4. Separate discovery from a failed call

For `retrieve_tools` mode, ask the agent:

> Use MCPProxy retrieve_tools to find context7:resolve-library-id. Use the exact returned name, current schema and call_with tool. Resolve "Vue Router" with query "How do I configure a basic Vue Router route?". Do not use a shell or web search instead. If the call fails, report the tool error rather than guessing a result.

If retrieval returns no match despite client access being allowed, try a short query describing the tool's operation. Check that discovery has completed and the server's Tools view lists the expected tool. If the agent does not attempt MCPProxy at all, use [Agent instructions](/features/agent-instructions). In other routing modes, use their [documented call surface](/features/routing-modes).

If a call was attempted, inspect Activity:

```bash
mcpproxy activity list --server context7 --tool resolve-library-id --from=-15m
```

Open the relevant ID with `mcpproxy activity show <id> --include-response` or in the Web UI. Match time, client/session, arguments and result—not just the tool name.

| Evidence | Remedy |
| --- | --- |
| Invalid or missing arguments | Use the returned/current input schema. For the example resolver, both `libraryName` and `query` are required. |
| Upstream authentication error | Refresh that upstream's OAuth login or configured key; this is separate from client-to-proxy authentication. |
| Rate limit, timeout or network error | Follow the upstream's retry guidance; inspect connection status and logs. Avoid an unbounded retry loop. |
| Policy or review refusal | Return to the specific access/review check above. |
| No matching upstream call | The agent may not have called it, or it may have failed before dispatch. Check the client's tool transcript and unfiltered recent Activity for internal calls or policy decisions. |

For broader Activity filters and request IDs, see [Activity commands](/cli/activity-commands).

## 5. Prove the recovery

Repeat the **same original request in the same client**, with the intended profile and credential. Verify:

1. Preflight no longer reports the original blocker.
2. The agent makes the intended tool call and receives a successful result.
3. Activity contains that retry with matching arguments, client/session and status.

If it still fails, save the sanitized error, MCPProxy version, timestamp/request ID, routing mode and preflight reason when [reporting an issue](https://github.com/smart-mcp-proxy/mcpproxy-go/issues). A ready preflight alone is not proof of a successful recovery.

*Validation: 22 October 2026, MCPProxy v0.71.1 on macOS. Quarantine, approval, transient initialization, tool disable/re-enable, unknown ID, invalid arguments and successful retry were checked in an isolated instance. Maintained by the MCPProxy maintainers.*

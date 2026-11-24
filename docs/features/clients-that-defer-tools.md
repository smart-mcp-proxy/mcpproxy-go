---
id: clients-that-defer-tools
title: Clients that defer MCP tools
sidebar_label: Clients that defer tools
description: Keep MCPProxy's built-in tools such as retrieve_tools loaded in clients that hide MCP tools behind their own tool search or code mode.
keywords: [tool search, deferred tools, alwaysLoad, deferTools, codemode, exposure, retrieve_tools, claude code, copilot cli, pi, opencode, codex, kiro]
---

# Clients that defer MCP tools

Some MCP clients no longer put every MCP tool definition in the model's context. They list tool names only, or hide MCP behind a code sandbox, and the model finds a tool through the client's own search first. MCPProxy already does its own discovery, so a deferring client adds a second search layer in front of it.

**What goes wrong.** The model must first find `retrieve_tools` through the client's search. That costs a round trip, can fail ("tool not found" for a name the model never searched for), and the two searches run one after the other.

**The fix.** Mark the MCPProxy server as "always loaded" in the client. The few built-in tools (`retrieve_tools`, `describe_tool`, `call_tool_read`, `call_tool_write`, `call_tool_destructive`, …) then sit in context from the first turn. In the default `retrieve_tools` routing mode your upstream servers' tools stay out of context, because MCPProxy serves them on demand. In `direct` mode the pinned endpoint would load every upstream tool, so pin only a `retrieve_tools` or `code_execution` endpoint (for example `/mcp/call`). See [Routing modes](/features/routing-modes).

Clients that load every tool up front (for example Cursor, Windsurf or Gemini CLI) need none of this.

:::note Verified against vendor documentation
Each snippet below was checked against the client's own documentation on 24 November 2026. Clients change these settings often, so confirm against the linked page for your version. A row marked **not verified** means the vendor documentation showed no per-server setting.
:::

## Claude Code

Claude Code defers MCP tools by default ([MCP docs](https://code.claude.com/docs/en/mcp)). Set `alwaysLoad` on the MCPProxy server entry. It works for every server type and overrides `ENABLE_TOOL_SEARCH`:

```bash
claude mcp add-json --scope user mcpproxy '{"type":"http","url":"http://127.0.0.1:8080/mcp","headers":{"X-API-Key":"YOUR_KEY"},"alwaysLoad":true}'
```

Or in `.mcp.json`:

```json
{
  "mcpServers": {
    "mcpproxy": {
      "type": "http",
      "url": "http://127.0.0.1:8080/mcp",
      "headers": { "X-API-Key": "YOUR_KEY" },
      "alwaysLoad": true
    }
  }
}
```

Setting `alwaysLoad: true` makes startup wait for the server's tools (up to the 5-second connect timeout). Setting `ENABLE_TOOL_SEARCH=false` also works, but turns deferral off for every server.

## GitHub Copilot CLI

Tool search is on by default for supported models ([docs](https://docs.github.com/en/copilot/concepts/agents/copilot-cli/tool-search)). Set `deferTools` to `"never"` on the server, either through `/mcp edit` (Defer Tools → Never) or in `~/.copilot/mcp-config.json`:

```json
{
  "mcpServers": {
    "mcpproxy": {
      "type": "http",
      "url": "http://127.0.0.1:8080/mcp",
      "headers": { "X-API-Key": "YOUR_KEY" },
      "deferTools": "never",
      "tools": ["*"]
    }
  }
}
```

`toolSearch: false` in your personal settings turns tool search off for all servers.

## pi

Since pi 0.99 an MCP server's tools default to `codemode`: they are not declared to the model. Give the MCPProxy server `direct` exposure so its tools are declared like built-ins ([MCP docs](https://github.com/badlogic/pi-mono/blob/main/packages/coding-agent/docs/mcp.md)). You can also change it from `/mcp` in a session.

```json
{
  "mcpServers": {
    "mcpproxy": {
      "url": "http://127.0.0.1:8080/mcp",
      "headers": { "X-API-Key": "YOUR_KEY" },
      "exposure": "direct"
    }
  }
}
```

The file location and the transport keys are described in pi's MCP document; only `exposure` is the setting that matters here.

## opencode (Code Mode)

In opencode versions with Code Mode, a server's tools are reached through a single `execute` tool by default. Set `codemode` to `false` on the server to keep them on the model's native tool list ([docs](https://opencode.ai/docs/mcp-servers/)):

```jsonc
{
  "mcp": {
    "servers": {
      "mcpproxy": {
        "type": "remote",
        "url": "http://127.0.0.1:8080/mcp",
        "codemode": false
      }
    }
  }
}
```

The `mcp.servers` layout is the one in the Code Mode documentation. Older opencode releases (and the entry MCPProxy's Connect writes) use `mcp.<name>` directly; the `codemode` key sits in the same server entry either way. Add the API key header using the remote-server options in opencode's documentation.

## Codex CLI and Kiro

| Client | Status |
| --- | --- |
| Codex CLI | **Not verified.** Codex's MCP documentation ([developers.openai.com/codex/mcp](https://developers.openai.com/codex/mcp)) lists `enabled_tools` and `disabled_tools` per server, but no setting to force a server's tools to be declared directly. Do not assume `enabled_tools` does this. If `retrieve_tools` is not found, name it in the prompt or add the [agent instructions](/features/agent-instructions). |
| Kiro | **Partly verified.** Kiro IDE 1.3+ and Kiro CLI 2.31+ (V3) load MCP tools on demand by default. The [Tool Search page](https://kiro.dev/docs/mcp/tool-search/) documents `toolSearch.enabled`, `toolSearch.minPct` and `toolSearch.minTokens` but no per-server exemption. Whether `toolSearch.enabled false` turns it off for V3 was not verified. |

## Check that it worked

Start a fresh session and ask the model to call `retrieve_tools` with a short query, without searching first. If the client's own tool trace shows it calling `retrieve_tools` directly, with no search step first, the pin works. `mcpproxy activity list --type internal_tool_call --tool retrieve_tools --from=-15m` confirms MCPProxy received the call, but not how the client found the tool.

If the client still hides the tools, see [Connect Clients](/features/connect-clients) to confirm which entry the client reads, and [Find out why an agent cannot use a tool](/guides/diagnose-unavailable-tool).

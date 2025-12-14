---
id: quick-start
title: Quick Start
sidebar_label: Quick Start
sidebar_position: 2
description: Get MCPProxy running in 5 minutes
keywords: [quickstart, getting started, first run, configuration]
---

import Tabs from '@theme/Tabs';
import TabItem from '@theme/TabItem';

# Quick Start

This guide will get MCPProxy running in 5 minutes.

## Prerequisites

- MCPProxy installed (see [Installation](/getting-started/installation))
- An MCP-compatible AI client (Cursor IDE, Claude Desktop, Claude Code CLI, etc.) - see [Connect Your AI Client](#4-connect-your-ai-client) below

## 1. Start MCPProxy

Choose the method that matches how you installed MCPProxy:

### Using the Tray App (Recommended for Installer Users)

If you installed MCPProxy using the **DMG installer** (macOS) or **Windows installer**, the easiest way to run MCPProxy is through the tray application:

<Tabs>
<TabItem value="macos" label="macOS">

1. Open **Launchpad** or use **Spotlight** (Cmd + Space)
2. Search for "**MCPProxy**" and click to launch
3. Look for the MCPProxy icon in your **menu bar** (top right)

</TabItem>
<TabItem value="windows" label="Windows">

1. Open the **Start Menu**
2. Find "**MCPProxy**" in your apps list and click to launch
3. Look for the MCPProxy icon in your **system tray** (bottom right, near the clock)

</TabItem>
</Tabs>

**What the tray app does:**

- **Automatically starts** the MCPProxy core server when launched
- **Automatically stops** the core server when you quit the tray app
- Provides **quick access** to the Web UI, logs, and settings via the tray menu
- **Runs in background** - minimize to tray and MCPProxy keeps running
- **Auto-starts on login** (optional) - configure in tray settings

:::tip Tray Menu Options
Right-click (or click on macOS) the tray icon to access:
- **Open Web UI** - Launch the management dashboard
- **View Logs** - See server activity
- **Upstream Servers** - View status of all MCP servers, enable/disable individual servers
- **Quit** - Stop MCPProxy completely
:::

### Using the Terminal (For Homebrew/Binary Users)

If you installed via **Homebrew** or **manual binary download**, start MCPProxy from your terminal:

```bash
mcpproxy serve
```

You should see output like:

```
MCPProxy server started
Listening on http://127.0.0.1:8080
Web UI available at http://127.0.0.1:8080/ui/
```

:::note Running Both
If you're using the tray app, you don't need to run `mcpproxy serve` manually - the tray app handles this for you. Running both will cause a port conflict.
:::

## 2. Open the Web UI

Open your browser to [http://127.0.0.1:8080/ui/](http://127.0.0.1:8080/ui/) to access the management dashboard.

## 3. Add Your First MCP Server

You can add MCP servers in two ways:

### Via Web UI

1. Click "Add Server" in the Web UI
2. Enter the server details
3. Click "Save"

### Via Configuration File

Edit `~/.mcpproxy/mcp_config.json`:

```json
{
  "mcpServers": [
    {
      "name": "filesystem",
      "command": "npx",
      "args": ["-y", "@modelcontextprotocol/server-filesystem", "/path/to/directory"],
      "protocol": "stdio",
      "enabled": true
    }
  ]
}
```

## 4. Connect Your AI Client

MCPProxy exposes an MCP endpoint at `http://localhost:8080/mcp`. Connect your AI client using one of these methods:

<Tabs>
<TabItem value="cursor" label="Cursor IDE">

**One-Click Install:**

<a href="cursor://anysphere.cursor-deeplink/mcp/install?name=MCPProxy&config=eyJ0eXBlIjoiaHR0cCIsInVybCI6Imh0dHA6Ly9sb2NhbGhvc3Q6ODA4MC9tY3AvIn0=" class="button button--primary">Add to Cursor</a>

**Manual Setup:**

1. Open Cursor Settings (Cmd/Ctrl + ,)
2. Go to **Features** → **MCP**
3. Click **Add Server**
4. Enter:
   - Name: `MCPProxy`
   - URL: `http://localhost:8080/mcp`

</TabItem>
<TabItem value="claude-desktop" label="Claude Desktop">

Add MCPProxy to your Claude Desktop configuration file:

**macOS:** `~/Library/Application Support/Claude/claude_desktop_config.json`
**Windows:** `%APPDATA%\Claude\claude_desktop_config.json`

```json
{
  "mcpServers": {
    "mcpproxy": {
      "command": "npx",
      "args": [
        "-y",
        "mcp-remote",
        "http://localhost:8080/mcp"
      ]
    }
  }
}
```

After saving, restart Claude Desktop to apply the changes.

:::info Why mcp-remote?
Claude Desktop doesn't natively support HTTP MCP servers yet. The `mcp-remote` package bridges this gap by proxying the HTTP connection through stdio.
:::

</TabItem>
<TabItem value="claude-code" label="Claude Code CLI">

Add MCPProxy with a single command:

```bash
claude mcp add --transport http mcpproxy http://localhost:8080/mcp
```

Verify it was added:

```bash
claude mcp list
```

</TabItem>
</Tabs>

:::tip Multiple Clients
You can connect multiple AI clients to the same MCPProxy instance simultaneously. All clients share the same upstream servers and configuration.
:::

## 5. Verify Connection

In your AI client, ask it to list available tools. You should see tools from all your configured MCP servers.

## Next Steps

- Learn about [configuration options](/configuration/config-file)
- Set up [upstream servers](/configuration/upstream-servers)
- Explore [CLI commands](/cli/command-reference)

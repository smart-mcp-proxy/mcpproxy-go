---
id: agent-install
title: Install with your AI agent
sidebar_label: Install with your agent
description: "Paste one prompt into Claude Code or Codex and let it install, start and connect MCPProxy for you"
keywords: [agent, claude code, codex, install, prompt, llms, automation]
---

# Install with your AI agent

Paste the prompt below into **Claude Code**, **Codex** or any coding agent that can run shell commands. The agent installs MCPProxy for your operating system, starts it, checks that it is healthy, and registers MCPProxy as an MCP server in the client it is running in.

The prompt is written so the agent **stops and asks you** before it replaces or restarts anything that is already running, and it **never prints your config file or any API key**.

<!-- agent-install-prompt:start -->
```text
Install MCPProxy (https://mcpproxy.app), start it, and connect it to the AI client you are running in. Work step by step and show me each command before you run it.

RULES
- Do not read, print or paste ~/.mcpproxy/mcp_config.json, any API key, token or credential. Use the commands below, which return masked output.
- If MCPProxy is already installed or running (step 1), STOP and ask me what to do. Never reinstall over a running instance.
- Do not use sudo unless a step needs it; tell me first.
- Install only from the sources listed here and verify checksums where stated.

1. CHECK FOR AN EXISTING INSTALL
   Run: command -v mcpproxy   (Windows PowerShell: Get-Command mcpproxy)
   Then: mcpproxy status      (it reports whether an instance is running)
   If either shows MCPProxy is present or running, report that and ask me before doing anything else.

2. INSTALL (pick the first that applies to this OS)
   macOS / Linux with Homebrew:   brew install smart-mcp-proxy/mcpproxy/mcpproxy
   Windows with winget:           winget install --id smart-mcp-proxy.mcpproxy -e
   Windows with scoop:            scoop bucket add mcpproxy https://github.com/smart-mcp-proxy/scoop-bucket ; scoop install mcpproxy
   Otherwise (Linux without Homebrew, or no package manager), the release archive:
     VER=$(curl -fsSL https://api.github.com/repos/smart-mcp-proxy/mcpproxy-go/releases/latest | sed -n 's/.*"tag_name": *"v\([^"]*\)".*/\1/p' | head -1)
     OS=linux; ARCH=$(uname -m | sed 's/x86_64/amd64/;s/aarch64/arm64/')    # use OS=darwin on macOS
     cd "$(mktemp -d)"
     curl -fsSLO "https://github.com/smart-mcp-proxy/mcpproxy-go/releases/download/v${VER}/mcpproxy-${VER}-${OS}-${ARCH}.tar.gz"
     curl -fsSLO "https://github.com/smart-mcp-proxy/mcpproxy-go/releases/download/v${VER}/checksums.txt"
     grep " mcpproxy-${VER}-${OS}-${ARCH}.tar.gz\$" checksums.txt | (sha256sum -c - 2>/dev/null || shasum -a 256 -c -)
     tar xzf "mcpproxy-${VER}-${OS}-${ARCH}.tar.gz" && mkdir -p ~/.local/bin && install -m 0755 mcpproxy ~/.local/bin/mcpproxy
     mkdir -p ~/.mcpproxy && echo curl > ~/.mcpproxy/install-channel     # records the install channel only
     (Make sure ~/.local/bin is on PATH; the checksum line must print "OK" or STOP.)
   Prefer the macOS desktop app? Tell me; it is: brew install --cask smart-mcp-proxy/mcpproxy/mcpproxy

3. START IT
   mcpproxy serve --listen 127.0.0.1:8080 > ~/.mcpproxy/serve.log 2>&1 &     # Windows: Start-Process mcpproxy -ArgumentList serve -WindowStyle Hidden
   If port 8080 is taken, use another, e.g. --listen 127.0.0.1:18080.
   It must keep running after your command returns: start it with your tool's background/long-running mode rather than relying on a trailing `&`, which some agent shells kill when the call ends.
   Wait until `mcpproxy status` reports it is running (retry for up to 30 seconds).

4. VERIFY (these commands print masked output)
   mcpproxy status
   mcpproxy doctor

5. CONNECT THE CLIENT YOU ARE RUNNING IN
   Claude Code: mcpproxy connect claude-code
   Codex:       mcpproxy connect codex
   Others (cursor, windsurf, vscode, gemini, opencode): mcpproxy connect <client>
   Claude Desktop is not in this list: tell me to open the Connect wizard in the MCPProxy Web UI (the URL printed by `mcpproxy status --web-url`).
   Then run: mcpproxy connect --list   and confirm the client shows as connected.

6. TELL ME WHAT HAPPENED
   - What you installed and how, and the Web UI URL from `mcpproxy status --web-url`.
   - That the client needs a reload or restart before it sees MCPProxy (Claude Code: start a new session or run /mcp; Codex: restart it). Do not claim it is connected until I have done that.
   - MCPProxy sends anonymous usage telemetry. To turn it off: set MCPPROXY_TELEMETRY=false before starting it, or toggle it in Settings. Details: https://docs.mcpproxy.app/features/telemetry
```
<!-- agent-install-prompt:end -->

## What the prompt does

| Step | Why |
|------|-----|
| Existing-install check | Never overwrites a running MCPProxy or the tray app that already owns port 8080 |
| Package manager first | Homebrew, winget and scoop keep MCPProxy updatable with their normal upgrade command |
| Checksum-verified archive fallback | Used only when no package manager applies; the archive's SHA-256 is checked against the release's `checksums.txt` |
| `mcpproxy connect` | Writes a per-client credential for you and returns it masked; the agent never handles your admin API key |
| Truthful follow-up | Clients load MCP servers at start, so the agent asks you to reload instead of claiming a live connection |

## Install without an agent

See [Installation](/getting-started/installation) for the macOS app, Windows installer, winget, scoop, Linux packages and Docker.

## For agents and tools

This site publishes [`/llms.txt`](https://docs.mcpproxy.app/llms.txt) (an index of the documentation, starting with this page) and [`/llms-full.txt`](https://docs.mcpproxy.app/llms-full.txt) (the full text).

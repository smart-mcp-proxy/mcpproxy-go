---
id: installation
title: Installation
sidebar_label: Installation
sidebar_position: 1
description: Install MCPProxy on macOS, Windows, or Linux
keywords: [install, setup, homebrew, dmg, windows, linux]
---

# Installation

MCPProxy can be installed on macOS, Windows, and Linux. Choose the installation method that works best for your platform.

## macOS

### DMG Installer (Recommended)

Download the latest `.dmg` file from the [releases page](https://github.com/smart-mcp-proxy/mcpproxy-go/releases) and drag MCPProxy to your Applications folder.

The DMG installers are signed and notarized by Apple.

### Homebrew

```bash
brew tap smart-mcp-proxy/mcpproxy
brew install mcpproxy
```


## Windows

### Installer (Recommended)

Download the latest Windows installer (`.exe`) from the [releases page](https://github.com/smart-mcp-proxy/mcpproxy-go/releases).

The installer will:
- Install MCPProxy to `%LOCALAPPDATA%\Programs\mcpproxy`
- Add MCPProxy to your system PATH
- Create Start Menu shortcuts

### Manual Installation

1. Download the Windows binary from the releases page
2. Extract to a directory of your choice
3. Add the directory to your PATH

## Linux

### Binary Download

Download the appropriate binary for your architecture from the [releases page](https://github.com/smart-mcp-proxy/mcpproxy-go/releases):

- `mcpproxy-linux-amd64` for x86_64
- `mcpproxy-linux-arm64` for ARM64

```bash
# Download and install
curl -L https://github.com/smart-mcp-proxy/mcpproxy-go/releases/latest/download/mcpproxy-linux-amd64 -o mcpproxy
chmod +x mcpproxy
sudo mv mcpproxy /usr/local/bin/
```

## Verify Installation

After installation, verify MCPProxy is working:

```bash
mcpproxy --version
```

## Next Steps

Once installed, proceed to the [Quick Start](/getting-started/quick-start) guide to configure and run MCPProxy.

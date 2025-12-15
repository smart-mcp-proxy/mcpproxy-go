---
id: building
title: Building
sidebar_label: Building
sidebar_position: 3
description: How to build MCPProxy from source
keywords: [build, compile, development]
---

# Building MCPProxy

This document covers building MCPProxy from source.

## Prerequisites

- Go 1.21+
- Node.js 20+ (for web UI and E2E tests)
- CGO enabled for tray application (macOS/Linux)

## Quick Build

```bash
# Core server (headless)
go build -o mcpproxy ./cmd/mcpproxy

# Tray application (requires CGO on macOS)
CGO_ENABLED=1 go build -o mcpproxy-tray ./cmd/mcpproxy-tray

# Both with make
make build
```

## Platform-Specific Builds

### macOS

```bash
# Intel
GOOS=darwin GOARCH=amd64 go build -o mcpproxy-darwin-amd64 ./cmd/mcpproxy

# Apple Silicon
GOOS=darwin GOARCH=arm64 go build -o mcpproxy-darwin-arm64 ./cmd/mcpproxy

# Tray (requires CGO)
GOOS=darwin CGO_ENABLED=1 go build -o mcpproxy-tray ./cmd/mcpproxy-tray
```

### Windows

```bash
# AMD64
GOOS=windows GOARCH=amd64 go build -o mcpproxy.exe ./cmd/mcpproxy

# ARM64
GOOS=windows GOARCH=arm64 go build -o mcpproxy.exe ./cmd/mcpproxy

# Tray (no CGO needed on Windows)
GOOS=windows go build -o mcpproxy-tray.exe ./cmd/mcpproxy-tray
```

### Linux

```bash
# AMD64
GOOS=linux GOARCH=amd64 go build -o mcpproxy-linux-amd64 ./cmd/mcpproxy

# ARM64
GOOS=linux GOARCH=arm64 go build -o mcpproxy-linux-arm64 ./cmd/mcpproxy
```

## Cross-Platform Build Script

```bash
# Build for all platforms
./scripts/build.sh
```

## Windows Installer

```powershell
# Using Inno Setup (recommended)
.\scripts\build-windows-installer.ps1 -Version "v1.0.0" -Arch "amd64"

# Using WiX Toolset
wix build -arch x64 -d Version=1.0.0.0 -d BinPath=dist\windows-amd64 wix\Package.wxs
```

## Icon Generation

```bash
# Generate all icon files (PNG for macOS/Linux, ICO for Windows)
./scripts/logo-convert.sh

# Generate Windows ICO only
python3 scripts/create-ico.py
```

## Build Tags

The tray application uses build tags:

- `tray_gui.go` - GUI implementation (default)
- `tray_stub.go` - Stub for headless builds

## Development Mode

```bash
# Run with hot reload (requires air)
air

# Or manually rebuild and run
go build -o mcpproxy ./cmd/mcpproxy && ./mcpproxy serve
```

## Verifying Build

```bash
# Check version
./mcpproxy --version

# Run health check
./mcpproxy doctor
```

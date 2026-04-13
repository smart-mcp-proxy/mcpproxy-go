---
id: installation
title: Installation
sidebar_label: Installation
sidebar_position: 1
description: Install MCPProxy on macOS, Windows, or Linux
keywords: [install, setup, homebrew, dmg, windows, linux, deb, rpm, apt, dnf]
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

### Debian / Ubuntu (.deb)

Download the latest `.deb` from the [releases page](https://github.com/smart-mcp-proxy/mcpproxy-go/releases) and install it with `apt`.

**One-liner (auto-detects latest version):**

```bash
VERSION=$(curl -fsSL https://api.github.com/repos/smart-mcp-proxy/mcpproxy-go/releases/latest \
  | grep -oE '"tag_name": *"v[^"]+"' | sed -E 's/.*"v([^"]+)"/\1/')
ARCH=$(dpkg --print-architecture)   # amd64 or arm64
curl -fLO "https://github.com/smart-mcp-proxy/mcpproxy-go/releases/latest/download/mcpproxy_${VERSION}_${ARCH}.deb"
sudo apt install "./mcpproxy_${VERSION}_${ARCH}.deb"
```

**Or pin a specific version:**

```bash
# AMD64 (x86_64)
curl -LO https://github.com/smart-mcp-proxy/mcpproxy-go/releases/download/v0.24.2/mcpproxy_0.24.2_amd64.deb
sudo apt install ./mcpproxy_0.24.2_amd64.deb

# ARM64 (Raspberry Pi, AWS Graviton, etc.)
curl -LO https://github.com/smart-mcp-proxy/mcpproxy-go/releases/download/v0.24.2/mcpproxy_0.24.2_arm64.deb
sudo apt install ./mcpproxy_0.24.2_arm64.deb
```

The package installs `mcpproxy` to `/usr/bin`, ships a hardened systemd unit at `/lib/systemd/system/mcpproxy.service`, and creates a dedicated `mcpproxy` system user. The service is **enabled and started automatically** on first install. Subsequent upgrades preserve your config and `try-restart` the unit.

After install:

```bash
sudo systemctl status mcpproxy
sudo journalctl -u mcpproxy -f      # tail logs
sudo nano /etc/mcpproxy/mcp_config.json   # edit config (then restart)
sudo systemctl restart mcpproxy
```

### Fedora / RHEL / CentOS / openSUSE (.rpm)

**One-liner (auto-detects latest version):**

```bash
VERSION=$(curl -fsSL https://api.github.com/repos/smart-mcp-proxy/mcpproxy-go/releases/latest \
  | grep -oE '"tag_name": *"v[^"]+"' | sed -E 's/.*"v([^"]+)"/\1/')
ARCH=$(uname -m)   # x86_64 or aarch64
curl -fLO "https://github.com/smart-mcp-proxy/mcpproxy-go/releases/latest/download/mcpproxy-${VERSION}-1.${ARCH}.rpm"
sudo dnf install "./mcpproxy-${VERSION}-1.${ARCH}.rpm"
```

**Or pin a specific version:**

```bash
# AMD64 (x86_64)
curl -LO https://github.com/smart-mcp-proxy/mcpproxy-go/releases/download/v0.24.2/mcpproxy-0.24.2-1.x86_64.rpm
sudo dnf install ./mcpproxy-0.24.2-1.x86_64.rpm

# ARM64 (aarch64)
curl -LO https://github.com/smart-mcp-proxy/mcpproxy-go/releases/download/v0.24.2/mcpproxy-0.24.2-1.aarch64.rpm
sudo dnf install ./mcpproxy-0.24.2-1.aarch64.rpm
```

The same `systemctl` workflow applies. On systems without `dnf`, use `sudo rpm -i ./mcpproxy-*.rpm` or `sudo zypper install ./mcpproxy-*.rpm`.

### Tarball (any distro)

If you don't want a system service or you're on a distro without `.deb`/`.rpm` support, grab the raw binary tarball:

```bash
# AMD64
curl -LO https://github.com/smart-mcp-proxy/mcpproxy-go/releases/latest/download/mcpproxy-latest-linux-amd64.tar.gz
tar -xzf mcpproxy-latest-linux-amd64.tar.gz
sudo install -m 0755 mcpproxy /usr/local/bin/

# ARM64
curl -LO https://github.com/smart-mcp-proxy/mcpproxy-go/releases/latest/download/mcpproxy-latest-linux-arm64.tar.gz
tar -xzf mcpproxy-latest-linux-arm64.tar.gz
sudo install -m 0755 mcpproxy /usr/local/bin/
```

You can then run `mcpproxy serve` directly, or wire up your own systemd unit modelled on the one shipped in the `.deb`/`.rpm`.

### Package layout (.deb / .rpm)

| Path | Purpose |
|------|---------|
| `/usr/bin/mcpproxy` | Binary |
| `/lib/systemd/system/mcpproxy.service` | Hardened systemd unit (runs as `mcpproxy` user) |
| `/etc/mcpproxy/mcp_config.json` | Config (`config|noreplace` — never overwritten on upgrade) |
| `/etc/mcpproxy/mcp_config.json.example` | Reference example, refreshed on upgrade |
| `/var/lib/mcpproxy/` | Data dir (BBolt DB, search index) |
| `/var/log/mcpproxy/` | Log directory |
| `/usr/share/doc/mcpproxy/{LICENSE,README.md}` | Documentation |

The systemd unit launches mcpproxy with `--config=/etc/mcpproxy/mcp_config.json --data-dir=/var/lib/mcpproxy` and uses `NoNewPrivileges`, `ProtectSystem=strict`, `PrivateTmp`, and friends.

## Verify Installation

After installation, verify MCPProxy is working:

```bash
mcpproxy --version
```

## Next Steps

Once installed, proceed to the [Quick Start](/getting-started/quick-start) guide to configure and run MCPProxy.

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

### Debian / Ubuntu — apt repository (recommended)

Add the MCPProxy apt repository once; `apt upgrade` handles updates from then on, like any other system package.

```bash
sudo install -m 0755 -d /etc/apt/keyrings
curl -fsSL https://apt.mcpproxy.app/mcpproxy.gpg \
  | sudo tee /etc/apt/keyrings/mcpproxy.gpg > /dev/null
echo "deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/mcpproxy.gpg] https://apt.mcpproxy.app stable main" \
  | sudo tee /etc/apt/sources.list.d/mcpproxy.list > /dev/null
sudo apt update
sudo apt install mcpproxy
```

Supported architectures: `amd64`, `arm64`. See [Linux Package Repositories](../features/linux-package-repos.md) for details on retention, pinning older versions, mirroring, and troubleshooting.

Repository signing key fingerprint: `3B6F A1AD 5D53 59DA 51F1  8DDC E1B5 9B9B A1CB 8A3B`. You can verify it with `gpg --show-keys` against the public key URL above.

### Fedora / RHEL / Rocky / AlmaLinux — dnf repository (recommended)

```bash
sudo dnf config-manager --add-repo https://rpm.mcpproxy.app/mcpproxy.repo
sudo dnf install -y mcpproxy
```

Supported architectures: `x86_64`, `aarch64`.

### Debian / Ubuntu — direct `.deb` download (fallback)

If the apt repository isn't reachable (air-gapped installs, behind corporate proxies blocking `mcpproxy.app`, etc.), download the `.deb` from the [releases page](https://github.com/smart-mcp-proxy/mcpproxy-go/releases) and install it locally.

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

### Network exposure: localhost by default

Both `.deb` and `.rpm` packages ship a default config that binds **only to `127.0.0.1:8080`** — meaning the service is reachable only from the same machine. This is intentional: MCPProxy proxies tools that can read your filesystem, call paid APIs, and execute code, so a wide-open default would be unsafe.

You can confirm the default in the shipped example:

```bash
cat /etc/mcpproxy/mcp_config.json
# {
#   "listen": "127.0.0.1:8080",
#   ...
# }
```

#### Reaching it from another host on your LAN

If you're installing on a server (a homelab box, a VPS, a Raspberry Pi) and you want other machines to connect to it, you need to do **two** things:

1. **Switch the listen address** from `127.0.0.1:8080` to `0.0.0.0:8080` (all interfaces) or to a specific LAN IP.
2. **Set an `api_key`** so the REST API and tray-style endpoints aren't anonymously accessible. MCPProxy auto-generates one on first start if you leave the field empty, but for a network-exposed install you should set it explicitly to a strong random value.

Example:

```bash
# Generate a strong random API key
API_KEY=$(openssl rand -hex 32)

# Edit the config
sudo tee /etc/mcpproxy/mcp_config.json >/dev/null <<EOF
{
  "listen": "0.0.0.0:8080",
  "api_key": "${API_KEY}",
  "data_dir": "/var/lib/mcpproxy",
  "enable_socket": true,
  "enable_web_ui": true,
  "require_mcp_auth": false,
  "mcpServers": []
}
EOF

# Restart so the new bind takes effect
sudo systemctl restart mcpproxy

# From another machine on the LAN:
curl -H "X-API-Key: ${API_KEY}" http://<server-ip>:8080/api/v1/status
```

:::caution Security checklist before exposing on a LAN

- **Always set a non-empty `api_key`.** The REST API enforces it, but a network-reachable instance with an obvious or empty key is one `curl` away from full tool execution.
- **Put a firewall in front.** If only one workstation needs access, prefer binding to a single LAN IP (e.g. `"listen": "192.168.1.10:8080"`) or restrict port 8080 in `ufw`/`firewalld` to known source addresses.
- **Consider `require_mcp_auth: true`** if you also want the `/mcp` endpoint to require the API key. It's `false` by default for AI-client compatibility.
- **Don't expose to the public internet without TLS in front.** Run nginx/Caddy/Traefik as a reverse proxy with HTTPS, or tunnel via Tailscale/WireGuard/SSH. The built-in HTTP server is not designed to be a public ingress.

:::

For a deeper dive on auth, agent tokens, and the security model, see the [Configuration Reference](/configuration/config-file) and the [REST API reference](/api/rest-api).

### Fedora / RHEL / CentOS / openSUSE — direct `.rpm` download (fallback)

For air-gapped or offline installs where the dnf repository isn't reachable.

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
| `/var/lib/mcpproxy/` | Data dir (BBolt DB, search index, per-server logs) |
| `/usr/share/doc/mcpproxy/{LICENSE,README.md}` | Documentation |

The systemd unit launches mcpproxy with `--config=/etc/mcpproxy/mcp_config.json --data-dir=/var/lib/mcpproxy` and uses `NoNewPrivileges`, `ProtectSystem=strict`, `PrivateTmp`, and friends.

## Verify Installation

After installation, verify MCPProxy is working:

```bash
mcpproxy --version
```

## Next Steps

Once installed, proceed to the [Quick Start](/getting-started/quick-start) guide to configure and run MCPProxy.

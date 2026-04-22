---
id: linux-package-repos
title: Linux Package Repositories
sidebar_label: Linux package repos
sidebar_position: 5
description: How the apt.mcpproxy.app and rpm.mcpproxy.app repositories work — retention, pinning, mirroring, troubleshooting.
---

# Linux Package Repositories

MCPProxy hosts signed apt and yum repositories so Linux users can install and update like any other first-class system package.

- Debian / Ubuntu: `https://apt.mcpproxy.app` (suite `stable`, component `main`, arches `amd64` + `arm64`)
- Fedora / RHEL / Rocky / Alma: `https://rpm.mcpproxy.app` (arches `x86_64` + `aarch64`)

See [Installation → Linux](../getting-started/installation.md#linux) for the one-time setup commands.

## How the repositories are hosted

The apt and yum metadata + packages are served as static objects from Cloudflare R2 buckets bound to `apt.mcpproxy.app` and `rpm.mcpproxy.app`. There is no dynamic server layer; the repositories are refreshed by a GitHub Actions job on every release tag.

Repository contents are signed with a dedicated GPG key (separate from any other project keys).

**Signing key fingerprint**: `3B6F A1AD 5D53 59DA 51F1  8DDC E1B5 9B9B A1CB 8A3B`

You can verify it against the key served at `https://apt.mcpproxy.app/mcpproxy.gpg`:

```bash
curl -fsSL https://apt.mcpproxy.app/mcpproxy.gpg | gpg --show-keys --with-fingerprint
```

The output's primary-key fingerprint must match the one above. If it doesn't, do not trust the repository — report it via `mcpproxy feedback` or open a GitHub issue.

## Retention

Each repository retains the **last 10 stable versions** per architecture. On every new release, the publish job prunes artifacts older than the retention window. Pruned versions remain downloadable directly from the corresponding [GitHub Release](https://github.com/smart-mcp-proxy/mcpproxy-go/releases) as individual `.deb` / `.rpm` files.

## Pinning a specific version

While a version is still within the retention window, you can pin it:

```bash
# Debian / Ubuntu
sudo apt install mcpproxy=0.24.7

# Fedora / RHEL
sudo dnf install mcpproxy-0.24.7-1
```

Use `apt-cache madison mcpproxy` or `dnf --showduplicates list mcpproxy` to see what versions are currently available in the repo. For versions older than the retention window, download the `.deb` / `.rpm` from GitHub Releases directly.

## Mirroring for air-gapped environments

Both repositories are plain HTTPS static sites. Standard tools work for mirroring:

```bash
# apt: mirror to a local directory
apt-mirror  # with /etc/apt/mirror.list containing:
#   deb-amd64 https://apt.mcpproxy.app stable main
#   deb-arm64 https://apt.mcpproxy.app stable main

# rpm: use reposync
sudo dnf install -y dnf-utils
reposync --repoid=mcpproxy --download-path=/srv/mirror/mcpproxy
```

Serve the mirror from any local HTTPS server, swap the `baseurl` / `deb` line for your mirror URL, and you're set.

## Channel policy

Only the **stable** channel is published today. Prerelease builds (e.g. tagged `v*-rc1`) are not added to the repository — they're still available as artifacts on the corresponding prerelease GitHub Release.

If a prerelease channel is added later, it will use a parallel URL structure (`https://apt.mcpproxy.app` suite `prerelease`, `https://rpm.mcpproxy.app/prerelease`) so existing `stable` users are unaffected.

## Troubleshooting

### `NO_PUBKEY` or `signature couldn't be verified`

Your system doesn't have the MCPProxy signing key installed or cached. Re-run the key installation step:

```bash
# Debian / Ubuntu
curl -fsSL https://apt.mcpproxy.app/mcpproxy.gpg \
  | sudo tee /etc/apt/keyrings/mcpproxy.gpg > /dev/null

# Fedora / RHEL
sudo rpm --import https://rpm.mcpproxy.app/mcpproxy.gpg
```

### `404` on `https://apt.mcpproxy.app/dists/stable/Release` just after a release

Cloudflare's edge cache is `max-age=300` (5 minutes) on metadata files, so you might see the old state briefly. Wait up to 5 minutes and retry. If it persists longer than 30 minutes after a tag push, the publish job may have failed — check the Actions tab of the repository.

### `apt update` reports `Packages file is expired`

The `Release` file has a recency check. If system clock is badly off, it can fail. Check `date` and `timedatectl` first. If the system clock is correct, the repository's `Release` file is genuinely old — the publish job may have stopped; open an issue.

### `Hash sum mismatch`

Usually means a stale CDN edge cache. Run `sudo apt clean && sudo apt update` to force a fresh fetch. If it persists, reach out via `mcpproxy feedback` — there's either a genuine bug in the publish pipeline or a transient bucket-sync race.

### `mcpproxy` appears outdated after `apt upgrade`

`apt upgrade` won't cross a major-version boundary that introduces new dependencies without explicit operator consent. Use `apt full-upgrade` to let it update across such boundaries.

## Related docs

- [Installation guide (Linux section)](../getting-started/installation.md#linux) — user-facing install commands.
- [Linux Package Repositories — Operations](../operations/linux-package-repos-infrastructure.md) — maintainer runbook (key rotation, manual republish, purge).

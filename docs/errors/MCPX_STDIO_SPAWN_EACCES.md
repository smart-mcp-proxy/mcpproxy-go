---
id: MCPX_STDIO_SPAWN_EACCES
title: MCPX_STDIO_SPAWN_EACCES
sidebar_label: SPAWN_EACCES
description: The OS denied permission to execute the stdio MCP server command.
---

# `MCPX_STDIO_SPAWN_EACCES`

**Severity:** error
**Domain:** STDIO

## What happened

The configured command exists on `PATH`, but the OS refused to execute it
(`EACCES`). The subprocess never started.

## Common causes

- The file is missing the executable bit (`chmod +x`).
- The file is on a noexec-mounted volume (e.g. some `/tmp` or NFS mounts).
- macOS Gatekeeper / quarantine flagged a downloaded binary.
- AppArmor / SELinux denied the exec syscall.

## How to fix

### Restore the executable bit

```bash
ls -l "$(which <command>)"        # check current mode
chmod +x "$(which <command>)"     # add execute permission
```

### macOS quarantine

If the binary was downloaded outside the App Store / Homebrew, macOS may have
quarantined it:

```bash
xattr -d com.apple.quarantine "$(which <command>)"
```

For pre-built tray bundles, prefer the signed/notarised DMG — see
[Installation](../getting-started/installation.md).

### Move off a noexec volume

```bash
mount | grep noexec
# Reinstall the tool to ~/.local/bin or /usr/local/bin instead of /tmp
```

### AppArmor / SELinux

Check the audit log for `DENIED exec`:

```bash
sudo dmesg | grep -i denied
sudo journalctl -t audit | grep DENIED
```

Adjust the policy or run the upstream server in [Docker isolation](../features/docker-isolation.md)
to sidestep host policy.

## Related

- [`MCPX_STDIO_SPAWN_ENOENT`](MCPX_STDIO_SPAWN_ENOENT.md) — file does not exist
- [Docker Isolation](../features/docker-isolation.md)

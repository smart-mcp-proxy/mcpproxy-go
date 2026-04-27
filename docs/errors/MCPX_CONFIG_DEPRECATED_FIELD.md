---
id: MCPX_CONFIG_DEPRECATED_FIELD
title: MCPX_CONFIG_DEPRECATED_FIELD
sidebar_label: DEPRECATED_FIELD
description: Your mcpproxy config uses a deprecated field that will be removed in a future release.
---

# `MCPX_CONFIG_DEPRECATED_FIELD`

**Severity:** warn
**Domain:** Config

## What happened

mcpproxy parsed your config (`~/.mcpproxy/mcp_config.json`) successfully, but
detected a field that has been renamed or replaced. The current release still
honours the old field; a future release will not.

## How to fix

### Preview the migration

The error panel includes a **Preview migration (dry-run)** fix-step. CLI:

```bash
mcpproxy config migrate --dry-run
```

This prints the diff that would be applied. Review it, then apply:

```bash
mcpproxy config migrate
```

A `.bak` is written next to the original file.

### Manual migration

If you prefer to edit the file by hand, the warning message names the exact
field. The most common renames:

| Old | New |
|---|---|
| `mcpServers[].docker_isolation` (bool) | `mcpServers[].isolation.enabled` |
| `top_k` (deprecated) | `tools_limit` |
| `enable_quarantine` | `quarantine_enabled` |

### Suppress the warning until you migrate

Deprecated fields keep working. If you can't migrate today, the warning is
informational — there's no operational impact in this release.

## Related

- [Configuration → config file](../configuration/config-file.md)

---
id: MCPX_CONFIG_PARSE_ERROR
title: MCPX_CONFIG_PARSE_ERROR
sidebar_label: PARSE_ERROR
description: mcpproxy could not parse the configuration file as JSON.
---

# `MCPX_CONFIG_PARSE_ERROR`

**Severity:** error
**Domain:** Config

## What happened

mcpproxy reads `~/.mcpproxy/mcp_config.json` (or the path you configured) on
startup and on every change. The file failed JSON parsing — usually a
trailing comma, an unquoted key, or a stray character.

mcpproxy refuses to start with a broken config rather than silently loading a
partial state.

## How to fix

### Validate the JSON

```bash
jq . ~/.mcpproxy/mcp_config.json
```

`jq` will print the line/column of the first parse error. Common offenders:

- Trailing comma after the last item in an array or object.
- Smart quotes pasted from a doc tool instead of `"`.
- Comment lines (JSON has no comments).
- Two top-level objects without an enclosing wrapper.

### Restore from backup

mcpproxy writes `.bak` files when applying migrations / web-UI edits:

```bash
ls ~/.mcpproxy/*.bak
cp ~/.mcpproxy/mcp_config.json.bak ~/.mcpproxy/mcp_config.json
```

### Edit via the web UI

If you're not comfortable hand-editing JSON, edit through the web UI: it
applies changes atomically and never leaves the file in a broken state.

## Related

- [Configuration → config file](../configuration/config-file.md)
- [`MCPX_CONFIG_MISSING_SECRET`](MCPX_CONFIG_MISSING_SECRET.md)

---
id: MCPX_CONFIG_MISSING_SECRET
title: MCPX_CONFIG_MISSING_SECRET
sidebar_label: MISSING_SECRET
description: An upstream config refers to a secret that does not exist in the secret store.
---

# `MCPX_CONFIG_MISSING_SECRET`

**Severity:** error
**Domain:** Config

## What happened

The upstream config references a secret (typically via `${SECRET:NAME}` in
`env` or `headers`), but the secret was not found in the active secret store
when mcpproxy tried to start that server.

## How to fix

### List what's defined

```bash
mcpproxy secret list
```

If the name you referenced isn't there, add it.

### Add the missing secret

```bash
mcpproxy secret set <NAME>           # prompts for the value, never echoed
```

Secrets are stored in the OS keychain on macOS / Windows and in an encrypted
file on Linux.

### Check the reference syntax

```jsonc
{
  // env values support ${SECRET:NAME} expansion:
  "env": { "GITHUB_TOKEN": "${SECRET:github_personal_token}" },

  // headers do too:
  "headers": { "Authorization": "Bearer ${SECRET:my_api_key}" }
}
```

Plain string values are used as-is (no expansion).

### Migration from inline secrets

If you previously stored secrets inline and want to move them into the secret
store:

```bash
mcpproxy secret set github_personal_token < /dev/stdin <<<"ghp_xxx"
# Then update the config to use ${SECRET:github_personal_token}
```

## Related

- [Configuration → environment variables](../configuration/environment-variables.md)
- [`MCPX_CONFIG_PARSE_ERROR`](MCPX_CONFIG_PARSE_ERROR.md)

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

The upstream config references a secret (typically via `${keyring:NAME}` in
`env` or `headers`), but the secret was not found in the active secret store
when mcpproxy tried to start that server.

### HTTP upstreams: nothing is sent

When the missing reference is in a remote server's `url`, `headers` or `oauth`
settings, mcpproxy does not connect, sign in or refresh a token for that
server, so the raw `${keyring:NAME}` text never reaches the upstream. The
server shows **Missing secret** with the reference name, and the error names
the field, for example:

```text
unresolved secret reference ${keyring:my_api_key} in Headers[Authorization]: the secret is not available, so nothing was sent to the upstream. Add it with: mcpproxy secrets set my_api_key, then restart the server (mcpproxy upstream restart my-server)
```

Other servers are not affected. A missing reference in a local (stdio)
server's `env`, `args` or `working_dir` is logged and the value is used as
written; a stdio server never sends its `headers`, `url` or `oauth` values, so
a reference there does not stop it either.

This also applies to `${env:NAME}` references and to any text in those fields
that has the `${type:name}` form: earlier versions sent them literally when
they could not be resolved, and mcpproxy now refuses instead. A `${env:NAME}`
reference reads mcpproxy's own environment, so set the variable and restart
mcpproxy (or its service) rather than the server.

## How to fix

### List what's defined

```bash
mcpproxy secrets list
```

If the name you referenced isn't there, add it.

Secrets are resolved when the server's connection is created. Storing a
secret through the Web UI or REST API restarts the enabled servers that use it;
after `mcpproxy secrets set`, restart the server:

```bash
mcpproxy upstream restart <server>
```

### Add the missing secret

```bash
mcpproxy secrets set <NAME>          # prompts; typed characters are echoed in the terminal
```

Secrets are stored in the OS keychain on macOS / Windows and in an encrypted
file on Linux.

### Check the reference syntax

```jsonc
{
  // env values support ${keyring:NAME} expansion:
  "env": { "GITHUB_TOKEN": "${keyring:github_personal_token}" },

  // headers do too:
  "headers": { "Authorization": "Bearer ${keyring:my_api_key}" }
}
```

Plain string values are used as-is (no expansion).

### Migration from inline secrets

If you previously stored secrets inline and want to move them into the secret
store:

```bash
mcpproxy secrets set github_personal_token
# Enter the value at the prompt (terminal input is echoed).
# Then update the config to use ${keyring:github_personal_token}
```

## Related

- [Configuration → environment variables](../configuration/environment-variables.md)
- [`MCPX_CONFIG_PARSE_ERROR`](MCPX_CONFIG_PARSE_ERROR.md)

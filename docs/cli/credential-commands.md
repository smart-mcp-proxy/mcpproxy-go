---
title: "Credential Commands"
sidebar_label: "Credential Commands"
description: "Server edition: manage per-user stored credentials for shared upstream servers — stored, not injected."
---

# Credential CLI Commands (Server Edition)

**A stored credential is kept for a future broker and is NOT injected into upstream calls in this release.** Connecting an upstream records a credential tied to your user; tool calls to that upstream still go out with the server's shared identity. Every human-readable `credential list` / `credential status` output begins with this statement.

> **Server edition only.** These commands are built into `mcpproxy-server`
> (`go build -tags server`). They are not present in the personal edition.

The `mcpproxy credential` command group manages your **per-user stored
credentials** for shared upstream servers that carry an `auth_broker` block
with `mode: oauth_connect` (see [Auth Broker](../features/auth-broker.md)).
Each user connects their own credential through a browser consent flow; the
proxy stores it encrypted, per user, and reports its status here.

Secret values (access/refresh tokens) are **never displayed** by these
commands (FR-026). The CLI decodes responses into a non-secret view, so even a
misbehaving server cannot cause a token to be printed.

## Connecting to the server

These surfaces sit behind session-or-Bearer authentication (not the API-key
group), so the CLI targets a server URL and presents a **user JWT**:

| Setting | Flag | Environment variable | Default |
|---------|------|----------------------|---------|
| Server base URL | `--url` | `MCPPROXY_SERVER_URL` | local listen address (`http://<listen>`) |
| User token (JWT) | `--token` | `MCPPROXY_TOKEN` | _none_ |

Obtain a token from the Web UI after signing in, or via
`POST /api/v1/auth/token`. The `connect` subcommand does **not** need a token —
it prints a URL you open in a browser where you are already signed in.

```bash
export MCPPROXY_SERVER_URL=https://mcp.example.com
export MCPPROXY_TOKEN=eyJ...        # your user JWT
```

## Commands

### `credential list`

List every connectable upstream with your stored-credential status. No secrets.

```bash
mcpproxy credential list
mcpproxy credential list -o json
```

```
Stored credentials are kept for a future broker and are NOT injected into upstream calls in this release.

SERVER                   MODE             STATUS          TOKEN      EXPIRES
------------------------------------------------------------------------------------------
github                   oauth_connect    connected       Bearer     2026-07-01 12:00
jira                     oauth_connect    not_connected * -          -

* connectable: run 'mcpproxy credential connect <server>'
```

Status values: `connected` (a valid, non-expired credential is stored — it
says nothing about what upstream calls carry), `expired`, `not_connected`,
`unavailable` (the server's credential store is disabled because no
`credential_encryption_key` / `MCPPROXY_CRED_KEY` is configured).

### `credential status <server>`

Show the stored-credential detail for one upstream. The output opens with the
same "stored, not injected" statement.

```bash
mcpproxy credential status github
mcpproxy credential status github -o yaml
```

### `credential connect <server>`

Print the browser URL that starts the per-user OAuth connect flow. Open it in a
browser where you are signed in to mcpproxy; the proxy binds the flow to your
user and stores the resulting credential server-side. A stored credential is
not refreshed — once it reports `expired`, run `connect` again.

```bash
mcpproxy credential connect github
```

### `credential rm <server>`

Delete your stored credential for an upstream. Aliases: `remove`,
`disconnect`.

```bash
mcpproxy credential rm github
```

## Output formatting

All read commands honor the global `-o table|json|yaml` flag and the
`MCPPROXY_OUTPUT` environment variable (table is the default). With `json` or
`yaml` the "stored, not injected" statement goes to **stderr** so stdout stays
machine-parseable. The payload is the REST data with the envelope removed:
`credential list` emits the bare array from the REST `credentials` field and
`credential status <server>` emits that server's single object; every field
keeps its REST name and meaning, and no secret values are ever included.

## Related REST endpoints (spec 074 T8)

| Endpoint | Description |
|----------|-------------|
| `GET /api/v1/user/credentials` | List stored-credential status (no secrets) |
| `DELETE /api/v1/user/credentials/{server}` | Delete the stored credential |
| `GET /api/v1/user/credentials/{server}/connect` | Start the browser connect flow |
| `GET /api/v1/user/credentials/{server}/callback` | OAuth callback (browser) |

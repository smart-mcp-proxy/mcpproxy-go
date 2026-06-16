# Credential CLI Commands (Server Edition)

> **Server edition only.** These commands are built into `mcpproxy-server`
> (`go build -tags server`). They are not present in the personal edition.

The `mcpproxy credential` command group manages your **per-user brokered
credentials** for shared upstream servers that use the credential broker
(spec 074). Brokered upstreams carry an `auth_broker` block in the server
config; each user connects their own credential, and the proxy injects it at
call time.

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

List every brokered upstream with your connection status. No secrets.

```bash
mcpproxy credential list
mcpproxy credential list -o json
```

```
SERVER                   MODE             STATUS          TOKEN      EXPIRES
------------------------------------------------------------------------------------------
github                   oauth_connect    connected       Bearer     2026-07-01 12:00
jira                     oauth_connect    not_connected * -          -

* connectable: run 'mcpproxy credential connect <server>'
```

Status values: `connected`, `expired`, `not_connected`, `unavailable`
(the latter means the server's credential store is disabled).

### `credential status <server>`

Show the connection detail for one brokered upstream.

```bash
mcpproxy credential status github
mcpproxy credential status github -o yaml
```

### `credential connect <server>`

Print the browser URL that starts the per-user OAuth connect flow. Open it in a
browser where you are signed in to mcpproxy; the proxy binds the flow to your
user and stores the resulting credential server-side.

```bash
mcpproxy credential connect github
```

### `credential rm <server>`

Disconnect (revoke) your stored credential for an upstream. Aliases: `remove`,
`disconnect`.

```bash
mcpproxy credential rm github
```

## Output formatting

All read commands honor the global `-o table|json|yaml` flag and the
`MCPPROXY_OUTPUT` environment variable (table is the default).

## Related REST endpoints (spec 074 T8)

| Endpoint | Description |
|----------|-------------|
| `GET /api/v1/user/credentials` | List connection status (no secrets) |
| `DELETE /api/v1/user/credentials/{server}` | Disconnect/revoke |
| `GET /api/v1/user/credentials/{server}/connect` | Start the browser connect flow |
| `GET /api/v1/user/credentials/{server}/callback` | OAuth callback (browser) |

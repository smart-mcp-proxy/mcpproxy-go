---
id: compose-stdio-sidecars
title: Docker Compose with stdio servers in sidecars
sidebar_label: Compose with Sidecars
description: 'Run mcpproxy in Docker Compose with each stdio MCP server in its own sidecar container on an internal network, so a compromised stdio package cannot read the data volume, the API key or the OAuth token database.'
keywords: [docker compose, sidecar, stdio, supergateway, internal network, isolation, data volume, require_mcp_auth, file secrets, stop_grace_period]
---

# Docker Compose with stdio servers in sidecars

When mcpproxy runs a stdio MCP server itself, the server is a child process in the
same container as the data directory. A compromised or malicious stdio package can then
read `/data`, which holds the proxy's `api_key` and the OAuth token database. This page
shows a Compose layout that avoids that: each stdio server runs in **its own sidecar
container**, wrapped to streamable HTTP, on a network with no route out. mcpproxy
connects to it over HTTP like any other remote server.

The layout comes from [issue #1707](https://github.com/smart-mcp-proxy/mcpproxy-go/issues/1707),
contributed and tested by [@james-crowley](https://github.com/james-crowley). The ready-to-copy
files are in [`examples/compose-stdio-sidecars/`](https://github.com/smart-mcp-proxy/mcpproxy-go/tree/main/examples/compose-stdio-sidecars)
and CI starts the stack on every change to them.

## Layout

```
                 127.0.0.1:18080
                        │
   ┌─────────── front (normal bridge) ───────────┐
   │                    │                        │
   │               ┌────┴─────┐                  │
   │               │ mcpproxy │──── /data volume (this container only)
   │               └────┬─────┘                  │
   └────────────────────┼────────────────────────┘
   ┌────── back (internal: true, no route out) ──┐
   │                    │ http://everything:8000/mcp  + Bearer key
   │             ┌──────┴───────┐                │
   │             │  sidecar     │  supergateway --stateful
   │             │  (stdio srv) │  read-only, no capabilities
   │             └──────────────┘                │
   └─────────────────────────────────────────────┘
```

- **`front`** is a normal bridge. Only mcpproxy is on it, and its port is published on
  `127.0.0.1` only.
- **`back`** is `internal: true`: no gateway, no route to the internet. mcpproxy and the
  sidecars are on it; a sidecar is on no other network.
- The proxy's `/data` volume is mounted **only in the mcpproxy container**. No container
  gets the Docker socket.
- Each sidecar requires an API key, read from a Compose file secret. mcpproxy sends it
  as `Authorization: Bearer ${file:/run/secrets/sidecar_api_key}`.
- The sidecar runs read-only as an unprivileged user with `cap_drop: [ALL]` and
  `no-new-privileges`.

## Run it

```bash
cd examples/compose-stdio-sidecars
./gen-secrets.sh                 # creates ./secrets/{mcpproxy_api_key,sidecar_api_key}
docker compose up --build --wait
curl -H "X-API-Key: $(cat secrets/mcpproxy_api_key)" \
  http://127.0.0.1:18080/api/v1/servers/everything/tools
```

`gen-secrets.sh` sets `secrets/` to `0700` (and refuses a symlink). The key files inside are
`0644` because the sidecar (uid 1000) and mcpproxy (root) both read them through the bind
mount; the directory is what keeps other host users out.

The demo server is `@modelcontextprotocol/server-everything`, so the example needs no
credentials. To wrap a different server, change `build.args.SERVER_PKG` in `docker-compose.yml`
(it overrides the default in `sidecar/Dockerfile`) and the `--stdio=` command beside it, add one sidecar service
per server, and add a matching entry to `mcpproxy/mcp_config.json`.

The mcpproxy image needs **v0.72.0 or newer**: `${file:}` references and
`MCPPROXY_API_KEY_FILE` are not in earlier releases. The example pins v0.73.0 by digest.

## What each setting is for

| Setting | Why |
|---|---|
| `--stateful` | supergateway's default is stateless: every request gets a fresh child process, so a server that keeps per-session state loses it between calls. See [the stateless pitfall](#the-stateless-pitfall). |
| `--apiKeyFile` | The sidecar refuses requests without its key, so another container on `back` cannot use it. |
| `--healthEndpoint /healthz --healthCheck server` and a Compose `healthcheck` | `/healthz` really starts the wrapped server (initialize plus ping) and answers without the API key. With `depends_on: condition: service_healthy`, mcpproxy starts only once the sidecar works, instead of logging `connection refused` and retrying. |
| `--sessionTimeout=120000` | Reaps a session mcpproxy abandoned after 2 minutes instead of the 30-minute default. See [limits](#limits). |
| `stop_grace_period: 60s` | mcpproxy's graceful-shutdown bound is 60 s; Compose's default of 10 s would cut it short. See [Shutdown Behavior](/operations/shutdown-behavior). |
| `./mcpproxy:/config:ro` | A directory, not a single file: mcpproxy saves its config by `rename(2)`, which fails on a single-file bind mount. Read-only keeps the declarative file as the source of truth, so mcpproxy does not rewrite your copy. |
| `"require_mcp_auth": true` | mcpproxy has to be on `back` to reach the sidecars, so a sidecar can reach `http://mcpproxy:8080/mcp`. Without MCP authentication a compromised sidecar could use the proxy. With it, the sidecar gets `401`. |
| `"quarantined": false`, `"trust_mode": "manual"` | The sidecar is an operator-authored entry, so it starts approved; `manual` still holds any later tool change for review. Remove `quarantined` to review the server first. |
| Pinned images (by digest) | A digest cannot be moved under you. Bump the tag and digest together. |

## Which servers go in a sidecar

Keep the proxy's own stdio spawning for servers you trust and that need the host or
the proxy's environment. Put a server in a sidecar when you do not fully trust the package,
or when it needs something the proxy container should not have (a browser, a cloud CLI,
different credentials). Pass a sidecar only the secrets that server needs, through its own
`environment` or file secrets, never through the proxy's.

## The stateless pitfall

supergateway 4.2.0 runs stateless unless told otherwise. In the test behind this example
a counter server called three times in one MCP session returned `count=1, count=1,
count=1` and no `Mcp-Session-Id`; with `--stateful` it returned `1, 2, 3`, one child per
session. Playwright keeps its open page between calls only with `--stateful`. Any server
with state needs the flag, and the example sets it on every sidecar.

## Limits

- **One upstream session per server for all clients.** mcpproxy keeps a single session to
  each upstream, so `--stateful` is per proxy, not per MCP client: a second client sees
  the first client's browser page.
- **A restart loses state.** Each mcpproxy start opens a new session and so a new child
  process. If a sidecar restarts, mcpproxy re-initializes on the `404` by itself, but state
  such as a Playwright page is gone.
- **Abandoned sessions linger up to `--sessionTimeout`.** mcp-go's `DELETE` that closes a
  session does not carry the configured headers, so a key-protected sidecar refuses it and
  the session's child process stays until the timeout. mcpproxy's own ping keeps a live
  session alive, which is why a short timeout is safe.
- **Not every server was tried.** The layout was tested with the demo server, Terraform,
  Grafana, Playwright and chrome-devtools MCP servers. Browser sidecars are large (about
  1 GB of image and 350-500 MB of memory with Chromium running). Servers that need
  network access, such as most API servers, need the sidecar on a non-internal network
  too, which gives up the no-egress guarantee for that sidecar; put it on its own network,
  not `back`.
- **supergateway is one wrapper.** Any stdio-to-HTTP wrapper that supports sessions and an
  API key fits the same layout; supergateway is what has been tested.
- **The example is not TLS.** It publishes on `127.0.0.1`. To expose it, put a reverse
  proxy in front and see [Reverse Proxy Deployment](/operations/reverse-proxy).

## Checks the CI job runs

`examples/compose-stdio-sidecars/test.sh` builds the sidecar, starts the stack, starts the stack
under its own Compose project name (`mcpproxy-sidecars-test`, port 18081), so the
deployment's data volume is never touched and only the test project's containers, network
and volume are removed at the end. Only the sidecar has a Compose health check, so
`up --wait` returns once the sidecar is healthy and the proxy container is running (the
image has no shell or HTTP client for a probe, and `mcpproxy status` exits 0 when the core
is down); the script then polls the proxy until it lists the tools. It checks that the proxy lists `everything:echo` with the API
key; that `/mcp` without a credential and the sidecar without its key both return `401`;
that the sidecar cannot reach `1.1.1.1` or `example.com`; that `/data` does not exist in
the sidecar; and that no container mounts the Docker socket.

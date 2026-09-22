---
id: reverse-proxy
title: Reverse Proxy Deployment
sidebar_label: Reverse Proxy
description: 'Run MCPProxy behind nginx or Caddy — fix "403 Forbidden: invalid Host header" with trusted_hosts, honour X-Forwarded-* only from trusted_proxies, and secure the exposed endpoint.'
keywords: [reverse proxy, nginx, caddy, trusted_hosts, trusted_proxies, X-Forwarded-For, X-Forwarded-Proto, invalid Host header, DNS rebinding, 403 Forbidden, Host header]
---

# Reverse Proxy Deployment

MCPProxy listens on a loopback address by default (`127.0.0.1:8080`) and is designed
to run locally. You can still put it behind a reverse proxy (nginx, Caddy, Traefik,
CloudPanel) to add HTTPS, a public hostname, or shared access — but three things need
attention: **Host-header validation**, **forwarded headers** (`trusted_proxies`) and
**authentication**.

## The `403 Forbidden: invalid Host header` error

When MCPProxy listens on a loopback address, it rejects any request whose `Host`
header is **not itself a loopback address**, returning:

```
403 Forbidden: invalid Host header
```

This is **DNS-rebinding protection**. It stops a malicious website from rebinding its
own domain to `127.0.0.1` and driving a victim's browser into the local MCP server.
The side effect: a reverse proxy that forwards the real public domain in the `Host`
header (e.g. `mcp.example.com`) is rejected the same way.

The fix is to add your public domain(s) to the `trusted_hosts` allowlist.

## `trusted_hosts` configuration

```json
{
  "listen": "127.0.0.1:8080",
  "trusted_hosts": ["mcp.example.com"]
}
```

| Behaviour | Detail |
|-----------|--------|
| Matching | Hostnames, compared **case-insensitively** |
| Without a port | `"mcp.example.com"` matches that host on **any** port |
| With a port | `"mcp.example.com:8443"` requires an **exact** port match |
| Subdomain wildcard | A leading dot — `".example.com"` — matches `example.com` **and every subdomain** (`mcp.example.com`, `a.b.example.com`), same convention as Django's `ALLOWED_HOSTS` and Vite's `server.allowedHosts` |
| Disable entirely | The single entry `"*"` turns Host **and Origin** validation off. **Not recommended:** it re-opens DNS rebinding — any website the local user visits can then drive requests into the proxy |
| Loopback | `localhost`, `127.0.0.1`, `[::1]` are **always** accepted — no need to list them |
| Non-loopback listeners | If `listen` is already a non-loopback address, Host validation never runs |
| Unix socket | Socket/named-pipe connections are never subject to Host validation |
| Default | Empty (`[]`) = full DNS-rebinding protection |

- **Environment override:** `MCPPROXY_TRUSTED_HOSTS` (comma-separated), e.g.
  `MCPPROXY_TRUSTED_HOSTS="mcp.example.com,mcp.internal:8443"`.
- **Hot-reloadable:** editing the config file applies without a restart.

`trusted_hosts` decides only which `Host` values a **loopback** listener accepts. It
never runs on a non-loopback listener (the Docker image's `0.0.0.0:8080`), and it says
nothing about whether the proxy believes `X-Forwarded-*` — that is `trusted_proxies`,
below. In particular it is **not** the control that secures the Server-edition SSO door.

### Origin validation (browser requests)

Per the MCP specification's security best practices, MCPProxy also validates the
`Origin` header on loopback listeners: when a request **carries** an `Origin` and its
host is neither loopback nor in `trusted_hosts`, it is rejected with
`403 Forbidden: invalid Origin header`. Requests **without** an `Origin` header —
every non-browser MCP client, CLI tool, and server-side integration — are unaffected.

Note that reverse proxies forward a browser's `Origin` header as-is. A browser
client served **from your public domain** works as soon as that domain is in
`trusted_hosts` (its `Origin` matches the same entry as its `Host`). A browser
frontend hosted on a **different** origin must have its own host added to
`trusted_hosts` too, or its requests are rejected.

## `trusted_proxies` — forwarded headers {#trusted_proxies-forwarded-headers}

A reverse proxy rewrites the connection MCPProxy sees: the peer address becomes the
proxy's, the scheme becomes plain `http`, and the real client, scheme and host arrive
only as `X-Forwarded-For`, `X-Real-IP`, `X-Forwarded-Proto` and `X-Forwarded-Host`.
Anyone who can reach the listener directly can send those headers too, so by default
MCPProxy **believes none of them**. `trusted_proxies` lists the peers it does believe:

```json
{
  "listen": "127.0.0.1:8080",
  "trusted_hosts": ["mcp.example.com"],
  "trusted_proxies": ["127.0.0.1", "10.42.0.0/16"]
}
```

| Behaviour | Detail |
|-----------|--------|
| Entries | CIDRs (`10.42.0.0/16`, `fd00::/8`) or single IP addresses (`127.0.0.1`, `::1`). Hostnames are not accepted |
| Default | Empty (`[]`) — **trust nobody**: every forwarded header is ignored and the direct `RemoteAddr` and listener scheme are used |
| Trusted peer | When `RemoteAddr` is inside the list, `X-Forwarded-Proto` (`http`/`https`) and `X-Forwarded-Host` are honoured, and the client IP is the **right-most `X-Forwarded-For` hop that is not itself a trusted proxy** (then `X-Real-IP`) |
| Untrusted peer | Headers ignored, no error. A direct client cannot spoof its IP, the request scheme or the host |
| What it feeds | The session client IP (and the per-request client IP tagged for attribution), the `Secure` decision of the Server-edition session cookie, the OAuth callback scheme/host when `server_edition.public_url` is unset, and the Swagger UI base URL |
| What it never feeds | Local/remote or administrator classification — no forwarded header can promote a caller |
| Validation | `trusted_proxies[0] "nginx" is not a valid CIDR or IP address` — the same message at boot, on `PATCH /api/v1/config` and on `/config/apply` |
| Environment override | `MCPPROXY_TRUSTED_PROXIES="127.0.0.1,10.42.0.0/16"` |
| Hot reload | Live — every reader evaluates the current list per request, no restart |

List the proxy's **source** address as MCPProxy sees it: `127.0.0.1` for nginx or Caddy on
the same host, the ingress pod or node range in Kubernetes, the Docker bridge network for a
proxy container in front of `mcpproxy-server`. Do not list `0.0.0.0/0` — that re-opens the
spoof.

nginx sends the standard headers with:

```nginx
proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
proxy_set_header X-Forwarded-Proto $scheme;
proxy_set_header X-Forwarded-Host $host;
```

Caddy's `reverse_proxy` sets `X-Forwarded-For` and `X-Forwarded-Proto` by default.

**Server edition (SSO):** behind a TLS-terminating ingress set **both**
`server_edition.public_url` (the exact origin registered at the IdP — the callback URL and
the `Secure` cookie then no longer depend on any header) and `trusted_proxies` (the ingress
range, so the session IP and the attributed client IP are the real client). See
[Server Edition](/configuration/config-file#server-edition) for the container topology.

## nginx

With `trusted_hosts` configured, a standard nginx block works without rewriting the
`Host` header:

```nginx
server {
    listen 443 ssl;
    server_name mcp.example.com;

    location / {
        proxy_pass http://127.0.0.1:8080;
        proxy_set_header Host $host;

        # Honoured only if 127.0.0.1 is in trusted_proxies (see above).
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_set_header X-Forwarded-Host $host;

        # MCPProxy streams responses (SSE at /events, streamable HTTP at /mcp).
        # Disable proxy buffering so events are delivered as they are produced.
        proxy_buffering off;
        proxy_read_timeout 3600s;
    }
}
```

## Caddy

Caddy forwards the request `Host` by default, so with `mcp.example.com` in
`trusted_hosts` no extra header handling is needed. Disable response buffering so
streaming works:

```caddy
mcp.example.com {
    reverse_proxy 127.0.0.1:8080 {
        flush_interval -1
    }
}
```

`flush_interval -1` disables Caddy's response buffering (the equivalent of nginx's
`proxy_buffering off`), which streaming endpoints require.

## Authentication through the proxy

Exposing MCPProxy beyond localhost changes the threat model. Two endpoint families
authenticate differently:

- **REST API (`/api/v1/...`)** — an API key is **always** required. Pass it as the
  `X-API-Key` header (recommended) or the `?apikey=` query parameter. If you don't
  set one, the key is auto-generated on first start, printed once to the terminal
  and written to your config file; it is never written to the log files, so read
  `api_key` from `~/.mcpproxy/mcp_config.json` to recover it.
- **MCP endpoint (`/mcp`)** — **unauthenticated by default** for client
  compatibility. When you expose MCPProxy through a reverse proxy, enable
  `require_mcp_auth` so `/mcp` also rejects unauthenticated requests:

  ```json
  {
    "listen": "127.0.0.1:8080",
    "trusted_hosts": ["mcp.example.com"],
    "require_mcp_auth": true
  }
  ```

  MCP clients then authenticate with the same API key (`X-API-Key` header).

  In the **Server edition** `require_mcp_auth` is forced to `true` whenever
  `server_edition.enabled` is `true`; an explicit `false` is overridden with one boot
  notice and a `mcpproxy doctor` finding. A browser session cookie or user JWT is never
  an MCP credential — `/mcp` accepts agent tokens, the API key and the socket only.

Prefer the `X-API-Key` header over `?apikey=` where the client supports it — query
strings are more likely to be logged by intermediate proxies.

## Related

- [Configuration File](/configuration/config-file) — full option reference
- [Environment Variables](/configuration/environment-variables) — `MCPPROXY_TRUSTED_HOSTS`, `MCPPROXY_TRUSTED_PROXIES`
- [REST API](/api/rest-api) — authentication details

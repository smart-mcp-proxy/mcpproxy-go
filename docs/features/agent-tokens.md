---
id: agent-tokens
title: Agent Tokens
sidebar_label: Agent Tokens
sidebar_position: 10
description: Scoped API credentials for AI agents with server and permission restrictions
keywords: [agent, tokens, authentication, security, scoping, permissions, mcp]
---

# Agent Tokens

Agent tokens provide **scoped, revocable credentials** for AI agents connecting to MCPProxy. Instead of sharing the admin API key with every agent, each agent gets its own token with restricted access to specific servers and permission tiers.

## Why Agent Tokens?

MCPProxy sits between AI agents and upstream MCP servers. Without agent tokens, every connection gets full admin access — any agent can call any tool on any server with no restrictions.

This creates real problems:

- **A CI/CD bot** that only needs to read GitHub issues can also delete repositories
- **A monitoring agent** that checks server status can also modify configurations
- **A compromised agent** has unlimited access to all upstream servers
- **No audit trail** — you can't tell which agent performed which action

Agent tokens solve this with **defense-in-depth scoping**:

```
┌─────────────────────────────────────────┐
│  AI Agent (e.g., deploy-bot)            │
│  Token: mcp_agt_a1b2c3...              │
│  Servers: github, gitlab                │
│  Permissions: read, write               │
└──────────────┬──────────────────────────┘
               │
               ▼
┌─────────────────────────────────────────┐
│  MCPProxy                               │
│                                         │
│  1. retrieve_tools → filters results    │
│     to github + gitlab only             │
│                                         │
│  2. call_tool_write → allowed           │
│  3. call_tool_destructive → BLOCKED     │
│  4. call_tool_read(slack:...) → BLOCKED │
└─────────────────────────────────────────┘
```

## Token Format

Agent tokens use the `mcp_agt_` prefix followed by 64 hex characters:

```
mcp_agt_a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2
```

Tokens are hashed with HMAC-SHA256 before storage — the raw token is shown once at creation and cannot be retrieved again.

## Quick Start

### Create a Token

```bash
mcpproxy token create \
  --name deploy-bot \
  --servers github,gitlab \
  --permissions read,write \
  --expires 30d
```

Output:
```
Agent token created successfully.

  Token: mcp_agt_a1b2c3d4...

  IMPORTANT: Save this token now. It cannot be retrieved again.

  Name:        deploy-bot
  Servers:     github, gitlab
  Permissions: read, write
  Expires:     2026-04-05 14:30
```

### Use the Token

Agents authenticate by passing the token via any standard method:

```bash
# X-API-Key header
curl -H "X-API-Key: mcp_agt_a1b2c3d4..." http://localhost:8080/mcp

# Authorization: Bearer header
curl -H "Authorization: Bearer mcp_agt_a1b2c3d4..." http://localhost:8080/mcp

# Query parameter
curl "http://localhost:8080/mcp?apikey=mcp_agt_a1b2c3d4..."
```

In MCP client configurations:
```json
{
  "mcpServers": {
    "mcpproxy": {
      "url": "http://localhost:8080/mcp",
      "headers": {
        "X-API-Key": "mcp_agt_a1b2c3d4..."
      }
    }
  }
}
```

## Enforcing Authentication on /mcp

By default, the `/mcp` endpoint allows unauthenticated access for backward compatibility with existing MCP clients. This means agent tokens are **optional** — agents that don't provide a token get full admin access.

To make agent tokens **mandatory**, enable `require_mcp_auth`:

```json
{
  "require_mcp_auth": true
}
```

Or via CLI flag:

```bash
mcpproxy serve --require-mcp-auth
```

With this enabled:
- Requests without a token → **401 Unauthorized**
- Requests with an invalid token → **401 Unauthorized**
- Requests with a valid agent token → scoped access
- Requests with the admin API key → full admin access
- Tray/socket connections → always trusted (OS-level auth)

**Recommended setup:** Enable `require_mcp_auth` when deploying MCPProxy in environments where multiple agents connect, or when you want to enforce least-privilege access.

## Permission Tiers

Each token lists the permission tiers the agent holds. A tier unlocks the matching `call_tool_*` variant:

| Permission | Tool Variant Unlocked | Use Case |
|------------|----------------------|----------|
| `read` | `call_tool_read` | Monitoring, querying, status checks |
| `write` | `call_tool_write` | Creating issues, updating records |
| `destructive` | `call_tool_destructive` | Deleting resources, admin operations |

Permissions are **exact-match, not cumulative**: the token holds exactly the tiers listed, so `destructive` does not imply `write`. `mcpproxy token create` stores the list verbatim; the only rule is that the list must include `read`. A token minted as `read,destructive` can use `call_tool_read` and `call_tool_destructive` but is refused on `call_tool_write` — list every tier the agent needs.

### Target tool tier

Holding a tier for a *variant* is only half the check. Every dispatch path — the `call_tool_*` variants, direct-name dispatch on `/mcp/all` (see [Routing Modes](https://docs.mcpproxy.app/features/routing-modes)) and `call_tool()` inside [code execution](https://docs.mcpproxy.app/features/code-execution) — also authorizes the token against the tier of the **target tool**, derived from the tool's own MCP annotations (`readOnlyHint` / `destructiveHint`) as reported at discovery. The two checks are independent and both are exact-match: `call_tool_read` on a write-tier tool needs `write`, and a `read,destructive` token is refused on a write-tier tool on every path.

- **Annotation-less tools default to `read`.** A discovered tool that publishes no annotations derives to the read tier, so a read-only token can call it. Operators who want stricter handling of unannotated tools use the [intent declaration](https://docs.mcpproxy.app/features/intent-declaration) validation rules.
- **Unresolved identity on a known server is refused for every caller.** The tier comes from the tool's registration identity in the live discovery snapshot — the exact `server:tool` pair that will be dispatched. If the server is known and connected but its discovery has not completed yet, or its completed discovery result does not list the tool (undiscovered name, stale name after a server redeployed its tool set, a server that lists no tools at all), no tier can be established: the call is refused with the insufficient-permission body and never reaches the upstream, for agent tokens *and* for administrators — including stdio and in-process callers inside code execution. The refusal names the reason: while discovery has not completed for the server, retry shortly (there is no list to refresh from yet); once it has and the name is absent, refresh with `retrieve_tools` and retry with a listed name. A server MCPProxy does not know at all keeps its ordinary "server not found" answer, and a server whose snapshot is not authoritative because of its own state — quarantined, disabled, disconnected or still connecting — keeps its server-level answer (the quarantine analysis, the disabled block, the not-connected message and `reconnect_on_use`) exactly as before, for every name on that server alike. Those server-level verdicts and the identity check read the same source: quarantined and disabled come from the persisted server configuration (not from the cached status view, which catches up with an operator's write a moment later), and connected comes from the live upstream client. So an operator who quarantines or disables a server right after its tools were discovered gets the quarantine analysis or the disabled block for an unlisted name and a listed name alike — never an "undiscovered or stale name" refusal whose `retrieve_tools` remediation cannot heal a quarantined server — and the identity refusal fires only when the same read cannot answer quarantined or disabled.
- **No approval record under an active quarantine gate is pending.** While tool-level quarantine applies to a server (`quarantine_enabled` on and the server not opted out via `trust_mode: auto` / `auto_approve_tool_changes`), a tool the snapshot contains that has no [approval record](https://docs.mcpproxy.app/features/security-quarantine) of its own is treated as pending approval — never as implicitly approved — at every gate: dispatch, preflight and `describe_tool`. A name on a server whose snapshot is empty because of its own state (quarantined, disabled, disconnected, connecting) is not held pending — the server-level answer owns it, as above — and once the server reconnects, a name its fresh discovery result does not list is refused as unresolved before any upstream call. The refusal says `no_approval_record`: nothing is listed for review yet, and the record is filed on the server's next discovery pass (`upstream_servers` operation `refresh`, or `mcpproxy upstream restart <server>`). Approval records are keyed by the exact upstream tool name, so a namespaced tool such as `ns:erase` is approved only by its own name and never inherits the approval of a sibling `erase` — see [Namespaced tool names](https://docs.mcpproxy.app/features/security-quarantine#namespaced-tool-names).

```bash
# Read-only monitoring agent
mcpproxy token create --name monitor --servers "*" --permissions read

# CI/CD agent that creates and updates
mcpproxy token create --name ci-agent --servers github --permissions read,write

# Full-access admin agent
mcpproxy token create --name admin-bot --servers "*" --permissions read,write,destructive
```

## Server Scoping

Tokens restrict which upstream servers an agent can access:

```bash
# Only GitHub and GitLab
mcpproxy token create --name deploy-bot --servers github,gitlab --permissions read,write

# All servers (wildcard)
mcpproxy token create --name all-access --servers "*" --permissions read
```

Server scoping is enforced at three levels:
1. **Tool discovery** (`retrieve_tools`) — only returns tools from allowed servers
2. **Tool execution** (`call_tool_*`) — blocks calls to out-of-scope servers
3. **Enumeration** — since issue #1166, `allowed_servers` also scopes what the
   REST surface will *list*, not only what the token may call. A scoped token
   sees only its own servers on `GET /api/v1/servers` (array **and** the
   `stats` counters), `GET /api/v1/status` (`upstream_stats`), the `/events`
   SSE stream, `GET /api/v1/tools`, `GET /api/v1/index/search`,
   `GET /api/v1/diagnostics` / `doctor`, `GET /api/v1/profiles`,
   `GET /api/v1/annotations/coverage` and `GET /api/v1/security/scans`.

   **The whole `/api/v1/servers/{id}` subtree** answers `404 Server not found`
   for a server outside the scope — `tools`, `logs`, `tool-calls`,
   `diagnostics`, `scan/status`, `scan/report`, `scan/files`, `integrity`,
   `tools/export`, `tools/{tool}/diff` and every sub-resource added later, since
   the gate is a middleware on the subtree. It is the *same* `404` a server that
   does not exist returns — byte for byte, once the echoed name is normalised —
   so the response cannot be used to probe for hidden servers. `logs` matters
   most: upstream stderr routinely echoes the argv and env the server process
   was launched with.

   **The activity, tool-call and usage doors** are scoped to records
   attributable to an allowed server: `GET /api/v1/activity`,
   `/activity/summary`, `/activity/usage`, `/activity/export`, `/activity/{id}`,
   `GET /api/v1/tool-calls` and `/tool-calls/{id}` (plus its `/replay`). The
   entitlement is applied inside the query, so `total` and the page always
   describe the same record set, and a `?server=` filter narrows *within* the
   scope rather than escaping it. Records with no server attribution
   (`system_start`, `config_change`, …) are operator-plane events and are not
   shown. On `/activity/usage`, aggregates that cannot be re-derived per server
   — the tokens-saved headline and the global timeline — are omitted rather than
   reported fleet-wide.

   **Denied outright (`403`)** to agent tokens, because there is nothing
   per-server to project:
   - `GET /api/v1/config` — an admin document, and it carries the admin API key.
   - `GET /api/v1/stats/tokens` — `per_server_tool_list_sizes` is keyed by every
     configured server, and the scalars beside it are fleet-wide.
   - `GET /api/v1/sessions`, `GET /api/v1/sessions/{id}` — an MCP session
     describes a *client* and the user's workspace, with no server attribution.
   - `GET /api/v1/security/overview`, `GET /api/v1/security/queue` — fleet-wide
     scan and finding counts, and a queue that names every server waiting to be
     scanned. A scoped caller reads its own server's verdict from
     `GET /api/v1/servers/{id}/scan/status`, which the subtree gate scopes.
   - `GET /api/v1/telemetry/payload` — the heartbeat carries `server_count`,
     `connected_server_count`, `tool_count` and `server_docker_isolated_count`:
     precisely the count oracle removed from `/status`.
   - `GET /api/v1/onboarding/state`, `POST /api/v1/onboarding/mark` (which
     echoes the same document) — `configured_server_count` is an inventory size
     and `connected_client_ids` is the operator's MCP-client inventory.
   - `GET /api/v1/secrets/refs`, `GET /api/v1/secrets/config` — values are
     masked, so this is a credential *inventory* rather than a disclosure, but
     it names the secrets of servers the caller may not enumerate. A strictly
     narrower view of the document `GET /api/v1/config` already denies.

   **Withheld rather than denied.** `GET /api/v1/status` stays open — agents
   legitimately poll it for liveness — but its `activation` block is omitted for
   a scoped caller. `mcp_clients_seen_ever` is the operator's MCP-client
   inventory and `retrieve_tools_calls_24h` is an exact deployment-wide counter,
   neither of which has a per-server part to project. The key is already absent
   when telemetry is unwired, so clients tolerate its absence.

   `PUT /api/v1/profiles/active` answers `403`: the active profile is
   server-level shared state that decides what the Web UI and tray render, so a
   read-scoped credential must not be able to change it. It is gated by the same
   `config_write` policy as the other config-level writes.

   On the `/events` stream, scoping applies **per event**, not only to the
   `servers.changed` server list:

   - An event that names a server the token may not enumerate — through
     `server_name`, `server`, `target_server` or `affected_entity`, which is
     every activity, OAuth and security event — is **not delivered** to that
     subscriber at all. It is dropped rather than blanked, because a frame with
     the name removed still discloses the mutation, its timing and the number
     of servers being hidden.
   - `servers.changed` is the exception and is always delivered, because it is
     coalesced last-write-wins and carries the state a client renders. Its
     server list is narrowed, its `stats` recomputed, and any coalescer extra
     that names an out-of-scope server (`"server": "beta"`) is removed.
   - `config.reloaded`, `config.saved` and `secrets.changed` announce mutations
     of the admin config document and are dropped, matching the `403` on
     `GET /api/v1/config`.

   Admin subscribers — the API key, the Web UI, the tray over the unix socket —
   receive every event unchanged; the stream is rendered per connection.
4. **Cached responses** (`read_cache`) — a truncated response is parked behind
   a cache key, and the key is a hash, not a credential. Every entry is stamped
   with the authorization snapshot that authorized producing it (server scope,
   permission tier, profile pin, effective profile, caller kind), captured when
   the call was authorized — a profile narrowed while the call was in flight
   does not re-stamp the response. `read_cache` — on every MCP surface and on
   the REST direct call path (`POST /api/v1/tools/call`) — refuses, on every
   page, any request whose current authorization is neither equal to nor a
   superset of that snapshot, so a narrower token sharing the same MCP session
   cannot page a broader token's response. Superset is ordered by **caller kind
   first**: an administrator may read any entry regardless of its own profile
   binding; an agent token never reads an administrator's entry; between agent
   entries the allowed-server set, permission set and effective profile scope
   must each contain the entry's. Profile scope is compared as a server set, so
   deleting or narrowing a profile after the entry was produced revokes cached
   access as well (a stale pin resolves to a deny-all scope and reads nothing).
   An unauthenticated `/mcp` caller ranks below an authenticated admin: it
   cannot page an entry an API-key admin produced.

   A page that `read_cache` itself has to truncate again is stamped with its
   *parent's* snapshot, never the redeemer's, so provenance is monotone down
   the chain. For a scoped caller every refusal — an entry it may not read, an
   expired entry, an internal entry, a key that never existed — answers with
   the same `cache key not found` body, status and timing (a refusal commits
   the same stats write a miss does), so a key cannot be probed for
   existence. A refusal also never decodes the entry's payload: the gate
   reads a small header stored in front of each record, so a multi-megabyte
   entry is refused as quickly as a one-line one. An expired entry is refused
   like a miss and left for the periodic cleanup sweep to evict, so the
   refusing read writes exactly what a miss writes.

   **Upgrading.** Entries written by any release before this one — including
   the immediately preceding one, which stamped a producer but no schema
   version — are refused for **every** caller, administrators included, and
   are invalidated on the first attempt to read them (a one-time
   `cache key not found` on keys minted before the upgrade; re-run the
   original tool call). The registry and repository-metadata caches mcpproxy
   keeps for itself are stamped internal from this release on: never readable
   through `read_cache`, and kept rather than evicted when refused. Registry
   and repository-metadata entries persisted *before* the upgrade carry no
   stamp, so the first `read_cache` probe of such a key after upgrading
   invalidates it once — the next registry search or repository lookup
   re-fetches and re-stamps it. The no-eviction guarantee applies to entries
   written after the upgrade.

## Administrative Operations Are Admin-Only

Agent tokens can **discover and call** tools (within their scope and permission tier) but can **never administer servers**. Server-mutating operations require the admin API key (or a local tray/socket connection, which is admin by OS-level auth) on **every** surface — the MCP tools and the REST API share one policy (`internal/auth`), so an agent cannot do over HTTP what it is blocked from doing over MCP.

Denied to agent tokens on both surfaces:

- **Lifecycle**: add, remove, update/patch, enable, disable, restart, reconnect, refresh/discover-tools, add-from-registry, login/logout, move-config-value-to-secret
- **Security state**: quarantine, unquarantine, tool approve/block, and the security scanner (scan start/cancel, security approve/reject)
- **Config & registries**: applying/patching configuration (which can add/remove/enable/disable servers) and mutating registry sources — an agent must not bypass the per-server gate by rewriting config or a registry wholesale

On the MCP surface (`upstream_servers`, `quarantine_security`) these return a tool error; on the REST surface (mutating `/api/v1/servers/...`, `/api/v1/config/...`, and `/api/v1/registries/...` routes) they return **`403 Forbidden`** (`operation requires admin access`). Read-only operations stay available to scoped tokens: `upstream_servers` `list`/`tail_log`, `GET /api/v1/servers`, per-server diagnostics, registry reads, and `GET /api/v1/index/search` (which honors quarantine — a quarantined server's tools are withheld from search on every surface). Those reads are **scope-filtered** as described above. `GET /api/v1/config` is the exception: it is an admin document (it carries the global `api_key`, every server's credentials, and a second enumeration of server names under `profiles[].servers`), so it returns `403` for an agent token rather than a filtered view.

## Profile Pinning

A [profile](./profiles.md) scopes tool discovery and calls to a named subset of upstream servers. With `--profile-pin`, you can **bind a token to a single profile** so it can never operate outside it — regardless of the URL it connects to or any `set_profile` call it makes.

```bash
# This token can ONLY ever see/use the "research" profile
mcpproxy token create \
  --name research-agent \
  --servers "*" \
  --permissions read \
  --profile-pin research
```

Server-side enforcement (no client cooperation required):

- **`set_profile("other")` is rejected** — a pinned token cannot switch its session to a different profile (switching to its own pinned profile, or clearing, is allowed).
- **`/mcp/p/<other>` returns `403`** — connecting to any profile URL other than the pinned one is forbidden; the pinned profile's own URL works.
- **The pin is the highest-precedence resolver source**, above an explicit `/mcp/p/<slug>` URL scope and above a session `set_profile` selection.
- **Every dispatch surface resolves it** — `retrieve_tools`, `describe_tool`, `call_tool_*`, the `code_execution` sandbox, direct-routing mode (`server__tool`) and [preflight](./tools-preflight.md) all bound themselves by the pin, so no routing mode is a way around it.

Resolution precedence (highest wins):

```
1. agent-token profile_pin   (server-enforced; this section)
2. /mcp/p/<slug> URL scope    (per-request override)
3. set_profile session state  (base /mcp endpoint default for the session)
4. none                        (no profile filtering — all allowed servers)
```

**Validation & config changes**: the pinned slug must name a configured profile at creation time (creation is rejected otherwise). If the profile is **later removed** from the configuration, the pin resolves to a **deny-all scope**: the token sees no upstream servers and no tools, on the MCP session path and in [preflight](./tools-preflight.md#disclosure-tiers) alike. The request is logged with a warning naming the removed profile, not hard-failed at the transport. The pin is a restriction the operator applied, so losing the profile it names must never hand the token a wider view than it had the day before — re-create the profile, or re-mint the token against a live one, to restore it. Pinning composes with server scoping and permission tiers: a request must satisfy **all** of them.

The pin is shown by `token list` (PROFILE PIN column) and `token show` (Profile Pin field), and is preserved across `token regenerate`.

## Managing Tokens

### List All Tokens

```bash
mcpproxy token list
```

```
NAME                 PREFIX         SERVERS                   PERMISSIONS          REVOKED  EXPIRES
deploy-bot           mcp_agt_a1b2   github,gitlab             read,write           no       2026-04-05 14:30
monitor              mcp_agt_c3d4   *                         read                 no       2026-04-05 14:30
old-bot              mcp_agt_e5f6   github                    read                 yes      2026-03-01 10:00
```

### Show Token Details

```bash
mcpproxy token show deploy-bot
```

### Revoke a Token

Immediately invalidates the token. Revoke is a **soft delete**: the record is kept
(so the token name stays reserved) and any further use is rejected:

```bash
mcpproxy token revoke deploy-bot
```

### Delete a Token

Permanently removes the token, freeing its name for reuse. Unlike revoke, delete
removes the record entirely — after deleting, you can create a new token with the
same name:

```bash
mcpproxy token delete deploy-bot   # aliases: rm, remove
```

### Regenerate a Token

Invalidates the old secret and generates a new one, keeping the same name and settings:

```bash
mcpproxy token regenerate deploy-bot
```

The new token is displayed once — save it immediately.

### JSON Output

All commands support JSON output for scripting:

```bash
mcpproxy token list -o json
mcpproxy token create --name bot --servers github --permissions read -o json
```

## Activity Logging

Agent token usage is tracked in the activity log. Each tool call records the agent identity:

```bash
# Filter activity by agent
mcpproxy activity list --agent deploy-bot

# Filter by auth type
mcpproxy activity list --auth-type agent
mcpproxy activity list --auth-type admin
```

Activity records include `_auth_type`, `_auth_agent`, and `_auth_token_prefix` metadata fields for audit trails.

## REST API

Agent tokens can also be managed via the REST API (requires admin API key):

| Method | Endpoint | Description |
|--------|----------|-------------|
| `POST` | `/api/v1/tokens` | Create a new agent token |
| `GET` | `/api/v1/tokens` | List all tokens |
| `GET` | `/api/v1/tokens/{name}` | Get token details |
| `DELETE` | `/api/v1/tokens/{name}` | Revoke a token (soft delete; name stays reserved) |
| `DELETE` | `/api/v1/tokens/{name}/permanent` | Permanently delete a token (frees the name for reuse) |
| `POST` | `/api/v1/tokens/{name}/regenerate` | Regenerate token secret |

### Create Token via API

```bash
curl -X POST http://localhost:8080/api/v1/tokens \
  -H "X-API-Key: your-admin-key" \
  -H "Content-Type: application/json" \
  -d '{
    "name": "deploy-bot",
    "allowed_servers": ["github", "gitlab"],
    "permissions": ["read", "write"],
    "expires_in": "30d"
  }'
```

## Security Model

- **HMAC-SHA256 hashing** — raw tokens are never stored; only HMAC hashes are persisted
- **Constant-time comparison** — prevents timing attacks during token validation
- **Automatic expiry** — tokens expire after a configurable duration (default: 30 days)
- **Revocation** — tokens can be immediately invalidated
- **Prefix identification** — the `mcp_agt_` prefix distinguishes agent tokens from admin API keys without database lookups
- **Tray bypass** — local tray/socket connections always get admin access (authenticated by OS-level socket permissions)

## Configuration Reference

### Config File

```json
{
  "require_mcp_auth": false,
  "api_key": "your-admin-key"
}
```

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `require_mcp_auth` | bool | `false` | Require authentication on `/mcp` endpoint |
| `api_key` | string | auto-generated | Admin API key for full access |

### CLI Flags

```bash
mcpproxy serve --require-mcp-auth    # Enforce /mcp authentication
```

### Token Create Flags

| Flag | Required | Default | Description |
|------|----------|---------|-------------|
| `--name` | Yes | — | Unique token name |
| `--servers` | Yes | — | Comma-separated server names or `"*"` |
| `--permissions` | Yes | — | Comma-separated: `read`, `write`, `destructive` |
| `--expires` | No | `30d` | Expiry duration (e.g., `7d`, `90d`, `365d`) |
| `--profile-pin` | No | — | Pin the token to a single profile (see [Profile Pinning](#profile-pinning)) |

### Server-edition incident response

Administrators authenticated through a server-edition session or bearer JWT can list safe metadata for all owners with `GET /api/v1/admin/tokens`. Each entry includes `user_id`, `name`, scope, permissions, timestamps, prefix, profile pin, and revocation state. Raw credentials and token hashes are never listed.

Revoke one tenant credential with `POST /api/v1/admin/users/{user_id}/tokens/revoke` and JSON body `{"name":"exact stored name"}`. The name is body data so names containing slashes, percent signs, spaces, or Unicode remain addressable through routers and reverse proxies. The older `POST /api/v1/admin/users/{user_id}/tokens/{name}/revoke` form remains available for URL-safe names. Owner and name identify the credential together; another user's same-named token is unaffected. Revocation is durable and takes effect on the next authenticated request, including requests using an existing MCP session.

Owned tokens are checked against the owner's current server entitlement on every authentication. Unsharing an administrator-configured server removes it from a tenant token's effective scope without rotation or restart. Explicit scopes never gain additional servers; historical wildcard grants are bounded by current entitlement. Current administrator owners may still access administrator-configured servers. Missing owners, disabled accounts, and entitlement lookup errors fail closed. Ownerless operator tokens retain their existing behavior.

Calls already authorized and running are not cancelled. A long-lived SSE `/events` response revalidates its agent token before each status or runtime event: unsharing immediately narrows the next frame, while token revocation closes the stream before another event is delivered.

### Sharing CLI status safely

`mcpproxy status` masks the API key in both its key field and Web UI URL across table, JSON, and YAML output. `--show-key` reveals the key. `--web-url` deliberately prints a usable login URL containing the unmasked key; treat that output as a credential. `--reset-key` also explicitly reveals the newly generated key.

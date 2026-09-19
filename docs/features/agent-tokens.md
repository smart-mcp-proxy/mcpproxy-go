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
   - `GET /api/v1/code/scripts` — the stored-script listing (every name, its
     host path and the scripts directory) is exactly the enumeration the
     missing-script error withholds from a scoped caller, so the door is
     closed on the REST surface too (see
     [What a scoped token cannot learn](#what-a-scoped-token-cannot-learn)).

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
   the REST direct call path (`POST /api/v1/tools/call`) — admits, on every
   page, exactly three kinds of request and refuses every other, so a
   narrower token sharing the same MCP session cannot page a broader token's
   response. Ordered by **caller kind first**: an administrator may read any
   entry regardless of its own profile binding (an unauthenticated `/mcp`
   caller ranks below an authenticated admin and cannot page an entry an
   API-key admin produced); an agent token never reads an administrator's
   entry. Between agent entries a reader is admitted when it presents the
   **same effective authorization** the entry was produced under — the same
   token, server grant, permission tiers, pin and effective profile server
   set (compared as sets, so list order and the profile's name do not
   matter) — or when it is **unrestricted**: a `*` server grant, no pin, no
   effective profile, and every permission tier the entry's producer held. A
   token that is wider than the producer but still bounded (an `{a,b}` grant
   over an `{a}` entry, a session that left the profile it produced under)
   is refused: it re-runs the call under its own credential instead. Profile
   scope is compared as a server set, so deleting or narrowing a profile
   after the entry was produced revokes cached access as well (a stale pin
   resolves to a deny-all scope and reads nothing).

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
   refusing read writes exactly what a miss writes. The header and the record
   behind it are two encodings of the same stamp; an entry on which they
   disagree (a corrupt or hand-edited database) is treated as unreadable —
   refused for every caller, invalidated, never served. The header is
   fixed-size and decides the whole verdict by itself: it carries the caller
   kind, the permission tiers and a digest of the producer's effective
   authorization, and the reader's own digest is compared against it — so a
   refusal never loads the producer's snapshot, a pre-upgrade entry is
   invalidated without being decoded, and a probe costs what a miss costs on
   the first request after a restart as much as on the thousandth, however
   many servers the producer's authorization names. Each distinct snapshot
   is still stored once, under that digest, for administrator diagnostics;
   nothing reads it to decide. The size statistics are reconciled from the
   store by the periodic cleanup sweep, which is why an invalidated
   pre-upgrade entry can leave `total_size_bytes` over-counting for at most
   one sweep interval.

   Server-edition OAuth **users** are bounded by the same dispatch gates as
   agent tokens (server allowlist, permission tier, effective profile), so a
   user's cached entry is stamped with those dimensions as well as the user
   id, and redemption requires the same user *with the same* authorization:
   a grant changed or a profile changed since the entry was produced revokes
   cached access exactly as it does for an agent token.

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

### What a scoped token cannot learn

**Invariant.** No proxy-produced response to an agent-token caller — a tool
result, a refusal, a listing, a count, a suggestion, a notification, a cached
page or a log line — names, counts or otherwise discloses a server, tool,
prompt, profile or stored resource outside the caller's effective scope, and an
out-of-scope resource is refused exactly as a nonexistent one would be.
Administrators (the admin API key, the tray over the local socket, and native
stdio) keep every capability they have today; the exceptions where an
administrator's answer deliberately differs from a token's are named and tested
one by one.

> **Rollout status.** This invariant is being landed surface by surface as the
> agent-scope hardening series (Spec 105) merges; each release's notes list the
> surfaces it closes. The rules on this page that are stated as present-tense
> guarantees — the stored-script rules below, the REST doors listed above and
> the `read_cache` rule — are enforced by the version that documents them. Until
> the series is complete, a listing or suggestion on a surface not yet covered
> can still name an out-of-scope resource; treat that as a known gap, not a
> configuration mistake.

> **Who counts as an administrator.** The admin API key, the tray over the
> local socket, native stdio, an in-process caller — and, under the default
> `require_mcp_auth: false`, an **unauthenticated** `/mcp` client, which the
> proxy has always treated as an administrator for backward compatibility. Only
> an agent token is a scoped caller; if unauthenticated clients must not see
> administrator answers, set
> [`require_mcp_auth: true`](https://docs.mcpproxy.app/configuration/) so every
> `/mcp` request carries a key or a token.

**Covered surfaces.** The invariant holds for agent-token requests on every
HTTP MCP surface — `/mcp`, `/mcp/all`, `/mcp/call`, `/mcp/code`,
`/mcp/p/<slug>` and the trailing-slash alias of each (see
[Routing Modes](https://docs.mcpproxy.app/features/routing-modes/)) — and on
the REST doors listed above. Native stdio is local-administrator-only and is
not a token surface.

**Retained, documented effects.** Some shared resources are fleet-wide by
construction and this invariant does not change them; a hidden server can still
*affect* what an authorized caller experiences, without being *named*:

- **Display-name collision admission on `/mcp/all`** — two servers exposing the
  same display name collide fleet-wide, so a hidden server can withhold an
  authorized entry from the direct listing.
- **Prompt collision rule and the global prompt cap** — evaluated over the whole
  fleet.
- **Fleet-wide `list_changed` notifications** on the fixed surfaces — a hidden
  server's change still emits the (content-free) notification.
- **Shared call limiter** — the proxy-wide concurrency limit is global, so calls
  held on a hidden server can make a call to an authorized server fail with the
  existing "proxy-wide limit saturated" response.
- **Cross-server security-scan admission** — under `trust_mode: scan`, a
  same-name near-identical tool on a hidden server can hold an authorized
  server's newly added tool pending as a shadowing finding, changing that
  tool's discovery and dispatch outcome (see
  [Security Quarantine](https://docs.mcpproxy.app/features/security-quarantine/)).
- **Shared prompt-refresh deadline** — prompts are collected under one
  fleet-wide deadline, so a slow hidden server can exhaust it before an
  authorized server's prompts are collected.
- **Shared log rotation and retention** — attribution filters what a token can
  read back, not what history survives rotation.

**Operator-published content — keep secrets out.** Two kinds of operator-authored
content are published to every caller by design and sit outside the invariant:

1. **Custom initialization `instructions`** (the `instructions` key in the
   [config file](https://docs.mcpproxy.app/configuration/)) are returned
   verbatim to every client that initializes, scoped or not.
2. **Stored code-execution scripts** — any caller allowed to run
   `code_execution` can run a script it knows the name of and receive whatever
   the script returns without an upstream call. What the invariant *does*
   cover: a missing-script error never enumerates the other script names, the
   script count or the scripts directory to an agent-token caller (the refusal
   is identical for an empty and a populated directory, and the directory is
   never read on the caller's behalf: on Linux and the BSDs the scoped
   resolver answers ONLY from an exact-name index of the directory that
   matches its CURRENT state — built when the daemon starts and refreshed by
   a background rebuild whenever a call finds the directory changed — so no
   call ever lists it, whatever name is asked for and however many scripts
   are stored, and every step one call takes (the directory stat, the
   candidate probe, the open, and the re-check after it) is bound to a
   single directory descriptor retained for that call rather than a fresh
   resolution of the path each time; a call that lands while that rebuild is
   scheduled or in
   flight is refused once, exactly like a call against a directory it has
   never seen, rather than answered from what the index held a moment ago —
   an entry the index once listed under an earlier spelling must never still
   authorize it after a rename. The index authorizes a hit only once its
   directory timestamp is provably settled — old enough (roughly two
   seconds, the coarsest directory-timestamp granularity assumed) that no
   write could still be landing on the same tick unseen — so a matching
   generation alone is not enough; a script added to (or renamed within) the
   directory becomes callable by agent tokens only after the index has both
   refreshed and settled — retry a call refused in that window, up to
   roughly two seconds — while administrators see the change immediately.
   Linux, the BSDs, darwin and Windows all answer from this same index, so
   a differently-cased name and one that is not stored at all cost the
   same — both are plain misses. darwin re-checks the opened descriptor's
   on-disk spelling as an extra, belt-and-suspenders proof; Windows performs
   every step of a call — probing, opening, and the background listing that
   refreshes the index — relative to ONE directory handle retained for the
   whole call, so a rename or a reparse point cannot redirect where a
   "relative" open lands, and the post-open check need only confirm the
   opened descriptor's own name; an ambiguous or unusable script is
   reported by
   name and reason only, without its host path or a raw OS error;
   the REST listing `GET /api/v1/code/scripts` answers an agent token with
   `403`; administrators keep today's listing and paths; and every
   `call_tool()` a script makes is checked against the caller's server scope
   and permission tier — a hidden server is refused exactly as a nonexistent
   one. The published `code_execution` definition says so — enumeration is
   administrator-only and an agent-token caller must already know the script
   name. See
   [Stored scripts](https://docs.mcpproxy.app/code_execution/overview/#stored-scripts).

Do **not** place server names, hostnames, credentials, tokens or any other
secret in either — a scoped agent can read them, and a script's constant return
value is as public as its name.

## Administrative Operations Are Admin-Only

Agent tokens can **discover and call** tools (within their scope and permission tier) but can **never administer servers**. Server-mutating operations require the admin API key (or a local tray/socket connection, which is admin by OS-level auth) on **every** surface — the MCP tools and the REST API share one policy (`internal/auth`), so an agent cannot do over HTTP what it is blocked from doing over MCP.

Denied to agent tokens on both surfaces:

- **Lifecycle**: add, remove, update/patch, enable, disable, restart, reconnect, refresh/discover-tools, add-from-registry, login/logout, move-config-value-to-secret
- **Security state**: quarantine, unquarantine, tool approve/block, and the security scanner (scan start/cancel, security approve/reject)
- **Config & registries**: applying/patching configuration (which can add/remove/enable/disable servers) and mutating registry sources — an agent must not bypass the per-server gate by rewriting config or a registry wholesale

On the MCP surface (`upstream_servers`, `quarantine_security`) these return a tool error; on the REST surface (mutating `/api/v1/servers/...`, `/api/v1/config/...`, and `/api/v1/registries/...` routes) they return **`403 Forbidden`** (`operation requires admin access`). Read-only operations stay available to scoped tokens: `upstream_servers` `list`/`tail_log`, `GET /api/v1/servers`, per-server diagnostics, registry reads, and `GET /api/v1/index/search` (which honors quarantine — a quarantined server's tools are withheld from search on every surface). Those reads are **scope-filtered** as described above. `GET /api/v1/config` is the exception: it is an admin document (it carries the global `api_key`, every server's credentials, and a second enumeration of server names under `profiles[].servers`), so it returns `403` for an agent token rather than a filtered view.

**Log attribution on `tail_log`.** Per-server log files are named from a
*sanitised* server name, so two configured servers can share one file —
`a/b` and `a_b` both write `server-a_b.log`, and on a case-insensitive
filesystem so do `A` and `a`. `upstream_servers` `tail_log` therefore
returns a scoped token only the records **attributable to the server it
named**: every record mcpproxy writes carries its writer's server identity,
and the reader filters on it *before* applying the line limit, so
`lines_returned` counts the authorized tail and a co-owner's interleaved
record never displaces an authorized one. The rule is the same whether or
not a co-owner exists — a scoped caller never gets a whole-file refusal that
depends on another server sharing the file, and a single over-long line in
the shared file (longer than 1 MiB) is skipped rather than failing the read.
Withheld from scoped callers:
records with no writer identity (lines written before this rule existed,
hand-appended lines, torn fragments), records about a container — by id,
name or count — that do not prove the container's owner (`container_owner`,
written by container housekeeping since this rule — earlier housekeeping
records are treated as non-attributable), and records whose subject is
another server (an OAuth callback tear-down that an earlier version routed
through the wrong server's logger), and a child process's own output line
that names a container — Docker's `docker run` name-conflict error, for
instance, quotes the *other* container's name and id when two servers'
generated container names collide (`a/b` and `a-b` both produce
`mcpproxy-a-b-…`) — the same rule covers the "Connection failed" record
whose error re-emits that stderr. For the same reason a scoped token's
`connection_status.last_error` (on `tail_log` and `list`, and the health
detail derived from it) has container ids, canonical container names and
Docker's name-conflict phrase replaced by `[container]`; a server that is
itself *named* like a container (`mcpproxy-tenant-abcd`) is not a container
mention, so its ordinary child output stays attributable. Retained effects: rotation and retention
of a shared file stay shared, so a co-owner's output can rotate an authorized
record out of the readable history; and child process output is attributed
to the server whose process wrote it — a child cannot forge another server's
identity. The administrator readers — `tail_log` with the API key or over the
local socket, and `mcpproxy upstream logs` — keep the whole file exactly as
before; a profile on the URL (`/mcp/p/<slug>`) bounds *which* server an
administrator may name, not which records of it they see. The REST endpoint
`GET /api/v1/servers/{id}/logs` is **not** attributed: it serves the whole
shared file to any caller entitled to the server name, agent tokens
included. Until it is aligned with `tail_log`, do not rely on it to keep a
co-owner's records from a scoped token — the REST management API's scope
policy is a separate piece of work.

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

- **`set_profile("other")` is rejected** — a pinned token cannot switch its session to a different profile (switching to its own pinned profile while it still has reach, or clearing, is allowed; clearing reports `active_profile: ""` and the pin's servers).
- **`/mcp/p/<other>` returns `404`** — connecting to any profile URL other than the pinned one is refused with the same non-disclosing `unknown profile` body every other non-selectable slug produces (see [404 responses](./profiles.md#404-responses)); the pinned profile's own URL works while the pin has reach.
- **The pin is the highest-precedence resolver source**, above an explicit `/mcp/p/<slug>` URL scope and above a session `set_profile` selection.
- **Every dispatch surface resolves it** — `retrieve_tools`, `describe_tool`, `call_tool_*`, the `code_execution` sandbox, direct-routing mode (`server__tool`) and [preflight](./tools-preflight.md) all bound themselves by the pin, so no routing mode is a way around it.

Resolution precedence (highest wins):

```
1. agent-token profile_pin   (server-enforced; this section)
2. /mcp/p/<slug> URL scope    (per-request override)
3. set_profile session state  (base /mcp endpoint default for the session)
4. none                        (no profile filtering — all allowed servers)
```

**Validation & config changes**: the pinned slug must name a configured profile at creation time (creation is rejected otherwise). If the profile is **later removed** from the configuration, the pin resolves to a **deny-all scope**: the token sees no upstream servers and no tools, on the MCP session path and in [preflight](./tools-preflight.md#disclosure-tiers) alike. A pin with **zero reach** — the profile still exists but is empty, names only unconfigured servers, or no longer overlaps the token's `allowed_servers` — is treated exactly like a deleted one on `set_profile` and `/mcp/p/<pin>`, so the token cannot tell whether its own pin still exists. A request under a **deleted** pin is logged with a warning naming the removed profile, not hard-failed at the transport; a refused `/mcp/p/<pin>` initialization (deleted or zero-reach alike) is logged as `profile URL refused for scoped caller`. The pin is a restriction the operator applied, so losing the profile it names must never hand the token a wider view than it had the day before — re-create the profile, or re-mint the token against a live one, to restore it. Pinning composes with server scoping and permission tiers: a request must satisfy **all** of them.

The pin is shown by `token list` (PROFILE PIN column) and `token show` (Profile Pin field), and is preserved across `token regenerate`.

## Managing Tokens

### Token Limit

A deployment stores at most **100 agent tokens**, and in the server edition
each signed-in user may hold at most **25** of them. Revoked tokens still
occupy a slot until they are permanently deleted, so once a limit is reached,
creating another token answers `409 Conflict`:

- **Your own quota (server edition, 25 per user).** The message tells you it is
  your limit; permanently delete one of your unused tokens to free a slot. The
  quota keeps one user from taking the whole pool, but every stored token still
  counts toward the deployment limit below, so a deployment whose records add
  up to 100 refuses the next token for everyone. The quota is checked first: a
  user already at 25 always sees this message, whatever the deployment total.
- **The deployment limit (100 stored records).** In the personal edition every
  token belongs to the one operator, so this is the only limit and deleting one
  of your tokens frees a slot. In the server edition a caller who is still under
  their own quota gets this message; it says the limit is shared and points at
  an administrator, because deleting your own tokens may not free a slot that
  other users' records are filling.

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

### Documented invariant (Spec 107 FR-046)

A person who signs in through the team's IdP — directly through their session
on the REST API and Web UI, or through any agent token they mint — can see and
use exactly the servers their group grants, and cannot learn about or act on
any other server through proxy-produced data; every tool-call authorization
decision about them is recorded on the audit line with the real server name,
which is never echoed to them.

- **Covered surfaces.** The server-edition REST routes (`/api/v1/auth/*`,
  `/user/*`, `/admin/*`); the [core REST API](../development/server-edition-multiuser-auth.md#tenant-session-principal-on-core-rest-spec-107-pr-c)
  and `/events` for the tenant session principal; the HTTP MCP surfaces
  (`/mcp`) for agent tokens a tenant owns, scoped exactly as described
  throughout this page; and the Web UI, which reaches nothing a tenant's own
  session and owned tokens could not already reach. `/mcp` never accepts a
  session cookie or user JWT — a tenant reaches tools only through an agent
  token they own.
- **Staleness bound (FR-011), including the closed JWT self-renewal.** Groups
  refresh only at login: `session_ttl + max(bearer_token_ttl, longest owned
  token expiry ≤ 365 days)`. This bound holds specifically because a bearer
  JWT can no longer renew itself through `POST /auth/token`, nor mint or
  rotate an agent token through `POST /user/tokens(/…/regenerate)` — those
  three doors accept only a live session cookie (see
  [Freshness bound](../development/server-edition-multiuser-auth.md#freshness-bound-and-session-cookie-only-minting-doors-fr-011)).
  An administrator `disable` is immediate and is not subject to this bound.
- **Retained Spec 105 effects.** Everything Spec 105 already scopes for an
  agent token — [server scoping](#server-scoping), [administrative denial](#administrative-operations-are-admin-only),
  and [`read_cache`](#server-scoping) authorization-stamped entries — applies
  identically whether a server is excluded by group (this spec) or by token
  scope (Spec 105); a group-excluded server is indistinguishable from a
  nonexistent one on every one of those surfaces.
- **Still-open Spec 105 items.** Three surfaces on `main` still leak the
  *existence* (not the content) of an excluded server, whether excluded by
  group or by token: `retrieve_tools`'s `usage_summary`/`session_risk`
  statistics, the "Available servers" error text, and the scope-denial text.
  This spec adds nothing new to that leak and closes it the moment the
  corresponding Spec 105 item merges — it is not something a server-edition
  deployment can configure around today.
- **Single-replica assumption.** Pending OAuth login state, the SSE
  per-frame principal re-resolution and the entitlement computation above all
  run in-process with no shared cross-replica store; a second replica of the
  server edition is unsupported.

### Server-edition incident response

Administrators authenticated through a server-edition session or bearer JWT can list safe metadata for all owners with `GET /api/v1/admin/tokens`. Each entry includes `user_id`, `name`, scope, permissions, timestamps, prefix, profile pin, and revocation state. Raw credentials and token hashes are never listed.

Revoke one tenant credential with `POST /api/v1/admin/users/{user_id}/tokens/revoke` and JSON body `{"name":"exact stored name"}`. The name is body data so names containing slashes, percent signs, spaces, or Unicode remain addressable through routers and reverse proxies. The older `POST /api/v1/admin/users/{user_id}/tokens/{name}/revoke` form remains available for URL-safe names. Owner and name identify the credential together; another user's same-named token is unaffected. Revocation is durable and takes effect on the next authenticated request, including requests using an existing MCP session.

Owned tokens are checked against the owner's current server entitlement on every authentication. Unsharing an administrator-configured server removes it from a tenant token's effective scope without rotation or restart. Explicit scopes never gain additional servers; historical wildcard grants are bounded by current entitlement. Current administrator owners may still access administrator-configured servers. Missing owners, disabled accounts, and entitlement lookup errors fail closed. Ownerless operator tokens retain their existing behavior.

Calls already authorized and running are not cancelled. A long-lived SSE `/events` response revalidates its agent token before each status or runtime event: unsharing immediately narrows the next frame, while token revocation closes the stream before another event is delivered.

### Sharing CLI status safely

`mcpproxy status` masks the API key in both its key field and Web UI URL across table, JSON, and YAML output. `--show-key` reveals the key. `--web-url` deliberately prints a usable login URL containing the unmasked key; treat that output as a credential. `--reset-key` also explicitly reveals the newly generated key.

---
title: "Profiles"
sidebar_label: "Profiles"
description: "Named views over your upstream servers with a tool policy: tier caps, rules, client and token bindings, anonymous confinement and an access explainer."
---

# Profiles

:::note Profiles are optional
You do not need a profile to use MCPProxy. Without an effective profile (no profiles configured, or a caller that none of them applies to), no profile-level restriction applies to that caller: it can reach every configured server, unless its own credential is scoped (for example an agent token with an allowed-servers list). Use profiles when you need to scope access, for example to give one client or token read-only access to a few servers.
:::

A **profile** is a named view over your upstream servers plus a **tool policy**. It decides which servers a caller reaches, which of their tools the caller can discover and call, and whether the caller gets code execution and the management tools. The same profile is used by every surface that lets you work with MCPProxy: the config file, the Web UI and macOS app (**Profiles** in the sidebar), the CLI (`mcpproxy profile ...`), the MCP `profiles` tool and the REST API.

A profile is enforced by the core, on every path a tool can be discovered or called through. "Work Read-only" really is read-only: a session under it cannot find, describe or call a write, destructive, denied or unclassified tool, whichever way it connected.

Three ideas fit together:

- A **profile** is the policy: servers, a tier cap, tool rules.
- A **binding** attaches a profile to a client's credential or to an agent token, so the profile follows the caller without any URL or setup in the client. A binding is **locked** (the caller can never leave it) or **switchable** (within the profile's `switchable_to`).
- `anonymous_profile` confines callers that present no credential.

## Quick start

This quick start is for users who need scoped access. If you do not, you can skip profiles entirely.

```json
{
  "require_mcp_auth": true,
  "profiles": [
    {
      "name": "work-readonly",
      "title": "Work · Read-only",
      "servers": ["github", "notion"],
      "max_tier": "read"
    },
    { "name": "work-full", "servers": ["github", "notion", "filesystem"] }
  ]
}
```

Then bind a client and a token to it (the client's own config file is written once, with its own credential):

```bash
mcpproxy connect cursor --profile work-readonly --lock
mcpproxy token create --name ci --profile work-readonly
mcpproxy profile show work-readonly --effective
```

See [Profile and client commands](../cli/profile-commands.md) for every command, and [Connect clients](./connect-clients.md) for the credentials.

## Profile fields

| Field | Values | Default | Meaning |
|---|---|---|---|
| `name` | slug `^[a-z0-9][a-z0-9_-]{0,62}$` | required | Reserved: `all`, `code`, `call`, `p` (URL segments) and `active`, `try` (REST routes) |
| `servers` | list of server names | required | The servers the profile reaches. An empty list is a legal "deny everything" profile |
| `title` | text, at most 80 characters | name | Shown by the UIs, the explainer and activity. Never shown to an agent |
| `description` | text, at most 500 characters | none | Free text |
| `max_tier` | `read`, `write`, `destructive` | no cap | The highest tool tier the profile admits |
| `unannotated` | `deny`, `as_write`, `as_read` | see below | What to do with a tool that declares no tier |
| `tools.allow` | `server:tool` patterns | none | Admit a tool the cap would hide (not a server outside `servers`) |
| `tools.deny` | `server:tool` patterns | none | Hide a tool. Deny beats allow when both match |
| `tools.classify` | map `server:tool` to `read`, `write` or `destructive` | none | Give an **unannotated** tool a tier in this profile. Ignored for a tool that carries its own annotations |
| `code_execution` | `true`, `false` | inherit | Whether the `code_execution` tool exists for the profile |
| `management_tools` | `true`, `false` | inherit | Whether `upstream_servers` and `quarantine_security` are visible under the profile |
| `switchable_to` | list of profile names | none | The profiles a client bound to this profile (or a confined anonymous caller) may switch to with `set_profile` |

The field names are identical in the config file, REST, the CLI (as kebab-case flags, `--max-tier`), the MCP `profiles` tool and the UIs. Enum values are spelled the same everywhere (`as_write`, `as_read`).

**Fail-closed defaults.** With `max_tier` `read` or `write`, an unset `unannotated` means `deny` and an unset `code_execution` means off, because a tool of unknown risk, or a tool built to orchestrate other tools, would otherwise sidestep the cap. With `max_tier` `destructive` or no cap, `unannotated` defaults to `as_read` and `code_execution` follows the global `enable_code_execution` setting. A profile can only narrow: it never widens a token's permissions, bypasses quarantine or approval, or overrides `read_only_mode`, `disable_management` or `enable_code_execution`.

**Legacy profiles keep working.** A profile that sets only `name` and `servers` behaves exactly as before: no cap, unannotated tools count as read, no rules.

### Tiers

A tool's tier comes from its MCP annotations: `destructiveHint: true` is `destructive`; `readOnlyHint: false` is `write`; `readOnlyHint: true` is `read`; a tool with neither hint is **unannotated**. MCPProxy never guesses a tier for an unannotated tool; a profile decides through `unannotated` or a `tools.classify` entry.

### How a tool is decided

One predicate computes the decision, and everything uses it: discovery, execution, the Tools "view as" listing, "Try it" and the explainer. In order:

1. The tool's server is not in `servers`: **`server_not_in_profile`**.
2. The tool matches a `tools.deny` pattern: **`denied_by_rule`**.
3. The tool matches a `tools.allow` pattern: admitted, even above the cap.
4. The tool is unannotated, unclassified and `unannotated` is `deny`: **`unannotated_hidden`**.
5. The tool's tier is above `max_tier`: **`above_tier_cap`**.
6. Otherwise admitted.

Patterns are `server:tool` with `*` as the only wildcard, matched case-sensitively. A pattern naming a server outside `servers` is saved with a warning and ignored. Saving a profile (create or replace) also returns one warning per `tools.classify` entry that no longer applies (`classify entry "server:tool" is stale: annotated|missing`), the same entries the effective-tools view reports as stale.

## What a caller sees

| Surface | A tool the profile excludes |
|---|---|
| `retrieve_tools` | Left out **before** the result limit, and counted in `hidden_by_profile` without naming any of them. The response names the caller's own profile (`profile`) when it came from the caller's own credential or choice, never the operator's anonymous profile |
| `describe_tool` | The same not-found answer as a tool that does not exist |
| `call_tool_read`, `call_tool_write`, `call_tool_destructive`, `/mcp/all`, REST `/tools/call` | Refused before any upstream call with `blocked by profile: <server>:<tool> is a <tier> tool; profile "<title>" (<slug>) allows <cap> tools only`, or `... is denied by a rule in profile "<title>" (<slug>)`, or `... has no tier annotation; an operator can classify it in profile "<title>" (<slug>) to allow it` |
| `code_execution` | Absent and refused when the profile turns it off; every nested `call_tool` goes through the same gate and is recorded with the same `block_reason` |

A refusal names the profile only to a caller whose effective profile is its own: one that came from the caller's pin, its client binding, the URL or `set_profile`, so an agent can tell the operator which profile to change. A caller that connects without a credential and falls under `anonymous_profile` gets the same refusal without the profile name (`... this profile allows <cap> tools only`, `... is denied by a profile rule`, `... in the profile to allow it`), and so does a caller whose profile no longer exists, so the operator's anonymous confinement is never handed out. The title is quoted and escaped, so it cannot add a line to the refusal. The operator sees the profile in the activity record and in the explainer. A blocked call is recorded with `status=blocked` and `block_reason` `profile_tier`, `profile_rule` or `profile_unannotated` (`profile_code_execution` and `profile_management` for the tools above). A nested `call_tool` in `code_execution` to a server outside the profile is recorded with `block_reason` `profile_server_scope`; the text the script receives stays the non-disclosing out-of-scope refusal.

A profile does not change prompts: tier and rules apply to tools only.

## Which profile applies

When more than one source could select a profile, the highest wins:

| # | Source | Notes |
|---|---|---|
| 1 | **pin** | A **locked** client credential, or an [agent token](./agent-tokens.md#profile-pinning) with a profile. Server-enforced and immutable for the connection. A pin whose profile was deleted denies everything instead of widening |
| 2 | **url** | `/mcp/p/<slug>`. Authoritative for that request. A locked or confined caller may name only its own profile (or, if switchable, one in `switchable_to`) |
| 3 | **session** | The `set_profile` selection. Re-checked on every request |
| 4 | **binding** | The profile of a **switchable** client credential |
| 5 | **anonymous** | `anonymous_profile`, for a caller with no credential |
| 6 | **none** | No profile: all servers |

Activity records, session rows and the Clients page show the profile together with its source (`pin`, `binding`, `url`, `session`, `anonymous`). The UIs word them as "locked by credential", "switchable", "from URL", "switched in session" and "anonymous".

### Client credentials and bindings

`mcpproxy connect` (and the Connect screens) never write the instance admin API key into a client's config. They mint a per-client credential (`mcp_cli_...`), bound to a profile, valid on MCP endpoints only. Reassigning a client to another profile takes effect on its next request without touching its config file, and its live session is told its tool list changed. Reassign from the Web UI Clients page, the macOS Clients view or tray submenu, `mcpproxy client set-profile`, or the `profiles` MCP tool. Details are in [Connect clients](./connect-clients.md).

A **locked** client cannot switch: `set_profile` answers `cannot switch to profile '<name>': this client's profile is locked`, and `/mcp/p/<other>` gets the same refusal as an unknown profile. A **switchable** client may switch to the profiles in its bound profile's `switchable_to` and nowhere else.

### Callers without a credential

Set `anonymous_profile` to confine every caller that presents no credential (or an unrecognised token while `require_mcp_auth` is off) to that profile. Set it in the config, in **Settings, Security, Anonymous callers** (Web and macOS) or with `mcpproxy profile anonymous <name>`. Changing it applies immediately, without a restart.

### The binding guard

While `require_mcp_auth` is **off**, a client bound to a named profile could escape it by simply omitting its credential. MCPProxy refuses any change that would leave such a binding bypassable: connecting, rebinding, bulk moves, and also profile edits, classification changes, deletes and `reassign_to` that narrow what a binding reaches or widen what anonymous callers reach. The refusal is `409` with code `binding_bypassable_without_auth`:

```
a client bound to profile <p> could escape it by omitting its credential while require_mcp_auth is off
```

It lists the bindings concerned and two fixes: turn `require_mcp_auth` on, or set `anonymous_profile` to a profile that is not wider than the binding. After either, the same request succeeds. "Wider" compares everything a caller can reach: servers, tier cap, unannotated handling, allow and deny rules, `switchable_to` reachability and the code-execution and management capabilities. The refusal text and code are the same on REST, the CLI, the Web UI, macOS and MCP.

## Management tools

`management_tools` decides whether `upstream_servers` and `quarantine_security` are visible under a profile: `false` hides and refuses them, `true` shows them, unset keeps the pre-profile behaviour. It never widens a credential: an agent token or client credential, and a confined anonymous caller, can only `list` and `tail_log` servers inside their scope, and `quarantine_security` stays administrator-only; adding, changing, restarting or removing servers remains an operator action. The `profiles` tool is separate: it is listed only for an administrator session (API key or socket), and only when the session's effective profile is none or sets `management_tools: true`. The `credentials` tool, which issues, lists and revokes the locked client credentials and profile-pinned agent tokens a profile is used through, follows exactly the same rule; see [Credential lifecycle over MCP](./mcp-credential-lifecycle.md).

## Managing profiles from every surface

| | Web UI | macOS | CLI | MCP | REST |
|---|---|---|---|---|---|
| List with tool counts by tier | Profiles page | Profiles view | `profile list` | `profiles list` | `GET /profiles` |
| Effective tools with reasons | Editor table | Editor table | `profile show --effective` | `profiles effective_tools` | `GET /profiles/{name}/effective-tools` |
| Create, update | Editor | Editor | `profile create`, `update` | `profiles create`, `update` | `POST /profiles`, `PUT /profiles/{name}` |
| Rename, delete | Editor | Editor | `profile rename`, `delete` | `profiles rename`, `delete` | `POST /profiles/{name}/rename`, `DELETE /profiles/{name}` |
| Classify a tool | Editor table | Editor table | `profile classify` | `profiles classify` | `PUT /profiles/{name}` |
| Try a draft | Try it | Try it | `profile try` | not offered | `POST /profiles/try` |
| Explain access | Explain access | Explain access | `access explain` | `profiles explain` | `GET /access/explain` |

**Try it** runs a real `retrieve_tools` under the unsaved draft and shows what is returned and what is hidden with reasons, without saving anything. The tool table and its visible/hidden counts show the saved profile; while you have unsaved edits they are labelled "Saved profile". Try it says whether it used your unsaved edits.

**Deleting a profile** that clients, tokens or `anonymous_profile` still use is refused with `409 profile_in_use` (listing who) until you give `reassign_to`; `force` leaves the references dangling, which denies everything and never widens. A profile that is the `anonymous_profile` is refused even with `force`. **Renaming** moves every pin, binding, `switchable_to` and `anonymous_profile` reference to the new name.

The **access explainer** answers "why can't Cursor use `github:create_issue`?" by walking the chain in the order enforcement uses: credential, profile, server in scope, tool rule, tier cap, token permission, global gate, server state, tool approval. It stops at the first failing link, names the fix, and its verdict (`allowed`, `blocked`, `hidden`) is computed by the same predicate as an actual call.

## Attribution and filters

Every activity record carries the profile, its source, the client id, the token name and (advisory) the client's self-reported name. Activity, Sessions, Usage, Tools, Servers, Clients and Tokens can be filtered by `profile`, `client` and `token` in the Web UI (the filters live in the URL, so a link keeps them), in the macOS app, with `mcpproxy activity list --profile --client --token`, and with the REST query parameters of the same names. A record keeps the profile it was made under, so filtering by the profile a client had before you reassigned it still lists the earlier calls. `/tools?client=cursor` shows exactly what Cursor can see and call, with the reason for every row it cannot.

## Upgrading and downgrading

- **Upgrading.** Existing profiles and tokens keep working. Existing client entries that hold the admin API key or no credential are reported on the Clients page and by `mcpproxy doctor`; nothing is rewritten automatically. Use the previewed "Upgrade all clients holding the admin key" action (`mcpproxy client upgrade-admin-key-holders`), then rotate the admin API key.
- **The old "active profile".** `GET` and `PUT /api/v1/profiles/active` still answer, with a `Deprecation: true` header, for one more minor release and are called by no first-party surface. The Web UI header no longer has a "Profile:" switcher; a **Viewing** chip filters what you look at and is labelled as a view filter.
- **Downgrading.** A pre-profiles-v3 binary does not recognise `mcp_cli_` credentials. With `require_mcp_auth` on it rejects them (`401`); with it off it treats them like an omitted credential, which gives them unconfined access, and it knows nothing of `anonymous_profile` or bindings. Turn `require_mcp_auth` on **before downgrading** to a pre-108 binary, otherwise bound clients run unconfined.

## URL profiles and `set_profile`

Profiles are also addressable as permanent URLs at `/mcp/p/<slug>`, and an agent can switch inside a live session with `set_profile`. These mechanics are unchanged and are described below.

### Stateful selection: `set_profile`

The `set_profile` MCP tool switches the active profile **inside a live session** — no reconnect, no re-index:

```jsonc
// request
{ "name": "set_profile", "arguments": { "profile": "research" } }
// result
{ "active_profile": "research", "servers": ["research-srv"] }
```

- The selection is keyed by the MCP session id (stable per streamable-HTTP / SSE connection) and persists for the lifetime of that session.
- It applies to subsequent `retrieve_tools`, `call_tool_*`, `code_execution` and direct-mode (`server__tool`) calls on the base `/mcp` endpoint — `retrieve_tools` searches the profile's per-profile index directly.
- Passing an empty string (`""`) clears the selection and returns to all servers. `active_profile` always reports the **stored session selection** — `""` after a clear, even for a token with a [`profile_pin`](./agent-tokens.md#profile-pinning) — while `servers` reports the **effective scope** the session can actually reach after the update: the pin's servers for a pinned token (nothing once the pinned profile has been deleted), the URL profile on a `/mcp/p/<slug>` endpoint, otherwise the selection or every configured server.
- The `servers` list is always bounded by the caller's credential, using the same rule that scopes `retrieve_tools`: for an [agent token](./agent-tokens.md) scoped to specific servers it is the intersection of the effective profile (resolved pin > URL > session, see [Which profile applies](#which-profile-applies)) with the token's `allowed_servers`, so a token restricted to one server is never told about the others. On a `/mcp/p/<slug>` endpoint the URL still governs the request, so `set_profile("other")` there stores `other` as `active_profile` but reports `<slug> ∩ allowed_servers` in `servers`. API-key and socket callers see the full lists.
- An unknown slug is rejected. An administrator (API key, socket, anonymous back-compat) gets the discovery affordance: `unknown profile '<slug>' (available: research, deploy)`. An agent token gets `unknown profile '<slug>'` with no list at all: it may select only the profiles overlapping its `allowed_servers` (or its pin while the pin still has reach), and a profile entirely outside its reach (an empty profile, a profile whose servers are all outside `allowed_servers`, or the token's own pin once it no longer exists or no longer overlaps the token's servers) is rejected with that same error rather than confirmed as existing. A pinned token asking for any profile OTHER than its pin (see [profile pinning](./agent-tokens.md#profile-pinning)) is rejected with that same `unknown profile '<slug>'` error too — never a distinct "pinned to..." message, which would let the token confirm from the wording alone that it is pinned, and to what, from a refusal aimed at a different slug. The check looks only at the requested slug (and the token's pin) and tests the token's own `allowed_servers` against that profile's precomputed server set — its cost does not depend on how many other profiles are configured, on how many servers the requested profile declares or on how many servers are configured at all, only on the size of the token's own grant — so a token cannot learn which profiles or servers exist, or whether it is pinned, from `set_profile`, by body or by timing.
- A [client credential](#client-credentials-and-bindings) (`mcp_cli_...`) is told why a switch failed, by its own binding mode alone: a locked client gets `cannot switch to profile '<slug>': this client's profile is locked`, a switchable client `cannot switch to profile '<slug>': it is not a profile this client may switch to`. The text never depends on whether the slug exists or is in `switchable_to` (every refused slug gets the same text with the slug substituted) and never names the bound profile. Setting `profile` to the client's own bound profile is still admitted.
- Session state is cleared automatically on session close.

`set_profile` is available on the default `/mcp` server and the `call_tool` / `code_execution` routing-mode servers.

### REST API

| Method & path | Description |
|---------------|-------------|
| `GET /api/v1/profiles` | List profiles with their policy, effective servers, tool counts by tier, the unannotated count, who uses each (administrators only) and last-24h calls and blocked calls |
| `POST /api/v1/profiles`, `PUT /api/v1/profiles/{name}`, `DELETE /api/v1/profiles/{name}`, `POST /api/v1/profiles/{name}/rename` | Create, replace, delete (`reassign_to`, `force`) and rename |
| `GET /api/v1/profiles/{name}/effective-tools` | Every tool with its verdict and reason |
| `POST /api/v1/profiles/try` | Run `retrieve_tools` under an unsaved draft |
| `GET /api/v1/access/explain` | Explain access for a client, token, profile or anonymous caller |
| `GET /api/v1/profiles/active`, `PUT /api/v1/profiles/active` | **Deprecated**: the server-level default for UI surfaces. They send a `Deprecation: true` header and are removed in the next minor release |

The deprecated "active profile" is independent of, and does not override, a live MCP session's `set_profile` selection. All responses use the standard `{ "success", "data" }` envelope and require the API key. Clients, tokens and activity filters are in the [REST API reference](../api/rest-api.md).

### Activity logging

Tool-call activity records carry the profile and its source (`profile`, `profile_source`), plus `client_id`, `token_name` and `block_reason` when they apply. Records written before profiles v3 keep the legacy `metadata["profile"]` and remain filterable.

### Per-profile search index

Each profile gets a physically separate Bleve index so switching profiles is fast and a config reload that changes one profile does not re-index the others.

Layout under the data dir (`~/.mcpproxy/` by default):

```
index.bleve/                 # shared default index — all servers' tools (used by /mcp)
index.bleve/profiles/<slug>/ # one index per profile — only that profile's servers' tools
```

Notes:

- Per-profile indexes live under `index.bleve/profiles/` (not directly under `index.bleve/<slug>/`) so they never collide with Bleve's own internal files and `store/` subdirectory.
- A per-profile index is a derived view: it is (re)built from the shared default index, so the shared index remains the source of truth and the allow-all fallback for `/mcp`.
- `<slug>` is the validated profile name (`^[a-z0-9][a-z0-9_-]{0,62}$`), so the directory name is always filesystem-safe.

Lifecycle:

| Event | Effect |
|-------|--------|
| Profile added / first use | Its index is built lazily from the shared index. |
| A member server's tools change | Only the profiles that include that server are rebuilt. |
| Profile membership changes on reload | Only the affected profile is rebuilt; others are untouched. |
| Profile removed from config | Its index directory is deleted (including orphans left by a prior run). |
| Server disabled / quarantined | Profiles that include it are refreshed so its tools drop out. |

### Hot reload

Profile changes, and a change of `anonymous_profile`, take effect without a restart: new requests use the new policy and the sessions a change governs receive `notifications/tools/list_changed`. In-flight sessions keep a consistent snapshot for the request they are serving.

### 404 responses

For API-key, socket and (when `require_mcp_auth` is off) unauthenticated callers:

| Condition | Body |
|-----------|------|
| No profiles configured | `{"error":"no profiles configured"}` |
| Unknown slug | `{"error":"unknown profile '<slug>'","available":["research","deploy"]}` |

An [agent token](./agent-tokens.md) may initialize through `/mcp/p/<slug>` only when that profile is one it could select with `set_profile` — its servers overlap the token's `allowed_servers`, or it is the token's pin and the pin still has reach. Every other request — a missing or deleted slug, a configured profile outside the token's reach, an empty profile, a pin mismatch, the slug-less `/mcp/p` and `/mcp/p/`, and an empty fleet — receives one and the same `404 {"error":"unknown profile '<slug>'"}` with no `available` list, and the check itself looks only at the requested slug (and the token's pin), testing the token's own `allowed_servers` against that profile's precomputed server set — its cost does not depend on how many other profiles are configured, on how many servers the requested profile declares, on how many servers are configured at all, or on whether this is the first request after a reload (the profile index is rebuilt when the configuration changes, not on demand); it scales only with the size of the token's own grant — so a scoped caller cannot learn which profiles or servers exist from the profile URL, by body or by timing. The refusal is silent towards the agent only: each one is logged (`profile URL refused for scoped caller`, with the token name, the requested slug and the remote address) so an operator can spot a token probing the slug space.

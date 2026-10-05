---
id: profile-commands
title: Profile and Client Commands
sidebar_label: Profile and Client Commands
sidebar_position: 4
description: CLI commands to define profiles, bind clients and tokens to them, and explain why a tool is or is not available
keywords: [profile, client, binding, token, access, explain, connect, cli, max_tier, anonymous_profile]
---

# Profile and Client Commands

A **profile** is a named view over your upstream servers with a tool policy: the highest tool tier it admits (`read`, `write` or `destructive`), rules for individual tools, and what happens to tools that declare no tier. A **client** is an AI tool that connects to MCPProxy (Cursor, Codex, a script); each client holds its own credential and is **bound** to a profile. See [Profiles](../features/profiles.md) for the model and [Connect clients](../features/connect-clients.md) for the credentials.

```
mcpproxy
├── profile   list | show | create | update | rename | delete | classify | try | anonymous
├── client    list | show | set-profile | lock | unlock | add | rotate | forget | upgrade-admin-key-holders
├── access    explain
├── connect   <client> [--profile ...]
└── token     create --profile ...
```

Every command except `connect` needs a running daemon and honours `-o json|yaml`; JSON output is exactly the REST `data` object. Every flag is the kebab-case spelling of the REST field it sets (`max_tier` is `--max-tier`), and the same operations exist in the [Web UI](../web-ui/dashboard.md), the macOS app and the MCP `profiles` tool, with the same names and the same refusal texts.

```bash
mcpproxy profile create work-readonly --servers github,notion --max-tier read
mcpproxy connect cursor --profile work-readonly --lock
mcpproxy token create --name ci --profile work-readonly
mcpproxy profile show work-readonly --effective
```

## `mcpproxy connect`

Registers MCPProxy in an AI client's MCP configuration. Connect **never writes the instance admin API key**: every write carries a per-client credential (`mcp_cli_...`) that identifies the client and binds it to a profile. The credential is valid on MCP endpoints only, so it cannot open the REST API.

| Flag | Meaning |
|---|---|
| `--profile <name\|all>` | The profile the client's credential binds to; `all` is the built-in All servers scope. A new credential defaults to `all` (switchable); a reconnect keeps the client's existing binding unless `--profile` is given |
| `--lock` / `--switchable` | Lock the client to its profile so it can never switch (the default for a named profile), or let it switch within the profile's `switchable_to` (the default for `all`) |
| `--keyless` | Write an entry with no credential. Only possible while `require_mcp_auth` is off, and the client is then unidentified. Cannot be combined with `--profile`, `--lock` or `--switchable` |
| `--force`, `--name`, `--all` | Overwrite an existing entry; the server name in the client config; connect every supported client with the same options (a refusal for one client never aborts the others) |

The credential is shown masked; the secret is written only into the client's config file. Reconnecting over an active credential is a **staged rotation**: the old secret keeps working until the new config has been written.

With `require_mcp_auth` off, a named binding is refused (exit 1, nothing written) when an anonymous caller could reach more than the client, because the client could then escape its profile by omitting its credential. The refusal lists the fixes: turn `require_mcp_auth` on, or set `anonymous_profile` to a profile that is not wider than the binding.

With a running daemon the write goes through the daemon (it mints, records and notifies); with none, the command runs locally over the data directory with the strictest guard (any named binding is refused while `require_mcp_auth` is off).

## `mcpproxy profile`

| Command | What it does |
|---|---|
| `profile list` | One row per profile: servers, tool counts by tier, who uses it (administrators only), last-24h calls and blocked calls |
| `profile show <name> [--effective] [--client <id>] [--server <s>] [--reason <r>]` | The profile with the effective value of each policy field and its origin (`off (default under max tier read)`, `on (inherited)`). `--effective` lists every tool with its verdict (callable, visible, hidden) and reason; a classification that no longer applies is flagged `classification ignored — tool is now annotated` |
| `profile create <name> --servers a,b [--title] [--description] [--max-tier] [--unannotated deny\|as_write\|as_read] [--allow p,...] [--deny p,...] [--code-execution on\|off\|inherit] [--management-tools on\|off\|inherit] [--switchable-to x,y]` | Create a profile. `--switchable-to ''` means "none"; `inherit` leaves a field unset. `as-write` and `as-read` are accepted as aliases and stored as `as_write` and `as_read` |
| `profile update <name> [create flags] [--add-server] [--remove-server] [--add-allow] [--remove-allow] [--add-deny] [--remove-deny] [--clear-<field>]` | Read the stored profile, apply the flags (set, then add/remove, then clear) and write it back. Contradicting flags are rejected before any request. REST has no ETag, so a concurrent edit between the read and the write is lost (last writer wins) |
| `profile rename <old> <new>` | Rename and move every client binding and token pin; prints what moved |
| `profile delete <name> [--reassign-to <p>] [--force]` | Exits 1 and prints who uses the profile unless `--reassign-to` moves them or `--force` leaves them dangling (a dangling reference denies everything, never widens). The `anonymous_profile` can only be deleted with `--reassign-to` |
| `profile classify <name> <server:tool> read\|write\|destructive` / `--clear` | Give an unannotated tool a tier in this profile |
| `profile try <name> --query "..." [--limit N] [--set k=v ...]` | Preview what `retrieve_tools` would return under a draft (the saved profile, or a new one when the name does not exist, plus `--set` overrides such as `max_tier=write`); nothing is saved |
| `profile anonymous [<name> \| --clear]` | Print, set or clear the profile callers without a credential are confined to |

A write that would let a client bound to a profile escape it while `require_mcp_auth` is off exits 1 with the refusal, the bindings it concerns and the fixes (turn `require_mcp_auth` on, or `mcpproxy profile anonymous <p>`).

## `mcpproxy client`

`client list` and `client show` carry the credential and binding columns `CREDENTIAL PROFILE MODE SOURCE BLOCKED 24H` after the presence columns; `client list --profile <p|->` and `--client <id>` filter by the client's current binding. Warnings print to stderr with the command that fixes them.

| Command | What it does |
|---|---|
| `client set-profile <id> <profile\|all> [--lock\|--switchable]` | Rebind a client. Without a flag the current mode is kept; `all` is All servers. The client's config file is not touched, so the change takes effect on its next request and its live session is told its tool list changed |
| `client set-profile --from-profile <p\|all> --to-profile <p\|all> [--lock\|--switchable]` | Move every client of one profile to another; skipped clients (no client credential, or refused by the guard) print to stderr and the command exits 0 |
| `client lock <id>` / `client unlock <id>` | Keep the client's profile and change only its mode |
| `client add <id> [--display-name N] [--profile P] [--lock\|--switchable] [--expires-in 90d]` | A custom client (a script, a CI job) with its own credential, printed once with a header snippet |
| `client rotate <id> [--yes]` / `--finalize` | Replace a credential without cutting the client off. A supported client previews the config change and asks to confirm (`--yes` for scripts; a non-interactive run without it exits 1); a custom client gets the new secret once and stays pending until `--finalize` or 24 hours |
| `client upgrade-admin-key-holders [--profile P\|all] [--lock\|--switchable] [--yes]` | Replace the admin API key in every supported client's config with a per-client credential, after a preview. With `--profile` and `require_mcp_auth` off the guard can refuse: the command prints the preview and the refusal, exits 1 and changes nothing. It ends with the step that remains: rotate the admin API key |
| `client forget <id> [--disconnect]` | Revoke the credential without touching the client's config; with `--disconnect` also remove the config entry of a supported client |

`mcpproxy disconnect <client>` is the reverse of `connect`: it removes the config entry **and revokes the client's credential** (`client-<id>`), so a secret copied out of the config stops authenticating at once. With a running daemon the command calls `DELETE /api/v1/connect/{client}`; without one it revokes over `config.db`. It prints `Credential: revoked (token client-cursor)`, or `Credential: none to revoke` for a client that had none. If the entry was removed but the revoke failed, the command still exits 0, prints `Credential: NOT revoked (...)` and the fix is `mcpproxy client forget <id>`. Undoing a disconnect restores the config file only; the credential stays revoked and a later `connect` mints a new one. The credential belongs to the client, so `--name <entry>` for a non-default entry revokes it too.

A client without an active client credential exits 1 with `mcpproxy connect <id> --profile <p>` as the fix.

## `mcpproxy access explain`

```bash
mcpproxy access explain --tool github:create_issue (--client cursor | --token ci | --profile work-readonly | --anonymous)
```

Walks the gates a call meets (credential, profile, server in scope, tool rule, tier cap, token permission, global gate, server state, tool approval), prints the verdict (`allowed`, `blocked`, `hidden`) and the fixes in preference order, each with the command that performs it. The verdict is computed by the same predicate that enforces the call, so it never disagrees with what actually happens. The move-client fix prints `mcpproxy client set-profile <client> <destination-profile>` with the profile it names. Exit code 0 whenever an explanation is produced: the verdict is data.

## `mcpproxy token`

- `token create --profile <p>` pins the token to a profile; its scope comes from the profile. `--servers`/`--permissions` are optional with `--profile`; without it at least one is required (a legacy scope, which prints a hint to prefer a profile). `--profile-pin` still works as a hidden alias and prints a deprecation notice. Names starting with `client-` are reserved for client credentials.
- `token list` columns: `NAME PREFIX KIND CLIENT PROFILE MODE SERVERS PERMISSIONS LEGACY SCOPE REVOKED EXPIRES`; `--profile <p|->` and `--token <name>` filter.
- `token show` prints `Kind`, `Client`, `Profile`, `Mode` and, for a legacy scope, how to migrate it.

## Filtering activity by profile, client and token

`mcpproxy activity list`, `watch`, `summary` and `export` take `--profile`, `--client` and `--token` (`-` selects records with none; `--agent` is a deprecated alias of `--token`). A record keeps the profile, client and token it was made under, so filtering by the profile a client used to have still lists its earlier calls after you reassign it. See [Activity commands](./activity-commands.md) for the full flag list.

```bash
mcpproxy activity list --client cursor --status blocked
```

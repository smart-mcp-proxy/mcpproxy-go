---
id: connect-clients
title: Connect Clients
sidebar_label: Connect Clients
sidebar_position: 12
description: Preview-before-write connect flow that registers mcpproxy in an MCP client's config file, with a sanitized disclosure of what is replaced and a precondition token that makes a stale preview unwritable
keywords: [connect, client, preview, precondition, claude desktop, cursor, vs code, opencode]
---

# Connect Clients

**Connect Clients** registers mcpproxy inside another MCP client's configuration
file (Claude Desktop, Cursor, VS Code, Codex, Gemini, OpenCode, …) so that client
talks to the proxy instead of to each upstream server directly. It is available
from the Web UI wizard, the macOS tray ("Connect Client…"), and the
`mcpproxy connect` CLI.

## Clients hub

The Clients hub in the Web UI and macOS app brings client presence, Endpoint &
mode, and Agent tokens together. The Clients tab shows lightweight presence
counts; expanding a row fetches its retained sessions on demand. The Endpoint &
mode tab lists the MCP endpoints and active routing mode, and the Agent tokens
tab manages credentials. `mcpproxy client list` shows the same presence rows,
while `mcpproxy client show <id>` includes the recorded sessions. An installed
config is distinct from a client that has initialized against MCPProxy; after a
successful write, follow that client's reload hint.

Both Clients hubs include an **Other client?** example for the default `/mcp`
endpoint. It contains no admin key; select and copy it into a compatible MCP
client's configuration, then restart that client to connect.

The UI flows are **preview → confirm → write** (the CLI writes directly, with
`--force` to overwrite an existing entry):

1. `GET /api/v1/connect/{client}/preview` renders the exact change — target
   config path, format, server key, and the entry that would be written (the
   client credential masked as `mcp_cli_••••`).
2. `POST /api/v1/connect/{client}` performs the write, taking a timestamped
   backup of an existing file first.
3. `POST /api/v1/connect/{client}/undo` reverts that write byte-for-byte (or
   removes the file the connect created).

Endpoint-level reference — request/response shapes, backup naming, undo
semantics, and the macOS App Data privacy prompt — lives in the
[REST API reference](../api/rest-api.md#connect-client-wizard).

## Client credentials (Spec 108)

Connect never writes the instance admin API key. Every write embeds a
**per-client credential** (`mcp_cli_…`) that identifies the client and binds it
to a profile (`profile`, `mode` locked or switchable; the default for a new
credential is All servers, switchable, and a reconnect keeps the existing
binding). The credential is accepted on MCP endpoints only — the REST API
answers it with `403` — and reconnecting over an active credential is a staged
rotation, so the old secret keeps working until the new config is written.

- `contains_api_key` is always `false`; the previewed `credential` is the masked
  client credential, and `profile`/`mode` echo the requested binding.
- `keyless: true` writes no credential and is only possible while
  `require_mcp_auth` is off (and never together with a profile).
- With `require_mcp_auth` off, a named binding is refused with
  `409 binding_bypassable_without_auth` when the client could escape its
  profile by omitting its credential; nothing is minted or written. A token
  named `client-<id>` that is held by a regular agent token is refused with
  `409` and `conflicting_token`.
- `GET /api/v1/connect/{client}` reports `credential_state`
  (`client`, `admin_key`, `none`, `revoked`, `expired`); the stat-only
  `GET /api/v1/connect` listing reports `unknown` because it never reads a
  config. Both are administrator-only.
- Undo is "as if the connect never happened": a credential the connect minted
  is revoked unless the restored config still holds it (`credential_revoked`).

Reassigning a connected client (`PUT /api/v1/clients/{client}/binding`) changes
its profile or mode in the token store only; the client's config file is never
touched and its live sessions are notified.

### Using client credentials

| To | Web UI and macOS | CLI |
|----|-----------------|-----|
| Connect a client to a profile | Connect sheet: choose a profile and Locked or Switchable | `mcpproxy connect cursor --profile work-readonly --lock` |
| Connect without a credential (only while `require_mcp_auth` is off) | "Keyless" in the advanced options | `mcpproxy connect cursor --keyless` |
| Move a client to another profile, or lock it | The profile chip on the client's row, the lock switch; the macOS tray Clients submenu | `mcpproxy client set-profile cursor work-full`, `client lock`, `client unlock` |
| Move every client of one profile to another | Bulk move on the Clients page | `mcpproxy client set-profile --from-profile X --to-profile Y` |
| Add a client that has no config file (a script, a CI job) | "Other client": the credential and a header snippet are shown once | `mcpproxy client add ci-bot --profile work-readonly` |
| Replace a credential | Rotate on the row: a supported client previews the config change first; a custom client shows the new secret once and stays pending until you finalize | `mcpproxy client rotate cursor`, `client rotate ci-bot --finalize` |
| Revoke a credential | Forget (optionally also remove the config entry) | `mcpproxy client forget cursor --disconnect` |

A client whose config still holds the instance admin API key (or no credential, or a revoked or expired one) is reported on the Clients page with a warning and by `mcpproxy doctor`. Nothing is rewritten automatically. **Upgrade clients holding the admin key** (Clients page, or `mcpproxy client upgrade-admin-key-holders`) previews, then replaces the admin key in every such client's config with a per-client credential; afterwards rotate the admin API key, which the action offers as its last step. A client without an active client credential cannot be bound to a profile until it is connected with one.

The credential is shown masked everywhere except the single moment it is created for a custom client. It is valid on MCP endpoints only; a client can never use it to read activity, config or other clients over REST.

**Rolling back.** Before you downgrade to a pre-profiles-v3 binary, set `require_mcp_auth: true`. A binary that predates client credentials rejects them (`401` on REST, and on MCP while authentication is required), but with `require_mcp_auth` off it treats an unrecognised credential like an omitted one and gives it unconfined access. See [Profiles, upgrading and downgrading](./profiles.md#upgrading-and-downgrading).

## What the preview discloses (Spec 091)

A preview shows the entry that *will be written*, which mcpproxy constructs and
can therefore mask. It historically said nothing about the entry being
**replaced**, which is user-authored content mcpproxy cannot safely echo. Two
preview fields close that gap without leaking config contents.

### `existing_entry_summary`

Present only when `entry_exists` is true. A fixed, whitelist-built projection of
the entry the write would replace:

| Field | Meaning |
|-------|---------|
| `entry_name` | The key the entry actually lives under. May differ from the requested `server_name` when the write **adopts** an endpoint-equivalent entry stored under a non-canonical name. |
| `type` | Transport type (`http`, `stdio`, …). |
| `endpoint` | Endpoint URL with **query string, userinfo (`user:pass@`) and fragment stripped**. |
| `command` | Command path for stdio/bridge entries. |
| `header_names` | Header **names** only — never values. Includes names parsed out of `--header` / `-H` bridge arguments. |
| `env_names` | Environment variable **names** only — never values. |

Secrecy holds *by construction*, not by masking heuristics: no other field of the
existing entry is copied, header/env values are never read, and a URL-shaped
field is emitted only after being reparsed into `scheme://host/path`. Backend
tests feed entries containing rotated API keys, bearer headers, env secrets,
`?apikey=` URLs and `user:pass@` URLs and assert none of those values appear
anywhere in the serialized preview response.

The summary is **display-only** and is never used for drift detection — that is
the precondition token's job, which hashes the raw entry.

### `connect_refusal`

A string, present only when a subsequent connect would refuse **regardless of
user intent**, carrying the same verbatim reason the write would return. The
preview runs the write's own guard rather than a copy, so the two can't drift.

Today the single case is a client that mcpproxy will not create a config for
from scratch: **OpenCode** owns a config schema mcpproxy will not invent, so with
no `opencode.jsonc` / `opencode.json` present the connect refuses instead of
creating one. Consumers must treat a non-empty `connect_refusal` as
"Connect unavailable" and surface the reason — the macOS form hides its Connect
button entirely in that state.

## Precondition token and the discriminated conflict (Spec 091)

`precondition_token` is an opaque string **always present** in a preview
response. Passing it back on the write binds the write to the exact state the
preview described:

```jsonc
// POST /api/v1/connect/{client}
{ "server_name": "mcpproxy", "force": true, "precondition_token": "…" }
```

The token is an HMAC-SHA256 over a canonical **length-prefixed** encoding of:

- the config path,
- whether the file exists (create vs. update),
- the **resolved** target entry name — after the same equivalent-entry adoption
  the write performs — and its **raw** value (so a credential rotation *inside*
  the existing entry drifts the token even though the sanitized summary hides
  it), and
- the exact pending entry mcpproxy would write right now (so proxy-side drift —
  API-key rotation, `require_mcp_auth` toggle, listen-address change —
  invalidates the preview too, and a credential can never be embedded without
  the credential notice having been shown).

It is keyed with a **per-core-instance random in-memory key**: tokens are
single-session by design, never persisted, and not usable as an offline
confirmation oracle for masked values.

### Behavior

- **Token matches** → the write proceeds normally.
- **Token stale** → `409 Conflict` with `"action": "precondition_failed"`, and
  **nothing is written** — the check runs before any backup or write, so a
  refusal is completely inert. The caller should re-preview, not retry.
- **`force=true` with a stale token** → still `precondition_failed`, still no
  write. The token, not the absence of `force`, is the overwrite safety.
- **Token absent** → exactly the previous behavior. Existing Web UI and CLI
  callers are unaffected.

The `409` body is machine-discriminable at the top level via `action`:

| `action` | Meaning | Caller should |
|----------|---------|---------------|
| `precondition_failed` | The preview is stale (file, existing entry, or pending entry changed). | Re-fetch the preview and show the user the new change. |
| `already_exists` | Pre-existing semantics: an entry with that name is present and `force` was not set. | Ask the user, then retry with `force=true` (plus a fresh token). |

### Consumers

- The **macOS Connect Client form** always sends the token from its rendered
  preview; replace flows send `force=true` **with** the token, never `force`
  alone. A `precondition_failed` triggers exactly one automatic re-preview.
- The **Web UI** Clients hub uses the shared Connect client list and preview
  flow; the setup wizard embeds the same flow inline so its native dialog does
  not cover the wizard's remaining steps.
- Write gating is unchanged: connect/disconnect routes still reject restricted
  agent tokens, and the tray's Unix-socket transport carries admin context.

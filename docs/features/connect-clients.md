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

The UI flows are **preview → confirm → write** (the CLI writes directly, with
`--force` to overwrite an existing entry):

1. `GET /api/v1/connect/{client}/preview` renders the exact change — target
   config path, format, server key, and the entry that would be written (any
   embedded API key masked).
2. `POST /api/v1/connect/{client}` performs the write, taking a timestamped
   backup of an existing file first.
3. `POST /api/v1/connect/{client}/undo` reverts that write byte-for-byte (or
   removes the file the connect created).

Endpoint-level reference — request/response shapes, backup naming, undo
semantics, and the macOS App Data privacy prompt — lives in the
[REST API reference](../api/rest-api.md#connect-client-wizard).

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
- The **Web UI** ConnectModal is unchanged and may adopt the token later.
- Write gating is unchanged: connect/disconnect routes still reject restricted
  agent tokens, and the tray's Unix-socket transport carries admin context.

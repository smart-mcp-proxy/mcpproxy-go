---
title: "Credential lifecycle over MCP"
sidebar_label: "Credential lifecycle (MCP)"
description: "Issue, inspect and revoke worker credentials (locked client credentials and profile-pinned agent tokens) from an administrator agent over MCP with the credentials tool."
---

# Credential lifecycle over MCP

An administrator agent can delegate a task end to end over MCP:

`task brief → profile → review effective access → issue a locked client or pinned token → worker runs → inspect activity → revoke`

The [`profiles`](./profiles.md) tool covers the profile steps. The **`credentials`** tool covers the identity steps: it issues a credential bound to a profile, lists and shows credentials without their secrets, and revokes them. Nothing here needs the CLI, a REST call or a hand edit.

`credentials` is an administrator tool. It uses exactly the same visibility rule as `profiles`: it is listed and callable only for a session authenticated with the instance API key or the tray socket, whose effective profile is none or sets `management_tools: true`. Agent tokens, client credentials and anonymous callers never see it. A forged `tools/call credentials` over MCP is rejected by the transport with a JSON-RPC "tool not found" error, the same answer as for a tool that does not exist, and changes nothing. There is no tool result to read, so a client should treat that protocol error as the refusal. (Only a caller that reaches the handler without the MCP tool filter, such as a direct handler call in tests, gets the defensive tool result `unknown tool: credentials`.)

## Walkthrough

```jsonc
// 1. A profile for the task (profiles tool)
{"name":"profiles","arguments":{"operation":"create","name":"daily-research","servers":["library"],
  "max_tier":"read","unannotated":"deny","tools":{"deny":["library:read_private*"]},
  "code_execution":false,"management_tools":false}}

// 2. Review what it admits
{"name":"profiles","arguments":{"operation":"effective_tools","name":"daily-research"}}

// 3. Issue a locked worker client. The secret is in this result only.
{"name":"credentials","arguments":{"operation":"create_client","client":"delegated-worker",
  "profile":"daily-research","expires_in":"1h",
  "purpose":"Summarise today's library additions; assumes no writes needed"}}

// 4. Give the credential to the worker, which sends it as X-API-Key
//    (or Authorization: Bearer) on /mcp.

// 5. Inspect, then revoke when the task ends
{"name":"credentials","arguments":{"operation":"get","client":"delegated-worker"}}
{"name":"credentials","arguments":{"operation":"revoke","client":"delegated-worker"}}
```

Revocation takes effect on the worker's **next request**, including on an MCP session it opened before: the request is answered `401 Agent token invalid: token has been revoked`. A credential whose expiry passes stops the same way (`token has expired`) with no action from anyone.

Reassigning a worker is the existing `profiles assign client=<id> profile=<other>`; the worker's next request on the same session uses the new grant.

## Operations

| Operation | Arguments | Result |
|---|---|---|
| `list` | `kind?` (`client`, `token`, `all`), `profile?`, `state?` (`active`, `expired`, `revoked`, `all`) | `{credentials: [...], total}` |
| `get` | exactly one of `client` or `token` | `{credential, links}` |
| `create_client` | `client`, `profile`, `expires_in`, `mode?` (`locked` default, `switchable`), `display_name?`, `purpose?` | one-time delivery (below) |
| `create_token` | `name`, `profile`, `expires_in`, `purpose?` | one-time delivery (below) |
| `revoke` | exactly one of `client` or `token` | `{credential, changed, client_config_untouched?}` |

Every credential the tool issues:

- names an **existing profile**. `profile` is required, and `""` (All servers) is refused with `profile_required`, so nothing silently defaults to full access. A client is **locked** unless you ask for `switchable`. A token is **pinned**: its scope is the profile only (`allowed_servers: ["*"]`, all three permissions, narrowed by the profile).
- has an **explicit expiry**. `expires_in` is required (`30m`, `4h`, `7d`; at most `365d`). A lifetime of at most 24 h is a **lease**: the Web UI shows "Lease ends in 45 min" and, afterwards, "Lease ended", with no "expiring soon" warning.
- has a **new identity**. An id or name that already has a record, active, expired or revoked, answers `identity_exists` with its `state`. Revoked identities are never revived, so two credentials' histories never merge. Connect-registry client ids (`cursor`, `claude-code`, …) and token names starting with `client-` are `reserved_identity`: supported clients are provisioned through [connect](./connect-clients.md).

`purpose` is optional free text (at most 500 characters) that the user reads beside the credential, labelled **"Stated purpose — not enforced"**. It is never enforced; the enforced restrictions are the profile's.

`list` and `get` return safe metadata only: kind, id, display name, profile, binding (`locked`, `switchable`, `pinned`), `state`, `profile_state` (`ok`, or `dangling` when the profile was deleted, which means deny-all), created/expires/revoked/last-used times, `lease`, the issuer, the purpose and the 12-character `token_prefix`. They never return the secret or its hash.

## The one-time delivery

```jsonc
{
  "client": { "kind": "client", "id": "delegated-worker", "binding": "locked", "profile": "daily-research",
              "state": "active", "lease": true, "issuer": { "actor_kind": "api_key", "surface": "mcp" }, "...": "..." },
  "credential": "mcp_cli_…",
  "snippet": { "generic_http": "{\"mcpServers\":{\"mcpproxy\":{\"url\":\"http://127.0.0.1:8080/mcp\",\"headers\":{\"X-API-Key\":\"mcp_cli_…\"}}}}",
               "header_name": "X-API-Key" },
  "delivery": { "shown_once": true, "endpoint": "http://127.0.0.1:8080/mcp", "header_name": "X-API-Key",
                "alternate_header": "Authorization: Bearer <credential>", "install_note": "…" },
  "links": { "identity": "http://127.0.0.1:8080/ui/clients?client=delegated-worker",
             "profile": "http://127.0.0.1:8080/ui/profiles/daily-research",
             "effective_tools": "http://127.0.0.1:8080/ui/profiles/daily-research?tab=tools&reason=callable",
             "activity": "http://127.0.0.1:8080/ui/activity?client=delegated-worker", "ui_path": { "...": "..." } }
}
```

`create_token` returns the same shape with `token` as the first key, and `links.identity` pointing at `/clients?tab=tokens&token=<name>`.

**Provisioning is not installation.** The tool never writes any client application's config and does not expose connect. It returns the endpoint and a header snippet; giving the credential to the worker and adding it to that worker's MCP client config is the caller's job. The secret cannot be shown again: if the result is lost, revoke the credential and issue a new one.

## Errors

A refusal is a tool result with `isError: true` whose text is a JSON object `{code, error, field?, ...}`. The codes:

| Code | When |
|---|---|
| `secret_in_argument` | An argument key or value (at any depth) looks like a credential, the instance API key, or a secret the sensitive-data detector recognises. Nothing is stored; the recorded arguments are replaced by a summary. |
| `arguments_too_large` | The arguments exceed 16 KiB of JSON. Nothing of them is stored. |
| `unknown_operation`, `missing_argument`, `invalid_argument` | The operation or an argument is wrong. An unknown argument name is reported as `"(unknown argument)"`, never by name. |
| `profile_required`, `unknown_profile` | `profile` is `""` or names no profile. |
| `invalid_expiry` | `expires_in` does not parse, is not positive, or exceeds 365 days. |
| `identity_exists`, `reserved_identity`, `identity_not_found` | Identity rules above. |
| `token_limit_reached` | 100 stored tokens. |
| `read_only_mode`, `management_disabled` | `create_*` and `revoke` under `read_only_mode` or `disable_management` (`list` and `get` still work). The texts are those of `profiles`. |
| `unsupported_edition` | Client operations in the server edition (tokens work there). |
| `binding_bypassable_without_auth` | The binding guard below. |

Error texts never quote a secret, a free-text argument (`purpose`, `display_name`) or an unparsed value (`expires_in`, an unknown `operation`).

## The binding guard: why issuing can be refused

A worker confined to a profile must not escape it by dropping its credential. When `require_mcp_auth` is off and an anonymous caller would reach **more** than the requested profile, `create_client` and `create_token` are refused with `binding_bypassable_without_auth` and the fixes (turn on `require_mcp_auth`, or set a confining `anonymous_profile`). Turn authentication on first:

```json
{ "require_mcp_auth": true }
```

The guarantee lasts as long as the credential: a token issued over MCP stays a **standing binding**. While it is active, a config write through the API that would turn `require_mcp_auth` off or widen `anonymous_profile` is refused naming the token; a hand edit of the config file that does the same is applied, but every anonymous request is then denied until the token is revoked or expires. A credential whose profile was deleted (`profiles delete force=true`) is deny-all for its holder and stays guarded the same way.

Tokens created through REST or the CLI keep their existing behaviour and are not standing bindings.

## Audit, activity and live updates

- Every issue and revoke writes one `profile_change` activity record with `change: issue` or `change: revoke`, the actor (`actor_kind`, `actor_name`, `surface`), the `client_id` or `token_name`, the profile and a diff with `credential_kind`, `binding`, `expires_at`, `lease`, `token_prefix`, `purpose_set` and `via`. It never holds the secret, its hash or the purpose text. Filter them with `/api/v1/activity?type=profile_change&client=<id>` (or `token=`, `profile=`). REST and CLI token create/revoke and custom client add write the same records.
- The `credentials` call itself is recorded as an internal tool call. For a successful create its stored response is a server-built summary in which `credential` and `snippet` read `[REDACTED: one-time credential]`.
- Each issue, revoke and Clients-page Forget publishes the SSE invalidation `credentials.changed {kind, id, token_name, change, profile}` (administrator-only, no secret). The Web UI Clients, Tokens and Profiles views refresh on it without a reload.

The raw secret appears only in the create result. It is not in activity records or exports, SSE events, logs, MCP notifications, error texts or the config file; `config.db` stores only its HMAC hash.

## In the Web UI

Clients and Tokens rows for these credentials show the binding ("Locked to daily-research", "Pinned to daily-research"), the issuer ("via MCP · api_key"), the lease, the state ("Revoked 2026-10-10 12:41"), and the stated purpose. The profile editor's **Assigned to** section links each client to its filtered Clients view, and a profile whose read cap admits explicit write tools reads "Read-only + 1 write exception". Activity labels the records "Issued client credential", "Revoked token" and so on, with links to the identity and the profile.

## See also

- [Profiles](./profiles.md): the policy a credential is bound to, and the `profiles` tool.
- [Agent tokens](./agent-tokens.md): token formats, REST and CLI management.
- [Connect clients](./connect-clients.md): provisioning supported clients through connect.

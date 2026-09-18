---
id: audit-log
title: Audit Log
sidebar_label: Audit Log
sidebar_position: 7.5
description: Attributable, redacted JSONL audit lines for every tool-call authorization decision, tool call and login attempt.
keywords: [audit, audit-log, jsonl, compliance, siem, server edition]
---

# Audit Log

The audit log is an edition-neutral, append-only JSONL stream: one JSON object per
line, written **synchronously** at the funnel that made the decision — never through
the (droppable, 256-slot) activity event bus. It is a separate, narrower record than
the [Activity Log](activity-log.md): where the activity log is a rich, queryable
history read back through REST/CLI, the audit log exists only to answer "who called
what, and what happened" from an external file or stream, with a stable schema a
security reviewer signs off once.

There is **no** REST, SSE, MCP, CLI or Web UI surface that reads or lists audit
lines. The file or stdout stream is the only reader's surface — point a log shipper
at it.

## What gets audited

Three event kinds, one schema:

| Event | Written | Count |
|---|---|---|
| `authz` | Once per pre-dispatch authorization decision (`decision: allow` after the last gate passes and before the upstream call, or `decision: deny` at the refusing gate) | one per decision |
| `tool_call` | Once per completed dispatch whose `authz` line said `allow` — success, upstream error, a post-dispatch output-sanitisation/schema block, or a limiter shed | one per `authz allow` |
| `auth_event` | Once per terminal login attempt and once per logout (server edition only) | one per attempt |

Audited surfaces: `call_tool_read`/`call_tool_write`/`call_tool_destructive`,
direct-name dispatch, nested `code_execution` sub-calls, and the REST
`/tools/call` / `/code/exec` / replay paths when they reach a `(server, tool)`
pair. **Not audited in v1**: invocations of built-in tools as such —
`retrieve_tools`, `describe_tool`, `read_cache`, `set_profile`,
`upstream_servers`, `quarantine_security`, `list_registries`, `search_servers`,
`doctor`, and the `code_execution` wrapper call itself. Those have no canonical
`(server, tool)` pair and keep their existing [activity log](activity-log.md) rows
unchanged.

A limiter shed is **not** a second authorization decision: admission runs after
every gate, so the `authz allow` line already exists; the shed is recorded on the
`tool_call` line (`outcome: rejected`, `reason: limiter_queue_full|limiter_queue_timeout`).
A post-dispatch output-sanitisation or output-schema block is likewise a
`tool_call` line (`outcome: blocked`), never a second `authz` line — the call was
authorized.

### Count invariants

`#authz == #pre-dispatch decisions`; `#tool_call == #authz(decision=allow)`; no
line for a built-in invocation as such; no `authz`/`tool_call` line ever carries
`caller.kind: session_user` (tenant sessions cannot reach dispatch — see
[server-edition SSO hardening](/features/oauth-authentication)); no `authz` line
carries `outcome` or an `output_*` reason; `#auth_event(surface=login) ==` the
number of terminal login attempts. These hold even when the activity event bus is
saturated (2,000 dispatches with the bus at its 256-slot cap still produce 2,000
`authz` lines and the matching `tool_call` lines) — the audit sink never shares
the bus's drop-on-full behaviour.

## Configuration

See [Configuration File → `audit_log`](/configuration/config-file#audit_log-edition-neutral-jsonl-audit-record)
for the full key reference (`enabled`, `path`, `stdout`, `max_size_mb`,
`max_backups`, `max_age_days`, `compress`) and environment overrides.

Defaults differ by edition, the code does not:

- **Personal edition**: `enabled: false`. A single-operator proxy already keeps
  the activity log; a second copy of every call by default doubles the PII
  surface with no consumer.
- **Server edition, block absent**: `enabled: true, stdout: true, path: ""` —
  container-native, one startup line states it.
- **Server edition, `enabled: false` explicit**: nothing is written; a startup
  warning says attribution is off.

### Docker / stdout

`audit_log.stdout` writes raw JSON lines straight to `os.Stdout`, bypassing the
coloured console log encoder — the two streams never interleave malformed JSON.
In the distroless server image, `mcpproxy version`/`doctor` need `--entrypoint`
(the image has no shell); stdio upstreams remain unavailable there as today.

```bash
# Tail the audit stream out of a running container
docker logs -f my-mcpproxy | jq -c 'select(.schema_version == 1)'
```

### Native stdio transport exception

Under `mcpproxy serve` with no listener, standard output **is** the MCP
JSON-RPC transport, so the stdout sink can never be used there:

- Block **absent** → the server-edition default resolves to `{enabled: false}`
  with one `WARN` naming `audit_log.path` as the stdio-compatible sink (only the
  default is suppressed — an explicit value always wins).
- Explicit `enabled: true, stdout: true` with **no `path`** → a sink-construction
  failure: the process exits with code `4` and
  `audit_log.stdout cannot be used under the stdio transport (stdout carries JSON-RPC); set audit_log.path`.
- Explicit `path` (with or without `stdout: true` alongside it) → the file sink is
  used; a redundant `stdout: true` is dropped with the same `WARN`.

The HTTP transport is unaffected by any of this.

### Sink failure policy

An unwritable `audit_log.path` at boot fails startup with exit code `4` and an
actionable message (`audit_log.path %q cannot be opened for append: %v`). A
**runtime** write failure (disk full, permissions revoked, …) never fails the
call it would have recorded: it increments the `mcpproxy_audit_write_failures_total`
counter (when metrics are enabled), is surfaced as a `mcpproxy doctor` finding,
and is logged at most once per minute. There is no `audit_log.strict` mode.

### Crash window

The `authz` line is written **before** the upstream call, so a process killed
mid-call still leaves the record that a (possibly destructive) call was
authorized — but the matching `tool_call` line for that one in-flight call is
lost. This is the one documented gap in the "every decision has a line"
guarantee: it affects at most the calls in flight at the moment of a crash, never
completed or subsequent calls.

## Schema (`schema_version: 1`)

The wire format is a JSON Schema (draft 2020-12) checked in at
[`docs/schemas/audit-line-v1.schema.json`](https://github.com/smart-mcp-proxy/mcpproxy-go/blob/main/docs/schemas/audit-line-v1.schema.json) —
validate against it directly; the tables below are a human-readable summary of
the same contract. (Publishing it as a static, versioned URL under
`docs.mcpproxy.app` is tracked as a docs-site follow-up alongside the sidebar
entry for this page — see `verification.md`.)

### Common keys (every event)

| Key | Required | Notes |
|---|---|---|
| `schema_version` | ✔ | Always `1` |
| `ts` | ✔ | RFC 3339, UTC (`Z`), fixed nine fractional digits: `2006-01-02T15:04:05.000000000Z` — not `RFC3339Nano`, which trims trailing zeros |
| `event` | ✔ | `authz` \| `tool_call` \| `auth_event` |
| `request_id` | ✔ | Activity request id — the join key to `mcpproxy activity show <id>` / `activity list --request-id <id>` |
| `transport_request_id` | optional | REST only; equals `request_id` on REST-originated direct dispatch |
| `parent_id` | optional | Nested `code_execution` children only — the wrapper's activity request id (a correlation key; the wrapper writes no line of its own) |
| `session_id`, `work_session_id` | optional | Activity resolver values |
| `origin` | ✔ | `local` (TCP) \| `socket` (tray) \| `remote` (reserved, not yet emitted) |
| `source` | ✔ | Mount point: `mcp` (`/mcp*`), `api` (every REST-originated line), `internal` (proxy-originated) — **never** derived from the caller-asserted `X-MCPProxy-Client` header |
| `caller` | ✔ | `{kind, user_id, user_email, email_hash, role, provider, token_name, token_prefix, profile_pin}` — see [Caller identity](#caller-identity) |
| `client` | optional | `{name, version}` caller-asserted (MCP `clientInfo` / `X-MCPProxy-Client`) — untrusted; `{ip}` via `trusted_proxies` |
| `profile` | optional | |

### `authz` — one per pre-dispatch decision

| Key | Required | Notes |
|---|---|---|
| `surface` | ✔ | `call_tool_read`\|`call_tool_write`\|`call_tool_destructive`\|`direct`\|`code_execution`\|`rest` |
| `server`, `tool`, `operation` | ✔ | Canonical `(server, tool)` pair, recorded even on a non-disclosing refusal |
| `decision` | ✔ | `allow`\|`deny` |
| `reason` | ✔ | `none` iff `allow`; else one closed pre-dispatch gate — see [Reason vocabulary](#authz-reason-vocabulary) |
| `disclosed` | required when `deny` | `false` for a non-disclosing refusal (the caller's own response is unchanged) |
| `args_sha256`, `args_bytes` | ✔ | See [Argument hashing](#argument-hashing-and-redaction) |
| `parent_id` | optional | Nested children only |
| Forbidden | — | `outcome`, `error_class`, `duration_ms`, `request_bytes`, `response_bytes`, `flags` — those belong to `tool_call` lines; the `authz` line is written before the call exists |

#### `authz` reason vocabulary

`intent_invalid` · `intent_rejected` · `profile_scope` · `token_scope` ·
`token_permission` · `server_quarantined` · `tool_pending_approval` ·
`tool_changed_approval` · `tool_not_callable` · `other`

### `tool_call` — one per `authz allow`, at completion

| Key | Required | Notes |
|---|---|---|
| `surface`, `server`, `tool`, `operation`, `args_sha256`, `args_bytes` | ✔ | Same values as the paired `authz` line |
| `outcome` | ✔ | `success`\|`error`\|`blocked` (post-dispatch output sanitisation/schema)\|`rejected` (limiter shed) |
| `reason` | required iff `blocked`/`rejected`; forbidden on `success`/`error` | `output_sanitisation`\|`output_schema`\|`limiter_queue_full`\|`limiter_queue_timeout` |
| `error_class` | required iff `error`; forbidden otherwise | `upstream_error`\|`upstream_timeout`\|`upstream_unavailable`\|`validation`\|`sanitisation`\|`internal`\|`cancelled` — a bounded class, never message text |
| `duration_ms` | ✔ | |
| `request_bytes`, `response_bytes` | optional | As measured by the completion path (pre-truncation) |
| `parent_id` | optional | Nested children |
| Forbidden | — | `decision`, `disclosed`, `flags` |

### `auth_event` — one per terminal login attempt, one per logout (server edition)

| Key | Required | Notes |
|---|---|---|
| `surface` | ✔ | `login`\|`logout` |
| `reason` | ✔ | The singular terminal result — see [Reason vocabulary](#auth_event-reason-vocabulary) |
| `flags` | optional | Closed array of non-terminal facts of the same attempt: `provider_rebound`\|`redirect_rejected`\|`groups_claim_missing`; omitted when empty |
| `caller.user_id` | when the store was reached and a record exists (`ok`, `logout`, `subject_mismatch`, `user_disabled`, `internal_error`) | forbidden on `provider_error` |
| `caller.email_hash` | only when a verified email is known and the store was **not** yet consulted (`domain_not_allowed`, `userinfo_subject_mismatch`, `provider_error` raised by the userinfo fetch after a verified ID token) | forbidden beside `user_id`/`user_email` — SHA-256 of the normalised email |
| `caller.kind` | ✔ | `session_user`\|`session_admin`\|`anonymous` (refused before identity) |
| `client.ip` | optional | Via `trusted_proxies` |
| Forbidden | — | `server`, `tool`, `operation`, `decision`, `disclosed`, `outcome`, `error_class`, `duration_ms`, `request_bytes`, `response_bytes`, `args_sha256`, `args_bytes`, `parent_id`, `profile` |

Identity is **stage-dependent, not reason-dependent**: pre-identity refusals
(`state_invalid`, `authorization_denied`, `discovery_failed`, `provider_error`
from discovery/JWKS/token-exchange, `id_token_invalid`, `nonce_mismatch`,
`audience_mismatch`, `issuer_mismatch`, `token_expired`, `email_missing`,
`email_unverified`) carry neither `user_id` nor `email_hash` — an unverified
claim is never hashed. `provider_error` is the one reason that occurs at two
stages: before the user store is consulted, or raised by the userinfo fetch
*after* a verified ID token, in which case it carries `email_hash` of the
verified email and never `user_id`. An abandoned redirect (pending state that
never returns) writes no line.

#### `auth_event` reason vocabulary

`ok` · `logout` · `authorization_denied` · `id_token_invalid` · `nonce_mismatch`
· `audience_mismatch` · `issuer_mismatch` · `token_expired` · `email_missing` ·
`email_unverified` · `domain_not_allowed` · `subject_mismatch` ·
`userinfo_subject_mismatch` · `user_disabled` · `state_invalid` ·
`provider_error` · `discovery_failed` · `internal_error`

## Caller identity

`caller.kind` is derived from `auth.AuthContextFromContext(ctx)` +
`transport.GetConnectionSource(ctx)` — never from arguments, a caller-supplied
header, or `_auth_*` request metadata, so it cannot be forged by a caller and
cannot be dropped under load.

| `caller.kind` | Meaning | Carries | Never carries |
|---|---|---|---|
| `api_key` | `X-API-Key` / `?apikey=` administrator | — | `user_id`, `user_email`, `email_hash`, `role`, `provider`, `token_name`, `token_prefix` |
| `socket` | Tray, over the Unix socket / named pipe | — | same as `api_key` |
| `stdio` | Native `stdio` transport (administrator context, no listener) | — | same as `api_key` |
| `anonymous` | `require_mcp_auth: false`, no credential presented | — (except `email_hash` on the three `auth_event` reasons above) | `user_id`, `user_email`, `role`, `provider`, `token_name`, `token_prefix` |
| `agent_token` | `mcp_agt_…` | `token_name`, `token_prefix`; when owned, all of `user_id`/`user_email`/`role`/`provider` together | `email_hash`; an ownerless token also carries none of `user_id`/`user_email`/`role`/`provider` |
| `session_user` | Tenant browser/API session | `user_id`, `role: user` — **`auth_event` only**, unreachable for dispatch (FR-002/FR-003) | `email_hash`, `token_name`, `token_prefix` |
| `session_admin` | Administrator browser/API session | `user_id`, `role: admin` | `email_hash`, `token_name`, `token_prefix` |
| `internal` | Proxy-originated | — | same as `api_key` |

## Argument hashing and redaction

`args_sha256` is SHA-256 over the RFC 8785 (JSON Canonicalization Scheme)
serialisation of `security.StripInternalArgs(args)` — members sorted by UTF-16
code unit, ES6 number serialisation (`1`, `1.0` and `1e0` hash alike, `-0`
serialises as `0`), minimal escaping, no whitespace, UTF-8 — computed **before**
any masking or truncation, so it is stable. The implementation is stdlib-only.
`args_bytes` is the length of that canonical serialisation.

A line never contains: an argument value, a response fragment, a raw token,
cookie, JWT, API key, `Authorization` header, prose reason text, error message
text, or any `_auth_*` key. This is a **structural** guarantee — the line is
built only from the closed key set above, and no key is ever populated from an
argument, a response, or an error message — not a best-effort filter.

The one documented exception: on a **refused** dispatch, `server` and `tool` are
caller-supplied strings (the caller may name any `server:tool` pair), so — like
`client.name`/`client.version`, `token_name` and `profile` — they are sanitised
per field at build time against the fixed-prefix credential patterns (`AKIA…`,
`ghp_…`, `Bearer …`, …) plus a length cap, never the generic high-entropy rule
(which would also mask every legitimate `args_sha256`/`email_hash`). A
credential-shaped name is recorded masked; a configured name that is not
credential-shaped is always recorded verbatim. Every serialised line
additionally passes through the same fixed-prefix sanitizer as defence in
depth — on a well-formed line this pass is the identity; a hit there means a
builder bug, is counted (`Sink.SanitizerHits()`, mirrored to
`mcpproxy_audit_sanitizer_hits_total`, surfaced by `mcpproxy doctor`) and
logged at most once per minute, and the (masked) line is still written.

Hidden-server names are **recorded, not echoed**: a non-disclosing refusal
writes the real `server`/`tool` to the line with `disclosed: false`, but the
caller's own response is unchanged — the audit sink is for the operator, never
an oracle the caller can query.

## Versioning

`schema_version` is bumped only for a **removed or renamed key, or a narrowed
vocabulary**. A bump advances three things together: the `schema_version`
constant, the schema's `$id`, and the published filename
(`docs/schemas/audit-line-v<N>.schema.json`) — the previous version's file stays
published for old consumers.

**Adding a key or an enum value is a minor change**: it ships as an updated copy
of the *same* schema file (`$id` unchanged, `docs/schemas/audit-line-v1.schema.json`)
with a change-log entry on this page. The published schema is
consumer-tolerant (`additionalProperties: true`), so a strict consumer holding
the v1 document keeps validating after a minor additive change under the same
`$id`.

### Change log

| Version | Change |
|---|---|
| 1 | Initial schema (Spec 107 PR-D) |

## Log-shipper example

The audit sink has no built-in connector — point any file/stdout log shipper at
it and filter on `schema_version`. A minimal, vendor-neutral tail-and-forward
example (works with Filebeat, Promtail, Vector, Fluent Bit, or a shell pipe
alike — substitute your shipper's own input stage):

```bash
# File sink: forward every well-formed audit line as it's appended
tail -F -n0 /var/log/mcpproxy/audit.jsonl \
  | jq -c --unbuffered 'select(.schema_version == 1)' \
  | your-log-forwarder --input -

# stdout sink (e.g. inside Docker/Kubernetes): the container runtime already
# captures stdout — point your log collector's normal container-log input at
# it, or pipe a local run the same way:
docker logs -f my-mcpproxy \
  | jq -c 'select(.schema_version == 1)' \
  | your-log-forwarder --input -
```

Because the schema is closed and versioned, a SIEM/log pipeline can index on
`event`, `caller.kind`, `decision`/`outcome`, `reason` and `server`/`tool`
without a proxy-side connector or a vendor-specific export format. The
[Sensitive Data Detection SIEM recipe](sensitive-data-detection.md#integration-with-siem)
exports the richer, queryable activity log for the same kind of downstream
ingestion — the audit log is the narrower, schema-pinned line for attribution.

## Related

- [Configuration File → `audit_log`](/configuration/config-file#audit_log-edition-neutral-jsonl-audit-record) — config keys, defaults and validation
- [Activity Log](activity-log.md) — the queryable REST/CLI history the audit log intentionally does not duplicate
- [Sensitive Data Detection](sensitive-data-detection.md) — the separate, asynchronous detector; its verdicts are never carried on an audit line (joined only by `request_id`)
- [OAuth Authentication](oauth-authentication.md) — the login flow `auth_event` lines record

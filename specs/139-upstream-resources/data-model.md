# Data Model: Upstream Resources

No new persistent buckets. In-memory state lives in `MCPProxyServer`; one new activity type is stored in the existing activity bucket.

## Config

| Field | Where | Type | Default | Notes |
|-------|-------|------|---------|-------|
| `aggregate_upstream_resources` | `Config` | bool | `false` | Hot-reloadable. |
| `expose_resources` | `ServerConfig` | `*bool` | absent | Absent = include when the server announces `resources`; `false` = exclude. Patch semantics as `expose_prompts`. |

## ServerResource (upstream manager output)

| Field | Type | Notes |
|-------|------|-------|
| `Server` | string | Canonical server name. |
| `Resource` | `mcp.Resource` | As the upstream sent it (URI original). |

`ServerResourceTemplate` is the same with `mcp.ResourceTemplate`.

## Aggregated snapshot (server side, one generation)

| Field | Type | Notes |
|-------|------|-------|
| `owners` | `map[proxiedURI]server` | Registered resources. |
| `templateOwners` | `map[proxiedPattern]server` | Registered templates. |
| `withheld` | `map[server]set[originalURI]` | Dropped by the metadata scan; a template read of one of these is refused. |
| `fingerprint` | string | PR-2: hash of the sorted proxied set, to notify only on change. |

Published under one mutex before the SDK `SetResources` / `SetResourceTemplates` calls, so filter and handlers read one generation.

## Activity row `resource_read`

| Field | Source |
|-------|--------|
| `type` | `resource_read` |
| `server_name` | owning server |
| `tool_name` | empty (not a tool) |
| `status` | `success` / `error` / `blocked` |
| `error_message` | on error |
| `duration_ms` | upstream round trip + policy |
| `request_id` | correlation id shared with a companion `policy_decision` |
| `session_id`, attribution | as tool calls (caller, profile, work session) |
| `metadata.uri` | original upstream URI |
| `metadata.proxied_uri` | address the client used |
| `metadata.content_bytes` | sum of text bytes + blob encoded bytes |
| `metadata.content_count` | number of content items |
| `metadata.mime_types` | distinct MIME types |
| `response` | result body under the tool-call truncation and detection rules |

Validation: exactly one row per read that reaches a registered handler; not counted by `AttemptRoleOf`.

## Upstream subscription (PR-3)

| Field | Type | Notes |
|-------|------|-------|
| key | `(server, originalURI)` | One upstream subscription. |
| sessions | set of session ids → proxied address | Refcount; release on last unsubscribe or session end. |
| state | `pending` / `active` / `failed` | Re-established on upstream reconnect. |

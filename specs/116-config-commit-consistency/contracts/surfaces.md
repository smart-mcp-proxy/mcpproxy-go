# Contract: Surface Behaviour Deltas (REST, MCP, CLI, SSE)

**Spec**: [../spec.md](../spec.md)

This feature changes **when** the surfaces' effects become true. They become
true before the call returns rather than eventually. It adds one optional field
and one error code. Everything else stays byte-compatible.

## REST

| Endpoint | Before | After | PR |
|---|---|---|---|
| `POST /api/v1/servers` | 201. 409 "already exists". Storage and runtime published; disk saved best-effort | Same codes. Live, storage and disk are committed together before the 201 | PR1 |
| `PATCH /api/v1/servers/{id}` | resolved against a snapshot read before the write; `max_concurrent_requests`, `queue_size`, `queue_timeout` were accepted but not applied | resolved inside the commit against the live entry. 404 if the server was removed concurrently. The concurrency overrides are now applied, and validated inside the commit against the live global defaults: a value the next start would refuse (negative, or a positive queue on a disabled limit) is **400** with the live config, storage and file unchanged (review R2.1) | PR1 |
| `Server.UpdateServer` (controller method, no route of its own (there is no PUT on a server); `PATCH /api/v1/servers/{id}` calls it only on a controller without `PatchServer`) | storage first, then in-place snapshot mutation | one commit | PR1 |
| `POST .../enable`, `.../disable`, `.../quarantine`, `.../unquarantine` | storage flag, then save, then async reconcile | one commit. Quarantine has purged the index by the time the call returns | PR1 |
| `PATCH /api/v1/config`, `POST /api/v1/config/apply` | publishes, then async reconcile | publishes the reconciled state. `ConfigApplyResult` unchanged | PR1 |
| `DELETE /api/v1/servers/{id}` | partial purge, then save | one commit with the full purge set. A follow-up `POST` of the same name succeeds immediately | PR2 |
| `GET /api/v1/servers[/{id}]` | — | adds `"generation"` and `"incarnation"` (int64) | PR2 |
| Tool and prompt approve, block and unblock endpoints, security approve | — | accept optional `"expected_incarnation"`. Mismatch → **409** `{"success":false,"error":"server_replaced: server '<name>' was removed or replaced since it was reviewed"}` | PR2 |

`oas/swagger.yaml` and `docs/api/rest-api.md` are updated in PR2.

## MCP (`upstream_servers`, `quarantine_security`)

| Operation | After | PR |
|---|---|---|
| `add` | unchanged result shape. The commit is complete when the call returns | PR1 |
| `update`, `patch` | merge runs inside the commit. On a removed server: `isError` "Server 'x' not found" | PR1 |
| `remove` | full purge (PR2). The result shape `{id,name,removed}` is unchanged | PR1/PR2 |
| `quarantine_security quarantine_server` | routed through the commit. Tools are purged from the index before the call returns (B1) | PR1 |
| approval-related operations | `isError` "server_replaced: …" on an incarnation mismatch | PR2 |

The tool schemas are unchanged, so the frozen tool-surface goldens are
untouched.

## CLI

`mcpproxy upstream add|remove|patch|enable|disable|quarantine` maps REST 409
`server_replaced` to exit code 1 with the message shown verbatim. There are no
new flags.

## SSE events

`servers.changed`, `review.changed`, `config.saved` and `config.reloaded` are
sent after the commit lock is released, at most once per commit and reason.
The commit sends them itself; a caller adds its own payload fields to that
one event (`CommitTx.EventExtra`) instead of sending a second copy, so
`enable_toggle`, `quarantine_toggle` and `bulk_enable_toggle` keep their fields
(`server`, `enabled`, `quarantined`, `count`) and gain the commit's `servers`
and `generation` (additive, review R2.5). The coalescer may replace a pending
`servers.changed` marker with a later one, so a client must not rely on
`generation` arriving in SSE. There are no new event types.

## Activity log

Records keep their current kinds (`server_removed`, `quarantine_change`,
`config_change`). Removal records add a `purged` summary
(`{bucket: count}`) in `metadata`. This is additive.

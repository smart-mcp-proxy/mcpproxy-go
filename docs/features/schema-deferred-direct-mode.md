---
id: schema-deferred-direct-mode
title: Schema-Deferred Direct Mode
sidebar_label: Schema-Deferred Direct
sidebar_position: 14
description: Enumerate every upstream tool on the direct surface without shipping its inputSchema — compact signatures now, full schemas on demand
keywords: [direct mode, tools/list, schema, describe_tool, tokens, signatures, deferred]
---

# Schema-Deferred Direct Mode

Direct mode (`/mcp/all`) lists every tool of every connected upstream server. That
is its whole point — nothing is hidden behind a search step — and also its whole
cost: each entry ships its complete `inputSchema` and `outputSchema`.

How much that costs depends heavily on the fleet. On the two reference corpora
this project measures against, the upstream input schema is **43%** of a
45-tool listing (6116 tokens total) and a similar share of a 527-tool one
(99918 tokens, ~190 tokens per tool). A schema-heavy fleet will be higher; a
fleet of small, flat tools will be lower.

Schema-deferred serialization keeps the full listing and drops the schemas. Every
tool still appears, under the same `serverName__toolName` name, with the same
description and the same annotations, but the advertised input schema shrinks to
`{"type":"object"}` and a one-line compact signature is appended to the
description. Agents call flat tools straight from the signature; for anything the
signature had to collapse, they call `describe_tool` to get the real schema.

This is a **serialization mode, not a routing mode**. `routing_mode` still takes
exactly `retrieve_tools`, `direct`, and `code_execution`; deferral is an
independent axis that changes how the direct surface renders the tools it was
already going to list.

## When to turn it on

Turn it on when the `tools/list` payload itself is the problem:

- **Large fleets.** Past roughly 50 tools, the full direct listing starts eating a
  meaningful share of the context window before the agent has done anything.

  **Measured savings, so you can judge whether it is worth it:** **29.7%** on the
  frozen 45-tool reference corpus and **34.8%** on a 527-tool snapshot. Deferral
  removes the input schema and adds a signature, so the ceiling is set by how
  much of *your* payload the schemas actually are — descriptions, names and the
  annotations block are untouched and, on these corpora, dominate what is left.
  Do not plan around an order-of-magnitude reduction; measure your own fleet with
  `go run ./bench/cmd/bench` if the number matters to you.
- **Agent clients, not human UIs.** The consumer is a model that reads text and
  guesses arguments. See [Client compatibility](#client-compatibility) — a
  schema-driven form UI is exactly the case that should stay on `full`.
- **You want full visibility, not search.** If you were considering
  `retrieve_tools` mode purely for the token savings but did not want to give up
  seeing the whole catalog, this is the middle option.

Leave it off (the default) for small setups, for clients that render forms from
the advertised schema, and anywhere the extra `describe_tool` round trip on lossy
tools is not worth the tokens saved.

## Enabling it

One key in `~/.mcpproxy/mcp_config.json`:

```json
{
  "routing_mode": "direct",
  "direct_tool_response_mode": "deferred"
}
```

| Field | Type | Default | Values |
|-------|------|---------|--------|
| `direct_tool_response_mode` | string | `"full"` | `full`, `deferred` |

Equivalents, in the usual precedence:

```bash
MCPPROXY_DIRECT_TOOL_RESPONSE_MODE=deferred mcpproxy serve
mcpproxy serve --direct-tool-response-mode=deferred
```

Or from the UI — **Settings → General → "Detail in Direct-mode listings"**, in
both the Web UI and the macOS tray. The routing-mode field ("How agents find
tools") heads the same section, two rows above. Note the asymmetry: changing the
serialization mode applies hot, but changing `routing_mode` needs a restart (the
badge on that field says so), because `/mcp` binds its routing mode once at
startup.

**This is a separate axis from `tool_response_mode`.** That key (and its
`--tool-response-mode` flag and `MCPPROXY_TOOL_RESPONSE_MODE` alias) still governs
`retrieve_tools` serialization only, and its meaning is unchanged. The two
combine freely: `tool_response_mode: "compact"` plus
`direct_tool_response_mode: "deferred"` is a legal configuration where each key
governs its own surface.

`routing_mode: "schema_deferred"` is rejected at config validation with a message
naming the supported composition — it was proposed as a fourth routing mode and
deliberately built as this key instead.

### Which endpoints it governs

| Endpoint | Governed by `direct_tool_response_mode` |
|----------|------------------------------------------|
| `/mcp/all` | Always |
| `/mcp` | When `routing_mode: "direct"` |
| `/v1/tool_code`, `/v1/tool-code` (legacy aliases) | When `routing_mode: "direct"` |
| `/mcp/call`, `/mcp/code`, `/mcp/p/<slug>` | Never — these are not direct surfaces |

All four direct routes share one server instance, so there is no per-endpoint
divergence: they are always in the same serialization mode.

### Flipping it on a running proxy

The setting is hot-reloadable. Edit the config file on a running proxy and the
direct tool set is rebuilt in place — no restart — and connected direct-surface
sessions receive `notifications/tools/list_changed` so caching clients refetch.
The tool *set* is identical across the flip: same names, same count, same
built-ins. Only the rendering changes.

A config edit that does not change the effective serialization mode rebuilds
nothing and emits no notification, so unrelated edits do not churn every
connected client's cache.

## What a deferred entry looks like

```json
{
  "name": "github__create_issue",
  "description": "[github] Create a new issue in a repository.\ncreate_issue(owner*:str, repo*:str, title*:str, labels:[str], milestone~:obj)",
  "inputSchema": { "type": "object" },
  "annotations": { "... unchanged from full mode ..." }
}
```

- **Name and annotations** are byte-for-byte what full mode would emit.
- **Description** is the full-mode description (`[server] …`, untruncated),
  then a newline, then the bare tool name and its compact signature.
- **`inputSchema`** is exactly `{"type":"object"}` — never literal `{}` and never
  absent. Some strict clients read `{}` or a missing schema as "this tool takes no
  arguments" and prune everything the model passes; `{"type":"object"}` accepts
  any properties and keeps those clients working.
- **`outputSchema`** is absent. It is part of the token cost the mode exists to
  remove, and MCP permits structured content without a declared output schema.
  `describe_tool` returns it as an additive `output_schema` field when the tool
  declares one.

Only *upstream* entries are deferred. The `describe_tool` built-in keeps its real
parameter schema in both modes — an agent that could not read the schema of the
tool it needs to recover schemas would be stuck.

Signatures are compiled at index time and served from cache, never compiled per
request. If a tool's signature is not in the cache yet — most often a tool the
indexer has not reached, or one held by tool-level quarantine — the entry is
listed **without** the signature suffix rather than dropped or delayed. That is
correct behaviour, not a bug; the suffix appears once indexing settles.

## The signature grammar

The suffix is the tool's bare name followed by a parenthesised parameter list:

```
create_issue(owner*:str, repo*:str, title*:str, labels:[str], milestone~:obj)
fetch_page(origin*:str, account~:obj, format:enum[json|text], ttl:int=3600)
list_repos()
run_query(~)
```

| Marker | Meaning |
|--------|---------|
| `*` | Required. Required parameters are **never** elided, even when nothing else about them is known. |
| `~` | Collapsed / lossy — a nested object, a `$ref`, a long enum, or a type that could not be represented in one line. Detail was dropped. |
| `(~)` | The whole schema was unavailable or unparseable. Treat the tool as fully undocumented. |
| *(none)* | Optional, and fully described by what you see. |

Types abbreviate as `str`, `int`, `num`, `bool`, `obj`, `any`, arrays as `[str]`,
unions as `str|int`, short enums as `enum[a|b|c]`. Scalar defaults are inlined
(`ttl:int=3600`).

**Ordering is deterministic and is not declaration order.** Required parameters
come first, in the order the schema's `required` array lists them; optional ones
follow, sorted by name. The same schema therefore always produces the same
signature — but do not read the signature as telling you the order a schema
declares its properties in, because it does not.

**The rule for agents is one line long:**

> A signature with no `~` in it is directly callable. A signature containing `~`
> (including the bare `(~)`) means the listing is lossy — call `describe_tool`
> before calling the tool.

On a representative corpus, more than 80% of tools are flat, so most calls cost
zero extra round trips.

## `describe_tool` on the direct surface

`describe_tool` is registered on the direct surface in **both** serialization
modes, so flipping the mode never adds or removes a capability mid-session. It
survives every upstream refresh — it is composed into direct tool-set
construction rather than registered once beside it.

**Both id forms are accepted.** Pass the direct display name you saw in
`tools/list`, or the canonical form:

```json
{ "tool_ids": ["github__create_issue"] }
{ "tool_ids": ["github:create_issue"] }
```

Direct ids are resolved through the same registration mapping the dispatch
handler was built from, never by re-splitting the display name on its first `__`.
That matters for server or tool names that themselves contain `__`: Inspect and
Execute cannot disagree about which tool an id means.

**Definition mode** (the default) returns the full input schema, the untruncated
description, annotations, and `output_schema` when the tool declares one. Batches
are capped at 5 ids — an agent needing more makes more than one call.

**Check mode** (`"check": true`) returns availability verdicts instead of
definitions, batched up to 50 ids. Use it to gate a plan before its first call:
each id comes back `ready`, or `status: "unavailable"` with a reason such as
`tool_pending_approval`, `tool_changed`, `server_disabled`, or `not_found`. This
is the same preflight vocabulary the `retrieve_tools` surface uses — the direct
surface projects it, never translates it. See
[Required-Tools Preflight](tools-preflight.md).

**Visibility is parity with your own listing.** Every tool a session's direct
`tools/list` included is describable by that session — a listed tool is never
undescribable, including one that is pending tool-level approval. Every id that
session's listing omitted — out of an agent token's server scope, above its
permission tier, outside the active profile, on a quarantined server, or simply
nonexistent — comes back as a plain `not_found`. No reason code, remediation
string, or "did you mean" suggestion ever confirms the existence of a tool you
could not already see.

## The self-healing retry

Deferral means agents sometimes guess wrong. What this bounds is the cost of
*learning* the schema: the proxy hands it back on the first bad call, so the
agent never has to discover it by trial and error against the upstream.

It does **not** guarantee the next attempt succeeds. The proxy returns the
schema; whether the agent then builds valid arguments, and whether the upstream
call itself succeeds, are outside its control. What is guaranteed is narrower and
still worth having: a wrong guess never reaches the upstream, and the response
carries everything needed to correct it without a separate lookup.

Direct-mode calls are validated against the tool's **stored upstream schema**
before anything is dispatched. A call with missing or invalid arguments is
rejected by the proxy — it never reaches the upstream server — and the error body
carries everything needed to fix it:

```json
{
  "error": "invalid arguments for github__create_issue: missing property 'title'",
  "error_type": "invalid_params",
  "tool": "github__create_issue",
  "input_schema": { "type": "object", "properties": { "...": {} }, "required": ["title"] },
  "hint": "Fix arguments to match input_schema, then retry. For the full definition call describe_tool({tool_ids:[\"github__create_issue\"]})."
}
```

`input_schema` is the tool's complete stored schema, so the agent does not need a
`describe_tool` round trip to recover — it corrects and retries. Three properties
are worth knowing:

- **Mode-independent.** The same validation and the same error shape apply in
  `full` mode. Self-healing is not a deferral feature; deferral is what makes it
  load-bearing.
- **Fail-open.** A schema that cannot be compiled or uses unsupported constructs
  is skipped (and counted in the logs), and the call dispatches exactly as it
  would have without validation. Validation never blocks a call a schemaless
  proxy would have allowed.
- **Argument failures only.** Transport errors, auth failures, timeouts, and
  upstream crashes keep their existing shapes with no schema attached.

## Client compatibility

Deferral changes what clients see. Three consequences are worth reading before
enabling it.

### Schema-driven form UIs render an empty form

A client that builds an input form from the advertised `inputSchema` will render a
deferred tool as a form with **no fields**, because the advertised schema
declares no properties. This follows from the wire shape rather than from any
particular client's behaviour: `{"type":"object"}` declares no properties, so
there is nothing for a schema-driven UI to draw. The
information is not lost (it is one `describe_tool` call away), but the UI has no
way to know that.

This is an accepted, documented consequence of an opt-in mode, not a bug to work
around. **If your client renders forms from tool schemas, stay on `full`.** The
operator who chooses deferral is optimizing for agents that read text, not for
UIs that read schemas.

### A stale cached listing is safe in both directions

Clients cache `tools/list`. A mode flip can leave a client holding entries from
the other mode, and neither direction breaks:

- **Client holds full-mode entries, proxy is now deferred.** Calls still succeed.
  Validation always runs against the stored upstream schema, never against the
  advertised permissive placeholder, so arguments built from a cached real schema
  validate correctly.
- **Client holds deferred entries, proxy is now full.** Calls built from a
  signature still work; if the guess was wrong, the self-healing error returns the
  full schema and the retry succeeds. The worst case is describe-before-call.

The `notifications/tools/list_changed` emitted on the flip bounds the staleness
window for clients that honour it.

### The direct server's `initialize` response now carries `instructions`

This is a client-visible change and it applies **in both serialization modes**,
not only under deferral. Previously the direct server instance attached no
`instructions` to its `initialize` result; it now does.

The value is the operator's configured `instructions` when that key is set,
otherwise a direct-specific default naming what this surface actually offers
(`server__tool` calling and `describe_tool`), followed by a blank line and the
signature legend:

> Some tool descriptions end with a compact signature `(param*:type, ...)`: `*`
> marks a required parameter and `~` marks collapsed/lossy details. When a
> signature is present the listed inputSchema is a placeholder, not the real
> schema — flat signatures are directly callable, and for `~`-marked tools call
> 'describe_tool' with the listed tool name to get the full schema.

The legend is emitted unconditionally and phrased conditionally ("Some tool
descriptions…") so it stays true in `full` mode, where no entry carries a
signature. Emitting it only under deferral would make the `initialize` response
depend on a serialization setting and give clients two shapes to cache instead of
one.

Note that `instructions` is resolved when the server instance is constructed, so
editing the `instructions` config key needs a restart — unlike
`direct_tool_response_mode`, which is read live on every rebuild.

## What does not change

- **Which tools you can see.** Agent-token server scope, permission tiers, profile
  scope, server quarantine, and tool-level quarantine all run *before*
  serialization. Deferral changes how tools are rendered, never which ones are
  listed, describable, or callable.
- **Other surfaces.** `retrieve_tools` mode, the code-execution surface, prompts,
  and resources are untouched. With `direct_tool_response_mode` at its default
  `full`, direct-surface payloads are unchanged apart from the single added
  `describe_tool` entry.

## Related Documentation

- [Routing Modes](routing-modes.md) — what the direct surface is and when to choose it
- [Required-Tools Preflight](tools-preflight.md) — `describe_tool` check mode and the reason taxonomy
- [Search & Discovery](search-discovery.md) — the `retrieve_tools` surface and its own [`tool_response_mode` axis](search-discovery.md#tool-response-mode)
- [Agent Tokens](agent-tokens.md) — scoping which tools a session lists at all
- [Configuration Reference](../configuration/config-file.md)

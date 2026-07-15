# Contract: describe_tool

Built-in second-stage tool (FR-010/011/012). Registered in the **retrieve_tools routing mode
only** (v1): the default server (`registerTools`, mcp.go:689) and `buildCallToolModeTools`
(mcp_routing.go:354). **Not** registered in code_execution or direct mode.

## Tool definition (agent-facing, ≤ ~150 tokens — FR-011)

```
name: describe_tool
description: "Return full JSON Schema + long description for specific tools found via
  retrieve_tools. Use when a compact signature is marked lossy ('~') or you need the exact
  schema before calling."
params:
  tool_ids: [str]  (required)  # 1..5 ids in "<server>:<tool>" format, from retrieve_tools results
```

## Request

```json
{ "tool_ids": ["digitalocean:cdn_create", "cloudflare:zone_create"] }
```

Rules:
- `tool_ids` required, non-empty, 1–5 entries. Each `"<server>:<tool>"`.
- >5 ids ⇒ single error (no partial dump — anti-bulk-loophole, spec edge case):
  ```json
  { "error": "too many tool_ids: 7 (max 5). Narrow your selection." }
  ```
  (returned as an MCP tool error result; the batch is not processed.)
- 0 ids / missing param ⇒ `"Missing required parameter 'tool_ids'"` style error (matches
  existing `RequireString`/param-error convention).

## Response (success — mixed valid/invalid still succeeds)

```json
{
  "definitions": [
    {
      "name": "digitalocean:cdn_create",
      "description": "Create a CDN for a Spaces bucket. Full multi-paragraph text …",
      "inputSchema": { "type": "object", "properties": { … }, "required": ["origin"] },
      "server": "digitalocean",
      "annotations": { … },
      "call_with": "call_tool_write"
    }
  ],
  "errors": [
    { "id": "cloudflare:zone_create", "error": "not_found",
      "remediation": "Tool not found or no longer available; re-run retrieve_tools." }
  ]
}
```

Contract guarantees:
- **Definition-field equality (FR-010) — NOT whole-object byte-equality**: `describe_tool` is not
  a ranked search, so a definition **omits the ranked fields** (`score`, and any future
  ranking-only field). Equality is asserted over the **definition fields**: `name`,
  `description`, `inputSchema`, `server`, `annotations`, `call_with`. Those fields MUST be
  byte-equal to the corresponding fields of the full-mode `retrieve_tools` entry for the same
  tool. Implementation: `buildToolEntry(..., full)` produces the full entry, then describe_tool
  strips the ranking-only keys (`delete(entry, "score")`) — so the shared fields cannot drift,
  while `score` is neither `0` nor invented. (This corrects the earlier "score:0 byte-equal"
  claim, which was impossible because full entries carry `result.Score` at mcp.go:1455.)
  Spec FR-010 (as clarified) requires exactly this: field-equality over the definition
  fields; the ranked field is out of scope for a non-ranked lookup.
- **Batch resilience (FR-010)**: unknown/invisible ids become per-id `errors` entries; the call
  as a whole returns success with whatever definitions resolved.
- **Mode independence (FR-012)**: identical output whether `tool_response_mode` is `full` or
  `compact` — describe_tool ignores the mode.

## Visibility pipeline (FR-011 — strictly narrower than retrieve_tools)

For each id, resolve `(server, tool)` and call the resolver `p.toolVisibleToSession`
(research.md R10 / tasks.md T010). It is built from the SAME step helpers `retrieve_tools`
filters with (scope + `isToolCallable`), **plus** the describe-only gates 1/3/4 below.
`retrieve_tools`' own result filter applies only steps 2 and 5 — the merge-base FULL-mode
semantics, which FR-006 byte-identity freezes (search never gated server quarantine or
pending/changed approvals on indexed hits). Because describe_tool only ADDS gates, the
security invariant (never return what search would not) holds by construction; the extra
gates make describe stricter, never looser. Check order for describe_tool:
1. **Index presence** — the tool exists in the (profile-scoped) index. Absent ⇒ per-id error
   `not_found`.
2. **Profile scope (Spec 057) + agent-token server scope (Spec 028)** — out of scope ⇒ per-id
   error `invisible` (not distinguished from not_found in any way that leaks existence beyond what
   search reveals).
3. **Server-level quarantine** — quarantined server ⇒ per-id error (search hides these too).
4. **Tool-level approval (Spec 032)** — `pending`/`changed` ⇒ per-id error, not a definition.
5. **`isToolCallable(server, tool)`** (disabled/blocked) ⇒ per-id error.

Only when all five pass does the handler resolve the full definition via
`indexManager.GetToolsByServer(server)` (filtered to `tool`) and render it. Per-id error `error`
codes: `not_found`, `invisible`, `quarantined`, `pending_approval`, `changed`, `disabled`. Each
carries a `remediation` string reusing the existing `disabledToolRemediation` / quarantine
remediation text where applicable.

**Security invariant (SC + Constitution IV)**: `describe_tool` MUST NOT return a definition the
same session's `retrieve_tools` could not return. A test drives an agent-token session scoped to
server A and asserts `describe_tool(["B:anything"])` yields an error, never a definition.

## Test obligations

- Valid id ⇒ **definition-field** equality with the full-mode retrieve_tools entry over
  `{name, description, inputSchema, server, annotations, call_with}` (compare those keys after
  deleting `score` from a captured full entry); assert the definition carries **no** `score` key.
- Mixed valid + unknown ⇒ definitions for valid, per-id errors for unknown, overall success.
- 6 ids ⇒ limit error, no processing.
- Quarantined / disabled / out-of-profile / out-of-agent-scope id ⇒ per-id error, no leak
  (parity test against the **shared visibility resolver** — see plan.md/tasks.md — on the same
  session, so describe and retrieve provably use the same predicate).
- Same output in full and compact mode (FR-012).
- Registered in retrieve_tools mode servers; absent from code_execution and direct mode
  (tools/list assertion).
- **≤150-token budget (FR-011)**: count the `describe_tool` definition's tokens with the
  **pinned tokenizer** the bench uses — tiktoken `cl100k_base` (same encoder the spec-083
  profiler counts with, so the budget and the profiler agree) — and assert ≤150.

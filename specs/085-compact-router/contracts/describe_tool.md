# Contract: describe_tool

Built-in second-stage tool (FR-010/011/012). Registered in the **retrieve_tools routing mode
only** (v1): the default server (`registerTools`, mcp.go:689) and `buildCallToolModeTools`
(mcp_routing.go:354). **Not** registered in code_execution or direct mode.

> **Amended by spec 099 (`099-describe-check-mode`), 2026-08-16.** Three changes, marked
> `[099]` below:
> 1. an optional `check: true` mode returning availability verdicts instead of definitions,
>    with an optional `filters` object and a 50-id cap of its own;
> 2. the per-id error code `invisible` is **RETIRED** — out-of-scope ids report `not_found`.
>    This is a compatibility break for any consumer switching on the code; the remediation
>    string is unchanged, because it was already the shared not-found text;
> 3. the definition token budget is raised from ≤150 to **≤250** (measured: 243).
>
> Everything else on this page is unchanged, and plain-mode responses (no `check`) remain
> byte-identical to the pre-099 release apart from change (2).

## Tool definition (agent-facing, ≤250 tokens — FR-011 as amended by 099 FR-015)

```
name: describe_tool
description: "Return full JSON Schema + long description for specific tools found via
  retrieve_tools. Use when a compact signature is marked lossy ('~') or you need the exact
  schema before calling. With check:true it returns one availability verdict per id instead
  of schemas ('ready', or a reason code with retryable/action), to gate a plan before its
  first call."
params:
  tool_ids: [str]  (required)  # ids in "<server>:<tool>" format; max 5, or 50 with check:true
  check:    bool   [099]       # availability only, no schemas (default false)
  filters:  object [099]       # check:true only — {read_only_only, exclude_destructive,
                               #   exclude_open_world}, the spec-094 annotation filters
```

## Request

```json
{ "tool_ids": ["digitalocean:cdn_create", "cloudflare:zone_create"] }
```

Rules:
- `tool_ids` required, non-empty, 1–5 entries (50 under `check: true` — **[099]**). Each `"<server>:<tool>"`.
- >5 ids ⇒ single error (no partial dump — anti-bulk-loophole, spec edge case):
  ```json
  { "error": "too many tool_ids: 7 (max 5). Narrow your selection." }
  ```
  (returned as an MCP tool error result; the batch is not processed.)
- 0 ids / missing param ⇒ `"Missing required parameter 'tool_ids'"` style error (matches
  existing `RequireString`/param-error convention). Check mode names its own cap in that
  message ("1-50 tool ids"), never the plain-mode one — **[099]**.

### Check mode — **[099]**

`check: true` switches the response shape entirely; `check: false` or an absent `check` is
plain mode, unchanged. Under check mode:

- Up to **50** ids, counted on the RAW array before trimming and dedup; over the cap the whole
  call fails and nothing is evaluated.
- Ids are trimmed, then deduplicated: one result per unique id, in first-occurrence order,
  each echoing the normalized id. (Plain mode still renders one entry per occurrence.)
- Optional `filters` — `{read_only_only, exclude_destructive, exclude_open_world}` — with
  spec-094 semantics and order. Sent WITHOUT `check: true` it is a request error, because
  ignoring it would let an agent believe a safety filter was applied when it was not.
- **Strict validation** (099 FR-012a), each a request error rather than a coercion: a
  non-boolean `check` (including `null`), a non-object `filters`, an unknown `filters` member,
  a non-boolean filter value, and the RESERVED `expect_hashes` field in any shape (in-band
  hash pins were trimmed from v1; the name is reserved so they can be added later without a
  silent-drop window).
- Response — no `definitions`, no `errors`; a caller branches on the presence of `verdict`:

```json
{
  "verdict": "blocked",
  "checked_at": "2026-08-16T09:14:02.117Z",
  "request_id": "1755331442117-describe_tool-42",
  "results": [
    { "id": "gh:sync", "status": "ready" },
    { "id": "slack:post", "status": "unavailable", "reason": "server_quarantined",
      "retryable": false, "action": "approve", "detail": "…", "remediation": "…" },
    { "id": "gh:nope", "status": "unavailable", "reason": "not_found", "retryable": false,
      "action": "configure", "detail": "…", "remediation": "…",
      "did_you_mean": ["gh:sync"] }
  ]
}
```

- The verdicts come from the spec-098 evaluator through the same glue the REST surface uses:
  same closed 15-code enum, same precedence, same state sources. **No hash is ever returned**,
  and the tier is ALWAYS the agent-token tier, so `server_not_in_scope` and
  `server_not_configured` collapse into a byte-indistinguishable `not_found`.
- Zero upstream I/O and zero mutation, with one permitted local write: the preflight activity
  record, written synchronously before the verdict. A failed write fails the call, and check
  runs do NOT additionally emit the plain mode's `internal_tool_call` record.
- Conditions the REST surface answers with 400/503 are MCP tool errors here, never a verdict.

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
- **Batch resilience (FR-010)**: unknown and out-of-scope ids become per-id `errors` entries;
  the call as a whole returns success with whatever definitions resolved.
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
   error `not_found`, identical to an id that does not exist (**[099]**: this used to be a
   distinct `invisible` code, which confirmed existence to exactly the caller who may not know
   it; the remediation text was already the shared not-found string and is unchanged).
3. **Server-level quarantine** — quarantined server ⇒ per-id error (search hides these too).
4. **Tool-level approval (Spec 032)** — `pending`/`changed` ⇒ per-id error, not a definition.
5. **`isToolCallable(server, tool)`** (disabled/blocked) ⇒ per-id error.

Only when all five pass does the handler resolve the full definition via
`indexManager.GetToolsByServer(server)` (filtered to `tool`) and render it. Per-id error `error`
codes: `not_found`, `quarantined`, `pending_approval`, `changed`, `disabled` (**[099]**:
`invisible` retired). Each
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
- **≤250-token budget (FR-011 as amended by 099 FR-015)**: count the `describe_tool`
  definition's tokens with the **pinned tokenizer** the bench uses — tiktoken `cl100k_base`
  (same encoder the spec-083 profiler counts with, so the budget and the profiler agree) — and
  assert ≤250. The exact definition is additionally pinned by the `tools/list` goldens, so
  prose cannot drift silently under the ceiling.
- **[099] Check mode**: one cell per observable reason code (the committed sabotage matrix's
  `mcp-check` rows), the 50/51 cap boundary, every FR-012a rejection, the activity record and
  its write-failure path, in-band/REST parity at the agent-token tier, and plain-mode
  byte-identity with the single enumerated `invisible` → `not_found` delta.

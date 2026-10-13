# Contract: Self-healing InvalidParams Error

FR-013 / FR-013b / US3. Argument-validation failures on `call_tool_read|write|destructive`
return an error whose body embeds the **failing tool's full input schema** and a one-line
corrective hint, so the agent's next attempt can succeed. Mode-independent (identical in `full`
and `compact`). Non-argument failures are untouched.

## Two trigger paths

### Path A — pre-dispatch validation (new capability, FR-013)
Today `handleCallToolVariant` (mcp.go:1649) parses `args` (~1730) and dispatches to
`upstreamManager.CallTool` (~1955) with **no** schema check. New: after args are parsed and the
target `(server, tool)` is resolved and callable — but **before** `CallTool` — validate `args`
against the tool's stored `ParamsJSON` (via `internal/index` lookup, same source signatures
use) using `internal/server/mcp_input_validation.go` (santhosh v6, R3).

- Validation **fails** ⇒ return the self-healing error below **without** dispatching upstream
  (cheaper + faster than the round-trip; design mitigation 4).
- Schema **cannot compile** / unsupported construct ⇒ **fail-open** (FR-013b): skip validation,
  dispatch as today, increment a `validation_skipped` log counter. Never block a call a
  schemaless proxy would have allowed.

### Path B — upstream InvalidParams (best-effort classification, FR-013)
When `CallTool` returns an error, `createDetailedErrorResponse` (mcp.go:4767) classifies it. If
it is a JSON-RPC error with InvalidParams code (`-32602`) or the message best-effort matches an
argument-validation shape, attach the schema (Path A's renderer). Otherwise (transport, auth,
timeout, HTTP 5xx, upstream crash) keep today's error shape — **no schema attached** (FR-013
scenario 2).

## Error body (both paths)

```json
{
  "error": "invalid arguments for github:create_issue: missing required property 'title'",
  "error_type": "invalid_params",
  "tool": "github:create_issue",
  "input_schema": {
    "type": "object",
    "properties": { "title": {"type":"string"}, "body": {"type":"string"} },
    "required": ["title"]
  },
  "hint": "Fix arguments to match input_schema, then retry. For the full definition call describe_tool({tool_ids:[\"github:create_issue\"]})."
}
```

- `input_schema` is the tool's **full** stored schema (the same `ParamsJSON` object full-mode
  retrieve_tools / describe_tool return) — always full, even in compact mode (that is the point:
  it caps lossiness at one retry).
- `error_type: "invalid_params"` distinguishes self-healing errors from the existing
  HTTP/JSON-RPC error shapes `createDetailedErrorResponse` already emits.
- Returned via `mcp.NewToolResultError(json)` — same envelope as existing detailed errors.

## Guarantees

- **Zero happy-path cost (SC-006)**: the schema is built only on the error branch. Successful
  calls are unchanged; pre-dispatch validation of *valid* args adds only a local schema check
  (no schema serialized).
- **Mode independence (US3 scenario 3)**: the error is identical regardless of
  `tool_response_mode` or per-call `detail`.
- **Non-argument failures unchanged (FR-013 scenario 2)**: transport/auth/timeout/5xx keep the
  current `createDetailedErrorResponse` output; no `input_schema`.
- **Fail-open (FR-013b)**: an uncompilable/exotic schema ⇒ call dispatches as today; the failure
  mode is "behaves like today", never "blocks a previously-allowed call".

## Test obligations

- Omit a required param ⇒ pre-dispatch error with `input_schema` + `hint`, upstream **not**
  called (assert via a stub upstream that records invocations).
- One schema-informed retry with corrected args ⇒ success (E2E, SC-006).
- Upstream 500 / auth 401 / timeout ⇒ existing error shape, **no** `input_schema`.
- Uncompilable stored schema ⇒ dispatch proceeds (fail-open), `validation_skipped` counted.
- Same failing call in `full` and `compact` mode ⇒ byte-identical error.

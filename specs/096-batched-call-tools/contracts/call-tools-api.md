# Contract: call_tools() Sandbox API (Spec 096)

## Signature

```js
var slots = call_tools(requests, options);
```

- `requests`: `Array<{server: string, tool: string, args?: object}>` — dense array, ≤100 elements.
- `options` (optional): `{max_parallel?: integer}` — 1..32; unknown keys ignored.
- Returns `Array` of slots, `slots.length === requests.length`, input order.

## Slot envelope (identical to call_tool)

```js
{ok: true,  result: <wire-shaped value>}          // success
{ok: false, error: {code: string, message: string}}  // failure
```

## Whole-call errors (single envelope, nothing dispatched, no budget)

Returned (never thrown) when: requests is not an array; an element is not an object
with non-empty `server`/`tool` strings; supplied `args` not an object; sparse hole;
options not an object; `max_parallel` non-integer or out of 1..32; length > 100;
missing arguments. Message names the first offending element index.

```js
{ok: false, error: {code: "INVALID_ARGS", message: "call_tools: element 3: ..."}}
```

## Semantics

- Per-element enforcement/parity: same gates, codes, records as a lone `call_tool()` today.
- Pre-dispatch checks run in input order before any dispatch (budget cannot race).
- Concurrency ≤ effective max_parallel; per-server Spec-093 admission applies inside the call path (queue or shed per that server's config — never bypassed).
- Bounded by the execution timeout; workers are cancelled with the execution context and never mutate script-visible execution state; an in-flight element completing after the execution returns lands only in internally-synchronized records excluded from that execution's response (lone-call timeout parity).
- Empty array → `[]`, zero cost.
- Synchronous from the script's perspective; sandbox stays timer-free.

## Config contract

`code_execution_max_parallel` (int, default 8, range 1..32, hot-reload applies to
subsequent executions). REST `POST /api/v1/code/exec` accepts
no execution-level `max_parallel`; precedence is per-batch override >
`code_execution_max_parallel` > built-in 8.

## Tool description contract

Both code_execution registrations (default surface and routing-mode builder)
document `call_tools(requests, options)` beside `call_tool` and list
`max_parallel` in the options section, with identical text.

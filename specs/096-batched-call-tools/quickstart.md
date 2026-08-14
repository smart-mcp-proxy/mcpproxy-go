# Quickstart: call_tools() batched upstream calls

Fan out independent upstream calls in one code_execution request:

```js
var prs = call_tools(
  [1, 2, 3, 4, 5].map(function (n) {
    return {server: "github", tool: "get_pull_request",
            args: {owner: "acme", repo: "api", pullNumber: n}};
  }),
  {max_parallel: 5}
);

var titles = prs.map(function (r) {
  if (!r.ok) { return "ERR: " + r.error.code; }
  return JSON.parse(r.result.content[0].text).title;
});
({titles: titles})
```

- Results come back in input order; a failed slot never poisons its siblings.
- Each element costs one unit of `max_tool_calls` budget, checked in input order.
- Default concurrency comes from `code_execution_max_parallel` (8); override per
  call with `options.max_parallel` (1..32).
- Per-server concurrency limits (Spec 093) still govern: a server capped at
  `max_concurrent_requests: 1` with `queue_size: 9` serializes 10 elements; the
  same server with no queue sheds the overflow as per-slot errors.
- The whole batch lives inside the execution timeout — size batches accordingly.

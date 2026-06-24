# Code Execution — Orchestration Cookbook

**TypeScript** recipes for orchestrating multiple upstream MCP tools in a
single `code_execution` call. Each recipe shows the pattern, the multi‑call
sequence it replaces, and the token/latency it saves. Treat them as
**templates to adapt**, not literal paste‑and‑run scripts: swap the placeholder
server/tool names (`crm`, `orders`, `github`, `slack`, …) for your own. The
leading `// language:` / `// input:` lines are annotations — the executed
*code* is the body below them; `language` and `input` are separate
`code_execution` tool parameters.

TypeScript code execution is **generally available** (GA) — it shipped in
preview in v0.45.0 and graduates to GA in v0.46.0 (MCP-38, roadmap #15). See the
[Upgrade note](#upgrade-note-typescript-ga). Write TypeScript by setting
`language: "typescript"`; types are stripped before execution with <5 ms
overhead. Every recipe below also works as plain JavaScript — just drop the type
annotations and omit `language`.

> **New to code execution?** Read [overview.md](overview.md) first, then the
> [api-reference.md](api-reference.md) for the full tool schema. This cookbook
> assumes you know that `call_tool(server, tool, args)` returns
> `{ ok: true, result }` or `{ ok: false, error }`, and that the script's
> **last expression** becomes the result `value`. A bare top‑level `return`
> is a **SyntaxError** (`Illegal return statement`) — `return` is only legal
> inside a function. To early‑exit, wrap the body in an IIFE (see the
> sandbox‑contract table and Recipe 3).

## Table of Contents

1. [When to reach for the cookbook](#when-to-reach-for-the-cookbook)
2. [The sandbox contract (read this first)](#the-sandbox-contract-read-this-first)
3. Recipes
   - [Recipe 1 — Batch call (one tool, many inputs)](#recipe-1--batch-call-one-tool-many-inputs)
   - [Recipe 2 — Fan‑out + merge (many tools, one object)](#recipe-2--fan-out--merge-many-tools-one-object)
   - [Recipe 3 — Sequential pipeline (chain calls)](#recipe-3--sequential-pipeline-chain-calls)
   - [Recipe 4 — Conditional routing](#recipe-4--conditional-routing)
   - [Recipe 5 — Retry on retryable error](#recipe-5--retry-on-retryable-error)
   - [Recipe 6 — Continue‑on‑error (partial results)](#recipe-6--continue-on-error-partial-results)
   - [Recipe 7 — Map‑reduce aggregation](#recipe-7--map-reduce-aggregation)
   - [Recipe 8 — Cursor / pagination walk](#recipe-8--cursor--pagination-walk)
   - [Recipe 9 — Rate‑limit via chunking](#recipe-9--rate-limit-via-chunking)
   - [Recipe 10 — Deduplicate + enrich](#recipe-10--deduplicate--enrich)
4. [Benchmarks — token & latency](#benchmarks--token--latency)
5. [Upgrade note — TypeScript GA](#upgrade-note-typescript-ga)

---

## When to reach for the cookbook

`code_execution` wins whenever a task needs **2+ tool calls plus logic between
them**. Every recipe here collapses what would be N model round‑trips (the agent
reads each tool result and decides the next call) into **one** round‑trip: the
agent emits a single script, the proxy runs the whole orchestration server‑side,
and only the final result returns to the model.

If you are calling a single tool with no branching, use
`call_tool_read|write|destructive` directly — `code_execution` adds no value
there.

## The sandbox contract (read this first)

Every recipe depends on these facts about the goja sandbox. Violating them is
the #1 source of surprises:

| Capability | Status | Implication for recipes |
|------------|--------|-------------------------|
| `call_tool(server, tool, args)` | ✅ | The only way to reach upstream tools. Synchronous — returns when the tool responds. |
| `input` global | ✅ | Your parameters. Type it with `as` or an `interface` for IDE‑grade safety. |
| ES2020+ stdlib (`map`/`filter`/`reduce`, `JSON`, `Math`, `Date`) | ✅ | Use it freely for transforms and aggregation. |
| `console.log` | ✅ | Goes to **server logs**, not the result. Use for debugging. |
| Top‑level `return` | ❌ | `return` outside a function is a **SyntaxError** (`Illegal return statement`). The result is the script's **last expression**. To early‑exit, wrap the body in an IIFE: `(() => { … return x; })()`. |
| `setTimeout` / `setInterval` | ❌ | **No wall‑clock sleep.** "Backoff" and "rate‑limit" recipes work by *bounding* and *chunking*, never by sleeping. |
| `require` / `import` / `fetch` / `fs` | ❌ | No modules, no network, no filesystem. All I/O goes through `call_tool`. |
| Concurrency | ❌ (sequential) | Tool calls run **one at a time** server‑side. "Fan‑out" saves *round‑trips*, not wall‑clock from parallelism. Be honest about this when estimating latency. |

Two control knobs you will reach for constantly (set in `options`):

- `max_tool_calls` — a hard ceiling that aborts the script with
  `MAX_TOOL_CALLS_EXCEEDED`. Always set it on loops so a bad `input` can't fan
  out unbounded.
- `timeout_ms` — wall‑clock budget (default 120 000, max 600 000). The
  transpile step counts toward it (negligibly).

---

## Recipe 1 — Batch call (one tool, many inputs)

**Problem:** Call the same tool once per item in a list and collect the results.
Classic N‑round‑trip task: "look up all of these."

```typescript
// language: "typescript"
// input: { "usernames": ["octocat", "torvalds", "gaearon"] }
interface User { login: string; name: string; followers: number }

const out: Array<User | { login: string; error: string }> =
  (input.usernames as string[]).map((login: string) => {
    const res = call_tool("github", "get_user", { username: login });
    if (!res.ok) return { login, error: res.error.message };
    const u = res.result as User;
    return { login, name: u.name, followers: u.followers };
  });

({ users: out, count: out.length });
```

**Replaces:** N separate `call_tool` round‑trips, each requiring the model to
read the previous result before issuing the next.

**Guardrail:** set `options.max_tool_calls` to `usernames.length` (or a sane
cap) so an oversized input can't run away.

---

## Recipe 2 — Fan‑out + merge (many tools, one object)

**Problem:** Gather related facts from several *different* tools/servers and
return one merged object — a "dashboard" call.

```typescript
// language: "typescript"
// input: { "repo": "octocat/Hello-World" }
const [owner, name] = (input.repo as string).split("/");

const repo   = call_tool("github", "get_repo",        { owner, repo: name });
const issues = call_tool("github", "list_issues",     { owner, repo: name, state: "open" });
const ci     = call_tool("ci",     "latest_pipeline", { project: input.repo });

({
  repo:       repo.ok   ? { stars: repo.result.stargazers_count } : { error: repo.error.message },
  openIssues: issues.ok ? issues.result.length                    : null,
  ci:         ci.ok     ? ci.result.status                        : "unknown",
});
```

**Replaces:** 3 round‑trips + a final model turn to stitch the pieces together.
Here the merge happens server‑side; the model sees one tidy object.

**Note on "parallel":** the three calls run sequentially in the sandbox. The win
is collapsing 4 model turns into 1 — not parallel network I/O.

---

## Recipe 3 — Sequential pipeline (chain calls)

**Problem:** The output of one tool is the input to the next. Each step depends
on the last, so it *must* be sequential — but it shouldn't cost a round‑trip per
step.

```typescript
// language: "typescript"
// input: { "email": "user@example.com" }
// Wrap the body in an IIFE so early‑exit `return`s are legal — a bare
// top‑level `return` is a SyntaxError. The IIFE's value is the result.
(() => {
  const lookup = call_tool("crm", "find_customer", { email: input.email });
  if (!lookup.ok) return { stage: "find_customer", error: lookup.error.message };

  const customerId: string = lookup.result.id;
  const orders = call_tool("orders", "list_orders", { customerId, limit: 10 });
  if (!orders.ok) return { stage: "list_orders", error: orders.error.message };

  const latest = orders.result[0];
  const detail = call_tool("orders", "get_order", { orderId: latest.id });

  return { customerId, latestOrder: detail.ok ? detail.result : null };
})();
```

**Replaces:** a 3‑hop "find → list → get" sequence that would otherwise be three
model turns. The `stage` field makes failures self‑describing. The IIFE wrapper
is the idiom for any recipe that needs an early `return` — `return` is illegal
at the script's top level.

---

## Recipe 4 — Conditional routing

**Problem:** Pick which tool to call based on the shape of the data — branching
the model would normally do across multiple turns.

```typescript
// language: "typescript"
// input: { "query": "weather in Berlin" }
type Intent = "weather" | "search" | "math";

function route(q: string): Intent {
  if (/weather|forecast|temperature/i.test(q)) return "weather";
  if (/^[\d\s+\-*/().]+$/.test(q))             return "math";
  return "search";
}

const intent = route(input.query as string);
const res =
  intent === "weather" ? call_tool("weather", "current", { location: input.query }) :
  intent === "math"    ? { ok: true, result: { answer: "use a calc tool" } } :
                         call_tool("search", "web", { q: input.query });

({ intent, answer: res.ok ? res.result : { error: res.error.message } });
```

**Replaces:** an extra model turn whose only job was to decide which tool to
call. The decision is now code.

---

## Recipe 5 — Retry on retryable error

**Problem:** A flaky tool fails transiently. Retry a bounded number of times,
but only for *retryable* failures.

```typescript
// language: "typescript"
// input: { "id": 42, "maxAttempts": 3 }
interface CallResult { ok: boolean; result?: unknown; error?: { message: string; code?: string } }

function isRetryable(err: { message: string; code?: string }): boolean {
  if (err.code) return /^(5\d\d|TIMEOUT|UNAVAILABLE)$/.test(err.code);
  return /timeout|temporarily|unavailable|rate.?limit/i.test(err.message);
}

const max = (input.maxAttempts as number) || 3;
let last: CallResult = { ok: false, error: { message: "not attempted" } };

for (let attempt = 1; attempt <= max; attempt++) {
  last = call_tool("flaky-api", "fetch", { id: input.id }) as CallResult;
  if (last.ok) { (last as any).attempts = attempt; break; }
  if (!isRetryable(last.error!)) break; // fatal — stop early
}

last.ok
  ? ({ ok: true, result: last.result, attempts: (last as any).attempts })
  : ({ ok: false, error: last.error });
```

**⚠️ No wall‑clock backoff.** The sandbox has no `setTimeout`, so retries are
*immediate*. That is the right behaviour for transient races, but it will **not**
help a server that needs seconds to recover. For true exponential backoff, let
the failure surface to the agent and retry across turns, or push the retry into
the upstream tool. Do **not** busy‑wait — it burns the `timeout_ms` budget and
the CPU for nothing.

**Replaces:** the agent manually re‑issuing the same call and re‑reading the
error each time.

---

## Recipe 6 — Continue‑on‑error (partial results)

**Problem:** One bad item shouldn't sink the whole batch. Return the successes
*and* a structured list of failures.

```typescript
// language: "typescript"
// input: { "ids": [1, 2, 3, 4, 5] }
const ok: unknown[] = [];
const failed: Array<{ id: number; error: string }> = [];

for (const id of input.ids as number[]) {
  const res = call_tool("api-server", "process_item", { id });
  if (res.ok) ok.push(res.result);
  else        failed.push({ id, error: res.error.message });
}

({ processed: ok, failed, total: (input.ids as number[]).length, failures: failed.length });
```

**Replaces:** an all‑or‑nothing sequence where a single failure forces the agent
to restart. The caller gets a complete picture in one result.

---

## Recipe 7 — Map‑reduce aggregation

**Problem:** Fetch many records, then compute a summary the model would
otherwise have to do token‑by‑token.

```typescript
// language: "typescript"
// input: { "symbols": ["AAPL", "MSFT", "GOOG"] }
interface Quote { symbol: string; price: number }

const quotes: Quote[] = (input.symbols as string[])
  .map((s: string) => call_tool("market", "quote", { symbol: s }))
  .filter((r: any) => r.ok)
  .map((r: any) => r.result as Quote);

const prices = quotes.map((q) => q.price);
({
  count: quotes.length,
  total: prices.reduce((a, b) => a + b, 0),
  avg:   prices.length ? prices.reduce((a, b) => a + b, 0) / prices.length : 0,
  max:   quotes.sort((a, b) => b.price - a.price)[0] ?? null,
});
```

**Replaces:** N fetch round‑trips **plus** the model doing arithmetic over the
raw rows. Returning the *summary* instead of the rows is also a large token win
on the response side.

---

## Recipe 8 — Cursor / pagination walk

**Problem:** Walk a paginated API until the cursor runs out — without one
round‑trip per page.

```typescript
// language: "typescript"
// input: { "query": "open source", "maxPages": 5 }
const items: unknown[] = [];
let cursor: string | null = null;
const maxPages = (input.maxPages as number) || 5;

for (let page = 0; page < maxPages; page++) {
  const res = call_tool("search", "list", { q: input.query, cursor });
  if (!res.ok) break;
  items.push(...res.result.items);
  cursor = res.result.nextCursor ?? null;
  if (!cursor) break; // no more pages
}

({ items, fetched: items.length, exhausted: cursor === null });
```

**Replaces:** the agent paging by hand — read page, see cursor, ask for next,
repeat. **Always** bound the loop with `maxPages` *and* set
`options.max_tool_calls`; a non‑terminating cursor must not loop forever.

---

## Recipe 9 — Rate‑limit via chunking

**Problem:** A downstream tool rejects large bursts. You can't `sleep`, so you
control pressure by **bounding batch size**, not by waiting.

```typescript
// language: "typescript"
// input: { "ids": [/* 100 ids */], "chunkSize": 10 }
function chunk<T>(arr: T[], size: number): T[][] {
  const out: T[][] = [];
  for (let i = 0; i < arr.length; i += size) out.push(arr.slice(i, i + size));
  return out;
}

const size = (input.chunkSize as number) || 10;
const batches = chunk(input.ids as number[], size);
const results: unknown[] = [];

for (const batch of batches) {
  // Prefer ONE bulk call per batch over N singles — fewer calls = less pressure.
  const res = call_tool("api-server", "bulk_process", { ids: batch });
  if (res.ok) results.push(...res.result);
}

({ batches: batches.length, chunkSize: size, processed: results.length });
```

**Why this works:** real rate‑limit relief comes from **fewer, bulkier calls**,
not from sleeping between many small ones. If the tool has no bulk variant, cap
the per‑script call count with `max_tool_calls` and let the agent resume across
turns. (See [troubleshooting.md](troubleshooting.md) for the
`setTimeout`‑is‑unavailable rationale.)

**Replaces:** 100 individual round‑trips with ~10 bulk calls in one script.

---

## Recipe 10 — Deduplicate + enrich

**Problem:** Collect IDs from one source, dedupe them, then enrich each unique
ID from another — a two‑stage gather the model would do across many turns.

```typescript
// language: "typescript"
// input: { "channels": ["general", "random"] }
const ids = new Set<string>();

for (const ch of input.channels as string[]) {
  const res = call_tool("slack", "list_members", { channel: ch });
  if (res.ok) for (const m of res.result.members as string[]) ids.add(m);
}

const enriched = Array.from(ids).map((id: string) => {
  const res = call_tool("slack", "get_user", { user: id });
  return res.ok ? { id, name: res.result.real_name } : { id, name: null };
});

({ uniqueMembers: enriched.length, members: enriched });
```

**Replaces:** the agent manually merging member lists, spotting duplicates, and
issuing one lookup per unique ID. `Set` does the dedupe in‑sandbox.

---

## Benchmarks — token & latency

The numbers behind these recipes come from the reproducible harness in
[`bench/`](../../bench/README.md), published on every release to the
[benchmark dashboard](https://mcpproxy-bench.pages.dev). Reproduce locally with:

```bash
go run ./bench/cmd/bench            # deterministic token reduction
go run ./bench/cmd/bench -live ...  # live accuracy + client-side latency
```

### Token reduction — context window

Measured by tiktoken over the tool definitions a model must hold in context
(frozen corpus, full input schemas counted on both sides):

| Routing mode | Tools in context | Tokens | Reduction vs. all‑tools baseline |
|--------------|------------------|--------|-----------------------------------|
| Baseline (all upstream tools inline) | 10 | 1707 | — |
| `retrieve_tools` (BM25 on demand) | 10 | 1431 | **~17%** |
| **`code_execution`** | 6 | 986 | **~43%** |

`code_execution` wins biggest because the model only needs `code_execution` +
`retrieve_tools` + management tools in context — the upstream toolset never
enters the window. The savings grow with the size of the upstream catalog.

### Latency / round‑trips — the orchestration win

The decisive metric for these recipes is **model round‑trips**, not server‑side
wall‑clock. An N‑step orchestration costs:

| Approach | Model round‑trips | Model output tokens (the expensive part) |
|----------|-------------------|------------------------------------------|
| Sequential `call_tool` (N steps) | **N** | N × (reasoning + tool‑call JSON + result echo) |
| One `code_execution` script | **1** | 1 × (the script) |

Each eliminated round‑trip removes a full model turn — its latency, its
generated tool‑call JSON, and its re‑reading of the intermediate result. For a
5‑step recipe that is roughly a **5×** reduction in model turns. Server‑side the
N tool calls still run sequentially (the sandbox is single‑threaded — see [the
sandbox contract](#the-sandbox-contract-read-this-first)), so `code_execution`
optimizes the *agent loop*, not raw upstream I/O. Quote round‑trip savings, not
parallelism, when you describe the win.

---

## Upgrade note — TypeScript GA

**Status:** TypeScript code execution (Spec 033) graduates from **preview to
GA** in v0.46.0 (it shipped in preview in v0.45.0). There is no
preview/experimental flag and none was ever removed from config — the TypeScript
path graduated by meeting its acceptance criteria, not by flipping a gate. It
rides the same master switch as JavaScript code execution.

**To enable code execution** (off by default for security — sandboxed code is a
deliberate opt‑in):

```json
{
  "enable_code_execution": true,
  "code_execution_timeout_ms": 120000,
  "code_execution_max_tool_calls": 0,
  "code_execution_pool_size": 10
}
```

**Using TypeScript** — no new configuration:

- **MCP tool / REST:** add `"language": "typescript"` to the request.
- **CLI:** `mcpproxy code exec --language typescript --code "const x: number = 42; ({ x })"`.

**Backward compatibility:** omitting `language` (or setting `"javascript"`)
behaves exactly as before — no transpilation step, byte‑for‑byte the same
execution path. Existing JavaScript scripts are unaffected. Transpilation adds
<5 ms and counts toward `timeout_ms`.

**What you get with TypeScript:** type annotations, `interface`/`type`,
generics, enums, and namespaces — all stripped to plain ES2020+ before running
in the goja sandbox. The runtime feature set is unchanged from JavaScript (same
sandbox contract above); TypeScript only improves authoring ergonomics and
catches shape mistakes at write time.

See also: [overview.md](overview.md) · [api-reference.md](api-reference.md) ·
[examples.md](examples.md) · [troubleshooting.md](troubleshooting.md).

---
id: code-execution
title: Code Execution
sidebar_label: Code Execution
sidebar_position: 3
description: Execute JavaScript or TypeScript to orchestrate multiple MCP tools
keywords: [code, execution, javascript, typescript, orchestration]
---

# Code Execution

The `code_execution` tool enables orchestrating multiple upstream MCP tools in a single request using sandboxed JavaScript (ES2020+) or TypeScript.

## Overview

Code execution allows AI agents to:

- Chain multiple tool calls together
- Process and transform tool outputs
- Implement complex logic and conditionals
- Reduce round-trip latency
- Run [stored scripts](#stored-scripts) by name instead of re-sending the source every call

## Configuration

Enable code execution in your config:

```json
{
  "enable_code_execution": true,
  "code_execution_timeout_ms": 120000,
  "code_execution_max_tool_calls": 0,
  "code_execution_pool_size": 10,
  "code_execution_max_parallel": 8
}
```

| Option | Type | Default | Description |
|--------|------|---------|-------------|
| `enable_code_execution` | boolean | `false` | Enable the code execution tool |
| `code_execution_timeout_ms` | integer | `120000` | Execution timeout (2 minutes) |
| `code_execution_max_tool_calls` | integer | `0` | Max tool calls (0 = unlimited) |
| `code_execution_pool_size` | integer | `10` | Number of VM instances to pool |
| `code_execution_max_parallel` | integer | `8` | Default concurrency for `call_tools()` batches (1-32) |

## CLI Usage

### Basic Execution

```bash
mcpproxy code exec --code="({ result: input.value * 2 })" --input='{"value": 21}'
```

### TypeScript Execution

```bash
mcpproxy code exec --language typescript --code="const x: number = 42; ({ result: x })"
```

### Tool Orchestration

```bash
mcpproxy code exec --code="call_tool('github', 'get_user', {username: input.user})" --input='{"user":"octocat"}'
```

### Stored Script

```bash
mcpproxy code scripts list
mcpproxy code exec --script fetch-prs --input='{"owner":"acme","repo":"api"}'
```

## API

### Input Schema

```json
{
  "code": "string - inline JavaScript or TypeScript code to execute",
  "script": "string - name of a stored script to execute instead of 'code'",
  "language": "string (optional) - 'javascript' (default) or 'typescript'",
  "input": "object (optional) - Input data available as 'input' variable"
}
```

Provide **exactly one** of `code` or `script` — both or neither is an error.
Neither is schema-required, because JSON Schema cannot express the rule; the
tool enforces it on every surface (MCP, REST, CLI).

### Built-in Functions

#### call_tool(server, tool, args)

Execute a tool on an upstream server:

```javascript
var result = call_tool('github', 'create_issue', {
  repo: 'owner/repo',
  title: 'Bug report',
  body: 'Description'
});
```

#### call_tools(requests, options)

Execute **independent** tools in parallel and get one result slot per request, in
input order:

```javascript
var slots = call_tools([
  {server: 'github', tool: 'get_pull_request', args: {owner: 'acme', repo: 'api', pullNumber: 1}},
  {server: 'github', tool: 'get_pull_request', args: {owner: 'acme', repo: 'api', pullNumber: 2}},
  {server: 'ci', tool: 'latest_pipeline', args: {project: 'acme/api'}}
], {max_parallel: 5});

slots.map(function (s) { return s.ok ? s.result : 'ERR: ' + s.error.code; });
```

- `requests`: array of `{server, tool, args?}` (max 100). `args` defaults to `{}`.
- `options.max_parallel`: 1-32, defaults to `code_execution_max_parallel` (8).
- Each slot is the same `{ok: true, result}` / `{ok: false, error}` envelope
  `call_tool()` returns, so one failing element never fails its siblings.
- Malformed arguments return a single `{ok: false, error}` envelope naming the
  first offending element index, and nothing is dispatched.
- Every element costs one unit of `max_tool_calls`, checked in input order.
- Per-server concurrency limits still apply: a server capped with
  `max_concurrent_requests` and **no** `queue_size` sheds the overflow as
  per-slot `queue_full` errors. Set `queue_size` (or lower `max_parallel`) when
  fanning out against a limited server.

#### log(message)

Log a message (visible in tool response):

```javascript
log('Processing started');
```

### Return Value

The last expression in the code is returned as the tool result:

```javascript
// Return an object
({
  success: true,
  data: result
})
```

## Stored Scripts

A stored script is a `<name>.js` / `<name>.ts` file the operator drops into the
`scripts/` directory next to the active config file. Agents then run it by name
with `script: "<name>"` instead of re-sending the whole source on every call — a
19 KB workflow costs a name plus its `input` per run.

```bash
mkdir -p ~/.mcpproxy/scripts
cat > ~/.mcpproxy/scripts/fetch-prs.js <<'JS'
var rs = call_tools([1, 2, 3].map(function (n) {
  return {server: "github", tool: "get_pull_request",
          args: {owner: input.owner, repo: input.repo, pullNumber: n}};
}));
({titles: rs.map(function (r) { return r.ok ? JSON.parse(r.result.content[0].text).title : "ERR"; })});
JS

mcpproxy code scripts list
mcpproxy code exec --script fetch-prs --input='{"owner":"acme","repo":"api"}'
```

An MCP client runs the same script with
`{"script": "fetch-prs", "input": {"owner": "acme", "repo": "api"}}`.

### Authoring rules

| Rule | Value |
|------|-------|
| Location | `scripts/` next to the **active config file** (default `~/.mcpproxy/scripts/`) |
| Name | 1–64 characters of `A-Za-z0-9_-`, case-sensitive — a bare name, never a path |
| Extension | lowercase `.js` or `.ts` only; the extension decides the language |
| Size | 1 byte – 256 KB (empty and oversized files are rejected) |
| File type | regular files only — a symlink at the script path is rejected |

`.ts` scripts take exactly the same transpilation path as inline
`language: "typescript"`. Passing a `language` that contradicts the extension is
an error; omitting it is the normal case.

Both `name.js` and `name.ts` present is **ambiguous** — the invocation fails
naming both files rather than picking one.

### Freshness

Every invocation opens and reads the file once, with no cache and no watcher.
Edit a script by **atomic replace** (write a temp file, then rename over it) and
the next invocation runs the new content — no daemon restart. Added and removed
files are likewise picked up on next use. An in-place write racing an invocation
yields unspecified (but validated) content, which is why atomic replace is the
supported edit.

### Discovery

- **CLI / REST**: `mcpproxy code scripts list` (or `GET /api/v1/code/scripts`)
  lists every name with its path and a status: `ok`, `ambiguous`, or `invalid`
  (with a reason). Only `ok` scripts are invocable.
- **MCP clients**: discovery is error-driven. Invoking a name that does not
  exist returns an error listing the first 20 available names alphabetically
  plus the total count — the current name set is always one failed call away.
  Tool registrations stay static; there is no listing tool and no
  `tools/list_changed` notification.

### No write path

Nothing creates, edits, or deletes scripts through any API — no MCP tool, no
REST endpoint, no CLI verb. The filesystem is the only authoring interface, so
an agent can run stored workflows but never author them.

## Examples

### Simple Calculation

```javascript
// input: { "a": 5, "b": 3 }
({ sum: input.a + input.b })
```

### Tool Chaining

```javascript
// Get user info then create an issue
var user = call_tool('github', 'get_user', { username: input.username });
var issue = call_tool('github', 'create_issue', {
  repo: input.repo,
  title: 'Issue from ' + user.name,
  body: 'Created by code execution'
});
({ user: user, issue: issue })
```

### Conditional Logic

```javascript
var files = call_tool('filesystem', 'list_directory', { path: input.path });

if (files.length > 100) {
  ({ status: 'too_many_files', count: files.length });
} else {
  var results = [];
  for (var i = 0; i < files.length; i++) {
    if (files[i].endsWith('.md')) {
      results.push(files[i]);
    }
  }
  ({ markdown_files: results });
}
```

### Error Handling

```javascript
try {
  var result = call_tool('api', 'fetch_data', { url: input.url });
  ({ success: true, data: result });
} catch (e) {
  ({ success: false, error: e.message });
}
```

## Security

### Sandbox Environment

- Code runs in isolated JavaScript VM (goja)
- No access to file system
- No access to network (except via call_tool)
- No access to process environment
- Memory limits enforced

### Tool Call Security

- Tools inherit quarantine status
- Rate limiting applied
- Response size limits enforced

## Troubleshooting

### Timeout Errors

Increase the timeout or optimize your code:

```json
{
  "code_execution_timeout_ms": 300000
}
```

### Syntax Errors

Modern JavaScript syntax (ES2020+) is fully supported, including arrow functions, const/let, template literals, destructuring, optional chaining, and nullish coalescing:

```javascript
// All of these work
const result = () => call_tool('server', 'tool');
const name = user?.profile?.name ?? 'unknown';
const msg = `Hello, ${name}!`;
const { data, error } = response;
```

### Tool Not Found

Verify the server and tool names:

```bash
mcpproxy tools list --server=server-name
```

### Stored Script Not Found

The error already lists the available names. To see the full set — including
`ambiguous` and `invalid` entries and the directory that was read:

```bash
mcpproxy code scripts list
```

## TypeScript Support

Set `language: "typescript"` to write code with type annotations. TypeScript types are automatically stripped before execution using esbuild, with near-zero transpilation overhead (<5ms).

### Supported Features

- Type annotations: `const x: number = 42`
- Interfaces: `interface User { name: string; age: number; }`
- Type aliases: `type StringOrNumber = string | number`
- Generics: `function identity<T>(arg: T): T { return arg; }`
- Enums: `enum Direction { Up = "UP", Down = "DOWN" }`
- Namespaces and type assertions

### Example

```bash
mcpproxy code exec --language typescript \
  --code="interface User { name: string; }
const user: User = { name: input.username };
({ greeting: 'Hello ' + user.name })" \
  --input='{"username": "Alice"}'
```

### Important Notes

- TypeScript support uses type-stripping only (no type checking or semantic validation)
- Valid JavaScript is also valid TypeScript
- Transpilation errors return the `TRANSPILE_ERROR` error code with line/column information
- See `docs/code_execution/overview.md` in the repository for comprehensive TypeScript documentation

## Best Practices

1. **Keep code simple**: Complex logic is harder to debug
2. **Handle errors**: Use try/catch for tool calls
3. **Minimize tool calls**: Batch operations when possible; run independent calls with `call_tools()` instead of a sequential loop
4. **Use logging**: Add log() calls for debugging
5. **Test locally**: Use CLI to test before integrating

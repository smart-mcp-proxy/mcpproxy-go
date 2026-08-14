# Code Execution - API Reference

Complete reference for the `code_execution` MCP tool (JavaScript and TypeScript).

## Table of Contents

1. [Tool Schema](#tool-schema)
2. [Request Format](#request-format)
3. [Response Format](#response-format)
4. [JavaScript API](#javascript-api)
5. [Error Codes](#error-codes)
6. [Configuration](#configuration)
7. [CLI Reference](#cli-reference)

---

## Tool Schema

### MCP Tool Definition

```json
{
  "name": "code_execution",
  "description": "Execute JavaScript code that orchestrates multiple upstream MCP tools in a single request...",
  "inputSchema": {
    "type": "object",
    "properties": {
      "code": {
        "type": "string",
        "description": "JavaScript or TypeScript source code (ES2020+) to execute..."
      },
      "language": {
        "type": "string",
        "description": "Source code language. When set to 'typescript', the code is automatically transpiled to JavaScript before execution.",
        "enum": ["javascript", "typescript"],
        "default": "javascript"
      },
      "input": {
        "type": "object",
        "description": "Input data accessible as global `input` variable in code",
        "default": {}
      },
      "options": {
        "type": "object",
        "description": "Execution options",
        "properties": {
          "timeout_ms": {
            "type": "number",
            "description": "Execution timeout in milliseconds (1-600000)",
            "minimum": 1,
            "maximum": 600000
          },
          "max_tool_calls": {
            "type": "number",
            "description": "Maximum number of tool calls (0 = unlimited)",
            "minimum": 0
          },
          "allowed_servers": {
            "type": "array",
            "items": {"type": "string"},
            "description": "Array of server names allowed to be called (empty = all allowed)"
          }
        }
      }
    },
    "required": ["code"]
  }
}
```

---

## Request Format

### Basic Request

```json
{
  "code": "({ result: input.value * 2 })",
  "input": {
    "value": 21
  }
}
```

### TypeScript Request

```json
{
  "code": "const x: number = 42; const msg: string = 'hello'; ({ result: x, message: msg })",
  "language": "typescript",
  "input": {}
}
```

### Full Request with Options

```json
{
  "code": "var res = call_tool('github', 'get_user', {username: input.username}); return res.ok ? res.result : {error: res.error};",
  "input": {
    "username": "octocat"
  },
  "options": {
    "timeout_ms": 30000,
    "max_tool_calls": 10,
    "allowed_servers": ["github", "gitlab"]
  }
}
```

### Request Parameters

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `code` | string | **Yes** | JavaScript or TypeScript source code to execute (ES2020+ syntax supported) |
| `language` | string | No | Source language: `"javascript"` (default) or `"typescript"` |
| `input` | object | No | Input data accessible as `input` global variable (default: `{}`) |
| `options` | object | No | Execution options (see below) |

### Options Object

| Field | Type | Required | Default | Description |
|-------|------|----------|---------|-------------|
| `timeout_ms` | number | No | `120000` (2 min) | Execution timeout in milliseconds (range: 1-600000) |
| `max_tool_calls` | number | No | `0` (unlimited) | Maximum number of `call_tool()` invocations allowed (0 = no limit) |
| `allowed_servers` | array of strings | No | `[]` (all allowed) | Server names allowed to be called. Empty array = all servers allowed |

---

## Response Format

### Success Response

```json
{
  "ok": true,
  "value": <JavaScript return value>
}
```

**Example**:
```json
{
  "ok": true,
  "value": {
    "result": 42,
    "timestamp": 1699564800000
  }
}
```

### Error Response

```json
{
  "ok": false,
  "error": {
    "code": "<ERROR_CODE>",
    "message": "<error message>",
    "stack": "<stack trace>"
  }
}
```

**Example**:
```json
{
  "ok": false,
  "error": {
    "code": "RUNTIME_ERROR",
    "message": "Cannot read property 'name' of undefined",
    "stack": "ReferenceError: Cannot read property 'name' of undefined\n    at <eval>:1:23"
  }
}
```

### Response Fields

#### Success Response

| Field | Type | Description |
|-------|------|-------------|
| `ok` | boolean | Always `true` for successful execution |
| `value` | any | The return value from the JavaScript code (must be JSON-serializable) |

#### Error Response

| Field | Type | Description |
|-------|------|-------------|
| `ok` | boolean | Always `false` for failed execution |
| `error` | object | Error details |
| `error.code` | string | Error code (see [Error Codes](#error-codes)) |
| `error.message` | string | Human-readable error message |
| `error.stack` | string | Stack trace (for runtime errors) |

---

## JavaScript API

### Global Variables

#### `input`

The input data passed via the `input` parameter in the request.

**Type**: `object`

**Example**:
```javascript
// Request: {"input": {"username": "octocat", "limit": 10}}

// In JavaScript:
var username = input.username;  // "octocat"
var limit = input.limit;        // 10
```

### Global Functions

#### `call_tool(serverName, toolName, args)`

Calls an upstream MCP tool.

**Parameters**:
- `serverName` (string, required): Name of the upstream MCP server
- `toolName` (string, required): Name of the tool to call
- `args` (object, required): Arguments to pass to the tool

**Returns**: Object with the following structure:

```javascript
// Success
{
  "ok": true,
  "result": <tool result>
}

// Error
{
  "ok": false,
  "error": {
    "message": "<error message>",
    "code": "<optional error code>"
  }
}
```

**Example**:
```javascript
var res = call_tool('github', 'get_user', {username: 'octocat'});

if (res.ok) {
  return {
    name: res.result.name,
    repos: res.result.public_repos
  };
} else {
  return {
    error: 'Failed to get user: ' + res.error.message
  };
}
```

**Error Handling**:
```javascript
// Always check res.ok before accessing res.result
var res = call_tool('server', 'tool', {arg: 'value'});

if (!res.ok) {
  // Handle error
  return {error: res.error.message};
}

// Use result
var data = res.result;
```

#### `call_tools(requests, options)`

Calls **independent** upstream MCP tools in parallel and returns one result slot
per request, in input order.

**Parameters**:
- `requests` (array, required): Up to 100 elements of `{server, tool, args}`.
  `server` and `tool` are non-empty strings; `args` is optional and defaults to `{}`.
- `options` (object, optional): `{max_parallel}` — integer 1-32, defaults to the
  configured `code_execution_max_parallel` (8). Unknown keys are ignored.

**Returns**: An array with `slots.length === requests.length`, where each slot is
the same envelope `call_tool()` returns:

```javascript
// slots[i] for a successful requests[i]
{
  "ok": true,
  "result": <tool result>
}

// slots[i] for a failed requests[i]
{
  "ok": false,
  "error": {
    "message": "<error message>",
    "code": "<error code>"
  }
}
```

**Example**:
```javascript
var slots = call_tools(
  [1, 2, 3, 4, 5].map(function (n) {
    return {server: 'github', tool: 'get_pull_request',
            args: {owner: 'acme', repo: 'api', pullNumber: n}};
  }),
  {max_parallel: 5}
);

var titles = slots.map(function (r) {
  if (!r.ok) { return 'ERR: ' + r.error.code; }
  return JSON.parse(r.result.content[0].text).title;
});
({titles: titles});
```

**Semantics**:
- Per-element enforcement matches a lone `call_tool()`: the same gates, the same
  error codes, the same activity records. One failing element never affects its
  siblings.
- Each element costs one unit of `max_tool_calls`, checked in input order before
  anything is dispatched.
- Concurrency never exceeds the effective `max_parallel`, and per-server
  concurrency limits still apply inside the call path.
- The whole batch runs inside the execution timeout; a timeout cancels in-flight
  elements.
- `call_tools([])` returns `[]` and costs nothing. Like `call_tool()`, the
  function is **synchronous** — do not use `await`.

**Whole-call errors**: a malformed call returns a **single** envelope (not an
array) and dispatches nothing:

```javascript
{ok: false, error: {code: "INVALID_ARGS", message: "call_tools: element 3: ..."}}
```

This happens when `requests` is not an array, an element is not an object with
non-empty `server`/`tool` strings, a supplied `args` is not an object, the array
has a sparse hole, `options` is not an object, `max_parallel` is not an integer
in 1-32, or the batch exceeds 100 elements. The message names the first
offending element index.

### Available JavaScript Features

#### JavaScript Standard Library (ES2020+)

✅ **Available**:
- **Objects**: `Object.keys()`, `Object.create()`, `Object.defineProperty()`, etc.
- **Arrays**: `Array.isArray()`, `[].map()`, `[].filter()`, `[].reduce()`, `[].forEach()`, etc.
- **Strings**: `String.prototype.split()`, `.trim()`, `.indexOf()`, `.substring()`, etc.
- **Math**: `Math.round()`, `Math.floor()`, `Math.random()`, `Math.max()`, etc.
- **Date**: `new Date()`, `Date.now()`, `.getTime()`, `.toISOString()`, etc.
- **JSON**: `JSON.parse()`, `JSON.stringify()`
- **Console**: `console.log()` (for debugging, outputs to server logs)

❌ **Not Available**:
- **Modules**: `require()`, `import`, `export`
- **Timers**: `setTimeout()`, `setInterval()`, `setImmediate()`
- **Filesystem**: No `fs` module or file I/O
- **Network**: No `http`, `https`, `fetch`, or network access
- **Process**: No `process` object or environment variables
- **Node.js APIs**: No Node.js-specific APIs (Buffer, Stream, etc.)

#### Type Conversions

```javascript
// String to number
var num = parseInt('42', 10);        // 42
var float = parseFloat('3.14');      // 3.14

// Number to string
var str = (42).toString();           // "42"
var fixed = (3.14159).toFixed(2);    // "3.14"

// Boolean conversions
var bool = Boolean(value);           // true or false
var isTruthy = !!value;              // true or false

// Array/Object checks
var isArray = Array.isArray(value);
var isObject = typeof value === 'object' && value !== null;
```

---

## Error Codes

### Error Code Reference

| Code | Description | Cause | Solution |
|------|-------------|-------|----------|
| `SYNTAX_ERROR` | JavaScript syntax error | Invalid JavaScript syntax | Fix syntax errors in code |
| `RUNTIME_ERROR` | JavaScript runtime error | Uncaught exception during execution | Add error handling, check variable access |
| `TIMEOUT` | Execution timeout | Code exceeded `timeout_ms` limit | Optimize code, increase timeout, avoid infinite loops |
| `MAX_TOOL_CALLS_EXCEEDED` | Tool call limit exceeded | Code called `call_tool()` more than `max_tool_calls` times | Reduce tool calls, increase limit, or use pagination |
| `SERVER_NOT_ALLOWED` | Server not in allowed list | Attempted to call server not in `allowed_servers` | Add server to allowed list or remove restriction |
| `SERIALIZATION_ERROR` | Result not JSON-serializable | Return value contains functions, circular refs, etc. | Return only plain objects, arrays, primitives |
| `INVALID_ARGS` | Host function called with arguments it cannot interpret | Wrong arity for `call_tool()`, or a malformed `call_tools()` batch (bad element shape, bad `max_parallel`, >100 elements) | Fix the offending argument — the message names the first offending element index |

### Error Examples

#### SYNTAX_ERROR

```javascript
// Request
{
  "code": "var x = { missing bracket"
}

// Response
{
  "ok": false,
  "error": {
    "code": "SYNTAX_ERROR",
    "message": "SyntaxError: Unexpected end of input",
    "stack": ""
  }
}
```

#### RUNTIME_ERROR

```javascript
// Request
{
  "code": "var x = null; x.property"
}

// Response
{
  "ok": false,
  "error": {
    "code": "RUNTIME_ERROR",
    "message": "TypeError: Cannot read property 'property' of null",
    "stack": "TypeError: Cannot read property 'property' of null\n    at <eval>:1:17"
  }
}
```

#### TIMEOUT

```javascript
// Request
{
  "code": "while(true) {}",
  "options": {"timeout_ms": 1000}
}

// Response
{
  "ok": false,
  "error": {
    "code": "TIMEOUT",
    "message": "JavaScript execution timed out",
    "stack": ""
  }
}
```

#### MAX_TOOL_CALLS_EXCEEDED

```javascript
// Request
{
  "code": "for(var i=0;i<10;i++){call_tool('api','ping',{})}",
  "options": {"max_tool_calls": 5}
}

// Response
{
  "ok": false,
  "error": {
    "code": "MAX_TOOL_CALLS_EXCEEDED",
    "message": "Exceeded maximum tool calls limit (5)",
    "stack": ""
  }
}
```

#### SERVER_NOT_ALLOWED

```javascript
// Request
{
  "code": "call_tool('gitlab', 'get_user', {username: 'test'})",
  "options": {"allowed_servers": ["github"]}
}

// Response
{
  "ok": false,
  "error": {
    "code": "SERVER_NOT_ALLOWED",
    "message": "Server 'gitlab' is not in the allowed servers list",
    "stack": ""
  }
}
```

#### SERIALIZATION_ERROR

```javascript
// Request
{
  "code": "({fn: function() { return 42; }})"
}

// Response
{
  "ok": false,
  "error": {
    "code": "SERIALIZATION_ERROR",
    "message": "Result contains non-JSON-serializable values (functions, circular references, etc.)",
    "stack": ""
  }
}
```

---

## Configuration

### Global Configuration

Edit `~/.mcpproxy/mcp_config.json`:

```json
{
  "enable_code_execution": false,
  "code_execution_timeout_ms": 120000,
  "code_execution_max_tool_calls": 0,
  "code_execution_pool_size": 10,
  "code_execution_max_parallel": 8
}
```

### Configuration Fields

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `enable_code_execution` | boolean | `false` | Enable/disable code execution feature (must be `true` to use) |
| `code_execution_timeout_ms` | number | `120000` | Default timeout in milliseconds (range: 1-600000) |
| `code_execution_max_tool_calls` | number | `0` | Default max tool calls (0 = unlimited) |
| `code_execution_pool_size` | number | `10` | Number of JavaScript VM instances in pool (range: 1-100) |
| `code_execution_max_parallel` | number | `8` | Default concurrency for `call_tools()` batches (range: 1-32). Hot-reloaded; applies to executions started after the change |

### Per-Request Overrides

Per-request options override global configuration:

```json
{
  "code": "...",
  "options": {
    "timeout_ms": 60000,           // Override global timeout
    "max_tool_calls": 20,          // Override global max_tool_calls
    "allowed_servers": ["github"]  // Override (no global equivalent)
  }
}
```

**Priority**: Request options > Global config > Built-in defaults

`max_parallel` is deliberately **not** a request option: batch concurrency is
overridden inside the script, per batch, with `call_tools(requests, {max_parallel})`.
Its priority is per-batch override > `code_execution_max_parallel` > built-in 8.

---

## CLI Reference

### Command: `mcpproxy code exec`

Execute JavaScript code from the command line without an MCP client connection.

#### Basic Usage

```bash
mcpproxy code exec [flags]
```

#### Flags

| Flag | Type | Default | Description |
|------|------|---------|-------------|
| `--code` | string | | JavaScript code to execute (required if `--file` not provided) |
| `--file` | string | | Path to JavaScript file (required if `--code` not provided) |
| `--input` | string | `"{}"` | Input data as JSON string |
| `--input-file` | string | | Path to JSON file containing input data |
| `--timeout` | int | `120000` | Execution timeout in milliseconds (1-600000) |
| `--max-tool-calls` | int | `0` | Maximum tool calls (0 = unlimited) |
| `--allowed-servers` | []string | `[]` | Comma-separated list of allowed server names |
| `--log-level` | string | `"info"` | Log level (trace, debug, info, warn, error) |
| `--config` | string | `~/.mcpproxy/mcp_config.json` | Path to MCP configuration file |

#### Exit Codes

| Code | Meaning |
|------|---------|
| `0` | Successful execution |
| `1` | Execution failed (syntax error, runtime error, timeout, etc.) |
| `2` | Invalid arguments or configuration |

#### Examples

```bash
# Basic inline code
mcpproxy code exec --code="({ result: input.value * 2 })" --input='{"value": 21}'

# Code from file
mcpproxy code exec --file=script.js --input-file=params.json

# Call upstream tools
mcpproxy code exec --code="call_tool('github', 'get_user', {username: input.user})" --input='{"user":"octocat"}'

# With timeout and limits
mcpproxy code exec --code="..." --timeout=60000 --max-tool-calls=10

# Restrict to specific servers
mcpproxy code exec --code="..." --allowed-servers=github,gitlab

# Debug logging
mcpproxy code exec --code="..." --log-level=debug
```

#### Output Format

**Success**:
```json
{
  "ok": true,
  "value": {
    "result": 42
  }
}
```

**Failure**:
```json
{
  "ok": false,
  "error": {
    "code": "RUNTIME_ERROR",
    "message": "Cannot read property 'name' of undefined",
    "stack": "..."
  }
}
```

#### Common CLI Patterns

```bash
# Test simple calculation
mcpproxy code exec --code="({sum: input.a + input.b})" --input='{"a":5,"b":10}'

# Test tool call
mcpproxy code exec \
  --code="var r = call_tool('github','get_user',{username:input.user}); r" \
  --input='{"user":"octocat"}'

# Test error handling
mcpproxy code exec --code="throw new Error('Test error')" 2>&1

# Test timeout
mcpproxy code exec --code="while(true){}" --timeout=1000 2>&1

# Save code to file for complex scripts
cat > /tmp/script.js << 'EOF'
const users = ['octocat', 'torvalds'];
const names = users
  .map(username => call_tool('github', 'get_user', {username}))
  .filter(res => res.ok)
  .map(res => res.result.name);
return {names};
EOF

mcpproxy code exec --file=/tmp/script.js
```

---

## Validation Rules

### Code Validation

- **Required**: `code` parameter must be provided
- **Type**: Must be a string
- **Syntax**: Must be valid JavaScript (ES2020+ supported)
- **Serialization**: Return value must be JSON-serializable

### Input Validation

- **Type**: Must be a valid JSON object
- **Default**: `{}` if not provided
- **Size**: Subject to overall tool response limit

### Options Validation

- **timeout_ms**: Must be between 1 and 600000 (10 minutes)
- **max_tool_calls**: Must be >= 0
- **allowed_servers**: Must be array of strings (server names)

### `call_tools()` Batch Validation

- **requests**: Must be a dense array of at most 100 elements
- **element**: Must be an object with non-empty `server` and `tool` strings; a
  supplied `args` must be an object (omitted = `{}`)
- **options.max_parallel**: Must be an integer between 1 and 32
- A violation returns one `INVALID_ARGS` envelope naming the first offending
  index; no element is dispatched and no budget is consumed

### Return Value Validation

**Valid return values**:
- Primitives: `null`, `true`, `false`, numbers, strings
- Arrays: `[1, 2, 3]`, `["a", "b"]`
- Objects: `{key: "value"}`, `{nested: {object: true}}`

**Invalid return values**:
- Functions: `function() {}`
- Undefined: `undefined`
- Circular references: `var a = {}; a.self = a; return a;`
- Special objects: `new Date()`, `new RegExp()` (return `.toString()` or `.toISOString()` instead)

---

## Performance Considerations

### Pool Size

The pool size determines how many concurrent executions can run simultaneously:

- **Small pool (1-5)**: Sequential execution, low memory usage
- **Medium pool (10-20)**: Balanced for typical workloads
- **Large pool (50-100)**: High concurrency, higher memory usage

**Recommendation**: Start with default (10) and adjust based on load.

### Batch Concurrency (`call_tools`)

`code_execution_max_parallel` (default 8) bounds how many elements of one batch
run at once; `call_tools(requests, {max_parallel})` overrides it per batch
(1-32). A batch of N independent calls costs roughly `ceil(N / max_parallel) ×
slowest-call` instead of the sum of all calls.

**Interaction with per-server limits**: Spec 093 concurrency limits are enforced
inside the call path and are never bypassed by batching. A server with
`max_concurrent_requests: 1` and `queue_size: 9` serializes a 10-element batch;
the same server with **no** `queue_size` sheds the overflow, returning 1 result
and 9 per-slot `queue_full` errors. Give limited servers `queue_size` headroom —
or lower `max_parallel` to match their cap — before fanning out against them.

### Timeout Settings

| Use Case | Recommended Timeout |
|----------|---------------------|
| Quick calculations | 5-10 seconds |
| Single tool call | 30 seconds |
| Multiple tool calls (2-5) | 1-2 minutes (default) |
| Complex workflows (10+ calls) | 3-5 minutes |
| Heavy processing | Up to 10 minutes (max) |

### Tool Call Limits

| Use Case | Recommended Limit |
|----------|-------------------|
| No limit needed | 0 (unlimited) |
| Single tool call | 1-2 |
| Small batch (2-10 items) | 20 |
| Medium batch (10-50 items) | 100 |
| Large batch (50+ items) | 500+ |

---

## Next Steps

- **Examples**: See [examples.md](examples.md) for working code samples
- **Troubleshooting**: See [troubleshooting.md](troubleshooting.md) for common issues
- **Overview**: See [overview.md](overview.md) for architecture and best practices

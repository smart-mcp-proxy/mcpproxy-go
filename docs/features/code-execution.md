---
id: code-execution
title: Code Execution
sidebar_label: Code Execution
sidebar_position: 3
description: Execute JavaScript to orchestrate multiple MCP tools
keywords: [code, execution, javascript, orchestration]
---

# Code Execution

The `code_execution` tool enables orchestrating multiple upstream MCP tools in a single request using sandboxed JavaScript (ES5.1+).

## Overview

Code execution allows AI agents to:

- Chain multiple tool calls together
- Process and transform tool outputs
- Implement complex logic and conditionals
- Reduce round-trip latency

## Configuration

Enable code execution in your config:

```json
{
  "enable_code_execution": true,
  "code_execution_timeout_ms": 120000,
  "code_execution_max_tool_calls": 0,
  "code_execution_pool_size": 10
}
```

| Option | Type | Default | Description |
|--------|------|---------|-------------|
| `enable_code_execution` | boolean | `false` | Enable the code execution tool |
| `code_execution_timeout_ms` | integer | `120000` | Execution timeout (2 minutes) |
| `code_execution_max_tool_calls` | integer | `0` | Max tool calls (0 = unlimited) |
| `code_execution_pool_size` | integer | `10` | Number of VM instances to pool |

## CLI Usage

### Basic Execution

```bash
mcpproxy code exec --code="({ result: input.value * 2 })" --input='{"value": 21}'
```

### Tool Orchestration

```bash
mcpproxy code exec --code="call_tool('github', 'get_user', {username: input.user})" --input='{"user":"octocat"}'
```

## API

### Input Schema

```json
{
  "code": "string (required) - JavaScript code to execute",
  "input": "object (optional) - Input data available as 'input' variable"
}
```

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

Use ES5.1 syntax (no arrow functions, let/const, template literals):

```javascript
// Wrong
const result = () => call_tool('server', 'tool');

// Correct
var result = function() { return call_tool('server', 'tool'); };
```

### Tool Not Found

Verify the server and tool names:

```bash
mcpproxy tools list --server=server-name
```

## Best Practices

1. **Keep code simple**: Complex logic is harder to debug
2. **Handle errors**: Use try/catch for tool calls
3. **Minimize tool calls**: Batch operations when possible
4. **Use logging**: Add log() calls for debugging
5. **Test locally**: Use CLI to test before integrating

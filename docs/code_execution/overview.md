# JavaScript Code Execution - Overview

## What is Code Execution?

The `code_execution` tool enables LLM agents to orchestrate multiple upstream MCP tools in a single request using JavaScript. Instead of making multiple round-trips to the model, you can execute complex multi-step workflows with conditional logic, loops, and data transformations—all within a single execution context.

## When to Use Code Execution

✅ **Use code_execution when:**
- You need to call 2+ tools and combine their results
- You need conditional logic based on tool responses
- You need to transform or aggregate data from multiple sources
- You need to iterate over data and call tools for each item
- You need to handle errors gracefully with fallbacks

❌ **Don't use code_execution when:**
- You're calling a single tool (use `call_tool` directly)
- The workflow is simple and linear (use sequential tool calls)
- You need long-running operations (>2 minutes)
- You need access to filesystem, network, or Node.js modules

## Key Benefits

### 1. Reduced Latency
Execute multiple tool calls in a single request, eliminating the network round-trips between agent and model.

**Before (3 round-trips):**
```
Agent → Model: "Get user data"
Model → Agent: call_tool(github, get_user, {username: "octocat"})
Agent → Model: "Here's the user data"
Model → Agent: call_tool(github, list_repos, {user: "octocat"})
Agent → Model: "Here are the repos"
Model → Agent: call_tool(github, get_repo, {repo: "Hello-World"})
```

**After (1 round-trip):**
```
Agent → Model: "Get user and their repos"
Model → Agent: code_execution({code: "...", input: {...}})
```

### 2. Complex Logic
Implement conditional branching, loops, and error handling that would require multiple model invocations.

```javascript
// Conditional logic
const user = call_tool('github', 'get_user', {username: input.username});
if (!user.ok) {
  return {error: 'User not found'};
}

// Loop with accumulation
const results = [];
for (let i = 0; i < input.repos.length; i++) {
  const repo = call_tool('github', 'get_repo', {name: input.repos[i]});
  if (repo.ok) {
    results.push(repo.result);
  }
}
return {repos: results, count: results.length};
```

### 3. Data Transformation
Transform, filter, and aggregate data from multiple tool calls before returning results.

```javascript
const repos = call_tool('github', 'list_repos', {user: input.username});
if (!repos.ok) return repos;

// Filter and transform
const activeRepos = repos.result.filter(function(r) {
  return !r.archived && r.pushed_at > input.since;
}).map(function(r) {
  return {name: r.name, stars: r.stargazers_count, language: r.language};
});

return {repos: activeRepos, total: activeRepos.length};
```

## How It Works

### Architecture

```
┌─────────────────────────────────────────────┐
│  LLM Agent                                   │
│  - Receives code_execution tool description │
│  - Writes JavaScript to orchestrate tools   │
└────────────┬────────────────────────────────┘
             │
             │ code_execution request
             │ {code, input, options}
             ▼
┌─────────────────────────────────────────────┐
│  MCPProxy Server                             │
│  - Validates request and options            │
│  - Checks if feature is enabled             │
└────────────┬────────────────────────────────┘
             │
             ▼
┌─────────────────────────────────────────────┐
│  JavaScript Runtime Pool                     │
│  - Acquires VM from pool (blocks if full)   │
│  - Creates isolated sandbox                 │
│  - Binds input global and call_tool()       │
└────────────┬────────────────────────────────┘
             │
             ▼
┌─────────────────────────────────────────────┐
│  JavaScript Execution                        │
│  - Runs code with timeout watchdog          │
│  - Enforces max_tool_calls limit            │
│  - Restricts to allowed_servers             │
│  - Returns JSON-serializable result         │
└────────────┬────────────────────────────────┘
             │
             ▼
┌─────────────────────────────────────────────┐
│  Upstream Tool Calls                         │
│  - Forwards call_tool() to upstream servers │
│  - Respects quarantine and security rules   │
│  - Returns {ok, result} or {ok, error}      │
└─────────────────────────────────────────────┘
```

### Execution Flow

1. **Request Parsing**: Extract `code`, `input`, and `options` from the request
2. **Validation**: Verify timeout (1-600000ms) and max_tool_calls (>= 0)
3. **Pool Acquisition**: Acquire a JavaScript VM from the pool (blocks if all VMs are in use)
4. **Sandbox Setup**: Create isolated environment with `input` global and `call_tool()` function
5. **Execution**: Run JavaScript with timeout enforcement and tool call tracking
6. **Result Extraction**: Validate result is JSON-serializable and return structured response
7. **Pool Release**: Return VM to pool for reuse
8. **Response**: Return `{ok: true, value: <result>}` or `{ok: false, error: {...}}`

## Security Model

### Sandbox Restrictions

The JavaScript execution environment is **heavily sandboxed** to prevent security issues:

❌ **Not Available:**
- `require()` - No module loading
- `setTimeout()` / `setInterval()` - No timers
- Filesystem access - No `fs` module
- Network access - No `http` or `fetch`
- Environment variables - No `process.env`
- Node.js built-ins - ES5.1+ standard library only

✅ **Available:**
- `input` - Global variable with request input data
- `call_tool(serverName, toolName, args)` - Function to call upstream MCP tools
- ES5.1+ standard library (Array, Object, String, Math, Date, JSON, etc.)

### Configuration & Limits

```json
{
  "enable_code_execution": false,           // Must be explicitly enabled (default: false)
  "code_execution_timeout_ms": 120000,      // Default: 2 minutes, max: 10 minutes
  "code_execution_max_tool_calls": 0,       // Default: unlimited
  "code_execution_pool_size": 10            // Default: 10 concurrent VMs
}
```

**Per-Request Overrides:**
```javascript
{
  "code": "...",
  "input": {...},
  "options": {
    "timeout_ms": 60000,              // Override timeout for this request
    "max_tool_calls": 20,             // Limit tool calls for this request
    "allowed_servers": ["github"]     // Restrict to specific servers
  }
}
```

### Quarantine Integration

Code execution **respects existing MCPProxy security features**:
- Quarantined servers cannot be called via `call_tool()`
- Server enable/disable settings are enforced
- Authentication requirements are preserved

## Getting Started

### 1. Enable the Feature

Edit your configuration file (`~/.mcpproxy/mcp_config.json`):

```json
{
  "enable_code_execution": true,
  "mcpServers": [
    {
      "name": "github",
      "url": "https://api.github.com/mcp",
      "protocol": "http",
      "enabled": true
    }
  ]
}
```

### 2. Restart MCPProxy

```bash
pkill mcpproxy
mcpproxy serve
```

### 3. Test with CLI

```bash
# Simple test
mcpproxy code exec --code="({ result: input.value * 2 })" --input='{"value": 21}'

# Call upstream tool
mcpproxy code exec --code="call_tool('github', 'get_user', {username: input.user})" --input='{"user":"octocat"}'
```

### 4. Use from LLM Agent

The `code_execution` tool will appear in the tools list when an LLM agent connects to MCPProxy:

```json
{
  "name": "code_execution",
  "description": "Execute JavaScript code that orchestrates multiple upstream MCP tools...",
  "inputSchema": {
    "type": "object",
    "properties": {
      "code": {"type": "string", "description": "JavaScript source code..."},
      "input": {"type": "object", "description": "Input data accessible as global input variable..."},
      "options": {"type": "object", "description": "Execution options..."}
    },
    "required": ["code"]
  }
}
```

## Common Patterns

### Pattern 1: Sequential Tool Calls

```javascript
// Fetch user, then fetch their repos
const userRes = call_tool('github', 'get_user', {username: input.username});
if (!userRes.ok) {
  return {error: userRes.error.message};
}

const reposRes = call_tool('github', 'list_repos', {user: input.username});
if (!reposRes.ok) {
  return {error: reposRes.error.message};
}

return {
  user: userRes.result,
  repos: reposRes.result,
  repo_count: reposRes.result.length
};
```

### Pattern 2: Conditional Logic

```javascript
// Try primary server, fallback to secondary
var result = call_tool('primary-db', 'query', {sql: input.query});

if (!result.ok) {
  // Primary failed, try backup
  result = call_tool('backup-db', 'query', {sql: input.query});
}

return result.ok ? result.result : {error: 'Both databases unavailable'};
```

### Pattern 3: Loop with Aggregation

```javascript
// Fetch details for multiple items
var results = [];
var errors = [];

for (var i = 0; i < input.ids.length; i++) {
  var res = call_tool('api-server', 'get_item', {id: input.ids[i]});

  if (res.ok) {
    results.push(res.result);
  } else {
    errors.push({id: input.ids[i], error: res.error});
  }
}

return {
  success: results,
  failed: errors,
  success_count: results.length,
  error_count: errors.length
};
```

### Pattern 4: Data Transformation

```javascript
// Get repos and compute statistics
var reposRes = call_tool('github', 'list_repos', {user: input.username});
if (!reposRes.ok) return reposRes;

var repos = reposRes.result;
var totalStars = 0;
var languages = {};

for (var i = 0; i < repos.length; i++) {
  totalStars += repos[i].stargazers_count || 0;

  var lang = repos[i].language || 'Unknown';
  languages[lang] = (languages[lang] || 0) + 1;
}

return {
  total_repos: repos.length,
  total_stars: totalStars,
  avg_stars: Math.round(totalStars / repos.length),
  languages: languages
};
```

## Error Handling

### JavaScript Errors

```javascript
// Syntax error - caught before execution
code_execution({code: "invalid javascript {"})
// Returns: {ok: false, error: {code: "SYNTAX_ERROR", message: "...", stack: "..."}}

// Runtime error - caught during execution
code_execution({code: "throw new Error('Something went wrong')"})
// Returns: {ok: false, error: {code: "RUNTIME_ERROR", message: "Something went wrong", stack: "..."}}
```

### Tool Call Errors

```javascript
// Tool returns error - handled in JavaScript
var res = call_tool('github', 'get_user', {username: 'nonexistent-user-12345'});
if (!res.ok) {
  return {error: 'User not found: ' + res.error.message};
}
return res.result;
```

### Timeout Errors

```javascript
// Execution exceeds timeout (default: 2 minutes)
code_execution({
  code: "while(true) {}",  // Infinite loop
  options: {timeout_ms: 1000}
})
// Returns: {ok: false, error: {code: "TIMEOUT", message: "JavaScript execution timed out"}}
```

### Max Tool Calls Exceeded

```javascript
// Exceeds max_tool_calls limit
code_execution({
  code: "for (var i = 0; i < 100; i++) { call_tool('api', 'ping', {}); }",
  options: {max_tool_calls: 10}
})
// Returns: {ok: false, error: {code: "MAX_TOOL_CALLS_EXCEEDED", message: "..."}}
```

## Best Practices

### 1. Keep Code Simple
- Use ES5.1 syntax (no arrow functions, template literals, or async/await)
- Avoid deeply nested logic
- Prefer explicit error handling over implicit failures

### 2. Handle Errors Gracefully
```javascript
// Bad: Assumes success
var user = call_tool('github', 'get_user', {username: input.username});
return user.result.name;  // Crashes if user.ok is false

// Good: Checks response
var user = call_tool('github', 'get_user', {username: input.username});
if (!user.ok) {
  return {error: user.error.message};
}
return {name: user.result.name};
```

### 3. Set Appropriate Timeouts
```javascript
// Quick operations: 30 seconds
{options: {timeout_ms: 30000}}

// Multiple tool calls: 2 minutes (default)
{options: {timeout_ms: 120000}}

// Heavy processing: 5 minutes
{options: {timeout_ms: 300000}}
```

### 4. Limit Tool Calls
```javascript
// Protect against runaway loops
{
  code: "for (var i = 0; i < input.items.length; i++) { ... }",
  options: {max_tool_calls: 100}
}
```

### 5. Use Allowed Servers
```javascript
// Restrict to specific servers for sensitive operations
{
  code: "call_tool('production-db', 'delete', {id: input.id})",
  options: {allowed_servers: ["production-db"]}
}
```

## Next Steps

- **Examples**: See [examples.md](examples.md) for 10+ working code samples
- **API Reference**: See [api-reference.md](api-reference.md) for complete schema documentation
- **Troubleshooting**: See [troubleshooting.md](troubleshooting.md) for common issues and solutions
- **CLI Usage**: Run `mcpproxy code exec --help` for command-line testing

# JavaScript Code Execution - Troubleshooting

Common issues, error messages, and solutions for the `code_execution` tool.

## Table of Contents

1. [Configuration Issues](#configuration-issues)
2. [Syntax Errors](#syntax-errors)
3. [Runtime Errors](#runtime-errors)
4. [Timeout Issues](#timeout-issues)
5. [Tool Call Errors](#tool-call-errors)
6. [Serialization Errors](#serialization-errors)
7. [Performance Issues](#performance-issues)
8. [Debugging Tips](#debugging-tips)

---

## Configuration Issues

### Error: "code_execution is disabled in configuration"

**Symptom**:
```
Error: code_execution is disabled in configuration. Set 'enable_code_execution: true' in config file.
```

**Cause**: The `code_execution` feature is disabled by default for security.

**Solution**:
1. Edit your configuration file (`~/.mcpproxy/mcp_config.json`)
2. Add or update: `"enable_code_execution": true`
3. Restart mcpproxy: `pkill mcpproxy && mcpproxy serve`

**Example Configuration**:
```json
{
  "enable_code_execution": true,
  "code_execution_timeout_ms": 120000,
  "code_execution_max_tool_calls": 0,
  "code_execution_pool_size": 10,
  "mcpServers": [...]
}
```

---

### Error: "code_execution tool not found in tools list"

**Symptom**: LLM agent lists available tools but `code_execution` is missing.

**Cause**: Feature is not enabled or server didn't restart after configuration change.

**Solution**:
1. Verify configuration has `"enable_code_execution": true`
2. Restart mcpproxy server
3. Reconnect LLM client
4. List tools again

**Verification**:
```bash
# Check if code_execution tool is registered
mcpproxy call tool --tool-name=retrieve_tools --json_args='{"query":"code execution"}'
```

---

### Error: "timeout must be between 1 and 600000 milliseconds"

**Symptom**:
```json
{
  "ok": false,
  "error": {
    "message": "timeout_ms must be between 1 and 600000 milliseconds"
  }
}
```

**Cause**: Invalid `timeout_ms` value in request options.

**Solution**: Use a timeout between 1ms and 600000ms (10 minutes):
```json
{
  "code": "...",
  "options": {
    "timeout_ms": 120000  // 2 minutes (valid)
  }
}
```

---

## Syntax Errors

### Error: "SyntaxError: Unexpected token"

**Symptom**:
```json
{
  "ok": false,
  "error": {
    "code": "SYNTAX_ERROR",
    "message": "SyntaxError: Unexpected token '{'",
    "stack": ""
  }
}
```

**Common Causes**:
1. Missing closing brackets/braces
2. Invalid JavaScript syntax
3. Using ES6+ features (not supported)

**Solutions**:

**Problem**: Missing brackets
```javascript
// Bad
var x = { value: 1, missing: 2

// Good
var x = { value: 1, missing: 2 };
```

**Problem**: ES6 arrow functions
```javascript
// Bad (ES6)
var doubled = arr.map(x => x * 2);

// Good (ES5)
var doubled = arr.map(function(x) { return x * 2; });
```

**Problem**: Template literals
```javascript
// Bad (ES6)
var msg = `Hello, ${name}!`;

// Good (ES5)
var msg = 'Hello, ' + name + '!';
```

**Problem**: Const/let
```javascript
// Bad (ES6)
const x = 42;
let y = 10;

// Good (ES5)
var x = 42;
var y = 10;
```

---

### Error: "SyntaxError: Unexpected end of input"

**Symptom**: Code execution fails with "end of input" error.

**Cause**: Incomplete JavaScript expression or statement.

**Solution**: Ensure all blocks are properly closed:
```javascript
// Bad
var x = function() {
  return 42;
// Missing closing brace

// Good
var x = function() {
  return 42;
};
```

---

## Runtime Errors

### Error: "TypeError: Cannot read property 'X' of undefined/null"

**Symptom**:
```json
{
  "ok": false,
  "error": {
    "code": "RUNTIME_ERROR",
    "message": "TypeError: Cannot read property 'name' of undefined",
    "stack": "..."
  }
}
```

**Cause**: Accessing properties on `undefined` or `null` values.

**Solution**: Always check values before accessing properties:

```javascript
// Bad
var name = input.user.name;  // Crashes if input.user is undefined

// Good
var name = input.user ? input.user.name : 'Unknown';

// Better
var name = 'Unknown';
if (input && input.user && input.user.name) {
  name = input.user.name;
}
```

**For Tool Calls**:
```javascript
// Bad
var user = call_tool('github', 'get_user', {username: input.username});
var name = user.result.name;  // Crashes if user.ok is false

// Good
var user = call_tool('github', 'get_user', {username: input.username});
if (!user.ok) {
  return {error: user.error.message};
}
var name = user.result.name;
```

---

### Error: "ReferenceError: X is not defined"

**Symptom**:
```json
{
  "ok": false,
  "error": {
    "code": "RUNTIME_ERROR",
    "message": "ReferenceError: myVariable is not defined",
    "stack": "..."
  }
}
```

**Cause**: Using variables or functions that don't exist.

**Common Mistakes**:

**Problem**: Undefined variables
```javascript
// Bad
return result;  // 'result' was never declared

// Good
var result = 42;
return result;
```

**Problem**: Using unavailable functions
```javascript
// Bad
var data = require('fs').readFileSync('file.txt');  // require() not available

// Bad
setTimeout(function() { doWork(); }, 1000);  // setTimeout() not available

// Good
var data = call_tool('filesystem', 'read_file', {path: 'file.txt'});
```

---

### Error: "RangeError: Maximum call stack size exceeded"

**Symptom**: Execution fails with stack overflow.

**Cause**: Infinite recursion or very deep recursion.

**Solution**: Check for infinite loops or recursive calls:

```javascript
// Bad
function factorial(n) {
  return n * factorial(n - 1);  // No base case!
}

// Good
function factorial(n) {
  if (n <= 1) return 1;  // Base case
  return n * factorial(n - 1);
}

// Better (iterative)
function factorial(n) {
  var result = 1;
  for (var i = 2; i <= n; i++) {
    result *= i;
  }
  return result;
}
```

---

## Timeout Issues

### Error: "JavaScript execution timed out"

**Symptom**:
```json
{
  "ok": false,
  "error": {
    "code": "TIMEOUT",
    "message": "JavaScript execution timed out",
    "stack": ""
  }
}
```

**Common Causes**:
1. Infinite loops
2. Too many tool calls
3. Slow upstream servers
4. Heavy computation

**Solutions**:

**Problem**: Infinite loop
```javascript
// Bad
while (true) {
  // This will timeout
}

// Bad
for (var i = 0; i < 1000000000; i++) {
  // This takes too long
}

// Good
for (var i = 0; i < input.items.length; i++) {
  // Bounded loop
}
```

**Problem**: Insufficient timeout for workload
```javascript
// Bad: Default 2-minute timeout for 100 tool calls
{
  "code": "for(var i=0;i<100;i++){call_tool(...)}",
  "options": {"timeout_ms": 120000}  // Might not be enough
}

// Good: Increase timeout for heavy workloads
{
  "code": "for(var i=0;i<100;i++){call_tool(...)}",
  "options": {"timeout_ms": 300000}  // 5 minutes
}
```

**Problem**: Slow upstream tools
```javascript
// Solution: Reduce number of calls or increase timeout
{
  "code": "...",
  "options": {
    "timeout_ms": 300000,      // Increase timeout
    "max_tool_calls": 50       // Limit calls to prevent runaway
  }
}
```

---

## Tool Call Errors

### Error: "Exceeded maximum tool calls limit"

**Symptom**:
```json
{
  "ok": false,
  "error": {
    "code": "MAX_TOOL_CALLS_EXCEEDED",
    "message": "Exceeded maximum tool calls limit (10)",
    "stack": ""
  }
}
```

**Cause**: Code called `call_tool()` more times than `max_tool_calls` allows.

**Solution**:

**Option 1**: Increase limit
```json
{
  "code": "for(var i=0;i<20;i++){call_tool(...)}",
  "options": {"max_tool_calls": 25}  // Set higher than needed
}
```

**Option 2**: Reduce tool calls
```javascript
// Bad: Call tool for every item
for (var i = 0; i < 1000; i++) {
  call_tool('api', 'process', {id: items[i]});
}

// Good: Batch items if tool supports it
var batchSize = 100;
for (var i = 0; i < items.length; i += batchSize) {
  var batch = items.slice(i, i + batchSize);
  call_tool('api', 'process_batch', {items: batch});
}
```

---

### Error: "Server 'X' is not in the allowed servers list"

**Symptom**:
```json
{
  "ok": false,
  "error": {
    "code": "SERVER_NOT_ALLOWED",
    "message": "Server 'gitlab' is not in the allowed servers list",
    "stack": ""
  }
}
```

**Cause**: Attempted to call a server not in `allowed_servers` option.

**Solution**:

**Option 1**: Add server to allowed list
```json
{
  "code": "call_tool('gitlab', 'get_user', {username: 'test'})",
  "options": {
    "allowed_servers": ["github", "gitlab"]  // Add gitlab
  }
}
```

**Option 2**: Remove restriction (allow all servers)
```json
{
  "code": "call_tool('gitlab', 'get_user', {username: 'test'})",
  "options": {
    "allowed_servers": []  // Empty array = all servers allowed
  }
}
```

---

### Error: "server not found: X"

**Symptom**: `call_tool()` returns error saying server doesn't exist.

**Cause**: Server name is incorrect or server is not configured.

**Solution**:

1. **Check server name**: Verify spelling and case
```javascript
// Bad
call_tool('GitHub', 'get_user', {});  // Wrong case

// Good
call_tool('github', 'get_user', {});  // Correct name
```

2. **List available servers**:
```bash
mcpproxy call tool --tool-name=upstream_servers --json_args='{"operation":"list"}'
```

3. **Add server if missing**:
```bash
mcpproxy call tool --tool-name=upstream_servers \
  --json_args='{"operation":"add","name":"github","url":"https://api.github.com/mcp","protocol":"http","enabled":true}'
```

---

## Serialization Errors

### Error: "Result contains non-JSON-serializable values"

**Symptom**:
```json
{
  "ok": false,
  "error": {
    "code": "SERIALIZATION_ERROR",
    "message": "Result contains non-JSON-serializable values (functions, circular references, etc.)",
    "stack": ""
  }
}
```

**Cause**: JavaScript return value contains functions, circular references, or other non-JSON types.

**Solutions**:

**Problem**: Returning functions
```javascript
// Bad
return {
  calculate: function() { return 42; }
};

// Good
return {
  result: 42
};
```

**Problem**: Returning undefined
```javascript
// Bad
return undefined;  // Not JSON-serializable

// Good
return null;  // JSON-serializable
```

**Problem**: Circular references
```javascript
// Bad
var obj = {value: 42};
obj.self = obj;  // Circular reference
return obj;

// Good
return {value: 42};
```

**Problem**: Special objects (Date, RegExp)
```javascript
// Bad
return {
  created: new Date()  // Date object not JSON-serializable
};

// Good
return {
  created: new Date().toISOString()  // String is JSON-serializable
};
```

---

## Performance Issues

### Issue: Slow execution times

**Symptom**: Code takes longer than expected to execute.

**Diagnosis**:
1. Check number of tool calls
2. Measure upstream tool latency
3. Look for inefficient loops
4. Check for excessive data processing

**Solutions**:

**Reduce tool calls**:
```javascript
// Bad: N tool calls
for (var i = 0; i < items.length; i++) {
  call_tool('api', 'get', {id: items[i]});
}

// Good: 1 batch tool call
call_tool('api', 'get_batch', {ids: items});
```

**Cache repeated calls**:
```javascript
// Bad: Call same tool multiple times
for (var i = 0; i < items.length; i++) {
  var config = call_tool('api', 'get_config', {});  // Repeated call
  process(items[i], config);
}

// Good: Call once, reuse result
var config = call_tool('api', 'get_config', {});
if (!config.ok) return config;

for (var i = 0; i < items.length; i++) {
  process(items[i], config.result);
}
```

**Optimize loops**:
```javascript
// Bad: Inefficient nested loops
for (var i = 0; i < arr1.length; i++) {
  for (var j = 0; j < arr2.length; j++) {
    if (arr1[i] === arr2[j]) {
      results.push(arr1[i]);
    }
  }
}

// Good: Use object for O(1) lookup
var set = {};
for (var i = 0; i < arr2.length; i++) {
  set[arr2[i]] = true;
}
for (var i = 0; i < arr1.length; i++) {
  if (set[arr1[i]]) {
    results.push(arr1[i]);
  }
}
```

---

### Issue: Pool exhaustion (all VMs busy)

**Symptom**: Requests take longer to start, or you see "waiting for VM" logs.

**Cause**: More concurrent executions than pool size allows.

**Solution**: Increase pool size in configuration:
```json
{
  "code_execution_pool_size": 20  // Increase from default 10
}
```

**Trade-offs**:
- Larger pool = more concurrency, more memory usage
- Smaller pool = less concurrency, less memory usage

**Recommendation**: Monitor pool usage and adjust based on load.

---

## Debugging Tips

### Enable Debug Logging

**CLI**:
```bash
mcpproxy code exec --code="..." --log-level=debug
```

**Server**: Edit config to set log level:
```json
{
  "log_level": "debug"
}
```

**What you'll see**:
- Tool call details (server, tool name, arguments)
- Execution timing
- Pool acquisition/release
- Detailed error messages

---

### Use console.log() for Debugging

```javascript
var user = call_tool('github', 'get_user', {username: input.username});
console.log('User response:', JSON.stringify(user));  // Logs to server logs

if (!user.ok) {
  console.log('Error occurred:', user.error);
  return {error: user.error.message};
}

console.log('User name:', user.result.name);
return {name: user.result.name};
```

**Where to find logs**: `~/.mcpproxy/logs/main.log` (or platform-specific log directory)

---

### Test Code Incrementally

Start simple and build up:

```javascript
// Step 1: Verify input access
return input;

// Step 2: Test tool call
var res = call_tool('github', 'get_user', {username: 'octocat'});
return res;

// Step 3: Add error handling
var res = call_tool('github', 'get_user', {username: 'octocat'});
if (!res.ok) return {error: res.error.message};
return res.result;

// Step 4: Add data transformation
var res = call_tool('github', 'get_user', {username: 'octocat'});
if (!res.ok) return {error: res.error.message};
return {
  name: res.result.name,
  repos: res.result.public_repos
};
```

---

### Validate JSON Serialization

Test that your return value is JSON-serializable:

```javascript
var result = {/* your data */};

// This will throw if result is not serializable
var json = JSON.stringify(result);

// If it succeeds, return it
return result;
```

---

### Use Error Boundaries

Wrap risky operations in try-catch (though not available in ES5, use checks instead):

```javascript
// Check before accessing
if (res && res.ok && res.result && res.result.name) {
  return {name: res.result.name};
} else {
  return {error: 'Invalid response structure'};
}

// For loops, check each iteration
for (var i = 0; i < items.length; i++) {
  if (!items[i]) {
    console.log('Skipping null item at index', i);
    continue;
  }
  // Process items[i]
}
```

---

### Reproduce Issues Locally

Use the CLI to reproduce issues quickly:

```bash
# Save problematic code to file
cat > /tmp/debug.js << 'EOF'
var user = call_tool('github', 'get_user', {username: input.username});
console.log('Response:', JSON.stringify(user));
return user;
EOF

# Run with debug logging
mcpproxy code exec \
  --file=/tmp/debug.js \
  --input='{"username":"octocat"}' \
  --log-level=debug
```

---

## Getting Help

If you're still stuck after trying these solutions:

1. **Check the examples**: [examples.md](examples.md) has 10+ working patterns
2. **Review API reference**: [api-reference.md](api-reference.md) has complete schema
3. **Read the overview**: [overview.md](overview.md) explains architecture
4. **Check server logs**: `~/.mcpproxy/logs/main.log` for detailed error messages
5. **File an issue**: [GitHub Issues](https://github.com/smart-mcp-proxy/mcpproxy-go/issues)

### What to Include in Bug Reports

```markdown
**Environment**:
- MCPProxy version: (run `mcpproxy --version`)
- OS: (macOS/Linux/Windows)
- Configuration: (relevant config fields)

**Code**:
```javascript
// Your JavaScript code here
```

**Input**:
```json
{
  "input": {...}
}
```

**Expected**: What you expected to happen

**Actual**: What actually happened (include full error message)

**Logs**: Relevant lines from `~/.mcpproxy/logs/main.log`
```

---

## Next Steps

- **Examples**: See [examples.md](examples.md) for working code samples
- **API Reference**: See [api-reference.md](api-reference.md) for complete schema
- **Overview**: See [overview.md](overview.md) for architecture and best practices

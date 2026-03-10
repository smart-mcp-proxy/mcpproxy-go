# JavaScript Code Execution - Examples

This guide provides working examples demonstrating common use cases for the `code_execution` tool.

## Table of Contents

1. [Basic Examples](#basic-examples)
2. [Multi-Tool Composition](#multi-tool-composition)
3. [Error Handling](#error-handling)
4. [Loops and Iteration](#loops-and-iteration)
5. [Conditional Logic](#conditional-logic)
6. [Data Transformation](#data-transformation)
7. [Partial Results](#partial-results)
8. [Advanced Patterns](#advanced-patterns)

---

## Basic Examples

### Example 1: Simple Calculation

**Use Case**: Transform input data without calling any tools.

```javascript
// Request
{
  "code": "({ result: input.value * 2, original: input.value })",
  "input": {"value": 21}
}

// Response
{
  "ok": true,
  "value": {
    "result": 42,
    "original": 21
  }
}
```

**CLI Command**:
```bash
mcpproxy code exec \
  --code="({ result: input.value * 2, original: input.value })" \
  --input='{"value": 21}'
```

---

### Example 2: Single Tool Call

**Use Case**: Call a single upstream tool and return its result.

```javascript
// Request
{
  "code": "call_tool('github', 'get_user', {username: input.username})",
  "input": {"username": "octocat"}
}

// Response
{
  "ok": true,
  "value": {
    "ok": true,
    "result": {
      "login": "octocat",
      "id": 583231,
      "name": "The Octocat",
      "public_repos": 8
    }
  }
}
```

**CLI Command**:
```bash
mcpproxy code exec \
  --code="call_tool('github', 'get_user', {username: input.username})" \
  --input='{"username": "octocat"}'
```

---

## Multi-Tool Composition

### Example 3: Sequential Tool Calls

**Use Case**: Fetch user data, then fetch their repositories.

```javascript
// Request
{
  "code": `
const userRes = call_tool('github', 'get_user', {username: input.username});
if (!userRes.ok) {
  return {error: 'Failed to get user: ' + userRes.error.message};
}

const reposRes = call_tool('github', 'list_repos', {user: input.username, limit: 5});
if (!reposRes.ok) {
  return {error: 'Failed to get repos: ' + reposRes.error.message};
}

const { name, login, public_repos } = userRes.result;

return {
  user: { name, login, public_repos },
  repos: reposRes.result.map(r => ({name: r.name, stars: r.stargazers_count})),
  total_repos: public_repos
};
  `,
  "input": {"username": "octocat"}
}

// Response
{
  "ok": true,
  "value": {
    "user": {
      "name": "The Octocat",
      "login": "octocat",
      "public_repos": 8
    },
    "repos": [
      {"name": "Hello-World", "stars": 2145},
      {"name": "Spoon-Knife", "stars": 12345},
      ...
    ],
    "total_repos": 8
  }
}
```

**CLI Command**:
```bash
# Save to file for readability
cat > /tmp/github_user_repos.js << 'EOF'
const userRes = call_tool('github', 'get_user', {username: input.username});
if (!userRes.ok) {
  return {error: `Failed to get user: ${userRes.error.message}`};
}

const reposRes = call_tool('github', 'list_repos', {user: input.username, limit: 5});
if (!reposRes.ok) {
  return {error: `Failed to get repos: ${reposRes.error.message}`};
}

const { name, login, public_repos } = userRes.result;

return {
  user: { name, login, public_repos },
  repos: reposRes.result.map(r => ({name: r.name, stars: r.stargazers_count})),
  total_repos: public_repos
};
EOF

mcpproxy code exec --file=/tmp/github_user_repos.js --input='{"username": "octocat"}'
```

---

### Example 4: Parallel-Style Aggregation

**Use Case**: Fetch data from multiple sources and combine results.

```javascript
// Request
{
  "code": `
const sources = ['github', 'gitlab', 'bitbucket'];
const profiles = {};
const availableOn = [];

for (const source of sources) {
  const res = call_tool(source, 'get_user', {username: input.username});
  profiles[source] = res.ok ? res.result : null;
  if (res.ok) availableOn.push(source);
}

return {
  username: input.username,
  available_on: availableOn,
  profiles
};
  `,
  "input": {"username": "johndoe"}
}
```

---

## Error Handling

### Example 5: Graceful Error Handling

**Use Case**: Handle tool call failures and return meaningful error messages.

```javascript
// Request
{
  "code": `
const res = call_tool('github', 'get_user', {username: input.username});

if (!res.ok) {
  // Return structured error with details
  return {
    success: false,
    error: {
      type: 'USER_NOT_FOUND',
      message: res.error.message,
      username: input.username
    }
  };
}

const { name, login, bio } = res.result;
return { success: true, data: { name, login, bio } };
  `,
  "input": {"username": "this-user-definitely-does-not-exist-12345"}
}

// Response (on error)
{
  "ok": true,
  "value": {
    "success": false,
    "error": {
      "type": "USER_NOT_FOUND",
      "message": "Not Found",
      "username": "this-user-definitely-does-not-exist-12345"
    }
  }
}
```

---

### Example 6: Fallback Strategy

**Use Case**: Try primary source, fallback to secondary if it fails.

```javascript
// Request
{
  "code": `
// Try primary database
var result = call_tool('primary-db', 'query', {
  sql: input.query,
  params: input.params
});

if (result.ok) {
  return {
    source: 'primary',
    data: result.result,
    timestamp: Date.now()
  };
}

// Primary failed, try replica
result = call_tool('replica-db', 'query', {
  sql: input.query,
  params: input.params
});

if (result.ok) {
  return {
    source: 'replica',
    data: result.result,
    timestamp: Date.now(),
    warning: 'Primary database unavailable, used replica'
  };
}

// Both failed
return {
  error: 'All databases unavailable',
  primary_error: result.error.message
};
  `,
  "input": {
    "query": "SELECT * FROM users WHERE id = ?",
    "params": [123]
  }
}
```

---

## Loops and Iteration

### Example 7: Batch Processing

**Use Case**: Fetch details for multiple items in a loop.

```javascript
// Request
{
  "code": `
const results = [];
const errors = [];

for (const repoName of input.repo_names) {
  const res = call_tool('github', 'get_repo', {
    owner: input.owner,
    repo: repoName
  });

  if (res.ok) {
    const { stargazers_count: stars, forks_count: forks, language } = res.result;
    results.push({ name: repoName, stars, forks, language });
  } else {
    errors.push({ name: repoName, error: res.error.message });
  }
}

return {
  success: results,
  failed: errors,
  success_count: results.length,
  error_count: errors.length
};
  `,
  "input": {
    "owner": "octocat",
    "repo_names": ["Hello-World", "Spoon-Knife", "nonexistent-repo"]
  },
  "options": {
    "max_tool_calls": 10
  }
}

// Response
{
  "ok": true,
  "value": {
    "success": [
      {"name": "Hello-World", "stars": 2145, "forks": 892, "language": "JavaScript"},
      {"name": "Spoon-Knife", "stars": 12345, "forks": 5678, "language": "HTML"}
    ],
    "failed": [
      {"name": "nonexistent-repo", "error": "Not Found"}
    ],
    "success_count": 2,
    "error_count": 1
  }
}
```

**CLI Command**:
```bash
cat > /tmp/batch_repos.js << 'EOF'
const results = [];
const errors = [];

for (const repoName of input.repo_names) {
  const res = call_tool('github', 'get_repo', {
    owner: input.owner,
    repo: repoName
  });

  if (res.ok) {
    const { stargazers_count: stars, forks_count: forks } = res.result;
    results.push({ name: repoName, stars, forks });
  } else {
    errors.push({name: repoName, error: res.error.message});
  }
}

return {success: results, failed: errors};
EOF

mcpproxy code exec \
  --file=/tmp/batch_repos.js \
  --input='{"owner":"octocat","repo_names":["Hello-World","Spoon-Knife"]}' \
  --max-tool-calls=10
```

---

## Conditional Logic

### Example 8: Dynamic Tool Selection

**Use Case**: Choose which tool to call based on input conditions.

```javascript
// Request
{
  "code": `
var toolName;
var args;

if (input.type === 'user') {
  toolName = 'get_user';
  args = {username: input.identifier};
} else if (input.type === 'repo') {
  toolName = 'get_repo';
  args = {owner: input.owner, repo: input.identifier};
} else if (input.type === 'org') {
  toolName = 'get_org';
  args = {org: input.identifier};
} else {
  return {error: 'Unknown type: ' + input.type};
}

var res = call_tool('github', toolName, args);

if (!res.ok) {
  return {error: res.error.message, type: input.type};
}

return {
  type: input.type,
  data: res.result,
  tool_used: toolName
};
  `,
  "input": {
    "type": "user",
    "identifier": "octocat"
  }
}
```

---

## Data Transformation

### Example 9: Aggregation and Statistics

**Use Case**: Fetch data and compute statistics.

```javascript
// Request
{
  "code": `
const reposRes = call_tool('github', 'list_repos', {
  user: input.username,
  limit: 100
});

if (!reposRes.ok) {
  return {error: reposRes.error.message};
}

const repos = reposRes.result;
const totalStars = repos.reduce((sum, r) => sum + (r.stargazers_count ?? 0), 0);
const totalForks = repos.reduce((sum, r) => sum + (r.forks_count ?? 0), 0);
const languages = {};

let activeRepos = 0;
for (const repo of repos) {
  const lang = repo.language ?? 'Unknown';
  languages[lang] = (languages[lang] ?? 0) + 1;

  if (!repo.archived && repo.pushed_at) {
    activeRepos++;
  }
}

const sorted = [...repos].sort((a, b) =>
  (b.stargazers_count ?? 0) - (a.stargazers_count ?? 0)
);

return {
  username: input.username,
  total_repos: repos.length,
  active_repos: activeRepos,
  archived_repos: repos.length - activeRepos,
  total_stars: totalStars,
  total_forks: totalForks,
  avg_stars: repos.length > 0 ? Math.round(totalStars / repos.length) : 0,
  languages,
  most_popular_repo: sorted[0]?.name ?? 'N/A'
};
  `,
  "input": {"username": "octocat"}
}

// Response
{
  "ok": true,
  "value": {
    "username": "octocat",
    "total_repos": 8,
    "active_repos": 6,
    "archived_repos": 2,
    "total_stars": 15234,
    "total_forks": 8123,
    "avg_stars": 1904,
    "languages": {
      "JavaScript": 3,
      "Python": 2,
      "Go": 1,
      "HTML": 1,
      "Unknown": 1
    },
    "most_popular_repo": "Spoon-Knife"
  }
}
```

---

### Example 10: Filtering and Mapping

**Use Case**: Filter and transform data from tool results.

```javascript
// Request
{
  "code": `
const reposRes = call_tool('github', 'list_repos', {
  user: input.username,
  limit: 50
});

if (!reposRes.ok) {
  return {error: reposRes.error.message};
}

// Filter: only non-archived repos updated recently
const cutoffDate = new Date(Date.now() - 90 * 24 * 60 * 60 * 1000); // 90 days ago

const activeRepos = reposRes.result.filter(repo =>
  !repo.archived && repo.pushed_at && new Date(repo.pushed_at) > cutoffDate
);

// Map, transform, and sort by stars descending
const simplified = activeRepos
  .map(repo => ({
    name: repo.name,
    description: repo.description ?? 'No description',
    language: repo.language ?? 'Unknown',
    stars: repo.stargazers_count,
    last_updated: repo.pushed_at
  }))
  .sort((a, b) => b.stars - a.stars)
  .slice(0, 10); // Top 10

return {
  repos: simplified,
  total_active: activeRepos.length,
  total_repos: reposRes.result.length
};
  `,
  "input": {"username": "octocat"}
}
```

---

## Partial Results

### Example 11: Continue on Error

**Use Case**: Collect all successful results even if some calls fail.

```javascript
// Request
{
  "code": `
const users = ['octocat', 'torvalds', 'nonexistent-user-xyz'];
const successful = [];
const failed = [];

for (const username of users) {
  const res = call_tool('github', 'get_user', {username});

  if (res.ok) {
    const { name, public_repos } = res.result;
    successful.push({ username, name, public_repos });
  } else {
    failed.push({ username, reason: res.error.message });
  }
}

return {
  successful,
  failed,
  summary: {
    success_count: successful.length,
    failure_count: failed.length,
    total: users.length
  }
};
  `,
  "input": {},
  "options": {"max_tool_calls": 5}
}

// Response
{
  "ok": true,
  "value": {
    "successful": [
      {"username": "octocat", "name": "The Octocat", "public_repos": 8},
      {"username": "torvalds", "name": "Linus Torvalds", "public_repos": 5}
    ],
    "failed": [
      {"username": "nonexistent-user-xyz", "reason": "Not Found"}
    ],
    "summary": {
      "success_count": 2,
      "failure_count": 1,
      "total": 3
    }
  }
}
```

---

## Advanced Patterns

### Example 12: Rate Limiting with Delays

**Use Case**: Add delays between tool calls (simulated via counting).

```javascript
// Request
{
  "code": `
const { items } = input;
const results = [];

for (const item of items) {
  // Simulate delay by doing some computation
  for (let j = 0; j < 1000000; j++) {
    // Busy wait
  }

  const res = call_tool('api-server', 'process_item', { id: item });

  if (res.ok) {
    results.push(res.result);
  }
}

return {processed: results, count: results.length};
  `,
  "input": {"items": [1, 2, 3]},
  "options": {"timeout_ms": 60000}
}
```

**Note**: True delays with setTimeout are not available. For rate limiting, implement pagination or batch processing on the server side.

---

### Example 13: Nested Data Processing

**Use Case**: Fetch data, then fetch related data for each item.

```javascript
// Request
{
  "code": `
// Get user's repos
const reposRes = call_tool('github', 'list_repos', {
  user: input.username,
  limit: 5
});

if (!reposRes.ok) {
  return {error: reposRes.error.message};
}

// For each repo, fetch contributors
const reposWithContributors = reposRes.result.map(repo => {
  const contributorsRes = call_tool('github', 'list_contributors', {
    owner: input.username,
    repo: repo.name,
    limit: 3
  });

  return {
    name: repo.name,
    stars: repo.stargazers_count,
    contributors: contributorsRes.ok ? contributorsRes.result : []
  };
});

return {
  username: input.username,
  repos: reposWithContributors
};
  `,
  "input": {"username": "octocat"},
  "options": {"max_tool_calls": 20}
}
```

---

## Testing Your Code

### Using the CLI

```bash
# Test simple code
mcpproxy code exec --code="({result: input.x + input.y})" --input='{"x":5,"y":10}'

# Test with file
echo "({result: input.value * 2})" > /tmp/test.js
mcpproxy code exec --file=/tmp/test.js --input='{"value":21}'

# Test with timeout
mcpproxy code exec --code="var x = 0; for(var i=0;i<1000000000;i++){x++}; ({result:x})" --timeout=5000

# Test with max tool calls
mcpproxy code exec \
  --code="var r=[]; for(var i=0;i<5;i++){r.push(call_tool('api','ping',{}))}; ({calls:r.length})" \
  --max-tool-calls=3
```

### Common Test Patterns

```javascript
// Test 1: Verify input access
code: "input"
input: {"test": "value"}
// Expected: {"ok": true, "value": {"test": "value"}}

// Test 2: Verify JSON serialization
code: "({a: 1, b: 'test', c: [1,2,3], d: {nested: true}})"
// Expected: {"ok": true, "value": {"a": 1, "b": "test", "c": [1,2,3], "d": {"nested": true}}}

// Test 3: Verify error handling
code: "throw new Error('Test error')"
// Expected: {"ok": false, "error": {"code": "RUNTIME_ERROR", "message": "Test error", "stack": "..."}}
```

---

## Next Steps

- **API Reference**: See [api-reference.md](api-reference.md) for complete schema documentation
- **Troubleshooting**: See [troubleshooting.md](troubleshooting.md) for common issues
- **Overview**: See [overview.md](overview.md) for architecture and best practices

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
var userRes = call_tool('github', 'get_user', {username: input.username});
if (!userRes.ok) {
  return {error: 'Failed to get user: ' + userRes.error.message};
}

var reposRes = call_tool('github', 'list_repos', {user: input.username, limit: 5});
if (!reposRes.ok) {
  return {error: 'Failed to get repos: ' + reposRes.error.message};
}

return {
  user: {
    name: userRes.result.name,
    login: userRes.result.login,
    public_repos: userRes.result.public_repos
  },
  repos: reposRes.result.map(function(r) {
    return {name: r.name, stars: r.stargazers_count};
  }),
  total_repos: userRes.result.public_repos
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
var userRes = call_tool('github', 'get_user', {username: input.username});
if (!userRes.ok) {
  return {error: 'Failed to get user: ' + userRes.error.message};
}

var reposRes = call_tool('github', 'list_repos', {user: input.username, limit: 5});
if (!reposRes.ok) {
  return {error: 'Failed to get repos: ' + reposRes.error.message};
}

return {
  user: {
    name: userRes.result.name,
    login: userRes.result.login,
    public_repos: userRes.result.public_repos
  },
  repos: reposRes.result.map(function(r) {
    return {name: r.name, stars: r.stargazers_count};
  }),
  total_repos: userRes.result.public_repos
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
var githubUser = call_tool('github', 'get_user', {username: input.username});
var gitlabUser = call_tool('gitlab', 'get_user', {username: input.username});
var bitbucketUser = call_tool('bitbucket', 'get_user', {username: input.username});

var results = {
  github: githubUser.ok ? githubUser.result : null,
  gitlab: gitlabUser.ok ? gitlabUser.result : null,
  bitbucket: bitbucketUser.ok ? bitbucketUser.result : null
};

var availableOn = [];
if (results.github) availableOn.push('github');
if (results.gitlab) availableOn.push('gitlab');
if (results.bitbucket) availableOn.push('bitbucket');

return {
  username: input.username,
  available_on: availableOn,
  profiles: results
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
var res = call_tool('github', 'get_user', {username: input.username});

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

return {
  success: true,
  data: {
    name: res.result.name,
    login: res.result.login,
    bio: res.result.bio
  }
};
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
var results = [];
var errors = [];

for (var i = 0; i < input.repo_names.length; i++) {
  var repoName = input.repo_names[i];
  var res = call_tool('github', 'get_repo', {
    owner: input.owner,
    repo: repoName
  });

  if (res.ok) {
    results.push({
      name: repoName,
      stars: res.result.stargazers_count,
      forks: res.result.forks_count,
      language: res.result.language
    });
  } else {
    errors.push({
      name: repoName,
      error: res.error.message
    });
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
var results = [];
var errors = [];

for (var i = 0; i < input.repo_names.length; i++) {
  var repoName = input.repo_names[i];
  var res = call_tool('github', 'get_repo', {
    owner: input.owner,
    repo: repoName
  });

  if (res.ok) {
    results.push({
      name: repoName,
      stars: res.result.stargazers_count,
      forks: res.result.forks_count
    });
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
var reposRes = call_tool('github', 'list_repos', {
  user: input.username,
  limit: 100
});

if (!reposRes.ok) {
  return {error: reposRes.error.message};
}

var repos = reposRes.result;
var totalStars = 0;
var totalForks = 0;
var languages = {};
var activeRepos = 0;

for (var i = 0; i < repos.length; i++) {
  var repo = repos[i];

  totalStars += repo.stargazers_count || 0;
  totalForks += repo.forks_count || 0;

  var lang = repo.language || 'Unknown';
  languages[lang] = (languages[lang] || 0) + 1;

  if (!repo.archived && repo.pushed_at) {
    activeRepos++;
  }
}

return {
  username: input.username,
  total_repos: repos.length,
  active_repos: activeRepos,
  archived_repos: repos.length - activeRepos,
  total_stars: totalStars,
  total_forks: totalForks,
  avg_stars: repos.length > 0 ? Math.round(totalStars / repos.length) : 0,
  languages: languages,
  most_popular_repo: repos.sort(function(a, b) {
    return (b.stargazers_count || 0) - (a.stargazers_count || 0);
  })[0].name
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
var reposRes = call_tool('github', 'list_repos', {
  user: input.username,
  limit: 50
});

if (!reposRes.ok) {
  return {error: reposRes.error.message};
}

// Filter: only non-archived repos updated recently
var cutoffDate = new Date(Date.now() - 90 * 24 * 60 * 60 * 1000); // 90 days ago

var activeRepos = reposRes.result.filter(function(repo) {
  if (repo.archived) return false;
  if (!repo.pushed_at) return false;

  var pushedDate = new Date(repo.pushed_at);
  return pushedDate > cutoffDate;
});

// Map: extract relevant fields
var simplified = activeRepos.map(function(repo) {
  return {
    name: repo.name,
    description: repo.description || 'No description',
    language: repo.language || 'Unknown',
    stars: repo.stargazers_count,
    last_updated: repo.pushed_at
  };
});

// Sort by stars descending
simplified.sort(function(a, b) {
  return b.stars - a.stars;
});

return {
  repos: simplified.slice(0, 10), // Top 10
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
var users = ['octocat', 'torvalds', 'nonexistent-user-xyz'];
var successful = [];
var failed = [];

for (var i = 0; i < users.length; i++) {
  var username = users[i];
  var res = call_tool('github', 'get_user', {username: username});

  if (res.ok) {
    successful.push({
      username: username,
      name: res.result.name,
      public_repos: res.result.public_repos
    });
  } else {
    failed.push({
      username: username,
      reason: res.error.message
    });
  }
}

return {
  successful: successful,
  failed: failed,
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
var items = input.items;
var results = [];

for (var i = 0; i < items.length; i++) {
  // Simulate delay by doing some computation
  for (var j = 0; j < 1000000; j++) {
    // Busy wait
  }

  var res = call_tool('api-server', 'process_item', {
    id: items[i]
  });

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
var reposRes = call_tool('github', 'list_repos', {
  user: input.username,
  limit: 5
});

if (!reposRes.ok) {
  return {error: reposRes.error.message};
}

var reposWithContributors = [];

// For each repo, fetch contributors
for (var i = 0; i < reposRes.result.length; i++) {
  var repo = reposRes.result[i];

  var contributorsRes = call_tool('github', 'list_contributors', {
    owner: input.username,
    repo: repo.name,
    limit: 3
  });

  reposWithContributors.push({
    name: repo.name,
    stars: repo.stargazers_count,
    contributors: contributorsRes.ok ? contributorsRes.result : []
  });
}

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

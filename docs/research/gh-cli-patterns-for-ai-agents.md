# GitHub CLI (gh) Patterns for AI Agents

Research findings on what makes GitHub CLI excellent for both humans and AI agents.

Date: 2025-12-17

## Executive Summary

The GitHub CLI (gh) is exceptionally well-designed for AI agent automation due to its consistent command structure, machine-readable output formats, context awareness, and powerful API access layer. Key features include structured JSON output with jq filtering, templating support, predictable exit codes, and the `gh api` subcommand that provides direct authenticated access to the GitHub REST and GraphQL APIs.

## 1. Command Hierarchy

### Noun-Verb Pattern

The gh CLI follows a consistent `gh <entity> <action> [flags]` structure that makes it predictable and discoverable:

```bash
# Entity: repository, Action: view
gh repo view

# Entity: pull request, Action: create
gh pr create

# Entity: issue, Action: list
gh issue list
```

### Complete Command Structure

**Core Operations:**
- `gh auth` - Authentication (login, logout, refresh, setup-git, status, switch, token)
- `gh api` - Direct API access (REST and GraphQL)
- `gh browse` - Open repository in browser
- `gh alias` - Custom shortcuts (delete, import, list, set)
- `gh completion` - Shell completion scripts
- `gh config` - Configuration management (get, set, list, clear-cache)

**Repository Operations:**
- `gh repo` - Repository management (create, clone, fork, view, archive, delete, etc.)
- `gh gist` - Gist operations (create, list, view, edit, delete)
- `gh release` - Release management (create, list, view, upload, download)

**Issue & PR Operations:**
- `gh issue` - Issue management (create, list, view, close, comment, edit, etc.)
- `gh pr` - Pull request operations (create, checkout, merge, review, view, etc.)
- `gh project` - Project board management

**CI/CD & Actions:**
- `gh workflow` - GitHub Actions workflows (run, list, view, enable, disable)
- `gh run` - Workflow runs (view, rerun, cancel, download logs)
- `gh cache` - Actions cache management

**Search & Discovery:**
- `gh search` - Search across code, commits, issues, PRs, repos
- `gh label` - Label management
- `gh ruleset` - Repository rulesets

**Development Tools:**
- `gh codespace` - GitHub Codespaces management
- `gh extension` - CLI extensions

### Consistent Subcommands

Common actions repeat across entities, making the CLI learnable:
- `list` - List items (issues, PRs, repos, releases)
- `view` - View details of a specific item
- `create` - Create a new item
- `edit` - Edit an existing item
- `delete` - Delete an item
- `close` - Close an issue or PR

## 2. Help Output Format

### Structured Help System

The gh CLI provides comprehensive help at multiple levels:

```bash
gh help                    # Top-level help
gh repo --help             # Command group help
gh repo create --help      # Specific command help
```

### Special Help Topics

```bash
gh help environment        # Environment variables
gh help exit-codes         # Exit code documentation
gh help formatting         # Output formatting
gh help reference          # Complete command reference
```

### Help Characteristics for AI Agents

1. **Consistent Format**: All help output follows the same structure
2. **Flag Documentation**: Every flag is documented with short and long forms
3. **Examples Section**: Real-world usage examples included
4. **JSON Field Discovery**: Use `--json help` or `--json ?` to discover available fields

```bash
# Discover available JSON fields
gh pr list --json ?
gh issue list --json help
```

## 3. Output Formats

### JSON Output (Primary for Automation)

Every major command supports `--json` flag for structured output:

```bash
# Basic JSON output with specific fields
gh pr list --json number,title,state,author,url

# Full JSON output (all fields)
gh issue view 123 --json

# Available JSON fields vary by command
gh pr list --json number,title,state,author,headRefName,baseRefName,url,createdAt,updatedAt,isDraft,mergeable,reviewDecision
```

**Common JSON Fields by Entity:**

**Pull Requests:**
- Metadata: `number`, `title`, `state`, `url`, `body`
- People: `author`, `assignees`, `reviewers`
- Branches: `headRefName`, `baseRefName`, `headRepository`
- Timestamps: `createdAt`, `updatedAt`, `closedAt`, `mergedAt`
- Status: `isDraft`, `mergeable`, `reviewDecision`
- Stats: `additions`, `deletions`, `changedFiles`
- Organization: `labels`, `milestone`, `projectCards`

**Issues:**
- Metadata: `number`, `title`, `state`, `url`, `body`
- People: `author`, `assignees`
- Organization: `labels`, `milestone`, `projectCards`
- Timestamps: `createdAt`, `updatedAt`, `closedAt`
- Interaction: `comments`, `reactionGroups`

**Repositories:**
- Basic: `name`, `nameWithOwner`, `description`, `url`
- Status: `isPrivate`, `isFork`, `isArchived`, `isTemplate`
- Stats: `stargazerCount`, `forkCount`, `openIssuesCount`
- Refs: `defaultBranchRef`, `pushedAt`, `createdAt`

### JQ Filtering (--jq flag)

Combine JSON output with jq expressions for powerful filtering:

```bash
# Extract only titles
gh pr list --json title --jq '.[].title'

# Filter by condition
gh issue list --json number,title,labels --jq '.[] | select(.labels[].name == "bug")'

# Complex transformations
gh pr list --json number,title,author --jq '.[] | "\(.number): \(.title) by \(.author.login)"'

# Count items
gh pr list --json number --jq 'length'

# Get first N items
gh pr list --json number,title --jq '.[:5]'
```

**Key Advantage for AI Agents**: Single command can fetch, filter, and format data without intermediate processing.

### Template Output (--template flag)

Go template formatting for custom output:

```bash
# Simple template
gh pr list --json number,title --template '{{range .}}{{.number}}: {{.title}}{{"\n"}}{{end}}'

# Complex template with functions
gh issue list --json number,title,labels --template '
{{range .}}
Issue #{{.number}}: {{.title}}
Labels: {{range .labels}}{{.name | color "yellow"}} {{end}}
{{"\n"}}
{{end}}
'
```

**Available Template Functions:**
- `color` - Colorize output
- `pluck` - Extract field from array
- `join` - Join array elements
- `truncate` - Truncate strings
- `timeago` - Format time as relative

### Tab-Separated Output (Default)

Without flags, most commands output human-readable tab-separated format:

```bash
# Default output is tab-separated
gh pr list

# Can be parsed with cut, awk
gh pr list | cut -f1  # Get PR numbers
gh pr list | awk '{print $1, $3}'  # Select columns
```

### Quiet Mode

```bash
# Minimal output, useful for scripting
gh pr create --title "Fix" --body "Fixed bug" --quiet
```

## 4. gh api Subcommand - The Power Feature

### Overview

The `gh api` command is arguably the most powerful feature for AI agents, providing direct authenticated access to the entire GitHub API (both REST and GraphQL).

**Syntax:**
```bash
gh api <endpoint> [flags]
```

### Key Features

#### Placeholder Substitution

Automatically replaces context-aware placeholders:

```bash
# These placeholders are auto-replaced from current repo
gh api repos/{owner}/{repo}/releases
gh api repos/{owner}/{repo}/branches/{branch}

# Example in non-repo context
GH_REPO=cli/cli gh api repos/{owner}/{repo}/pulls
```

**Available Placeholders:**
- `{owner}` - Repository owner
- `{repo}` - Repository name
- `{branch}` - Current branch

#### HTTP Method Control

```bash
# Default is GET
gh api repos/{owner}/{repo}/issues

# POST automatically selected when parameters added
gh api repos/{owner}/{repo}/issues -f title="Bug" -f body="Description"

# Explicit method override
gh api -X PATCH repos/{owner}/{repo}/issues/123 -f state="closed"
gh api -X DELETE repos/{owner}/{repo}/releases/12345
```

#### Parameter Types

**Static String Fields (-f, --raw-field):**
```bash
gh api repos/{owner}/{repo}/issues -f title="Bug report" -f body="Description"
```

**Typed Fields (-F, --field):**
Auto-converts types:
```bash
# Boolean
gh api repos/{owner}/{repo} -F private=true

# Number
gh api repos/{owner}/{repo}/issues -F per_page=100

# Null
gh api repos/{owner}/{repo}/issues/123 -F milestone=null

# File content
gh api gists -F 'files[script.sh][content]=@script.sh'

# Stdin
cat data.txt | gh api gists -F 'files[data.txt][content]=@-'
```

**Nested Parameters:**
```bash
# Nested objects
gh api repos/{owner}/{repo}/rulesets -F 'rules[0][type]=branch_name_pattern'

# Arrays
gh api repos/{owner}/{repo}/issues -F 'labels[]=bug' -F 'labels[]=urgent'

# Empty array
gh api repos/{owner}/{repo}/issues -F 'labels[]'
```

#### Request Body from File

```bash
# JSON file as request body
gh api repos/{owner}/{repo}/rulesets --input ruleset.json

# From stdin
cat issue.json | gh api repos/{owner}/{repo}/issues --input -

# Note: When using --input, field flags become query parameters
gh api repos/{owner}/{repo}/issues?per_page=100 --input issue.json
```

#### Custom Headers

```bash
# Accept specific API version
gh api -H 'Accept: application/vnd.github.v3+json' endpoint

# Media type variations
gh api -H 'Accept: application/vnd.github.raw+json' repos/{owner}/{repo}/readme

# API preview features
gh api --preview corsair,scarlet-witch repos/{owner}/{repo}/checks
```

#### Output Processing

**Built-in jq:**
```bash
# Extract specific fields
gh api repos/{owner}/{repo}/issues --jq '.[].title'

# Complex filtering
gh api search/issues -f q='repo:cli/cli is:open' --jq '.items | map({number, title, labels: [.labels[].name]})'
```

**Template formatting:**
```bash
gh api repos/{owner}/{repo}/issues --template '
{{range .}}
Issue #{{.number}}: {{.title}}
{{end}}
'
```

**Include headers:**
```bash
# Show HTTP headers in output
gh api -i repos/{owner}/{repo}/rate_limit
```

**Silent mode:**
```bash
# Check status code only
gh api --silent repos/{owner}/{repo}/actions/workflows/123/enable
echo $?  # 0 for success, non-zero for failure
```

#### Pagination

**Automatic pagination:**
```bash
# Fetch all pages automatically
gh api --paginate repos/{owner}/{repo}/issues

# Combine pages into JSON array
gh api --paginate --slurp repos/{owner}/{repo}/issues
```

**Manual pagination:**
```bash
# Page parameter
gh api repos/{owner}/{repo}/issues?page=2&per_page=100
```

### GraphQL Support

```bash
# GraphQL queries
gh api graphql -f query='
  query {
    viewer {
      login
      name
    }
  }
'

# With variables
gh api graphql -F owner='{owner}' -F name='{repo}' -f query='
  query($owner: String!, $name: String!) {
    repository(owner: $owner, name: $name) {
      releases(last: 3) {
        nodes {
          tagName
          publishedAt
        }
      }
    }
  }
'

# GraphQL pagination
gh api graphql --paginate -f query='
  query($endCursor: String) {
    viewer {
      repositories(first: 100, after: $endCursor) {
        nodes { nameWithOwner }
        pageInfo {
          hasNextPage
          endCursor
        }
      }
    }
  }
'
```

**GraphQL Pagination Requirements:**
- Query must accept `$endCursor: String` variable
- Must fetch `pageInfo { hasNextPage, endCursor }`
- gh automatically handles pagination logic

### Enterprise Support

```bash
# GitHub Enterprise Server
gh api --hostname github.example.com endpoint
```

### Why gh api is Powerful for AI Agents

1. **Complete API Coverage**: Access any GitHub API endpoint, not just what gh commands expose
2. **Automatic Authentication**: Uses stored credentials, no token management needed
3. **Context Awareness**: Placeholders reduce boilerplate
4. **Type Safety**: Typed fields prevent common JSON serialization errors
5. **Built-in Processing**: jq and templates eliminate post-processing steps
6. **Pagination Handling**: Automatic pagination prevents data truncation
7. **Error Handling**: Clear exit codes and error messages
8. **No Rate Limit Concerns**: Uses same pool as gh commands

### Example Workflows

**Check CI Status:**
```bash
# Get workflow runs for current branch
gh api repos/{owner}/{repo}/actions/runs \
  -f branch={branch} \
  -f event=push \
  --jq '.workflow_runs[0] | {status, conclusion, html_url}'
```

**Bulk Operations:**
```bash
# Close multiple issues
for issue in 123 124 125; do
  gh api -X PATCH repos/{owner}/{repo}/issues/$issue -f state="closed"
done
```

**Complex Queries:**
```bash
# Find PRs needing review from specific team
gh api search/issues \
  -f q='repo:owner/repo is:pr is:open review-requested:@team' \
  --jq '.items[] | {number, title, author: .user.login}'
```

## 5. Configuration

### Authentication

**Interactive Login:**
```bash
gh auth login                           # Web-based flow
gh auth login --with-token < token.txt  # Token from stdin
gh auth login --hostname github.enterprise.com  # Enterprise
```

**Multi-Account Support:**
```bash
gh auth login                           # Add additional account
gh auth switch                          # Switch between accounts
gh auth status                          # Show current account
gh auth status --show-token             # Display token
```

**Token Management:**
```bash
gh auth token                           # Get current token
gh auth refresh                         # Refresh token
gh auth logout                          # Remove credentials
```

**Git Integration:**
```bash
gh auth setup-git                       # Configure git to use gh for auth
```

### Configuration Management

```bash
# Set preferences
gh config set editor vim
gh config set prompt disabled           # Disable interactive prompts
gh config set git_protocol ssh

# View configuration
gh config list
gh config get editor

# Clear cache
gh config clear-cache
```

**Configuration File Location:**
- Linux/macOS: `~/.config/gh/config.yml`
- Windows: `%AppData%\GitHub CLI\config.yml`

### Environment Variables

**Authentication:**
```bash
GH_TOKEN=ghp_xxxx                       # Use specific token
GITHUB_TOKEN=ghp_xxxx                   # Alternative token variable
GH_ENTERPRISE_TOKEN=ghp_xxxx            # Enterprise token
```

**Configuration:**
```bash
GH_HOST=github.enterprise.com           # Target host
GH_REPO=owner/repo                      # Default repository
GH_EDITOR=vim                           # Preferred editor
GH_BROWSER=firefox                      # Browser for --web
GH_PAGER=less                           # Pager for output
NO_COLOR=1                              # Disable color output
CLICOLOR=0                              # Alternative color disable
```

**Debugging:**
```bash
GH_DEBUG=1                              # Enable debug output
GH_DEBUG=api                            # Debug API calls only
```

**Non-Interactive Mode:**
```bash
# Disable interactive prompts (important for AI agents)
GH_PROMPT_DISABLED=1
gh config set prompt disabled

# Prevent browser opening
BROWSER=none gh auth login
```

### Aliases

Create custom shortcuts for frequent commands:

```bash
# Create alias
gh alias set viewpr "pr view --web"
gh alias set bugs "issue list --label=bug"
gh alias set co "pr checkout"

# Use alias
gh viewpr 123                           # Opens PR in browser
gh bugs                                 # Lists bug issues

# List aliases
gh alias list

# Complex aliases with positional arguments
gh alias set prc 'pr create --title "$1" --body "$2"'
gh prc "Bug fix" "Fixed the thing"

# Multi-command aliases (shell script)
gh alias set push-pr '!git push && gh pr create'
```

**Alias Patterns for AI Agents:**
```bash
# Quick status checks
gh alias set prs "pr list --json number,title,state,author"
gh alias set open-prs "pr list --state=open --json number,title"

# Common operations
gh alias set merge-pr "pr merge --squash --delete-branch"
gh alias set approve "pr review --approve"

# Search patterns
gh alias set my-prs "search prs --author=@me --json number,title,url"
gh alias set my-issues "search issues --assignee=@me --json number,title,url"
```

### Fine-Grained Permissions (Security for AI Agents)

When setting up gh for AI agent use, use fine-grained personal access tokens with minimal required permissions:

**Recommended Approach:**
1. Create token with specific repository access
2. Grant only necessary permissions:
   - Read-only for monitoring tasks
   - Issue write for issue management
   - PR write for PR operations
3. Set expiration date
4. Use separate tokens per agent/task

**Token Scopes (Classic):**
- `repo` - Full repository access
- `repo:status` - Commit status only
- `public_repo` - Public repositories only
- `read:org` - Read org data
- `write:discussion` - Discussions

**Fine-Grained Permissions (Recommended):**
- More granular control
- Repository-specific access
- Shorter expiration options
- Better audit trail

## 6. Scripting Features

### Exit Codes

**Standard Exit Codes:**
```bash
# 0 - Success
gh pr list && echo "Success"

# 1 - General error
gh pr view 99999 || echo "Failed"

# 2 - Cancelled operation
# User pressed Ctrl+C

# 4 - Authentication required
gh pr list || echo "Not authenticated"
```

**Usage in Scripts:**
```bash
#!/bin/bash
set -e  # Exit on error

# Check if authenticated
if ! gh auth status >/dev/null 2>&1; then
  echo "Not authenticated. Run: gh auth login"
  exit 1
fi

# Try operation
if gh pr create --title "Update" --body "Changes"; then
  echo "PR created successfully"
else
  echo "Failed to create PR" >&2
  exit 1
fi
```

### Error Handling Best Practices

**Capture stderr:**
```bash
# Separate stdout and stderr
result=$(gh pr view 123 --json title 2>error.log)
if [ $? -eq 0 ]; then
  echo "Success: $result"
else
  echo "Error: $(cat error.log)" >&2
fi
```

**Check before operation:**
```bash
# Verify PR exists before operating on it
if gh pr view 123 >/dev/null 2>&1; then
  gh pr merge 123
else
  echo "PR not found"
fi
```

**Graceful degradation:**
```bash
# Try authenticated call, fall back to public data
gh api repos/{owner}/{repo} || gh api repos/public-owner/public-repo
```

### Context Awareness

**Automatic Repository Detection:**
```bash
# When run inside a git repository, gh automatically detects:
# - Repository owner
# - Repository name
# - Current branch
# - Remote configuration

# These commands work without specifying repo
cd ~/projects/myrepo
gh pr list                              # Lists PRs for current repo
gh issue create                         # Creates issue in current repo
gh browse                               # Opens current repo in browser
```

**Override Context:**
```bash
# Specify different repository
gh pr list --repo owner/other-repo
gh issue view 123 -R owner/other-repo

# Set default repository
gh repo set-default owner/repo          # Interactive selection
gh repo set-default owner/repo          # Direct set
gh repo set-default --view              # Show current default

# Environment variable override
GH_REPO=owner/repo gh pr list
```

**Branch Context:**
```bash
# Commands aware of current branch
gh pr create                            # Creates PR from current branch
gh pr view                              # Shows PR for current branch
gh pr status                            # Status of current branch's PR

# Override branch
gh pr create --head feature-branch
```

### Non-Interactive Mode

Critical for AI agents - disable all interactive prompts:

**Global Setting:**
```bash
gh config set prompt disabled
```

**Environment Variable:**
```bash
export GH_PROMPT_DISABLED=1
```

**Command Flags:**
```bash
# Provide all required information via flags
gh pr create \
  --title "Feature" \
  --body "Description" \
  --base main \
  --head feature-branch

# Use --yes to skip confirmations
gh pr merge 123 --yes --squash

# Use --web to open browser (useful fallback)
gh issue create --web
```

### Web Fallback

```bash
# Open in browser when CLI is insufficient
gh pr create --web                      # Opens PR creation form
gh issue view 123 --web                 # Opens issue in browser
gh browse                               # Opens repo in browser

# Useful for:
# - Complex forms (project boards, releases with assets)
# - Visual tasks (reviewing images, diagrams)
# - Fallback when CLI command fails
```

### Pagination in Scripts

```bash
# Get all results automatically
gh api --paginate repos/{owner}/{repo}/issues > all_issues.json

# Process paginated results
gh pr list --limit 1000 --json number,title | jq '.[] | .number'

# Manual pagination for control
page=1
per_page=100
while true; do
  data=$(gh api "repos/{owner}/{repo}/issues?page=$page&per_page=$per_page")
  [ "$(echo "$data" | jq 'length')" -eq 0 ] && break
  echo "$data" | jq -r '.[] | "\(.number): \(.title)"'
  ((page++))
done
```

### Shell Completion

Enable tab completion for better interactive use:

```bash
# Bash
echo 'eval "$(gh completion -s bash)"' >> ~/.bashrc

# Zsh
gh completion -s zsh > /usr/local/share/zsh/site-functions/_gh

# Fish
gh completion -s fish > ~/.config/fish/completions/gh.fish

# PowerShell
gh completion -s powershell | Out-String | Invoke-Expression
```

**Benefits:**
- Tab-complete commands and subcommands
- Complete flag names
- Suggest repository names
- Complete branch names

### Batch Operations Pattern

```bash
# Process multiple items
gh pr list --json number --jq '.[].number' | while read -r pr_num; do
  gh pr review $pr_num --approve
done

# Parallel processing (with GNU parallel)
gh issue list --json number --jq '.[].number' | \
  parallel -j5 gh issue close {}

# Conditional operations
gh pr list --json number,title,isDraft | jq -r '.[] | select(.isDraft) | .number' | \
  while read -r pr_num; do
    gh pr ready $pr_num
  done
```

### Error Output to stderr

```bash
# gh properly separates data from errors
data=$(gh pr list --json number 2>errors.log)
if [ $? -eq 0 ]; then
  echo "$data" | jq .
else
  echo "Error occurred:" >&2
  cat errors.log >&2
fi
```

### JSON Output Consistency

**Predictable Structure:**
- List commands always return arrays: `[{...}, {...}]`
- View commands return objects: `{...}`
- Empty results return `[]` or `null`, not errors
- Error responses include `message` field

```bash
# Always safe to pipe to jq
gh pr list --json number | jq 'length'  # 0 if empty, not error

# Check for empty results
count=$(gh pr list --json number | jq 'length')
if [ "$count" -eq 0 ]; then
  echo "No PRs found"
fi
```

## 7. Design Patterns for AI Agents

### Discovery Pattern

**Step 1: List available items**
```bash
gh repo list --limit 100 --json name,nameWithOwner
gh pr list --json number,title,state
gh issue list --json number,title,labels
```

**Step 2: Get details for specific item**
```bash
gh pr view 123 --json number,title,body,commits,files
gh issue view 456 --json number,title,body,comments
```

**Step 3: Take action**
```bash
gh pr review 123 --approve --body "LGTM"
gh issue close 456 --comment "Fixed in #789"
```

### Check-Then-Act Pattern

```bash
# Check state first
state=$(gh pr view 123 --json state --jq .state)

if [ "$state" = "OPEN" ]; then
  # Perform operation
  gh pr merge 123 --squash
else
  echo "PR not in OPEN state: $state"
fi
```

### Idempotent Operations

```bash
# Safe to run multiple times
gh repo create myrepo 2>/dev/null || echo "Repo exists"
gh label create bug --color FF0000 --force  # --force makes it idempotent
```

### Fallback Chain

```bash
# Try specific command, fall back to API, then web
if ! gh pr create --title "Fix" --body "Description"; then
  if ! gh api repos/{owner}/{repo}/pulls -f title="Fix" -f body="Description"; then
    gh pr create --web  # Last resort: manual via browser
  fi
fi
```

### Structured Logging

```bash
#!/bin/bash
log() {
  echo "[$(date -Iseconds)] $1" | tee -a script.log
}

log "Starting PR creation"
if gh pr create --title "Update" --body "Changes"; then
  log "PR created successfully"
else
  log "ERROR: PR creation failed"
  exit 1
fi
```

### Validation Before Execution

```bash
# Validate inputs
if [ -z "$PR_TITLE" ] || [ -z "$PR_BODY" ]; then
  echo "Error: Title and body required" >&2
  exit 1
fi

# Check authentication
if ! gh auth status >/dev/null 2>&1; then
  echo "Error: Not authenticated" >&2
  exit 4
fi

# Verify repository access
if ! gh api repos/{owner}/{repo} >/dev/null 2>&1; then
  echo "Error: Cannot access repository" >&2
  exit 1
fi

# Perform operation
gh pr create --title "$PR_TITLE" --body "$PR_BODY"
```

### Atomic Operations with Rollback

```bash
#!/bin/bash
set -e

# Create PR
pr_url=$(gh pr create --title "Update" --body "Changes" --json url --jq .url)
pr_number=$(echo "$pr_url" | grep -oE '[0-9]+$')

# Trap errors and cleanup
trap 'gh pr close $pr_number && gh pr delete $pr_number' ERR

# Perform additional operations
gh pr ready $pr_number
gh pr merge $pr_number --auto --squash

echo "Success: $pr_url"
```

## 8. What Makes gh Excellent for AI Agents

### 1. Predictable Structure

- **Consistent command patterns**: `gh <entity> <action>`
- **Uniform flag syntax**: `--json`, `--jq`, `--template` work everywhere
- **Stable output formats**: JSON structure doesn't change between versions

### 2. Machine-Readable Output

- **JSON-first design**: Every major command supports `--json`
- **Structured errors**: Errors include machine-parseable information
- **No parsing required**: No need for regex/awk to extract data from human-readable text

### 3. Context Awareness

- **Automatic detection**: Repository, branch, owner inferred from environment
- **Placeholder system**: `{owner}`, `{repo}`, `{branch}` reduce boilerplate
- **Sane defaults**: Commands "just work" when run in repository directory

### 4. Complete API Coverage

- **gh api subcommand**: Access any GitHub API endpoint
- **Both REST and GraphQL**: Choose appropriate API for task
- **Automatic authentication**: No token management needed

### 5. Non-Interactive Operation

- **Fully scriptable**: All prompts can be disabled
- **Required flags**: Clear documentation of required vs optional parameters
- **Exit codes**: Reliable status reporting for automation

### 6. Error Handling

- **Predictable exit codes**: 0 (success), 1 (error), 2 (cancelled), 4 (auth)
- **Structured errors**: Error messages include actionable information
- **Stderr vs stdout**: Data on stdout, errors on stderr

### 7. Composability

- **Unix philosophy**: Does one thing well, composes with other tools
- **Pipe-friendly**: JSON output pipes perfectly to jq
- **Standard streams**: Proper use of stdin, stdout, stderr

### 8. Security

- **Built-in authentication**: Secure credential storage
- **Fine-grained tokens**: Limit agent permissions
- **Token rotation**: Easy to refresh/update credentials

### 9. Discoverability

- **Comprehensive help**: `--help` on every command
- **Field discovery**: `--json help` shows available fields
- **Examples**: Help output includes real-world examples

### 10. Extensibility

- **Aliases**: Create custom commands
- **Extensions**: Install community extensions
- **API access**: Build on top of GitHub API

## 9. AI Agent Best Practices

### Setup Checklist

1. **Install gh CLI** on agent's system
2. **Authenticate** with fine-grained token
3. **Set non-interactive mode**:
   ```bash
   gh config set prompt disabled
   export GH_PROMPT_DISABLED=1
   ```
4. **Test authentication**: `gh auth status`
5. **Verify API access**: `gh api user`

### Agent Instructions Template

```markdown
You have access to the `gh` (GitHub CLI) command. Use it for all GitHub operations.

RULES:
1. Always use `--json` flag for structured output
2. Parse output with `--jq` when possible
3. Check exit codes: 0 = success, 1 = error, 4 = auth required
4. For any operation, show the exact command before executing
5. Use `gh api` for operations not covered by regular commands
6. Never merge PRs without explicit approval
7. Always verify repository context before operations
8. Use `gh auth status` to check authentication if commands fail

EXAMPLES:
- List PRs: `gh pr list --json number,title,state`
- Create issue: `gh issue create --title "Bug" --body "Description"`
- Check CI: `gh run list --json status,conclusion,url`
- API access: `gh api repos/{owner}/{repo}/releases`
```

### Security Guidelines

**Permission Restrictions:**
```bash
# Use minimal token scopes
# READ: Monitoring, listing, viewing
# WRITE: Only for specific operations needed
# ADMIN: Never for AI agents
```

**Boundary Definition:**
```markdown
ALLOWED:
- Create issues and PRs
- Add comments
- List/view any information
- Check CI status
- Create releases (if authorized)

PROHIBITED:
- Merge/close PRs without approval
- Delete issues/PRs/repos
- Modify repository settings
- Force push
- Bypass branch protection
```

**Audit Trail:**
```bash
# Log all gh commands
exec > >(tee -a gh-audit.log)
exec 2>&1

# Prepend commands with context
echo "[$(date -Iseconds)] USER=agent REPO=$(gh repo view --json nameWithOwner -q .nameWithOwner)"
```

### Error Recovery

```bash
#!/bin/bash

retry_command() {
  local max_attempts=3
  local attempt=1

  while [ $attempt -le $max_attempts ]; do
    if "$@"; then
      return 0
    fi

    echo "Attempt $attempt failed, retrying..." >&2
    sleep $((attempt * 2))  # Exponential backoff
    ((attempt++))
  done

  echo "Command failed after $max_attempts attempts" >&2
  return 1
}

# Usage
retry_command gh pr create --title "Update" --body "Changes"
```

### Testing Pattern

```bash
# Dry-run pattern (for operations that support it)
gh pr create --title "Test" --body "Test" --dry-run

# Verify pattern (check before action)
verify_pr_exists() {
  gh pr view "$1" >/dev/null 2>&1
}

if verify_pr_exists 123; then
  gh pr merge 123
else
  echo "PR 123 not found"
fi
```

## 10. Comparison: Human vs Agent Usage

| Aspect | Human Usage | AI Agent Usage |
|--------|-------------|----------------|
| **Input** | Interactive prompts OK | Must provide all flags |
| **Output** | Human-readable default | `--json` always |
| **Errors** | Can interpret messages | Needs exit codes |
| **Context** | Understands implicit context | Explicit verification needed |
| **Auth** | Web-based login | Token via env var |
| **Confirmation** | Interactive prompts | `--yes` flags |
| **Fallback** | `--web` opens browser | Must handle programmatically |
| **Learning** | Explores via `--help` | Pre-trained on command structure |

## 11. Real-World Agent Workflows

### Workflow 1: Automated Issue Triage

```bash
#!/bin/bash
# Agent: Triage new issues, add labels, assign to projects

# Get untriaged issues (no labels)
gh issue list --json number,title,body,author,createdAt --label="" |
  jq -r '.[] | @json' |
  while read -r issue; do
    number=$(echo "$issue" | jq -r .number)
    title=$(echo "$issue" | jq -r .title)
    body=$(echo "$issue" | jq -r .body)

    # AI determines appropriate labels
    labels=$(ai_classify_issue "$title" "$body")

    # Apply labels
    echo "$labels" | jq -r '.[]' | while read -r label; do
      gh issue edit $number --add-label "$label"
    done

    # Add to project board
    gh project item-add 123 --owner owner --content-id $(
      gh issue view $number --json id -q .id
    )

    # Comment with triage info
    gh issue comment $number --body "Auto-triaged with labels: $labels"
  done
```

### Workflow 2: PR Review Assistant

```bash
#!/bin/bash
# Agent: Review PRs, check for issues, add comments

# Get open PRs
gh pr list --state open --json number,title,headRefName |
  jq -r '.[] | @json' |
  while read -r pr; do
    number=$(echo "$pr" | jq -r .number)

    # Get PR diff
    diff=$(gh pr diff $number)

    # AI analyzes diff
    review=$(ai_review_code "$diff")

    # Get file-specific comments
    echo "$review" | jq -r '.comments[] | @json' |
      while read -r comment; do
        path=$(echo "$comment" | jq -r .path)
        line=$(echo "$comment" | jq -r .line)
        body=$(echo "$comment" | jq -r .body)

        # Add review comment via API
        gh api repos/{owner}/{repo}/pulls/$number/comments \
          -f body="$body" \
          -f path="$path" \
          -F position=$line \
          -f commit_id="$(gh pr view $number --json commits -q '.commits[-1].oid')"
      done

    # Submit review
    decision=$(echo "$review" | jq -r .decision)
    if [ "$decision" = "APPROVE" ]; then
      gh pr review $number --approve --body "$(echo "$review" | jq -r .summary)"
    elif [ "$decision" = "REQUEST_CHANGES" ]; then
      gh pr review $number --request-changes --body "$(echo "$review" | jq -r .summary)"
    fi
  done
```

### Workflow 3: CI/CD Monitor

```bash
#!/bin/bash
# Agent: Monitor CI, notify on failures, create issues for flaky tests

# Get recent workflow runs
gh run list --limit 50 --json conclusion,headBranch,event,workflowName,url,databaseId |
  jq -r '.[] | select(.conclusion == "failure") | @json' |
  while read -r run; do
    run_id=$(echo "$run" | jq -r .databaseId)
    branch=$(echo "$run" | jq -r .headBranch)
    workflow=$(echo "$run" | jq -r .workflowName)
    url=$(echo "$run" | jq -r .url)

    # Get logs
    logs=$(gh run view $run_id --log-failed)

    # AI analyzes logs
    analysis=$(ai_analyze_failure "$logs")

    # Check if issue already exists
    existing=$(gh issue list --label "ci-failure" --search "in:title $workflow" --json number | jq -r '.[0].number')

    if [ -z "$existing" ]; then
      # Create new issue
      gh issue create \
        --title "CI Failure: $workflow on $branch" \
        --body "$(cat <<EOF
Workflow run failed: $url

**Analysis:**
$analysis

**Logs:**
\`\`\`
$logs
\`\`\`
EOF
)" \
        --label "ci-failure" \
        --label "automated"
    else
      # Update existing issue
      gh issue comment $existing --body "Another failure detected: $url"
    fi
  done
```

### Workflow 4: Release Automation

```bash
#!/bin/bash
# Agent: Create releases from merged PRs

# Get merged PRs since last release
last_release=$(gh release view --json tagName -q .tagName)
prs=$(gh pr list --state merged --json number,title,mergedAt,labels |
  jq --arg tag "$last_release" '[.[] | select(.mergedAt > $tag)]')

# Group by category
features=$(echo "$prs" | jq '[.[] | select(.labels[].name == "feature")]')
bugs=$(echo "$prs" | jq '[.[] | select(.labels[].name == "bug")]')
breaking=$(echo "$prs" | jq '[.[] | select(.labels[].name == "breaking")]')

# Determine version bump
if [ "$(echo "$breaking" | jq 'length')" -gt 0 ]; then
  bump="major"
elif [ "$(echo "$features" | jq 'length')" -gt 0 ]; then
  bump="minor"
else
  bump="patch"
fi

# Calculate new version
new_version=$(semver_bump "$last_release" "$bump")

# Generate release notes
notes=$(cat <<EOF
## What's Changed

$([ "$(echo "$breaking" | jq 'length')" -gt 0 ] && echo "### Breaking Changes
$(echo "$breaking" | jq -r '.[] | "- \(.title) (#\(.number))"')
")

$([ "$(echo "$features" | jq 'length')" -gt 0 ] && echo "### Features
$(echo "$features" | jq -r '.[] | "- \(.title) (#\(.number))"')
")

$([ "$(echo "$bugs" | jq 'length')" -gt 0 ] && echo "### Bug Fixes
$(echo "$bugs" | jq -r '.[] | "- \(.title) (#\(.number))"')
")

**Full Changelog**: https://github.com/{owner}/{repo}/compare/$last_release...$new_version
EOF
)

# Create release
gh release create "$new_version" \
  --title "$new_version" \
  --notes "$notes" \
  --target main
```

## 12. Advanced Patterns

### Rate Limit Awareness

```bash
# Check rate limit before operations
check_rate_limit() {
  remaining=$(gh api rate_limit --jq .rate.remaining)
  if [ "$remaining" -lt 100 ]; then
    echo "Warning: Only $remaining API calls remaining" >&2
    reset=$(gh api rate_limit --jq .rate.reset)
    echo "Resets at: $(date -d @$reset)" >&2
    return 1
  fi
  return 0
}

# Use before batch operations
if check_rate_limit; then
  # Perform operations
  gh pr list --limit 100
fi
```

### Caching Pattern

```bash
# Cache expensive operations
CACHE_DIR=~/.cache/gh-agent
CACHE_TTL=300  # 5 minutes

cached_gh() {
  local cache_key=$(echo "$*" | md5sum | cut -d' ' -f1)
  local cache_file="$CACHE_DIR/$cache_key"

  if [ -f "$cache_file" ]; then
    local age=$(($(date +%s) - $(stat -f %m "$cache_file")))
    if [ $age -lt $CACHE_TTL ]; then
      cat "$cache_file"
      return 0
    fi
  fi

  gh "$@" | tee "$cache_file"
}

# Usage
cached_gh pr list --json number,title
```

### Parallel Execution

```bash
# Process multiple repos in parallel
repos=(
  "owner/repo1"
  "owner/repo2"
  "owner/repo3"
)

process_repo() {
  local repo=$1
  echo "Processing $repo..."
  GH_REPO=$repo gh pr list --json number,title
}

export -f process_repo
printf '%s\n' "${repos[@]}" | xargs -P 5 -I {} bash -c 'process_repo "$@"' _ {}
```

### State Management

```bash
# Track agent state between runs
STATE_FILE=~/.config/gh-agent/state.json

save_state() {
  local key=$1
  local value=$2
  jq --arg key "$key" --arg value "$value" '.[$key] = $value' "$STATE_FILE" > tmp.json
  mv tmp.json "$STATE_FILE"
}

load_state() {
  local key=$1
  jq -r --arg key "$key" '.[$key] // empty' "$STATE_FILE"
}

# Usage
last_pr=$(load_state "last_processed_pr")
# ... process PRs ...
save_state "last_processed_pr" "$current_pr"
```

## 13. Troubleshooting Guide

### Common Issues for Agents

**Issue: Authentication Failures**
```bash
# Check auth status
gh auth status

# Verify token
gh auth token

# Test API access
gh api user

# Re-authenticate
gh auth login --with-token < token.txt
```

**Issue: Rate Limiting**
```bash
# Check current limits
gh api rate_limit

# Use authenticated requests (higher limits)
GH_TOKEN=... gh api endpoint

# Implement exponential backoff
```

**Issue: Context Detection**
```bash
# Verify repository detection
gh repo view

# Set explicit repository
GH_REPO=owner/repo gh pr list

# Check remote configuration
git remote -v
```

**Issue: JSON Parsing**
```bash
# Validate JSON output
gh pr list --json number | jq .

# Handle empty results
result=$(gh pr list --json number)
[ "$(echo "$result" | jq 'length')" -eq 0 ] && echo "No PRs"

# Escape special characters
title=$(echo "$raw_title" | jq -Rs .)
```

**Issue: Exit Code Misinterpretation**
```bash
# Capture exit code
gh pr view 123
exit_code=$?

case $exit_code in
  0) echo "Success" ;;
  1) echo "Error" ;;
  2) echo "Cancelled" ;;
  4) echo "Auth required" ;;
  *) echo "Unknown: $exit_code" ;;
esac
```

## 14. Key Takeaways

### What to Adopt for Other CLIs

1. **JSON-first output**: `--json` flag on all commands
2. **Structured filtering**: Built-in jq support via `--jq`
3. **Context awareness**: Infer parameters from environment
4. **Placeholder system**: `{variable}` substitution
5. **API access layer**: Direct API subcommand
6. **Predictable exit codes**: Document and stick to them
7. **Non-interactive mode**: Global setting to disable prompts
8. **Field discovery**: `--json help` to show available fields
9. **Consistent subcommands**: Same actions across entities
10. **Alias system**: User-defined shortcuts

### Anti-Patterns to Avoid

1. **Inconsistent output formats** between commands
2. **Required interactive prompts** with no flag alternative
3. **Parsing human-readable output** instead of structured data
4. **Inconsistent flag naming** across commands
5. **Mixing data and errors** on stdout
6. **Undocumented exit codes**
7. **Breaking API changes** without versioning
8. **Context-dependent behavior** without explicit overrides
9. **Poor error messages** without actionable information
10. **Token management** left entirely to users

## 15. Conclusion

The GitHub CLI (`gh`) represents a gold standard for CLI design that serves both human operators and AI agents effectively. Its success stems from:

- **Consistency**: Predictable patterns throughout
- **Structure**: Machine-readable output everywhere
- **Power**: Complete API access when needed
- **Simplicity**: Context awareness reduces complexity
- **Safety**: Clear boundaries and audit trails
- **Extensibility**: Aliases, extensions, and raw API access

For AI agents specifically, `gh` excels because it:
1. Provides complete functionality without requiring browser/GUI
2. Outputs structured JSON that's trivial to parse
3. Supports non-interactive operation completely
4. Has predictable error handling and exit codes
5. Includes powerful filtering/templating built-in
6. Offers direct API access for any edge case

These patterns should be studied and adopted by any CLI tool intended for automation or agent use.

## Sources

1. [Automate GitHub workflows with AI agents and the GitHub CLI](https://elite-ai-assisted-coding.dev/p/gh-for-agentic-github)
2. [gh api - GitHub CLI Manual](https://cli.github.com/manual/gh_api)
3. [gh exit codes - GitHub CLI Manual](https://cli.github.com/manual/gh_help_exit-codes)
4. [Getting Started with GitHub CLI (gh)](https://blogs.reliablepenguin.com/2025/12/11/getting-started-with-github-cli-gh-github-from-your-terminal)
5. [GitHub CLI - GitHub and command line in 2025](https://www.boxpiper.com/posts/github-cli/)
6. [GitHub CLI Manual](https://cli.github.com/manual/)
7. [gh auth - GitHub CLI Manual](https://cli.github.com/manual/gh_auth)
8. [gh completion - GitHub CLI Manual](https://cli.github.com/manual/gh_completion)
9. [gh repo view - GitHub CLI Manual](https://cli.github.com/manual/gh_repo_view)
10. [GitHub: top commands in gh, the official GitHub CLI](https://adamj.eu/tech/2025/11/24/github-top-gh-cli-commands/)
11. [GitHub CLI Tutorial: Complete Guide to gh Commands | Codecademy](https://www.codecademy.com/article/github-cli-tutorial)
12. [Disable interactive mode using env var or flag · Issue #1739 · cli/cli](https://github.com/cli/cli/issues/1739)
13. [CLI Best Practices Collection](https://github.com/arturtamborski/cli-best-practices)
14. [How to use the command 'gh api' (with examples)](https://commandmasters.com/commands/gh-api-common/)
15. [GitHub API using Command Line](https://github-app-tutorial.readthedocs.io/en/latest/gh-api-cmd-line.html)

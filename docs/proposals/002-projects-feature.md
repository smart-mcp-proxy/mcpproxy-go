# RFC-002: Profiles and Projects

**Status**: Draft
**Created**: 2025-12-19
**Related Issues**: #55 (Projects/Profiles Support)

---

## Summary

This proposal introduces two approaches for managing different sets of MCP servers:

1. **Profiles** (simpler) - Named server groups stored globally, switched explicitly
2. **Projects** (alternative) - Directory-based config in `.mcpproxy/`, git-checkable, with local scripts

Users can choose one approach or combine both.

---

## Problem Statement

From Issue #55:

1. **Directory Context Problem**: MCP servers like ast-grep-mcp assume they operate from a specific directory. When multiple projects use the same proxy, servers execute in the wrong context.

2. **Access Control Problem**: Private corporate MCP servers shouldn't be available to all projects—only those within the company's organization should access them.

3. **Cross-Project Confusion**: Users cannot easily restrict which servers apply to specific projects without running multiple proxy instances.

---

## Comparison: Profiles vs Projects

| Aspect | Profiles | Projects |
|--------|----------|----------|
| **Storage** | `~/.mcpproxy/profiles/` (global) | `.mcpproxy/` in repo |
| **Selection** | Explicit: `--profile work` | Auto-detect from CWD |
| **Git-friendly** | No | Yes |
| **Team sharing** | No (each dev configures) | Yes (commit to repo) |
| **Local scripts** | Global only | Project-local, agent-editable |
| **MCP endpoint** | `/profiles/{name}/mcp` | `/projects/{name}/mcp` |
| **Complexity** | Simple | More complex |

**Recommendation**: Start with Profiles for v1.0, add Projects later if needed.

---

# Part 1: Profiles

## What is a Profile?

A **profile** is a named group of MCP servers. Users switch between profiles to change which servers are active.

```
~/.mcpproxy/
├── mcp_config.json          # Global config + default servers
└── profiles/
    ├── work.json            # Work profile
    ├── personal.json        # Personal profile
    └── minimal.json         # Minimal set for testing
```

## Profile Configuration

### `~/.mcpproxy/profiles/work.json`

```json
{
  "name": "work",
  "description": "Corporate development servers",
  "servers": ["github-work", "jira", "confluence", "datadog"],
  "inherit_global": true,
  "exclude": ["personal-*"]
}
```

### Profile Fields

| Field | Type | Description |
|-------|------|-------------|
| `name` | string | Profile identifier (matches filename) |
| `description` | string | Human-readable description |
| `servers` | string[] | Server names to include (whitelist) |
| `inherit_global` | boolean | Include all global servers as base |
| `exclude` | string[] | Glob patterns to exclude from global |
| `env` | object | Environment variables for this profile |

## Profile Resolution

```
┌─────────────────────────────────────────────────────────────┐
│ Global Config (mcp_config.json)                             │
│   servers: [github-work, github-personal, jira, confluence, │
│             datadog, personal-gitlab, home-assistant]       │
└─────────────────────────────────────────────────────────────┘
                              │
                              ▼
┌─────────────────────────────────────────────────────────────┐
│ Profile: "work"                                             │
│   inherit_global: true                                      │
│   exclude: ["personal-*", "home-*"]                         │
└─────────────────────────────────────────────────────────────┘
                              │
                              ▼
┌─────────────────────────────────────────────────────────────┐
│ Effective Servers                                           │
│   [github-work, jira, confluence, datadog]                  │
│                                                             │
│   Excluded: github-personal, personal-gitlab, home-assistant│
└─────────────────────────────────────────────────────────────┘
```

## CLI Commands

```bash
# List profiles
mcpproxy profile list
# NAME       SERVERS  DESCRIPTION
# work       4        Corporate development servers
# personal   3        Personal projects
# minimal    1        Just github for testing

# Show profile details
mcpproxy profile show work --json

# Create profile
mcpproxy profile create gaming --servers "steam,discord-bot"

# Switch active profile
mcpproxy profile switch work
# Switched to profile 'work' (4 servers active)

# Current profile
mcpproxy profile current
# work

# Delete profile
mcpproxy profile delete gaming --yes

# Use profile for single command
mcpproxy --profile personal upstream list
```

## MCP Endpoint Routing

Each profile gets its own MCP endpoint:

```
/mcp                      # Default profile (or no profile)
/profiles/work/mcp        # Work profile
/profiles/personal/mcp    # Personal profile
```

### Client Configuration

**Claude Desktop:**
```json
{
  "mcpServers": {
    "mcpproxy-work": {
      "url": "http://localhost:8080/profiles/work/mcp"
    },
    "mcpproxy-personal": {
      "url": "http://localhost:8080/profiles/personal/mcp"
    }
  }
}
```

**Claude Code** (CLI):
```bash
# Uses current active profile
mcpproxy tools list

# Or specify profile
mcpproxy --profile work tools list
```

## REST API

```
GET    /api/v1/profiles              # List all profiles
GET    /api/v1/profiles/current      # Get active profile
POST   /api/v1/profiles/switch       # Switch active profile
GET    /api/v1/profiles/{name}       # Get profile details
POST   /api/v1/profiles              # Create profile
PUT    /api/v1/profiles/{name}       # Update profile
DELETE /api/v1/profiles/{name}       # Delete profile

# All endpoints accept ?profile= to override context
GET    /api/v1/servers?profile=work
GET    /api/v1/tools?profile=personal
```

## Web UI

- Profile selector dropdown in sidebar
- Profile management page (`/ui/profiles`)
- Current profile indicator
- Quick switch buttons

## Environment Variable

```bash
MCPPROXY_PROFILE=work mcpproxy serve
```

---

# Part 2: Projects (Alternative)

## What is a Project?

A **project** is a directory containing `.mcpproxy/` configuration. Projects are:

- **Git-checkable** - commit config to share with team
- **Auto-detected** - mcpproxy finds `.mcpproxy/` from CWD
- **Script-enabled** - local scripts that agents can edit

## When to Use Projects Instead of Profiles

| Use Case | Profiles | Projects |
|----------|----------|----------|
| Work vs Personal separation | ✅ Best | Overkill |
| Team shares same MCP setup | ❌ Manual | ✅ Commit to git |
| Agent-editable scripts | ❌ Global only | ✅ Per-project |
| Quick context switching | ✅ Fast | Slower (change dir) |
| Different servers per repo | ✅ Can work | ✅ Natural fit |

## Project Structure

```
my-project/
├── .mcpproxy/
│   ├── config.json          # Project-specific config
│   └── scripts/             # Project-local scripts (agent-editable)
│       ├── analyze.js
│       └── deploy.js
├── src/
└── package.json
```

## Project Configuration

### `.mcpproxy/config.json`

```json
{
  "project_name": "my-awesome-project",
  "inherit_global": ["github", "jira"],
  "mcpServers": [
    {
      "name": "ast-grep-project",
      "command": "npx",
      "args": ["ast-grep-mcp"],
      "working_dir": "${PROJECT_ROOT}",
      "protocol": "stdio"
    }
  ],
  "scripts": {
    "analyze": "scripts/analyze.js",
    "deploy": "scripts/deploy.js"
  }
}
```

### Project Fields

| Field | Type | Description |
|-------|------|-------------|
| `project_name` | string | Unique name (defaults to directory name) |
| `inherit_global` | string[] | Global servers to include |
| `mcpServers` | array | Project-specific servers |
| `scripts` | object | Named scripts in `scripts/` directory |
| `extends_profile` | string | Base profile to extend (optional) |

## Project Naming and Registration

Projects must be **registered** with the daemon to get an MCP endpoint:

```bash
# Initialize new project (creates .mcpproxy/ + registers)
cd ~/repos/my-project
mcpproxy project init
# Created .mcpproxy/config.json
# Registered project 'my-project'
# MCP endpoint: /projects/my-project/mcp

# With custom name
mcpproxy project init --name acme-frontend
# Registered project 'acme-frontend'
# MCP endpoint: /projects/acme-frontend/mcp

# Register existing project
mcpproxy project register /path/to/project --name custom-name

# List registered projects
mcpproxy project list
# NAME              PATH                           ENDPOINT
# my-project        /Users/user/repos/my-project   /projects/my-project/mcp
# acme-frontend     /Users/user/repos/frontend     /projects/acme-frontend/mcp
```

### Naming Rules

1. **Default**: Directory name containing `.mcpproxy/`
2. **Override**: `project_name` in config.json
3. **Collision**: Error if name already registered, must use `--name` to override
4. **Format**: lowercase alphanumeric + hyphens, max 64 chars

## MCP Endpoint Routing

```
/mcp                           # Default (no project context)
/projects/{name}/mcp           # Project-specific endpoint
```

### Client Configuration

```json
{
  "mcpServers": {
    "mcpproxy-my-project": {
      "url": "http://localhost:8080/projects/my-project/mcp"
    }
  }
}
```

## CLI Auto-Detection

When running CLI from a project directory, mcpproxy auto-detects:

```bash
cd ~/repos/my-project
mcpproxy upstream list
# Auto-detected project: my-project
# Lists project-specific servers
```

## Project Scripts

Scripts in `.mcpproxy/scripts/` can be:
- Executed via CLI or code_execution
- Edited by AI agents (Claude Code)
- Hot-reloaded on change

```bash
# Run project script
mcpproxy run analyze --input '{"target": "src/"}'

# List available scripts
mcpproxy run --list
# analyze  - scripts/analyze.js
# deploy   - scripts/deploy.js
```

Scripts have access to project context:
```javascript
// .mcpproxy/scripts/analyze.js
const projectRoot = input.PROJECT_ROOT;
const results = call_tool('ast-grep-project', 'search', {
  pattern: 'console.log($_)',
  path: projectRoot
});
return { findings: results.length };
```

---

# Part 3: Combining Profiles and Projects

Projects can extend profiles:

```json
// .mcpproxy/config.json
{
  "project_name": "my-project",
  "extends_profile": "work",
  "mcpServers": [
    {
      "name": "project-specific-server",
      "command": "...",
    }
  ]
}
```

Resolution order:
1. Profile servers (from `extends_profile`)
2. + Project servers (from `mcpServers`)
3. - Exclusions (from `exclude`)

---

## Implementation Priority

| Phase | Features |
|-------|----------|
| **Phase 1** | Profiles: CLI commands, config format, MCP routing |
| **Phase 2** | Profiles: REST API, Web UI selector |
| **Phase 3** | Projects: `.mcpproxy/` detection, registration, MCP routing |
| **Phase 4** | Projects: Scripts, auto-detection, extends_profile |

---

## Discussion Questions

1. **Profiles first?** Should we implement profiles only for v1.0 and defer projects?

2. **Profile storage**: Single `profiles.json` file or separate files per profile?

3. **Project registration**: Required explicit registration, or auto-register on first use?

4. **Naming collisions**: Error on duplicate names, or auto-suffix (`my-project-2`)?

5. **Scripts location**: Only in `.mcpproxy/scripts/` or also allow global `~/.mcpproxy/scripts/`?

---

## References

- Issue #55: Projects/Profiles Support
- Wrangler CLI: Project-local `wrangler.toml` pattern
- Git: `.git` directory detection pattern
- npm: `package.json` and `node_modules` patterns
- AWS CLI: Named profiles in `~/.aws/credentials`

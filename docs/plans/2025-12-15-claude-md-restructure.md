# CLAUDE.md Restructure Design

**Date**: 2025-12-15
**Status**: Approved
**Branch**: claude-md-restructure

## Problem

1. **Context bloat**: CLAUDE.md is ~780 lines, all loaded regardless of relevance
2. **Character limit warnings**: Too much content triggers Claude Code warnings
3. **Merge conflicts**: Claude updates CLAUDE.md during work (Recent Changes, HTTP APIs, etc.), causing conflicts when pulling main
4. **No routing**: Claude doesn't know where to put new learnings, defaults to dumping in root CLAUDE.md

## Solution

Restructure using Claude Code's modular memory system:
- **Root CLAUDE.md**: Slim (~60 lines), stable, with routing table
- **`.claude/rules/`**: Cross-cutting concerns with `paths:` frontmatter for conditional loading
- **Per-directory CLAUDE.md**: Deep implementation details, loaded when working in that directory
- **CLAUDE.local.md**: Gitignored, branch-specific volatile notes

## Structure

```
CLAUDE.md                              # ~60 lines: overview, essential commands, routing table
CLAUDE.local.md                        # Gitignored: branch notes, active work (volatile)

.claude/rules/
├── testing.md                         # paths: **/*_test.go, tests/**, scripts/*test*
├── security.md                        # paths: internal/quarantine/**, **/isolation/**
└── development.md                     # No paths (always loaded): workflow, commits

cmd/mcpproxy/CLAUDE.md                 # CLI commands, socket, exit codes
cmd/mcpproxy-tray/CLAUDE.md            # State machine, process monitoring
internal/oauth/CLAUDE.md               # OAuth flows, PKCE, token refresh
internal/httpapi/CLAUDE.md             # REST endpoints, SSE, OpenAPI
internal/server/CLAUDE.md              # MCP protocol, tool routing
internal/runtime/CLAUDE.md             # Event bus, lifecycle
internal/upstream/CLAUDE.md            # 3-layer client architecture
frontend/CLAUDE.md                     # Vue/TypeScript frontend
```

## Root CLAUDE.md Design

~60 lines containing:
- Project overview (what MCPProxy is)
- Essential commands (`make build`, `./scripts/test-api-e2e.sh`, etc.)
- File locations (config, logs, database)
- **Routing table**: tells Claude where to put updates

### Routing Table

| What changed | Update location |
|--------------|-----------------|
| CLI commands, socket comm | `cmd/mcpproxy/CLAUDE.md` |
| Tray state machine, monitoring | `cmd/mcpproxy-tray/CLAUDE.md` |
| OAuth flows, tokens | `internal/oauth/CLAUDE.md` |
| HTTP API, SSE, endpoints | `internal/httpapi/CLAUDE.md` |
| MCP protocol, tool routing | `internal/server/CLAUDE.md` |
| Runtime, event bus | `internal/runtime/CLAUDE.md` |
| Upstream client architecture | `internal/upstream/CLAUDE.md` |
| Security (quarantine, TPA, docker) | `.claude/rules/security.md` |
| Testing patterns, commands | `.claude/rules/testing.md` |
| Architecture principles | `.specify/memory/constitution.md` |
| Branch-specific work notes | `CLAUDE.local.md` (gitignored) |

## Rules Files

### `.claude/rules/testing.md`
- Path-scoped: `**/*_test.go, tests/**, scripts/*test*`
- Quick commands, E2E requirements, test patterns

### `.claude/rules/security.md`
- Path-scoped: `internal/quarantine/**, **/isolation/**`
- Quarantine system, API key auth, Docker isolation

### `.claude/rules/development.md`
- No paths (always loaded)
- Pre-commit workflow, commit format, conventions

## Per-Directory Files

Each contains ~20-40 lines of focused, implementation-specific details:
- Key files and their purposes
- Important patterns/flows
- Debugging commands specific to that area

## Speckit Integration

The `.specify/memory/constitution.md` currently says "Update CLAUDE.md if architectural principles change" (line 245). This will be updated to follow the routing table instead.

## Benefits

1. **Root CLAUDE.md stays stable** → no merge conflicts
2. **Volatile content** → gitignored `CLAUDE.local.md`
3. **Path-scoped rules** → conditional loading reduces context
4. **Per-directory files** → deep details load only when relevant
5. **Routing table** → Claude knows where to put updates

## Migration

1. Extract content from existing CLAUDE.md into appropriate locations
2. Create routing table in root CLAUDE.md
3. Add CLAUDE.local.md to .gitignore
4. Update constitution.md routing directive

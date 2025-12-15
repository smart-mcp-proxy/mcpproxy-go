# CLAUDE.md

## Project Overview

MCPProxy is a Go-based MCP proxy for AI agents. Provides intelligent tool discovery, token savings, and security quarantine against malicious servers.

**Architecture**: Core server (`mcpproxy`) + Tray GUI (`mcpproxy-tray`)

## Essential Commands

```bash
make build                        # Build everything
./scripts/test-api-e2e.sh         # Required before commits
./scripts/run-linter.sh           # Lint check
./scripts/run-all-tests.sh        # Full test suite
./mcpproxy serve                  # Start core server
./mcpproxy-tray                   # Start tray app
```

## File Locations

- **Config**: `~/.mcpproxy/mcp_config.json`
- **Database**: `~/.mcpproxy/config.db`
- **Logs**: `~/.mcpproxy/logs/` (macOS: `~/Library/Logs/mcpproxy/`)

## Quick Debugging

```bash
mcpproxy doctor                   # Health check
mcpproxy upstream list            # Server status
tail -f ~/Library/Logs/mcpproxy/main.log  # Watch logs
```

## Documentation Updates

When you learn something during development, update the appropriate location:

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
| Development workflow, commits | `.claude/rules/development.md` |
| Architecture principles | `.specify/memory/constitution.md` |
| Branch-specific work notes | `CLAUDE.local.md` (gitignored) |

**Never add to this file** unless it's a new essential command or file location.

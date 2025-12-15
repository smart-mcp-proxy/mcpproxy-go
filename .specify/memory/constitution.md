# MCPProxy Constitution

<!--
  SYNC IMPACT REPORT

  Version Change: Initial → 1.0.0

  Modified Principles:
  - NEW: I. Performance at Scale
  - NEW: II. Actor-Based Concurrency
  - NEW: III. Configuration-Driven Architecture
  - NEW: IV. Security by Default
  - NEW: V. Test-Driven Development (TDD)
  - NEW: VI. Documentation Hygiene

  Added Sections:
  - Core Principles (6 principles)
  - Architecture Constraints (4 constraints)
  - Development Workflow (4 workflow rules)
  - Governance

  Removed Sections: N/A (initial version)

  Templates Requiring Updates:
  ✅ plan-template.md - Already contains Constitution Check section
  ✅ spec-template.md - Requirements align with constitution principles
  ✅ tasks-template.md - Task structure supports testing and documentation principles

  Follow-up TODOs: None
-->

## Core Principles

### I. Performance at Scale

MCPProxy MUST handle up to 1,000 tools efficiently without degradation in search, indexing, or tool routing performance.

**Rules**:
- BM25 search MUST return results in <100ms for queries across 1,000 tools
- Tool indexing MUST complete in the background without blocking API requests
- Connection pooling and reuse MUST be employed for upstream MCP servers
- Memory usage MUST remain stable under sustained load (no memory leaks)

**Rationale**: AI agents need fast, reliable tool discovery across hundreds of MCP servers. Blocking operations or memory issues break the user experience and limit practical deployment scale.

### II. Actor-Based Concurrency

All concurrent operations MUST follow actor-based patterns using Go idioms (goroutines + channels). Locks and mutexes MUST be avoided except where proven necessary through benchmarking.

**Rules**:
- Use manager/supervisor pattern: one goroutine owns each resource (e.g., upstream server connection)
- Communicate via channels, not shared memory
- Managers send events about state changes; receive commands via channels
- The main thread MUST never block on I/O or long-running operations
- Context propagation MUST be used for cancellation and timeouts

**Rationale**: Lock-based concurrency introduces deadlocks, race conditions, and performance bottlenecks. Actor-based patterns provide clearer ownership, easier debugging, and better scalability.

### III. Configuration-Driven Architecture

The core application MUST store all settings in a configuration file (`mcp_config.json`) to enable headless and remote deployment.

**Rules**:
- All runtime behavior MUST be configurable via JSON config file
- Environment variables MAY override config values for deployment flexibility
- Configuration changes MUST trigger automatic reload without restart (hot-reload)
- Default values MUST be sensible and documented
- The tray application MUST NOT maintain its own state; it acts as a UI controller that reads/writes core config via REST API

**Rationale**: Remote deployment, automation, and containerization require configuration-driven systems. Tray-specific state would break headless mode and create sync issues.

### IV. Security by Default

MCPProxy MUST protect users from malicious MCP servers through automatic quarantine, transparency, and isolation.

**Rules**:
- All new MCP servers added via LLM tools MUST be quarantined by default
- Quarantined servers MUST NOT execute tools until manually approved
- Tool calls MUST be logged with full transparency (request + response)
- stdio MCP servers MUST run in Docker containers by default (unless explicitly disabled)
- localhost-only binding MUST be the default for network listeners (127.0.0.1)
- API key authentication MUST be enabled by default and auto-generated if not provided

**Rationale**: Tool Poisoning Attacks and malicious MCP servers pose real security risks. Defense-in-depth with quarantine + logging + isolation protects users without requiring security expertise.

### V. Test-Driven Development (TDD)

All features and bug fixes MUST include tests. Tests MUST be written before implementation (red-green-refactor cycle) when feasible.

**Rules**:
- Unit tests MUST cover core logic in `internal/` packages
- Integration tests MUST cover API endpoints and MCP protocol flows
- E2E tests MUST verify real MCP server interactions
- All tests MUST pass before merging to main branch
- Code MUST pass `golangci-lint` without errors
- Test coverage SHOULD increase with each PR (no coverage regressions)

**Rationale**: Go's excellent testing tools make TDD practical. Tests prevent regressions, document behavior, and enable confident refactoring.

### VI. Documentation Hygiene

After adding a feature or fixing a bug, developers MUST update:

**Rules**:
- Tests (unit, integration, E2E as applicable)
- `CLAUDE.md` (if architecture or commands change)
- `README.md` (if user-facing behavior changes)
- Code comments (for complex logic or non-obvious decisions)
- API documentation (if REST endpoints or MCP protocol changes)

**Rationale**: Documentation drift causes confusion, wasted time, and incorrect behavior. Treating docs as first-class artifacts keeps the project maintainable.

## Architecture Constraints

### Separation of Concerns: Core + Tray Split

**Rule**: The core server (`mcpproxy`) and tray application (`mcpproxy-tray`) MUST remain separate binaries with clear responsibilities.

**Core Responsibilities**:
- MCP protocol implementation
- REST API endpoints
- Tool indexing and search
- Upstream server connection management
- Configuration file storage

**Tray Responsibilities**:
- System tray UI rendering
- Real-time status updates via SSE
- User commands relayed to core via REST API
- Auto-launching core server if not running

**Rationale**: Headless deployment requires a core that runs without GUI. Separate binaries enable flexible deployment (server-only, GUI-only, or both).

### Event-Driven Updates

**Rule**: State changes MUST propagate via the event bus, not polling or file watching.

**Implementation**:
- Runtime operations emit events (`servers.changed`, `config.reloaded`)
- Server forwards events to SSE endpoint (`/events`)
- Tray subscribes to SSE for real-time UI updates
- Web UI subscribes to SSE for dashboard updates

**Rationale**: Event-driven architecture eliminates polling overhead, reduces latency, and provides a single source of truth for state changes.

### Domain-Driven Design (DDD) Layering

**Rule**: Code MUST follow DDD layering with separation between domain logic and infrastructure.

**Layers**:
- **Domain**: Core business logic (tool indexing, quarantine rules, search algorithms) in `internal/index/`, `internal/cache/`
- **Application**: Use cases and orchestration in `internal/runtime/`
- **Infrastructure**: HTTP server, storage, logging in `internal/server/`, `internal/storage/`, `internal/logs/`
- **Presentation**: REST API endpoints in `internal/httpapi/`

**Rationale**: Clear layering prevents domain logic from leaking into HTTP handlers or database code, making tests easier and refactoring safer.

### Upstream Client Modularity (3-Layer Design)

**Rule**: MCP client implementations MUST follow the 3-layer design in `internal/upstream/`.

**Layers**:
- **Core** (`core/`): Stateless, transport-agnostic MCP protocol client
- **Managed** (`managed/`): Production client with state management, retries, connection pooling
- **CLI** (`cli/`): Debug client with enhanced logging for manual tool testing

**Rationale**: Layered clients enable reuse of core protocol logic while supporting different use cases (production vs debugging) without code duplication.

## Development Workflow

### Pre-Commit Quality Gates

**Rule**: Before committing changes, developers MUST run:

```bash
# Linting
./scripts/run-linter.sh

# Unit tests
go test ./internal/... -v

# E2E tests (API)
./scripts/test-api-e2e.sh

# Full test suite (for major changes)
./scripts/run-all-tests.sh
```

**Rationale**: Automated quality gates prevent broken code from entering the main branch and reduce review overhead.

### Error Handling Standards

**Rule**: All errors MUST follow Go idioms:

- Use `error` return values (no exceptions)
- Wrap errors with context using `fmt.Errorf("context: %w", err)`
- Handle context cancellation properly in long-running operations
- Use structured logging (zap) for error details
- Return specific exit codes from the core binary (see `cmd/mcpproxy/exit_codes.go`)

**Rationale**: Consistent error handling improves debuggability and enables automated error recovery (e.g., tray app retry logic).

### Git Commit Discipline

**Rule**: Commits MUST be atomic, descriptive, and follow conventional commits format:

- `feat:` for new features
- `fix:` for bug fixes
- `docs:` for documentation changes
- `refactor:` for code restructuring without behavior changes
- `test:` for test additions or fixes
- `chore:` for build, CI, or tooling changes

**Issue References**:
- ✅ **Use**: `Related #123` - Links commits to issues without auto-closing
- ❌ **Do NOT use**: `Fixes #123`, `Closes #123`, `Resolves #123` - These auto-close issues on merge
- **Rationale**: Issues should only be closed manually after verification and testing in production, not automatically on merge

**Co-Authorship**:
- ❌ **Do NOT include**: `Co-Authored-By: Claude <noreply@anthropic.com>`
- ❌ **Do NOT include**: "🤖 Generated with [Claude Code](https://claude.com/claude-code)"
- **Rationale**: Commit authorship should reflect the human contributors, not the AI tools used

**Rationale**: Clear commit history enables better code review, easier rollbacks, and automated changelog generation.

### Branch Strategy

**Rule**: Development follows a two-branch strategy:

- **`main` branch**: Stable releases (production-ready)
- **`next` branch**: Prerelease builds with latest features
- Feature branches merge to `next`, then `next` merges to `main` after validation

**Rationale**: Allows users to test cutting-edge features while maintaining a stable release channel.

## Governance

This constitution supersedes all other development practices and guidelines. Any deviation MUST be justified, documented, and approved through a Pull Request.

**Amendment Procedure**:
1. Propose changes via Pull Request to `.specify/memory/constitution.md`
2. Increment `CONSTITUTION_VERSION` following semantic versioning
3. Update `LAST_AMENDED_DATE` to the merge date
4. Propagate changes to dependent templates (`plan-template.md`, `spec-template.md`, `tasks-template.md`)
5. Update documentation per the routing table in `CLAUDE.md` (architecture → constitution, features → per-directory CLAUDE.md, cross-cutting → `.claude/rules/`)

**Compliance Review**:
- All Pull Requests MUST verify compliance with this constitution
- Complexity introduced MUST be justified in the plan.md "Complexity Tracking" section
- New abstractions (repositories, factories, etc.) MUST demonstrate clear value over simpler alternatives

**Versioning Policy**:
- **MAJOR** (X.0.0): Backward-incompatible principle removal or redefinition
- **MINOR** (0.X.0): New principle added or materially expanded guidance
- **PATCH** (0.0.X): Clarifications, wording fixes, non-semantic refinements

**Version**: 1.1.0 | **Ratified**: 2025-11-08 | **Last Amended**: 2025-11-08

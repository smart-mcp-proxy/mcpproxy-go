# Development Workflow

## Pre-Commit Checklist

1. `./scripts/run-linter.sh`
2. `./scripts/test-api-e2e.sh`
3. If REST endpoints changed: `./scripts/verify-oas-coverage.sh`

## Commit Format

Use conventional commits:
- `feat:` - New features
- `fix:` - Bug fixes
- `docs:` - Documentation
- `refactor:` - Code restructuring
- `test:` - Test additions/fixes
- `chore:` - Build, CI, tooling

## Commit Guidelines

**Issue References**:
- Use: `Related #123` (links without auto-closing)
- Avoid: `Fixes #123`, `Closes #123` (auto-closes on merge)

**Do NOT include**:
- `Co-Authored-By: Claude <noreply@anthropic.com>`
- "Generated with Claude Code"

## Branch Strategy

- `main` - Stable releases
- `next` - Prerelease builds
- Feature branches merge to `next`, then `next` to `main`

## Error Handling Standards

- Use `error` return values (no exceptions)
- Wrap with context: `fmt.Errorf("context: %w", err)`
- Handle context cancellation in long-running operations
- Use structured logging (zap)

## File Organization

- Place features in appropriate `internal/` subdirectories
- Follow Go package naming conventions
- Unit tests alongside source (`*_test.go`)

## Building

```bash
make build                                                    # Build everything
go build -o mcpproxy ./cmd/mcpproxy                          # Core only
GOOS=darwin CGO_ENABLED=1 go build -o mcpproxy-tray ./cmd/mcpproxy-tray  # Tray
```

## Running

Kill existing instances first (database lock):
```bash
pkill mcpproxy && ./mcpproxy serve
```

# Repository Guidelines

## Project Structure & Module Organization
- `cmd/mcpproxy` hosts the core daemon entrypoint; `cmd/mcpproxy-tray` builds the CGO-based tray binary.
- Runtime logic, HTTP handlers, storage, and upstream orchestration live in `internal/` (`internal/runtime`, `internal/httpapi`, `internal/upstream`, `internal/storage`).
- Vue/Tailwind assets are kept in `frontend/`; bundled static output is embedded from `web/`.
- Integration and regression assets reside under `tests/` and `scripts/`; Go unit tests sit beside source files.

## Build, Test, and Development Commands
- `make build` — compile the proxy and tray binaries for the host platform.
- `go build ./cmd/mcpproxy` — quick core rebuild; `GOOS=darwin CGO_ENABLED=1 go build ./cmd/mcpproxy-tray` validates the macOS tray target.
- `npm install && npm run build` inside `frontend/` — install web deps and emit production assets.
- `scripts/run-web-smoke.sh` — spin up the proxy and execute the Playwright smoke suite.
- `scripts/verify-api.sh` — exercise the `/api/v1` REST surface with curated curl calls.

## Coding Style & Naming Conventions
- Run `gofmt` (tabs, goimports defaults) on all Go sources; prefer descriptive package-level names (`runtime`, `upstream`, `storage`).
- TypeScript/Vue files follow Prettier defaults (`npm run lint`); components use `PascalCase.vue`, composables use `useThing.ts`.
- Configuration and DTO structs live in shared packages (`internal/contracts`, `internal/httpapi`); avoid anonymous maps between layers.

## Testing Guidelines
- Unit tests: `go test ./internal/...` (set `GOCACHE`/`GOMODCACHE` to workspace paths when sandboxed).
- Targeted suites: `go test ./internal/server -run TestMCP -v` for lifecycle checks; `npm run test:unit` for frontend units.
- End-to-end: `scripts/run-e2e-tests.sh` after ensuring `@modelcontextprotocol/server-everything` is warmed and reachable.
- Name Go tests `TestFeatureScenario`; snapshot or fixture data belong under `tests/` with explicit prefixes.

## Commit & Pull Request Guidelines
- Use concise, imperative commit messages (e.g., `Fix upstream disable locking`); avoid AI co-author tags.
- PR descriptions should summarize impact, list verification commands, and link relevant P# items or issues; attach UI screenshots or log excerpts when behavior changes.
- Keep change scopes bounded to one surface (runtime vs. tray vs. web); document follow-up ideas in `IMPROVEMENTS.md`.

## Security & Configuration Tips
- Never hardcode secrets; load them via the tray secure store or environment lookups in `internal/secret`.
- When editing configs, prefer `runtime.SaveConfiguration()` flows so disk state and in-memory state stay aligned; regenerated files land in `~/.mcpproxy/`.

## Active Technologies
- Go 1.24 (toolchain go1.24.10) + BBolt (storage), Chi router (HTTP), Zap (logging), regexp (stdlib), existing ActivityService (026-pii-detection)
- BBolt database (`~/.mcpproxy/config.db`) - ActivityRecord.Metadata extension (026-pii-detection)

## Recent Changes
- 026-pii-detection: Added Go 1.24 (toolchain go1.24.10) + BBolt (storage), Chi router (HTTP), Zap (logging), regexp (stdlib), existing ActivityService

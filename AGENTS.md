# AGENTS.md — Coordination Notes for Automation & LLM Contributors

## Mission Snapshot (January 2025)
- **Primary objective**: Stabilise the core `mcpproxy` daemon (REST/SSE + embedded web UI) and ship the separate tray binary. Everything else is secondary until the split is reliable on macOS.
- **Source of truth for sequencing**: `REFACTORING.md`. The current work queue is focused on P3–P6 (core/tray separation + API + embedded UI).
- **Module integrity rule**: Treat each component as a testable boundary the team can lock. Avoid broad edits that span runtime, HTTP, storage, tray, and UI in one pass; reinforce boundaries with tests before handing off.

## Roles & Focus Areas
| Role | Scope | Key Paths |
| ---- | ----- | --------- |
| Core Runtime | MCP proxy server, storage/index, upstream lifecycle | `cmd/mcpproxy`, `internal/{server,upstream,storage,index,cache}` |
| HTTP/API Layer | REST `/api/v1`, SSE events | `internal/httpapi`, HTTP wiring in `internal/server/server.go` |
| Web UI | Vue/Tailwind frontend embedded with `go:embed` | `frontend/`, `web/` |
| Tray App | Native systray binary (CGO-on) | `cmd/mcpproxy-tray`, `internal/tray/` |
| Release & Packaging | CI/CD split, DMG, updater safety | `.github/`, `scripts/` |

Each task should declare which role it touches and avoid cross-role churn unless explicitly planned.

## Collaboration Playbook
1. **Start with REFACTORING.md** – confirm you are advancing the active milestone (currently P3–P6). If unsure, leave a note in `IMPROVEMENTS.md` instead of landing speculative code.
2. **Touch one surface at a time** – e.g. if you are improving the tray API client, limit edits to `cmd/mcpproxy-tray/internal/api` and related contracts; do not reshape `internal/server` in the same PR.
3. **Prefer shared contracts** – when exchanging data between core ↔ tray ↔ web, add/update DTOs in a shared package instead of sprinkling `map[string]interface{}` (see the suggestion in `IMPROVEMENTS.md`).
4. **Lock mature modules with tests** – before modifying a bounded context, run or add its targeted tests. If a feature must stay intact, write a quick unit/contract test first.
5. **Document intent** – update `IMPROVEMENTS.md` or link to the relevant P# item whenever you introduce a structural change. Future agents rely on those breadcrumbs.

### Standard LLM Generate → Verify Loop
- **Generate**: Draft the smallest reasonable change within one module boundary.
- **Verify backend**: `go test` for the touched package(s); run focused suites instead of the full tree by default.
- **Verify API**: Exercise `/api/v1` endpoints with the shared `curl` scripts (or `scripts/verify-api.sh`) covering servers list, enable/disable, tool sync, and logs; legacy `/api` routes are off-limits.
- **Verify UI**: Use the Playwright smoke test (`.playwright-mcp/web-smoke.spec.ts`) via `scripts/run-web-smoke.sh`, which boots a local proxy and runs `npx playwright test`. Append `--show-report` if you want the HTML report server for manual inspection. Record failures as TODOs before exiting and keep artefacts under `tmp/web-smoke-artifacts`.
- **Report**: Capture results in the PR/commit or the relevant doc; do not skip verification steps without noting why.

### MCP Tooling Expectations
- Warm a reusable instance of `@modelcontextprotocol/server-everything` before running CLI or E2E suites; point helpers to the cached binary/socket instead of invoking `npx` per test.
- Keep the Playwright smoke spec (`.playwright-mcp/web-smoke.spec.ts`) green; extend it when new UI affordances land and regenerate fixtures as part of the same change.
- When bouncing MCP servers, monitor `logs/mcpproxy.log` for startup regressions and add findings to `IMPROVEMENTS.md` before shipping fixes.

## Test & Build Checklist Before Handoffs
- Core changes: `go test ./internal/...` plus targeted e2e (`go test ./internal/server -run TestMCP -v`).
- Tray changes: `GOOS=darwin CGO_ENABLED=1 go build ./cmd/mcpproxy-tray`.
- Web changes: `npm run lint && npm run build` within `frontend/` (or `npm run test:unit` + `scripts/run-web-smoke.sh [--show-report]` if UI touched).
- API checks: run the curated `curl` suite (or `scripts/verify-api.sh`) against `/api/v1/*`; ensure no legacy `/api` endpoints remain in use.
- MCP suites: ensure the cached everything-server is alive before invoking `scripts/run-e2e-tests.sh` to avoid startup hangs.
- Release plumbing: run modified workflows locally with `act` where possible.

Skip expensive suites only with an explicit TODO in your PR/commit message.

## Communication Norms
- Use neutral, factual commit messages (no AI co-author tags).
- When blocked by missing context, leave a note in `MEMORY.md` or `IMPROVEMENTS.md` and stop—do not guess at security-sensitive behaviour.
- If unexpected filesystem changes appear (generated DB/index artifacts), pause and confirm with a human before deleting them.

Stay focused on delivering a working core daemon + tray pair; deeper hardening (P7+) comes after that foundation is solid.

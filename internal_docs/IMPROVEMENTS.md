# Architectural Improvement Suggestions

_Context_: We're midway through the core ⇄ tray split described in `REFACTORING.md`. The immediate priority is a stable core `mcpproxy` daemon (REST/SSE + embedded web UI) with a separate tray binary that talks to it over the new HTTP API. Alongside architecture and quality goals, every component should present a testable module boundary so we can lock behaviour for future LLM-driven edits. The items below focus on stabilising that architecture before chasing later phases.

## 1. Carve Out a Core Runtime Layer (High Impact, Near-Term)
- **Why**: `internal/server/server.go` combines lifecycle management, HTTP wiring, MCP tool routing, storage/index orchestration, and tray-facing status fan-out in a single 1.4k LOC type. This makes it hard to evolve the REST API and tray separately.
- **Action**: Extract a `runtime` (or `app/core`) package that owns config loading, storage/index lifecycle, upstream manager wiring, and status events. Keep HTTP concerns (`internal/httpapi`, SSE fan-out, static web) in thin adapters that depend on the runtime via interfaces.
- **Outcome**: Core logic becomes testable without HTTP, alternative interfaces (CLI helpers, future gRPC) can reuse the runtime, and the tray binary interacts with a narrow contract.

- **Why**: REST handlers (`internal/httpapi/server.go`) and the tray client (`cmd/mcpproxy-tray/internal/api/client.go`) each define their own structs, mostly as `map[string]interface{}`. This duplication causes drift whenever we add fields or modify JSON formats.
- **Action**: Introduce a shared `pkg/contracts` (name TBD) with typed DTOs for server status, logs, tool metadata, and SSE payloads. The HTTP layer should marshal these types, and generated TypeScript definitions (via `go2ts`/`swag`) can feed the Vue UI and tray bindings.
- **Outcome**: One schema for all surfaces, easier validation, and clearer evolution path for `/api/v1`.

## 3. Replace File-Watcher Feedback Loops with an Internal Event Bus
- **Why**: `Server.EnableServer`/`QuarantineServer` persist to Bolt, then rely on the tray's fsnotify watcher to rediscover changes (see `internal/server/server.go` ~lines 720-820). That introduces eventual consistency and duplicate logic in the tray managers.
- **Action**: Let the core runtime publish typed events (server added/enabled/quarantined, OAuth flow required) on an internal bus. The REST layer, SSE broadcaster, and tray bridge subscribe directly, eliminating the need for the tray to watch config files or poke at Bolt.
- **Outcome**: Immediate UI updates, fewer cross-process DB conflicts, and a cleaner path to future remote UIs.

## 4. Retire Remaining `/api` Shims in Favour of `/api/v1`
- **Why**: We now expect every client (tray + web UI) to use the new REST surface introduced during this refactor. Keeping the older handler stack alive only increases maintenance cost.
- **Action**: Delete the duplicate mux wiring for `/api`, move any still-needed helper logic under the shared contracts package, and update the tray client to rely solely on `/api/v1` endpoints (done Jan 2025; legacy handlers removed).
- **Outcome**: Single REST surface to secure, document, and test (OpenAPI, golden tests in P11) with no legacy code paths lingering.

## 5. Observation & Health Surface as Dedicated Modules
- **Why**: `REFACTORING.md` P10 calls for health/readiness, Prometheus, and traces. Today, status reporting is baked into `internal/server` and the SSE stream.
- **Action**: Introduce an `internal/observability` package that exposes health probes, metrics registration, and trace helpers. HTTP adapters simply mount the handlers. This keeps telemetry independent from the runtime refactor and avoids leaking prom/otel globals through business logic.
- **Outcome**: Cleaner layering now, easier to implement P10 without touching tray/web.

## 6. Testing & Tooling Support for the Split
- **Why**: We currently rely on end-to-end tests in `internal/server` that spin up the whole stack. As we separate binaries, we need targeted coverage.
- **Action**: Add subpackage tests around the new runtime interfaces (mocking transport/storage), and introduce contract tests that exercise the REST API + tray client over HTTP using the shared DTOs.
- **Outcome**: Confidence that the core remains functional while the tray migrates, enabling quicker iterations on both binaries.

## 7. Enforce Module Boundaries So Features Stay Locked
- **Why**: Large, monolithic packages make it easy for automated refactors to accidentally remove cross-cutting behaviour. Clear boundaries let us freeze mature areas while iterating on adjacent code.
- **Action**: Define explicit interfaces between runtime, HTTP adapters, storage/index, tray bindings, and UI. Guard each module with focused unit/contract tests and document the "public" surface so future LLM agents know what not to delete.
- **Outcome**: Safer concurrent workstreams and a path to lock high-value features through tests instead of conventions.

## 8. Lean Into LLM Generate → Verify Loops
- **Why**: Refactor velocity hinges on how quickly we can iterate with LLM help without regressing behaviour.
- **Action**: Standardise a loop where agents generate a change, then verify it with:
  - `go test` / targeted module suites for backend pieces.
  - `curl` recipes for `/api/v1` endpoints (healthy servers, enable/disable, tool search).
  - Playwright CLI scenarios (`npx playwright test .playwright-mcp/web-smoke.spec.ts`) that drive the embedded web UI (status dashboard, tool search, server detail flows).
- **Outcome**: Faster feedback for both humans and LLM contributors, higher confidence before code review, and reusable scripts for CI gating.

## 9. Pre-Boot MCP "Everything" Server for Repeatable CLI/E2E Runs
- **Why**: The MCP protocol E2Es invoke `npx @modelcontextprotocol/server-everything` for every scenario, introducing multi-minute hangs and occasional timeouts.
- **Action**: Build or download the everything server once per test session (e.g. `npm exec --yes --package @modelcontextprotocol/server-everything -- mcp-server --stdio`) and cache the executable/socket. Point the CLI wrapper and `scripts/run-e2e-tests.sh` to the warmed instance so individual tests reuse it instead of spawning `npx` repeatedly.
- **Outcome**: MCP suites finish inside CI time budgets, binary smoke tests regain reliability, and local developer loops remain fast while keeping coverage.

## 10. Automate Playwright + Curl Verification Recipes
- **Why**: Contributors frequently skip manual API/UI validation, leaving regressions for later phases.
- **Action**: Check in scripted helpers alongside the new `.playwright-mcp/web-smoke.spec.ts` run:
  - `scripts/verify-api.sh` to exercise the canonical `curl` calls (servers list, enable/disable, tool sync, status stream).
  - `scripts/run-web-smoke.sh` wrapper that boots a local server and invokes `npx playwright test .playwright-mcp/web-smoke.spec.ts`, capturing HTML/console artifacts on failure (pass `--show-report` for an interactive HTML viewer).
  - Document both scripts in `REFACTORING.md` Phase 1 so every handoff runs them by default.
- **Outcome**: API/UI verification becomes a single command, LLM agents follow a consistent workflow, and regressions surface before merges.

---

These changes keep us focused on the core goal—shipping a working core daemon + web UI plus a thinner tray app—while laying scaffolding for the later resilience/security milestones in the refactor plan.

# Phase 0 Research: Telemetry Tier 2

This document resolves the technical unknowns identified in `plan.md` and records the chosen approach for each.

## R1. Counter primitive: atomic vs locked map

**Decision**: Use `atomic.Int64` for fixed-cardinality counters and `sync.RWMutex` + `map[string]int64` for variable-cardinality counters.

**Rationale**: There are five surface counters (mcp/cli/webui/tray/unknown) and one upstream tool counter — six total fixed counters that get incremented on every request. Atomics are the lowest-overhead primitive for these. The maps for built-in tools, REST endpoints, error categories, and doctor checks have variable but small cardinality (≤30 keys each). A single short-lived `Lock`/`Unlock` around a map increment is fine because the lock is never held during I/O and the contention is bounded by request rate.

**Alternatives considered**:
- Pure `sync.Map` — has O(1) per-key cost but is hard to snapshot atomically (Range is best-effort under concurrent mutation), and the JSON snapshot needs a coherent view.
- One actor goroutine per registry — adds inbox latency on every increment and is hard to test for backpressure.
- Lock-free hash maps (e.g., `cespare/xxhash` based) — overkill for ≤30 keys.

## R2. Surface classification placement

**Decision**: Add a Chi middleware that wraps the `/api/v1` router and increments the appropriate surface counter on every request. The middleware reads `X-MCPProxy-Client`, splits on `/`, takes the prefix, and maps it through a small allow-list (`tray`, `cli`, `webui`) to enum values; everything else is `unknown`. The `/mcp` endpoint is handled separately in `internal/server/mcp.go` which calls `registry.RecordSurface(SurfaceMCP)` directly when an MCP request enters the protocol layer.

**Rationale**: Chi middleware runs once per request and has O(1) cost. Doing classification once at the boundary keeps the rest of the request path unaware of telemetry. For `/mcp`, the surface is always `mcp` regardless of any header — that classification is unconditional and fits naturally at the MCP request entry point.

**Alternatives considered**:
- A request hook on every handler — duplicates code and is easy to miss for new endpoints.
- An HTTP server interceptor at the `net/http.Handler` wrapping level — works, but Chi middleware is the project's existing convention.

## R3. REST endpoint template extraction

**Decision**: Use `chi.RouteContext(r.Context()).RoutePattern()` after Chi has matched the route. The template extraction lives in a deferred middleware that wraps `http.ResponseWriter` to also capture the status code. Unmatched requests (404 because nothing in Chi's tree matched) get the literal pattern `UNMATCHED`.

**Rationale**: Chi populates the route context lazily as it walks its trie. By the time the response is written, the pattern is set. We can't read it *before* the handler runs (it would be empty), so the middleware structure is `defer func() { record() }` after the handler returns. This is exactly how the existing request-id middleware works in `internal/httpapi/middleware.go`.

**Reference**: [Chi RouteContext docs](https://pkg.go.dev/github.com/go-chi/chi/v5#RouteContext)

**Alternatives considered**:
- Recording the raw URL with placeholder substitution via regex — fragile, leaks data, and would re-derive what Chi already knows.
- Using a middleware chain inside each route group — same logic in multiple places, easy to miss.

## R4. Status code class derivation

**Decision**: Wrap `http.ResponseWriter` with a `statusRecorder` type that implements `WriteHeader(int)` and stores the code. Default to 200 if `WriteHeader` is never called. Convert to class string with `fmt.Sprintf("%dxx", code/100)`.

**Rationale**: The standard pattern. The existing request-id middleware already wraps the writer for similar reasons; we extend the same wrapper rather than introducing a second wrapper.

## R5. Built-in tool detection

**Decision**: `internal/server/mcp.go` already has explicit handler functions for each built-in tool (`handleRetrieveTools`, `handleCallToolVariant` with variant-aware routing, etc.). Each handler gets a one-liner: `s.telemetryRegistry.RecordBuiltinTool(toolName)`. For upstream-proxied calls (the path through `handleCallToolVariant` that ends up calling an upstream server), we add `s.telemetryRegistry.RecordUpstreamTool()` after we've determined the call is not a built-in.

**Rationale**: The mapping from "is this a built-in tool" to "is this an upstream tool" is already known at the MCP routing level. We just instrument the existing branches.

**Alternatives considered**:
- Wrapping at the `mcp-go` library handler level — would be cleaner but requires hooking into a third-party library's interceptor API which mcp-go doesn't expose cleanly.
- A separate registration table mapping tool names to flags — duplicates information already encoded in the dispatch logic.

## R6. Doctor integration

**Decision**: `internal/doctor/doctor.go` returns `[]CheckResult`. Add a method `RecordDoctorRun(results []CheckResult)` on the registry that iterates the slice and increments `pass` or `fail` per `result.Name`. Call this from the doctor command handler in `cmd/mcpproxy/doctor_cmd.go` and any REST handler that runs doctor (if one exists).

**Rationale**: Doctor check names are a fixed enum from the doctor package itself, so they're safe to use as map keys without sanitization. The slice form makes it easy to pass through without coupling the registry to doctor internals.

## R7. Env var precedence and DO_NOT_TRACK semantics

**Decision**: At telemetry service construction time, evaluate the precedence chain once:
1. If `os.Getenv("DO_NOT_TRACK")` is non-empty (any value other than `""` or `"0"`) → disabled
2. Else if `os.Getenv("CI")` is `"true"` or `"1"` → disabled
3. Else if `os.Getenv("MCPPROXY_TELEMETRY")` is `"false"` → disabled
4. Else if config `telemetry.enabled` is explicitly `false` → disabled
5. Else → enabled

The result is stored in `service.enabled` and never re-checked. The heartbeat goroutine is only started if enabled.

**Rationale**: The [consoledonottrack.com](https://consoledonottrack.com/) convention says any non-empty `DO_NOT_TRACK` value should opt out, not just `1`. We honor this. CI detection follows the convention used by GitHub Actions, GitLab CI, Travis, and Circle (`CI=true`). The precedence puts the user's intent (env var) above config and above default.

**Alternatives considered**:
- Only honoring `DO_NOT_TRACK=1` — less compatible with the de-facto standard.
- Re-checking env vars on every heartbeat — pointless overhead since env doesn't change in a running process.

## R8. First-run notice — output channel and persistence

**Decision**: Print to `os.Stderr` directly (not via the zap logger) the first time `mcpproxy serve` runs without `telemetry_notice_shown=true` in the config. Persist the flag immediately after printing using the existing `runtimeConfig.SaveTelemetryConfig()` (or whatever the existing helper is — research will identify it during implementation).

**Rationale**: zap formats output as JSON or structured logs, which is wrong for a one-time human-readable banner. Stderr keeps the notice out of stdout (which is reserved for `serve`'s actual output, including any `--help-json` style machine consumers).

**Notice text** (fixed in code):

```
mcpproxy collects anonymous usage telemetry to help shape the roadmap.
Learn what's collected: https://mcpproxy.app/telemetry
Disable with: mcpproxy telemetry disable    OR    DO_NOT_TRACK=1
```

## R9. Annual ID rotation — when to check, when to persist

**Decision**: The rotation check happens inside `Snapshot()` (which is called by both the heartbeat send loop and the `show-payload` command). If `time.Since(createdAt) > 365*24*time.Hour && !createdAt.IsZero() && createdAt.Before(now)`, regenerate `anonymous_id` and reset `anonymous_id_created_at` to `now`. Persist the new values to config immediately (synchronous write), since rotation is rare (~once per year per install) and we want it to survive a process crash that happens between rotation and next heartbeat.

**Clock skew handling**: If `createdAt` is in the future (clock rolled back), do not rotate — `createdAt.Before(now)` guards this.

**Legacy installs**: If `createdAt.IsZero()`, do not rotate; instead, set `createdAt = now` and persist. This means a legacy install gets one extra year before its first rotation, which is acceptable.

## R10. Upgrade funnel — write timing

**Decision**: `last_reported_version` is updated in `Service.send()` inside the success branch, only after `resp.StatusCode/100 == 2`. The persisted value is read at `Snapshot()` time into the payload. If the send fails for any reason (network error, non-2xx status, marshal error), the persisted value is unchanged.

**Rationale**: This guarantees that intermittent send failures don't silently lose the upgrade signal. The receiver will see `previous_version = vN` for as many heartbeats as it takes to land one successfully, which is the correct behavior.

## R11. Schema versioning

**Decision**: Add a top-level `schema_version: 2` integer field to the heartbeat payload. v1 payloads have no such field, so the absence implies v1.

**Rationale**: Backend ingester needs to know which fields to expect. Forward compatibility: if we ever ship Tier 3, increment to 3. The receiver can route by `schema_version`.

## R12. Swift tray HTTP client header

**Decision**: Add `request.setValue("tray/\(version)", forHTTPHeaderField: "X-MCPProxy-Client")` to the central URLSession request builder in `native/macos/MCPProxy/`. Identify the file at implementation time via grep for `URLRequest` and `URLSession`.

**Rationale**: A single point of enforcement avoids per-call duplication. Verifies via a Swift test if one exists, otherwise via manual header inspection during build.

## R13. Web UI fetch wrapper

**Decision**: The web UI in `frontend/src/api/client.ts` (or equivalent) likely already wraps `fetch()` for adding the API key. Add `headers: { ..., 'X-MCPProxy-Client': 'webui/<build version>' }` to that wrapper. The build version comes from a `__APP_VERSION__` Vite define that the existing build setup may already provide.

**Rationale**: Single point of enforcement, mirrors the Swift change.

## R14. CLI HTTP client header

**Decision**: `internal/cliclient/client.go` (or wherever the CLI's HTTP client is defined) gets a request transport wrapper that sets `X-MCPProxy-Client: cli/<version>` on every request. The version comes from `version.AppVersion` (or whatever constant the existing code uses).

**Rationale**: Same single-point-of-enforcement pattern.

## R15. Forbidden-substrings test

**Decision**: Add a test in `internal/telemetry/payload_privacy_test.go` that constructs a fully populated payload (all counters non-zero, every flag set, every error category present, every doctor check present) and asserts the rendered JSON contains none of:
- `localhost`, `127.0.0.1`, `192.168.`, `10.0.`
- `/Users/`, `/home/`, `C:\\`
- `Bearer `, `apikey=`, `password`, `secret`
- `error: `, `failed: `, `Error:` (any uppercase Error followed by colon)
- A canary upstream server name set in the test fixture

**Rationale**: Catches accidental privacy regressions in one place. Cheap to maintain.

## R16. Personal vs server edition build verification

**Decision**: Both `go build ./cmd/mcpproxy` and `go build -tags server ./cmd/mcpproxy` are run as part of the implementation tasks. CI already does this; the local task list mirrors it.

**Rationale**: Constitution Principle III implicitly requires both editions to remain functional; a verification step makes that explicit.

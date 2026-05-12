# Implementation Plan: Local Launcher for HTTP / SSE upstreams

**Branch**: `046-local-launcher-for-http-sse` | **Date**: 2026-05-10 | **Status**: Draft — pre-implementation plan only, no code written yet.

> **Hand-off context for a fresh session.** Everything below is what you need to pick this up from scratch. Read top-to-bottom, then jump to "Implementation" once the goal is clear.

## Where this lives + how the PR opens

This work targets the **`mcpproxy-go` upstream repository**, not the outer Halo `tools/` super-repo. `mcpproxy-go` is included into Halo as a git submodule at `tools/common/mcpproxy-go/`; all editing happens *inside* that submodule.

**Remotes in the submodule** (verified 2026-05-10):

| Remote name | URL | Role |
|---|---|---|
| `origin` | `https://github.com/HaloCollar/mcpproxy-go.git` | Halo's fork — push branches here so we can open PRs |
| `upstream` | `https://github.com/smart-mcp-proxy/mcpproxy-go.git` | The original project — PR target |

**Branch & PR mechanics:**

1. Inside the submodule (`cd tools/common/mcpproxy-go`), branch off the fork's tip — practically that means `git checkout main` then `git checkout -b 046-local-launcher-for-http-sse`. If you want to base it on upstream's tip instead, `git fetch upstream && git checkout -b 046-local-launcher-for-http-sse upstream/main` — pick whichever matches what's actually shipped at submission time; the fork tends to track upstream closely.
2. All commits for this feature land on that branch (refactor commits in Phase 0, feature commits in Phase 1, tests + docs in Phase 2 — see "Implementation phases" below; keep them as separate commits so the reviewer can read them in order).
3. Push to **HaloCollar fork**: `git push -u origin 046-local-launcher-for-http-sse`. Do NOT push to `upstream` — we don't have direct write access there.
4. Open the PR with `gh pr create --repo smart-mcp-proxy/mcpproxy-go --base main --head HaloCollar:046-local-launcher-for-http-sse` (or via the GitHub UI). Use the PR description template the upstream maintainers prefer (check `CONTRIBUTING.md` in the submodule before submitting).
5. The outer `tools/` super-repo gets a **submodule pointer bump only after the upstream PR merges**. That's a separate, tiny commit on the Halo side that just advances `tools/common/mcpproxy-go`'s SHA. Don't touch it until merge.

**This plan file itself** lives at `specs/046-local-launcher-for-http-sse/plan.md` inside the submodule, on the `046-local-launcher-for-http-sse` branch. It's part of the upstream PR's first commit so reviewers can read the design before reading the code.

## Summary

Today mcpproxy will spawn a child process for an upstream MCP server **only** when the transport is stdio. If the upstream uses `http`, `sse`, or `streamable-http` transport, mcpproxy reads `url` and connects directly — the `command` field on the server config is silently ignored.

We want to allow `command` + `url` + (`http`|`sse`|`streamable-http`) together so that mcpproxy:

1. Spawns the user's local command (e.g. `node my-server.js`, `docker run ...`, `./my-mcp-binary --port 9999`).
2. Waits for that command's HTTP/SSE endpoint at the configured `url` to come up.
3. Connects via the existing HTTP/SSE transport once the endpoint is reachable.
4. Owns the child's lifecycle: kills it on disconnect / restart / mcpproxy shutdown, captures its stdout+stderr to the per-server log.

This lets users self-host an MCP server that exposes HTTP/SSE (instead of stdio) without separately starting it before launching mcpproxy.

## Goal in one sentence

Make `{command, url, protocol: "http"|"sse"|"streamable-http"}` a first-class config combo that mcpproxy launches *and* connects to, with full lifecycle ownership.

## Why this is an implementation choice, not a protocol limitation

The MCP spec doesn't dictate process management — that's mcpproxy's job. Stdio happens to *require* spawning (you need stdin/stdout for the protocol bytes); HTTP/SSE happens to not require it (the protocol bytes flow over network), so the original implementation conflated "needs spawn" with "is stdio". Decoupling launcher from transport is the whole point of this feature.

## Codebase reconnaissance — read these before touching anything

Pre-existing files relevant to this plan. Read each at least skim-level before implementing:

| File | Why it matters |
|---|---|
| `internal/config/config.go:211-215` | `ServerConfig` already has `Command`, `Args`, `WorkingDir`, `Env`, plus `Environment *secureenv.EnvConfig`. No schema changes needed. |
| `internal/transport/http.go:409-426` | `DetermineTransportType` — the dispatch logic. Today: protocol > command > url > default-stdio. After this change: leave dispatch unchanged; the launcher activates orthogonally when command is set. |
| `internal/upstream/core/connection.go:96-160` | The transport-dispatch switch lives here (`switch c.transportType`). Inject the launcher step *before* this switch when transport != stdio AND command != "". |
| `internal/upstream/core/connection_stdio.go` | Existing stdio spawn logic — has the env-resolution, secret-injection, Docker-detection, working-dir, signal-handling patterns we want to reuse, not duplicate. |
| `internal/upstream/core/connection_http.go` | `connectHTTP` and `connectSSE`. These don't need to change — they still own the transport handshake. They just trust that the URL is reachable by the time they run. |
| `internal/upstream/core/client.go` | The Client struct. We'll add a launcher handle here so `Disconnect()` / `Restart()` can kill the child. |
| `internal/upstream/manager.go` | Manager-level reconnect / restart logic. Confirm restart goes through Disconnect → Connect; if it skips Disconnect we need to fix that so the child gets reaped. |
| `internal/logs/` | Per-server log files. Already used by stdio; child stdout/stderr should route here for `mcpproxy upstream logs <name>` to keep working. |
| `cmd/mcpproxy/upstream_cmd.go:76-81, 775+` | The `upstream restart` CLI handler. No code change expected — just verify the new behaviour is correct. |
| `docs/configuration.md`, `docs/cli-management-commands.md` | User-facing docs to update so the new combo is documented + the "command + url → stdio wins" footgun is called out. |

## Design

### Orthogonal launcher concept

A server is described by two independent concerns:

- **Transport** — how to send/receive MCP messages once a connection exists (stdio / http / sse / streamable-http). Decides which `connectXxx()` runs.
- **Launcher** *(optional)* — how the upstream process gets started. Decides whether mcpproxy spawns a child before connecting.

Today they're coupled (`stdio = always spawn`, `http/sse = never spawn`). After this change:

| `command` set? | `url` set? | `protocol` | Behaviour |
|---|---|---|---|
| yes | no | stdio (or empty/auto) | **unchanged** — stdio transport, child via stdin/stdout, no URL. |
| no | yes | http/sse/streamable-http (or empty/auto) | **unchanged** — connect to remote URL, no spawn. |
| **yes** | **yes** | **http/sse/streamable-http (explicit)** | **NEW** — spawn child, wait for URL, connect via HTTP/SSE. |
| yes | yes | empty/auto | Existing behaviour (command wins → stdio, URL ignored). **Keep this** to preserve back-compat. Document it clearly. |
| yes | yes | stdio (explicit) | Existing behaviour — stdio, URL ignored. |

The auto-detect rules in `DetermineTransportType` stay exactly as they are. The only behavioural change is "explicit http/sse/streamable-http + command set → spawn".

### Launcher API (new package)

`internal/upstream/launcher/` — new package. Roughly:

```go
package launcher

type Handle interface {
    // Stop signals the child to exit (SIGTERM → grace → SIGKILL on timeout).
    // Blocks until the child is actually reaped.
    Stop(ctx context.Context) error

    // Wait blocks until the child exits on its own. Returns the exit error.
    Wait() error

    // Done is closed when the child exits for any reason — used by the
    // connection manager to react to crashes (trigger reconnect / mark
    // server unhealthy).
    Done() <-chan struct{}
}

// Spawn launches the child described by cfg. It does NOT block on the
// URL becoming reachable — that's the caller's job (via WaitForURL).
// Returns a Handle the caller is responsible for stopping.
func Spawn(ctx context.Context, cfg SpawnConfig, log *zap.Logger) (Handle, error)

type SpawnConfig struct {
    Command    string
    Args       []string
    WorkingDir string
    Env        map[string]string             // resolved env (after secret injection)
    Environment *secureenv.EnvConfig          // for Docker-isolation decisions
    LogSink    io.Writer                     // child stdout+stderr go here
    // Docker fields, mirroring what connection_stdio.go uses today.
}

// WaitForURL polls url until it accepts a TCP connection (NOT a full HTTP
// GET — see "Health check" gotcha below). Returns when reachable or when
// timeout/ctx fires.
func WaitForURL(ctx context.Context, url string, timeout time.Duration) error
```

**Refactor before you add:** the env-resolution + Docker-detection + working-dir-resolve logic in `connection_stdio.go` should be lifted into the launcher package as the first refactor step. The stdio path then calls the same Spawn helper but pipes the child's stdin/stdout into its mcp-go transport instead of routing stdout to a log file. **Don't fork; share.**

### Connection-side wiring

In `connection.go`, before the existing transport switch:

```go
// Pseudocode — see connection.go:121 for the existing dispatch.

if c.transportType != transportStdio && c.config.Command != "" {
    c.launcher, err = launcher.Spawn(ctx, c.buildSpawnConfig(), c.logger)
    if err != nil { return err }

    waitTimeout := c.config.LauncherWaitTimeout  // new config field, default 30s
    if err := launcher.WaitForURL(ctx, c.config.URL, waitTimeout); err != nil {
        _ = c.launcher.Stop(context.Background())
        c.launcher = nil
        return fmt.Errorf("local launcher: url %s not reachable in %s: %w",
            c.config.URL, waitTimeout, err)
    }

    // Goroutine to react to unexpected child exit during steady-state.
    go c.watchLauncher()
}

switch c.transportType {
case transportStdio:     err = c.connectStdio(ctx)
case transportHTTP, transportHTTPStreamable:
                         err = c.connectHTTP(ctx)
case transportSSE:       err = c.connectSSE(ctx)
default:                 return fmt.Errorf("unsupported transport type: %s", c.transportType)
}
```

`watchLauncher()` listens on `Handle.Done()` and, if the child dies while we still have a transport-level connection, calls `Disconnect()` so the existing reconnect path kicks in.

`Disconnect()` (existing) needs one new line at the end: if `c.launcher != nil`, call `c.launcher.Stop(ctx)` with a graceful timeout and nil out the handle.

### Config schema

No mandatory schema change — `Command`, `Args`, `WorkingDir`, `Env` already exist on `ServerConfig`. Optional addition:

```go
// LauncherWaitTimeout — how long Spawn-then-WaitForURL will wait before
// declaring the launch failed. Optional; default 30s.
LauncherWaitTimeout time.Duration `json:"launcher_wait_timeout,omitempty" mapstructure:"launcher_wait_timeout"`
```

If left zero, default to 30 seconds. Keep it tight enough that misconfigured commands surface as connect failures fast.

## Gotchas — decide each one explicitly during implementation

These are the boring questions that break a launcher in the field. Pick a default for each and write it into the spec.

### 1. Port ownership

User pins the port in `url` (e.g. `http://127.0.0.1:9999/mcp`) and is responsible for making the command listen on that port. **Simple, ship this first.**

Future-work: `{port}` templating in `args` / `url` so mcpproxy can pick a free port and substitute it before spawn. Don't ship this in v1 — it doubles the spec.

### 2. Health check method

Don't use `http.Get(url)`. SSE endpoints return a streaming response that never closes; an HTTP GET may either hang or return non-2xx for a perfectly-healthy server (the SSE endpoint typically only accepts GET-with-text/event-stream-Accept).

**Do**: TCP-dial `host:port` from the URL until it accepts a connection. That proves the listener is bound; the transport-level connect will then do its own protocol handshake. Cleaner separation of concerns.

```go
// pseudo
host, port := splitURLHostPort(url)
deadline := time.Now().Add(timeout)
for time.Now().Before(deadline) {
    conn, err := net.DialTimeout("tcp", host+":"+port, 1*time.Second)
    if err == nil { conn.Close(); return nil }
    time.Sleep(200 * time.Millisecond)
}
return errors.New("url not reachable in time")
```

### 3. Process group + parent-death cleanup

If mcpproxy crashes without reaping, the child stays alive holding the port. This is the same problem stdio has — see how `connection_stdio.go` handles it today and reuse the pattern. Cross-platform notes:

- **Linux**: `syscall.SysProcAttr{Pdeathsig: syscall.SIGTERM, Setpgid: true}` makes the kernel deliver SIGTERM to the child when the parent thread dies. Cheap + reliable.
- **macOS**: No pdeathsig. Run a parent-watcher goroutine that polls `os.Getppid()` and sends SIGTERM when it changes to 1 (parent reaped). Or rely on the existing graceful-shutdown handler in `internal/runtime/` to enumerate live launchers and stop them.
- **Windows**: Use Job Objects (kill-on-close). `os/exec` doesn't help directly; use `golang.org/x/sys/windows/job` or similar.

Most stdio code today already handles this — copy that pattern wholesale.

### 4. Docker-isolation interop

`ServerConfig.Environment` (the `*secureenv.EnvConfig` field) already drives Docker isolation for stdio commands. The launcher must honour those same fields — Spawn() should pick Docker-via-`docker run` if the config asks for it, exactly the way `connection_stdio.go` does today. This is also why the refactor step is mandatory: the Docker-isolation logic must live in one place after this lands.

### 5. Restart semantics

`mcpproxy upstream restart <name>` currently calls Disconnect → Connect. After this change:

- Disconnect must Stop the launcher *and* close the transport, in that order. Wait for the child to actually exit (with a 5s kill-after-grace timeout) so the next start doesn't fight for the port.
- Connect re-runs Spawn + WaitForURL + connectXxx.

Verify the manager's reconnect loop (`internal/upstream/manager.go`) doesn't try to reconnect *during* a restart — there's an existing race-protection pattern for stdio that probably already covers this, but explicit test required.

### 6. Crash-while-connected

If the child dies after a successful transport-level connect, the transport will detect the dropped connection eventually (TCP keepalive / SSE stream EOF). But our `watchLauncher()` goroutine can detect it instantly and trigger Disconnect, which lets the existing reconnect/backoff path kick in faster.

### 7. Logging

Reuse `internal/logs/` per-server log files. Pipe `Handle.Spawn`'s `LogSink` to the same writer the stdio path uses. `mcpproxy upstream logs <name>` works for free.

Add a header line on spawn (`[launcher] starting: cmd args...`) and on exit (`[launcher] exited code=N`) so users can read the log and see where the protocol layer ends and the child process layer begins.

### 8. Config docs footgun

The existing "command without explicit protocol wins over url and forces stdio" rule is staying. After this change, users who had `{command, url}` with no protocol will keep getting stdio — which is what they got yesterday too, no behaviour change. But anyone *expecting* "command + url = launch + HTTP" needs to set `protocol: "http"` explicitly.

**Doc must spell this out** with a side-by-side table identical to the one in the "Design — Orthogonal launcher concept" section above. Burying it in prose guarantees a support issue.

## Implementation phases

### Phase 0 — Refactor (no behaviour change)

1. Extract `internal/upstream/launcher/` package.
2. Move env-resolution + Docker-detection + working-dir + signal-handling + log-piping out of `connection_stdio.go` into `launcher`.
3. `connection_stdio.go` now calls `launcher.Spawn()` then wires the resulting child's stdin/stdout into mcp-go's stdio transport.
4. All existing stdio tests must pass unchanged.

**Exit criteria**: `go test ./... -race` green; `mcpproxy upstream restart <stdio-server>` works exactly as before.

### Phase 1 — HTTP/SSE launcher

1. Add the spawn-before-transport step in `connection.go` (sketched above).
2. Add `LauncherWaitTimeout` to `ServerConfig` (default 30s).
3. Add `launcher.WaitForURL` TCP-dial helper.
4. Wire `Handle.Stop()` into `Disconnect()`.
5. Wire `watchLauncher()` into the connection lifecycle.

**Exit criteria**: a test config like

```json
{
  "name": "local-http-mcp",
  "protocol": "http",
  "url": "http://127.0.0.1:9999/mcp",
  "command": "node",
  "args": ["./examples/echo-http-server.js", "--port", "9999"],
  "working_dir": "/path/to/repo",
  "enabled": true
}
```

starts the node process, waits ~1s for the listener, connects via HTTP, and reaps the node process on `mcpproxy upstream restart local-http-mcp`.

### Phase 2 — Tests + docs

- Unit tests: `launcher.WaitForURL` (mock listener bound late, never bound, immediately bound, ctx cancelled mid-wait).
- Integration: a small Go binary in `internal/upstream/launcher/testdata/` that binds a port + serves a trivial HTTP `/mcp` endpoint. Mcpproxy spawns it, connects, calls a tool, restarts, asserts the PID changes.
- E2E: extend `scripts/test-api-e2e.sh` with a launcher-flavoured server.
- Docs:
  - `docs/configuration.md` — new section "Locally-launched HTTP/SSE servers" with the config snippet above + the back-compat table.
  - `docs/cli-management-commands.md` — restart semantics call-out.
  - `docs/architecture.md` — diagram update showing launcher as a sibling of transport.

### Phase 3 — Polish (optional, post-merge)

- `{port}` templating in args/url for ephemeral ports.
- Per-launcher health probe customization (TCP / HTTP GET path / custom command).
- Backoff for launcher restarts when the child crashes repeatedly.

## Estimated effort

- Phase 0 refactor: ~1 day. Read-heavy; mechanical extraction. Done badly it spawns subtle stdio regressions, so go slow.
- Phase 1 launcher: ~1 day. Bulk of new code is in `launcher.go` (~150 lines) + connection.go wiring (~30 lines).
- Phase 2 tests + docs: ~1 day.
- **Total**: ~3 days end-to-end, plus PR review cycles.

## Open questions to resolve before coding

These don't have an obvious right answer and should be settled in the spec, not in PR review:

1. **Should `command` + `url` + auto-protocol promote to "launch + HTTP" or stay as stdio?** Current plan: stay as stdio (back-compat). Confirm.
2. **`Handle.Stop()` grace timeout** — 2s? 5s? 10s? Default 5s with config override is reasonable.
3. **Should the launcher restart the child on crash, or always defer to mcpproxy's transport-level reconnect logic to handle it?** Plan: defer to transport-level. The launcher just dies and signals via `Done()`.
4. **Where does `Stop()` get its context from on shutdown?** Plan: use `context.Background()` with a fixed 10s deadline; mcpproxy's shutdown handler can pass a stricter ctx if it has one.

## Where to look if you forget the design halfway through

- This file.
- The codebase reconnaissance table above — every relevant file is listed.
- `internal/upstream/core/connection.go` — start at line 96 (the connect entrypoint) and follow the call graph from there.
- `internal/upstream/core/connection_stdio.go` — your reference implementation for "spawn a child + manage its lifecycle". Most of what you're building already exists here in a stdio-flavoured form.

## Definition of done

1. `{command, url, protocol: "http"}` in `mcp_config.json` launches the command, waits for the URL, connects, and is restartable / disconnectable cleanly.
2. Same for `protocol: "sse"` and `protocol: "streamable-http"`.
3. Child process is reaped on Disconnect, on Restart, on Server-disable, on mcpproxy shutdown (graceful and SIGKILL paths).
4. Child stdout/stderr lands in the per-server log; `mcpproxy upstream logs <name>` shows it.
5. All existing stdio tests still pass after the Phase 0 refactor.
6. New tests cover: successful launch, URL-never-reachable timeout, child-crashes-during-steady-state, restart, graceful-shutdown.
7. `docs/configuration.md` documents the new combo + back-compat table (inside the submodule — `docs/` here means `tools/common/mcpproxy-go/docs/`, the upstream's own docs tree).
8. CHANGELOG entry inside the submodule (whatever convention `mcpproxy-go` uses — check the repo before adding) describes the feature + the no-behaviour-change refactor.
9. PR opened from `HaloCollar:046-local-launcher-for-http-sse` → `smart-mcp-proxy:main`, passing upstream CI.
10. After PR merge: separate Halo super-repo commit advances the `tools/common/mcpproxy-go` submodule SHA. Optionally add a Halo-side changelog entry at `tools/docs/changelog/` noting that local-launcher support is now available downstream.

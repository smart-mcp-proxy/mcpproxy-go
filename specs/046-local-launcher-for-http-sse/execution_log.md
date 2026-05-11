# Execution Log — 046-local-launcher-for-http-sse

State maintained per `CLAUDE.md` autonomous-operation requirement. Each
session appends a dated entry; do not rewrite history.

## 2026-05-10 — Initial scaffold (Roman + Claude)

**Status**: Phase 0 + Phase 1 code landed in working tree (uncompiled —
sandbox network blocks `proxy.golang.org`, see end of log). Phase 2 partial.

### Files added

- `internal/upstream/launcher/launcher.go` — `Spec`, `Handle`, `Spawn`. Owns the
  child's lifecycle (Stop with SIGTERM → grace → SIGKILL fallback, Wait, Done,
  Pid). Pumps stdout+stderr line-by-line into a caller-supplied `io.Writer`,
  one Write per line so a zap-bridge sink produces one log entry per line.
- `internal/upstream/launcher/launcher_unix.go` — Setpgid + signal-the-pgroup
  for SIGTERM/SIGKILL on Linux/macOS.
- `internal/upstream/launcher/launcher_windows.go` — best-effort stubs
  (matches the existing `process_windows.go` TODO; Job Objects are a
  follow-up).
- `internal/upstream/launcher/wait.go` — `WaitForURL` does TCP-dial polling
  rather than HTTP GET (gotcha #2 in plan: SSE endpoints stream forever and
  break HTTP-GET probes).
- `internal/upstream/launcher/wait_test.go` — 6 cases (immediately bound,
  bound late, never bound, ctx-canceled, bad URLs, default-port inference).
- `internal/upstream/launcher/launcher_test.go` — 7 cases (graceful exit,
  SIGKILL fallback when SIGTERM is trapped, Done on natural exit, exit-code
  capture via `*exec.ExitError`, Stop idempotency, log sink capture, nil
  guards).
- `internal/upstream/launcher/integration_test.go` — full Spawn + WaitForURL
  with a python-listener subprocess; skips when python3 is missing or on
  Windows. (Pure Go testdata helper would be cleaner — TODO.)
- `internal/upstream/core/connection_launcher.go` — `connectWithLauncher`,
  `stopLauncher`, `watchLauncher`, `buildLauncherCmd`, `loggerWriter`.

### Files modified

- `internal/config/config.go` — `LauncherWaitTimeout Duration` on
  `ServerConfig`. Default 30s when zero/unset.
- `internal/config/merge.go` — `CopyServerConfig` carries the new field.
- `internal/upstream/core/client.go` — `launcherHandle launcher.Handle` and
  `launcherCIDFile string` on `Client`; new import.
- `internal/upstream/core/connection.go` — pre-transport launcher dispatch
  for `http`/`sse`/`streamable-http` when `Command != ""`. Stops launcher
  in the connect-failure cleanup path.
- `internal/upstream/core/connection_lifecycle.go` — `stopLauncher` after
  the MCP-client close in Disconnect (so the child sees the network
  transport go away first); also clears `processCmd`.
- `docs/configuration.md` — new "Locally-launched HTTP / SSE servers"
  section + back-compat behaviour matrix; `launcher_wait_timeout` row in
  the Server Fields table.
- `docs/cli-management-commands.md` — restart-semantics note covering the
  launcher stop-then-start order.

### Decisions / assumptions

1. **Stdio path untouched.** Plan's Phase 0 contemplated lifting env/Docker
   plumbing out of `connection_stdio.go` and routing stdio through
   `launcher.Spawn`. Doing that requires reworking how mcp-go owns the
   stdio process (mcp-go's `Stdio` transport spawns via a `CommandFunc` it
   controls — externally-spawned children can't be wired into it without
   patching the upstream library). To honour the spirit of "Docker-isolation
   logic must live in one place" without that reshuffling, the new
   `buildLauncherCmd` reuses the same Client methods (`setupDockerIsolation`,
   `injectEnvVarsIntoDockerArgs`, `insertCidfileIntoShellDockerCommand`,
   `wrapWithUserShell`) the stdio path already calls. Single source of
   truth, but no double-spawn risk.

2. **Launcher-managed children stay invisible to stdio cleanup helpers.**
   `connectWithLauncher` deliberately does NOT set `c.processCmd` /
   `c.processGroupID`. The `launcher.Handle` owns lifecycle; setting those
   would let stdio's `killProcessGroup` race with `Handle.Stop`. This is a
   minor deviation from the original plan (which suggested wiring the same
   process-group tracking) — the result is cleaner ownership.

3. **Health check is a TCP dial.** Per the plan's gotcha #2.
   `addrFromURL` infers default ports for http/https/ws/wss; rejects
   unknown schemes early so misconfigurations surface fast.

4. **StopGrace default is 5s.** Plan asked for an explicit decision (open
   question #2). 5s matches `processGracefulTimeout` in
   `internal/upstream/core/connection.go`. No per-server override yet —
   `Spec.StopGrace` is plumbed but not exposed in `ServerConfig`. Promote to
   config if a real-world server needs more.

5. **Crash-while-connected → Disconnect.** `watchLauncher` calls the
   `Client.Disconnect()` path on unexpected child exit (gotcha #6).
   Existing reconnect logic in `internal/upstream/managed` then handles
   the come-back attempt — no separate launcher-internal restart loop
   (open question #3 settled toward "defer to transport-level reconnect").

6. **Stop ctx on shutdown.** `stopLauncher` currently uses
   `context.WithTimeout(context.Background(), 10s)` everywhere. Plan
   open question #4 — accept this default; raise the limit if shutdown
   really needs to wait for slow Docker stop.

### Verification round 1 (2026-05-11)

After `sbx policy allow network proxy.golang.org,sum.golang.org` was set:

| Command | Result |
|---|---|
| `GOTOOLCHAIN=local go vet ./internal/upstream/...` | ✅ clean |
| `GOTOOLCHAIN=local go test ./internal/upstream/launcher/...` | ✅ 15/15 |
| `GOTOOLCHAIN=local go test ./internal/upstream/...` | ✅ all packages |
| `GOTOOLCHAIN=local go test ./internal/config/...` | ✅ |
| `go test -race` | ⚠️ blocked — cgo (gcc) not installed in sandbox; user can run on host |
| `go build ./cmd/mcpproxy` | ❌ blocked — needs `storage.googleapis.com` (some Go modules CDN-served from there); user must add `sbx policy allow network storage.googleapis.com` |

### Bugs found + fixed during verification round 1

1. **Deadlock in connect-failure cleanup.** `Connect` holds `c.mu` for its
   entire duration; my original failure-path call to `c.stopLauncher(...)`
   re-acquired the same lock → hang. Fixed by inlining the stop sequence
   in `connection.go`'s cleanup branch (read fields under the held lock,
   release `c.mu` around `handle.Stop()`, reacquire before return).
2. **`connectWithLauncher` redundant locking.** Same root cause —
   `connectWithLauncher` is called from `Connect` which already holds
   `c.mu`. Removed the inner `c.mu.Lock()/Unlock()` for the launcher
   field writes; the wait-for-url failure path still releases the lock
   around the blocking `handle.Stop()` and reacquires before returning.
3. **`bytes.Buffer` LogSink race.** Test failures from the stdout pump,
   stderr pump, and the startup-banner write all racing on a single
   `*bytes.Buffer` in tests. Fixed by wrapping `LogSink` internally with
   a `serializedWriter` (mutex around `Write`). zap-bridge in production
   is already thread-safe, so this is a robustness fix for test sinks
   and any future single-writer adapters.
4. **SIGKILL-fallback test could detect "ready" in the banner.** The
   launcher startup banner echoes the script source verbatim, so any
   marker token literally present in the script also matched in the
   banner — making the test think the trap was installed before the
   shell even ran. Fixed by using a shell-substituted marker
   (`__LNCTICK__:$$`) and a regex detector (`__LNCTICK__:[0-9]+`).
5. **`bad scheme + explicit port` test case.** Test asserted error on
   `ftp://example.com:21/foo` but the launcher correctly accepts any
   scheme when the port is explicit (user took responsibility). Removed
   that case; replaced with the actually-invalid `ftp://example.com/foo`.

### Outstanding network blocker

```
sbx policy allow network storage.googleapis.com
```

Needed for `go build ./cmd/mcpproxy` to fetch Bleve/Roaring/etc. CDN-backed
modules. Once allowed, the verification commands are:

```
GOTOOLCHAIN=local go build ./cmd/mcpproxy
./scripts/test-api-e2e.sh    # optional smoke test
```

### Outstanding follow-ups (post-PR)

- Replace `integration_test.go`'s python-shellout with a Go test-binary
  helper invoked via `os.Args` re-entry pattern, so the test runs on any
  CI that has Go (which is all of them). Plan called for a tiny binary in
  `internal/upstream/launcher/testdata/`.
- Extend `scripts/test-api-e2e.sh` with a launcher-flavoured server (plan
  Phase 2 item).
- Phase 3 (post-merge): `{port}` templating in `args` / `url`, per-launcher
  custom health probe, exponential backoff for repeated launcher crashes.

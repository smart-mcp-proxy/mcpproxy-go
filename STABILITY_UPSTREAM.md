# Upstream Stability Investigation Plan

## Observed Symptoms
- JetBrains MCP over SSE never surfaces tools, while stdio loads but drops connections; when this happens the dashboard freezes or the daemon hangs until forced termination (reported on Xubuntu 25.04 running as a user-scoped systemd service).
- Intermittent upstream instability is suspected to stem from SSE transport behaviour and OAuth refresh handling.

## Working Hypotheses (code references)
1. **SSE timeout ejects long-lived streams** – `internal/transport/http.go:225-279` hard-codes `http.Client{Timeout: 180 * time.Second}` for SSE. Go’s client timeout covers the entire request, so an otherwise healthy SSE stream is forcibly closed every three minutes, likely leaving the proxy in a bad state when the upstream cannot recover quickly.
2. **Endpoint bootstrap deadline too aggressive** – the SSE transport waits only 30s for the `endpoint` event (`github.com/mark3labs/mcp-go@v0.38.0/client/transport/sse.go:176-187`). If JetBrains (or other) servers delay emitting the endpoint while doing OAuth/device checks, we fail before tools load.
3. **OAuth browser flow races with remote UX** – manual OAuth waits 30s for the callback (`internal/upstream/core/connection.go:1722-1759`). In a remote/systemd scenario the user may need more time (or use an out-of-band browser), causing repeated failures and triggering connection churn.
4. **Connection-loss handling gaps** – we never register `Client.OnConnectionLost(...)` on SSE transports, so HTTP/2 idle resets or GOAWAY frames (which JetBrains emits) go unnoticed until the next RPC, amplifying freeze perceptions. This also limits our ability to surface diagnostics in logs/UI.

## Phase 1 – Reproduce & Capture Baseline
- Configure two JetBrains upstreams (SSE and stdio) with `log.level=debug` and, if possible, `transport` trace logging.
- Exercising `scripts/run-web-smoke.sh` and manual UI navigation, collect:
  - Upstream-specific logs under `~/.mcpproxy/logs/<server>.log`.
  - HTTP traces for `/events` (SSE) and `/api/v1` from the proxy (e.g. `MITM_PROXY=1 go run ./cmd/mcpproxy` or curl with `--trace-time`).
  - OAuth callback timing from `internal/oauth` logs to confirm 30s deadline trigger frequency.
- Inspect BoltDB (`bbolt` CLI or `scripts/db-dump.go`) for stored OAuth tokens to see if refresh metadata is present/updated.

**Verification checklist**
- [ ] Baseline reproduction yields “timeout waiting for endpoint” or “context deadline exceeded” in logs when SSE fails.
- [ ] Confirm whether OAuth callback timeout entries align with user interaction delays.
- [ ] Identify whether SSE stream closes almost exactly at 180s uptime.

## Phase 2 – SSE Transport Hardening
- Audit the full SSE pipeline:
  - Replace the global `http.Client.Timeout` with per-request contexts or keepalive idle deadlines; ensure this does not regress HTTP fallback.
  - Capture GOAWAY/NO_ERROR disconnects by wiring `client.OnConnectionLost` inside `core.connectSSE` and propagate them to the managed client.
  - Revisit the 30s endpoint wait; consider JetBrains-specific delay or signal logging (e.g. log the time between `Start` and first `endpoint` frame).
- Develop instrumentation hooks:
  - Record SSE connection uptime, retry counters, and last-error state in `StateManager`.
  - Emit structured events (e.g., `EventTypeUpstreamTransport`) with transport diagnostics for `/events`.

**Verification checklist**
- [ ] Stress an SSE upstream for >10 minutes and confirm no forced disconnect occurs due to client timeout.
- [ ] Simulate endpoint delay (e.g., proxy that waits 90s before emitting) and confirm new logic handles it or logs actionable warnings.
- [ ] Ensure managed state transitions (`ready` → `error` → `reconnecting`) align with injected connection-lost scenarios.

## Phase 3 – OAuth Token Lifecycle Review
- Trace refresh flow end-to-end:
  - Instrument `PersistentTokenStore.SaveToken/GetToken` to log token expiry deltas (guarded by debug level).
  - Validate `MarkOAuthCompletedWithDB` propagation by queuing fake events in BoltDB and ensuring `Manager.processOAuthEvents` consumes them without double-processing.
  - Explore extending the OAuth callback wait window and providing CLI guidance for headless setups (e.g., print verification URL without failing immediately).
- Consider tooling to introspect OAuth state (`/api/v1/oauth/status` or tray dialog) so users can identify expired/invalid tokens.

**Verification checklist**
- [ ] Refreshing an OAuth token updates BoltDB and triggers a reconnect without manual intervention.
- [ ] Extending callback timeout (experimentally) eliminates repeated “OAuth authorization timeout” messages for remote environments.
- [ ] Cross-process completion events always drive a reconnect within the expected polling window (≤5s default).

## Phase 4 – Introspection & User-Facing Diagnostics
- Design lightweight diagnostics:
  - CLI subcommand (e.g., `mcpproxy debug upstream <name>`) to dump current transport stats, token expiry, last error, and SSE uptime.
  - Optional `/api/v1/diagnostics/upstream` endpoint returning same payload for UI integration.
- Expand logging guidance in `MANUAL_TESTING.md` for capturing SSE issues (e.g., enabling trace on `transport` logger, how to tail upstream logs).
- Evaluate adding Prometheus-style counters (connection retries, OAuth failures) to aid longer-term monitoring.

**Verification checklist**
- [ ] Diagnostics output surfaces enough context for a user to determine whether the issue is OAuth, SSE transport, or upstream crash.
- [ ] UI/tray can surface a human-readable warning when SSE drops repeatedly (without freezing).
- [ ] Documentation changes tested by a fresh install following the guide reproduce the troubleshooting steps successfully.


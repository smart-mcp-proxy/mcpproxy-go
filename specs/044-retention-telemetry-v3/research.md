# Phase 0 Research: Retention Telemetry v3

**Feature**: 044-retention-telemetry-v3
**Date**: 2026-04-24
**Status**: All decisions resolved. No NEEDS CLARIFICATION markers.

## R1. env_kind decision tree — detection mechanics

**Decision**: Implement the ordered decision tree from design §4.2 as a single pure function `DetectEnvKind(env map[string]string, fs FileProber, osName string, ttyChecker TTYChecker) (EnvKind, EnvMarkers)`. Wrap it in `DetectEnvKindOnce()` that caches the result in a package-level `sync.Once`.

**Rationale**:
- Pure function is trivially unit-testable with a fake env map + file prober.
- `sync.Once` guarantees detection runs exactly once per process lifetime per FR-001.
- Cached value survives config hot-reload (env_kind is a process property, not a config property).

**Alternatives considered**:
- Re-detect on every heartbeat: rejected because env vars set after process start would incorrectly re-classify a CI runner as interactive.
- Detect lazily on first heartbeat: rejected because tray + CLI status commands need the value earlier.

## R2. File probe robustness

**Decision**: Use `os.Stat("/.dockerenv")` and `os.Stat("/run/.containerenv")` with no timeout (local FS stat is synchronous and fast). Fallback to `$container` env var check.

**Rationale**: These paths are local and stat is O(1). No timeout needed; the syscall itself is bounded.

**Alternatives considered**:
- Read `/proc/1/cgroup` for container detection: broader coverage but platform-specific (no /proc on macOS). Dockerenv+containerenv+env var already covers the common cases.

## R3. Launch source detection

**Decision**: Precedence (first-match wins):
1. `MCPPROXY_LAUNCHED_BY=installer` env var → `installer`.
2. Tray socket handshake already sets a `launched_via=tray` field on the core's `runtime.LaunchContext`; surface it → `tray`.
3. macOS: PPID's process name == `launchd` AND OS bundle contains `LSBackgroundOnly` → `login_item`.
4. Windows: PPID traced to `explorer.exe` via `Run` registry key → `login_item`.
5. `os.Stdin` is a TTY → `cli`.
6. Otherwise → `unknown`.

**Rationale**: Installer flag is one-shot and unambiguous; tray handshake is explicit; launchd/explorer parentage is the OS contract for login items; TTY is the fallback for interactive CLI launches.

**Alternatives considered**:
- Only use env vars: rejected because login-item launches have no natural env var.
- Ask the user: rejected — zero interruption policy per CLAUDE.md.

## R4. Installer env-var lifecycle

**Decision**: Core reads `MCPPROXY_LAUNCHED_BY=installer` at startup, passes it to `DetectLaunchSource`, then writes a flag to the activation bucket: `installer_heartbeat_pending=true`. First heartbeat emits `launch_source=installer`, then sets `installer_heartbeat_pending=false`. Subsequent heartbeats use the runtime-detected source (typically `tray` or `login_item`).

**Rationale**: Design §4.3 says "cleared after one heartbeat". Persisting the "pending" bit in BBolt survives a crash between install and first heartbeat.

**Alternatives considered**:
- Re-read env var every heartbeat: fails because login_item/tray launches won't have it set.
- One-shot in-memory only: fails the crash-recovery edge case.

## R5. macOS autostart mechanics

**Decision**:
- **Modern (macOS 13+)**: use `SMAppService.mainApp` to register the main app bundle as a login item. State read via `SMAppService.mainApp.status`.
- **Older (macOS 12 and below)**: not supported — design doc targets macOS 13+ and the spec's tray explicitly targets 13+.
- Tray exposes current status via a socket endpoint (`/autostart` or similar) that core polls once per heartbeat. Core caches the value with a 1-hour TTL.

**Rationale**: `SMAppService` is the Apple-recommended API post-13; the older `SMLoginItemSetEnabled` is deprecated. Tray is the only component with a GUI session to call SMAppService reliably.

**Alternatives considered**:
- Core directly calls SMAppService: fails because core runs in a background context without a GUI session.
- Store autostart state in config: violates Principle III (tray state belongs to the OS, not the config).

## R6. Activation BBolt bucket design

**Decision**: Single new bucket `activation` with fixed keys:

```
activation/
  first_connected_server_ever     : 1 byte (0x00 or 0x01)
  first_mcp_client_ever           : 1 byte
  first_retrieve_tools_call_ever  : 1 byte
  mcp_clients_seen_ever           : JSON []string, cap 16
  retrieve_tools_calls_24h        : 16 bytes: uint64 count + int64 window_start_unix
  estimated_tokens_saved_24h      : 16 bytes: uint64 count + int64 window_start_unix
  installer_heartbeat_pending     : 1 byte
```

**Rationale**: Fixed keys + fixed encoding = no migration needed. Missing bucket = fresh install (all flags false). Missing key = default value for that key.

**Alternatives considered**:
- Single JSON blob: simpler to read/write but atomically-consistent reads during concurrent writes become complicated. Per-key buckets gives natural transactional boundaries.
- New dedicated BBolt file: overkill for <1KB of data; adds a second file to the user's data directory.

## R7. MCP client name sanitization

**Decision**: Accept `params.clientInfo.name` from `initialize` only if it matches `^[a-z0-9][a-z0-9-_.]{0,63}$` and does NOT contain `/`, `\`, `..`, or `@`. Otherwise record `"unknown"`. Known whitelist for documentation purposes only: `claude-code`, `cursor`, `windsurf`, `codex-cli`, `gemini-cli`, `vscode`, `continue`.

**Rationale**: MCP spec defines `clientInfo.name` as a short identifier. Sanitizing at ingest prevents a malicious or buggy client from leaking user paths into our activation bucket (which then hits telemetry).

**Alternatives considered**:
- Strict whitelist (only allow 7 names): rejected because we want to measure new IDEs as they appear in the ecosystem.
- Accept verbatim: rejected — fails FR-007 and FR-011.

## R8. Token savings estimator

**Decision**: At heartbeat time:
```
estimated_tokens_saved = Σ (total_upstream_tool_count - tools_exposed_to_clients_this_window) × 150
```
Bucketed immediately:

| Range | Bucket |
|-------|--------|
| 0 | "0" |
| 1-99 | "1_100" |
| 100-999 | "100_1k" |
| 1000-9999 | "1k_10k" |
| 10000-99999 | "10k_100k" |
| >= 100000 | "100k_plus" |

Constant 150 chosen from measurement: average tool schema in the BM25 index serializes to ~150 tokens (JSON description + params, tokenized with tiktoken cl100k_base on a sample of 1,000 tools). Exact value is not critical because the result is bucketed.

**Rationale**: Bucketing caps cardinality at 6 values — the dashboard can render a stacked bar without any high-cardinality explosion. Constant fudge factor is acceptable because "measurable lift" is the success criterion, not "precise token count".

**Alternatives considered**:
- Count actual tokens via tiktoken: adds a Go dependency and CPU cost for zero gain once bucketed.
- Log-scale bins: buckets are already log-shaped; no benefit.

## R9. Anonymity scanner

**Decision**: New `internal/telemetry/anonymity.go` with `ScanForPII(payload []byte) error`. Rules:
- Fail if payload bytes contain any of: `/Users/`, `/home/`, `C:\\Users\\`, `/var/folders/`, current `os.UserHomeDir()` result, current `os.Hostname()` result, any env-var value from a blocked set (`GITHUB_TOKEN`, `GITLAB_TOKEN`, `OPENAI_API_KEY`, `ANTHROPIC_API_KEY`, common SSH key filenames).
- Fail if any field under `env_markers` is non-boolean in the serialized JSON.
- Fail if `anonymous_id` has changed format (not UUIDv4).

Runs inside `BuildPayload()` before the payload is handed to the HTTP client. On failure: log an error (without logging the payload!), increment a `telemetry_anonymity_violations` counter, skip the heartbeat.

**Rationale**: Defense-in-depth. Even if a future contributor adds a field that accidentally includes a path, the scanner catches it before transmission.

**Alternatives considered**:
- Only unit-test: catches today's fields but not future regressions.
- Server-side scan: the worker already validates; adding client-side protection is cheap and catches issues before they leave the machine.

## R10. macOS DMG post-install

**Decision**: Post-install script (executed by the DMG installer's "Install" step, or by a pkg embedded in the DMG) at `packaging/macos/postinstall.sh`:

```bash
#!/bin/bash
set -e
open -a MCPProxy --env MCPPROXY_LAUNCHED_BY=installer
exit 0
```

**Rationale**: `open -a` launches the tray app with the env var visible to the child process. The tray, on startup, forwards the env var to core via the socket handshake (or core reads it directly if launched by installer via a secondary path). After one heartbeat the flag is cleared (see R4).

**Alternatives considered**:
- LaunchAgent plist with RunAtLoad: conflates install-launch with login-item auto-start; rejected.
- Skip post-install (user manually launches): fails SC-004 (≥50% installer attribution).

## R11. HTTP status endpoint exposure

**Decision**: Extend `internal/httpapi/server.go` `/api/v1/status` response with an `activation` field (read-only snapshot) and the process-level `env_kind` + `launch_source` + `autostart_enabled`. Tray reads this to show "You've connected 1 server, 0 IDEs, 0 retrieve_tools calls" progress UI in a future pass. CLI `mcpproxy telemetry status` surfaces the same.

**Rationale**: Tray is a UI consumer of core state (Principle III). Surfacing activation via the existing status API avoids a new endpoint and aligns with the SSE-based realtime update pattern.

**Alternatives considered**:
- Dedicated `/api/v1/activation` endpoint: unnecessary fan-out; status endpoint is the conventional place.
- Only CLI exposure: makes tray integration harder in a follow-up.

## R12. Test strategy summary

- `env_kind_test.go`: table-driven, one row per decision-tree branch (7 branches × pass/no-match = 14 rows minimum).
- `launch_source_test.go`: table-driven, one row per source.
- `activation_test.go`: fresh BBolt → all-false; set flag → persist → reopen → still true; 17 clients → truncate at 16; 24h boundary → counter decays.
- `payload_v3_test.go`: golden payload fixture + anonymity scanner runs against it.
- `payload_privacy_test.go` (existing): extend with assertions that `env_markers` is boolean-only and no user-path prefix appears.
- E2E: fixture heartbeat HTTP POST reaches a local test server; test asserts it contains v3 shape.

**All decisions resolved. Ready for Phase 1.**

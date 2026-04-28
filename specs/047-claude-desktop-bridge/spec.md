# Feature Specification: Claude Desktop Bridge Support

**Feature Branch**: `047-claude-desktop-bridge`
**Created**: 2026-04-28
**Status**: Draft
**Input**: User description: "Claude Desktop bridge support via npx mcp-remote, conditional API key, round-trip safe writer."

## Context

Spec 039 (`Connect Clients & Dashboard Visual Redesign`) introduced the `POST /api/v1/connect/{client}` endpoint, the `mcpproxy connect <client>` CLI, and a Web UI Clients section that writes MCPProxy into a client's native MCP configuration file. Seven adapters ship today in `internal/connect/clients.go`: `claude-code`, `cursor`, `windsurf`, `vscode`, `codex`, `gemini`, plus a stub for `claude-desktop`.

Claude Desktop is the only intentional gap. MCPProxy exposes `/mcp` over HTTP/SSE, while Claude Desktop only spawns stdio MCP servers. The current adapter therefore returns an error and the wizard introduced in spec 046 (US1 — adaptive onboarding) surfaces *"Claude Desktop only supports stdio transport; HTTP/SSE not available"* as a dead-end result. Spec 046's US3 (onboarding telemetry) records the abandonment.

This spec closes the gap by shipping a stdio↔HTTP bridge using `npx -y mcp-remote` (1.4k★, MIT, full MCP Authorization spec) as the command Claude Desktop spawns. The bridge proxies JSON-RPC over stdio to mcpproxy's `/mcp` endpoint. Zero install — Claude Desktop already resolves bare `npx` via the user's login-shell `PATH` on macOS at startup (verified empirically in `~/Library/Logs/Claude/mcp-server-*.log`: *"Using MCP server command: /opt/homebrew/bin/npx"*).

## Assumptions (No Clarification Needed)

1. **Bridge tool is `mcp-remote`** — npm package, MIT, supported via `npx -y mcp-remote <url> [flags]`. No vendoring; users get the latest at first launch.
2. **Bare command emission** — the adapter writes `command: "npx"` (not an absolute path). Claude Desktop's PATH resolution covers macOS and Linux. Windows resolution is the user's responsibility; a `where npx` doctor check warns when missing.
3. **No `resolveNpx()` PATH probe inside the Claude Desktop adapter** — empirically unnecessary. Other Spec 039 adapters (Cursor especially) may revisit this in a future spec; explicitly out of scope here.
4. **Conditional auth** — by project policy (`CLAUDE.md` "Security Notes"), `/mcp` is unprotected by default. The bridge gets `--header X-API-Key:${MCPPROXY_API_KEY}` and an `env` block **only when `require_mcp_auth: true`** in mcpproxy config.
5. **No-space header form** — `mcp-remote` mishandles spaces inside arg values on Windows. The adapter emits `X-API-Key:${MCPPROXY_API_KEY}` (no space after colon). The literal API key value is injected via the `env` block, never substituted into args.
6. **Sentinel header for round-trip identity** — every entry the adapter writes carries `--header X-MCPProxy-Adapter:1`. The reader matches on this sentinel (NOT on URL), so users can hand-edit URL/port without confusing the writer.
7. **Backup before modify** — same convention as spec 039 (`.bak.<UTC-timestamp>` next to the original config).
8. **Config path** — macOS: `~/Library/Application Support/Claude/claude_desktop_config.json`; Windows: `%APPDATA%/Claude/claude_desktop_config.json`; Linux: not officially supported by Claude Desktop today (adapter returns a clear unsupported-platform error).
9. **Restart prompt** — after a successful write the response/CLI/UI tells the user to fully quit and relaunch Claude Desktop (Cmd-Q on macOS); changes are not picked up live. This matches Claude Desktop's existing behavior for any `mcpServers` edit.
10. **No new backend storage** — pure filesystem + in-memory; no BBolt buckets, no migrations.

## User Scenarios & Testing *(mandatory)*

### User Story 1 — Connect Claude Desktop via the bridge (Priority: P1)

A user already running mcpproxy locally wants Claude Desktop to discover mcpproxy's tool catalog. They run `mcpproxy connect claude-desktop` (CLI) or click the **Connect** button next to *Claude Desktop* in the Web UI Clients section (or accept the wizard's recommendation from spec 046). MCPProxy reads `~/Library/Application Support/Claude/claude_desktop_config.json`, takes a backup, inserts an `mcpServers.mcpproxy` entry whose `command` is `npx`, whose `args` invoke `mcp-remote` against `http://127.0.0.1:<port>/mcp` with the sentinel header, and writes the file atomically. The user fully quits and relaunches Claude Desktop, opens any conversation, and the mcpproxy built-in tools (`retrieve_tools`, `call_tool_read`, etc.) appear in the tool picker.

**Why this priority**: This is the entire point of the spec. Without P1 the wizard remains a dead-end for the largest desktop client in the ecosystem and Claude Desktop users have no path to mcpproxy.

**Independent Test**: On a clean macOS machine with Claude Desktop installed and no prior mcpproxy entry, run `mcpproxy connect claude-desktop`, restart Claude Desktop, and verify mcpproxy tools appear. Round-trip identity is verified by re-running `mcpproxy connect claude-desktop` and confirming the response reports `action: "exists"` (not `"created"` again) and the file content is byte-identical.

**Acceptance Scenarios**:

1. **Given** mcpproxy is running on `127.0.0.1:8080` with `require_mcp_auth: false`, **When** the user runs `mcpproxy connect claude-desktop`, **Then** `claude_desktop_config.json` gains an `mcpServers.mcpproxy` entry with `command: "npx"`, `args: ["-y", "mcp-remote", "http://127.0.0.1:8080/mcp", "--header", "X-MCPProxy-Adapter:1"]`, no `env` block, and a `.bak.<timestamp>` backup exists alongside the original file.
2. **Given** the user re-runs `mcpproxy connect claude-desktop` against the same config (no auth drift), **When** the adapter detects the sentinel header on the existing entry, **Then** the response reports `action: "exists"` and the file is left unchanged (no new backup).
3. **Given** the user runs `mcpproxy disconnect claude-desktop` (parity with spec 039), **When** the adapter finds the sentinel-tagged entry, **Then** the entry is removed and a backup is taken; user-authored entries with the same name but no sentinel are left untouched and a warning is returned.
4. **Given** Claude Desktop launches before mcpproxy is up, **When** Claude Desktop spawns the bridge, **Then** `mcp-remote`'s built-in retry succeeds once mcpproxy starts and the user sees tools without re-launching Claude Desktop.

---

### User Story 2 — `require_mcp_auth` toggle handling (Priority: P2)

The user later flips `require_mcp_auth` from `false` to `true` (or vice versa). The bridge entry written previously becomes stale: either it lacks the `--header X-API-Key:...` flag and `env` block (auth was turned on) or it carries them needlessly (auth was turned off). When the user re-runs `mcpproxy connect claude-desktop`, the adapter detects the mismatch on the sentinel-tagged entry, takes a backup, rewrites the entry, and reports `action: "refreshed"`. The Web UI / wizard surfaces a "Refresh" call-to-action when this drift is detected.

**Why this priority**: Toggling auth is a routine operation (security review, hardening for shared environments), and a stale bridge silently breaks Claude Desktop's connection until manually fixed. P2 because the workaround (delete and re-run connect) exists, but the friction degrades trust in the connect feature.

**Independent Test**: With a sentinel-tagged entry already written under `require_mcp_auth: false`, set `require_mcp_auth: true` and re-run `mcpproxy connect claude-desktop`. Verify the response reports `action: "refreshed"`, args now include `--header X-API-Key:${MCPPROXY_API_KEY}` (no space after colon), and a new `env: { "MCPPROXY_API_KEY": "<resolved key>" }` block is present. Flip back and verify args/env shrink correspondingly.

**Acceptance Scenarios**:

1. **Given** a sentinel-tagged entry exists written under `require_mcp_auth: false`, **When** the user enables `require_mcp_auth: true` and re-runs connect, **Then** the args include `--header X-API-Key:${MCPPROXY_API_KEY}` and an `env` block carries the resolved key value.
2. **Given** a sentinel-tagged entry exists written under `require_mcp_auth: true`, **When** the user disables auth and re-runs connect, **Then** the args lose the `X-API-Key` header, the `env` block is removed, and `action: "refreshed"` is returned.
3. **Given** the API key is rotated in mcpproxy config but `require_mcp_auth` remains `true`, **When** the user re-runs connect, **Then** args are byte-identical (no-space header form references `${MCPPROXY_API_KEY}`) and only the `env.MCPPROXY_API_KEY` value changes.

---

### Edge Cases

- **`npx` not installed (Windows users without Node, or stripped Linux)**: `mcpproxy doctor` warns with a clear remediation pointing to nodejs.org. The adapter still writes the entry (fail-soft) but the API response includes a `warnings` array.
- **`claude_desktop_config.json` missing**: adapter creates a fresh file with `{ "mcpServers": { "mcpproxy": { ... } } }`. Backup is skipped (nothing to back up); response reports `action: "created"`.
- **`claude_desktop_config.json` malformed JSON**: adapter refuses to write, returns a structured error pointing to the malformed file path and offering `--force` semantics consistent with spec 039.
- **Existing `mcpServers.mcpproxy` entry without sentinel header (user hand-authored)**: adapter does NOT overwrite. Response reports `action: "conflict"` and lists the pre-existing entry. `--force` overrides and is recorded in the backup.
- **mcpproxy not running when Claude Desktop spawns the bridge**: `mcp-remote`'s exponential-backoff retry handles this; no spec changes needed beyond documenting the behavior in the response message.
- **Linux Claude Desktop**: not officially supported by Anthropic. Adapter returns an explicit *unsupported_platform* error with a link to a tracking issue.
- **Custom mcpproxy port** (`listen` ≠ `127.0.0.1:8080`): adapter reads the live port from runtime state (same source spec 039 uses) and embeds the resolved URL.

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: The `claude-desktop` adapter MUST emit `command: "npx"` as a bare command (no absolute path). Justification: Claude Desktop performs login-shell `PATH` resolution at startup on macOS and Linux; an absolute path would break across machines.
- **FR-002**: The adapter MUST emit args of the form `["-y", "mcp-remote", "<resolved-url>", "--header", "X-MCPProxy-Adapter:1", ...]`, where `<resolved-url>` is mcpproxy's live listen URL with `/mcp` suffix (taken from runtime state, not the static `listen` config field, to honour port overrides).
- **FR-003**: The adapter MUST include `--header X-MCPProxy-Adapter:1` (the sentinel) on every entry it writes, and MUST use the **presence of this sentinel** — not URL match — to decide whether an entry is its own.
- **FR-004**: When `require_mcp_auth: true`, the adapter MUST additionally emit `--header X-API-Key:${MCPPROXY_API_KEY}` in args **and** an `env: { "MCPPROXY_API_KEY": "<resolved key>" }` block on the entry.
- **FR-005**: When `require_mcp_auth: false`, the adapter MUST NOT emit `X-API-Key` headers and MUST NOT emit an `env` block. Existing sentinel-tagged entries with stale auth metadata MUST be refreshed in place.
- **FR-006**: All header values written by the adapter MUST use the no-space form (`Name:Value`, no whitespace after the colon). Justification: `mcp-remote` mishandles spaces in arg values on Windows; the literal key value travels via `env`, never via args.
- **FR-007**: The adapter MUST take a `.bak.<UTC-timestamp>` backup of `claude_desktop_config.json` before any write that mutates the file. Pure no-op re-runs (sentinel match, no auth drift) MUST NOT create backups.
- **FR-008**: The adapter MUST support round-trip read/write/disconnect: `connect` followed by `disconnect` MUST leave `claude_desktop_config.json` byte-identical to its pre-connect state (modulo the `.bak.*` files left behind).
- **FR-009**: The adapter MUST refuse to overwrite an `mcpServers.mcpproxy` entry that lacks the sentinel header, returning a structured `conflict` response. The `force: true` request body field MUST override this and record the overwritten entry in the backup file.
- **FR-010**: `mcpproxy doctor` MUST gain an `npx_available` check that runs `npx --version` (or `where npx` on Windows) and emits a warning when absent. The check MUST link to remediation guidance.
- **FR-011**: The `claude-desktop` adapter MUST NOT call any `resolveNpx()` PATH probe. Justification documented in the assumptions: Claude Desktop's startup PATH augmentation makes a probe redundant on macOS/Linux. This decision is scoped to this adapter; other Spec 039 adapters retain whatever probe behavior they have today and are out of scope here.
- **FR-012**: The wizard copy from spec 046 US1 referencing *"Claude Desktop only supports stdio transport; HTTP/SSE not available"* MUST be replaced with a recommendation card that runs the bridge connect path. Telemetry events from spec 046 US3 MUST track `bridge_connect_success` and `bridge_connect_failure` distinct from the existing `connect_success`/`connect_failure` counters.
- **FR-013**: When the resolved bridge URL fails to construct (e.g., listen address `0.0.0.0` with no usable host) the adapter MUST return an actionable error naming the offending config field, MUST NOT write a partial entry, and MUST NOT create a backup.
- **FR-014**: The adapter MUST emit a post-write user-facing message instructing the user to fully quit and relaunch Claude Desktop (Cmd-Q on macOS) for the change to take effect.
- **FR-015**: The adapter MUST NOT regress the seven existing Spec 039 adapters (`claude-code`, `cursor`, `windsurf`, `vscode`, `codex`, `gemini`, plus the existing `claude-desktop` stub being replaced). All existing adapter tests MUST continue to pass; the `claude-desktop` stub test is replaced with full adapter tests covering both auth modes and the round-trip identity invariant.

### Key Entities

- **Bridge Entry** — the `mcpServers.<name>` JSON object the adapter writes into Claude Desktop's config file. Attributes: `command` (always `"npx"`), `args` (ordered list including the sentinel header), optional `env` block (present iff `require_mcp_auth: true`).
- **Sentinel Header** — the literal arg pair `["--header", "X-MCPProxy-Adapter:1"]`, used as the round-trip identity marker.
- **Resolved Bridge URL** — mcpproxy's live `<scheme>://<host>:<port>/mcp` derived from runtime state, embedded as the third positional arg after `mcp-remote`.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: A user with a fresh Claude Desktop install completes the end-to-end flow (`mcpproxy connect claude-desktop` → quit & relaunch Claude Desktop → mcpproxy tools visible in the Claude Desktop tool picker) in **≤ 60 seconds**, measured wall-clock from CLI invocation to first tool render on a residential broadband connection.
- **SC-002**: Round-trip identity holds: in **100%** of `connect → disconnect` runs against a config that started without an mcpproxy entry, the post-disconnect file is byte-identical to the pre-connect file.
- **SC-003**: Re-running `connect` against a config the adapter previously wrote (no auth drift) reports `action: "exists"` and creates **zero** new backup files in **100%** of runs.
- **SC-004**: `mcpproxy doctor` trip-wires when `npx` is missing on the system PATH: the check emits a `warning` with a remediation link in **100%** of runs on a system without Node.
- **SC-005**: All seven existing Spec 039 adapters' integration tests continue to pass; **zero** regressions in `internal/connect/...` test packages.
- **SC-006**: Spec 046 US3 telemetry shows the share of Claude-Desktop wizard sessions that end in `bridge_connect_success` rises from **0%** (current dead-end) to a meaningful baseline within one release cycle of GA.
- **SC-007**: Toggling `require_mcp_auth` and re-running connect produces an entry that authenticates successfully against the live `/mcp` endpoint in **100%** of test runs spanning both directions of the toggle.

## Out of Scope

- **Tier-2 adapters that don't need a bridge**: Zed, OpenCode, and other clients with native HTTP/SSE support. They use Spec 039's existing adapter pattern. No bridge-related work for these clients in this spec.
- **Retroactive `resolveNpx()` PATH probes for other Spec 039 adapters**: Cursor in particular has historical PATH-resolution friction. A follow-up spec, scoped to the affected adapters, will address this if/when it bites users. Explicitly excluded from spec 047.
- **Vendoring `mcp-remote` or shipping it inside the mcpproxy binary**: out of scope. We rely on `npx -y` for zero-install fetch on first use.
- **Linux Claude Desktop support**: Anthropic does not ship a Linux build today. The adapter returns a clean unsupported-platform error rather than silently writing a config file Claude Desktop will never read.
- **Migrating users with hand-authored `mcpServers.mcpproxy` entries**: those are detected as `conflict` and left untouched unless `force: true`. Automatic migration is out of scope.
- **Bridge implementation alternatives**: we evaluated and rejected writing our own stdio↔HTTP shim for this spec; revisit only if `mcp-remote` upstream stalls.

## References

- **Spec 039** (`039-connect-and-dashboard`) — connect adapter framework and seven existing client adapters (`internal/connect/clients.go`).
- **Spec 046** — adaptive onboarding wizard (US1, PR #433) and onboarding telemetry (US3, PR #434). This spec replaces the wizard's Claude Desktop dead-end and extends the telemetry counters.
- **mcp-remote** (`npmjs.com/package/mcp-remote`) — npm package, MIT, ~1.4k★, full MCP Authorization spec support.
- **Claude Desktop config**: `~/Library/Application Support/Claude/claude_desktop_config.json` (macOS), `%APPDATA%/Claude/claude_desktop_config.json` (Windows).
- **Empirical PATH evidence**: `~/Library/Logs/Claude/mcp-server-*.log` shows Claude Desktop resolving bare `npx` to `/opt/homebrew/bin/npx` via login-shell PATH at startup.

## Commit Message Conventions *(mandatory)*

When committing changes for this feature, follow these guidelines:

### Issue References
- Use: `Related #047` — links the commit to the issue without auto-closing.
- Do NOT use: `Fixes #`, `Closes #`, `Resolves #` — these auto-close issues on merge, but issues should only be closed manually after verification in production.

### Co-Authorship
- Do NOT include `Co-Authored-By: Claude <noreply@anthropic.com>`.
- Do NOT include "🤖 Generated with [Claude Code]" trailers.

**Rationale**: Commit authorship should reflect the human contributors, not the AI tools used.

### Example Commit Message
```
feat(047): claude-desktop-bridge spec — npx mcp-remote with conditional auth

Adds spec 047 covering the Claude Desktop adapter that uses npx -y mcp-remote
to bridge stdio (Claude Desktop) <-> HTTP/SSE (mcpproxy /mcp). Conditional
X-API-Key header gated on require_mcp_auth. Round-trip identity guaranteed by
sentinel header X-MCPProxy-Adapter:1.

Related #047
```

# Contract: `/mcp/p/<slug>` profile-scoped MCP endpoint

Stateless, pinned selector. Same MCP protocol surface as `/mcp`; the only difference is the effective server set. Reuses the retrieve_tools-mode MCP server instance; profile resolution is via `profileMiddleware` (runs **after** `mcpAuthMiddleware`).

## Request resolution

| Condition | Response |
|-----------|----------|
| `profiles` absent or empty, any `/mcp/p/<anything>` | **404** JSON `{"error":"no profiles configured"}` (FR-008) |
| `profiles` non-empty, slug matches no profile | **404** JSON `{"error":"unknown profile '<slug>'","available":["research","deploy"]}` (FR-009) |
| slug matches profile `P` | **200** — MCP surface identical to `/mcp`, effective server set restricted to `P.servers` (FR-002) |
| `/mcp`, `/mcp/code`, `/mcp/call` | unchanged — full union, no profile applied (FR-010) |

`/mcp/p/<slug>` is stateless: no global "active profile" is mutated; concurrent requests to different profile URLs from the same client each see only their own profile (FR-003).

## Tool-surface behaviour at `/mcp/p/<slug>`

- `retrieve_tools` returns only tools from servers in the **effective set** (profile ∩ token ∩ enabled ∩ not-quarantined ∩ user-visible). Tools the profile excludes MUST NOT appear (FR-004).
- `call_tool_read` / `call_tool_write` / `call_tool_destructive` into an excluded server are rejected (FR-004).
- `upstream_servers` introspection (e.g. `list`/`get`) MUST exclude servers outside the profile from its result — a profile URL cannot enumerate out-of-profile servers (FR-004). *(Codex #621 finding 1.)*
- `code_execution` (when enabled on the reused retrieve-tools server) MUST run with the profile-intersected effective server set: `call_tool()` invoked from inside a code-execution sandbox at a profile URL is rejected for any server outside the active profile. An empty caller-supplied `allowed_servers` MUST NOT be interpreted as "all servers" at a profile URL. *(Codex #621 finding 2.)*
- Per-server `enabled_tools`/`disabled_tools` continue to apply inside the profile (FR-006); no profile-level tool list.

## Scope composition & error attribution

Effective allowed-servers = **intersection** of profile `servers` and (if an agent token is present) `AgentToken.AllowedServers`. A token wildcard `["*"]` is fully constrained by the profile (FR-005). The two checks are independent so errors name the responsible primitive (FR-012):

| Caller | Server | Result |
|--------|--------|--------|
| token `{github,fs,web}` @ `/mcp/p/deploy` (`{github,k8s}`) | `github` | allowed (in both) |
| same | `fs` | rejected — `"server 'fs' is not in profile 'deploy'"` |
| same | `k8s` | rejected — `"Server 'k8s' is not in scope for this agent token"` |
| **unauthenticated** @ `/mcp/p/research` | non-`research` server | **rejected by profile** (regression test — must hold even though unauth ⇒ AdminContext) |

## Activity logging

Tool-call activity records originating from `/mcp/p/<slug>` carry `metadata["profile"] = "<slug>"` (FR-011) in the existing `ActivityRecord.Metadata` map. Records from `/mcp` omit the field.

## Edge cases

- Profile references unknown server → warn at load, server omitted (FR-015).
- Profile references quarantined/disabled server → excluded from effective set while in that state; appears once cleared (no file re-read).
- Config hot-reload changes a profile mid-connection → in-flight session keeps its snapshot; new connections see the change.
- Reserved slug defined as a profile (`all`/`code`/`call`/`p`) → rejected at load (never reaches routing).

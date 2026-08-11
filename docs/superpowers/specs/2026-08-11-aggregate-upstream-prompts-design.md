# Aggregate upstream MCP prompts through prompts/list

GitHub issue: [#972](https://github.com/smart-mcp-proxy/mcpproxy-go/issues/972)

## Problem

mcpproxy exposes only two hardcoded prompts (`setup-new-mcp-server`,
`troubleshoot-mcp-server`) via `registerPrompts()` in `internal/server/mcp.go`.
Upstream servers that advertise their own `Capabilities.Prompts` are detected
(`internal/upstream/cli/client.go:203-210`, debug logging only) but never
forwarded. This is inconsistent with how `tools/list` already aggregates
upstream tools (direct routing mode).

## Goals

- `prompts/list` returns the two built-in prompts plus every prompt advertised
  by a connected upstream server whose `Capabilities.Prompts != nil`.
- `prompts/get` resolves a (possibly prefixed) prompt name to its owning
  upstream server and forwards the request, returning the upstream's
  `GetPromptResult` unchanged.
- Behavior is gated by the existing `enable_prompts` config flag (no new
  config surface — the issue's own "opt-in per-server flag" alternative is
  explicitly not pursued).

## Non-goals

- Extending prompt capability to the `direct` / `code_execution` / `call_tool`
  routing-mode MCP server instances. Today only the default `retrieve_tools`
  server (`p.server`) has `WithPromptCapabilities` wired up at all; the other
  three don't support prompts and this issue does not change that.
- A per-server opt-in flag for prompt exposure (issue's alternative,
  deliberately deferred).

## Architecture

Mirrors the existing tools-aggregation pattern (specifically direct-mode
tools, `internal/server/mcp_routing.go`), not the `retrieve_tools` BM25
search path — prompts are few and cheap enough to aggregate in full.

### Upstream client layer

- `internal/upstream/core/client.go`: add
  `ListPrompts(ctx context.Context) ([]mcp.Prompt, error)` and
  `GetPrompt(ctx context.Context, name string, args map[string]string) (*mcp.GetPromptResult, error)`,
  thin wrappers around the underlying mark3labs `mcp-go` client's
  `ListPrompts`/`GetPrompt` RPCs — same shape as the existing
  `ListTools`/`CallTool` in this file.
- `internal/upstream/cli/client.go`: thin passthroughs to the core client
  (mirrors existing `ListTools`/`CallTool` wrappers).

### Manager aggregation layer

`internal/upstream/manager.go`:

- `Manager.ListPrompts(ctx) ([]PromptMetadata, error)` iterates `m.clients`;
  for each client whose `Capabilities.Prompts != nil`, calls its
  `ListPrompts`. On a per-client error, log a warning and skip that client —
  the rest of the aggregation proceeds (mirrors `DiscoverTools` resilience).
  Each returned prompt name is formatted as `serverName__promptName` via a
  new `FormatDirectPromptName(serverName, promptName string) string` helper
  in `mcp_routing.go`, reusing the existing `DirectModeToolSeparator = "__"`
  constant (same convention as `FormatDirectToolName`).
- `Manager.GetPrompt(ctx, prefixedName string, args map[string]string) (*mcp.GetPromptResult, error)`
  parses the prefix via a new `ParsePromptName(prefixedName string) (serverName, promptName string, ok bool)`
  helper (mirrors `ParseDirectToolName`), resolves the owning client by name
  (same lookup pattern as `Manager.CallTool`), and forwards the call. Errors
  (unknown prefix, no matching client, upstream error) propagate unchanged to
  the caller — no swallowing.

### Server wiring

`internal/server/mcp.go` / `mcp_routing.go`:

- `registerPrompts()` is unchanged — still registers the 2 built-ins.
- New `RefreshPrompts()` on `MCPProxyServer`: early-returns if
  `!p.config.EnablePrompts`. Otherwise builds the built-in `ServerPrompt`
  entries plus one `ServerPrompt` per aggregated upstream prompt (handler
  closure calls `p.upstreamManager.GetPrompt(ctx, prefixedName, request.Params.Arguments)`),
  then calls `p.server.SetPrompts(all...)` to atomically replace the full
  prompt set — same pattern as `RefreshDirectModeTools`'s use of `SetTools`.
- Hook `RefreshPrompts()` into the existing `EventTypeServersChanged` branch
  in `listenForRoutingModeRefresh` (`internal/server/server.go:471-490`),
  alongside `RefreshDirectModeTools()` / `RefreshCodeExecModeTools()`. No
  separate manual call at startup is needed — upstream servers connect
  asynchronously and `servers.changed` fires once they do, matching the
  existing comment/pattern for direct-mode tools.

## Data flow

1. Upstream server connects → `EventTypeServersChanged` fires →
   `RefreshPrompts()` calls `Manager.ListPrompts` → `SetPrompts` on `p.server`.
2. Client sends `prompts/list` → mcp-go's built-in handler returns the
   current `s.prompts` map (built-ins + aggregated, kept in sync by step 1).
3. Client sends `prompts/get` with a prefixed name → the per-prompt handler
   closure calls `Manager.GetPrompt` → resolves server → forwards → returns
   the upstream's `GetPromptResult` unchanged. Built-in prompt names are
   unprefixed and never collide with an aggregated name (which is always
   prefixed by a unique server name).

## Error handling

- `Manager.ListPrompts`: per-server failure → log warning, skip that server,
  continue with the rest.
- `Manager.GetPrompt`: unknown prefix / unknown server / upstream error → all
  propagate as a normal MCP error to the client, same as `CallTool` today.
- No collision handling required: the `__` prefix is always the connecting
  server's (unique) name.

## Testing

- `internal/upstream/manager_test.go`: aggregation across multiple fake
  clients (one with prompts, one without the capability, one erroring on
  `ListPrompts`) — assert correct prefixing and that the erroring client is
  skipped without affecting the others. Plus `GetPrompt` resolution tests
  (valid prefix, unknown server, unknown prompt).
- Table tests for `FormatDirectPromptName` / `ParsePromptName` (pure
  functions), alongside the existing `mcp_routing_test.go` tests for the
  tool-name equivalents.
- `internal/server`: new `RefreshPrompts` test verifying built-ins are always
  present and aggregated prompts appear/disappear as a fake upstream
  manager's prompt set changes — first prompts test coverage in this
  package.
- `go test ./internal/... -race` per repo convention.
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
- Behavior is gated by the existing global `enable_prompts` config flag, plus
  a new per-server `expose_prompts` override (tri-state; nil inherits the
  default-aggregate behavior) so a server can be excluded individually even
  though it advertises the capability.

## Non-goals

- A way to force-expose prompts for a server that doesn't advertise
  `Capabilities.Prompts` — the per-server flag can only opt a server *out*,
  not fabricate a capability it doesn't have.
- Consuming an upstream's `notifications/prompts/list_changed` notification.
  Re-aggregation only happens on the proxy's own `servers.changed` event
  (connect/disconnect/config change); if a still-connected upstream adds or
  removes a prompt without a server-level config change, mcpproxy won't
  notice until the next `servers.changed`.

> **Update (PR #973 review):** the routing-mode servers (`direct`,
> `code_execution`, `call_tool`) now also get `WithPromptCapabilities` and
> the aggregated prompt set — the original non-goal above ("only the default
> `retrieve_tools` server supports prompts") turned out to make the feature
> unreachable over Streamable HTTP `/mcp` in every routing mode besides the
> default, since `config.Validate()` normalizes `routing_mode` away from
> `retrieve_tools`. See `RefreshPrompts` and `initRoutingModeServers` in
> `internal/server/mcp_routing.go`.

## Architecture

Mirrors the existing tools-aggregation pattern (specifically direct-mode
tools, `internal/server/mcp_routing.go`), not the `retrieve_tools` BM25
search path — prompts are few and cheap enough to aggregate in full.

### Per-server config

`internal/config/config.go`: add `ExposePrompts *bool` to `ServerConfig`
(`type ServerConfig struct` at line 209), following the same tri-state
pointer convention already used by `IsolationConfig.Enabled *bool` (line
~266, with its `IsEnabled()` nil-safe accessor) elsewhere in this file:

```go
ExposePrompts *bool `json:"expose_prompts,omitempty" mapstructure:"expose-prompts"`
```

- `nil` (unset, the default) → inherit the default-aggregate behavior: this
  server's prompts are included if it advertises `Capabilities.Prompts`.
- `false` → excluded from aggregation regardless of advertised capability.
- `true` → explicit no-op today (same effect as `nil` when the capability is
  present); reserved so a future global default flip doesn't require a config
  migration.

### Upstream client layer

Note: `internal/upstream/cli/client.go` is only used by the `mcpproxy auth`
CLI command and the tray app, not by the runtime aggregation path — it is
out of scope here. The runtime path is `Manager` → `managed.Client` →
`core.Client`.

- `internal/upstream/core/client.go`: add
  `ListPrompts(ctx context.Context) ([]mcp.Prompt, error)` (mirrors
  `ListTools`: checks connection, returns `nil, nil` if
  `serverInfo.Capabilities.Prompts == nil` **or** `c.config.ExposePrompts != nil && !*c.config.ExposePrompts`
  — both checks live here so `Manager.ListPrompts` needs no special-casing
  beyond the per-client error skip it already mirrors from `DiscoverTools` —
  otherwise calls the mark3labs client's `ListPrompts(ctx, mcp.ListPromptsRequest{})`,
  returns `result.Prompts`) and
  `GetPrompt(ctx context.Context, name string, args map[string]string) (*mcp.GetPromptResult, error)`
  (mirrors `CallTool`: checks connection, builds
  `mcp.GetPromptRequest{Params: mcp.GetPromptParams{Name: name, Arguments: args}}`,
  calls the mark3labs client's `GetPrompt`, returns the result).
- `internal/upstream/managed/client.go`: add `ListPrompts`/`GetPrompt` on
  `managed.Client`, mirroring the simple connectivity-check-then-delegate
  shape of `managed.Client.CallTool` (not the leader-election/coalescing
  shape of `managed.Client.ListTools` — prompts are only refreshed on
  `servers.changed`, not per-request, so no coalescing is needed).

### Manager aggregation layer

`internal/upstream/manager.go`:

- `Manager.ListPrompts(ctx context.Context) ([]mcp.Prompt, error)` iterates
  `m.clients` (same enabled/quarantined/connected snapshot pattern as
  `DiscoverTools`) and calls each client's `ListPrompts` (which already
  returns `nil, nil` for capability-less or opted-out servers, per above). On
  a per-client error, log a warning and skip that client — the rest of the
  aggregation proceeds (mirrors `DiscoverTools` resilience). Each returned
  prompt's `Name` is rewritten in place to
  `serverName:promptName` (colon) — the same internal-registry convention
  `Manager.CallTool` already uses for `"server:tool"`, entirely self-contained
  within the `upstream` package (no dependency on the `server` package's `__`
  convention).
- `Manager.GetPrompt(ctx context.Context, name string, args map[string]string) (*mcp.GetPromptResult, error)`
  takes a colon-qualified `"serverName:promptName"`, parses it via
  `strings.SplitN(name, ":", 2)` (identical to `Manager.CallTool`), resolves
  the owning client by name (same lookup pattern as `Manager.CallTool`), and
  forwards to `targetClient.GetPrompt(ctx, promptName, args)` with the
  unqualified prompt name. Errors (bad format, no matching client, upstream
  error) propagate unchanged to the caller — no swallowing.

### Server wiring

`internal/server/mcp.go` / `mcp_routing.go`:

- `registerPrompts()` is unchanged — still registers the 2 built-ins.
- New `RefreshPrompts()` on `MCPProxyServer`: early-returns if
  `!p.config.EnablePrompts`. Otherwise calls `p.upstreamManager.ListPrompts(ctx)`;
  for each colon-qualified `mcp.Prompt` returned, splits it back into
  `serverName`/`promptName` (the same inline `strings.SplitN(name, ":", 2)`
  pattern already used elsewhere in this package, e.g.
  `internal/server/mcp.go:1856`), builds a **display** copy of the prompt
  with `Name` rewritten to the client-facing `serverName__promptName` via a
  new one-line `FormatDirectPromptName` helper (a thin, readably-named
  wrapper around the existing `FormatDirectToolName` — same `__` separator,
  no duplicated logic), and pairs it with a `ServerPrompt.Handler` closure
  that captures the *original* colon-qualified name and calls
  `p.upstreamManager.GetPrompt(ctx, "serverName:promptName", request.Params.Arguments)`.
  The mcp-go library dispatches incoming `prompts/get` requests to this
  handler by matching the registered (client-facing, `__`) name, so no
  reverse-parsing of the `__` name is ever needed — the closure already
  knows which server it belongs to. Built-ins are added to the same slice
  unchanged, then `p.server.SetPrompts(all...)` atomically replaces the full
  prompt set — same pattern as `RefreshDirectModeTools`'s use of `SetTools`.
- Hook `RefreshPrompts()` into the existing `EventTypeServersChanged` branch
  in `listenForRoutingModeRefresh` (`internal/server/server.go:379-386`),
  alongside `RefreshDirectModeTools()` / `RefreshCodeExecModeTools()`. No
  separate manual call at startup is needed — upstream servers connect
  asynchronously and `servers.changed` fires once they do, matching the
  existing comment/pattern for direct-mode tools.

## Data flow

1. Upstream server connects → `EventTypeServersChanged` fires →
   `RefreshPrompts()` calls `Manager.ListPrompts` (colon-qualified names) →
   builds display copies (`__`-qualified) with bound handler closures →
   `SetPrompts` on `p.server`.
2. Client sends `prompts/list` → mcp-go's built-in handler returns the
   current `s.prompts` map (built-ins + aggregated, kept in sync by step 1).
3. Client sends `prompts/get` with a `__`-qualified name → mcp-go dispatches
   to that prompt's bound handler closure → calls `Manager.GetPrompt` with
   the closure's captured colon-qualified name → resolves server → forwards
   → returns the upstream's `GetPromptResult` unchanged. Built-in prompt
   names are unprefixed and never collide with an aggregated name (which is
   always prefixed by a unique server name).

## Error handling

- `Manager.ListPrompts`: per-server failure → log warning, skip that server,
  continue with the rest.
- `Manager.GetPrompt`: malformed name / unknown server / upstream error → all
  propagate as a normal MCP error to the client, same as `CallTool` today.
- No collision handling required: the qualifying prefix (colon internally,
  `__` for display) is always the connecting server's (unique) name.

## Testing

`Manager`, `managed.Client`, and `core.Client` are concrete types with no
mock-friendly interface boundary in this codebase today (confirmed: neither
`ListTools`/`CallTool` nor `DiscoverTools` have interface-based unit tests —
coverage there comes from real-but-disconnected `httptest` servers, e.g.
`internal/upstream/client_test.go`, or from the e2e suite). This feature
follows the same real-server approach rather than introducing mocks:

- `internal/upstream/core/client_test.go` (new): spin up a real
  `mcpserver.NewTestStreamableHTTPServer` backed by an `mcpserver.MCPServer`
  with `mcpserver.WithPromptCapabilities(true)` and 1-2 `AddPrompt`-registered
  prompts; connect a `core.Client` to it (`MCPPROXY_DISABLE_OAUTH=true`,
  mirroring `TestClient_Connect_WorkingTransports`); assert `ListPrompts`
  returns them and `GetPrompt` returns the expected `GetPromptResult`. A
  second case points at a test server with no prompt capability and asserts
  `ListPrompts` returns `nil, nil`. A third case sets
  `ExposePrompts: BoolPtr(false)` on the same capable server and asserts
  `ListPrompts` returns `nil, nil` without the test server ever being asked
  (verified via a call counter in the test server's list-prompts handler).
- `internal/upstream/manager_test.go` (new): using `Manager.AddServerConfig`
  + `Manager.ConnectAll` against two real test servers (one with prompts, one
  without) plus one server config pointed at a closed port (never connects),
  assert `Manager.ListPrompts` returns the capable server's prompts with
  colon-qualified names and silently omits the disconnected one. Separately,
  test `Manager.GetPrompt` resolution errors (malformed name with no colon,
  unknown server name) without needing any connected client, plus one
  success case against the real connected server.
- `internal/server/mcp_routing_test.go`: table tests for the new
  `FormatDirectPromptName` helper (pure function), alongside existing tests
  for `FormatDirectToolName`.
- `internal/server`: extract the aggregation-to-`ServerPrompt` step of
  `RefreshPrompts` into a pure helper,
  `buildAggregatedServerPrompts(builtins []mcpserver.ServerPrompt, upstreamPrompts []mcp.Prompt, getPrompt func(ctx context.Context, name string, args map[string]string) (*mcp.GetPromptResult, error)) []mcpserver.ServerPrompt`,
  so it can be unit-tested with a fake `getPrompt` func — no real server or
  manager needed. Table tests assert: built-ins pass through unchanged;
  colon-qualified upstream prompts get `__`-renamed; each aggregated
  prompt's handler calls `getPrompt` with the original colon-qualified name
  and forwards its result/error unchanged. `RefreshPrompts()` itself (the
  thin glue that calls `Manager.ListPrompts` and `SetPrompts`) is not
  separately unit-tested, consistent with its siblings
  `RefreshDirectModeTools`/`RefreshCodeExecModeTools`, neither of which have
  dedicated tests today.
- `internal/config/config_test.go`: round-trip `ExposePrompts` (nil / true /
  false) through JSON marshal-unmarshal on a `ServerConfig` value, asserting
  the pointer value survives the round trip in all three states.
- `go test ./internal/... -race` per repo convention.
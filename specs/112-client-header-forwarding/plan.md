# Implementation Plan: MCP Client Header Forwarding

**Branch**: `claude/mcp-client-header-forwarding-30d537` | **Date**: 2026-09-29 | **Spec**: [spec.md](spec.md)

## Summary

Capture allowlist-eligible headers once at the client-facing MCP edge (an mcp-go `WithHTTPContextFunc`), carry them as an immutable redacting value in the request context, and attach them only to the upstream `tools/call` POST through mcp-go's `WithHTTPHeaderFunc`. A second context key, set only inside `core.Client.CallTool`, makes every other upstream request (initialize, list, reconnect, background) structurally unable to forward. The header func enforces the deny list and "static and OAuth always win". Values are scrubbed from errors, traces and logs by exact value and by allowlisted name.

## Technical Context

- **Language**: Go 1.26, backend only (plus generated TS type and a read-only UI row).
- **Dependencies**: existing only (mcp-go v1.0.0, zap, bbolt). **No new dependencies.**
- **Storage**: one new `[]string` field on `UpstreamRecord`; one new top-level `*bool`.
- **Testing**: unit tests per package, `httptest` streamable upstream that records headers, `-race`, `./scripts/test-api-e2e.sh`, local isolated instance with `cmd/mcpfixture` extended by an `echo_headers` tool.
- **Constraints**: no per-request lock on shared state; no reconnect on allowlist edits; existing `CreateHTTPClient` timeout branches unchanged.

## Constitution Check

- Security by default: yes. Empty allowlist forwards nothing; deny list is enforced at runtime independent of config validation.
- No new dependencies: yes.
- Both editions: the capture point is shared, no build-tagged code.
- Tests first: every task in tasks.md writes the failing test first.

## Research & Decisions

| # | Decision | Rationale | Rejected alternatives |
|---|---|---|---|
| R1 | Capture in `WithHTTPContextFunc` inside `clientFacingStreamableOptions()` | One edit covers all five `/mcp*` mounts and aliases; per-POST by construction; REST paths never reach it | Router-wide middleware (would capture CLI/Web UI headers incl. `X-API-Key`); reading `request.Header` in every handler (many sites, easy to miss one) |
| R2 | Snapshot holds only the **union** of live allowlists, minus deny list | The Spec 082 workspace-roots goroutine keeps request ctx alive via `context.WithoutCancel` (`internal/server/workspace.go:72`); keeping the copy minimal limits retained secrets | Capturing all non-denied headers and filtering later |
| R3 | **Two ctx keys** (A: edge snapshot; B: per-server outbound set set only in `core.Client.CallTool`) | The header func sees only a ctx, not the JSON-RPC method. Reconnect-on-use, `ListTools` coalescing and refresh all run under the inbound ctx. Key B closes all of them at once | Scrubbing key A at every connect/list call site (many sites; a new one silently leaks) |
| R4 | `WithHTTPHeaderFunc`, not per-call `request.Header` | Returns a fresh map per invocation; `request.Header` is aliased and mutated by `sendHTTP` (`streamable_http.go:705-708`) | Per-call `request.Header` (free precedence, but aliasing hazard) |
| R5 | Forwarded is lowest precedence, enforced only by our func's filter (deny list + static-key drop). The order among the others is mcp-go v1.0.0's: transport headers, static, OAuth `Authorization`, then header func — mcp-go itself protects nothing | mcp-go applies the func last with `Header.Set`. Static headers are operator-controlled (often an upstream API key); letting a client override them would let any client impersonate the operator's credential | Forwarded overrides static (client can replace an operator secret); rejecting the whole request on collision (breaks calls for a config mistake) |
| R6 | Live policy via a provider closure from `managed.Client` | `CreateHTTPTransportConfig` and `core.Client` hold stale snapshots; `managed.Client.GetConfig/GetGlobalConfig` are atomic and updated on hot reload without reconnect | Pushing an atomic into core like `SetExposePrompts` (works but duplicates state); adding the field to `configChanged` (forces reconnects) |
| R7 | Name-only allowlist | The requirement is an explicit allowlist; renames or templates add injection and review surface with no request for them | `{"from":"X-A","to":"X-B"}` mapping; wildcard/prefix entries |
| R8 | Config validation strict at write, lenient at load, runtime filter authoritative | A hand-edited bad entry must not brick boot (memory: config-field-checklist point 5); runtime filter is the real boundary | Rejecting at boot via `validateDetailedCore` |
| R9 | Exact-value + name-pattern scrub at `core.Client.CallTool` return | Values are known at the call site; runs before `managed` classification so all six error sinks get clean text; name-pattern pass covers short values ("redaction keyed by user-selected names") | Global `RegisterResolvedSecret` (process-wide, never evicted, cross-client); relying on the shape-based sanitizer |
| R10 | `read_cache` digest fact (HMAC with per-process key) | Keeps large tenant-specific results from being redeemed by another client, without storing values | Disabling caching for forwarded calls (breaks large results) |
| R11 | Cross-origin redirect strips forwarded names | Go only strips `Authorization`/`Cookie`; custom headers follow 307/308 anywhere | Refusing all redirects (breaks renamed upstream URLs) |
| R12 | Deny `User-Agent`, `Origin`, `Referer` | mcpproxy logs `User-Agent` at Debug and `Origin` on reject; denying avoids per-request config reads in loggers | Masking those fields in the access log when allowlisted |
| R13 | Only `tools/call` in v1 | `prompts/get` is opt-in aggregated and rarely tenant-specific; resources are not proxied | Forwarding all request-scoped methods |
| R14 | Global key `forward_client_headers` (*bool, nil = on); per-server `forward_headers` | Distinct from Spec 107 `ForwardedHeaders`/`trusted_proxies` and from `auth_broker.header` (removed, `docs/features/auth-broker.md:12,26`) | `forward_headers` at global level; `headers_passthrough` |

## Data Model

```go
// internal/config/config.go
type Config struct {
    // ...
    ForwardClientHeaders *bool `json:"forward_client_headers,omitempty" mapstructure:"forward-client-headers"`
}
func (c *Config) IsClientHeaderForwardingEnabled() bool // nil => true; honours env override

type ServerConfig struct {
    // ...
    ForwardHeaders []string `json:"forward_headers,omitempty" mapstructure:"forward_headers"`
}

// internal/headerfwd (new package, no deps beyond stdlib + config)
type Snapshot struct{ h map[string]string } // canonical name -> joined value; immutable
func (Snapshot) String() string            // "forwarded_headers{names=[...] n=N}"
func (Snapshot) GoString() string
func (Snapshot) Format(fmt.State, rune)
func (Snapshot) MarshalJSON() ([]byte, error)
func (Snapshot) LogValue() slog.Value
func (Snapshot) MarshalLogObject(zapcore.ObjectEncoder) error

func Denied(name string) bool                                   // FR-004
func Capture(r *http.Request, union map[string]struct{}) Snapshot // FR-006, FR-012
func WithSnapshot(ctx, Snapshot) context.Context                // key A
func SnapshotFrom(ctx) (Snapshot, bool)
type Policy struct{ Enabled bool; Allow []string; Static map[string]string; Transport string }
func Outbound(s Snapshot, p Policy) Snapshot                     // FR-010
func WithOutbound(ctx, Snapshot) context.Context                // key B
func OutboundFrom(ctx) (Snapshot, bool)
func HeaderFunc(static map[string]string) transport.HTTPHeaderFunc // reads key B only; re-filters
func Scrub(text string, s Snapshot, allow []string) string       // FR-016
func Digest(s Snapshot) string                                    // FR-017, per-process HMAC key
func ValidateNames(names []string, static map[string]string) []error // FR-005(a)
func NormalizeNames(names []string) (kept []string, dropped []string) // FR-005(b)
```

## Flow

```
AI client POST /mcp (X-User-Id: alice, Authorization: Bearer <mcpproxy token>)
  -> mcpAuthMiddleware (unchanged)
  -> mcp-go handlePost -> contextFunc: Capture(r, union of live allowlists) -> key A
  -> call_tool_read handler -> Manager.CallTool(ctx) -> managed.Client.callTool(ctx)
     [reconnect_on_use: Connect(ctx) -> initialize: header func sees no key B -> nothing]
  -> core.Client.CallTool(ctx):
       policy := c.forwardPolicy()           // live, from managed provider
       out := Outbound(SnapshotFrom(ctx), policy)
       callCtx := WithOutbound(WithTimeout(ctx), out)
       res, err := client.CallTool(callCtx, req)
       err = scrubbed(err, out, policy.Allow)  // before return / logging
  -> mcp-go sendHTTP: ... static, OAuth, then HeaderFunc(callCtx) -> only key B, re-filtered
  -> http.Client.Do (CheckRedirect strips key-B names on cross-origin redirect)
```

## Wiring

| Place | Change |
|---|---|
| `internal/server/server.go:2889` | `clientFacingStreamableOptions(cfgProvider func() *config.Config)` adds `server.WithHTTPContextFunc`; update 5 call sites and the doc comment; test enumerating all five mounts |
| `internal/transport/http.go` | `HTTPTransportConfig.HeaderFunc`; append `transport.WithHTTPHeaderFunc` in both branches outside `len(headers)` branches; always pass `WithHTTPBasicClient` with a proxy-built client carrying `CheckRedirect` on **all** branches (OAuth with/without trace or Retry-After, plain with/without static headers, trace), replacing `WithHTTPTimeout` and preserving each branch's timeout (plain no-headers 180s, plain+headers none, OAuth none, trace 180s) — mcp-go's default `&http.Client{}` has no redirect filter; `CreateSSEClient` untouched |
| `internal/transport/logging.go` | mask key-B / allowlisted names in request **and response** headers, stdout and zap, before `RedactHeaders` (FR-018) |
| `internal/upstream/core/client.go` | `SetForwardPolicyProvider(func() headerfwd.Policy)`; `CallTool` sets key B, scrubs error + logged result copies, and returns the call's outbound set to its caller (per-call return, e.g. via a result wrapper, never stored on the client) |
| `internal/server/mcp.go`, `mcp_routing.go`, `mcp_code_execution.go` (activity / tool-call recording) | scrub the result copy written to activity `Response` and `ToolCallRecord` with the call's outbound set (FR-016.3) |
| `internal/upstream/managed/client.go` | installs the provider on its core client after construction and reconnect (reads `GetConfig`, `GetGlobalConfig`); one-time plain-HTTP and SSE/stdio warnings |
| `internal/server/mcp.go`, `internal/cache` | `read_cache` store/admit with `ForwardedDigest` |
| `internal/config` | fields, helper, env override Field, `ValidateDetailed` clause, load-time normalization, `CopyServerConfig`, `MergeServerConfig`, `DetectConfigChanges` clause |
| `internal/storage` | `UpstreamRecord.ForwardHeaders` in 5 conversion sites |
| `internal/contracts`, `cmd/generate-types`, `frontend/src/types/contracts.ts`, `oas/` | field + regenerate |
| `internal/httpapi/server.go`, `internal/server/mcp.go` (`upstream_servers`) | create/patch, preserve-on-omit |
| `internal/oauth/serverfields.go` | mask decision row |
| `cmd/mcpfixture` | `echo_headers` tool (via fixture-side `WithHTTPContextFunc`) for local verification |
| `docs/configuration.md` | new section, Server Fields row, env var row, validation bullets |

## PR slicing

- **PR A** (backend, one PR): headerfwd package, config/storage/API wiring, transport + core, redaction, read_cache, tests, docs.
- **PR B** (optional): read-only display in Web UI server detail and macOS.

## Risks

- A future code path that calls `client.CallTool` outside `core.Client.CallTool` would not forward (fail-closed, acceptable).
- mcp-go upgrade could change `sendHTTP` order; a test asserts OAuth/static still win with a forwarded collision.
- Forwarded values are unverified client assertions (spec FR-022); docs must say upstreams authenticate them.
- Echo scrubbing (errors, success-result sinks, trace headers) is best-effort by design (spec FR-015b); the absolute guarantee is only for values mcpproxy itself holds.
- Error scrubbing cannot catch transformed echoes (spec Known Limitation 7).

## Verification

- `go test -race ./internal/headerfwd/... ./internal/transport/... ./internal/upstream/... ./internal/config/... ./internal/storage/... ./internal/oauth/...`
- `go test -race -tags server -timeout 20m -skip "E2E|Binary|MCPProtocol|TestInfoEndpoint|TestGracefulShutdownNoPanic|TestSocketInfoEndpoint" ./internal/server/... ./internal/httpapi/...`
- `golangci-lint run --config .github/.golangci.yml ./...` and again with `--build-tags server`
- `make swagger` + `swagger-verify`; `go run ./cmd/generate-types`
- `./scripts/test-api-e2e.sh`
- Real instance: isolated `HOME`/data dir, high port, `mcpfixture` streamable upstream with `echo_headers`, curl JSON-RPC to `/mcp` with allowlisted + denied headers; grep logs for the sentinel.
- Cross-model review per the user's instruction (codex, Luna-class model), stdin closed, `gtimeout`.

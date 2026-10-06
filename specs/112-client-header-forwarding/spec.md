# Feature Specification: MCP Client Header Forwarding to Upstream Servers

**Feature Branch**: `claude/mcp-client-header-forwarding-30d537` (spec number 112; `112-client-header-forwarding`)
**Created**: 2026-09-29
**Status**: Draft
**Input**: User description: "Implement forwarding of MCP client HTTP headers to upstream servers. Add a forwarding setting that is enabled by default and can be disabled. Users must explicitly list the headers to forward in an allowlist for each upstream server. Forward only allowlisted headers from the current inbound HTTP request; do not store them in shared client or upstream state. Never forward the inbound `Authorization` header; always exclude `Host` and hop-by-hop headers; do not allow forwarding to override headers managed by the transport or proxy. Never write forwarded header values to logs, HTTP traces, errors, activity or audit logs; redaction must cover user-selected header names. Support HTTP-based upstream transports where per-request forwarding is compatible. Preserve compatibility with static upstream `headers` and existing OAuth." Corrections: SSE transport is deprecated and out of scope.

**Related**: Spec 105 (agent-scope hardening: `read_cache` authorization frames), Spec 107 (audit line contract, `trusted_proxies` / `config.ForwardedHeaders`, which is a *different* feature: trusting inbound `X-Forwarded-*`), memory note `project_client_header_forwarding_gcore` (the Gcore "all users share one LiteLLM key" request that motivates this). Spec number 111 is taken by `origin/111-personal-docker-image`; this spec is 112.

**Glossary**:
- *Client header forwarding* (this spec): copying named headers from the inbound `/mcp` HTTP request made by an AI client into the outbound HTTP request mcpproxy sends to an upstream MCP server.
- *Forwarded headers* in Spec 107 (`trusted_proxies`, `X-Forwarded-For`) are unrelated. This spec never uses that phrase for its own mechanism.
- *Static headers*: the existing per-server `headers` map in config.

## Context & Motivation

These facts were checked at `4ecc5bb12` (worktree `hopeful-poincare-caabb4`) and mcp-go `v1.0.0`:

1. **One option helper builds every client-facing MCP server.** All five `StreamableHTTPServer`s (`/mcp`, `/mcp/all`, `/mcp/code`, `/mcp/call`, `/mcp/p/<slug>`, plus the legacy `/v1/tool_code` aliases) are built with `clientFacingStreamableOptions()` (`internal/server/server.go:2889`, used at `:1115,2971,2978,2985,2994`). None uses `server.WithHTTPContextFunc`. No code in `internal/` reads inbound request headers other than Origin, the pprof gate and two access-log fields.
2. **mcp-go builds the tool-handler context per POST.** `handlePost` derives the ctx from the request, then calls `contextFunc(ctx, r)` if one is set, then stores the raw `r.Header` map under an **unexported** key (`mcp-go server/streamable_http.go:697-708`, `server/ctx.go`). GET listen streams and DELETE never call `contextFunc` (`streamable_http.go:1012-1219`). mcpproxy has no way to read the inbound headers today.
3. **The request ctx reaches the upstream call undetached.** `call_tool_*` → `dispatchOnEpoch` → `Manager.CallToolOnEpoch/CallTool` (`internal/server/mcp.go:3178-3181,8044-8051`); direct surface → `mcp_routing.go:669-681`; `code_execution` sub-calls run under `ExecutionContext.ctx` derived from the request (`internal/jsruntime/runtime.go:339-345,383-391`) and call the managed client directly (`mcp_code_execution.go:1003-1005`). All reach `core.Client.CallTool` (`internal/upstream/core/client.go:448`, `client.CallTool(callCtx…)` at `:515`).
4. **The same inbound ctx also drives shared-connection work.** `reconnect_on_use` runs `managed.Client.Connect` → `Start` + `initialize` under a ctx derived from the caller's (`internal/upstream/manager.go:1626-1651`, `managed/client.go:465-480`, `core/connection_http.go:175-211`). `ListTools` coalesces concurrent callers onto one leader ctx and shares the result (`managed/client.go:877-991`). `upstream_servers refresh` and quarantine inspection call `ListTools(ctx)` with the request ctx (`mcp.go:3917,4956,5312`). A ctx-only design would leak one client's headers into a shared session or a shared tool index.
5. **mcp-go applies the per-request header hook last.** `sendHTTP` sets, in order: Content-Type, Accept, `Mcp-Session-Id`, `Mcp-Protocol-Version`, static headers, Host, OAuth `Authorization`, then `headerFunc(ctx)` — all with `Header.Set` (`client/transport/streamable_http.go:705-755`). An unfiltered hook overrides the OAuth token, static headers and the session id. mcp-go does no conflict protection. The hook is not called for the `Close()` DELETE (`:230-270`).
6. **One choke point builds streamable upstream clients.** `transport.CreateHTTPClient` (`internal/transport/http.go:169`) has an OAuth branch (`:186-254`, `WithHTTPHeaders` at `:241`) and a plain branch (`:256-298`, `WithHTTPHeaders` at `:263`). The plain branch's timeout depends on `len(headers)` (`:266-291`). SSE uses the separate `CreateSSEClient` (`:301-427`). `auto` resolves to streamable HTTP and never falls back to SSE (`DetermineTransportType`, `:442-465`).
7. **Config snapshots go stale.** `CreateHTTPTransportConfig` copies the server config at connect time (`http.go:431`); `core.Client` keeps a global-config snapshot (`core/client.go:496`). `Manager.SetGlobalConfig` pushes the live config only to managed clients (`manager.go:380`, `managed/client.go:1500`), and `managed.Client.SetConfig` swaps the server config without reconnecting (`managed/client.go:739`). `AddServerConfig` reconnects only when URL/Protocol/Command/Args/Env/Headers/Enabled/Quarantined change (`manager.go:436-482`).
8. **Existing redaction is name- and shape-based.** `oauth.RedactHeaders` masks well-known secret names (`internal/oauth/logging.go:215-229`); the zap `SecretSanitizer` matches vendor token shapes and skips `zap.Error`/`zap.Any(map)` fields (`internal/logs/sanitizer.go:201-228`). A user-chosen `X-Tenant-Id: acme-42` passes both. The trace transport prints request headers to stdout (`internal/transport/logging.go:50-51`). `RegisterResolvedSecret` is a process-global never-evicted cache and must not be used for per-request values (`sanitizer.go:147-175`).
9. **Upstream error text reaches six sinks verbatim.** mcp-go puts non-2xx bodies into the error (`streamable_http.go:641`). That text lands in activity `ErrorMessage`, BBolt `ToolCallRecord.Error`, the per-server log, `main.log` via `zap.Error`, the client error result (`mcp.go:3306-3340`) and server health `LastError` (`managed/client.go:1180`). An upstream that echoes request headers in an error body would leak forwarded values into all six.
10. **Proxy credentials arrive as headers.** `ExtractToken` accepts `X-API-Key` and `Authorization: Bearer` (`internal/httpapi/server.go:754-765`); the server edition uses the `mcpproxy_session` cookie (`httpapi/server.go:582`). mcpproxy interprets `X-Forwarded-*` via `trusted_proxies` (`internal/config/config.go:393`).
11. **Large results are shared via `read_cache`.** `cache.Manager.StoreAs` stamps results with the producer's authorization (`internal/cache/manager.go:155`); anonymous and admin callers share one caller kind. A tenant-specific result could be redeemed by another client.
12. **Go follows redirects with custom headers.** The upstream `http.Client` sets no `CheckRedirect` (`http.go:283-286`); Go strips only `Authorization`, `WWW-Authenticate` and `Cookie` on a cross-domain redirect, so a forwarded custom header would follow a 307/308 to any host.

## Scope Boundary

| Already exists | Reused, not rebuilt |
|---|---|
| `clientFacingStreamableOptions()` shared by all five mounts | Gains one `WithHTTPContextFunc` (becomes a closure over a live config provider) |
| mcp-go `transport.WithHTTPHeaderFunc` | Used as the only injection point, with our own filter inside |
| Static `headers` map and OAuth token injection in mcp-go | Unchanged; both win over forwarded headers |
| `managed.Client.GetConfig/GetGlobalConfig` (live, atomic) | Source of the live allowlist and global switch |
| `internal/config/process_overrides.go` Field registry | Env kill switch `MCPPROXY_FORWARD_CLIENT_HEADERS` |
| Spec 105 `read_cache` authorization frames | Gain one fact: a digest of the forwarded set |
| Audit line (Spec 107), activity records | **Unchanged**. No new field, no header names or values |

**Out of scope**: the legacy SSE transport (`protocol: sse`), stdio and Docker-isolated stdio servers, `prompts/get` forwarding, header renaming or value templating, wildcard/prefix allowlists, forwarding from REST (`/api/v1/*`) or CLI callers, forwarding on `initialize`/`tools/list`, Web UI and macOS tray editing of the allowlist (display only).

## User Scenarios & Testing

### User Story 1 — Per-user identity reaches a shared upstream (Priority: P1)

An operator runs one mcpproxy in front of a shared HTTP MCP gateway (e.g. LiteLLM). Each developer's AI client sends `X-User-Id` and `X-Tenant-Id`. The operator lists those two names under the upstream's `forward_headers`. Each tool call reaches the gateway with the values that developer's client sent.

**Trust boundary**: forwarded values are *client assertions*, not authenticated identity. Any client that can reach `/mcp` can send any value, including another user's `X-User-Id`. mcpproxy copies the values; it does not verify them (FR-022). The upstream must authenticate these values itself (e.g. a signed or upstream-issued per-user token), or the operator must accept that every client able to reach mcpproxy is trusted to assert any identity. The concurrent-client tests prove values stay attached to their own request; they do not prove who sent them.

**Independent test**: an `httptest` streamable upstream records received headers. Two MCP clients with different `X-User-Id` values call the same tool.

1. **Given** `forward_headers: ["X-User-Id"]` on server `gw` and forwarding enabled, **When** a client calls `call_tool_read gw:echo` with `X-User-Id: alice`, **Then** the upstream `tools/call` POST carries `X-User-Id: alice`.
2. **Given** the same config, **When** the inbound request has no `X-User-Id`, **Then** the upstream request carries no `X-User-Id` (omitted, not empty).
3. **Given** a header not in the allowlist (`X-Other: 1`), **Then** it is never sent upstream.
4. **Given** server `other` has no `forward_headers`, **When** the same client calls a tool on `other`, **Then** nothing is forwarded to `other`.

### User Story 2 — Concurrent clients never see each other's headers (Priority: P1)

**Given** 50 concurrent clients each sending a unique `X-User-Id` to the same upstream (one shared upstream session), **Then** every upstream `tools/call` carries exactly its own caller's value, and `go test -race` reports no race.

### User Story 3 — Secrets and proxy-managed headers can never be forwarded (Priority: P1)

1. **Given** `forward_headers: ["Authorization"]` (any case), **When** written through REST/MCP, **Then** the write is rejected with a clear error naming the header. **When** hand-edited into the config file, **Then** boot succeeds, a name-only warning is logged, and the header is never forwarded.
2. **Given** an OAuth upstream and an inbound `Authorization: Bearer mcp_agt_…`, **Then** the upstream receives the proxy-obtained OAuth token and never the inbound token.
3. **Given** `Mcp-Session-Id`, `Host`, `Connection`, `X-API-Key`, `Cookie`, `X-Forwarded-For`, `traceparent` or any other denied name (FR-004) in the allowlist, **Then** none is forwarded.

### User Story 4 — Static headers and OAuth keep working (Priority: P1)

1. **Given** a static header `X-Api-Token: operator-secret` and `forward_headers: ["X-Api-Token"]`, **When** a client sends `X-Api-Token: attacker`, **Then** the upstream receives `operator-secret` (static wins). Write-time validation rejects the collision; runtime drops it regardless.
2. **Given** an existing config with static headers and/or OAuth and no `forward_headers`, **Then** upstream requests are byte-for-byte unchanged from today (including the timeout policy of `CreateHTTPClient`).

### User Story 5 — Operators can turn it off (Priority: P1)

1. **Given** `"forward_client_headers": false`, **Then** nothing is forwarded to any server, whatever the allowlists say.
2. **Given** `MCPPROXY_FORWARD_CLIENT_HEADERS=false` in the environment, **Then** forwarding is off for the process and the setting is **not** persisted to `mcp_config.json`.
3. **Given** a hot reload that flips the switch or edits an allowlist, **Then** the next tool call uses the new value, with no upstream reconnect.

### User Story 6 — Nothing leaks into logs, traces, errors, activity or audit (Priority: P1)

1. **Given** an allowlisted header with a unique sentinel value and an upstream that echoes all request headers in a 500 body, **When** the call fails at Debug log level with trace transport on, **Then** the sentinel appears in none of: `main.log`, the per-server log, trace stdout, the activity record, the BBolt tool-call record, the audit log line, server health `LastError`, or the error returned to the client.
2. **Given** the same header and an upstream that returns a **successful** result echoing the headers (exact, JSON-escaped and `Name: value` forms) and echoes the header back as a response header, **Then** the client receives the unmodified result, and the sentinel appears in none of the other sinks listed above (activity `Response`, tool-call record, logs, trace stdout).
3. **Given** the upstream echoes the value base64-encoded, **Then** it is not detected (documented boundary, Known Limitation 7); the test asserts this so the boundary is explicit.

### Edge cases

- **Multi-value inbound header** (`X-Tag: a` + `X-Tag: b`): values are joined with `", "` in arrival order (RFC 9110 §5.3) and sent as one field.
- **Oversized value**: a single value over 4 KiB, or a forwarded set over 16 KiB total, is dropped (not truncated), in allowlist order, with a Debug log naming the header only.
- **Invalid value** (CR, LF, NUL or other control characters except HTAB): the header is dropped for that request.
- **Empty value**: omitted.
- **Case**: `x-user-id`, `X-USER-ID` and `X-User-Id` in config or on the wire all match; names are canonicalized with `http.CanonicalHeaderKey`.
- **Duplicate allowlist entries** (case-insensitive): de-duplicated at load.
- **`reconnect_on_use` triggered by client A's call**: the `initialize` POST carries no forwarded headers; A's `tools/call` that follows does.
- **Upstream 307/308 to another origin**: forwarded headers are removed before the redirected request; same-origin redirects keep them.
- **`code_execution`**: every nested `call_tool()` / `call_tools()` sub-call in one script run forwards the headers of the one inbound request that started the script, each filtered by its target server's allowlist.
- **Tool call over the tray Unix socket**: it is a real HTTP `/mcp` request, so forwarding applies the same way.
- **stdio MCP serving mode** (no HTTP): nothing is forwarded.
- **Allowlist on an `sse` or `stdio` server**: accepted, inert, warned at load and write (name-only).
- **Upstream on plain `http://` to a non-loopback host**: forwarding applies (as static headers already do) and a warning is logged once per server at connect.

## Requirements

### Functional Requirements

**Configuration**

- **FR-001 (global switch)**: A top-level `forward_client_headers` (`*bool`, `json:"forward_client_headers,omitempty" mapstructure:"forward-client-headers"`) MUST default to enabled when absent (`Config.IsClientHeaderForwardingEnabled()` returns true for nil). `false` disables forwarding for every server. Because every allowlist is empty by default, the default forwards nothing.
- **FR-002 (per-server allowlist)**: `ServerConfig.ForwardHeaders []string` (`json:"forward_headers,omitempty" mapstructure:"forward_headers"`) lists inbound header **names**. Absent or empty means forward nothing. Entries are exact names (no wildcards, prefixes, renames or value templates), matched case-insensitively. At most 32 entries. It is a separate field from `headers` and holds no values.
- **FR-003 (env kill switch)**: `MCPPROXY_FORWARD_CLIENT_HEADERS=false|0|off` MUST disable forwarding for the process through the process-only override registry, so no save path persists it.

**Exclusions**

- **FR-004 (deny list)**: The following names MUST never be forwarded, compared on canonical form:
  - Credentials and identity: `Authorization`, `Proxy-Authorization`, `X-Api-Key`, `Cookie`, `Set-Cookie`, `Forwarded`, `X-Real-Ip`, and the prefix `X-Forwarded-`.
  - Host and hop-by-hop: `Host`, `Connection`, `Keep-Alive`, `Proxy-Connection`, `Te`, `Trailer`, `Transfer-Encoding`, `Upgrade`, `Expect`, the prefix `Proxy-`, and any name listed in the inbound request's `Connection` header.
  - Transport-managed: `Content-Type`, `Content-Length`, the prefix `Content-`, `Accept`, `Accept-Encoding`, `Range`, the prefix `If-`, `Last-Event-Id`, and the prefix `Mcp-` (covers `Mcp-Session-Id`, `Mcp-Protocol-Version`, `Mcp-Method`, `Mcp-Name`, `Mcp-Param-*`).
  - Tracing and proxy-managed: `Traceparent`, `Tracestate`, `Baggage`, `X-Request-Id`, the prefix `X-Mcpproxy-`, the prefix `Sec-`.
  - Logged by mcpproxy's access log or host validation: `User-Agent`, `Origin`, `Referer`.
- **FR-005 (three enforcement layers)**: (a) REST, MCP `upstream_servers` and `/config/apply` writes MUST reject a denied name, an invalid token name (RFC 9110 `token`), a wildcard, more than 32 entries, or a name that case-insensitively equals a key of the same server's static `headers`, with `ValidationError{Field:"mcpServers[i].forward_headers", …}` via `ValidateDetailed`. (b) Load (boot, hot reload) MUST NOT fail on these; it drops the offending entries and logs a warning naming the header and server only. (c) The runtime filter (FR-010) MUST re-apply the deny list and static-name collision on every request. Layer (c) is the security boundary; (a) and (b) are UX.

**Capture and scope**

- **FR-006 (capture)**: Headers MUST be captured only by a `server.WithHTTPContextFunc` inside `clientFacingStreamableOptions()`, which therefore covers all five `/mcp*` mounts and legacy aliases. The capture reads the live config and copies into an immutable snapshot only headers whose canonical name is in the **union** of allowlists of servers that currently resolve to streamable HTTP, minus the deny list, and only when forwarding is enabled. It MUST clone values, never alias `r.Header`, and never read the body. No other inbound path (REST `/api/v1/*`, code-exec REST, replay, CLI, stdio serving, tray REST) captures headers.
- **FR-007 (snapshot type)**: The snapshot type MUST implement `String`, `GoString`, `Format`, `MarshalJSON` and `slog.LogValuer`/`zapcore.ObjectMarshaler` so that any accidental formatting prints only the names and a count (e.g. `forwarded_headers{names=[X-User-Id] n=1}`), never values.
- **FR-008 (which upstream requests carry headers)**: Forwarded headers MUST be attached only to the `tools/call` HTTP POST made by `core.Client.CallTool` for a call that originates from an inbound `/mcp` POST: `call_tool_read|write|destructive`, direct-surface `server__tool` calls, and `code_execution` nested sub-calls. They MUST NOT be attached to: `initialize`, `server/discover`, `notifications/*`, `tools/list`, `prompts/*`, `ping`, the GET listen stream, `subscriptions/listen`, the `Close()` DELETE, responses to server-initiated requests (enforced by the body-inspecting RoundTripper gate), reconnect and `reconnect_on_use`, health checks, background discovery and indexing, `upstream_servers refresh`, quarantine inspection, REST-originated calls (`/api/v1/tools/call`, code-exec REST, replay), the CLI in-process client, or manual OAuth flows.
- **FR-009 (two-key gating)**: The capture stores the snapshot under a private ctx key A. `core.Client.CallTool` (only) derives the per-server outbound set from key A (FR-010) and puts it under a second private key B on the `callCtx` it passes to mcp-go. The header func reads **only key B**. Connect, initialize, list and all other paths never set key B, so they cannot forward even when key A is present in their ctx.
- **FR-010 (per-server filter and precedence)**: The outbound set for server S is computed per call from the snapshot using S's **live** config (`managed.Client.GetConfig()`) and the live global switch (`GetGlobalConfig()` plus the env override): keep a name only if forwarding is on, S's transport is `http`/`streamable-http`, the name is in S's allowlist, not denied (FR-004), not a static-header key of S, and within size limits (FR-012). **Forwarded headers have the lowest precedence**: a forwarded header never replaces any header mcp-go, OAuth, static config or mcpproxy set. mcp-go does not enforce this: it applies transport headers, then static headers, then OAuth `Authorization`, then the header func last, each with `Header.Set` (Context fact 5), so the func could override anything. The guarantee comes only from the proxy's filtering: the header func never emits a denied name (FR-004 covers every name mcp-go or OAuth sets) or a static-header key of S. The relative order of transport, static and OAuth headers is mcp-go's and is unchanged by this feature (a static header can already override some transport headers today). The header func returns a fresh map per invocation and re-applies the deny list and static-name check (defense in depth).
- **FR-011 (no shared state)**: Forwarded names or values MUST NOT be stored on `Manager`, `managed.Client`, `core.Client`, the mcp-go client or transport, any session object, the tool index, or any cache key or value except the digest in FR-017.
- **FR-012 (limits and encoding)**: Per value ≤ 4 KiB, total ≤ 16 KiB per outbound request; oversized headers are dropped whole in allowlist order. Multi-value inbound fields are joined with `", "`. Values containing bytes 0x00-0x08, 0x0A-0x1F or 0x7F are dropped. Empty values are omitted.
- **FR-013 (redirects)**: The upstream `http.Client` MUST have a `CheckRedirect` that, when the request ctx carries key B, deletes the forwarded names from the redirected request when its origin (scheme, host, port) differs from the original. Behaviour for requests without key B is unchanged (Go's default ≤10 redirects). This MUST hold on **every** streamable-HTTP client path, not only those that already build their own `http.Client`: today mcp-go's default `&http.Client{}` (no `CheckRedirect`) is used by (a) the plain branch with no static headers (`WithHTTPTimeout(180s)` only mutates that default client's timeout), (b) the plain branch with static headers and no trace/Retry-After, and (c) the OAuth branch without trace/Retry-After. `CreateHTTPClient` MUST therefore always pass `WithHTTPBasicClient` with a proxy-built client carrying the `CheckRedirect`, replacing `WithHTTPTimeout`, and preserving each branch's existing timeout exactly (plain no-headers: 180s; plain with headers: none; OAuth: none; trace: 180s) and transport (`http.DefaultTransport`, or `upstreamRoundTripper` when trace/Retry-After is on).

**Transports**

- **FR-014 (transport support)**: Forwarding applies to `protocol: http`, `streamable-http`, and `auto` resolving to streamable HTTP, in both the OAuth and plain branches of `CreateHTTPClient`, including launcher-spawned HTTP servers. `WithHTTPHeaderFunc` MUST be appended unconditionally, outside the `len(headers)` timeout branches, so the existing HTTP client and timeout policy are unchanged. `CreateSSEClient` and stdio are not changed; an allowlist on those servers is inert and warned about (name-only).

**Redaction**

- **FR-015 (never log values)**: two guarantees with different strength.
  - **(a) Absolute, for values mcpproxy holds**: mcpproxy code MUST NOT write a value taken from the inbound headers (the key A or key B snapshot) to any zap log, stdout print, error string, activity record, BBolt tool-call record, audit line, SSE event, OTLP span attribute or health field. Forwarding code may log only allowlisted header names and counts, at Debug. This is enforced by construction (FR-007 redacting type, no value accessors outside `headerfwd`/the header func) and tested with a sentinel through every sink.
  - **(b) Best-effort, for upstream echoes**: text that comes *back* from the upstream (error bodies, successful results, response headers) is upstream data. mcpproxy scrubs it before any sink per FR-016 and FR-018. The guarantee covers exactly the forms FR-016 lists; transformed echoes are not detected (Known Limitation 7).
- **FR-016 (echo scrubbing)**: `Scrub(text, out, allow)` replaces (a) each forwarded value of length ≥ 4, matched exactly and also in its JSON-string-escaped form, with `[forwarded:<Name>]`, and (b) for every allowlisted name of S regardless of value length, the name-anchored forms `<Name>: <value>`, `"<Name>":"<value>"` (optional whitespace around `:`) and `<Name>=<value>` (case-insensitive name). It is applied at these points, and these are the whole boundary:
  1. `core.Client.CallTool` scrubs its returned **error** before returning, i.e. before `managed.Client` classification, so health `LastError`, activity `ErrorMessage`, `ToolCallRecord.Error`, logs and the client error all receive scrubbed text.
  2. `core.Client.CallTool` scrubs every copy of the **result** it logs (the Debug `formatted_json` dumps).
  3. On **success**, the result returned to the calling client is not modified (the client sent the value itself). Every copy of that result written to a sink — activity `Response`, `ToolCallRecord` response fields, any server-layer log of the result — MUST be scrubbed with the same call's outbound set. `core.Client.CallTool` returns that set to its caller through a per-call return value (not shared state), and the recording code in `internal/server` passes it to `Scrub` before building the record.
  4. The trace transport scrubs request and response headers (FR-018).
  `read_cache` stores the unscrubbed result, isolated by FR-017, because it is only redeemable by a request with the same forwarded set.
- **FR-017 (read_cache isolation)**: A result produced by a call that forwarded ≥1 header MUST be stored in `read_cache` with a `ForwardedDigest` fact = HMAC-SHA256(per-process random key, sorted canonical `name=value` pairs). `read_cache` admits the entry only when the redeeming request's digest for the same server is equal. The digest is never logged or returned.
- **FR-018 (trace transport)**: `LoggingTransport` MUST print `[forwarded]` instead of the value for every request **and response** header whose name is in key B of `req.Context()` or in the server's allowlist, before `oauth.RedactHeaders`, in both the stdout print and the zap field. This must hold for arbitrary allowlisted names that `RedactHeaders` does not know (e.g. `X-Tenant-Id`). Header names stay visible (names are not secret, FR-015a).
- **FR-019 (audit and activity)**: No field is added to the audit line schema, `audit.Attempt`, `ActivityRecord` or its `Metadata`. Header names are not recorded there either.

**Trust**

- **FR-022 (no identity claim)**: mcpproxy MUST NOT treat, label, document or log a forwarded header as an authenticated identity. Documentation (T033) MUST state that forwarded values are unverified client assertions, that upstreams relying on them for authorization must authenticate them independently, and that `require_mcp_auth` plus per-user agent tokens only authenticate the caller to mcpproxy, not the forwarded values. Binding a forwarded value to the authenticated mcpproxy caller (a "trusted identity source") is out of scope for v1.

**Surfaces**

- **FR-020 (config surfaces)**: `forward_headers` MUST round-trip through the config file, BBolt `UpstreamRecord` (all five conversion sites), `CopyServerConfig`, `MergeServerConfig` (non-nil replaces; `[]` clears), REST create/PATCH (omitted = preserved), MCP `upstream_servers` add/patch, `contracts.Server` and the generated TS type, and `oas/swagger.yaml`. `forward_client_headers` MUST be detected by `DetectConfigChanges` as a resolved-bool comparison. An allowlist change MUST NOT trigger an upstream reconnect. Web UI and macOS show the allowlist read-only; CLI and UI editing are deferred.
- **FR-021 (secret-mask table)**: `oauth.ServerFieldMaskDecisions` gets `forward_headers: MaskDecisionNotSecret` (names only).

### Success Criteria

- **SC-001**: With 50 concurrent clients and unique values, 100% of upstream `tools/call` requests carry exactly their caller's value; 0 initialize/tools/list requests carry any forwarded header; `-race` clean.
- **SC-002**: For every name in FR-004 (three casings each), under plain, static-header and OAuth upstreams, 0 forwarded occurrences; the OAuth upstream always receives the proxy token.
- **SC-003**: A sentinel value is found 0 times across the eight sinks in User Story 6, for both the 500-error echo and the successful-result echo, in every form FR-016 lists.
- **SC-004**: With `forward_client_headers: false` or the env switch, 0 forwarded headers; the env switch never appears in the saved config.
- **SC-005**: Existing transport, OAuth, static-header and config tests pass unchanged; `CreateHTTPClient` timeout behaviour for configs without `forward_headers` is identical.
- **SC-006**: On a real local instance (isolated data dir, non-default port) with a header-echoing streamable upstream, a `tools/call` through `/mcp` shows the allowlisted header upstream and none of the denied ones; logs contain no sentinel.
- **SC-007**: Each of the five client-facing mounts (`/mcp`, `/mcp/all`, `/mcp/code`, `/mcp/call`, `/mcp/p/<slug>`) and the legacy `/v1/tool_code` aliases forwards an allowlisted header on a `tools/call`, verified by an integration test that enumerates the mounts from the router (so a new mount without the hook fails the test); REST `/api/v1/tools/call` forwards nothing.

## Assumptions

- Operators who forward identity headers either run an upstream that authenticates them or accept that all clients reaching mcpproxy are trusted to assert them (FR-022).
- AI clients can be configured to send custom HTTP headers on their MCP connection (most do: Claude Code `headers`, Cursor, LiteLLM).
- Upstreams that identify users per request (gateways) are the target. Upstreams that bind identity at `initialize` to the session are not served by this feature, because one upstream session is shared by all clients (Known limitations).
- A per-request header value is at most a few KiB.

## Known Limitations

1. **Shared session.** mcpproxy keeps one upstream session per server; `initialize` never carries client headers. Session-bound identity is not supported.
2. **tools/call only.** `tools/list`, prompts and resources never carry forwarded headers; tool definitions are client-independent. Resources are not proxied at all; `prompts/get` is deferred.
3. **SSE and stdio** servers are not supported (SSE is deprecated).
4. **Server-initiated messages** (sampling, elicitation, roots) and the DELETE on close carry no forwarded headers. mcp-go reuses the tools/call context for the reply POSTs to server-initiated requests, so `internal/transport/headerfwd_gate.go` (a RoundTripper wrapping every streamable client) strips forwarded names from any request that is not a single JSON-RPC `tools/call` request object (batches, responses, notifications and unparseable bodies are stripped; fail closed). mcp-go's DELETE also skips static and OAuth headers (pre-existing).
5. **Single-valued output.** Multi-value inbound headers are comma-joined.
6. **Plain HTTP** to a non-loopback upstream sends values in cleartext (warned, same as static headers).
7. **Echo scrubbing is best-effort** (FR-015b, FR-016): an upstream that transforms a value (base64, URL-encoding, hashing, splitting, case change) before echoing it in an error or a result is not caught, and values shorter than 4 characters are caught only in the name-anchored forms.
8. **Unverified values.** Forwarded headers are client assertions; mcpproxy does not authenticate them (FR-022).

## Commit Message Conventions *(mandatory)*

### Issue References
- ✅ **Use**: `Related #[issue-number]`
- ❌ **Do NOT use**: `Fixes`, `Closes`, `Resolves`

### Co-Authorship
- ❌ **Do NOT include** Claude co-author trailers or "Generated with Claude Code" lines.

### Example Commit Message
```
feat(upstream): forward allowlisted MCP client headers on tools/call

Related #[issue-number]

Spec 112: per-server forward_headers allowlist, global
forward_client_headers switch, two-key ctx gating, deny list,
value scrubbing in errors and traces.
```

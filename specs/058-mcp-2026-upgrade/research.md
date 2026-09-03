# Phase 0 Research: MCP Protocol Upgrade to 2026-07-28

**Feature**: 058-mcp-2026-upgrade · **Date**: 2026-09-03 · **Spec**: [spec.md](./spec.md)
**Library under test**: `github.com/mark3labs/mcp-go` v1.0.0 (stable, released 2026-09-02); current pin v0.57.0

All claims below were verified against the module cache (`/Users/user/go/pkg/mod/github.com/mark3labs/mcp-go@v1.0.0`) and a scratch checkout of `main @ fa2a28fa9` with the pin bumped. Line numbers are from those trees.

---

## Summary of what changed since the spec was written

The spec's Dependency Gate paragraph is **stale in two places** and must be amended:

1. **"client-side `initialize` is capped at `2025-11-25`" is false for v1.0.0 stable.** `client.Initialize` is *modern-first*: it probes `server/discover` and only falls back to a legacy handshake when that fails (`client/client.go:305-333`). `mcp.LATEST_PROTOCOL_VERSION` is now `"2026-07-28"` (`mcp/version.go:35`).
2. **"zero compile errors" no longer holds for stable.** `server.NewTestStreamableHTTPServer` and `server.NewTestServer` moved to a new `server/servertest` package. This is the *only* break, it is test-only, and the replacement has an identical signature.

Everything else in the gate analysis survived: production code compiles unchanged on both editions, and the two known profile tests are still the only functional failures.

### Compile and test probe (v1.0.0 stable, scratch checkout)

| Check | Result |
|---|---|
| `go build ./...` | exit 0 |
| `go build -tags server -o /dev/null ./cmd/mcpproxy` | exit 0 |
| `go vet -tags server ./internal/serveredition/...` | exit 0 |
| `go vet ./...` (type-checks tests) | 1 undefined symbol: `server.NewTestStreamableHTTPServer` |
| `go vet ./...` after the `servertest` import swap | exit 0 |
| `./internal/server -run TestProfile_` | 2 failures (both known, see D1) |
| `./internal/server -skip 'E2E\|Binary\|MCPProtocol\|Docker\|OAuth'` (193s, full package) | **exactly those same 2 failures, nothing else** |
| `./internal/upstream/...`, `./bench`, `./internal/transport/...` | pass |

The full-package run closes the SC-008 gap for `internal/server`: the entire failure set introduced by v1.0.0 is `TestProfile_SetProfileSessionScoped` and `TestProfile_SetProfileUnknown`.

The `servertest` swap touches 32 call sites in 6 test files: `bench/mcpcall_test.go`, `internal/server/mcp_routing_test.go`, `internal/upstream/listtools_logging_test.go`, `internal/upstream/manager_prompts_test.go`, `internal/upstream/managed/prompts_test.go`, `internal/upstream/core/prompts_test.go`.

---

## D1 — Spec-057 reconciliation: **Option A**, with two mandatory grafts

**Decision**: treat the `/mcp/p/<slug>` URL path as request-carried identity. Do not introduce a `_meta`/header carrier in this spec.

**Rationale**: the URL form is *already* request-carried and always has been. `profileMiddleware` (`internal/server/server.go:2306-2367`) re-derives the slug from `r.URL.Path` on every HTTP request, looks it up in the live config snapshot, and injects a scope into the request context; nothing is bound at `initialize`. mcp-go v1.0.0 passes the middleware's request context into handlers in **both** protocol eras (`server/streamable_http.go:695-699`), so URL-scoped filtering survives the stateless era with zero routing changes. Two of three judges chose A; the dissenting judge preferred a `_meta` carrier on strict-conformance grounds, which buys nothing FR-012 does not already have and adds two new untrusted inputs.

**The only genuine connection state is `set_profile`** (Profiles-v2 tier 3), which keys `SessionStore.activeProfiles` by session id (`profile_tool.go:51-103`, `profile_resolver.go:150-160`). Under the modern era that id is `""`, so the tool already fails cleanly.

**Graft 1 (mandatory, from Option B).** Gate resolver tier 3 on `!server.IsModernRequest(ctx)` *in addition to* `sid != ""`. The empty-session-id guard alone is not sufficient: stdio sessions return the constant `"stdio"` in both eras (`server/stdio.go:127-129`), so a modern stdio request would otherwise persist a `set_profile` selection — exactly the per-connection state FR-011 forbids.

**Graft 2 (mandatory, from Option C).** Add `resolveActiveProfileForList` that skips tier 3, and use it in the two list filters: `filterDirectModeToolsForAuth` (`mcp_direct_scope.go:44`) and `filterAggregatedPromptsForAuth` (`:179`). This closes a **pre-existing literal FR-012 violation**: today a legacy `/mcp` session that ran `set_profile` gets a `prompts/list` that varies by connection. `tools/list` never varies (no `WithToolFilter` on the default or retrieve_tools-mode servers), so the violation is confined to prompts and direct-mode tools.

**Accepted trade-off**: `set_profile`'s description is byte-frozen in 7 golden files and will still tell modern clients the selection "persists for the lifetime of the current MCP session" while the handler refuses it. The honest rewrite plus golden regeneration is scheduled as its own wire-format change at the end of the deprecation window, not in the bump.

**Also fix while in here** (cheap, all verified false today): the "stable session id, no synthetic fallback" comment at `profile_resolver.go:14-18`; the "background inactivity cleanup calls RemoveSession" comment at `session_store.go:70-71` (`internal/runtime/lifecycle.go:321-349` never does); and `docs/features/profiles.md:68,134`.

---

## D2 — MRTR (FR-015/FR-016): **detect and frame in 058, defer the relay**

**Decision**: in this spec, detect an upstream `input_required` result, return a structured `isError` notice, and reject unsolicited continuations. Move the actual relay to a follow-up spec. **This requires amending FR-015/FR-016 in spec.md**, which is a decision for the maintainer to ratify, not something the plan can assume.

**Rationale**: `client.CallTool` is hard-wired to an internal multi-round-trip loop (`client/client.go:631-643`, `client/mrtr.go:56-103`). It both swallows `input_required` results and clobbers any caller-supplied `inputResponses`/`requestState`. The single-shot entry points (`callToolOnce`, `sendRequest`) are unexported. **A proxy cannot relay MRTR through the v1.0.0 client API at all** without either reimplementing the call path or an upstream library change. Two of three judges chose deferral; the dissenter's sealed-envelope design is the right shape for the follow-up and is recorded below.

**Follow-up design (for the later spec)**: a proxy-owned sealed envelope — `mpx1.<b64url(payload)>.<b64url(tag)>`, HMAC-SHA256, tool binding, args fingerprint — rather than passing the upstream `requestState` verbatim, because verbatim relay lets a client redirect a continuation to a different upstream. File the upstream mcp-go issue now: an exported single-shot `CallTool` accepting `mcp.MultiRoundTripParams`, and an exported typed input-required error.

**Sibling task of the bump, not deferred**: `forwardContentResult` (`internal/server/content_forward.go:130-131`) and its direct-surface twin (`mcp_routing.go:595-596`) copy `ctr.Result` wholesale. Under v1.0.0 that struct gains `resultType` and the embedded MRTR fields, so an upstream's `input_required` marker would be forwarded to the client as if mcpproxy had produced it. All three MRTR judges independently flagged this. Copy the fields explicitly instead.

---

## D3 — FR-027: the bump alone flips the **upstream-facing** default

`internal/upstream/core/connection_lifecycle.go:19` sets `initRequest.Params.ProtocolVersion = mcp.LATEST_PROTOCOL_VERSION`, which is now `2026-07-28`. Bumping the library therefore makes mcpproxy speak the new protocol **to every upstream that supports it**, before any of this spec's work lands. FR-028's pin protects only the client-facing side.

**Decision**: pin the upstream side to `mcp.LATEST_LEGACY_PROTOCOL_VERSION` in the bump PR and lift it deliberately in a later task, so the bump is a no-op on the wire in both directions. Two consequences of an un-pinned upstream hop, both verified, justify this:

- **`Ping` becomes a no-op on modern connections**, making the Spec-074 health probes vacuous.
- **Server-initiated requests are gone**: `RequestRoots` returns `ErrServerInitiatedRequestUnsupported`, so roots-based workspace discovery cannot work against a modern peer.

Five client constructor sites take no `ClientOption` today and would need one threaded through if the pin is done client-side: `internal/transport/http.go:249,305,364,429` and `internal/upstream/core/connection_stdio.go:244`.

---

## D4 — Requirements that are vacuous or unmeasurable as written

| FR/SC | Finding | Proposed amendment |
|---|---|---|
| FR-018, SC-005 | mcpproxy proxies **no resources**. The only `AddResource` call is the JSON-schema compiler (`mcp_input_validation.go:144`); grep for `-32002`/`RESOURCE_NOT_FOUND` in non-test code is empty. The one JSON-RPC-code seam (`classifyUpstreamInvalidParams`, `mcp.go:5760-5778`) matches a type whose constructors have no callers. | Re-scope to "no resource proxying exists; revisit if resources are ever proxied". |
| FR-007 (notifications) | `StreamableHTTP.SendNotification` passes a nil header (`client/transport/streamable_http.go:915-925`) and `mcp.JSONRPCNotification` has no header field. mcpproxy originates no client-side notifications. | Re-scope FR-007 to **requests**. |
| SC-006 | No baseline, no measurement mechanism, and no reader could propose one. | Downgrade to "cache hints present and valid", or define a bench cell. |
| FR-023 | No identifier, doc or test in mcp-go v1.0.0 mentions `serverName:toolName` as blessed; the 2026-07-28 spec text was not consulted. | Soften the wording or cite the spec text directly. |
| FR-013 (server edition) | `go test -tags server ./internal/serveredition/...` is green under v1.0.0, but **no modern-era test** exercises two OAuth users with differing entitlements and no session. | Add that test; it is the only untested half of FR-013. |

---

## D5 — Session audit: 22 call sites, 1 breaks, 21 degrade to attribution loss

Under a modern request, mcp-go mints an **ephemeral session with `SessionID() == ""`** (`server/streamable_http.go:640-647`), not a per-request random id. Guards must test for `""`, not `nil` — every `sess != nil` check still passes.

- **Breaks (1)**: `profile_resolver.go:20` via `handleSetProfile`. Covered by D1.
- **Degrades (21)**: all use the session id only for activity attribution or `SessionStore` lookups that are already `""`-safe. No panics, no 500s.
- **Never fires for modern clients**: `AddAfterInitialize` and the session-registration hooks (`mcp.go:437-480`), because `initialize` is `METHOD_NOT_FOUND` in the modern era. This silently blinds **client-name attribution and the Spec-044 activation funnel**. Replacement data is available request-side via `server.RequestProtocolInfoFromContext(ctx).ClientInfo`.
- **Two things the spec names do not exist**: there is no `describe_tool` session cache and no per-session output-sanitisation state. Both are attribution-only. Remove them from the audit list.
- **Silently disabled for modern clients**: workspace/roots discovery and work-session grouping (Spec 082) — `TryClaimWorkspaceFetch("")` returns false, so the fetch never spawns.

**Open product decision**: are per-session token stats and `storage.SessionRecord` a legacy-only feature after this upgrade, or re-modelled on the request-carried identity (`principal` + `_meta` clientInfo) that `runtime.WorkSessionIdentity` already resembles?

---

## D6 — Library facts the plan will reference

New symbols, none used in mcpproxy today:

- `mcp.ProtocolVersion20260728` … `20241105`, `mcp.LATEST_LEGACY_PROTOCOL_VERSION`, `mcp.IsModernProtocol`, `mcp.LegacyProtocolVersions()` — `mcp/version.go:12-113`
- `server.WithStreamableHTTPProtocolVersions` — `server/streamable_http_modern.go:33-41`. **Enforced, not merely advertised**, but only for the Streamable HTTP transport; stdio, SSE and in-process stay modern-capable.
- `server.IsModernRequest(ctx)` / `server.RequestProtocolInfoFromContext(ctx)` — `server/protocol.go:49-59`, populated at `request_handler.go:91-92` **before** every hook and method dispatch, so both are valid inside `AddBeforeAny` and tool handlers.
- `server/servertest` — the replacement test-server package.

The FR-028 pin must be applied at **all five** `NewStreamableHTTPServer` sites (`server.go:948,2624,2631,2638,2647`), best via one shared `clientFacingStreamableOptions()` helper so it cannot drift. Make it a `const`, not a mutable package var.

Header validation on the inbound hop is library-served (`server/response.go:187-219` → `mcp.ValidateParamHeaders`), but it resolves the tool only from the server's own tool set — so in retrieve_tools mode, where the built-ins carry no `x-mcp-header`, inbound `Mcp-Param-*` headers are never checked. FR-009/FR-010 placement is an open decision across three candidate layers (`upstream/core/client.go:302-380`, `runtime/lifecycle.go:671`, or the Spec-032 quarantine pipeline).

---

## D7 — Test-infrastructure constraints

- **CI skip-regex trap**: `unit-tests.yml:143,149` skips `MCPProtocol`, but `release-qa-gate.yml:229` and `e2e-tests.yml:108` skip bare `MCP`. **A test whose name contains "MCP" runs in unit CI but is silently skipped by the qa-gate race suite.** Avoid that substring in new test names.
- **Acceptance tests must run under both eras.** Under the FR-028 pin an unpinned v1.0.0 client negotiates *down* to 2025-11-25, so any assertion of the form `ProtocolVersion() == "2026-07-28"` is **vacuous while the pin is in place**. Use a `forEachEra` helper that pins the client explicitly (`client.WithProtocolVersion`) rather than relying on negotiation.
- `mcp-go`'s own `server/conformance_test.go` and `testdata/` offer legacy byte-compatibility fixtures worth reusing.
- The three frozen tool-surface goldens **survive the bump**: `diff` of `mcp.Tool`, `ToolAnnotation`, `ToolArgumentsSchema` and their `MarshalJSON` between v0.57.0 and v1.0.0 is empty. The spec's stated golden risk (`CallToolResult` gaining `resultType`) does not touch them; the real risk is the `content_forward.go` copy in D2.

---

## D8 — Spec-032 hash drift: real, but measured at zero exposure

**The coupling is real.** v1.0.0 adds `rewriteDraft07LocalRefs` (`mcp/tools.go:838-846,855-870`), which rewrites `#/definitions/X` refs to `#/$defs/X` when a schema has a `definitions` block and no `$defs`. mcpproxy hashes the **library-decoded** schema — `upstream/core/client.go:359` marshals `tool.InputSchema`, and `mcp.Tool.RawInputSchema` is tagged `json:"-"`, so the raw upstream bytes are discarded during decode and cannot be recovered cheaply. `normalizeJSON` sorts keys; it does not touch ref values. The existing formula-migration escape hatch (`tool_quarantine.go:815-841`) does **not** absorb the change, so an affected tool would be reported as `tool_description_changed` — a rug-pull signal — after a library bump that changed nothing upstream.

**The blast radius is zero on real data.** A probe unmarshalling the same fixtures under both library versions shows drift only for schemas that have *both* a `definitions` block and a `#/definitions/` ref:

| Fixture | Drift |
|---|---|
| draft-07: `definitions` + `#/definitions/Filter` refs | **hashes differ** |
| plain schema, no `$defs`/`$ref` (the common case) | identical |
| 2019-09 native `$defs` + `#/$defs/` refs | identical |
| `#/definitions/A` ref with no `definitions` block | identical |

Against a real `~/.mcpproxy/config.db` (opened read-only on a copy; 28 servers, 1096 tool-approval records): **0 records contain `#/definitions/`**. Of 45 records carrying a `$ref`, 393 ref targets already point at `#/$defs/` and the rest are `#/properties/…` self-refs, which the rewrite never touches. The one record matching "definitions" has the word in its description prose.

That absence is a genuine measurement of upstream behaviour, not a decoder artifact: v0.57.0 already hoisted `definitions` → `$defs` while leaving the pointers dangling, so a draft-07 upstream would have left the tell-tale shape in storage. None did.

**Decision**: do not build the one-time hash migration. Instead:
- **Normalize the ref form inside `NormalizeJSON`** (`internal/hash/hash.go:115`) so the hash is library-version-independent in both directions. Three lines at a shared choke point, and it protects against a future revert too. Widening that function's contract from "sort keys" to "canonicalize schema refs" needs a comment and a test.
- **Add a guard test** asserting the four fixtures above hash stably, so a future library bump that reintroduces drift fails red instead of silently re-quarantining users' tools.

The version-gated re-baseline remains available if field telemetry ever shows draft-07 tool schemas are more common than this single-machine sample suggests; the precedent to copy is the existing `OutputSchemaHashSchemaVersion` backfill at `tool_quarantine.go:461-500`.

---

## Open questions carried into plan.md

1. **Ratify the two spec amendments** (D2 MRTR re-scope, D4 vacuous requirements) before implementation starts. Cross-review them.
2. ~~Spec-032 hash drift~~ — **resolved by measurement, see D8.**
3. **`tools/list_changed` from a modern upstream** without an open `Listen` stream — unverified, and it gates the upstream refresh path (`upstream/manager.go:1192`).
4. **`server/discover` before `initialize` against non-mcp-go upstreams** (python/TS SDKs) — connect-time cost and OAuth 401 discovery behaviour unmeasured. Mitigated for now by D3's upstream pin.
5. **Modern-era activity rows carry `session_id ""`** — the Web UI grouping was not inspected. Needs a request-carried correlation key.
6. **`inflightKey` collision**: `server/server.go:2479-2506` reportedly keys in-flight requests as `":<jsonrpc-id>"` for every modern request, which could let one client cancel another's call. Cited but not independently verified — **treat as a security item for the bump PR until disproved**.

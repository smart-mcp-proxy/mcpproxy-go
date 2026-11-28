# Implementation Plan: Upstream Resources

**Branch**: `139-upstream-resources` | **Date**: 2026-11-28 | **Spec**: [spec.md](spec.md)
**Input**: Feature specification from `/specs/139-upstream-resources/spec.md`

## Summary

Aggregate upstream MCP resources and resource templates into mcpproxy's client-facing MCP servers, under server-namespaced addresses `mcpproxy://upstream/<encoded-server>/<original-uri>`, and proxy `resources/read` with the same reach gates, output policy, size bound and activity record tool responses get. The design copies the aggregated-prompts path (Spec 100 / F-series findings): a refresh builds one snapshot from the upstream manager, registers it on every routing-mode server with handlers that know their server by construction, an after-list hook filters the list per caller, and every handler re-checks reach at read time. Skill files are excluded so Spec 125's approval stays the only way to them. PR-2 adds `list_changed` forwarding and `/mcp/s/<server>`; PR-3 adds subscriptions.

## Technical Context

**Language/Version**: Go 1.26
**Primary Dependencies**: mark3labs/mcp-go v1.6.0 (resources, templates, hooks), zap, existing `internal/security/scanner`, `internal/transport` response limit. **No new dependencies.**
**Storage**: BBolt activity log only (new activity type `resource_read`); no new buckets.
**Testing**: `go test -race` unit tests in `internal/upstream/...`, `internal/server/...`, `internal/config/...`; an in-process mcp-go fixture upstream serving resources/templates (the pattern of `mcp_prompt_scope_test.go`); `./scripts/test-api-e2e.sh`.
**Target Platform**: personal and server editions (shared code; no `//go:build server` part).
**Project Type**: single Go backend + small Web UI filter constant.
**Performance Goals**: refresh off the request path; read adds one map lookup + one gate check before dispatch.
**Constraints**: feature off by default; byte-identical behaviour when off (SC-002); bounded memory per read (8 MiB + margin).
**Scale/Scope**: caps 500 resources / 100 templates per server, 2,000 / 500 total.

## Constitution Check

| Principle | Status | Notes |
|-----------|--------|-------|
| I. Performance at Scale | Pass | Listing is a snapshot rebuilt off the request path, bounded by page and item caps; reads are bounded. |
| II. Actor-Based Concurrency | Pass | Refresh is serialised by one mutex like `RefreshPrompts`; no new goroutines in PR-1 (PR-2's debounce uses the existing prompts debounce pattern). |
| III. Configuration-Driven | Pass | `aggregate_upstream_resources` (global, hot-reload) and `expose_resources` (per server), both in the config file, REST config and `upstream_servers` tool schema. |
| IV. Security by Default | Pass | Off by default; quarantine, reach, pin, profile gates at list and read; metadata TPA scan; output policy; size bound; activity row; skill exclusion. |
| V. TDD | Pass | Each task below starts with a failing test. |
| VI. Documentation Hygiene | Pass | `docs/features/upstream-resources.md`, `docs/configuration.md`, CLAUDE.md "Recent Changes" line (by hand, not by the agent-context script). |
| Upstream 3-layer design | Pass | core (`ListResources`/`ListResourceTemplates`/`ReadResource`), managed (pass-through + state), manager (aggregation + caps + gates). |
| Event-driven updates | Pass | Rebuild hooks onto the same `servers.changed` / config-reload events that already call `RefreshPrompts`. |

No violations; Complexity Tracking is empty.

## Project Structure

### Documentation (this feature)

```text
specs/139-upstream-resources/
├── spec.md
├── plan.md
├── research.md
├── data-model.md
├── quickstart.md
├── contracts/
│   ├── resource-address.md
│   └── activity-resource-read.md
├── checklists/requirements.md
└── tasks.md
```

### Source Code (PR-1; new files where possible, to stay clear of sibling work in `internal/server` and `internal/upstream`)

```text
internal/config/config.go                         # AggregateUpstreamResources, ExposeResources (+ merge.go patch, defaults)
internal/upstream/core/resources.go               # NEW: ListResources / ListResourceTemplates (paged, capped) / ReadResource (response-limited)
internal/upstream/core/client.go                  # exposeResources atomic + setter (beside exposePrompts)
internal/upstream/managed/resources.go            # NEW: pass-through + connection-state checks
internal/upstream/manager_resources.go            # NEW: aggregation snapshot, per-server/total caps, ReadResource gate (enabled, quarantined, reconnect-on-use)
internal/resourceaddr/resourceaddr.go             # NEW leaf: Encode/Decode/PrefixTemplate/IsSkillAddress (pure, fuzzed)
internal/server/mcp_resources.go                  # NEW: RefreshResources, registration on every routing-mode server, handlers, read path
internal/server/mcp_resources_scope.go            # NEW: after-list hooks (resources + templates), handler-side gate
internal/server/mcp_resources_output.go           # NEW: output policy over ReadResourceResult (text + textual-blob block check)
internal/server/mcp.go / mcp_routing.go           # capability option + hook registration + RefreshResources call sites (small edits)
internal/server/server.go                         # call RefreshResources next to the three RefreshPrompts call sites
internal/storage/activity_models.go               # ActivityTypeResourceRead
internal/runtime/event_bus.go (+ new file)        # EmitActivityResourceReadAttributed
internal/httpapi/activity.go, sse_scope.go        # accept the new type in filters/scoping
cmd/mcpproxy/activity_cmd*.go                     # --type help text
frontend/src/utils/activity.ts, types/api.ts      # type label + filter option
oas/swagger.yaml, oas/docs.go                     # activity type enum (make swagger)
docs/features/upstream-resources.md               # NEW
docs/configuration.md                             # two settings
```

**Structure Decision**: follow the prompts path file for file, but in new `*_resources*.go` files; the address codec is a pure leaf package so it can be fuzzed and reused by PR-2/PR-3 without importing `internal/server`.

## Design

### D1. Address codec (`internal/resourceaddr`)

`Prefix = "mcpproxy://upstream/"`. `Encode(server, uri) = Prefix + escape(server) + "/" + uri`, where `escape` percent-encodes every byte outside `A-Z a-z 0-9 - . _ ~` as `%XX` (uppercase). `Decode(addr)` requires the prefix, splits at the first `/` after it, unescapes the segment strictly (a `%` not followed by two hex digits fails; lowercase hex fails because re-encoding differs), checks `escape(decoded) == segment`, requires a non-empty remainder, and returns `(server, uri)`. `PrefixTemplate(server, pattern) = Prefix + escape(server) + "/" + pattern`. The prefix and the escaped segment contain no `{`, `}` so RFC 6570 expansion of the prefixed template equals prefix + expansion of the original. `IsSkillAddress(uri)` = `strings.HasPrefix(uri, "skill://")`. Fuzz: `Decode(Encode(s,u)) == (s,u)` for every `s` and non-empty `u`; `Decode` never panics.

### D2. Upstream layer

`core.Client.ListResources` and `ListResourceTemplates` mirror `ListPrompts`: nil, nil when the server lacks `Capabilities.Resources` or `expose_resources:false`; driven page by page through `ListResourcesByPage` / `ListResourceTemplatesByPage` with 50 pages, the per-server item cap and a 4 MiB response limit per page (streamable HTTP); serialised on SSE; `beginRequest` accounting. `core.Client.ReadResource(ctx, uri)` wraps `ctx` with `transport.WithResponseLimit(ctx, MaxResourceResponseBytes)` (8 MiB) so streamable HTTP stops at the cap; after the read, the result's encoded size is re-checked (stdio and legacy SSE share one stream and cannot be cut per request, the exposure tool results already have) and a typed `*ResourceTooLargeError` is returned over the cap. Manager refusals that depend on admin state (unknown, disabled, quarantined) wrap `upstream.ErrResourceServerUnavailable`, which the server answers as "resource not found". `managed.Client` passes through with its connection checks. `upstream.Manager.ListResources/ListResourceTemplates` build `[]ServerResource{Server, Resource}` from enabled, non-quarantined, connected servers with per-server and total caps (logged); `Manager.ReadResource(ctx, server, uri)` repeats `GetPrompt`'s enabled / quarantined / reconnect-on-use guards.

### D3. Aggregation and registration (`internal/server/mcp_resources.go`)

`RefreshResources()` reads the live config; when `aggregate_upstream_resources` is off it `SetResources()`/`SetResourceTemplates()` with nothing on every routing-mode server (only if anything was registered before, so an off-from-start instance never gains the capability implicitly). When on: list from the manager; drop skill addresses (D5); run `scanner.ScanToolMetadataVerdict` over name+title+description and drop "dangerous" (logged with rule ids); encode addresses; build `mcpserver.ServerResource` / `ServerResourceTemplate` entries whose handler closes over `(server, originalURI)` (resources) or `(server, pattern)` (templates); `SetResources` / `SetResourceTemplates` on `p.server`, `p.directServer`, `p.codeExecServer`, `p.callToolServer`. Ownership needs no snapshot: it decodes from the address. Withheld resource URIs and withheld upstream templates are kept per server (`withheld`, `withheldTemplates`), and a read of a URI either names or matches is refused even through another template (deny wins). Upstream URIs and templates whose literal text holds bytes not allowed in a URI path (`resourceaddr.ValidOriginal` / `ValidTemplate`: e.g. `[`, `]`, space, non-ASCII) are not published.

The capability: when the feature is on at construction, an empty `SetResources()`/`SetResourceTemplates()` on each routing-mode server registers the capability implicitly (the SDK does so on the first set) before any client connects; when it is switched on at runtime, the first refresh does the same. The capability option is not added to the shared routing-mode options, because the single-server surfaces copy those. When off from construction and never on, nothing is registered and the handshake is unchanged (SC-002).

### D4. Per-caller filter and handler gate (`mcp_resources_scope.go`)

`p.hooks.AddAfterListResources` and `AddAfterListResourceTemplates` filter `result.Resources` / `result.ResourceTemplates` in place: decode each address (a template's owner decodes from its prefix), keep it iff `resourceServerAllowed(ctx, list)` holds. That predicate is reach — `promptServerAllowed(authCtx, scope)` with `scope` from `resourceProfileScope`, which resolves like tool discovery (`resolveActiveProfileWithIndex` overlaid by `ResolveProfileV3`, whose dangling/guarded results are deny-all; the list form passes `listOnlyProfileResolution`) — and live eligibility (`resourceServerLive`: live config switch, server configured, enabled, not quarantined, `expose_resources` not false, skills gate). Entries without an owner are dropped for every caller (fail closed). mcpproxy sets no pagination limit on these servers, so the filtered page is the whole list (if one is ever set, the filter must move before pagination). Every handler starts with the same predicate in read form, then the exclusion and withheld checks; a failure returns `resourceNotFound(ctx, uri)`: a `server.NewHandlerError` whose JSON-RPC error has the code the SDK itself uses for an unknown URI (`-32602` for a modern-era request, `-32002` otherwise, via `mcp.IsModernProtocol(server.RequestProtocolVersion(ctx))`) and the SDK's own message `handler not found for resource URI '<uri>': resource not found`, so a refused read is indistinguishable from an address that does not exist (a plain error would become `-32603`). A test compares the two answers in both eras. The hooks are shared with the single-server instances, which register no resources in PR-1.

### D5. Skill exclusion and skills gate

`Runtime.SkillExclusion(server) (addrs, complete, err)`: `addrs` = every skill URI and file URI (current and approved copies) of the server's stored records, whatever the approval state; `complete` = skills feature on and server exposed, last skills refresh without error, truncation or failed entry, no stored skill at `skills.MaxFilesPerSkill` files. `resourceSkillGate(server)`: when `upstream.Manager.ServerSupportsSkills(server)` and not complete, the server is withheld from lists and reads; a lookup error withholds too. For every server, a URI is excluded when `resourceaddr.IsSkillAddress(uri)` (case-insensitive scheme), when it is in `addrs`, and a template is dropped when `resourceaddr.IsSkillTemplate(pattern)` (literal head starts with `skill://`, or is a case-insensitive prefix of it followed by an expression). The read handler applies the URI rules to the final original URI, so a template such as `file:///{+path}` cannot be filled onto a skill file.

### D6. Read path

Handler: gate (D4) → for templates, `Decode(request.URI)` and check the server matches the closure's server → exclusion and withheld check (D5) → `manager.ReadResource(ctx, server, originalURI)` → size error → output policy (D7) → rewrite each content item's URI (FR-009: the item whose URI equals the original becomes the requested address; other items of the same server are encoded; anything else is left) → activity row (D8). The correlation id is minted once so the policy-decision companion and the `resource_read` row correlate (as `getPromptAggregated` does).

### D7. Output policy (`mcp_resources_output.go`)

`applyResourceResultSanitisation(ctx, server, uri, requestID, result)` mirrors `applyPromptResultSanitisation`: untrusted trust class; block on a critical detection over the concatenated text items plus blobs that are text (`blobText`: base64 decodes to valid UTF-8 without NUL; the declared MIME type is ignored); redact / strip / spotlight on text items and on text blobs (encoded again); binary blobs unchanged. Returns `(result, blocked)`.

### D8. Activity

New `storage.ActivityTypeResourceRead = "resource_read"`, not counted as an attempt by `AttemptRoleOf` (same class as `prompt_get`; a test pins it). `Runtime.EmitActivityResourceReadAttributed(server, uri, proxiedURI, sessionID, requestID, status, errMsg, durationMs, bytes, count, mimeTypes, response, attr)` stores metadata fields `uri`, `proxied_uri`, `content_bytes`, `content_count`, `mime_types`; the response body goes through the same truncation and sensitive-data detection the tool-call path uses. Type added to REST filters, SSE scoping (`sse_scope.go`: scoped callers see rows of their servers, as for `prompt_get`), CLI help, Web UI label, OpenAPI enum.

### D9. Config

`Config.AggregateUpstreamResources bool` (`aggregate_upstream_resources`, default false, hot-reload) and `ServerConfig.ExposeResources *bool` (`expose_resources`, tri-state, merge patch like `ExposePrompts`, wired into `upstream_servers` add/update/patch and REST server payload — the "5 wiring points" for a new server field). Hot reload calls `RefreshResources`; `ExposeResources` changes are pushed to the live core client like `SetExposePrompts`.

## PR-2 / PR-3 (outline)

- **PR-2**: `dispatchNotification` handles `notifications/resources/list_changed` → `onResourcesChanged` callback → debounced per-server rebuild (same debounce as prompts); `WithResourceCapabilities(false, true)`; rebuild compares the new snapshot's fingerprint and calls `SetResources` only on change (the SDK then notifies). `/mcp/s/<server>`: register that server's resources with original URIs on the single-server instance (`refreshSingleServerSurfacesLocked`), capability merged with `nativeSkillsServerOptions`; the native interceptor stays in front, unchanged.
- **PR-3**: a session-keyed subscription registry (`internal/server/mcp_resources_subs.go`) implementing the SDK's `SessionWithResourceSubscriptionsErr` on mcpproxy's session wrapper, refcounted upstream `resources/subscribe`/`unsubscribe` through new core methods, `notifications/resources/updated` dispatch through `dispatchNotification` → registry → `SendNotificationToSpecificClient` after a reach re-check, re-subscribe on reconnect, release on session unregister; `WithResourceCapabilities(true, true)`; both eras via the SDK's `subscriptions/listen` handling.

## Complexity Tracking

None.

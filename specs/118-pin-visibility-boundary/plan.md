# Implementation Plan: Profile Pin as the Visibility Boundary for Agent Tokens

**Branch**: `118-pin-visibility-boundary` · **Spec**: [spec.md](./spec.md) · **Issue**: #1686 · **Status**: implemented (one PR)

## Summary

The REST surface's one enumeration predicate (`auth.CanEnumerateServer` → `canSeeServer` / `visibleServers`) reads `AuthContext.AllowedServers`. Rather than teaching the predicate about profiles, the pin is folded INTO `AllowedServers` at the moment an agent token's `AuthContext` is minted: the context a handler sees for a pinned token already carries the visible set (grant ∩ pin reach). Every consumer of the grant — the predicate, `scopeAllowedServers` and the storage filters and usage cache key behind it, preflight's `TokenServers`, `management.Doctor` — then agrees without a per-door change.

## The configuration seam (FR-020)

`auth.CanEnumerateServer` takes only a context; the reach of a pin lives in the live configuration. The ONE seam is `(*Server).bindCallerToPin` in `internal/httpapi/pin_boundary.go`:

- reads `s.controller.GetCurrentConfig()` (the same read the auth middleware already performs),
- resolves the reach with `profileReach(cfg, pin)` = `ProfileConfig.EffectiveServers(cfg)` (configured servers only),
- calls the pure `auth.AuthContext.BoundToPinReach(reach)` (`internal/auth/context.go`), which returns a COPY with `AllowedServers` = grant ∩ reach, as an empty non-nil slice when the reach is nil/empty/non-overlapping (FR-002), and returns administrators and unpinned callers untouched.

It is applied at the two places that mint an agent token's context, and nowhere else:

1. `handleAgentTokenAuth` (`internal/httpapi/server.go`) — every REST request.
2. `newSSECallerContextRefresher` (`internal/httpapi/sse_scope.go`) — before every identity-bearing `/events` frame, so a profile deleted or emptied mid-stream narrows the next frame (FR-003). Session principals carry no pin; the tray socket is an administrator.

`internal/management/diagnostics.go` (the one predicate caller outside `internal/httpapi/`) is served by the same context and needs no change.

## Profile doors (FR-009, FR-012)

`profileReachable` (`internal/httpapi/profiles.go`) answers `false` for a pinned caller on every name but its pin, then runs the existing reach ∩ visible loop, so "own pin with visible reach" is the only `true`. `parseViewAs`, `handleListProfiles`, `handleGetProfile`, `handleProfileEffectiveTools` and `redactProfileView` (`switchable_to`) all already route through it. `handleGetActiveProfile` applies the withholding to every scoped caller (the FR-004 exception); `renderActiveProfileChangedForCaller` applies the same rule to the SSE twin.

## SSE (FR-007)

`serversChangedDeliverable` (`internal/httpapi/sse_scope.go`) drops a `servers.changed` frame for a PINNED caller only when every identity extra names a hidden server and the narrowed embed is empty or absent. `renderEventPayloadForCaller` withholds, for every scoped caller, all extras beside a withheld identity (`added`, `approved_count`, …), keeping `servers`, `stats`, `reason`.

## Kept per-endpoint filters (FR-011, A2)

`limitServersToCallerPin` / `filterProfileToolRows` stay for their `tool_count` / tool-row narrowing; `callerProfileServers` now reads the same `profileReach`, and the regression test asserts bare `/servers` equals `/servers?profile=<pin>`.

## Deferred

FR-013 MCP↔REST parity test and FR-015 CLI test (both surfaces already read the same set; the MCP resolver applies the pin as its first tier and the CLI prints what REST returns; the CLI test harness cannot host the REST server across packages). `/catalog/search` is exercised through `visibleServers` rather than end-to-end (no registry fixture). The activity door, the usage cache key and live status frames are covered in `pin_boundary_test.go`.

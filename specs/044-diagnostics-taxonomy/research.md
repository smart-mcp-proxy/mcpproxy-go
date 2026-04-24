# Phase 0 Research — Diagnostics & Error Taxonomy

**Date**: 2026-04-24
**Status**: Complete — all `NEEDS CLARIFICATION` items resolved.

## R1. Fix endpoint rate-limit implementation

**Decision**: Use an in-memory token-bucket keyed by `(server_id, code)`. Implementation is a small middleware in `internal/httpapi/diagnostics_fix.go` backed by `sync.Map` of `*atomic.Int64` last-timestamp values. No new third-party dependency.

**Rationale**: The rate limit is cheap to enforce (1/s per tuple), expected load is single-digit requests per day per user, and we already avoid imports of full-featured rate-limiter libraries elsewhere in the codebase. Keeping it in-memory matches FR-020 (no new persistence).

**Alternatives considered**:
- `golang.org/x/time/rate` — already a transitive dep, but heavier abstraction than needed for per-tuple 1/s guard.
- BBolt-backed persistent counter — rejected: restart should clear the limiter (fix attempts are session-ephemeral).

## R2. `MCPX_DOCKER_SNAP_APPARMOR` fix UX

**Decision**: Expose both options as distinct `fix_steps` on the code:
1. `link` — docs page explaining Docker flavor switch (Desktop/Colima/rootless).
2. `button` — "Disable scanner for this server" that patches the server's config to set `skip_scanner: true`, dry-run by default (destructive per FR-008a).

**Rationale**: Per design doc §11 this question was deferred to the domain-3 PR; we now have enough context. The user may not be able to uninstall snap-docker, so offering both escape hatches is the minimum viable remediation. Neither is auto-invoked.

**Alternatives considered**:
- Auto-switch to non-snap docker — rejected: violates FR-021.
- Auto-disable scanner globally — rejected: too broad; spec 041 quarantine invariants require per-server opt-out.

## R3. OAuth concurrent re-auth

**Decision**: Reuse the existing `internal/oauth/coordinator.go` singleflight coordinator. The fixer for `MCPX_OAUTH_REFRESH_EXPIRED` calls `coordinator.InitiateLogin(ctx, serverName)` which already serializes per-server.

**Rationale**: Spec 023 already added OAuth state persistence + flow coordinator; re-using it means concurrent clicks collapse to one login flow. No new primitive needed.

**Alternatives considered**:
- Per-fix distributed lock — rejected: existing coordinator already handles this.
- Client-side button-disabled state — partial solution only; server-side serialization is authoritative.

## R4. Catalog layout

**Decision**: Codes live as Go `const` strings in `internal/diagnostics/codes.go`. Registry populated in a package `init()` function inside `internal/diagnostics/registry.go`. Both are hand-written (no code generation).

**Rationale**: Consistent with stdlib idioms (`syscall.EINTR`, etc.). Avoids a generator dep in the core build path. Catalog is small (tens of entries, not thousands) so hand-maintenance is cheap.

**Alternatives considered**:
- `go:embed` a YAML/JSON catalog — rejected: makes IDE navigation harder, defeats compile-time name checking.
- Code-generated constants from JSON — rejected: extra build step with no real benefit at this size.

## R5. Classifier strategy

**Decision**: Prefer typed-error inspection via `errors.As` over string matching. Add dedicated sentinel error types in `internal/diagnostics/classifier.go` where third-party errors lack structure. String matching allowed ONLY as a fallback for library errors that cannot be introspected any other way (e.g. `os/exec` `*exec.Error` wraps `ENOENT` correctly, so we use that; Docker client errors sometimes require string inspection of `err.Error()`).

**Rationale**: String matching is brittle. `errors.Is` / `errors.As` are cheap and stable across Go releases.

**Alternatives considered**:
- Pure string matching — rejected: breaks silently on upstream library rewording.
- Regex dispatcher — rejected: harder to test, slower.

## R6. Telemetry v3 dependency on spec 042

**Decision**: Phase H (telemetry v3 `diagnostics` sub-object) is conditionally deferred. Decision gate: check if spec 042's v3 client is merged at the time Phase H would begin. If not, Phase H is split into a follow-up PR that lands after spec 042.

**Rationale**: Design doc §6 and spec FR-018 explicitly allow this; spec 042 is on the critical path. Non-blocking.

**Alternatives considered**:
- Bolt telemetry work onto this PR regardless — rejected: creates a cross-spec merge dependency that would delay core catalog delivery.
- Embed a local-only v2-style counter — rejected: spec 042's telemetry counter abstraction should be the single source of truth.

## R7. Docs generation + link-check

**Decision**:
- `docs/errors/README.md` is auto-generated via `go generate ./internal/diagnostics/...` from the in-memory catalog.
- Per-code pages (`docs/errors/<CODE>.md`) are hand-written with a stub template populated at code-registration time.
- `scripts/check-errors-docs-links.sh` verifies (a) every registered code has a file, (b) every file's code is in the registry, and (c) `README.md` is current.
- CI runs the script as a make target invoked from `./scripts/run-all-tests.sh`.

**Rationale**: Auto-generated index avoids drift; hand-written bodies allow nuanced fix instructions. Bidirectional link-check prevents orphaned pages.

**Alternatives considered**:
- Fully auto-generated pages — rejected: bodies need human authoring for good fix steps.
- Link-check only in CI (no local script) — rejected: developers need the same check locally before pushing.

## R8. Performance budget for the `GET /servers/{name}/diagnostics` endpoint

**Decision**: Target p95 < 50 ms (SC-003). Read path is: chi router → auth middleware → fetch server state from stateview snapshot (already in memory, lock-free read via atomic pointer) → serialize JSON. No DB I/O.

**Rationale**: All data is on the already-populated stateview snapshot. The only measurable work is JSON marshaling; with typical payload sizes (<2 KB per server), p95 < 5 ms is realistic. 50 ms budget is conservative.

**Alternatives considered**:
- Caching — unnecessary; snapshot is already the cache.

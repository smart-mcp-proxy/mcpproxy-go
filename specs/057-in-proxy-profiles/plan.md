# Implementation Plan: In-Proxy Profiles + Permanent URLs

**Branch**: `057-in-proxy-profiles` | **Date**: 2026-06-07 | **Spec**: [spec.md](./spec.md)
**Input**: Feature specification from `/specs/057-in-proxy-profiles/spec.md`

## Summary

Add an optional top-level `profiles` array to the config. Each profile `{name, servers}` is exposed at a stateless, pinned URL `/mcp/p/<name>` whose protocol surface is identical to `/mcp` except the caller's effective server set is restricted to the profile's `servers`. Filtering composes by **intersection** with agent-token scope (Spec 028) and per-user visibility (Spec 029), and reuses the existing per-server `enabled_tools`/`disabled_tools` for tool-level granularity (Spec 049). No storage, index, or token-model changes.

**Technical approach** (verified against current code, 2026-06-07):
- A single `/mcp/p/` prefix handler + `profileMiddleware` (registered after `mcpAuthMiddleware`) strips the slug, resolves the profile from the **current** config snapshot (`runtime.Config()`, lock-free atomic read → hot-reload for free), and injects a `ProfileScope` into the request context. The existing retrieve_tools-mode MCP server instance (`p.server`) is reused — **no per-profile server instances** (http.ServeMux can't deregister routes at runtime).
- Profile filtering runs as a **parallel, auth-type-independent** check at the two existing scope sites (`mcp.go:1113` retrieve_tools, `mcp.go:1529` call_tool_*). It MUST NOT ride the `enforceAgentScope` gate, because an unauthenticated `/mcp/p/...` connection gets `AdminContext()` (where `enforceAgentScope == false`). Two independent checks yield the FR-005 intersection and FR-012 distinct errors for free.
- `metadata["profile"]` is attached to tool-call activity records originating from a profile URL via the existing `Metadata map[string]interface{}` field (no schema change).

## Technical Context

**Language/Version**: Go 1.24 (toolchain go1.24.10)
**Primary Dependencies**: `net/http` (ServeMux), `github.com/mark3labs/mcp-go` (MCP protocol, `NewStreamableHTTPServer`), existing `internal/auth`, `internal/config`, `internal/runtime/configsvc` — **no new external dependencies**
**Storage**: None new. Config lives in `mcp_config.json`; profiles are config-only. Activity metadata reuses existing BBolt `ActivityRecord.Metadata`.
**Testing**: `go test` (unit), `internal/server` integration tests, `internal/server/e2e_test.go`-style E2E, `./scripts/test-api-e2e.sh`
**Target Platform**: Linux/macOS/Windows server (personal + server editions, no build-tag divergence — FR-013)
**Project Type**: Single Go project (`internal/...`)
**Performance Goals**: Profile filtering is O(servers-in-profile) set lookup over an already-paginated result; below existing `retrieve_tools` E2E latency budget (SC-006). BM25 <100ms invariant (Constitution I) unaffected — index is **not** partitioned.
**Constraints**: Zero migration; `profiles` absent ⇒ byte-identical config round-trip (SC-004) and unchanged `/mcp`/`/mcp/code`/`/mcp/call` behaviour (SC-002).
**Scale/Scope**: Server cardinality typically ≤ a few dozen; handful of profiles. 4 files touched + 2 new small files.

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-check after Phase 1 design.*

| Principle | Assessment | Verdict |
|-----------|-----------|---------|
| **I. Performance at Scale** | Per-request set-intersection over paginated results; no index reshape; BM25 path untouched. | ✅ PASS |
| **II. Actor-Based Concurrency** | No new shared mutable state. Profile resolution reads the lock-free `configsvc` atomic snapshot. `ProfileScope` is immutable per-request, injected via context (mirrors `AuthContext`). In-flight sessions keep their snapshot until reconnect. | ✅ PASS |
| **III. Configuration-Driven Architecture** | Pure config feature: top-level `profiles` array, hot-reloaded via existing `configsvc`. No tray state. | ✅ PASS |
| **IV. Security by Default** | Profiles only **narrow** access; never broaden. Quarantined/disabled servers excluded from a profile's effective set. Composes with agent tokens and per-user visibility by intersection. Unauth-at-profile-URL regression test mandated. | ✅ PASS |
| **V. Test-Driven Development** | Spec mandates unit (config + filter), integration, E2E, and backward-compat tests, written red-first. Named regression test gate before merge. | ✅ PASS |
| **VI. Documentation Hygiene** | Plan updates CLAUDE.md (MCP endpoints table + Built-in tools note), README (profiles section), `docs/`, and `oas/swagger.yaml` if any REST surface added (none in MVP). | ✅ PASS |

**No violations. Complexity Tracking not required.**

The one design choice worth recording (resolved in spec): profile filtering is a **parallel check**, not an `AuthContext.AllowedServers` overwrite. Rationale: an unauthenticated profile connection is `AdminContext()` with `enforceAgentScope=false`; stuffing servers into `AllowedServers` would be bypassed. This is the simpler-of-the-correct options (no `AuthContext`/token-bucket change), so it is not a constitution violation — it is the design that keeps token validation untouched.

## Project Structure

### Documentation (this feature)

```text
specs/057-in-proxy-profiles/
├── plan.md              # This file
├── research.md          # Phase 0 output — design decisions (mostly pre-resolved in spec)
├── data-model.md        # Phase 1 output — ProfileConfig, ProfileScope, Effective Server Set
├── quickstart.md        # Phase 1 output — config + curl walkthrough
├── contracts/           # Phase 1 output — config JSON schema + /mcp/p/ route contract
│   ├── profiles-config.schema.json
│   └── mcp-profile-endpoint.md
└── tasks.md             # Phase 2 output (/speckit.tasks — separate command)
```

### Source Code (repository root) — verified file:line seams

```text
internal/
├── config/
│   ├── config.go              # Config struct (L101); add `Profiles []ProfileConfig` after Servers (L109)
│   ├── loader.go              # Validate() (L1521) — call profile validation; SaveConfig() round-trip (L352)
│   └── profiles.go            # NEW — ProfileConfig struct, slug regex, reserved-set, dup + unknown-server validation
├── profile/
│   └── context.go             # NEW (~30 LOC) — ProfileScope{Allows(server) bool}, WithProfileScope/FromContext (mirrors auth/context.go)
└── server/
    ├── server.go              # Register `/mcp/p/` prefix handler + profileMiddleware after mcpAuthMiddleware (near L1690)
    └── mcp.go                 # Two parallel filter conditions: retrieve_tools (~L1113) + call_tool_* (~L1529); profile metadata write at emitActivity* call sites
```

**Structure Decision**: Single Go project, existing `internal/` layout. One new sub-package `internal/profile` (request-scoped scope type, peer of `internal/auth`) and one new file `internal/config/profiles.go`. No new top-level dirs, no frontend changes in the MVP (web-UI profile affordances are out of scope), no storage/index packages touched.

## Phase 0 — Research

The spec's *Resolved Design Decisions*, *Assumptions*, and *Implementation Design* sections already retire every open question. `research.md` consolidates them in decision/rationale/alternatives form. No outstanding NEEDS CLARIFICATION. Code-seam verification (2026-06-07) confirmed all 7 referenced seams MATCH current code (`GetMCPServerForMode`, `mcpAuthMiddleware`/`AuthContext`, the two `mcp.go` gates, `Config.Servers`, `ActivityRecord.Metadata`, `configsvc` lock-free snapshot).

## Phase 1 — Design & Contracts

- **data-model.md**: `ProfileConfig` (config entity), `ProfileScope` (request entity), and the *Effective Server Set* derivation (profile ∩ token ∩ not-disabled ∩ not-quarantined ∩ per-user-visible).
- **contracts/profiles-config.schema.json**: JSON Schema for the `profiles` array (name slug pattern, reserved set, servers list).
- **contracts/mcp-profile-endpoint.md**: behavioural contract for `/mcp/p/<slug>` (200 surface identical to `/mcp`; 404 bodies for no-profiles / unknown-slug; intersection + error-attribution rules).
- **quickstart.md**: the two-profile config + curl walkthrough from the spec, runnable end-to-end.
- **Agent context**: run `.specify/scripts/bash/update-agent-context.sh claude`.

## Phase 2 — Tasks (separate command)

`/speckit.tasks` will generate `tasks.md` from this plan, ordered TDD-first per the spec's Testing Strategy:
1. Config: `ProfileConfig` + validation (slug/reserved/dup/unknown-server) — unit tests first.
2. `internal/profile` context + `ProfileScope.Allows`.
3. Routing: `/mcp/p/` handler + `profileMiddleware` (auth→profile order).
4. Filter wiring: parallel checks at both `mcp.go` sites + the mandated unauth-at-profile-URL regression test.
5. Activity `metadata["profile"]`.
6. Integration + E2E (two-profile isolation, intersection, 404 paths, reserved slug) + backward-compat E2E (SC-002).
7. Docs (CLAUDE.md, README, docs/).

These tasks are sized for hand-off to Paperclip engineers after the plan is reviewed.

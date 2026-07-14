# Implementation Plan: Compact Router — Progressive-Disclosure Tool Discovery

**Branch**: `085-compact-router` | **Date**: 2026-07-14 | **Spec**: [spec.md](spec.md)
**Input**: Feature specification from `/specs/085-compact-router/spec.md`
**Rationale record**: [design.md](design.md) (judge-panel synthesis; spec.md wins on any conflict)

## Summary

Change only the *serialization* of `retrieve_tools`: emit compact one-line signatures
(required params never elided, optionals typed, short enums/defaults inline, nested/complex
params collapsed under a lossy `~` marker) plus a first-sentence description and a `lossy`
flag, instead of full JSON schemas. Add `describe_tool` (batch ≤5 ids → full definitions,
per-id errors) as the second stage, and make `call_tool_*` argument failures self-healing by
embedding the failing tool's full schema. A `tool_response_mode` config (`full` default |
`compact`, hot-reloadable) plus a per-call `detail` override control the shape; Phase 1 ships
`full` so responses are byte-identical to today. The BM25 query→rank→top-k path is untouched
by construction; the spec-083 profiler hard-gates ranked-ID identity between modes.

Technical approach, grounded in the code:
- **Signature compiler** lives in a new leaf package `internal/toolsig/` (pure, no server deps)
  so both the production response builder *and* the spec-083 bench arm import one grammar. It
  renders from `ToolMetadata.ParamsJSON` and is memoized in an in-memory cache keyed by
  `ToolMetadata.Hash` (the Spec-032 SHA-256), warmed during indexing so rendering is not
  per-request work.
- **Entry-builder seam**: the monolithic `retrieve_tools` assembly in
  `internal/server/mcp.go` (`handleRetrieveToolsWithMode`, per-entry build at ~1428–1492,
  cross-cutting appends at ~1494–1613) is refactored to route each result through a
  mode-aware `buildToolEntry` before compact mode lands. Full mode must reproduce today's
  map byte-for-byte.
- **Pre-dispatch validation** reuses `github.com/santhosh-tekuri/jsonschema/v6` — already a
  **direct** dependency (`internal/outputvalidation/`) — to validate `call_tool_*` args
  against the stored `ParamsJSON` before `upstreamManager.CallTool` (mcp.go ~1955), fail-open
  on compile failure (FR-013b). No new dependency.
- **describe_tool** reuses a **shared visibility resolver** (`p.toolVisibleToSession`, extracted
  from the retrieve handler's current `serverDiscoverable` closure at mcp.go:1324 plus the inline
  callable/quarantine passes) plus `indexManager.GetToolsByServer` to resolve ids to
  **definitions**. A definition = the full-mode entry with ranked-only keys stripped (`score`);
  equality with retrieve_tools is over `{name, description, inputSchema, server, annotations,
  call_with}`, not whole-object bytes (full entries carry `result.Score`, mcp.go:1455).
- **Config** adds one field beside `RoutingMode`; validation beside the `routing_mode` block
  (config.go ~1650); env alias `MCPPROXY_TOOL_RESPONSE_MODE`. Hot-reload needs **two wiring
  fixes** (not free): add a `tool_response_mode` clause to `DetectConfigChanges`
  (`internal/runtime/config_hotreload.go`, else an apply of only this field is "no changes
  detected"), and read the effective mode from `p.currentConfig()` (the live snapshot,
  `profile_resolver.go:38`) — **not** the construction-time `p.config` the retrieve path reads
  today (mcp.go:1236).
- **Signature cache ownership**: `Runtime` owns one `*toolsig.Cache`, passed into
  `NewMCPProxyServer`; the indexing path warms it and the retrieve/describe paths read it (the
  *same* instance) — proven by a compile-count test (post-index retrieve = cache hit, FR-008).

## Technical Context

**Language/Version**: Go 1.24 (backend/core). No frontend or Swift changes.
**Primary Dependencies**: existing only — `github.com/mark3labs/mcp-go` (tool registration),
`github.com/blevesearch/bleve/v2` (index, untouched), `go.uber.org/zap`,
`github.com/santhosh-tekuri/jsonschema/v6` (**already a direct dep** — reused for
pre-dispatch validation), stdlib (`encoding/json`, `strings`, `unicode`, `sort`) for the
signature compiler and first-sentence extraction. **No new third-party dependency** (CLAUDE.md).
**Storage**: BBolt (`config.db`) — tool hashes/approvals, unchanged shape; Bleve index —
`ToolMetadata{ParamsJSON, Hash, Description, …}` read unchanged, **no new index fields in v1**
(FR-008). Signature cache is in-memory only.
**Testing**: `go test -race ./internal/...`; table tests for `internal/toolsig`; byte-identity
golden test for full-mode `retrieve_tools`; E2E in `internal/server/e2e_test.go` +
`./scripts/test-api-e2e.sh`; bench/profiler tests under `bench/` (spec-083 arms).
**Target Platform**: Linux/macOS/Windows core binary (personal + server editions; this feature
is edition-agnostic — pure `internal/`, no `//go:build server`).
**Project Type**: Single Go project (core server). Web-app/mobile trees N/A.
**Performance Goals**: Constitution I — BM25 search <100ms across 1,000 tools, unaffected
(serialization-only). Signature rendering O(params) per tool, memoized by hash; SC-007 mode
toggle within one hot-reload cycle; FR-008 zero added per-request latency after warm cache.
**Constraints**: FR-006/SC-003 byte-identical full-mode payloads; SC-002 100% ranked-ID
identity across modes; SC-004 required params in 100% of signatures; SC-005 lossy rate <20%
on the frozen corpus; FR-013b validation must never block a call a schemaless proxy allowed.
**Scale/Scope**: 907-tool live deployment (context); 45-tool frozen corpus + 47-query golden
set for gates. ~6 packages touched: `internal/toolsig` (new), `internal/server`,
`internal/config`, `internal/runtime`, `bench/` (+ `bench/arms` on the 083 branch),
`internal/outputvalidation` pattern reused (not modified).

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-check after Phase 1 design.*

Constitution v1.1.0 (`.specify/memory/constitution.md`):

| Principle | Assessment |
|-----------|------------|
| **I. Performance at Scale** | PASS. Serialization-only; ranking/index untouched. Signatures compiled once per tool-hash into an in-memory cache warmed at index time (FR-008) — no per-request cost, no <100ms search regression. Pre-dispatch validation is a local schema check, cheaper than the upstream round-trip it prevents. |
| **II. Actor-Based Concurrency** | PASS (with a guardrail). The signature cache is the one new shared-memory structure. It is read on the request path and written on the indexing path; guard with a single `sync.RWMutex` (or `sync.Map`) — justified in Complexity Tracking below because a channel-owned actor for a pure memoized-pure-function cache would be heavier than the lock it replaces (the constitution permits locks "proven necessary"; here the alternative is strictly more code for a stateless derivation). |
| **III. Configuration-Driven Architecture** | PASS. `tool_response_mode` is a JSON config field with env override and hot-reload (FR-001/FR-015). Default sensible (`full` = today's behavior). No tray state. |
| **IV. Security by Default** | PASS. `describe_tool` applies the *same* visibility pipeline as search (profile scope, agent-token auth, callability, quarantine/disabled) — FR-011 forbids leaking a definition search would not return. Compact mode does not weaken quarantine; self-healing errors expose only the *target* tool's own schema (already visible to the caller via describe_tool). |
| **V. Test-Driven Development** | PASS. Every task is TDD-paired (failing test first). Byte-identity, ranked-ID-identity, never-elide-required, and lossy-rate are automated checks (SC-002/003/004/005). |
| **VI. Documentation Hygiene** | PASS. Tasks include `docs/`, `CLAUDE.md` MCP-tools line, and `oas/`/`retrieve_tools` description updates (FR-014). |

**Result**: PASS. One justified lock (Complexity Tracking). No new dependency, no new
abstraction beyond the `internal/toolsig` leaf package (which reduces duplication by unifying
production and bench grammars).

## Project Structure

### Documentation (this feature)

```text
specs/085-compact-router/
├── spec.md              # Feature spec (Codex-approved, normative)
├── design.md            # Rationale record (spec wins on conflict)
├── plan.md              # This file
├── research.md          # Phase 0: grammar divergence, validator choice, cache-keying, hot-reload
├── data-model.md        # Phase 1: Compact Signature, Response Mode, describe_tool, Self-healing Error, Flip Gates
├── quickstart.md        # Phase 1: build/run/toggle/measure walkthrough
├── contracts/
│   ├── signature-grammar.md      # Normative grammar + 6 worked edge-case examples
│   ├── describe_tool.md          # Request/response + per-id error contract
│   └── invalid-params-error.md   # Self-healing error contract
└── tasks.md             # Phase 2: TDD-paired, phased by user story
```

### Source Code (repository root) — REAL paths

```text
internal/
├── toolsig/                         # NEW leaf package (no server import → bench-safe)
│   ├── signature.go                 #   Render(paramsJSON) (Signature, error); FirstSentence(desc)
│   ├── signature_test.go            #   table tests over captured schemas + 6 edge cases
│   ├── cache.go                     #   hash-keyed memoized cache (RWMutex)
│   └── cache_test.go
├── server/
│   ├── mcp.go                       # handleRetrieveToolsWithMode (~1203); per-entry build (~1428);
│   │                                #   registerTools (~689) + buildManagementTools (~791);
│   │                                #   handleCallToolVariant (~1649) pre-dispatch validation site (~1747);
│   │                                #   createDetailedErrorResponse (~4767)
│   ├── mcp_entry_builder.go         # NEW: buildToolEntry (full|compact seam), extracted from mcp.go
│   ├── mcp_entry_builder_test.go    # NEW: byte-identity full-mode golden; compact-shape test
│   ├── mcp_describe_tool.go         # NEW: describe_tool registration + handler
│   ├── mcp_describe_tool_test.go
│   ├── mcp_input_validation.go      # NEW: pre-dispatch validator (santhosh v6, fail-open)
│   ├── mcp_input_validation_test.go
│   ├── mcp_visibility.go            # NEW: p.toolVisibleToSession — shared resolver extracted
│   │                                #   from serverDiscoverable closure (mcp.go:1324) + callable/quarantine
│   ├── mcp_visibility_test.go       # NEW: retrieve↔describe parity test
│   ├── profile_resolver.go          # currentConfig() (~38) — read live mode here, not p.config
│   ├── mcp_routing.go               # buildCallToolModeTools (~354) / initRoutingModeServers (~534):
│   │                                #   register describe_tool in retrieve_tools mode
│   └── e2e_test.go                  # self-healing + mode-toggle (reload + API-apply) E2E
├── config/
│   └── config.go                    # ToolResponseMode field (~290 beside RoutingMode);
│                                    #   validation (~1650 beside routing_mode block)
├── config/loader.go                 # MCPPROXY_TOOL_RESPONSE_MODE env alias (~570)
├── runtime/
│   ├── config_hotreload.go          # DetectConfigChanges: add tool_response_mode clause (else apply is a no-op)
│   ├── runtime.go / server wiring   # Runtime owns *toolsig.Cache → NewMCPProxyServer
│   └── lifecycle.go                 # applyDifferentialToolUpdate (~542): warm the shared signature cache on index
├── index/
│   └── bleve.go                     # GetToolsByServer (~398) reused by describe_tool (no change)
└── outputvalidation/                # REUSED pattern for santhosh v6 (not modified)

bench/                               # spec-083 profiler (partial in this tree; full on 083 branch)
├── live.go / live_report.go         # live arm — measures real compact responses (FR-017)
└── (on 083 branch) arms/compact.go  # offline arm — migrate to import internal/toolsig (FR-019 sharing)

cmd/mcpproxy/                        # --tool-response-mode flag wiring (server edition)
```

**Structure Decision**: Single Go project. The one structural addition is the
`internal/toolsig/` leaf package. It is deliberately **not** `internal/server/toolsig/` (as
design.md loosely suggested): the spec-083 bench (`bench/arms/`) already imports `internal/…`
and must share this grammar (FR-019), but importing `internal/server` would pull the entire
HTTP/MCP server surface and risk an import cycle. A leaf package with no `internal/server`
dependency is importable by both `internal/server` and `bench/arms`. Everything else edits
existing files in place.

## Complexity Tracking

| Violation | Why Needed | Simpler Alternative Rejected Because |
|-----------|------------|-------------------------------------|
| `sync.RWMutex`/`sync.Map` on the signature cache (Principle II prefers channel-owned actors) | The cache is read on every `retrieve_tools`/`describe_tool` request and written on the async indexing path; FR-008 requires "compiled at index time, not per request." | A channel-owned goroutine actor for a *pure memoized derivation* of an immutable input (hash→signature) adds a goroutine, a request/response channel, and lifecycle management to protect a map whose values never change for a given key. The constitution permits locks "proven necessary through benchmarking"; here the derivation is stateless and idempotent, so a read-mostly lock (or `sync.Map`) is the idiomatic, lower-complexity choice. |
| New leaf package `internal/toolsig` | FR-019 (deterministic, shared) + the profiler's compact arm must render identical bytes to production. | Duplicating the grammar in `internal/server` and `bench/arms` guarantees drift (the exact MCP-3161 catalog-drift lesson design.md cites). A shared package is the *simpler* long-run choice. |

# Implementation Plan: Preserve the New Tool Listing Fields

**Branch**: `137-tool-listing-fields` | **Date**: 2026-11-26 | **Spec**: [spec.md](spec.md)
**Input**: Feature specification from `specs/137-tool-listing-fields/spec.md`, issue #2042.

## Summary

Capture `title`, `icons`, `_meta`, `defer_loading` and `execution.taskSupport` at discovery, validate and cap them, store them on `config.ToolMetadata`, and re-emit them on the direct surface, `describe_tool`, `retrieve_tools`, REST and the Web UI. Titles (top-level and `annotations.title`) get a review rule of their own: an unapproved title is **withheld** from agent surfaces, never holding the tool. Titles are scanned on their own, never with the contract, and only review-bound approvals can approve them. The rug-pull hash is untouched, so the upgrade needs no hash migration; the review fingerprint moves to `rv3` to cover the titles.

## Technical Context

**Language/Version**: Go 1.26 (core), TypeScript 5.9 / Vue 3.5 (Web UI)
**Primary Dependencies**: `github.com/mark3labs/mcp-go` v1.0.0 (already parses every field), Bleve, BBolt. **No new dependencies.**
**Storage**: BBolt `tool_approvals` records (additive JSON fields), Bleve index (schema version bump `"2"` → `"3"`)
**Testing**: `go test -race`, `./scripts/test-api-e2e.sh`, vitest, Playwright Web UI sweep, `internal/qapilot` model + scenarios
**Target Platform**: macOS, Linux, Windows; both editions
**Project Type**: Go backend + Vue frontend
**Performance Goals**: no measurable change to discovery or `tools/list` latency on the 3,000-tool rig (`reference_many_tool_fleet_perf_rig`); the title scan adds at most two short fields per tool to an existing scan
**Constraints**: per-tool and per-server caps (spec FR-002-FR-004a); never fetch an icon; pending title text only on administrator-authenticated surfaces
**Scale/Scope**: ~25 files across `upstream/core`, `config`, `runtime`, `server`, `index`, `security/detect`, `security/scanner`, `contracts`, `httpapi`, `frontend`, `docs`

## Constitution Check

| Principle | Status | Note |
|---|---|---|
| I. Performance at Scale | PASS | Fields are captured from a listing already in memory. Validation is linear and capped. One-time index rebuild from the schema bump. |
| II. Actor-Based Concurrency | PASS | Title decisions are made inside the existing per-server approval lock (`toolApprovalServerLock`) and the capture-ordered admission (`admitInventoryHeld`, Spec 117 PR-3). No new locks or goroutines. |
| III. Configuration-Driven | PASS | No new config field. Behaviour follows existing `quarantine_enabled` and per-server `trust_mode`. |
| IV. Security by Default | PASS | Unreviewed titles are withheld by default; icons are never fetched; `_meta` cannot override mcpproxy-owned keys; `execution` is not forwarded. |
| V. TDD | PASS | Each PR starts with failing tests (see Test plan). |
| VI. Documentation Hygiene | PASS | `docs/features/security-quarantine.md`, new `docs/features/tool-listing-fields.md`, `oas/swagger.yaml`, CLAUDE.md "Recent Changes" at merge of each PR. |
| Core + Tray split | PASS | The tray is untouched. |
| 3-layer upstream | PASS | Capture sits in `core` (`client.go` tool conversion). `managed` and `cli` pass `ToolMetadata` through unchanged. |

Post-design re-check: PASS.

## Design

### D1. Capture (`internal/upstream/core/client.go`)

A new pure function `listingExtrasFromWire(tool *mcp.Tool) config.ToolListingExtras` sits next to `toolAnnotationsFromWire` (`client.go:564`) and is called in the conversion loop (`:520-550`). It applies the spec's validation (FR-002-FR-005) in a leaf package so the rules are unit-testable without a client:

- New leaf package `internal/toollisting` (stdlib only): `NormalizeTitle(raw, toolName) (title string, invalid string)`, `ValidateIcons([]Icon) (kept []Icon, dropped []DropReason)`, `CapMeta(raw json.RawMessage) (json.RawMessage, string)`, `NormalizeTaskSupport(string) string`, `OwnedMetaKeys` (FR-024), `ApplyServerBudget(tools)` (FR-004a). Icon data-URI checks decode the base64 header and length only; the image bytes are never parsed.
- `annotations.title` is also run through `NormalizeTitle`; an invalid one is kept on `ToolAnnotations.Title` (it is the live value) and flagged in `TitleInvalid`.
- `ToolMetadata.Hash` (the discovery hash, `internal/hash/hash.go:245`) gains a trailing length-prefixed `extras` field: a digest of the canonical JSON of the extras. When all extras are absent the field is omitted, so a tool without any new field keeps its exact current hash and its signature-cache entry. This makes the index, the signature cache and `toolSetFingerprint` react to extras changes (FR-006). The approval hash (`tool_quarantine.go:47`) is not touched (FR-011).

### D2. Storage shapes

- `config.ToolMetadata` gains one embedded struct `ToolListingExtras` (JSON `listing,omitempty`) rather than loose fields. No custom `MarshalJSON` on it (JSON methods on an embedded type promote to the embedder).
- `stateview.ToolInfo` gains the same struct.
- `storage.ToolApprovalRecord` (`internal/storage/models.go:402`) gains `ApprovedTitle`, `CurrentTitle`, `CurrentTitleInvalid`, `ApprovedAnnotationsTitle`, `CurrentAnnotationsTitleInvalid`, `TitleCaptured bool`, `TitleHeldEvidence *ScanHoldEvidence`. All `omitempty`. `HashSchemaVersion` stays 5.
- Store-wide marker: key `title_review_migrated` in the existing `meta` bucket (next to `schema_version`), which older binaries never rewrite.
- Older binaries decode records into their struct and re-encode only known fields, so any older write (enable/disable, approve; `tool_quarantine.go:3062-3101`, `storage/bbolt.go:514-540`) drops the title fields. FR-016's fail-closed rule turns that into "titles never approved", which is safe.

### D3. Title decision (`internal/runtime/tool_title_review.go`, new)

- **Migration pass** (`migrateTitleApprovals`, FR-015): runs once in `runtime.New`, after storage opens and before the first discovery, inside one BBolt transaction per server bucket: for every record, `ApprovedAnnotationsTitle = CurrentAnnotations.Title` when valid by FR-002, `TitleCaptured = true`; then set the store-wide marker. No live data is read.
- **Per-capture step**, inside `checkToolApprovalsResolved` (`tool_quarantine.go:892`), per tool, after the contract decision, under the same per-server approval lock:
  1. Normalise both live titles (FR-002), write `CurrentTitle`, `CurrentAnnotations.Title` and the invalid flags on the record. The held-record fast path (`tool_quarantine.go:528-552`, `:1587`) gains the two titles and flags in its "unchanged" comparison, so a title move on a held record is persisted.
  2. If `!TitleCaptured`: if the store-wide marker is set, this is a new record or lost state: approved titles stay empty (FR-016 / new tools); set `TitleCaptured`.
  3. If the record is `pending` or `changed`: stop (titles are approved with the contract, FR-012).
  4. For each title where live is valid, non-empty and ≠ approved: `auto` / quarantine off → adopt; `scan` → title-only scan (D5) → adopt if complete and no tiered finding, else withhold with evidence; `manual` → withhold. Invalid → withhold. Skip if the record already shows the same decision for the same live value (idempotent).
  5. Emit `tool_title_approved` / `tool_title_withheld` through the `*Ctx` emitters, and publish `review.changed` when the server's title-pending count changes (FR-013).
- The record's status never changes here; `enforceInvariant` is not involved.

### D4. Emitted projection (one function, every publisher)

- `Runtime.EmittedTitles(server, rawName, liveTitle, liveAnnTitle string) (title, annTitle string, pending bool)` reads the record under the read path the callability filter already uses and returns the FR-008 values; no record or a read error returns empty strings.
- Runtime publishers: `emittedListing(tool, rec)` is applied at the index write and the StateView update in `admitInventoryHeld` and in every re-apply path. The index diff (`lifecycle.go:1852-1865`, `:1996-2066`) compares the **projected** metadata, so an approval that changes only the emitted title rewrites the doc. The approval paths (`approveToolsLocked`, title adoption in D3, title approval in D6) call a new `republishEmittedListing(server, names)` that rewrites the index docs and StateView entries for those tools (today's re-apply at `tool_quarantine.go:2859-2867` passes no StateView callback).
- Server publishers: `buildDirectCatalog` (`mcp_routing.go:132`) calls `EmittedTitles` for every listed tool before rendering, so `/mcp/s/<server>`, the direct surface and direct `describe_tool` (`directCatalogEntry.toolMetadata()`) all carry projected values. `review.changed`/title approval already triggers a direct rebuild via `servers.changed`-style notification; PR-3 adds the title events to the rebuild triggers. `list_changed` follows from `toolSetFingerprint` over the rendered tools, so it fires exactly when the emitted listing changes (FR-006).
- Guard tests: an AST test asserts that every index upsert and StateView tool write in `internal/runtime` goes through `emittedListing`, and that `buildDirectCatalog` is the only constructor of `directCatalogEntry` outside tests. A qapilot mutation drops the projection on one surface and must be caught.
- Administrators' REST reads the record directly for pending values; agent-facing producers read only projected values.

### D5. Scanner (`internal/security/scanner`)

- New `ScanTitleVerdict(server, tool, field, title string) (verdict string, findings []ScanFinding, complete bool)`: builds a `detect.ToolView` whose `Description` is the title and whose schemas are empty, runs the same detector set (no peers), and returns tiered findings. Attribution is by construction: the view holds nothing but that title.
- `ScanToolMetadataVerdict` and the baseline, changed-contract and informational scans are unchanged (no titles), so contract admission is unaffected (FR-009).
- Eval: add title cases to `detect_corpus_v1.json` and copy into `bundled/selfcheck-corpus.json` (the self-check corpus is a copy, `project_tpa_selfcheck_corpus_copy_gate`); `cmd/scan-eval` gains a title mode that scores `ScanTitleVerdict` decisions (SC-004), including fixtures whose description carries a stronger finding.

### D6. Review fingerprint `rv3` and approval doors

- `review_fingerprint.go`: append `CurrentTitle`, `CurrentTitleInvalid`, `CurrentAnnotations.Title`, `CurrentAnnotationsTitleInvalid`, length-prefixed, `\x00absent` for empty. `reviewFingerprintVersion = "rv3"`.
- Bound doors (`ApproveToolsReviewedAt`, `ApproveAllToolsReviewedAt`, `tool_approval_review.go:76-185`): after `reviewBindingMismatch` passes under the lock, call `approveToolsLocked(..., approveTitles=true)`, which also sets `ApprovedTitle`/`ApprovedAnnotationsTitle` from the record's current **valid** values and clears `TitleHeldEvidence`. Candidate selection (`:167-172`) adds "approved with a pending title".
- Unbound doors (`ApproveToolsAt`, `ApproveAllToolsAt`, MCP `approve_tool`/`approve_all_tools`, the `expected == nil` branch at `:142`) pass `approveTitles=false`. Test: an unbound approval of a record with a pending title leaves the title withheld on every surface.
- CLI: `mcpproxy tools approve` gains the bound path (reads `/review`, sends fingerprints) so administrators can approve titles from the CLI.
- Review queue (`review.go:223-254`): count `TitlesPending`, keep servers whose only items are titles; `frontend/src/utils/reviewPresentation.ts:62-72` `needsReview` counts it.
- Agent-safe review projection (FR-013a): `composeReview` takes an `adminView bool`. The HTTP handler passes `true` only for a positively authenticated administrator (`auth.AuthContext.IsAdmin()`), never by default. MCP `inspect_quarantined` / `inspect_tools` always pass `false`, and the live-annotations fallback (`mcp.go:6158-6176`) strips both titles to emitted values. Previous values, diffs and evidence snippets are filtered the same way.
- qapilot (`internal/qapilot`): add the title state, scenarios S30-S35 (title change × trust mode; migration pass; new tool after migration; lost state after older rewrite; rv2 refusal; unbound approval does not approve titles), and the projection-drop mutation.

### D7. Re-emission

- **Direct surface** (`mcp_routing.go:386`, `:459`): set `mcpTool.Title`, `mcpTool.Icons`, `mcpTool.DeferLoading`, `mcpTool.Meta` from the projected catalog entry (new `Listing` field on `directCatalogEntry`, `mcp_direct_catalog.go:86`). `_meta`: build `AdditionalFields` from the stored object, delete `toollisting.OwnedMetaKeys`, then `stampDirectTool` and `annotateToolsWithMaxResultSize` add mcpproxy's keys as today; `stripDirectToolStamp` restores the upstream `_meta`. Full and deferred paths get the same four fields; the Spec 102 cross-mode identity test is extended. `Execution` is never set.
- **retrieve_tools** (`mcp_entry_builder.go`): full entry adds `title` from the projected metadata. Compact unchanged.
- **describe_tool** (`mcp_describe_tool.go`): both resolution paths (index metadata and `directCatalogEntry.toolMetadata()`) carry projected titles; add `defer_loading` (true only) and `task_support`.
- **Index** (`internal/index/bleve.go`): `ToolDocument` gains `Title` (searchable, projected value) and `ListingJSON` (stored only). `searchableText` appends the title; `indexSchemaVersion` → `"3"`.
- **REST**: `Runtime.GetServerTools` (`runtime.go:3747-3799`, both its StateView and index renderers), `searchResultsToMaps` and the typed converter (`internal/httpapi/server.go:4193-4218`, `:4348-4356`), and `contracts.Tool` gain the fields. Administrator-only fields are filled only when the caller is a positively authenticated administrator (not by copying unconditionally and zeroing later). The scanner-export renderer (`internal/server/server.go:4460-4519`, `:5748-5751`) passes live titles to the scanner. Regenerate `oas/swagger.yaml` + `oas/docs.go`.
- **Code execution**: its listing reuses the full entry builder; covered by a test.

### D8. Web UI (`frontend/src`)

- `types/api.ts` `Tool` gains the REST fields.
- New `ToolIcon.vue`: `data:` icons in `<img>` via attribute binding (never `v-html`); theme match and nearest size to 32px. `https`: placeholder with the host; a "Load icon" button that sets `<img src>` for that component instance, offered only when the served CSP's `img-src` allows `https:` (read in PR-4 from the header set in `internal/server`). No new-tab or navigation fallback.
- `ServerDetail.vue:620` card title becomes the display name with the raw name beneath; `AnnotationBadges.vue` gains `deferred` and `task support` badges.
- Review view: title items (approved vs pending, findings), counted in `needsReview`.
- Playwright sweep with a fixture covering every icon case; the network log must show no request to icon hosts (SC-005). Add the new spec to the sweep's spec-list Go guard.

## PR breakdown

| PR | Scope | Main tests (written first) |
|----|-------|----------------------------|
| PR-1 | D1, D2 (metadata + StateView only), D7 REST admin-only fields (no titles to agent tokens yet), `internal/toollisting` | table tests for every validation rule, the validation order and every cap (including drop-record bounds and full base64 decode); discovery hash unchanged for a tool with no extras (golden hashes); REST admin vs agent-token field visibility |
| PR-2 | D2 record fields + marker, D3, D4 runtime half, D5, D6, qapilot, quarantine docs | trust mode × title change matrix; held-record title persistence; migration pass from seeded v5 records (SC-002 with 500 records); new tool after migration; older-binary rewrite fails closed; title finding never holds a contract; description finding never blocks a clean title; rv2 refusal; unbound approval leaves title withheld; scoped `/review`, scoped activity reads and MCP `inspect_quarantined` carry no pending text; title-only review item in queue + `review.changed` |
| PR-3 | D4 server half (direct catalog projection), D7, discovery docs | direct surface field-by-field equality with a fixture upstream (SC-001) in both modes; owned-key override; no `execution` anywhere; withheld titles absent on every agent surface incl. `/mcp/s/<server>` and direct `describe_tool` (SC-003); direct rebuild that sees a newer title than the admitted inventory emits none; `list_changed` exactly when the emitted listing changes, including a title approval with no upstream change (SC-006); index and profile search after approval; index rebuild on version bump |
| PR-4 | D8 | vitest for `ToolIcon` selection and the no-fetch rule; Playwright sweep with network assertion |

PR-3 must not merge before PR-2 (spec Delivery). PR-1 and PR-4's frontend type change can be developed in parallel once PR-1's REST contract is merged.

## Risks

- **Scanner false positives on titles** in scan mode only withhold the title, never the tool (the contract scan never sees titles), so the blast radius is cosmetic. The eval gate keeps precision measurable.
- **Many tool-metadata producers** (StateView and index renderers, search converters, direct catalog, scanner export): each is named in D4/D7 and covered by the SC-003 every-surface test.
- **Older binary rewrites** drop title state; covered by re-running first observation (D2).
- **mcp-go upgrade** (Spec 135 moves to ≥ v1.4.0): the field names are the same in v1.4.0; PR-1 adds a compile-time test that reads each field so an upstream rename breaks the build, not behaviour.

## Project Structure

### Documentation (this feature)

```text
specs/137-tool-listing-fields/
├── spec.md
├── plan.md
├── research.md
├── data-model.md
├── quickstart.md
├── contracts/
│   └── tool-listing-fields.md
└── checklists/requirements.md
```

### Source Code (touched)

```text
internal/toollisting/            # new leaf: validation, caps, owned _meta keys
internal/upstream/core/client.go # capture
internal/hash/hash.go            # discovery hash extras field
internal/config/config.go        # ToolListingExtras on ToolMetadata
internal/runtime/stateview/      # ToolInfo extras
internal/storage/models.go       # title review fields on ToolApprovalRecord
internal/runtime/tool_quarantine.go, tool_title_review.go (new), review_fingerprint.go
internal/security/detect/, internal/security/scanner/inprocess.go
internal/index/bleve.go
internal/server/mcp_routing.go, mcp_direct_catalog.go, mcp_direct_scope.go,
  mcp_entry_builder.go, mcp_describe_tool.go, server.go
internal/contracts/types.go, oas/
internal/qapilot/
frontend/src/types/api.ts, components/ToolIcon.vue (new), AnnotationBadges.vue, views/ServerDetail.vue
docs/features/tool-listing-fields.md (new), docs/features/security-quarantine.md
```

## Complexity Tracking

No constitution violations to justify.

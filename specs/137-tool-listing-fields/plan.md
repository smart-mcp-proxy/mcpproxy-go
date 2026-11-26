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

- New leaf package `internal/toollisting` (stdlib only): `NormalizeTitle(raw, toolName) (title string, invalid string)`, `ValidateIcons([]Icon) (kept []Icon, dropped []DropReason)`, `CapMeta(*mcp.Meta) (json.RawMessage, string)`, `NormalizeTaskSupport(string) string`, `OwnedMetaKeys` (FR-024), `ApplyServerBudget(tools)` (FR-004a). Icon data-URI checks decode the whole bounded base64 payload (≤ 32 KiB of `src`) to prove it is valid; the decoded bytes are never interpreted as an image.
- The existing debug log of the raw `annotations.title` (`client.go:534-548`) drops the title field; new logs carry reasons and counts only.
- `_meta` arrives decoded as `*mcp.Meta`; `CapMeta` re-marshals `AdditionalFields` canonically for the 16 KiB check. A non-object `_meta` fails mcp-go's decode of the whole listing today; unchanged.
- `annotations.title` is also run through `NormalizeTitle`; an invalid one is **cleared** from `ToolAnnotations.Title` and recorded only as `AnnotationsTitleInvalid{reason, preview}`, like an invalid top-level title. No raw invalid value is kept anywhere.
- `ToolMetadata.Hash` (the identity hash) is **not** changed: managed-session recovery (`session_reinit.go:102-120`) and the routing fingerprint depend on it. A new `ToolMetadata.ListingDigest` (SHA-256 over the canonical JSON of the extras, empty when all absent) is computed at capture; consumers compare the digest of the **projected** extras (D4). The approval hash (`tool_quarantine.go:47`) is not touched (FR-011).

### D2. Storage shapes

- `config.ToolMetadata` gains one embedded struct `ToolListingExtras` (JSON `listing,omitempty`) rather than loose fields. No custom `MarshalJSON` on it (JSON methods on an embedded type promote to the embedder).
- `stateview.ToolInfo` gains the same struct.
- `storage.ToolApprovalRecord` (`internal/storage/models.go:402`) gains `ApprovedTitle`, `CurrentTitle`, `CurrentTitleInvalid`, `ApprovedAnnotationsTitle`, `CurrentAnnotationsTitleInvalid`, `TitleHeldEvidence *ScanHoldEvidence`. All `omitempty`. `HashSchemaVersion` stays 5.
- Older binaries decode records into their struct and re-encode only known fields, so any older write (enable/disable, approve; `tool_quarantine.go:3062-3101`, `storage/bbolt.go:514-540`) drops the title fields. The record then has no approved titles, so FR-010 runs again on the next capture (FR-016), which is safe.

### D3. Title decision (`internal/runtime/tool_title_review.go`, new)

- **No migration pass** (FR-015): records without approved titles (every pre-upgrade record, every new record, every record an older binary rewrote) simply have empty approved values, and step 4 below decides.
- **Index** (FR-015a): no title is stored in index documents; `AnnotationsJSON` is stored as today but its `title` is ignored on read. Every index reader projects titles from the approval records (D4), so stale, rebuilt-from-stored, failed-rebuild or older-binary documents can never carry title text out.
- **Per-capture step**, inside `checkToolApprovalsResolved` (`tool_quarantine.go:892`), per tool, after the contract decision, under the same per-server approval lock:
  1. Normalise both live titles (FR-002), write `CurrentTitle`, `CurrentAnnotations.Title` and the invalid flags on the record. The held-record fast path (`tool_quarantine.go:528-552`, `:1587`) gains the two titles and flags in its "unchanged" comparison, so a title move on a held record is persisted.
  2. If quarantine is disabled: adopt every valid title and stop. If the record is `pending` or `changed`: stop (titles are approved with the contract, FR-012).
  3. For each title where live is valid, non-empty and ≠ approved: `auto` → adopt; `scan` → title-only scan (D5) → adopt if complete and no tiered finding, else withhold with evidence; `manual` → withhold. Invalid → withhold. Skip if the record already shows the same decision for the same live value (idempotent).
  4. Existing contract events: `emitToolAutoApprovedChange` (`tool_quarantine_scan_approve.go:83-108`) and every other `tool_quarantine_change` writer serialise annotation snapshots with emitted titles only; the activity read doors and the SSE scope filter (`tool_quarantine.go:3793-3802`, `httpapi/sse_scope.go:283-331`) strip `title` from annotation snapshots for scoped callers, covering rows written by older versions.
  5. Emit `tool_title_approved` / `tool_title_withheld` through the `*Ctx` emitters, and publish `review.changed` whenever any title item's pending value, validity or evidence changes, or an item appears or clears (FR-013); a digest of the server's pending title items is compared, not the count.
- The record's status never changes here; `enforceInvariant` is not involved.

### D4. Emitted projection (one function, every publisher)

- Two helpers, both reading one exact-key approval record (server, raw name) on the read path the callability filter already uses; no record or a read error returns empty strings:
  - `Runtime.EmittedTitlesLive(server, rawName, liveTitle, liveAnnTitle)` for the direct catalog, which holds its own fresh listing: emits a live value only when it is valid and equals the approved one.
  - `Runtime.EmittedTitlesStored(server, rawName)` for index-backed and StateView-less responses: takes live and approved values from the record itself and ignores the index hit's `annotations.title`.
  - The full entry builder (`mcp_entry_builder.go:174-184`) uses the one projection for both the top-level `title` and `annotations.title`. Test: no StateView entry plus a legacy index document with a conflicting title.
- Index readers: `retrieve_tools`, `describe_tool`, search REST, the `GetServerTools` index fallback (`runtime.go:3774-3799`) and profile searches call `EmittedTitlesStored` per hit and overwrite both titles; a unit test feeds a v2 document carrying an `annotations.title` and asserts it never surfaces.
- Runtime publishers: `emittedListing(tool, rec)` is applied at the StateView update in `admitInventoryHeld` and in every re-apply path. The index diff (`lifecycle.go:1852-1865`, `:1996-2066`) compares the identity hash, the annotations without title and the `ListingDigest` (every stored extra except titles, including drop diagnostics), so icon, `_meta`, flag and diagnostic changes rewrite the doc; titles never need an index write because they are projected at read time. StateView is rewritten on any captured change, diagnostic change or title decision. The approval paths (`approveToolsLocked`, title adoption in D3, title approval in D6) call a new `republishEmittedListing(server, names)` that rewrites the index docs and StateView entries for those tools (today's re-apply at `tool_quarantine.go:2859-2867` passes no StateView callback).
- Server publishers: `buildDirectCatalog` (`mcp_routing.go:132`) calls `EmittedTitlesLive` for every listed tool before rendering, so `/mcp/s/<server>`, the direct surface and direct `describe_tool` (`directCatalogEntry.toolMetadata()`) all carry projected values. `review.changed`/title approval already triggers a direct rebuild via `servers.changed`-style notification; PR-3 adds the title events to the rebuild triggers. No change to the rebuild rule (`mcp_routing.go:1666-1690`, and the single-server path `mcp_single_server.go:284-333`): a rebuild is skipped when the rendered listing and the routing are both unchanged, otherwise it calls `SetTools` and notifies as today. A withheld-to-withheld title change renders identically and, with extras out of the identity hash, leaves routing unchanged, so it is skipped. A title approval or a visible extras change re-renders and triggers a normal rebuild; the title events are added to the direct-rebuild triggers. Describe-only fields (`task_support`): the rebuild computes a third digest over them; when only it changed, the new catalog is published (the `describe_tool` read path) without `SetTools` and without `list_changed`. Handlers do not read `task_support`, so no re-registration is needed. Test: `optional` → `required` on the direct endpoint and `/mcp/s/<server>`.
- Response-time enforcement (FR-017a): a direct tool filter (registered before the terminal `stripDirectToolStampFilter`, `mcp_direct_scope.go`) reads each tool's stamp, calls `EmittedTitlesLive` with the title the tool was rendered with, and clears `Title`/`Annotations.Title` on the copy it returns when the answer differs. Direct `describe_tool` does the same on its catalog entry before responding. The single-server surface (`mcp_single_server.go:284-333`) registers the same filter. Test: approve A, capture withheld B, suppress the rebuild (test hook), list and describe on both endpoints: no A, no B.
- Guard tests: an AST test asserts that every StateView tool write in `internal/runtime` goes through `emittedListing` and every index-hit renderer calls `EmittedTitlesStored`, and that `buildDirectCatalog` is the only constructor of `directCatalogEntry` outside tests. A qapilot mutation drops the projection on one surface and must be caught.
- Administrators' REST reads the record directly for pending values; agent-facing producers read only projected values.

### D5. Scanner (`internal/security/scanner`)

- New `ScanTitleVerdict(server, tool, field, title string) (verdict string, findings []ScanFinding, complete bool)`: builds a `detect.ToolView` whose `Description` is the title and whose schemas are empty, runs the same detector set (no peers), and returns tiered findings. Attribution is by construction: the view holds nothing but that title.
- `ScanToolMetadataVerdict` and the baseline, changed-contract and informational scans are unchanged (no titles), so contract admission is unaffected (FR-009).
- Eval: add title cases to `detect_corpus_v1.json` and copy into `bundled/selfcheck-corpus.json` (the self-check corpus is a copy, `project_tpa_selfcheck_corpus_copy_gate`); `cmd/scan-eval` gains a title mode that scores `ScanTitleVerdict` decisions (SC-004), including fixtures whose description carries a stronger finding.

### D6. Review fingerprint `rv3` and approval doors

- `review_fingerprint.go`: append `CurrentTitle`, `CurrentTitleInvalid`, `CurrentAnnotations.Title`, `CurrentAnnotationsTitleInvalid`, length-prefixed, `\x00absent` for empty. `reviewFingerprintVersion = "rv3"`.
- Bound doors (`ApproveToolsReviewedAt`, `ApproveAllToolsReviewedAt`, `tool_approval_review.go:76-185`): after `reviewBindingMismatch` passes under the lock, call `approveToolsLocked(..., titleTools)`, which also sets `ApprovedTitle`/`ApprovedAnnotationsTitle` from the record's current **valid** values and clears `TitleHeldEvidence` for the named tools only. Candidate selection (`:167-172`) adds "approved with a pending title" only for tools named in `approve_titles`; otherwise the set is unchanged, so an existing bulk request is not refused as out of date.
- Title intent: the bound REST requests gain `approve_titles` (list of tool names, default empty). The Web UI review screen's confirmation lists every title it will approve (approved → pending) and sends exactly those names; tools on other pages or hidden by a filter are never named (`ReviewScreen.vue:203-206`, `:358-377` bind the full array today, so the title list is built from the confirmation, not from `review.tools`). The macOS tray sends nothing and stays contract-only (test: the unchanged tray payload against the new backend approves no title).
- Bound server approval (`tool_approval_review.go:191-232`, `:345-395`): keep two sets: `matched` (every non-blocked record whose fingerprint matched in `serverApprovalBindingError`) and `promote` (the pending ones, today's `validated`). Titles are approved for `matched ∩ approve_titles`, contract status is promoted only for `promote`. Test: a re-quarantined server with one approved tool that has a pending title, one pending tool and one blocked tool.
- Unbound doors (`ApproveToolsAt`, `ApproveAllToolsAt`, the unbound server approval, MCP `approve_tool`/`approve_all_tools`, the `expected == nil` branch at `:142`) pass `approveTitles=false`. Tests: an unbound approval of a record with a pending title, and an unbound server approval, leave the title withheld on every surface; the bound variants approve it.
- CLI: `mcpproxy tools approve` stays unbound (contract only; `cmd/mcpproxy/tools_approval.go:115-171` shows no review). `mcpproxy review approve` (`cmd/mcpproxy/review_cmd.go:65-128`, `:340-408`) gains `--approve-titles`: only with it does the command print every pending title (approved → pending, with findings), also under `--yes`, and send exactly those names; `--yes` alone never approves a title. Its bulk candidate filter adds title-only records only with the flag, matching the backend.
- Review queue (`review.go:223-254`): count `TitlesPending`, keep servers whose only items are titles; `frontend/src/utils/reviewPresentation.ts:62-72` `needsReview` counts it.
- Agent-safe review projection (FR-013a): `composeReview` takes an `adminView bool`. The HTTP handler passes `true` only for a positively authenticated administrator (`auth.AuthContext.IsAdmin()`), never by default. MCP `inspect_quarantined` / `inspect_tools` always pass `false`, and the live-annotations fallback (`mcp.go:6158-6176`) strips both titles to emitted values. Previous values, diffs and evidence snippets are filtered the same way.
- qapilot (`internal/qapilot`): add the title state, scenarios S30-S35 (title change × trust mode; first observation after upgrade under each trust mode; lost state after older rewrite; rv2 refusal; unbound approval and intent-less bound approval do not approve titles; response-time withholding with a suppressed rebuild), and the projection-drop mutation.

### D7. Re-emission

- **Direct surface** (`mcp_routing.go:386`, `:459`): set `mcpTool.Title`, `mcpTool.Icons`, `mcpTool.DeferLoading`, `mcpTool.Meta` from the projected catalog entry (new `Listing` field on `directCatalogEntry`, `mcp_direct_catalog.go:86`). `_meta`: build `AdditionalFields` from the stored object, delete `toollisting.OwnedMetaKeys`, then `stampDirectTool` and `annotateToolsWithMaxResultSize` add mcpproxy's keys as today; `stripDirectToolStamp` restores the upstream `_meta`. Full and deferred paths get the same four fields; the Spec 102 cross-mode identity test is extended. `Execution` is never set.
- **retrieve_tools** (`mcp_entry_builder.go`): full entry adds `title` from the projected metadata. Compact unchanged.
- **describe_tool** (`mcp_describe_tool.go`): both resolution paths (index metadata and `directCatalogEntry.toolMetadata()`) carry projected titles; add `defer_loading` (true only) and `task_support`.
- **Index** (`internal/index/bleve.go`): `ToolDocument` gains `ListingJSON` (stored only, extras without titles). No title field, `searchableText` unchanged. `indexSchemaVersion` → `"3"` only to rebuild documents with the new stored field; nothing security-relevant depends on the rebuild succeeding.
- **REST**: `Runtime.GetServerTools` (`runtime.go:3747-3799`, both its StateView and index renderers), `searchResultsToMaps` (`internal/server/server.go:4463`), `contracts.ConvertGenericSearchResultsToTyped` (`internal/httpapi/server.go:4598`) and `contracts.Tool` gain the fields. Administrator-only fields are filled only when the caller is a positively authenticated administrator (not by copying unconditionally and zeroing later). Regenerate `oas/swagger.yaml` + `oas/docs.go`.
- **Scanner exports** (FR-020a): the cached `serverToolMaps` output, the live fallback `toolMetadataToScanMap` (`server.go:5653-5669`) and stored-contract exports carry projected titles only. Test: an administrator-triggered scan of a server with a pending title, then scoped reads of the per-server and job-ID scan reports, show no pending text.
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
| PR-1 | D1 (incl. `ListingDigest`; identity hash unchanged, tested with a metadata-only managed-session recovery), D2 (metadata + StateView only), D7 REST admin-only fields (no titles to agent tokens yet), `internal/toollisting` | table tests for every validation rule, the validation order and every cap (including drop-record bounds and full base64 decode); discovery hash unchanged for a tool with no extras (golden hashes); REST admin vs agent-token field visibility |
| PR-2 | D2 record fields, D3, D4 runtime half, D5, D6, qapilot, quarantine docs | trust mode × title change matrix; held-record title persistence; upgrade from seeded previous-version records (SC-002 with 500 records; under `manual` every title is withheld, under `scan` clean ones are adopted on the first discovery; an unbound approval persisted before its republish grandfathers nothing); index documents from any version never surface a title (v2 doc with `annotations.title`, failed rebuild, older-binary rewrite); bound request without `approve_titles` approves no title, and an unchanged bulk request against a server with a pending contract and a title-only item still succeeds; a scan-approved contract change with a withheld title leaves no title text in scoped activity list/detail/export/SSE; CLI `--yes` without `--approve-titles` approves no title; same-count pending title replacement publishes `review.changed`; bound vs unbound server approval; older-binary rewrite fails closed; title finding never holds a contract; description finding never blocks a clean title; rv2 refusal; unbound approval leaves title withheld; scoped `/review`, scoped activity reads and MCP `inspect_quarantined` carry no pending text; title-only review item in queue + `review.changed` |
| PR-3 | D4 server half (direct catalog projection), D7, discovery docs | direct surface field-by-field equality with a fixture upstream (SC-001) in both modes; owned-key override; no `execution` anywhere; withheld titles absent on every agent surface incl. `/mcp/s/<server>` and direct `describe_tool` (SC-003); direct rebuild that sees a newer title than the admitted inventory emits none; response-time withholding with a suppressed rebuild; `task_support`-only change refreshes direct `describe_tool` without `list_changed`; legacy index document with a conflicting title and no StateView entry; `list_changed` exactly when the wire listing changes, including a title approval with no upstream change, and none for a withheld-to-withheld change on the direct surface and `/mcp/s/<server>` (SC-006); debug logs carry no title text; index rebuild on version bump |
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

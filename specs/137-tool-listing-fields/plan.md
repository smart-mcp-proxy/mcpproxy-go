# Implementation Plan: Preserve the New Tool Listing Fields

**Branch**: `137-tool-listing-fields` | **Date**: 2026-11-26 | **Spec**: [spec.md](spec.md)
**Input**: Feature specification from `specs/137-tool-listing-fields/spec.md`, issue #2042.

## Summary

Capture `title`, `icons`, `_meta`, `defer_loading` and `execution.taskSupport` at discovery, validate and cap them, store them on `config.ToolMetadata`, and re-emit them on the direct surface, `describe_tool`, `retrieve_tools`, REST and the Web UI. Titles (top-level and `annotations.title`) get a review rule of their own: an unapproved title is **withheld** from agent surfaces, never holding the tool. The rug-pull hash is untouched, so the upgrade needs no hash migration; the review fingerprint moves to `rv3` to cover the titles.

## Technical Context

**Language/Version**: Go 1.26 (core), TypeScript 5.9 / Vue 3.5 (Web UI)
**Primary Dependencies**: `github.com/mark3labs/mcp-go` v1.0.0 (already parses every field), Bleve, BBolt. **No new dependencies.**
**Storage**: BBolt `tool_approvals` records (additive JSON fields), Bleve index (schema version bump `"2"` → `"3"`)
**Testing**: `go test -race`, `./scripts/test-api-e2e.sh`, vitest, Playwright Web UI sweep, `internal/qapilot` model + scenarios
**Target Platform**: macOS, Linux, Windows; both editions
**Project Type**: Go backend + Vue frontend
**Performance Goals**: no measurable change to discovery or `tools/list` latency on the 3,000-tool rig (`reference_many_tool_fleet_perf_rig`); the title scan adds at most two short fields per tool to an existing scan
**Constraints**: per-tool and per-server caps (spec FR-002-FR-004a); never fetch an icon
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

- `config.ToolMetadata` gains one embedded struct `ToolListingExtras` (JSON `listing,omitempty`) rather than six loose fields, so `ToolMetadata`'s JSON shape changes in one key and tests can compare it as a unit. **Note**: the repo memory warns that JSON methods on a shared type promote to embedders; `ToolListingExtras` gets no custom `MarshalJSON`.
- `stateview.ToolInfo` gains the same struct.
- `storage.ToolApprovalRecord` (`internal/storage/models.go:402`) gains `ApprovedTitle`, `CurrentTitle`, `ApprovedAnnotationsTitle`, `TitleCaptured bool`, `TitleHeldEvidence *ScanHoldEvidence` (reusing the Spec 118 evidence shape). All `omitempty`. No bucket or schema-version change; `HashSchemaVersion` stays 5.
- Older binaries decode records with `json.Unmarshal` into their struct and re-encode only known fields, so a write by an older binary drops the new fields. FR-016: when `TitleCaptured` is absent but the record carries a `CurrentHashSchemaVersion` of 5 and an older binary wrote it, the new binary re-runs first observation (FR-015). That re-adopts `annotations.title` (still safe, it was emitted by the older binary too) and re-applies FR-010 to the top-level title. A top-level title an older binary could have exposed: none (older binaries never emit it). So FR-016 reduces to "re-run FR-015", which is safe; the spec's FR-016 wording is tightened to that.

### D3. Title decision (`internal/runtime/tool_title_review.go`, new)

Runs inside `checkToolApprovalsResolved` (`tool_quarantine.go:892`), per tool, after the contract decision, under the same lock:

1. If `!TitleCaptured`: first observation (FR-015). `ApprovedAnnotationsTitle = live annotations.title` unless invalid; set `CurrentTitle`; `TitleCaptured = true`; then go to step 2 for the top-level title only.
2. For each of the two titles where live ≠ approved:
   - record status `pending`/`changed` (tool not yet approved): nothing extra; approving the tool approves the titles (FR-012).
   - invalid → withhold.
   - `quarantine_enabled: false` or `trust_mode: auto` → adopt.
   - `scan` → use the in-process verdict already computed for this server at `tool_quarantine.go:325` when present, else compute it; adopt only if coverage is complete and no finding's `Field` is that title (see D5). Else withhold, store findings in `TitleHeldEvidence`.
   - `manual` → withhold.
3. Emit activity rows `tool_title_approved` / `tool_title_withheld` through the `*Ctx` emitters (actor in ctx, `project_activity_actor_context`). Repeat decisions on the same live value write nothing (idempotent).

The record's status never changes in this function. `enforceInvariant` is not involved.

### D4. Emitted projection (one function, every writer)

`emittedListing(tool *config.ToolMetadata, rec *storage.ToolApprovalRecord) *config.ToolMetadata` returns a copy whose `Title` and `Annotations.Title` are the approved-and-live values or empty (FR-008). It is applied at every place the runtime publishes tool metadata to consumers: the index write, the StateView update, and the direct catalog build, i.e. in `admitInventoryHeld` and in the approval reindex/sweep re-apply paths (Spec 117 PR-3). The last-good snapshot keeps **live** values, so a re-apply recomputes the projection from the current record. An AST guard test (pattern of the Spec 126 outcome guard) asserts that every `index.Upsert*`/`StateView` tool write in `internal/runtime` goes through `emittedListing`.

REST for administrators reads the record directly for `pending_title` etc.; agent-token REST reads the projected metadata only.

### D5. Scanner (`internal/security/detect`, `internal/security/scanner`)

- `detect.ToolView` gains `Title`, `AnnotationsTitle`. `detect.FieldStrings` yields them right after the description with `SpanField` values `title` and `annotations_title`, each subject to the 64 KiB field cap (titles are ≤ 256 chars after FR-002, so the cap never applies).
- `NormalizedText`/`PhraseText` include the titles, so every phrase detector sees them.
- `ScanToolMetadataVerdict` (`inprocess.go:70`) passes the **live** titles (the decision is about the live text).
- Effect on existing approvals: a tool whose approved description is clean but whose (already emitted) `annotations.title` trips a detector now gets a finding. Under scan mode this only withholds that title on its next change; FR-015 adopts current `annotations.title` without consulting the scan (Assumption 2). The scan-eval gate corpora (`detect_corpus_v1.json`, `selfcheck-corpus.json`, `project_tpa_selfcheck_corpus_copy_gate`) get title-field cases; the self-check corpus is copied, not edited in place.

### D6. Review fingerprint `rv3` (`internal/runtime/review_fingerprint.go`)

Append `title:` + `CurrentTitle` and `atitle:` + `CurrentAnnotations.Title`, each length-prefixed, with `\x00absent` for empty. Bump `reviewFingerprintVersion` to `rv3`. `ApproveToolsReviewedAt` (Spec 117) on a matching fingerprint sets `ApprovedTitle = CurrentTitle` and `ApprovedAnnotationsTitle = CurrentAnnotations.Title` in the same records-only write, clears `TitleHeldEvidence`. A record that is `approved` with a pending title is accepted by the same door (FR-013); today that door may skip already-approved records, so the skip condition becomes "approved and no pending title". The qapilot model (`internal/qapilot`) gains the title state and scenarios S30-S33 (title change × trust mode, migration, rv2 refusal, combined contract+title approval), plus a mutation that drops the projection on one surface.

### D7. Re-emission

- **Direct surface** (`mcp_routing.go:386`, `:459`): set `mcpTool.Title`, `mcpTool.Icons`, `mcpTool.DeferLoading`, `mcpTool.Meta` from the catalog entry (new `Listing` field on `directCatalogEntry`, `mcp_direct_catalog.go:86`, projected per D4). `_meta`: build `AdditionalFields` from the stored object, delete `toollisting.OwnedMetaKeys`, then let `stampDirectTool` and `annotateToolsWithMaxResultSize` add mcpproxy's keys as today. `stripDirectToolStamp` keeps restoring the upstream `_meta` (now non-nil when the upstream had one). Both full and deferred render paths get the same four fields; the cross-mode identity test (D9 of Spec 102) is extended. `Execution` is never set (FR-029). `toolSetFingerprint` already covers `Hash`, which now covers extras (D1).
- **retrieve_tools** (`mcp_entry_builder.go`): full entry adds `title` from the projected metadata (top-level, else annotations title). Compact unchanged. The signature cache keys on `Hash`; a title change re-keys it, which is acceptable (signatures are cheap and warm on index add).
- **describe_tool** (`mcp_describe_tool.go`): `buildToolEntry` full mode gives `title`; add `defer_loading` (true only) and `task_support` after `applyDescribeOutputSchema`.
- **Index** (`internal/index/bleve.go`): `ToolDocument` gains `Title` (searchable) and `ListingJSON` (stored only, so search hits can render REST fields without a second lookup). `searchableText` appends the title. `indexSchemaVersion` → `"3"`.
- **REST** (`server.go:4331`, `:5755`): factor the two copies' common part into one helper while adding fields (both copies drifted once before). `contracts.Tool` gains `Title`, `Icons`, `DeferLoading`, `TaskSupport`, and admin-only `Meta`, `PendingTitle`, `PendingAnnotationsTitle`, `TitleInvalidReason`, `IconsDropped`, `MetaDropped`. Scoped callers get the admin-only fields zeroed in the same place `HeldReason` is zeroed today. Regenerate `oas/swagger.yaml` + `oas/docs.go` (`make swagger`).
- **Code execution**: its tool listing already reuses the full entry builder; covered by a test only.

### D8. Web UI (`frontend/src`)

- `types/api.ts` `Tool` gains the REST fields.
- New `ToolIcon.vue`: renders a `data:` icon in `<img>` (Vue attribute binding, never `v-html`), picks the theme-matching icon and the size nearest 32px; for `https` shows a placeholder with the host and a "Load icon" button that swaps in an `<img src>` for that component instance only. CSP: confirm `img-src` allows `data:`; for the explicit load, `https:` must be allowed. If the current CSP forbids it, the explicit load opens the URL in a new tab instead (decided in PR-4 after reading the CSP header in `internal/server`).
- `ServerDetail.vue:620` card title becomes the display name with the raw name in monospace beneath; `AnnotationBadges.vue` gains `deferred` and `task support` badges.
- Review view: title item with approved vs pending and findings, reusing `ToolDefinitionText.vue` diff styling.
- Playwright sweep (`docs/development/web-ui-verification.md`) with a fixture covering every icon case; the network log must show no request to icon hosts (SC-005). The sweep's spec-list Go guard must list the new spec (`reference_webui_sweep_launcher_traps`).

## PR breakdown

| PR | Scope | Main tests (written first) |
|----|-------|----------------------------|
| PR-1 | D1, D2 (metadata + StateView only), D7 REST admin fields, `internal/toollisting` | table tests for every validation rule and cap; discovery hash unchanged for a tool with no extras (golden hashes from current fixtures); hash changes when any extra changes; REST admin vs agent-token field visibility |
| PR-2 | D2 record fields, D3, D4, D5, D6, qapilot, quarantine docs | per trust mode × title change matrix; migration from seeded v5 records (no status change, no hash change; SC-002 with 500 records); rv2 refusal; AST guard for D4; scanner title-field corpus cases; combined approval |
| PR-3 | D7 direct surface, `retrieve_tools`, `describe_tool`, index, discovery docs | direct surface field-by-field equality with a fixture upstream (SC-001) in both modes; owned-key override; no `execution` anywhere; withheld titles absent on every agent surface (SC-003); `list_changed` on extras-only change (SC-006); index rebuild on version bump |
| PR-4 | D8 | vitest for `ToolIcon` selection and the no-fetch rule; Playwright sweep with network assertion |

PR-3 must not merge before PR-2 (spec Delivery). PR-1 and PR-4's frontend type change can be developed in parallel once PR-1's REST contract is merged.

## Risks

- **Scanner false positives on titles** in scan mode only withhold the title, never the tool, so the blast radius is cosmetic. The eval gate keeps precision measurable.
- **Two near-identical REST map builders** (`server.go:4331`, `:5755`): fixed by sharing a helper in PR-1.
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

# Data Model: Preserve the New Tool Listing Fields

## ToolListingExtras (new, on `config.ToolMetadata` as `listing` and on `stateview.ToolInfo`)

| Field | JSON | Type | Rule |
|---|---|---|---|
| Title | `title` | string | trimmed; empty or equal to the raw name → absent (FR-002) |
| ListingDigest | (on `ToolMetadata`, `listing_digest`) | string | SHA-256 of canonical JSON of every extra except titles and title diagnostics, including icon/`_meta` drop diagnostics; index change detection only, never identity |
| TitleInvalid / AnnotationsTitleInvalid | `title_invalid`, `annotations_title_invalid` | {reason, preview} | reason `too_long`, `control_char`, `bidi_control`; preview ≤ 64 chars with controls escaped; the raw value is not kept |
| Icons | `icons` | []Icon | ≤ 8, validated (FR-003) |
| IconsDropped | `icons_dropped` | {count, items[≤8]{index, reason}} | reasons: `scheme`, `too_long`, `bad_data_uri`, `mime`, `mime_mismatch`, `sizes`, `theme`, `count`, `tool_budget`, `server_budget` |
| Meta | `meta` | raw JSON object | ≤ 16 KiB canonical, `progressToken` removed |
| MetaDropped | `meta_dropped` | string | `too_large`, `server_budget` |
| DeferLoading | `defer_loading` | bool | |
| TaskSupport | `task_support` | string | `forbidden`, `optional`, `required`, `unknown`, or absent |

`Icon`: `src`, `mime_type`, `sizes[]`, `theme` (`light`|`dark`).

## ToolApprovalRecord additions (`internal/storage/models.go`)

| Field | JSON | Meaning |
|---|---|---|
| ApprovedTitle | `approved_title` | last approved top-level title ("" = none approved) |
| CurrentTitle | `current_title` | live top-level title (valid, normalised) |
| CurrentTitleInvalid / CurrentAnnotationsTitleInvalid | `current_title_invalid`, `current_annotations_title_invalid` | {reason, preview} when the live value is invalid |
| ApprovedAnnotationsTitle | `approved_annotations_title` | last approved `annotations.title`; live is `CurrentAnnotations.Title` |
| TitleHeldEvidence | `title_held_evidence` | scan findings for a withheld title |

No change to `ApprovedHash`, `CurrentHash`, `HashSchemaVersion`, `Status`.

No migration marker: a record without approved titles (pre-upgrade, new, or rewritten by an older binary) goes through FR-010.

## Title state per field (derived, never stored)

```
approved   : live == approved                → emitted only if also servable (observed in this process at the current incarnation, not suppressed; direct surfaces: rendered == live)
withheld   : live != approved, live valid    → not emitted; review item
invalid    : live fails FR-002               → not emitted; shown to admins
absent     : live == ""                      → nothing emitted
```

Validity is checked first: an invalid live value is `invalid` whatever the approved value. Transitions: withheld → approved by a title-only scan (scan mode), by adoption (auto / quarantine off), or by a review-bound approval (`rv3`); never by an unbound approval. Any → withheld when the upstream changes the value and the trust mode does not adopt it.

## Review fingerprint

`rv3-` + SHA-256 over the `rv2` parts, then `CurrentTitle`, `CurrentTitleInvalid` reason, `CurrentAnnotations.Title`, `CurrentAnnotationsTitleInvalid` reason, each length-prefixed, absent encoded as `\x00absent`.

## Activity types (administrator-only: added to `storage.IsAdminOnlyActivityType`; tool-quarantine rows are not)

- `tool_title_approved`: `{field, before, after, by: scan|auto|reviewer, verdict}`
- `tool_title_withheld`: `{field, approved, live, reason: scan_findings|manual|invalid, findings}`
- `tool_icons_changed`: `{before_count, after_count, before_digest, after_digest}`

## Search index (`internal/index`)

`ToolDocument` gains `ListingJSON` (stored only, extras without titles). No title is stored or searchable (`annotations_json` is written and rebuilt without `title`); titles are projected from approval records at read time. `indexSchemaVersion` `"2"` → `"3"`.

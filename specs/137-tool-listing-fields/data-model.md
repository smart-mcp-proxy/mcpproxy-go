# Data Model: Preserve the New Tool Listing Fields

## ToolListingExtras (new, on `config.ToolMetadata` as `listing` and on `stateview.ToolInfo`)

| Field | JSON | Type | Rule |
|---|---|---|---|
| Title | `title` | string | trimmed; empty or equal to the raw name → absent (FR-002) |
| TitleInvalid | `title_invalid` | string | reason: `too_long`, `control_char`, `bidi_control`; title kept for admins, never emitted |
| AnnotationsTitleInvalid | `annotations_title_invalid` | string | same for `annotations.title` |
| Icons | `icons` | []Icon | ≤ 8, validated (FR-003) |
| IconsDropped | `icons_dropped` | []{index, reason} | reasons: `scheme`, `too_long`, `bad_data_uri`, `mime`, `mime_mismatch`, `sizes`, `theme`, `count`, `tool_budget`, `server_budget` |
| Meta | `meta` | raw JSON object | ≤ 16 KiB canonical, `progressToken` removed |
| MetaDropped | `meta_dropped` | string | `too_large`, `not_object`, `server_budget` |
| DeferLoading | `defer_loading` | bool | |
| TaskSupport | `task_support` | string | `forbidden`, `optional`, `required`, `unknown`, or absent |

`Icon`: `src`, `mime_type`, `sizes[]`, `theme` (`light`|`dark`).

## ToolApprovalRecord additions (`internal/storage/models.go`)

| Field | JSON | Meaning |
|---|---|---|
| ApprovedTitle | `approved_title` | last approved top-level title ("" = none approved) |
| CurrentTitle | `current_title` | live top-level title |
| ApprovedAnnotationsTitle | `approved_annotations_title` | last approved `annotations.title`; live is `CurrentAnnotations.Title` |
| TitleCaptured | `title_captured` | migration marker (FR-015) |
| TitleHeldEvidence | `title_held_evidence` | scan findings for a withheld title |

No change to `ApprovedHash`, `CurrentHash`, `HashSchemaVersion`, `Status`.

## Title state per field (derived, never stored)

```
approved   : live == approved                → emitted
withheld   : live != approved, live valid    → not emitted; review item
invalid    : live fails FR-002               → not emitted; shown to admins
absent     : live == ""                      → nothing emitted
```

Transitions: withheld → approved by scan (scan mode), by adoption (auto / quarantine off), or by a review-bound approval (`rv3`). Any → withheld when the upstream changes the value and the trust mode does not adopt it.

## Review fingerprint

`rv3-` + SHA-256 over the `rv2` parts, then `CurrentTitle`, then `CurrentAnnotations.Title`, each length-prefixed, absent encoded as `\x00absent`.

## Activity types (administrator-only, like other quarantine rows)

- `tool_title_approved`: `{field, before, after, by: scan|auto|reviewer, verdict}`
- `tool_title_withheld`: `{field, approved, live, reason: scan_findings|manual|invalid, findings}`
- `tool_icons_changed`: `{before_count, after_count, before_digest, after_digest}`

## Search index (`internal/index`)

`ToolDocument` gains `Title` (searchable, emitted value only) and `ListingJSON` (stored only). `indexSchemaVersion` `"2"` → `"3"`.

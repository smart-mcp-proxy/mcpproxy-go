# Contract: Where each field is emitted

| Field | Direct surface (full + deferred) | `retrieve_tools` full | `retrieve_tools` compact | `describe_tool` | Code execution | REST admin | REST agent token | Web UI |
|---|---|---|---|---|---|---|---|---|
| top-level `title` | emitted value | `title` (emitted, else emitted annotations title) | — | `title` (same) | as full | emitted + `pending_title` + `title_invalid_reason` | emitted | display name |
| `annotations.title` | emitted value | inside `annotations` (emitted) | — | inside `annotations` (emitted) | as full | emitted + `pending_annotations_title` | emitted | badge |
| `icons` | validated set | — | — | — | — | `icons`, `icons_dropped` | `icons` | `ToolIcon` (data: inline img; https on request) |
| `_meta` | stored object, owned keys replaced | — | — | — | — | `meta`, `meta_dropped` | — | collapsed JSON (admin) |
| `defer_loading` | when true | — | — | `defer_loading: true` | — | `defer_loading` | `defer_loading` | "loaded on demand" badge |
| `execution.taskSupport` | **never** | — | — | `task_support` | — | `task_support` | `task_support` | badge; `required` = not callable yet |

"Emitted" = the live value when it equals the approved value, else absent (spec FR-008).

## REST `contracts.Tool` additions

```json
{
  "title": "Create issue",
  "icons": [{"src": "https://example.com/i.png", "mime_type": "image/png", "sizes": ["48x48"], "theme": "light"}],
  "defer_loading": true,
  "task_support": "optional",
  "meta": {"vendor/x": 1},
  "pending_title": "Create issue (v2)",
  "pending_annotations_title": "",
  "title_invalid_reason": "",
  "icons_dropped": [{"index": 2, "reason": "scheme"}],
  "meta_dropped": ""
}
```

The last six keys are present only for a positively authenticated administrator. `GET /api/v1/servers/{id}/review` for a scoped caller and MCP `quarantine_security` `inspect_quarantined`/`inspect_tools` for every caller use the agent-safe projection: emitted titles plus `title_pending: true|false`, no pending or invalid text anywhere (values, diffs, evidence).

## Review door

The existing review-bound tool approvals (`ApproveToolsReviewedAt`, `ApproveAllToolsReviewedAt`) accept `rv3-` fingerprints only. A record that is `approved` and has a pending title is listed (`titles_pending` in the queue) and selectable. On a match under the lock, the contract and both valid titles are approved in the same write. Unbound approvals (no fingerprint, including MCP `approve_tool`/`approve_all_tools`) approve the contract only, never a title.

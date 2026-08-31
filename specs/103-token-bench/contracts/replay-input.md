# Contract: replay input

## Source

`GET /api/v1/activity/export?format=json` — equivalently `mcpproxy activity export --format
json`. JSONL, one activity record per line.

**CSV is not a valid input.** It drops `work_session_id`, arguments, response and every byte
field, so it can neither group units of work nor account tokens.

## Required fields

| Field | Use |
|---|---|
| `work_session_id` | Groups records into one unit of work (spec 082). Records lacking it are unattributed and reported as such. |
| `request_id`, `parent_id` | Joins code-execution sub-calls to the call that issued them. |
| `tool_name`, `server_name` | Resolves the call against a mode's tool surface. |
| `status` | success / error / blocked / rejected. |
| `response_truncated` | Marks a record whose stored content was cut. |
| `request_bytes`, `response_bytes` | **Measured pre-truncation.** The accurate cost basis. |
| `has_sensitive_data` | Best-effort exclusion signal — see limitation below. |

### Backend change this contract requires

`request_bytes` and `response_bytes` exist on the storage record and are documented as
measured pre-truncation, but are **absent from the export contract**. They must be added to
the export DTO and copied in the export projection.

Without them, FR-002 can only *exclude* truncated records. With them, it can *annotate them
accurately*, which materially increases how much real traffic the headline can use. This is
the only production-code change the feature requires.

## Privacy contract

1. **Default is bodies-off.** The headline is computable from byte sizes alone.
2. **Bodies-on is a separate, explicit opt-in** and prints a warning. The export path does
   **not** mask: masking is wired into the list and detail handlers only, so a bodies-on
   export is raw and unmasked by design — it is the compliance surface.
3. **`has_sensitive_data` is not a guarantee.** It is written asynchronously *after* the
   record is persisted, so a freshly exported record may be sensitive but not yet flagged.
   Exclude-by-flag is a best-effort reducer and must be documented as such wherever relied on.
4. **Nothing crosses the loader boundary but counts.** Sessions and calls surrender sizes,
   statuses and derived measurements. No content reaches a report, a dashboard, or any
   third-party service including model providers.
5. **Inputs live outside the repository**, are never committed, and the documented procedure
   tells an operator how to delete them.

## Loader output

Grouped `ReplaySession` values carrying usability flags computed once at load:
`truncated`, `bodies_missing`, `sensitive`, `unreplayable`. Every exclusion is counted and
reported (FR-003, SC-008). Silence is never success.

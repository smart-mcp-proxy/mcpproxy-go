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
| `request_bytes`, `response_bytes` | **Byte lengths measured pre-truncation — not token counts.** Basis for an explicitly-estimated response cost only. |
| `has_sensitive_data` | Best-effort exclusion signal — see limitation below. |

### Backend change this contract requires

`request_bytes` and `response_bytes` exist on the storage record and are documented as
measured pre-truncation, but are **absent from the export contract**. They must be added to
the export DTO and copied in the export projection.

Without them, FR-002 can only *exclude* truncated records. With them, a truncated record can
carry an explicitly-estimated response cost instead of being dropped.

**They do NOT make response cost measurable.** Tokenizing requires the text; a byte length
yields an estimate at best. See the cost split below.

**Known gaps in byte coverage** — records where even the byte-length estimate is unavailable,
so they must fall to exclusion accounting rather than silently contributing zero:

- **Internal `retrieve_tools` emission carries no byte counts.** With bodies off its response
  cost cannot even be estimated. It is *not* unrecoverable in general — the handler writes the
  FULL response to the activity log (`internal/server/mcp.go:2061-2064`), so a bodies-on export
  carries it.
- **Code-execution sub-calls record both byte counts as zero**
  (`internal/server/mcp_code_execution.go:811-818`). A truncated sub-call therefore cannot use
  the byte-length estimate either, and must be counted as excluded.
- **A truncated `retrieve_tools` record does not tell you what the agent PAID, and today you
  cannot even identify one.** The activity log stores the FULL pre-truncation response while
  the agent consumed truncated text, so tokenizing the stored text OVERSTATES the agent's cost.
  Worse, the truncation is not recorded: `wasTruncated` is computed in the handler
  (`internal/server/mcp.go:2019-2035`) and only logged — `emitActivityInternalToolCall`
  (`:810`) has no truncation parameter, so the flag never reaches the activity record.

  **This makes a SECOND backend change necessary** for the exclusion rule to be implementable:
  propagate the truncation flag onto internal tool-call activity. Without it the loader cannot
  distinguish a truncated `retrieve_tools` record from a complete one and would silently
  overstate.

  **Fallback if that change is not wanted**: exclude ALL internal `retrieve_tools` records from
  response-cost accounting and report the exclusion count. Blunt, but honest — and it must be
  an explicit decision, not a silent default.

## Privacy contract

1. **Replay requires a FLEET INPUT as well as the recording.** A menu cost is a property of the
   tool definitions the agent was shown, and the export carries no fleet snapshot. Built-in
   proxy tools alone are not the menu — under `direct` the menu is the whole upstream fleet.
   So `-replay <jsonl>` on its own can compute nothing; it must be paired with either a frozen
   fleet corpus (the existing committed corpora already serve this) or a live proxy to read
   the current fleet from. **A recording-only invocation is an error, not a degraded run.**

2. **Default is bodies-off. What that yields, per cell:**

   | Mode cell | Menu cost | Absolute complete-workload cost | Cross-mode delta |
   |---|---|---|---|
   | `direct_full` | measured (needs fleet input) | **NO** — response text absent | **measured** vs `direct_deferred` |
   | `direct_deferred` | measured (needs fleet input) | **NO** — response text absent | **measured** vs `direct_full` |
   | `code_exec` | measured (needs fleet input) | **NO** | not comparable bodies-off |
   | `retrieve_full` | measured (needs fleet input) | **NO** | **NO** — the mode changes the response body |
   | `retrieve_compact` | measured (needs fleet input) | **NO** | **NO** — same |

   **No cell has a measured ABSOLUTE complete-workload cost bodies-off**, because complete
   workload includes every consumed response and that text is absent. What bodies-off does give
   is (a) menu cost per cell, and (b) the cross-mode DELTA between the two direct cells, whose
   identical call responses cancel out of the comparison. That delta is the honest bodies-off
   headline; an absolute figure is not available.

   **What the recording contributes** is the *recorded call shape* — call sequence, tool mix and
   call counts — evaluated against the supplied fleet input. It does not contribute fleet size.

   **Consequence to state in every replay report**: a replay scores a recorded workload against
   the SUPPLIED fleet, not the fleet as it stood when the session was recorded. Internally valid
   across modes; not a historical reconstruction.

3. **Bodies-on is a separate, explicit opt-in** and prints a warning. The export path does
   **not** mask: masking is wired into the list and detail handlers only, so a bodies-on
   export is raw and unmasked by design — it is the compliance surface.
4. **`has_sensitive_data` is not a guarantee.** There is no persisted sensitivity field: the
   flag is derived at export from detection metadata added asynchronously after initial
   persistence, so a freshly exported record may be sensitive but not yet flagged.
   Exclude-by-flag is a best-effort reducer and must be documented as such wherever relied on.
5. **Nothing crosses the loader boundary but counts.** Sessions and calls surrender sizes,
   statuses and derived measurements. No content reaches a report, a dashboard, or any
   third-party service including model providers.
6. **Inputs live outside the repository**, are never committed, and the documented procedure
   tells an operator how to delete them.

## Loader output

Grouped `ReplaySession` values carrying usability flags computed once at load:
`truncated`, `bodies_missing`, `sensitive`, `unreplayable`. Every exclusion is counted and
reported (FR-003, SC-008). Silence is never success.

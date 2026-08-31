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

**Known gap, precisely scoped**: internal `retrieve_tools` activity emission carries no byte
counts, so with bodies OFF its response cost cannot even be estimated. It is *not* unrecoverable
in general — the handler writes the FULL response to the activity log
(`internal/server/mcp.go:2061-2064`), so a bodies-on export does carry it. Report it as
unavailable in the bodies-off configuration, not as unavailable outright.

## Privacy contract

1. **Default is bodies-off. Be precise about what that can and cannot measure.**

   | Mode cell | What the mode changes | Measurable bodies-off? |
   |---|---|---|
   | `direct_full`, `direct_deferred` | the static tool-surface payload | **Yes** — computed from the fleet's tool definitions, no recorded content needed |
   | `retrieve_full`, `retrieve_compact` | the per-call `retrieve_tools` **response** | **No** — that response is the cost, and it is a body |
   | `code_exec` | the static surface | **Yes**, same as direct |

   So bodies-off covers three of five cells fully. The two `retrieve_tools` cells need bodies,
   because the thing their mode changes IS a response body.

   **What the recording contributes** is the *workload shape* — call sequence, tool mix, call
   counts, real fleet size — which is what makes the numbers about real usage rather than a
   frozen corpus. The tool definitions themselves come from the live fleet, not the recording.

   **Consequence to state in every replay report**: the export carries no fleet snapshot, so a
   replay scores the recorded workload against **today's** fleet, not the fleet as it stood
   when the session was recorded. If the fleet has changed, the comparison is still internally
   valid across modes but is not a historical reconstruction.

2. **Bodies-on is a separate, explicit opt-in** and prints a warning. The export path does
   **not** mask: masking is wired into the list and detail handlers only, so a bodies-on
   export is raw and unmasked by design — it is the compliance surface.
3. **`has_sensitive_data` is not a guarantee.** There is no persisted sensitivity field: the
   flag is derived at export from detection metadata added asynchronously after initial
   persistence, so a freshly exported record may be sensitive but not yet flagged.
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

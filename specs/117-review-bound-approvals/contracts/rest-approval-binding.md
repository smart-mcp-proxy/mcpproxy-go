# Contract: Review-Bound Approval REST API (PR-2, #1566)

Base `/api/v1`. Auth unchanged: `X-API-Key`, or the socket. Agent tokens are refused as today, because server mutation is admin-only. The macOS tray client of this contract is in PR #1572.

## GET /servers/{id}/review: additive field

```jsonc
{
  "success": true,
  "data": {
    "server": { "name": "notes2", "quarantined": true, "definitions_captured": true, ... },
    "tools": [
      {
        "name": "read_note",
        "tier": "read",
        "approval_status": "pending",
        "annotations": { "readOnlyHint": true },
        "current_hash": "rv2-3f1c…(64 hex)",   // NEW: review fingerprint (opaque)
        ...
      }
    ]
  }
}
```

- Every tool carries a non-empty `current_hash` (FR-001/FR-003).
- The value changes on a definition change or a hint change (FR-002).
- Clients send it back unchanged. It is not the storage hash and not the `current_hash` of `GET /servers/{id}/tools/{tool}/diff`.

## POST /servers/{id}/security/approve

Request:
```jsonc
{
  "force": false,
  "block": ["write_note"],
  "expected_hashes": { "read_note": "rv2-…", "write_note": "rv2-…" }  // NEW, optional
}
```

| `expected_hashes` | Meaning |
|---|---|
| absent or `null` | **unbound**: legacy behaviour, except FR-012 (no promote-all after unquarantine) |
| `{}` | bound to the empty snapshot: any reachable record fails the approval |
| `{name: fp, …}` | bound: the reachable set (pending + enabled approved − block) must equal-match |

OpenAPI: both request schemas declare `expected_hashes` as `type: [object, "null"]` with string values, so a schema-validating client accepts an explicit `null` and rejects a string or an array. swag cannot emit a type union, so the field is tagged `extensions:"x-nullable"` and `make swagger` rewrites the marker with `cmd/oas-nullable` (Sol r3).

Order of checks:
1. `requireSecurity`.
2. Body decode → 400.
3. Scanner dangerous gate (unless `force`) → 409 with the unchanged "dangerous" message.
4. Inside the commit section, the binding → 409 `review_out_of_date`.
5. Block target missing:
   - bound request → 409 `review_out_of_date` with `missing_blocks`;
   - unbound request → 400 as today.

Success (200) is unchanged: `{"status":"approved","server_name":…,"approved_count":…,"blocked_count":…,"still_pending":…,"still_changed":…}`. `still_pending` now also counts tools filed after the commit.

Stale (409):
```json
{
  "success": false,
  "error": "tool review for server 'notes2' is out of date (definition changed since review: read_note; not in the review: drop_db); nothing was approved — fetch the review again",
  "error_code": "review_out_of_date",
  "stale_tools": { "changed": ["read_note"], "unreviewed": ["drop_db"], "missing_blocks": [] },
  "request_id": "…"
}
```

Guarantees on 409:
- no integrity baseline is written;
- no tool record is written;
- the server stays quarantined in storage and config;
- no baseline marker is written;
- no `tool_approved` / `tool_blocked` events are emitted.

## POST /servers/{id}/tools/approve

```jsonc
{ "tools": ["t1"], "expected_hashes": { "t1": "rv2-…" } }          // bound per-tool
{ "approve_all": true, "expected_hashes": { "t1": "rv2-…", … } }   // bound approve-all
```

- With `tools`: each requested name that has a record must match. Names without a record → `not_approved`, as today.
- With `approve_all`: every `pending` and `changed` record must match.
- Mismatch → the same 409 body. Nothing is written and no events are emitted.
- Unbound requests behave exactly as today.

## Client rules (Web, CLI, tray: identical)

1. Build `expected_hashes` from the review shown when the operator clicks, never from a later reload.
2. Include every reviewed tool that carries `current_hash`, blocked ones included; extra names are harmless.
3. Send unbound only when **no** tool in the review carries `current_hash` (an older core).
4. A not-captured or empty review sends `{}`.
5. A force retry re-sends the binding of the attempt that returned "dangerous".
6. On 409 with "out of date" (or `error_code == "review_out_of_date"`): reload the review and show the message; do not open the force dialog.

## CLI surface

`mcpproxy review approve <server> [--all|--tools|--except] [--force] [--yes] [--from-review <file>]`:
- `--from-review <file>` (a saved `review show -o json`): selection and binding come from the file. An operator who reviewed H1 with `review show` and approves after the upstream moved to H2 gets 409.
- Without `--from-review`: the review is fetched once, and the interactive prompt lists every tool it will make reachable (tier, status, first description line, diff marker). The binding is that displayed fetch. With `--yes` and no file, it is "approve what the server serves now" (documented).
- On 409 out of date it prints the core message plus `Run 'mcpproxy review show <server>' and approve again.` and exits with code 1.
- `-o json` prints the error envelope as JSON.
- Unchanged: `mcpproxy security approve` stays unbound (follow-up O3).

## Compatibility matrix

| Client \ Core | main (no fingerprint) | PR-2 core |
|---|---|---|
| tray before #1572 / old scripts (unbound) | today | accepted; post-commit additions held (PR-2 ships on top of PR-1's marker) |
| tray #1572 | sends unbound (no hashes in review) | bound, validated |
| Web/CLI with PR-2 | n/a (shipped together) | bound |

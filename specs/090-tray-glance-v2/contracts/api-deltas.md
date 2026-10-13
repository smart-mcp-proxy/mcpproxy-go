# API Contract Deltas — Spec 090

No new endpoints. Three additive, backward-compatible deltas.

## 1. `GET /api/v1/activity` — projection whitelist

Request (tray glance poll changes its parameters):

```text
GET /api/v1/activity?type=tool_call,internal_tool_call,policy_decision&limit=100&exclude_payloads=true
```

- `type` now includes `policy_decision` (param already supports comma lists).
- `exclude_payloads=true` semantic change (additive): previously removed
  `arguments`, `response`, and ALL of `metadata`; now removes `arguments`,
  `response`, and all of `metadata` EXCEPT the contextual whitelist:

```json
{
  "metadata": {
    "intent": { "reason": "…", "operation_type": "read" },
    "decision": "blocked",
    "reason": "Intent rejected: …",
    "client_name": "claude-code",
    "client_version": "2.1.220"
  }
}
```

Whitelist keys absent from a record are simply omitted. Existing consumers of
`exclude_payloads` (the tray) only gain fields. Full (non-excluded) responses
are unchanged.

## 2. Policy-decision records and SSE event — `request_id`

- SSE event `activity.policy_decision` payload gains `"request_id": "<id>"`.
- Persisted `policy_decision` activity records gain top-level `request_id`
  (same field every other activity record already has).
- Records created before this change have no `request_id`; consumers must not
  collapse/correlate those (spec FR-015).

## 3. `GET /api/v1/sessions` — ordering guarantee

- Response shape unchanged.
- New guarantee: results are ordered by `last_activity` descending **before**
  `limit` truncation, over the whole retained set (≤100), regardless of
  session start time and `status`.
- The tray changes its call to `GET /api/v1/sessions?limit=100` (no status
  filter).

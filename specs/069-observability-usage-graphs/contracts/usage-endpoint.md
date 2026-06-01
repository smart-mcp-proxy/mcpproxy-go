# Contract: GET /api/v1/activity/usage

Serves the actor-owned usage aggregate (per-tool rollup + timeline + tokens-saved headline). Reads from the in-memory snapshot / TTL cache — **never** a full-log scan per request (SC-005).

## Request

`GET /api/v1/activity/usage`

| Query param | Type | Default | Notes |
|-------------|------|---------|-------|
| `window` | `24h` \| `7d` \| `all` | `24h` | Time span for both per-tool rollup and timeline (FR-004). |
| `server` | string | — | Filter to one server (FR-008). |
| `tool` | string | — | Filter to one tool. |
| `status` | `success` \| `error` \| `blocked` | — | Filter by status (FR-008). |
| `top` | int | `20` | Top-N tools by sort key; remainder folded into `other`. |
| `sort` | `calls` \| `resp_bytes` \| `error_rate` \| `p95` | `resp_bytes` | Drives the token-sink default ordering (US1). |

Auth: `X-API-Key` (REST default).

## Response 200 (`UsageAggregateResponse`)

```json
{
  "window": "24h",
  "generated_at": "2026-05-31T12:00:00Z",
  "freshness_ms": 1200,
  "token_source": "bytes",
  "tokens_saved": 184320,
  "tokens_saved_percentage": 92.4,
  "tools": [
    {
      "server": "github",
      "tool": "search_issues",
      "calls": 142,
      "errors": 3,
      "error_rate": 0.021,
      "blocked": 0,
      "total_resp_bytes": 5872013,
      "avg_resp_bytes": 41352,
      "total_req_bytes": 28400,
      "avg_req_bytes": 200,
      "sized_calls": 142,
      "p50_ms": 120,
      "p95_ms": 480,
      "last_used": "2026-05-31T11:58:00Z"
    }
  ],
  "other": { "tools_folded": 7, "calls": 33, "total_resp_bytes": 91240 },
  "timeline": [
    { "start": "2026-05-31T11:00:00Z", "calls": 40, "errors": 1, "total_resp_bytes": 1200000 }
  ]
}
```

- `token_source: "bytes"` labels the size-based proxy (FR-006); FR-010 will switch this to `"estimated_tokens"`.
- `tokens_saved*` echoed from existing `ServerTokenMetrics` (FR-007 / SC-008).
- `avg_*` computed over `sized_calls` only (records with `0` bytes excluded); `null`/omitted when `sized_calls == 0`.
- `other` present only when the tool list was truncated to `top`.
- Empty log → `tools: []`, `timeline: []`, `tokens_saved` from metrics (or 0) — never an error (FR-009 / SC-007).

## Response codes

| Code | When |
|------|------|
| 200 | Always on success, including empty data. |
| 400 | Invalid `window`/`sort`/`status` enum or non-int `top`. |
| 401 | Missing/invalid API key. |

## Test obligations (FR-011)

- API test: populate activity log (mix of servers/tools/statuses/durations/sizes incl. legacy 0-byte records), assert ranking order, error_rate math, avg excludes 0-byte calls, window filter, top-N + `other` fold, empty-state.
- Contract documented in `oas/swagger.yaml`; `./scripts/verify-oas-coverage.sh` must pass.

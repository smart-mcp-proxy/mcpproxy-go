# SSE Event Contracts — `servers.changed`

The Server-Sent Events stream exposed at `GET /events` (API-key gated) is the canonical push channel for state changes. This document describes the contract for the `servers.changed` event after this PR, and the legacy notify-only contract that older clients will continue to consume.

## Endpoint

```
GET /events
```

Authentication: `X-API-Key: <api-key>` header or `?apikey=<api-key>` query parameter.

Stream format: standard `text/event-stream`. Each event has an `event` name and `data` JSON object.

## Event: `servers.changed` (new payload)

Emitted whenever the set of upstream servers, their connection state, quarantine state, or tool counts change. Coalesced to at most one event per ~50 ms per coalescer window.

```
event: servers.changed
data: {
  "payload": {
    "reason": "<string: machine-readable cause>",
    "servers": [
      { /* contracts.Server, identical to GET /api/v1/servers data.servers[i] */ },
      ...
    ],
    "stats": { /* contracts.ServerStats, identical to GET /api/v1/servers data.stats */ }
  },
  "timestamp": <unix-seconds>
}
```

### Field semantics

- `reason` — machine-readable reason for the change. Values: `"sync"`, `"enable_toggle"`, `"quarantine_toggle"`, `"bulk_enable_toggle"`, `"restart"`, `"server-state-change"`, plus existing values from `lifecycle.go` callers. Clients MAY display this; they MUST NOT depend on a closed enum.
- `servers` — full server list **post-redaction**. Sensitive header values are masked using the same `redactServerHeaders` policy as the HTTP API. Identical shape to `GetServersResponse.Data.Servers`.
- `stats` — aggregate stats (total / connected / quarantined counts). Identical to the HTTP `stats` field.
- `timestamp` — UTC Unix seconds when the event was published (after coalescing).

### Producer guarantees

- Coalesced: bursts within ~50 ms produce a single event with the most recent payload.
- Ordered: events publish in the order their underlying state changes occurred (best-effort, single-publisher goroutine).
- Redacted: header redaction always applied before publish.
- Resilient: if `mgmt.ListServers` fails, a notify-only event (legacy contract, just `reason`) is published instead. Clients MUST tolerate either shape.

### Consumer guidance

1. On receipt, if `payload.servers` is present and non-null, replace your local server list state with it. Skip any `GET /api/v1/servers` refetch.
2. If `payload.servers` is missing, fall back to the existing refetch path. This handles transient producer-side errors and lets the same client work against older cores.
3. Do not assume `reason` is exhaustive — log unknown values; do not crash.

## Event: `servers.changed` (legacy notify-only fallback)

Older mcpproxy core releases publish the same event name with a notify-only payload:

```
event: servers.changed
data: {
  "payload": { "reason": "<string>" },
  "timestamp": <unix-seconds>
}
```

New clients MUST handle this shape (no `servers` field) by calling `GET /api/v1/servers` to fetch the current state.

## Other events (unchanged contracts)

- `status` — heartbeat plus `upstream_stats`. Cadence: on demand from supervisor + 30 s heartbeat. Unchanged.
- `config.reloaded` — full state reload. Unchanged.
- `activity` — new activity record. Unchanged.
- `ping` — 30 s liveness ping. Unchanged.

## Backward / forward compatibility

- Old clients ignore unknown `payload.servers` / `payload.stats` fields and continue refetching. Functionally unchanged from today.
- New clients can run against old cores: they look for `payload.servers`, find it absent, fall back to refetch.
- Future additions (e.g., a per-server `last_change_reason`) follow the same pattern: add fields to `payload`; old clients ignore.

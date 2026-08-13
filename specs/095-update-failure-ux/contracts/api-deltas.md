# API Deltas — spec 095

## New REST endpoint

`POST /api/v1/telemetry/update-failure`

- **Auth**: standard `/api/v1` chain — Unix-socket callers admitted (AdminContext), TCP requires `X-API-Key`. Not strict-socket-gated.
- **Request**: `application/json`, exactly `{"stage": "appcast"|"download"|"install"|"other"}`; decoder uses `DisallowUnknownFields`.
- **204 No Content**: stage accepted. Either the increment was durably persisted (telemetry active) or the request was a deliberate no-op (telemetry inactive at event time — config opt-out, env opt-out, CI, dev build). Callers cannot and must not distinguish.
- **400**: malformed JSON, unknown fields, trailing JSON values, or stage outside the closed set. Nothing recorded.
- **500**: persistence failure (`RecordUpdateFailure` returned an error) — 204 promises durability (FR-011), so a failed BBolt write must not masquerade as success. The tray treats it like any non-2xx (one log line).
- **Swagger**: full swaggo annotation block on the handler; `oas/swagger.yaml` + `oas/docs.go` regenerated via `make swagger` (CI `swagger-verify` enforced).

## ServerController seam (internal/httpapi)

One atomic addition, implemented by `internal/server.Server` (the interface's actual implementor) and by the test doubles (`MockServerController` + shared `baseController`):

```go
// Evaluates the telemetry gate (config enabled && !env-disabled && semver build)
// and, when active, maps stage → MCPX code and durably increments — one call,
// no gate/record race. recorded=false means deliberate no-op (still 204).
RecordUpdateFailure(stage string) (recorded bool, err error)
```

Handler strict decode: `DisallowUnknownFields` + trailing-value rejection (second `Decode` must return `io.EOF`).

## Heartbeat delta (no schema change)

Four new possible keys in the existing `diagnostics.error_code_counts_24h` map:
`MCPX_UPDATE_APPCAST_FAILED`, `MCPX_UPDATE_DOWNLOAD_FAILED`, `MCPX_UPDATE_INSTALL_FAILED`, `MCPX_UPDATE_OTHER_FAILED`.
Values are 24h-windowed occurrence counts (existing decay semantics); keys absent when zero. Two supporting changes (FR-014): the four constants are **registered in the diagnostics catalog** (`diagnostics.Has`/`All()` — `RecordErrorCode`'s own guard is prefix-only), and the anonymity scanner gains a structural rule for `diagnostics.error_code_counts_24h` (keys must be catalog-registered codes, values non-negative integers) — the scanner does not inspect that map today.

## Swift APIClient delta

```swift
func recordUpdateFailure(stage: UpdateFailureStage) async
```
One POST via a **status-only** request helper: sends the body, reads only the HTTP status, discards response data (the existing `postAction`/`performRequest` parse non-2xx bodies, which FR-016 forbids relying on); single attempt, bounded by the session timeout; on any failure exactly one local log line (stage + numeric status / error class name only). UpdateService receives the recorder as an injected `UpdateFailureRecorder` closure/protocol wired at the composition root (it has no APIClient dependency today).

## Version-skew matrix

| Tray | Core | Behavior |
|---|---|---|
| old | new | endpoint never called; unused |
| new | old | single POST → 404 → one local log line, nothing else |
| new | new | 204; counters flow into next heartbeat |

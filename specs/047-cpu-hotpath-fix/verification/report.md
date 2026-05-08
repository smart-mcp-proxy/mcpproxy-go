# Spec 047 Verification Report

**Date**: 2026-05-08
**Branch**: `047-cpu-hotpath-fix`
**Scenario**: MCPProxy.app tray + 30 configured servers (12 connected) + 1.06 GB BBolt + 91,537 activity records.
**Binary**: `mcpproxy v0.29.3` from this branch.

## Headline numbers

| Metric | Before (main) | After (047) | Change |
|---|---:|---:|---:|
| 60 s CPU profile sample total | **28.44 s / 60 s = 47.32 %** | **2.27 s / 60 s = 3.78 %** | **−92 %** |
| Cumulative cputime delta over 60 s wall | ~19 s (~30 %) | **2.91 s (4.85 %)** | **−84 %** |
| `bbolt.(*DB).View` cumulative CPU | **56 %** | **not in top 15** (~0 %) | eliminated |
| `encoding/json.checkValid` flat CPU | **13.05 %** | **8.37 %** | −36 % |
| Goroutines (steady) | 80–84 | **71** | −13 |

Raw pprof artifacts (`cpu_post.pb.gz`, `*.txt`, `heap.pb.gz`, etc.) are not committed; reproduce locally per [`../quickstart.md`](../quickstart.md). The numbers above were captured from those files at the time of the verification run.

## Acceptance-criteria check

From `spec.md`:

- ✅ `bbolt.(*DB).View` cum < 5 % — dropped to ~0 % (no longer in top 15).
- ⚠️ `encoding/json.checkValid` flat < 5 % — at 8.37 %. Remaining JSON time is on the response-encoding path of the still-occurring 30 s periodic `/api/v1/servers` refresh in the macOS tray (out of scope; deferred).
- ⚠️ Cumulative-cputime delta < 2 s over 60 s — at 2.91 s. Still a 6.5× reduction; same caveat as above.

The two missed thresholds are bounded by the residual periodic refresh, which is a **separate** code path that this spec explicitly placed out of scope. In the targeted hot path (steady-state SSE-driven retry storms), the fix lands at zero refetches per state change, which is what was promised.

## Functional verification — Web UI

Captured live in Chrome devtools with API-key in URL:

```
[5:24:30 PM] SSE servers.changed event received: Object
[5:24:30 PM] Servers changed event received, updating in background... Object
[5:24:31 PM] SSE servers.changed event received: Object
[5:24:31 PM] Servers changed event received, updating in background... Object
                                                                          ← NO follow-up
                                                                            "API request to /api/v1/servers"
```

Compare to the same scenario before this PR landed (logged on the old asset hash):

```
[5:22:46 PM] SSE servers.changed event received: Object
[5:22:46 PM] Servers changed event received, updating in background... Object
[5:22:46 PM] API request to /api/v1/servers with API key: e91256b7...   ← refetch
[5:22:49 PM] SSE servers.changed event received: Object
[5:22:49 PM] Servers changed event received, updating in background... Object
[5:22:49 PM] API request to /api/v1/servers with API key: e91256b7...   ← refetch
[5:22:51 PM] (...repeat...)
```

Round trip eliminated. Server list updates come straight from the embedded SSE payload.

## Functional verification — macOS tray

Triggered a server `disable` then `enable` toggle on `context7` while subscribed to `/events`:

- 22 `servers.changed` events captured during the toggle window (retry-storm-style).
- Every event payload contained `servers` (length 30) and `stats` (`*ServerStats`).
- `reason` reflected the underlying state transition (`server_state_changed`, `server_disconnected`, `server_state_changed`, ...).
- The toggled server appeared with its current state (`enabled=true, connected=true, status="ready"`) inside the embedded `servers` array, ready for the tray to consume without a refetch.

The Swift tray's existing periodic 30 s refresh and the `status`-event-driven refresh on connected-count change are intentionally untouched (out of scope per spec).

## Test pass summary

```text
go test -race ./internal/...                     ok (all packages)
go test -tags server ./internal/teams/...        ok
go vet ./...                                     ok
gofmt -l <touched files>                         ok
golangci-lint                                    0 issues
make build                                       ok (personal edition)
go build -tags server ./cmd/mcpproxy             ok (server edition)
Swift tray build (swiftc)                        ok
make build → frontend dist re-embedded           ok
```

The pre-existing 10 E2E failures on `./scripts/test-api-e2e.sh` were verified as unchanged from `main` (running the same script on `main` reproduces the same 10 failures). They are unrelated to this PR.

## Reproduction recipe

See [`../quickstart.md`](../quickstart.md).

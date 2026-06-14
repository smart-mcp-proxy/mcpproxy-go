# SSE reconnect-replay toast-storm reproduction (MCP-2215)

Parent: MCP-2207 ("tens of notifications" triage).

## What was investigated

Bug report: opening a security scan report surfaces **tens of Web UI toasts**
(browser top-right) while the backend core is stuck in a ~10s re-init loop, so
the SSE (`/events`) stream repeatedly drops, reconnects, and replays per-server
/ per-event state in bursts.

The earlier triage ruled the web UI out via a **static** read of `frontend/src`
at HEAD. MCP-2215 asked for a **live** reproduction, because a static read can
miss a dynamic, reconnect-only path.

## How to run

A built `mcpproxy` is required. Stand up a throwaway instance:

```bash
./mcpproxy serve --config=/tmp/mcpproxy-uitest-2215/mcp_config.json \
  --listen=127.0.0.1:18215 --log-level=info &
```

(config: `listen 127.0.0.1:18215`, `api_key uitest2215`, three sample servers).
Then, from a scratch dir symlinking `e2e/playwright/node_modules` and pinning
the Chromium binary in `playwright.config.ts` (see repo `CLAUDE.md`):

```bash
playwright test observer-positive-control.spec.ts   # proves the toast counter works
playwright test mocked-replay-storm.spec.ts          # 40-burst replay storm via mocked /events
# real-restart-loop.spec.ts: run alongside a bash loop that kills+restarts
# mcpproxy every ~7s for ~50s, so EventSource really drops/reconnects.
```

## The toast counter

A `MutationObserver` on `document` counts every node added that is an `.alert`
**inside** the `.toast.toast-end` container (exactly how `ToastContainer.vue`
renders a toast). Auto-dismiss removes toasts after 5s, so a point-in-time DOM
query under-counts — the observer counts cumulatively.

`observer-positive-control.spec.ts` injects one real toast node and asserts the
counter catches exactly it (count == 1). This makes the storm results a **true
negative**, not a broken selector. Note: matching bare `.alert` over-counts —
`.alert` is also used by the telemetry-consent banner and the "servers need
attention" warning, so the `.toast.toast-end` ancestor check is required.

## Result

| Scenario | Reconnects / replays | Toasts observed |
|---|---|---|
| Mocked `/events` replay storm | 40 bursts × N reconnects, 12s | **0** |
| Real backend restart loop | 7 real restarts (16 boots), 47s | **0** |

**Conclusion:** the Vue web UI emits **zero** toasts on SSE reconnect / state
replay. Every `addToast` call site in `frontend/src` is a user action
(button/form handler) or a one-shot scan-completion; there is no browser
Notification API usage, no global fetch-error→toast interceptor, and no
SSE-event→toast path. The reported storm does **not** originate in the web UI.

The likely real source (per the MCP-2207 triage) is the macOS tray's
`native/macos/.../NotificationService.swift` reacting to the same backend
restart loop — macOS Notification Center toasts render top-right, visually
similar to browser toasts. The frontend lane cannot fix that; re-route the
user-facing fix to the macOS lane.

The unit-level guard for the web-UI invariant lives at
`frontend/tests/unit/sse-reconnect-no-toast.spec.ts`.

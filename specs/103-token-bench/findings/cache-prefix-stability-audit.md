# Step 1: prefix-stability audit — is mcpproxy cache-hostile by construction?

**Date**: 2026-09-01. **Method**: static read of every tool-list mutation path, plus
the refresh log of one ordinary 4-server startup on an isolated instance. No API spend.

## Verdict

**Mixed, and the headline hazard is NOT the one I predicted.**

- The default `retrieve_tools` surface is **cache-stable**. Confirmed.
- The `code_execution` surface broadcasts `notifications/tools/list_changed` on every
  upstream state change for a tool list that provably cannot change. Gratuitous, but
  **not** a cache invalidation on its own (see "The correction").
- The `direct` surface genuinely re-writes its tool definitions on ordinary server
  churn, and that IS a full cache invalidation — `tools` sits first in the prefix, so
  it cascades through `system` and every message.
- The largest effect on the break-even argument is not invalidation at all. It is that
  **caching shrinks the absolute saving by ~10x while leaving the crossover fleet size
  roughly where it is.**

## Primary-source pricing (verified 2026-09-01, platform.claude.com)

| Fact | Value |
|---|---|
| Cache prefix order | `tools` -> `system` -> `messages`, hierarchical |
| 5-minute cache write | **1.25x** base input |
| 1-hour cache write | **2x** base input |
| Cache read | **0.1x** base input |
| Tool-definition change | "Modifying tool definitions (names, descriptions, parameters) **invalidates the entire cache**" |
| Minimum cacheable | 512 tok (Opus 5) / 1,024 (Sonnet 5, Opus 4.8) |

mcpproxy's `retrieve_tools` menu is 4,712 tokens — comfortably above every floor, so it
is cacheable on any current model.

## Mutation paths

`SetTools` in mark3labs/mcp-go sends `list_changed` to **every initialized client
unconditionally** — there is no content comparison, despite the code comment saying
"when the list of available tools changes":

```go
s.tools = newTools
...
if s.capabilities.tools.listChanged {
    s.SendNotificationToAllClients(mcp.MethodNotificationToolsListChanged, nil)
}
```

Three surfaces call it. What reaches each:

| Surface | Carries | Rebuilt on | Guarded? | Tool set can actually change? |
|---|---|---|---|---|
| `callToolServer` (`/mcp`, **default**) | built-ins only | `config.reloaded` only | n/a | Only on `enable_code_execution` flip |
| `codeExecServer` | built-ins only | **every `servers.changed`** | **NO** | **Never** — `buildCodeExecModeTools` does not iterate upstreams |
| `directServer` (`/mcp/all`) | the whole fleet | **every `servers.changed`** | **NO** | **Yes** — this is the real one |

`RefreshDirectModeToolsOnSerializationChange` IS guarded by a drift check, but that guard
only covers the `config.reloaded` path. The `servers.changed` path calls the **unguarded**
`RefreshDirectModeTools` (internal/server/server.go:554).

### What emits `servers.changed` — 17 sites

`config hot-reload`, `sync`, `restart` (x2), `enable_toggle`, `bulk_enable_toggle`,
`quarantine_toggle`, `tools_approved`, `tools_blocked`, `tools_changed`, `tools_indexed`,
`server_connected`, `server_disconnected`, `server_state_changed`, `oauth_logout`,
`oauth_logout_all`.

`server_connected` / `server_disconnected` / `server_state_changed` are the ones that
matter: they fire on ordinary connection churn, including every reconnect. Coalescing is
only a **50 ms** window (`newServersChangedCoalescer(rt, 50*time.Millisecond)`), which
collapses simultaneous bursts and does nothing for a reconnect loop retrying on a seconds
timescale.

### Measured on one ordinary startup (4 servers, 26 tools)

| Surface | Refreshes | Distinct tool_counts | No-op refreshes |
|---|---|---|---|
| `codeExecServer` | 9 | 1 (always 7) | **9 of 9** |
| `directServer` | 10 | 4 (1, 3, 18, 27) | at least 6 of 10 |

Nine broadcasts telling every client the code-execution tool list changed, on a list that
is structurally incapable of changing.

## The correction — and it undoes part of my own claim

I asserted that gratuitous `list_changed` is itself a caching hazard. **It is not**, and
the reason matters:

1. `list_changed` prompts a compliant client to re-fetch `tools/list`.
2. mcp-go sorts that listing (`sort.Strings(toolNames)`, server.go:1782), so an unchanged
   tool set returns a **byte-identical** payload.
3. The client re-sends an identical `tools` block, and the cache **still hits** — the
   invalidation trigger is a changed tool *definition*, not a notification.

So the code-exec surface's nine broadcasts are wasted client work and log noise, not lost
cache. They remain worth fixing (a client that re-fetches on every upstream flap is paying
round-trips for nothing) but they are a **correctness/efficiency** defect, not a cost one.

The real invalidation is the `directServer` column: 1 -> 3 -> 18 -> 27 tools during one
startup is three genuine tool-definition changes, each of which discards the entire cache
including system and all prior messages. Any later connect, disconnect, enable toggle,
quarantine change or `set_profile` does the same.

## The bigger correction — caching's actual effect on break-even

I previously said caching pushes the crossover "further right, possibly much further."
**That was wrong, and the error was worth catching.**

Both arms cache the same way. Over n turns with a stable menu:

```
cost(menu) = menu x (1.25 + 0.1 x (n-1))
```

The saving is `(B - M)` and BOTH terms carry that identical factor, so it divides out of
the crossing condition. **The break-even fleet size is roughly invariant under caching.**
The chart's 67-tool / 10-server crossover survives.

What caching does instead is shrink the **absolute** saving. In a cached steady state a
token is billed at 0.1x, so a headline "saves N tokens" is worth about a tenth of its face
value in money. Every figure this spec publishes is a raw token count, and none of them
say that.

Two second-order effects that do move the line, both against the proxy:

1. **Discovery responses are per-turn cache WRITES.** Each `retrieve_tools` result is new
   content appended to `messages`: written at 1.25x, only read at 0.1x on later turns. The
   baseline pays nothing extra per turn. So the proxy's marginal per-turn cost is higher,
   and a session that re-discovers repeatedly erodes its own advantage.
2. **Direct-mode invalidation is unbounded.** One tool-set change mid-session re-writes the
   entire prefix at 1.25x — including the conversation, which by then may dwarf the menu.
   This is the only mechanism found that can plausibly flip the sign, and it is confined to
   direct mode with a churning fleet.

## Recommendations

1. **Guard `RefreshCodeExecModeTools`** — it cannot change on `servers.changed`, so do not
   call it there, or compare content before `SetTools`. Removes 9 of 9 spurious broadcasts.
2. **Give `RefreshDirectModeTools` the same drift check** its serialization sibling already
   has. At least 6 of 10 startup rebuilds were no-ops; a content hash would drop them.
3. **Publish savings in money as well as tokens**, or state the cache multiplier beside the
   token figure. A raw token saving overstates value ~10x in a cached steady state.
4. **Do not measure step 2 without fixing 1 and 2 first** — otherwise the experiment prices
   avoidable churn as if it were inherent.

## What this did NOT establish

- No provider was called. The amortisation arithmetic above is a model, not a measurement.
- OpenAI and Gemini cache automatically under different rules; only Anthropic's numbers are
  pinned here.
- Whether real clients (Claude Code, Cursor, VS Code) actually re-fetch on `list_changed`
  and whether they re-serialize deterministically is **unverified** — step 3 above assumes
  a compliant, deterministic client, and a client that reorders or re-formats would break
  the byte-identity argument the correction rests on.

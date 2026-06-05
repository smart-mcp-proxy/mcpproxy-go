# Feature Specification: Configurable tool-discovery & health-check intervals

**Feature Branch**: `074-discovery-intervals`
**Created**: 2026-06-05
**Status**: Draft
**Input**: GitHub issue [#608](https://github.com/smart-mcp-proxy/mcpproxy-go/issues/608) — "Configure timing for tools discovery"

## Background

A user reports that mcpproxy sends a `ListToolsRequest` to each mounted upstream MCP server roughly every minute, with no setting to tune it, and notes the tool-list payload can be very large.

Two independent mechanisms generate this traffic today, both with hardcoded intervals:

1. A periodic **liveness probe** (~every 30s per connected, non-Docker server) that issues a full `tools/list` purely to confirm the connection is alive.
2. A periodic **tool-discovery sweep** (~every 5 minutes) that re-lists tools from every server to rebuild the search index.

Using the heavyweight `tools/list` as a liveness check is the dominant, and least necessary, source of the traffic the user sees. mcpproxy already receives push notifications (`notifications/tools/list_changed`) and re-discovers reactively, so the periodic sweep is a fallback rather than the primary freshness mechanism.

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Lightweight liveness probe instead of full tool list (Priority: P1)

An operator has an upstream server that returns a large tool catalog. They observe heavy periodic `tools/list` requests in that server's logs even when nobody is using it. After upgrading, the periodic liveness traffic becomes negligible because mcpproxy probes liveness with a standard empty-payload check instead of re-listing every tool.

**Why this priority**: This directly removes the bulk of the unwanted traffic the issue reporter sees, independent of any new configuration. It delivers value even if no settings are touched.

**Independent Test**: Connect an upstream server, idle the proxy, and confirm the upstream receives lightweight liveness pings on the health-check cadence and no periodic `tools/list` other than the (separately configurable) discovery sweep.

**Acceptance Scenarios**:

1. **Given** a connected non-Docker upstream and no user activity, **When** a health-check cycle fires, **Then** the upstream receives a `ping` request and does **not** receive a `tools/list` request from the health check.
2. **Given** an upstream whose transport has died, **When** a health-check cycle fires, **Then** the dead connection is detected and the server transitions toward reconnection, as before.
3. **Given** tool definitions change on an upstream that supports `listChanged`, **When** the change occurs, **Then** mcpproxy still re-discovers tools reactively (behaviour unchanged).

---

### User Story 2 - Configure the discovery & health-check intervals globally (Priority: P1)

An operator wants to reduce background traffic further (or, on a flaky network, probe more often). They open Settings and set a global health-check interval and a global tool-discovery interval. The new cadence takes effect without a full restart where possible.

**Why this priority**: This is the literal request in issue #608 — a tunable interval with a sensible default.

**Independent Test**: Set each interval via the Web UI / macOS app, confirm it persists and the background loops adopt the new cadence; set an out-of-range value and confirm it is rejected with a clear message.

**Acceptance Scenarios**:

1. **Given** the global health-check interval is unset, **When** the proxy runs, **Then** the effective interval is the built-in default (30s) and current behaviour is preserved.
2. **Given** the global tool-discovery interval is unset, **When** the proxy runs, **Then** the effective interval is the built-in default (5m).
3. **Given** an operator sets the health-check interval below the minimum (e.g. 2s) or above the maximum (e.g. 2h), **When** they save, **Then** the change is rejected with an explanatory validation error and the previous value is retained.
4. **Given** an operator sets either interval to `0s`, **When** they save, **Then** the corresponding background loop is disabled (no periodic probe / no periodic sweep) and this is accepted as a valid, intentional choice.

---

### User Story 3 - Override the intervals per server (Priority: P2)

An operator has one chatty upstream they want probed rarely (or never) while keeping defaults for the rest. They set an override on just that server; other servers are unaffected.

**Why this priority**: The issue explicitly suggests "a default and an override for single servers." It is valuable but secondary to the global knob, and the dedicated per-server form control can follow later.

**Independent Test**: Set a per-server override (via config / API), confirm only that server uses the overridden cadence and the rest inherit the global default; clear the override and confirm it reverts to inheriting the global value.

**Acceptance Scenarios**:

1. **Given** a per-server override is set, **When** the proxy resolves that server's interval, **Then** the override wins over the global value.
2. **Given** no per-server override, **When** the proxy resolves that server's interval, **Then** it inherits the global value (or the built-in default if the global is also unset).
3. **Given** a per-server override of `0s`, **When** resolved, **Then** that server's corresponding loop is disabled while other servers are unaffected.

---

### Edge Cases

- **Disabling discovery with a non-`listChanged` server**: if periodic discovery is disabled (`0s`) and a server does not support `listChanged`, tool changes are picked up only at connect time or on manual refresh. The settings help text MUST warn about this trade-off; the configuration is still permitted.
- **Hot reload**: changing an interval at runtime should take effect on the next cycle without requiring the operator to restart the whole proxy, where feasible.
- **Defaults unchanged on upgrade**: an existing config that does not mention the new keys MUST behave exactly as before the change.
- **Disabled health check**: with the probe disabled, a dead transport is detected lazily (on the next real tool call or discovery sweep) rather than proactively — acceptable and documented.

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: The periodic liveness probe MUST use the MCP-standard lightweight liveness check (`ping`) rather than a full tool listing.
- **FR-002**: The lightweight probe MUST still detect dead connections and drive reconnection equivalently to today's behaviour.
- **FR-003**: Reactive tool discovery via `notifications/tools/list_changed` MUST continue to work unchanged.
- **FR-004**: The system MUST expose a **global** health-check interval and a **global** tool-discovery interval, expressed as human-readable durations (e.g. `30s`, `5m`).
- **FR-005**: The system MUST expose an **optional per-server override** for each of the two intervals.
- **FR-006**: Resolution order MUST be: per-server override (if set) → global value (if set) → built-in default. A resolved value of zero MUST disable the corresponding loop.
- **FR-007**: Unset (absent) MUST be distinguishable from an explicit zero ("disabled"), so operators can intentionally turn a loop off.
- **FR-008**: The system MUST validate both intervals through the single authoritative config-validation path used by the file, the REST config update, and the raw-config editor. Health-check interval: `0s` (disabled) or within `[5s, 1h]`. Tool-discovery interval: `0s` (disabled) or within `[30s, 24h]`. Out-of-range values MUST be rejected with a clear, human-readable error.
- **FR-009**: Built-in defaults MUST be 30s (health check) and 5m (tool discovery) so existing deployments behave identically when the keys are unset.
- **FR-010**: Both global intervals MUST be editable in the Web UI settings and in the macOS app settings, using the existing duration input control, grouped under a clearly named section, with help text explaining `0s = disabled` and the discovery/`listChanged` trade-off.
- **FR-011**: Per-server overrides MUST be expressible in configuration and via the API in this iteration; a dedicated per-server form control MAY be deferred to a follow-up.
- **FR-012**: A runtime interval change SHOULD take effect on the next cycle without a full restart where feasible; otherwise the UI MUST indicate a restart is required.
- **FR-013**: User-facing documentation MUST describe both keys, their defaults, ranges, the `0s = disabled` semantics, and the per-server override.

### Key Entities

- **Global discovery settings**: two optional duration values (health-check interval, tool-discovery interval) on the top-level configuration, each capable of three states: unset, disabled, or a positive duration.
- **Per-server discovery override**: the same two optional duration values attached to an individual server entry, each capable of the same three states, where unset means "inherit global."

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: With default settings and an idle proxy, an upstream server receives **zero** periodic `tools/list` requests from the liveness probe (only the lightweight liveness check), eliminating the large recurring payload reported in #608.
- **SC-002**: An operator can change either interval and observe the new cadence take effect within one cycle, without editing files by hand.
- **SC-003**: Setting either interval to `0s` stops the corresponding background traffic entirely, confirmable from upstream logs / activity.
- **SC-004**: Out-of-range interval values are rejected 100% of the time with an actionable error, in every entry path (config file, REST, raw editor).
- **SC-005**: An existing configuration that does not set the new keys exhibits the same background-traffic behaviour as before the change (no regression for current users).
- **SC-006**: A per-server override changes only that server's cadence, leaving all other servers on the global/default cadence.

## Assumptions

- The underlying MCP client library supports the standard `ping` request (verified available in the pinned library version).
- The dominant traffic the reporter observed is the liveness probe re-listing tools; the discovery sweep is secondary. Both are addressed.
- Operators interact with settings primarily via the Web UI and macOS app; per-server overrides are acceptable to ship as config/API-only initially, with a form control to follow.
- The tool approval/hash mechanism is independent of these intervals and MUST remain unaffected.

## Commit Message Conventions *(mandatory)*

- Use `Related #608` (do **not** use auto-closing keywords; the issue is closed manually after verification).
- Do **not** add Claude co-authorship trailers. The Paperclip co-author trailer required by the executing agent's environment is the only permitted trailer.
- Conventional-commit style, e.g. `feat(config): configurable discovery & health-check intervals`.

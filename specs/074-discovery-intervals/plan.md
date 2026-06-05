# Implementation Plan: Configurable tool-discovery & health-check intervals

**Branch**: `074-discovery-intervals` · **Spec**: [spec.md](./spec.md) · **Issue**: #608

## Approach summary

Two changes, shippable as one PR (Personal edition; must not break the `server` build):

1. **Ping-based liveness** — replace the `tools/list` liveness probe with the MCP-standard `ping`.
2. **Configurable intervals** — global defaults + optional per-server overrides for both the health-check and tool-discovery loops, using a pointer-Duration tri-state (unset / `0s`-disabled / positive).

## Technical context

- Language: Go 1.24; Web UI TypeScript/Vue 3.5; macOS Swift 5.9.
- MCP lib: `github.com/mark3labs/mcp-go v0.54.1` exposes `client.Ping(ctx) error` (verified).
- Reactive discovery via `notifications/tools/list_changed` already exists (`internal/upstream/core/connection_lifecycle.go`) — unchanged.

## Design

### A. Ping liveness probe

- `internal/upstream/core/client.go`: add thin wrapper
  ```go
  func (c *Client) Ping(ctx context.Context) error { return c.client.Ping(ctx) }
  ```
- `internal/upstream/managed/client.go` `performHealthCheck` (~L844-889): replace the `acquireListToolsContext` + `coreClient.ListTools` + `publishListToolsResult` block with a 5s-timeout `mc.coreClient.Ping(ctx)`. Keep the existing error classification: only `isConnectionError(err)` → `recordHealthCheckFailure`/`SetError`; transient/timeout tolerated. On success → `recordHealthCheckSuccess`. The coalescing machinery (`acquireListToolsContext`/`publishListToolsResult`) remains for real `ListTools` callers; the health path no longer participates.

### B. Config schema (pointer-Duration tri-state)

- `internal/config/config.go`:
  - `Config` (global): add
    ```go
    HealthCheckInterval   *Duration `json:"health_check_interval,omitempty" mapstructure:"health-check-interval" swaggertype:"string"`
    ToolDiscoveryInterval *Duration `json:"tool_discovery_interval,omitempty" mapstructure:"tool-discovery-interval" swaggertype:"string"`
    ```
  - `ServerConfig` (per-server): add the same two keys (mapstructure underscore form `health_check_interval` / `tool_discovery_interval` to match neighbouring per-server keys).
  - Built-in defaults (constants): `defaultHealthCheckInterval = 30 * time.Second`, `defaultToolDiscoveryInterval = 5 * time.Minute`. **Do not** populate them in `DefaultConfig()` as non-nil — keep nil = inherit so absence means default (preserves SC-005). Defaults live only in the resolver.
  - Resolvers:
    ```go
    func (c *Config) ResolveHealthCheckInterval(sc *ServerConfig) time.Duration   // per-server → global → 30s
    func (c *Config) ResolveToolDiscoveryInterval(sc *ServerConfig) time.Duration // per-server → global → 5m
    ```
    A non-nil pointer wins at each level (including a pointer to 0 = disabled). Resolved `<= 0` ⇒ disabled.
  - Validation in `Validate()` (the one path file/REST/raw-editor share): for each non-nil pointer (global and per-server), enforce: health-check ∈ {0} ∪ [5s,1h]; tool-discovery ∈ {0} ∪ [30s,24h]. Clear messages, e.g. `health_check_interval must be 0s (disabled) or between 5s and 1h, got 2s`.

### C. Wire intervals into the loops

- `internal/upstream/managed/client.go` `backgroundHealthCheck` (~L765): replace the fixed `time.NewTicker(30s)` with a resettable `time.Timer`. Each cycle, resolve `globalConfig.ResolveHealthCheckInterval(mc.GetConfig())`. If `<= 0`, do not probe (sleep/wait on stop only, re-checking on the next reload signal). The managed client already holds `mc.globalConfig` and `mc.GetConfig()`.
- `internal/runtime/lifecycle.go` `backgroundToolIndexing` (~L235): replace fixed `5*time.Minute` ticker with a resettable timer reading `r.Config().ResolveToolDiscoveryInterval(nil)` (global only for the sweep). If `<= 0`, skip the periodic sweep (initial connect-time discovery + reactive `list_changed` still run).
  - Per-server discovery interval semantics: the index sweep is global; per-server discovery override is honoured at the schema/API level for forward-compat but the global sweep cadence governs the periodic rebuild. Document this nuance. (Per-server discovery scheduling is out of scope for this PR; per-server **health-check** override is fully wired.)

### D. Storage field-coverage canary

- Adding fields to `ServerConfig` trips `TestSaveServerSyncFieldCoverage` (internal/storage). Either copy the two fields into `UpstreamRecord` (preferred — they round-trip) or add them to the explicit-exclusion list with a comment. Run the **full** suite, not a narrow `-run`.

### E. UI (declarative catalogs, shared JSON keys)

- Web: `frontend/src/views/settings/fields.ts` — new `ADVANCED_ACCORDIONS` entry `discovery` ("Tool discovery & health checks") after `activity`, with two `control: 'duration'` fields + help text (`"0s" disables`; discovery-disable relies on connect + `list_changed`).
- macOS: `native/macos/MCPProxy/MCPProxy/Settings/SettingsCatalog.swift` — mirror `ConfigSection` with the same two keys, `control: .duration`.
- Per-server form control: **deferred** (schema works via Raw-JSON editor + API).

### F. Docs + swagger

- `docs/configuration.md`: document both keys, defaults, ranges, `0s=disabled`, per-server override, and the ping change.
- Let the swagger pre-push hook regenerate `oas/swagger.yaml`.

## Invariants / guardrails

- `calculateToolApprovalHash` MUST be untouched (run the approval-hash stability test).
- Personal **and** `-tags server` builds must compile.
- Defaults preserve current behaviour when keys are unset.

## Verification

- Unit: resolver precedence table; validation bounds (incl. `0s` accepted, `2s`/`2h` rejected, per-server + global); health-path-uses-ping (assert `Ping` called, `ListTools` not called by the health loop — via a fake/mock core client or interface seam).
- Race: `go test -race ./internal/upstream/... ./internal/runtime/...`.
- Full suite + `./scripts/test-api-e2e.sh` (covers the storage canary + e2e).
- Manual/Playwright: settings appear, validate, persist in Web UI; macOS catalog renders (declarative — low risk).
- Behavioural: idle proxy against a test upstream → confirm `ping` traffic, no health-loop `tools/list` (QA gate).

## Out of scope / follow-ups

- Dedicated per-server form control in the Web UI / macOS app.
- Per-server *discovery-sweep* scheduling (only per-server *health-check* is wired this PR).
- Capability-aware auto-tuning (skip/stretch the sweep for `listChanged` servers).

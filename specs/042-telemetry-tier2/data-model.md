# Data Model: Telemetry Tier 2

This document describes the entities introduced or extended by Tier 2.

## Entity 1: HeartbeatPayloadV2

The wire format sent to the telemetry endpoint. Purely additive over v1.

### Fields (v1, preserved)

| Field | Type | Source | Description |
|---|---|---|---|
| `anonymous_id` | string (UUIDv4) | config `telemetry.anonymous_id` | Per-install random ID, rotated annually |
| `version` | string | build-time | Binary version |
| `edition` | string | build-time | `personal` or `server` |
| `os` | string | runtime | `darwin`, `linux`, `windows` |
| `arch` | string | runtime | `amd64`, `arm64` |
| `go_version` | string | runtime | e.g. `go1.24.10` |
| `server_count` | int | runtime stats | Total upstream servers configured |
| `connected_server_count` | int | runtime stats | Currently connected upstream servers |
| `tool_count` | int | runtime stats | Total tools indexed |
| `uptime_hours` | int | runtime | Hours since process start |
| `routing_mode` | string | config | `dynamic` or `static` |
| `quarantine_enabled` | bool | config | Global quarantine flag |
| `timestamp` | string (RFC3339) | render time | When this payload was rendered |

### Fields (Tier 2, new)

| Field | Type | Source | Description |
|---|---|---|---|
| `schema_version` | int | constant `2` | Heartbeat schema version. v1 payloads have no such field. |
| `surface_requests` | object | counter registry | `{mcp: int, cli: int, webui: int, tray: int, unknown: int}` |
| `builtin_tool_calls` | object | counter registry | Map of built-in tool name → call count. Keys are a fixed enum. Zero entries omitted. |
| `upstream_tool_call_count_bucket` | string | counter registry | Bucketed total of upstream tool calls. One of: `"0"`, `"1-10"`, `"11-100"`, `"101-1000"`, `"1000+"` |
| `rest_endpoint_calls` | object | counter registry | `{"<METHOD> <template>": {"2xx": int, "4xx": int, ...}}`. Templates are Chi route patterns. The literal key `UNMATCHED` covers requests that didn't match any registered route. |
| `feature_flags` | object | config snapshot | See FeatureFlagSnapshot below. |
| `last_startup_outcome` | string | persisted config | One of: `"success"`, `"port_conflict"`, `"db_locked"`, `"config_error"`, `"permission_error"`, `"other_error"`, or empty if never recorded |
| `previous_version` | string | persisted config | The `last_reported_version` value at render time. Empty on first heartbeat after install. |
| `current_version` | string | build-time | Same value as `version`, included explicitly for upgrade analysis |
| `error_category_counts` | object | counter registry | Map of `ErrorCategory` enum string → count. Categories with zero counts omitted. |
| `doctor_checks` | object | counter registry | `{"<check_name>": {"pass": int, "fail": int}}`. Empty `{}` if doctor was never run since startup or last flush. |
| `anonymous_id_created_at` | string (RFC3339) | persisted config | When the current `anonymous_id` was generated. Used for age verification by the receiver. |

### Validation rules

- `surface_requests` keys MUST be exactly `{mcp, cli, webui, tray, unknown}`. No other keys permitted.
- `builtin_tool_calls` keys MUST be a subset of: `{retrieve_tools, call_tool_read, call_tool_write, call_tool_destructive, upstream_servers, quarantine_security, code_execution}`.
- `upstream_tool_call_count_bucket` MUST match the regex `^(0|1-10|11-100|101-1000|1000\+)$`.
- `rest_endpoint_calls.*` keys MUST match `^(UNMATCHED|GET|POST|PUT|DELETE|PATCH) /[^?# ]*$`. The path part comes from Chi templates and never contains query strings or fragments.
- `rest_endpoint_calls.*.*` (inner keys) MUST match `^[2345]xx$`.
- `error_category_counts` keys MUST be one of the `ErrorCategory` enum values defined in code.
- `feature_flags.oauth_provider_types` MUST be a sorted, deduplicated list of strings from `{google, github, microsoft, generic}`.
- All counts MUST be non-negative integers.
- The serialized JSON payload MUST be ≤ 8 KB when fully populated.

### State transitions

The payload is stateless from its own perspective — it is rendered fresh every flush. The interesting state lives in the `CounterRegistry` and the persisted config fields. State transitions for those:

```text
Counter increment ─────────────► increment in registry
Snapshot() ──────────────────► payload built (registry NOT reset)
Service.send() ─────────────► HTTP POST
   ├── 2xx ─────────────► registry.Reset() + persist last_reported_version
   └── error ─────────► no reset, no persist (counters survive)
```

## Entity 2: CounterRegistry

In-memory aggregate, single instance per `telemetry.Service`. Thread-safe.

### Structure

```go
type CounterRegistry struct {
    // Atomic counters (no lock needed)
    surfaceCounts [5]atomic.Int64    // Indexed by Surface enum
    upstreamTotal atomic.Int64

    // Locked maps (RWMutex)
    mu              sync.RWMutex
    builtinCalls    map[string]int64
    restEndpoints   map[string]map[string]int64  // template → status class → count
    errorCategories map[ErrorCategory]int64
    doctorChecks    map[string]*DoctorCounts
}

type DoctorCounts struct {
    Pass int64
    Fail int64
}

type Surface int
const (
    SurfaceMCP Surface = iota
    SurfaceCLI
    SurfaceWebUI
    SurfaceTray
    SurfaceUnknown
    surfaceCount
)
```

### Methods

| Method | Purpose | Concurrency |
|---|---|---|
| `RecordSurface(s Surface)` | Increment surface counter | Lock-free atomic |
| `RecordBuiltinTool(name string)` | Increment built-in tool counter; rejects unknown names | RWMutex Write |
| `RecordUpstreamTool()` | Increment upstream total | Lock-free atomic |
| `RecordRESTRequest(method, template string, statusClass string)` | Increment endpoint counter | RWMutex Write |
| `RecordError(category ErrorCategory)` | Increment error counter; rejects unknown categories | RWMutex Write |
| `RecordDoctorRun(results []CheckResult)` | Bulk-update doctor counts | RWMutex Write |
| `Snapshot() RegistrySnapshot` | Build a coherent immutable snapshot for the heartbeat payload | RWMutex Read |
| `Reset()` | Zero all counters; called only after successful flush | RWMutex Write |

### Privacy invariants enforced by the registry

- `RecordBuiltinTool` checks the name against the fixed enum and silently drops unknown values. Tests verify this with a fuzz-style assertion.
- `RecordError` rejects any `ErrorCategory` not in the typed enum at compile time (the function signature only accepts the type), and at runtime it also checks against the known set in case future code uses a literal cast.
- The registry has no method that accepts an arbitrary string for upstream tools. The only entry point is `RecordUpstreamTool()` which takes no arguments and only increments a counter.

## Entity 3: ErrorCategory

```go
type ErrorCategory string

const (
    ErrCatOAuthRefreshFailed     ErrorCategory = "oauth_refresh_failed"
    ErrCatOAuthTokenExpired      ErrorCategory = "oauth_token_expired"
    ErrCatUpstreamConnectTimeout ErrorCategory = "upstream_connect_timeout"
    ErrCatUpstreamConnectRefused ErrorCategory = "upstream_connect_refused"
    ErrCatUpstreamHandshakeFailed ErrorCategory = "upstream_handshake_failed"
    ErrCatToolQuarantineBlocked  ErrorCategory = "tool_quarantine_blocked"
    ErrCatDockerPullFailed       ErrorCategory = "docker_pull_failed"
    ErrCatDockerRunFailed        ErrorCategory = "docker_run_failed"
    ErrCatIndexRebuildFailed     ErrorCategory = "index_rebuild_failed"
    ErrCatConfigReloadFailed     ErrorCategory = "config_reload_failed"
    ErrCatSocketBindFailed       ErrorCategory = "socket_bind_failed"
)

var validErrorCategories = map[ErrorCategory]struct{}{
    ErrCatOAuthRefreshFailed: {}, ErrCatOAuthTokenExpired: {},
    ErrCatUpstreamConnectTimeout: {}, ErrCatUpstreamConnectRefused: {},
    ErrCatUpstreamHandshakeFailed: {},
    ErrCatToolQuarantineBlocked: {},
    ErrCatDockerPullFailed: {}, ErrCatDockerRunFailed: {},
    ErrCatIndexRebuildFailed: {}, ErrCatConfigReloadFailed: {},
    ErrCatSocketBindFailed: {},
}
```

### Adding a new category

1. Add a new `const` line.
2. Add it to `validErrorCategories`.
3. Wire the call site to `registry.RecordError(ErrCatNewThing)`.
4. Add a test that the new category appears in the rendered payload.

## Entity 4: FeatureFlagSnapshot

Built from `*config.Config` at heartbeat render time.

```go
type FeatureFlagSnapshot struct {
    EnableWebUI                bool     `json:"enable_web_ui"`
    EnableSocket               bool     `json:"enable_socket"`
    RequireMCPAuth             bool     `json:"require_mcp_auth"`
    EnableCodeExecution        bool     `json:"enable_code_execution"`
    QuarantineEnabled          bool     `json:"quarantine_enabled"`
    SensitiveDataDetectionEnabled bool  `json:"sensitive_data_detection_enabled"`
    OAuthProviderTypes         []string `json:"oauth_provider_types"`
}
```

### OAuth provider type derivation

For each upstream server with OAuth configured, classify by URL pattern:
- `accounts.google.com` → `google`
- `github.com` → `github`
- `login.microsoftonline.com` → `microsoft`
- Anything else → `generic`

The result is sorted, deduplicated, and emitted as `oauth_provider_types`. **No URLs, client IDs, tenant IDs, or scopes are included.** Only the four enum values.

## Entity 5: TelemetryConfig (extended)

```go
type TelemetryConfig struct {
    // v1 fields (preserved)
    Enabled            *bool  `json:"enabled,omitempty"`
    Endpoint           string `json:"endpoint,omitempty"`
    AnonymousID        string `json:"anonymous_id,omitempty"`

    // Tier 2 new fields (all persisted, all optional, all default-safe)
    AnonymousIDCreatedAt string `json:"anonymous_id_created_at,omitempty"` // RFC3339
    LastReportedVersion  string `json:"last_reported_version,omitempty"`
    LastStartupOutcome   string `json:"last_startup_outcome,omitempty"`
    NoticeShown          bool   `json:"notice_shown,omitempty"`
}
```

### Migration from v1

Legacy installs with v1 telemetry config (no Tier 2 fields) are handled gracefully:
- Missing `anonymous_id_created_at` → set to `now` on first Snapshot, no rotation
- Missing `last_reported_version` → first heartbeat reports `previous_version=""`
- Missing `last_startup_outcome` → omitted from payload (not a hard requirement to have on first run)
- Missing `notice_shown` → first-run notice will print on next `mcpproxy serve` invocation

No explicit migration step is needed; default-zero behavior is correct.

## Persistence

All Tier 2 state lives in:
1. Existing config file (`~/.mcpproxy/mcp_config.json`) for the four new persisted fields above.
2. In-memory `CounterRegistry` for everything else.

**No new files. No new BBolt buckets. No new directories.**

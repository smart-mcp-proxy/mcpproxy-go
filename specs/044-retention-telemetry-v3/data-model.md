# Phase 1 Data Model: Retention Telemetry v3

**Feature**: 044-retention-telemetry-v3
**Date**: 2026-04-24

## Entities

### EnvKind (enum)

| Value | Meaning |
|-------|---------|
| `interactive` | Real human on desktop OS (macOS/Windows) OR Linux with DISPLAY/TTY. |
| `ci` | Any known CI runner env var set. |
| `cloud_ide` | Codespaces, Gitpod, Replit, StackBlitz, Daytona, Coder. |
| `container` | `/.dockerenv` or `/run/.containerenv` present OR `$container` set, with no CI/cloud-IDE markers. |
| `headless` | Linux with no DISPLAY/TTY and none of the above. |
| `unknown` | Classifier fell through all rules. |

Serialization: lowercase string. Invalid value = programming error; payload builder rejects.

### LaunchSource (enum)

| Value | Meaning |
|-------|---------|
| `installer` | `MCPPROXY_LAUNCHED_BY=installer` env var at startup. One-shot: cleared after first heartbeat. |
| `tray` | Tray socket handshake set a `launched_via=tray` flag. |
| `login_item` | OS launched process as a registered login item (parent = launchd/explorer.exe). |
| `cli` | stdin is a TTY and no other rule matched. |
| `unknown` | None of the above. |

### AutostartEnabled (tri-state)

- `true`: tray reports login item is registered and enabled.
- `false`: tray reports login item is not registered OR was explicitly disabled.
- `null` (JSON)/`nil` (Go): platform does not support the read (Linux) OR tray is not running.

### ActivationState (Go struct → JSON field `activation`)

```go
type ActivationState struct {
    FirstConnectedServerEver      bool     `json:"first_connected_server_ever"`
    FirstMCPClientEver            bool     `json:"first_mcp_client_ever"`
    FirstRetrieveToolsCallEver    bool     `json:"first_retrieve_tools_call_ever"`
    MCPClientsSeenEver            []string `json:"mcp_clients_seen_ever"`        // cap 16, sanitized
    RetrieveToolsCalls24h         int      `json:"retrieve_tools_calls_24h"`     // raw int (not bucketed)
    EstimatedTokensSaved24hBucket string   `json:"estimated_tokens_saved_24h_bucket"`
    ConfiguredIDECount            int      `json:"configured_ide_count"`          // read from existing config-write tracker
}
```

**Validation rules**:
- Monotonic flags never transition from true → false.
- `MCPClientsSeenEver` deduplicated, max length 16, each entry sanitized per R7.
- `RetrieveToolsCalls24h` ≥ 0; window rolls every 24h at heartbeat time.
- `EstimatedTokensSaved24hBucket` is one of the 6 literal strings from FR-009.

### EnvMarkers (Go struct → JSON field `env_markers`)

```go
type EnvMarkers struct {
    HasCIEnv        bool `json:"has_ci_env"`
    HasCloudIDEEnv  bool `json:"has_cloud_ide_env"`
    IsContainer     bool `json:"is_container"`
    HasTTY          bool `json:"has_tty"`
    HasDisplay      bool `json:"has_display"`
}
```

**Validation rules**: all fields MUST be Go `bool` (enforced at struct level). Payload builder self-check re-asserts in serialized JSON that each field is `true` or `false` — never a string, number, or null.

### HeartbeatPayload (extended, v3)

Adds to the existing `telemetry.HeartbeatPayload` struct:

```go
// Spec 044 additions (schema_version stays at 3 since 042 already bumped it)
EnvKind          string           `json:"env_kind,omitempty"`
LaunchSource     string           `json:"launch_source,omitempty"`
AutostartEnabled *bool            `json:"autostart_enabled"` // tri-state: pointer so null is distinguishable
Activation       *ActivationState `json:"activation,omitempty"`
EnvMarkers       *EnvMarkers      `json:"env_markers,omitempty"`
```

`AutostartEnabled` uses a pointer specifically to allow JSON `null`. Other optional fields use `omitempty`.

### ActivationBucket (BBolt schema)

Bucket name: `activation`

| Key | Value encoding | Notes |
|-----|----------------|-------|
| `first_connected_server_ever` | 1 byte: `0x00` false, `0x01` true | Monotonic once true. |
| `first_mcp_client_ever` | 1 byte | Monotonic. |
| `first_retrieve_tools_call_ever` | 1 byte | Monotonic. |
| `mcp_clients_seen_ever` | JSON `[]string`, max 16 elements | Bounded cardinality. |
| `retrieve_tools_calls_24h` | 16 bytes: `uint64 count` (8) + `int64 window_start_unix` (8) | Decays on heartbeat if >=24h elapsed. |
| `estimated_tokens_saved_24h` | 16 bytes: `uint64 count` + `int64 window_start_unix` | Bucketed at emit. |
| `installer_heartbeat_pending` | 1 byte | Set when `MCPPROXY_LAUNCHED_BY=installer` observed; cleared after first heartbeat. |

Missing key → default value (false / empty / 0). Missing bucket → all defaults.

## State transitions

### Monotonic flags

```
         add first server
false  --------------------->  true
         (never transitions back)
```

Similar diagrams for `first_mcp_client_ever` (on first MCP `initialize`) and `first_retrieve_tools_call_ever` (on first builtin `retrieve_tools` call).

### retrieve_tools_calls_24h window

```
window_start_unix = T0
  ...retrieve_tools calls increment count...
heartbeat at time T1:
  if T1 - window_start_unix >= 86400:
    emit count (raw int)
    reset: count = 0, window_start_unix = T1
  else:
    emit count (no reset)
```

### installer_heartbeat_pending

```
process starts with MCPPROXY_LAUNCHED_BY=installer
  → set installer_heartbeat_pending = true
next heartbeat:
  if installer_heartbeat_pending:
    launch_source = "installer"
    installer_heartbeat_pending = false  (cleared before HTTP POST)
  else:
    launch_source = detected at startup (tray/login_item/cli/unknown)
```

## Relationships

- `HeartbeatPayload.Activation` is populated from `ActivationBucket` at heartbeat build time.
- `HeartbeatPayload.EnvKind` / `EnvMarkers` are populated from the cached `DetectEnvKindOnce()` result.
- `HeartbeatPayload.AutostartEnabled` is populated from the tray socket's `/autostart` endpoint response, cached with 1h TTL in `telemetry.Service`.
- `HeartbeatPayload.LaunchSource` is populated from `DetectLaunchSourceOnce()` at startup, with the one-shot `installer` override per §Installer.

## Constraints

- No Go map with user-controlled keys in any of the new fields (prevents cardinality blowup / PII leakage via map keys).
- All enum fields serialize as fixed lowercase strings.
- `MCPClientsSeenEver` is a slice, not a map — order preserved, dedup on insert.
- BBolt bucket keys are fixed at compile time; no dynamic key names.

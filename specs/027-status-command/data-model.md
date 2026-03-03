# Data Model: Status Command

**Date**: 2026-03-02 | **Branch**: `027-status-command`

## Entities

### StatusInfo

Represents the collected status data displayed by the `mcpproxy status` command. Not persisted - assembled at query time from live data or config.

| Field | Type | Source (Daemon) | Source (Config-only) |
|-------|------|----------------|---------------------|
| State | string ("Running" / "Not running") | Socket availability check | Socket availability check |
| ListenAddr | string | `/api/v1/status` `listen_addr` | Config `listen` field |
| Uptime | duration | `/api/v1/status` `started_at` | N/A (omitted) |
| APIKey | string (masked or full) | Config file | Config file |
| WebUIURL | string | `/api/v1/info` `web_ui_url` | Constructed from config |
| ServerCount | int | `/api/v1/status` `upstream_stats` | N/A (omitted) |
| QuarantinedCount | int | `/api/v1/status` `upstream_stats` | N/A (omitted) |
| SocketPath | string | `/api/v1/info` `endpoints.socket` | Socket detection |
| ConfigPath | string | Config loader | Config loader |
| Version | string | `/api/v1/info` `version` | N/A (omitted) |

### State Transitions

None. StatusInfo is a read-only snapshot. The `--reset-key` flag mutates the config file but does not change StatusInfo state.

### Validation Rules

- API key masking: if `len(key) <= 8`, show `****`; otherwise `key[:4] + "****" + key[len(key)-4:]`
- Listen address URL construction: if starts with `:`, prefix with `127.0.0.1`
- Web UI URL: `http://{listenAddr}/ui/?apikey={apiKey}`

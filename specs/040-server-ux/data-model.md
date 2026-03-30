# Data Model: Add/Edit Server UX

## Entities

### ServerConfiguration (existing, modified)

| Field | Type | Mutable | Notes |
|-------|------|---------|-------|
| name | string | NO | Primary key, immutable after creation |
| protocol | string | YES | "stdio" or "http" (auto-detected variant) |
| url | string | YES | Required for http protocol |
| command | string | YES | Required for stdio protocol |
| args | []string | YES | Command arguments for stdio |
| env | map[string]string | YES | Environment variables |
| working_dir | string | YES | Working directory |
| enabled | bool | YES | Default true |
| quarantined | bool | YES | Default true for new servers |
| docker_isolation | bool | YES | Docker container isolation |
| skip_quarantine | bool | YES | Skip tool-level quarantine |

### ImportPreview (new, transient)

| Field | Type | Notes |
|-------|------|-------|
| name | string | Server name from config file |
| protocol | string | Detected protocol |
| exists | bool | Already exists in MCPProxy |
| selected | bool | User-toggled checkbox (client-side only) |
| skip_reason | string | Why it would be skipped (e.g., "already exists") |

### ConnectionTestResult (new, transient)

| Field | Type | Notes |
|-------|------|-------|
| phase | enum | saving, connecting, success, failure |
| error_message | string | Actual backend error on failure |
| tool_count | int | Number of tools discovered on success |

### ValidationError (new, transient, client-side only)

| Field | Type | Notes |
|-------|------|-------|
| field | string | Field identifier (name, url, command) |
| message | string | Human-readable error ("Server name is required") |

## State Transitions

### Add Server Flow
```
idle → submitting(saving) → submitting(connecting) → success | failure
failure → submitting(saving) [retry] | idle [save anyway completes]
```

### Edit Config Tab
```
readonly → editing → saving → readonly | editing(validation error)
editing → readonly [cancel]
```

### Import Flow
```
browsing → previewing(loading) → previewing(loaded) → importing → results
previewing(loaded) → browsing [cancel]
```

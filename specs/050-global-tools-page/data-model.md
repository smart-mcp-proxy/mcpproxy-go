# Phase 1 Data Model — Global Tools Page

No persisted schema changes. New in-memory/transport types only.

## Existing types reused (no change)

- `contracts.Tool` (`internal/contracts/types.go:185`) — already has every field needed:
  `Name`, `ServerName`, `Description`, `Schema`, `Usage int`, `LastUsed *time.Time`,
  `Annotations`, `ApprovalStatus`, `Disabled`, `ConfigDenied`.
  Risk indicator = derived from `Annotations` / operation-type classification already inferred for spec 018 tool variants.
- `storage.ActivityRecord` — fields used: `Type` (filter `tool_call`), `ServerName`, `ToolName`, `Timestamp`.
- `ToolApprovalRecord` / `ToolApprovalBucket` — read for `ApprovalStatus` + `Disabled` enrichment (existing `controller.GetToolApproval`).

## New: `storage.ToolUsageStat`

```go
type ToolUsageStat struct {
    Count    int
    LastUsed time.Time // zero value = never used in window
}
```

`AggregateToolUsage(since time.Time) (map[string]ToolUsageStat, error)` — map key is
`serverName + "\x00" + toolName` (NUL separator avoids collision with `:` in names).
Empty bucket → empty map, no error. Records outside `[since, now]` skipped. `tool_call`
type only (read/write/destructive variants all record as tool calls).

## New: `contracts.GlobalToolsResponse`

```go
type GlobalToolsStats struct {
    Total           int `json:"total"`
    Enabled         int `json:"enabled"`
    Disabled        int `json:"disabled"`
    PendingApproval int `json:"pending_approval"`
}

type GlobalToolsResponse struct {
    Tools  []contracts.Tool  `json:"tools"`
    Stats  GlobalToolsStats  `json:"stats"`
    // Partial is set when one or more servers could not be read; the list
    // still contains every tool that could be gathered (spec edge case).
    Partial      bool     `json:"partial,omitempty"`
    FailedServers []string `json:"failed_servers,omitempty"`
}
```

### Derivation rules

- `enabled` (per tool, presented value) = `!Disabled && !ConfigDenied`. The JSON keeps the
  raw `disabled` / `config_denied` flags so the UI can distinguish user-disabled vs
  config-denied; `enabled` is computed client/CLI-side from them (no ambiguous third state).
- `Stats.Total` = len(tools). `Stats.Disabled` = count where `Disabled || ConfigDenied`.
  `Stats.Enabled` = Total − Disabled. `Stats.PendingApproval` = count where
  `ApprovalStatus == pending || changed`.
- `Usage` / `LastUsed` filled from `AggregateToolUsage`; absent key → `Usage=0`, `LastUsed=nil`.

## State transitions

Tools have no new lifecycle. Batch enable/disable simply flips the existing per-tool
`Disabled` flag via `POST /servers/{id}/tools/{tool}/enabled`; `ConfigDenied` tools reject
the flip (existing server behavior) and surface as a per-target failure.

## Entity relationships

```
GlobalToolsResponse 1—* Tool
Tool *—1 Server (by ServerName; server may be disabled — tool still listed)
Tool 0..1—1 ToolApprovalRecord (enrichment; absent = approved/never-toggled)
Tool 0..1—1 ToolUsageStat (from activity aggregation; absent = never used in window)
```

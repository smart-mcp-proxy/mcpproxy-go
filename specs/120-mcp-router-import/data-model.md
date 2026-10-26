# Data Model: MCP Router Importer

No new persisted state. All types are in-memory, in `internal/mcprouter`.

## Input

| Type | Fields | Notes |
|------|--------|-------|
| `Input` | `Kind` (`data_dir`\|`database`\|`export_file`), `Root`, `Files []string` | Output of `Locate`. `Files` are already containment-checked (plan D3). |
| `Workspace` | `ID`, `Name`, `Type` (`local`\|`remote`), `DBPath`, `Order` | `remote` workspaces have no rows; counted as "not imported". Default workspace (`local-default`) sorts first. |

## Source (immutable, one per `servers` row or export entry)

| Field | Source column | Missing-column default |
|-------|---------------|------------------------|
| `Workspace` | workspace id | `local-default` |
| `Name` | `name` | required, else row failed |
| `Kind` | `server_type` | `local` |
| `Command`, `Args` | `command`, `args` (JSON) | empty |
| `Env` | `env` (JSON object, plaintext) | `{}` |
| `RemoteURL` | `remote_url` | empty |
| `Bearer` | `bearer_token` | none; `BearerState` = `none`\|`plain`\|`encrypted` (prefix `mcpr-secret:v1:`) |
| `Disabled` | `disabled` | false |
| `AutoStart`, `ProjectID`, `Description`, `Version`, `Verification` | respective | unset (reported as not carried over when set) |
| `Params` | `input_params` (JSON) | none |
| `ToolOff []string` | keys of `tool_permissions` with `false` | none |
| `FromExport bool` | — | export entries lack Disabled/ToolOff/Bearer/Kind info |

## Plan (output of `BuildPlan`, pure, no side effects)

| Type | Fields |
|------|--------|
| `Plan` | `Input`, `Items []Item`, `NotImported` (workflows, hooks, skills, logs, cloud workspaces: booleans/counts from table presence only), `Clients []ClientAdvice`, `Warnings []string` |
| `Item` | `Source`, `Class`, `Reason`, `Server *config.ServerConfig` (nil unless addable), `Secrets []SecretPlan`, `NotCarried []string` (field names), `Differs []string` (conflict, field names only) |
| `Class` | `to_import`, `needs_configuration`, `already_present`, `conflict`, `collision`, `unmappable`, `skipped_self`, `filtered_out` |
| `SecretPlan` | `Field` (`env:NAME`\|`header:NAME`\|`arg:INDEX`), `Handling` (`keyring`\|`inline`\|`missing`\|`blocked_inline_credential`), `SecretLike bool` — never a value |
| `ClientAdvice` | `Client` (MCP Router id), `ConfigPath`, `Command` (`mcpproxy connect <id>` or empty), `Note` |

### State transitions of an Item

```text
read ─▶ resolve placeholders ─▶ validate (name, URL, command, `${`) ─▶ classify
   unmappable ◀──────────── any failure
   already_present / conflict / collision ◀── compare against existing servers (FR-016a)
   needs_configuration ◀── encrypted bearer
   to_import ◀── otherwise
apply: to_import|needs_configuration ─▶ added | failed(reason)
```

## Outcome (output of `Apply`)

`ServerOutcome{Name, Status(added|failed|uncertain), Reason, Enabled, Quarantined (read back, or `unknown`), OrphanRefs []string}`. `Classify(plan, outcomes)` gives the exit class; only `added` outcomes make a run telemetry-eligible.

`ItemView` is the only type that is ever rendered or serialised: it has no value-bearing fields; `Plan`/`Item` (which hold `*config.ServerConfig` with raw values) are never marshalled.

## Telemetry

`CounterRegistry.importCounts map[ImportSource]int64` (new), payload `import_source_counts: {"mcp-router": N}` — integer counts of runs, fixed key set, omitted when empty.

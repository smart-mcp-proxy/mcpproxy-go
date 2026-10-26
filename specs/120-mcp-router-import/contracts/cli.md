# CLI contract: `mcpproxy import`

```text
mcpproxy import --from <source> [--path <path>] [--dry-run] [--workspace <name-or-id>]
                [--yes] [--secrets-inline] [-o table|json|yaml]
```

| Flag | Meaning |
|------|---------|
| `--from` | required; `mcp-router` (unknown values: error listing supported sources) |
| `--path` | data directory, `mcprouter.db`, or an exported `mcp-servers-*.json`; default: auto-detect per OS |
| `--dry-run` | plan only: no add, no keyring write, no telemetry |
| `--workspace` | limit to one local workspace |
| `--yes` | skip the single confirmation prompt (never implies `--secrets-inline`) |
| `--secrets-inline` | allow secret-like values and inline credentials to stay in the config file when the keyring is unavailable or cannot hold them |
| `--help-json` | machine-readable help (existing mechanism) |

No `--no-quarantine`.

## Default locations checked (and printed when none exist)

| OS | Path |
|----|------|
| macOS | `~/Library/Application Support/MCP Router` |
| Windows | `%APPDATA%\MCP Router` |
| Linux | `~/.config/MCP Router` (best effort) |

## Output (`-o json`; the table shows the same content)

```json
{
  "source": "mcp-router",
  "input": {"kind": "data_dir", "path": "...", "workspaces": ["Local"]},
  "dry_run": true,
  "summary": {"total": 12, "to_import": 8, "needs_configuration": 1, "already_present": 1,
              "conflict": 0, "collision": 0, "unmappable": 2, "skipped": 0, "added": 0, "failed": 0},
  "servers": [
    {"name": "fs", "source_name": "fs", "workspace": "Local", "class": "to_import",
     "kind": "stdio", "summary": "npx -y @modelcontextprotocol/server-filesystem /tmp",
     "enabled": true, "hidden_tools": ["write_file"],
     "secrets": [{"field": "env:API_KEY", "secret_like": true, "handling": "keyring"}],
     "not_carried_over": ["auto_start", "project"], "warnings": []}
  ],
  "not_imported": {"workflows": true, "hooks": false, "skills": true, "logs": true, "cloud_workspaces": 0},
  "reconnect": [{"client": "cursor", "config_path": "~/.cursor/mcp.json", "command": "mcpproxy connect cursor"},
                {"client": "cline", "note": "MCPProxy has no Cline connector; re-point Cline's MCP settings manually"}],
  "warnings": [],
  "exit_class": "success"
}
```

Never present: any env/header/token/arg value, any `MCPR_TOKEN`. `servers[].summary` is the importer-owned summary (plan D4): fixed `‹redacted›` masks for sensitive argument values and for every URL query value and fragment; no tail bytes, no lengths.

## Exit codes

0 success · 20 partial · 21 nothing added · 22 source not found · 23 source unreadable/inconsistent · 24 refused by secret rules. (Plan D8.)

## Interaction

Interactive run: print the plan, then `Add N servers (X secrets to keyring, Y inline)? [y/N]`. Non-interactive without `--yes`: exit 24 with the message. After a real run: print the reconnect list and `mcpproxy upstream list`/review hints.

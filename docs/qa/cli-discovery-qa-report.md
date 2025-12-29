# CLI Discovery QA Report (2025-12-28)

## Environment
- Repo: /Users/user/repos/mcpproxy-go
- mcpproxy version: v0.11.5
- Network: enabled

## Scope
Server discovery: `search-servers`.
Docs referenced: docs/cli/command-reference.md.

## Test Cases

### TC1: Search servers help
Command:
```bash
./mcpproxy search-servers --help
```
Expected (docs/cli/command-reference.md):
- Flags for registry, search term, tag, limit, list-registries.
Actual:
- Help output matches the doc.
Result: Pass.

### TC2: List registries
Command:
```bash
./mcpproxy search-servers --list-registries
```
Expected:
- Table of known registries.
Actual:
- Table with registry ID, name, description.
Result: Pass.

### TC3: Search in registry
Command:
```bash
./mcpproxy search-servers --registry pulse --search obsidian --limit 3
```
Expected (docs/cli/command-reference.md):
- Table output by default (unless `-o json`).
Actual:
- Output is JSON array even without `-o json`.
Result: Partial pass; output format differs from docs.

## Issues / Observations
1) Default output for `search-servers` appears to be JSON array, not a table. Docs imply table by default.
2) No explicit `--output` example for `search-servers` in docs; consider documenting default output and how to force table/JSON/YAML.

## References
- docs/cli/command-reference.md

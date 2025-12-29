# CLI Core QA Report (2025-12-28)

## Environment
- Repo: /Users/user/repos/mcpproxy-go
- mcpproxy version: v0.11.5 (`./mcpproxy --version`)
- Daemon: running (CLI connected via socket)

## Scope
Core CLI utilities: `serve`, `doctor`, `completion`, and global behavior described in docs/cli/command-reference.md and docs/cli/management-commands.md.

## Test Cases

### TC1: Serve help
Command:
```bash
./mcpproxy serve --help
```
Expected (docs/cli/command-reference.md):
- Flags include listen address, socket toggle, read-only mode, management toggles.
Actual:
- Help lists flags matching the doc (listen, enable-socket, read-only, allow-server-add/remove, disable-management, enable-prompts, tool-response-limit).
Result: Pass.

### TC2: Doctor basic run
Command:
```bash
./mcpproxy doctor
```
Expected (docs/cli/management-commands.md):
- Health summary with upstream errors and remediation hints.
Actual:
- Command runs and reports multiple upstream auth errors plus remediation suggestions.
- Output format is a styled report (not a table); includes a note to use `--output=json` for more detail.
Result: Pass.

UX notes:
- Output uses emoji and heavy styling; no `--no-color` option for doctor specifically (global `--no-color` is not documented here). Consider a `--plain` or reuse global `--no-color` in doc.

### TC3: Completion script generation
Command:
```bash
./mcpproxy completion zsh | head -n 5
```
Expected (docs/cli/command-reference.md):
- Emits shell completion script to stdout.
Actual:
```
#compdef mcpproxy
compdef _mcpproxy mcpproxy

# zsh completion for mcpproxy                             -*- shell-script -*-
```
Result: Pass.

## Issues / Observations
1) Docs mention global flags but not `--no-color` (seen in root help). Consider adding to docs/cli/command-reference.md.
2) Doctor output uses styled/emoji status; no documented way to force plain output in docs (aside from `--output=json`).

## References
- docs/cli/command-reference.md
- docs/cli/management-commands.md

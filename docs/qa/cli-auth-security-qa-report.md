# CLI Auth and Security QA Report (2025-12-28)

## Environment
- Repo: /Users/user/repos/mcpproxy-go
- mcpproxy version: v0.11.5
- Daemon: running (socket mode)

## Scope
Authentication and security commands: `auth`, `secrets`, `trust-cert`.
Docs referenced: docs/cli/command-reference.md.

## Test Cases

### TC1: Auth help
Command:
```bash
./mcpproxy auth --help
```
Expected:
- Commands for login, logout, status.
Actual:
- Help output matches doc.
Result: Pass.

### TC2: Auth status (all)
Command:
```bash
./mcpproxy auth status --all
```
Expected (docs/cli/command-reference.md):
- Status per server with actions for missing/expired tokens.
Actual:
- Output includes per-server status and suggested login command.
- Some entries show "Connected" while still reporting OAuth-required errors in the same block.
Result: Partial pass; status and error lines appear inconsistent.

UX notes:
- Consider a single source of truth for health and error state in this view.

### TC3: Secrets list
Command:
```bash
./mcpproxy secrets list --json
```
Expected:
- Metadata for stored secrets without revealing values.
Actual:
- JSON array with secret names and keyring refs (names redacted in report).
Result: Pass (no secret values displayed).

UX notes:
- Listing secret names might still be sensitive; consider a `--masked-names` or `--count` option.

### TC4: Trust certificate help
Command:
```bash
./mcpproxy trust-cert --help
```
Expected:
- Instructions and flags for installing a certificate.
Actual:
- Help output matches doc.
Result: Pass.

Note: `trust-cert` was not executed to avoid modifying system trust stores.

## Issues / Observations
1) `auth status` mixes "Connected" with OAuth error messages in the same block, which is confusing.
2) Secrets list exposes secret key names by default; consider a mode that only shows counts or redacted names.

## References
- docs/cli/command-reference.md

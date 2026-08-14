# Contract: Stored Scripts API (Spec 097)

## MCP code_execution tool
- New optional string param `script`; `code` no longer schema-required.
- Exactly one of `code` | `script` (handler-enforced): both/neither → tool error explaining the rule.
- `script` value: validated name (never a path). Not found → error listing first 20 ok names alphabetically + total count (this IS the MCP discovery mechanism; registrations stay static, no list_changed).
- All other params (`input`, options, `language`) unchanged; explicit `language` contradicting the extension → error.
- Execution, budgets, scope enforcement, results: identical to inline.

## REST
- `POST /api/v1/code/exec`: body gains optional `script`; exactly-one-of violation → HTTP 400, envelope `{ok:false, error:{code:"INVALID_REQUEST", message}}` (the endpoint's existing shape); otherwise identical.
- `GET /api/v1/code/scripts` (NEW, read-only, API-key auth): `{success: true, data: {scripts: [{name, paths, status, reason?}], dir}}`.

## CLI
- `mcpproxy code exec --script <name>` — mutually exclusive with `--code`/`--file`; daemon mode sends the NAME (never content); standalone resolves locally from the same authority.
- `mcpproxy code scripts list` — daemon GET when running, else local; `-o json|yaml` supported.

## Authority
scripts dir = directory of the ACTIVE config file, passed into the server at construction on every surface (daemon: runtime config service; CLI standalone in-process server: --config or ~/.mcpproxy/mcp_config.json via a shared helper); config.GetConfigPath(DataDir) only as last-resort fallback when no path was provided. The handler is the only resolver for execution; the CLI resolves only for daemonless `code scripts list`. Never derived from --data-dir.

## Records
Activity/history keep storing the executed source as `code` (Spec 024 parity) plus additive `script: <name>`.

## Non-goals (v1)
No write/upload/delete surface; no config field; no dedicated MCP listing tool; no tools/list_changed.

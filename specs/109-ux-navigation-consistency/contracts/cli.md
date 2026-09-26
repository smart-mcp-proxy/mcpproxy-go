# Contract: CLI (Spec 109)

Every new command honours the global `-o table|json|yaml`, `--json`, `MCPPROXY_OUTPUT` and `--help-json`. JSON output equals the REST `data` object. Table columns use the terminology table's names. Exit codes follow `cmd/mcpproxy/exit_codes.go`.

## New groups and commands

| Command | Backs onto | Output (table) |
|---|---|---|
| `mcpproxy attention` | `GET /attention` | `#  KIND  SUBJECT  SUMMARY  FIX`; `All clear` when empty. Exit 0 in both cases (it is a report, not a check) |
| `mcpproxy review list` | `GET /review` | `SERVER  KIND  QUARANTINED  PENDING  CHANGED  TIERS  SCAN` |
| `mcpproxy review show <server>` | `GET /servers/{id}/review` | server header block, then `TOOL  TIER  APPROVAL  SCAN  DESCRIPTION (first line)`; `--full` prints full descriptions and diffs, indented under a `from the server, not verified:` label |
| `mcpproxy review approve <server> [--tools a,b \| --except a,b] [--force] [--yes]` | quarantined → `security/approve` (`block` = `--except`); trusted → `tools/approve` (`--tools` or all pending/changed) | `Approved <server> (12 tools, 2 blocked)`. A dangerous verdict without `--force` → exit 1 with the Spec 077 message |
| `mcpproxy review reject <server> [--tools a,b] [--yes]` | no `--tools` → `security/reject`; `--tools` → `tools/block` | `Rejected …` |
| `mcpproxy client list` | `GET /clients` | `CLIENT  STATE  LAST SEEN  SESSIONS  CALLS 24H  CONFIG PATH` (`display_path`). Spec 108 **appends** `CREDENTIAL  PROFILE  MODE  SOURCE  BLOCKED 24H` and `--profile`; it never removes or reorders these columns (`CONFIG PATH` stays) |
| `mcpproxy client show <id>` | `GET /clients/{id}` | detail block incl. `reload_hint`, full `config_path`, sessions |
| `mcpproxy catalog search <query> [--source id] [--tag t] [--limit n]` | `GET /catalog/search` | `TITLE  ID  PUBLISHER  ✓  POPULARITY  SOURCE`; unavailable sources on stderr as `note: source <id> unavailable: <reason>` |
| `mcpproxy catalog show <source>/<id>` | Spec 070 find | detail incl. required inputs |
| `mcpproxy catalog add <source>/<id> [--name] [--env K=V] [--secret-env K=V]` | Spec 070 add (+ secrets) | `Added <name> to MCPProxy (quarantined for review)` |

With no query (`mcpproxy catalog search ""` or `catalog search --browse`), the output prints the `official` and `popular` sections.

## Changed commands

| Command | Change |
|---|---|
| `mcpproxy status` | **declared table-text change** (JSON unchanged): the `MCP Endpoints` heading becomes `Endpoint & mode`, for the Clients-page terminology; listed in the 109-h PR body and the release notes. Line order is fixed so parallel PRs merge into one golden (`testdata/cli109/status.golden`): (1) first line after the header `Needs attention: N (run 'mcpproxy attention')` or `Needs attention: none` (109-d); (2) the existing summary lines (`Listen:` … `Config:`, unchanged, including `Routing:` for script compatibility); (3) `Token savings: ~N tokens/request` (suffixed ` (estimate)` when `estimated`) as a new summary line directly after `Servers:` (109-k); (4) the existing `MCP Endpoints` section renamed `Endpoint & mode`, with `Routing mode: <mode>` as its first line followed by the unchanged endpoint lines (no new data; JSON unchanged) (109-h) |
| `mcpproxy doctor` | first section `Needs attention (N)` from `GET /attention`; the existing sections follow under `Diagnostics`; the line "Found N issues that need attention" becomes `Diagnostics: N findings` |
| `mcpproxy upstream list` | **declared table-text change** (JSON additive only): `STATUS` = status label instead of the free-text summary (the summary stays in `-o json` `health.summary`; the GH #938 `· N held` suffix is kept); `ACTION` keeps its CLI-hint form, keyed on `actions[0]` (hint table in health-vocabulary.md#cli; unchanged strings for existing actions); new `--status <status>` (repeatable, enum from health-vocabulary.md). No `--legacy-columns` flag: scripts that parse the table should use `-o json`, which keeps every legacy field |
| `mcpproxy tools list` | new `TIER` column and `--tier read|write|destructive|unannotated` (`--risk` = deprecated alias, now filtering on the backend `tier`); `--approval` help: `approved, pending (new, needs review), changed (changed, needs review)` |
| `mcpproxy connect <client>` / `--all` | after each success: `Config: ~/.cursor/mcp.json` (the full path is in `-o json` as `config_path`; no new flag) and `Next: <reload_hint>` |
| `mcpproxy connect --list` | the existing `CONFIG PATH` column shows `display_path` (full path with `-o json`) |
| `mcpproxy upstream add` | new `--secret-env NAME=VALUE` and `--secret-header 'Name: value'` (repeatable): value → keyring entry `<server>-env-<name>` / `<server>-header-<name>` (FR-065 naming; a taken name gets `-2`, `-3`, …, never overwritten), config gets `${keyring:<that name>}`; refused with the keyring reason when the keyring is unavailable |
| `mcpproxy upstream approve <server> [tools…]` | unchanged behaviour; help: "Alias of `mcpproxy review approve <server> --tools …` for trusted servers" |
| `mcpproxy security approve|reject <server>` | unchanged behaviour; help names `mcpproxy review approve|reject <server>` |
| `mcpproxy tools approve|reject <server:tool>…` | unchanged behaviour; help names `mcpproxy review approve|reject <server> --tools …`; the "Mirrors the Web UI Block button" text becomes "Reject" |
| `mcpproxy registry search|add` | deprecated aliases: work as before and print `note: use 'mcpproxy catalog search|add'` on stderr |
| `mcpproxy registry list|add-source|edit|remove` | unchanged; help wording "catalog source" |
| `mcpproxy activity list|watch` | new `--view calls|system|all` (default `all`, research D11; `sessions` is deliberately absent: the CLI has no sessions listing (Spec 108 parity row 17) and `activity list --session` covers per-session history); `--from/--to` accepting RFC 3339 or relative `-24h`, `-7d` (aliases of `--start-time/--end-time` on `list`; client-side on `watch`, see below) |
| `mcpproxy activity summary|export` | `--from/--to`: aliases of `--start-time/--end-time` on `export`; on `summary` (endpoint takes only `period`) `--from` accepts exactly `-1h`, `-24h`, `-7d`, `-30d` → `--period`, anything else exits 1 (FR-075). On `watch`, `--from/--to` filter the streamed records client-side and `watch` exits once `--to` has passed |
| `mcpproxy activity export` | help cross-references `--format` (file format) and global `-o` (terminal rendering) (accepted contradiction C6) |

## Golden tests

`cmd/mcpproxy/*_test.go` goldens for each table above (`testdata/cli109/*.golden`), plus `--help-json` snapshots for `attention`, `review`, `client`, `catalog`. The parity test (109-m) reads `--help-json` and checks flag and column names against the terminology table.

# Research: MCP Router configuration format (E4, phase 1)

**Date**: 2026-10-20
**Source**: `github.com/mcp-router/mcp-router`, cloned read-only, `main` @ `7d6cd4e5e7e73ef54b938c29d14e0cab64850fae` (2026-09-18, "Remove remaining legacy purchase prompts from final release (#213)"). Last release tag: `v0.6.4` (final). Repo is archived; its README says new adoption is not recommended.

All paths below are relative to the MCP Router repo root unless prefixed `mcpproxy-go:`.

## Verdict

**Importable.** Server definitions live in a plain SQLite file with a small, stable, fully readable schema, and the app ships an export button. Three caveats shape the design:

1. The one-click **export is lossy**. It drops remote servers' URL, the disabled flag, tool toggles and tokens, and **writes remote servers as empty-command stdio entries**.
2. **Bearer tokens are encrypted** with the OS credential store (Electron `safeStorage`) and are not readable outside MCP Router. `env` values are **plaintext** in the database.
3. The format is **frozen**. The project is archived and `v0.6.4` is final, so a reader written against it will not drift. That removes the usual "unstable undocumented format" risk.

The format is undocumented in prose, but the schema is created by `CREATE TABLE` statements in source and covered by migrations, so every column and its meaning can be confirmed from code (see sources).

## 1. Where server definitions are stored

MCP Router is an Electron app (`apps/electron/package.json`: `"productName": "MCP Router"`, `"version": "0.6.4"`). All state sits under Electron `app.getPath("userData")`.

| OS | `userData` (default) | Source of the path |
|----|----------------------|--------------------|
| macOS | `~/Library/Application Support/MCP Router` | `docs/END_OF_SUPPORT.md` step 5 |
| Windows | `%APPDATA%\MCP Router` | `docs/END_OF_SUPPORT.md` step 5 |
| Linux | `~/.config/MCP Router` | Electron default for `productName`; **no official Linux build** (`apps/electron/forge.config.ts` makers are Squirrel, ZIP, DMG only; README lists "Windows and macOS"). Best effort only. |

Files inside `userData` that matter (from `apps/electron/src/main/modules/workspace/workspace.service.ts` and `infrastructure/shared-config-manager.ts`):

| File | What it holds |
|------|---------------|
| `mcprouter.db` | SQLite. Meta DB (`workspaces` table) **and**, in practice, the data DB of the default workspace `local-default`. `workspace.service.ts:38-39` opens it, then `:71-89` checks whether it exists. It always does by then (it was just created), so the default workspace always uses `databasePath: "mcprouter.db"`. |
| `workspaces/<workspaceId>/database.db` | SQLite data DB of each additional local workspace (`workspace.service.ts:205,442`). Located through `workspaces.localConfig` JSON (`{"databasePath": ...}`). |
| `shared-config.json` | App settings and `mcpApps.tokens` (the `MCPR_TOKEN` values issued to clients). Not server definitions. |
| `mcp-servers/dxt/<sha>/` | Extracted `.dxt` bundles (`mcp-server-manager/dxt-processor/dxt-processor.ts:17-41`). DXT servers' `command`/`args` point into this tree. |

The database runs in WAL mode (`infrastructure/database/sqlite-manager.ts`: `journal_mode = WAL`). A reader must either open read-only with the `-wal` file beside it, or copy the trio (`.db`, `-wal`, `-shm`) first. A running MCP Router holds the file open.

## 2. Schema (`servers` table)

`apps/electron/src/main/modules/mcp-server-manager/mcp-server-manager.repository.ts` (last changed `4c4642c`, 2026-06-24), `CREATE_TABLE_SQL`:

| Column | Type | Meaning | MCPProxy target |
|--------|------|---------|-----------------|
| `id` | TEXT PK | UUID, or `dxt-<name>-<uuid>` for DXT servers | not imported (identity is the name) |
| `name` | TEXT NOT NULL | display name, may contain spaces/Unicode; unique indexed non-uniquely (`idx_servers_name`) | `name`, sanitized |
| `command` | TEXT | stdio executable; `''`/NULL for remote servers | `command` |
| `args` | TEXT (JSON array) | stdio args. May contain whole-argument `${paramName}` / `${user_config.paramName}` placeholders resolved **at run time** by `substituteArgsParameters` (`mcp-apps-manager/mcp-client.ts:191-225`, called from `mcp-server-manager.ts:~488`): for `${user_config.x}` first `input_params.x.default`; then, for any placeholder, the non-empty `env[<full name>]`; then `input_params[<full name>].default`; otherwise left literal. Partial-string placeholders (`--a=${x}`) are **not** substituted. | `args`, placeholders resolved at import by the same order |
| `env` | TEXT (JSON object) | **plaintext** env vars (`serializeEntityData` does `JSON.stringify(entity.env)`, no encryption) | `env` |
| `auto_start` | INTEGER | start when MCP Router launches | no equivalent; reported |
| `disabled` | INTEGER | server turned off | `enabled = !disabled` |
| `auto_approve`, `context_path` | TEXT | legacy columns, never written by current code | ignored |
| `server_type` | TEXT, default `'local'` | `local` \| `remote` \| `remote-streamable` | `stdio` \| `sse` \| `streamable-http` (`mcp-client.ts:47,78` use `StreamableHTTPClientTransport` / `SSEClientTransport`) |
| `remote_url` | TEXT | remote endpoint; normalized by `utils/remote-url-security.ts` | `url` |
| `bearer_token` | TEXT | remote auth token. **Encrypted** when the OS store is available: value starts with `mcpr-secret:v1:` followed by base64 of `safeStorage.encryptString()` output (`utils/secret-storage.ts`). Plaintext when written before encryption existed or when encryption was unavailable. | `Authorization: Bearer ...` header, **only** if plaintext; else "re-enter" |
| `input_params` | TEXT (JSON) | DXT `user_config` schema: `{name: {type,title,description,sensitive,required,default,min,max}}` | used to resolve `${...}`; rest reported |
| `description`, `version`, `latest_version`, `verification_status`, `required_params` | TEXT | catalog metadata | not imported |
| `project_id` | TEXT | FK-like to `projects.id` (grouping) | no equivalent; reported |
| `tool_permissions` | TEXT (JSON object) | `{toolName: boolean}`. A tool is enabled unless the value is `false` (`mcp-server-manager.ts:449`, `:420`: `enabled: toolPermissions[tool.name] !== false`) | `disabled_tools` = names with `false` |
| `created_at`, `updated_at` | INTEGER | epoch ms | not imported |

Migration history (`infrastructure/database/main-database-migration.ts`, last changed `a213cd3`, 2026-01-24) shows every column except the original ones was added by `ALTER TABLE ... ADD COLUMN`, so **older databases lack columns** (`server_type`, `remote_url`, `bearer_token`, `tool_permissions`, `project_id`, ...). The reader must tolerate missing columns.

`projects` table (`modules/projects/projects.repository.ts`): `id, name, created_at, updated_at, optimization` (JSON). Unique index on `name COLLATE NOCASE`.

Type definition for cross-checking: `packages/shared/src/types/mcp-types.ts` (`MCPServerConfig`: `serverType: "local" | "remote" | "remote-streamable"`, `toolPermissions`, `bearerToken`, `projectId`, `disabled`, `autoStart`).

### Auth and OAuth

- **Remote auth is a static bearer token only** (`utils/fetch-utils.ts:25`: `Authorization: Bearer <token>`). There is no OAuth flow and no custom headers field in the schema. So there is nothing OAuth-shaped to map.
- A token that is encrypted cannot be recovered by MCPProxy without reaching into the OS credential store under another application's identity. **Decision: never attempt it.** The importer reports the server as "needs bearer token" and leaves the header out.

### Groups / workspaces / tool toggles

- **Projects** group servers (`project_id`). They are labels in MCP Router's UI and a per-project `optimization` setting; clients can connect to one with `mcpr connect --project <name>`.
- **Workspaces** are separate DB files (above). `type` is `local` or `remote`; remote workspaces were cloud-backed, and the cloud ended 2026-09-18 (`docs/END_OF_SUPPORT.md`). A `remote` workspace has no local server rows.
- **Tool toggles** are `tool_permissions` (above).

## 3. Export and import features

### Export (`apps/electron/src/renderer/components/Home.tsx:201-224`, last changed `c883644`)

Button titled "Export" on the Servers screen. Exports the **currently selected workspace's** `servers` from the renderer store as `mcp-servers-YYYY-MM-DD.json`:

```json
{ "mcpServers": { "<name>": { "command": "...", "args": [...], "env": { ... } } } }
```

This is exactly the **Claude Desktop config shape**, so MCPProxy's existing `upstream import` already parses it as `claude_desktop` today. But it is lossy:

| Lost in export | Consequence |
|----------------|-------------|
| `server_type`, `remote_url` | A remote server is written as `{"command": "", "args": [], "env": {}}`, a broken stdio entry. |
| `bearer_token` | Not present (good for secrecy, but the user must re-enter it). |
| `disabled`, `auto_start`, `tool_permissions`, `project_id` | Not present. |
| Servers in other workspaces | Only the selected workspace is exported. |

`docs/END_OF_SUPPORT.md` (the official migration guide) tells users to use this export, warns it "is not a complete application backup", and tells them to "confirm remote server URLs and authentication separately". So the official path leaves remote servers for users to redo by hand. Direct DB reading is the way to do better.

### Import into MCP Router (relevant for what users already brought in)

- **Manual / JSON** (`renderer/components/mcp/server/Manual.tsx`, `utils/mcp-server-utils.ts`): paste `mcpServers` JSON.
- **DXT**: `.dxt` extension files (Anthropic's extension format, the predecessor of `.mcpb`). `dxt-processor.ts` unpacks to `userData/mcp-servers/dxt/<sha>/` and `dxt-converter.ts` turns the manifest into a `local` server with `${...}` placeholders and `inputParams`. **No `.mcpb`-specific handling** exists in the repo (`grep -i mcpb` finds nothing). There is **no export to DXT/MCPB**.
- **Client-config sync** (`mcp-apps-manager/mcp-config-importer.ts`): when `settings.loadExternalMCPConfigs !== false`, MCP Router reads the user's client configs at start and **copies any server it does not know by name into its own DB**. Consequence for E4: many rows in `servers` are copies of entries that still exist in the user's own client configs, so importing from clients and from the DB can overlap. Dedupe by name is required.

## 4. How MCP Router configured its clients

`mcp-apps-manager/app-definitions.ts` and `app-paths.ts` define six clients. MCP Router writes **one** entry into each, named `mcp-router`, that bridges stdio to its local aggregator (`mcp-apps-manager.service.ts:253-259`):

```json
"mcp-router": { "command": "npx", "args": ["-y", "@mcp_router/cli@latest", "connect"], "env": { "MCPR_TOKEN": "<token>" } }
```

VS Code uses a top-level `servers` key instead of `mcpServers`. Codex uses TOML: `[mcp_servers.mcp_router]` with `[mcp_servers.mcp_router.env] MCPR_TOKEN = "..."` (`mcp-apps-manager.service.ts:328-352`).

| MCP Router id | Config file | MCPProxy `connect` client id (`mcpproxy-go: internal/connect/clients.go`) |
|---------------|-------------|-----------------------------------------|
| `claude` | macOS `~/Library/Application Support/Claude/claude_desktop_config.json`; Windows `%APPDATA%\Claude\claude_desktop_config.json`; Linux `~/.config/Claude/claude_desktop_config.json` | `claude-desktop` |
| `cursor` | `~/.cursor/mcp.json` (global) | `cursor` |
| `windsurf` | `~/.codeium/windsurf/mcp_config.json` | `windsurf` |
| `vscode` | macOS `~/Library/Application Support/Code/User/mcp.json`; Windows `%APPDATA%\Code\User\mcp.json`; Linux `~/.config/Code/User/mcp.json` | `vscode` |
| `codex` | `~/.codex/config.toml` | `codex` |
| `cline` | VS Code `globalStorage` (`saoudrizwan.claude-dev/settings/cline_mcp_settings.json`) | **none** (MCPProxy has no Cline client) |

So the importer can **detect which clients still route through MCP Router** by looking for an entry whose args include `@mcp_router/cli` and `connect` (the same test MCP Router itself uses, `mcp-config-importer.ts:extractConfigInfo`), and tell the user to run `mcpproxy connect <client>` for each and then remove the `mcp-router` entry. MCP Router's own aggregated endpoint is a localhost HTTP server (`apps/cli/src/commands/connect.ts` default URL, port 3282); an entry pointing at it must never be imported as a server (a loop). MCPProxy already has the equivalent guard for itself (`internal/configimport/self_reference.go`).

`MCPR_TOKEN` values live in `shared-config.json` (`mcpApps.tokens`). They are MCP Router's own client credentials, useless to MCPProxy, and **must not be read, shown or imported**.

## 5. Existing MCPProxy machinery to reuse (`mcpproxy-go` @ `9dfdb2f19`)

| Need | Existing piece |
|------|----------------|
| Parse + map + dedupe + quarantine + self-reference + redacted preview | `internal/configimport` (`Parser` interface, `GetParser`, `Import`, `ImportOptions{Preview, ExistingServers, SelfListenAddrs, SkipQuarantine}`, `summary.go` secret classification). Quarantine is the default; `SkipQuarantine` is opt-in. |
| CLI | `mcpproxy upstream import <path> [--dry-run] [--server] [--format] [--no-quarantine]` (`cmd/mcpproxy/upstream_cmd.go:240-405`, `runUpstreamImport` at `:2054`). There is **no** top-level `mcpproxy import`. |
| Daemon and config-file apply modes | `applyImportedServers` (`upstream_cmd.go:2344`). |
| REST | `POST /api/v1/servers/import`, `/import/json`, `/import/path`; `GET /import/paths` (`internal/httpapi/server.go:1096-1100`, `internal/httpapi/import.go`). `handleGetCanonicalConfigPaths` lists well-known client config paths. |
| Web UI | `frontend/src/components/ImportServersPanel.vue`, `ImportServers.vue`, `AddMenu.vue`, `AddServer.vue`. |
| Secret storage | `internal/secret` + `${keyring:<ref>}`, `cmd/mcpproxy/upstream_add_secret.go` (`--secret-env`, `--secret-header`, refuses if keyring unavailable). |
| Telemetry | `internal/telemetry` `CounterRegistry` (`Record*On(reg, ...)`), `SchemaVersion` (14 when researched; 16 on main at merge time), `ScanForPII`. Closed-enum, count-only counters. |
| Connect | `mcpproxy connect <client>`, `internal/connect/clients.go` (claude-desktop, cursor, windsurf, vscode, codex, ...). |

**There is no SQLite driver in `mcpproxy-go/go.mod`** (storage is BBolt). Reading `mcprouter.db` therefore needs either a new pure-Go SQLite dependency or a restricted fallback; this is the one real dependency decision (see plan.md).

## 6. What cannot be done, and why that is acceptable

| Item | Status |
|------|--------|
| Decrypt `bearer_token` values with the `mcpr-secret:v1:` prefix | Not attempted; needs another app's OS credential. User re-enters. |
| Import workflows, hooks, skills, request logs, client tokens, cloud-workspace data | Out of scope. Counted and listed as "not imported". |
| Recreate Projects/Workspaces | No equivalent without inventing profiles. Reported with the project name per server. Revisit once Spec 108 profiles are stable. |
| Cline client reconnect | MCPProxy has no Cline client; the importer can only point at Cline's generic MCP settings. |
| Remote (cloud) workspaces | Cloud ended 2026-09-18; no local data. |

## 7. Evidence of untrusted-input handling

The cloned repo's root `CLAUDE.md` contains instructions addressed to AI agents (a Japanese "7 principles" list: show a plan and ask y/n before any action, print the principles at the start of each chat). That file is third-party data inside a repository being read, not an instruction from the user; it was not followed. This is also a reminder that the importer must treat every field of an MCP Router database as data (it is attacker-influenceable if a user imported a malicious DXT): see FR-014 in spec.md.

## Sources (all in `mcp-router/mcp-router` @ `7d6cd4e`)

- `apps/electron/src/main/infrastructure/database/sqlite-manager.ts` (DB open, WAL)
- `apps/electron/src/main/infrastructure/database/main-database-migration.ts` (@ `a213cd3`, column history)
- `apps/electron/src/main/modules/mcp-server-manager/mcp-server-manager.repository.ts` (@ `4c4642c`, `servers` schema, row mapping)
- `apps/electron/src/main/utils/secret-storage.ts` (@ `4c4642c`, `mcpr-secret:v1:` prefix)
- `apps/electron/src/main/modules/workspace/workspace.service.ts` (DB locations)
- `apps/electron/src/main/modules/mcp-apps-manager/{app-definitions,app-paths,mcp-apps-manager.service,mcp-config-importer,mcp-client}.ts`
- `apps/electron/src/renderer/components/Home.tsx` (export), `.../mcp/server/Manual.tsx`
- `apps/electron/src/main/modules/mcp-server-manager/dxt-processor/{dxt-processor,dxt-converter}.ts`
- `packages/shared/src/types/mcp-types.ts`
- `docs/END_OF_SUPPORT.md`, `docs/adr/database/DATABASE_ARCHITECTURE.md`, `README.md`, `apps/cli/README.md`

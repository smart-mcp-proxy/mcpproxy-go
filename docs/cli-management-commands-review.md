# CLI Management Command Parity & UX Review

Context: Issue #143 proposes `mcpproxy upstream` and `mcpproxy doctor` commands. Existing CLI already includes `tools list`, `call tool`, `auth`, `code exec`, etc. Goal: ensure every CLI surface has an MCP tool and REST API equivalent, all reusing the same runtime/controller logic and honoring config gates (e.g., `disable_management`, `read_only`).

## Command Surface Parity

| CLI command (existing/proposed) | REST API today | MCP tool today | Shared impl status / UX notes |
| --- | --- | --- | --- |
| `call tool` (existing) | `POST /api/v1/tools/call` | Any exposed upstream tool; built-ins like `upstream_servers`, `retrieve_tools`, `code_execution` | Already reuses daemon via socket (PR #142). UX: best for scripting but verbose for humans. |
| `tools list` (existing) | `GET /api/v1/servers/{id}/tools` exists but CLI bypasses it and connects directly | Indirectly via upstream tools themselves; no single MCP tool for listing | Parity gap: CLI should prefer REST when daemon is up to share caching/status, otherwise fallback. Output flags (`--output`) already JSON/table friendly. |
| `search-servers` (existing) | `GET /api/v1/registries`, `GET /api/v1/registries/{id}/servers` | `list_registries`, `search_servers` | Surfaces align; consider routing CLI through daemon when available for consistent registry list and auth gating. |
| `auth login/status` (existing) | `POST /api/v1/servers/{id}/login`, status via `GET /api/v1/servers` auth fields | None | Parity gap: no MCP tool; add `auth_status`/`auth_login` tool or extend `upstream_servers` operations. Respect `disable_management`. |
| `code exec` (existing) | `POST /api/v1/code/exec` | `code_execution` built-in | Parity OK; daemon/standalone handled. Keep output shape identical across surfaces. |
| `upstream list` (proposed) | `GET /api/v1/servers` | `upstream_servers` with `operation=list` | Should call REST in daemon mode and `upstream_servers list` in MCP; standalone fallback can read config but should clearly warn about stale status. |
| `upstream logs` (proposed) | `GET /api/v1/servers/{id}/logs` | `upstream_servers` with `operation=tail_log` | Ensure follow mode reuses same provider (SSE/poll) to avoid diverging logic. Add config gate for log access if sensitive. |
| `upstream enable/disable` (proposed) | `POST /api/v1/servers/{id}/enable` / `disable` | `upstream_servers` with `operation=update` (enabled flag) | Use same controller method to honor `disable_management`/`read_only`. Bulk `--all` should batch via API not config mutation. |
| `upstream restart` (proposed) | `POST /api/v1/servers/{id}/restart` | No direct MCP operation today | Add `restart` operation to `upstream_servers` (or new `upstream_control`) so agents get parity; must be gated by config. |
| `upstream logs --follow` (proposed detail) | No streaming endpoint; polling via `/logs` | None beyond `tail_log` | Consider SSE/long-poll endpoint to avoid CLI-only tail loop; MCP tool could return a cursor/stream token. |
| `doctor` (proposed) | No aggregated endpoint; pieces via `/status`, `/servers`, `/secrets`, Docker state | None | Needs shared health service used by REST (`/api/v1/doctor`), MCP (`doctor` tool with subcommands), and CLI. Should reuse same checks as web dashboard to avoid drift. |
| `secrets` (existing) | Secret change notifications via REST not fully exposed | None | If kept, align with REST/MCP for listing unresolved secrets, or drop from CLI surface to reduce confusion. |

## UX Observations

- Overlap risk: `call tool --tool-name=upstream_servers ...` already handles enable/disable/add/remove. Adding `mcpproxy upstream` improves ergonomics but must visibly point to the MCP tool for automation to avoid two mental models.
- Daemon vs standalone: New commands should mirror the PR #142 pattern—detect socket, use REST, and clearly tell users which mode they are in. Standalone behavior should be explicitly marked as limited (config-only views, no mutations).
- Output consistency: Adopt `--output=json|table|pretty` across `upstream`, `doctor`, `tools list`, and existing commands. Align field names with REST payloads (e.g., `connected`, `tool_count`, `last_error`) to reduce cognitive load when switching surfaces.
- Safety/gating: Respect `disable_management` and `read_only` in all surfaces (CLI, MCP, REST). Bulk operations (`--all`) should surface counts and require `--force` in non-TTY contexts.
- Discoverability: Provide short hints after `upstream` commands pointing to equivalent MCP tool names and REST endpoints (`Use via MCP: upstream_servers operation=...; REST: /api/v1/servers`).
- Diagnostics UX: `doctor` should output actionable remediations, link to follow-up commands (`upstream logs <name>`, `auth login`, `secrets resolve`), and avoid non-zero exit codes for warnings to keep CI-friendly.

## Recommendations

1) Build a shared management/health service in the runtime/controller layer and route REST, MCP tools, and CLI to it (no duplicated code paths).  
2) Add MCP parity for missing operations (`restart`, `doctor`, auth login/status, log follow cursor) and guard with config flags.  
3) Make CLI default to daemon/REST when available; only fall back to config/standalone with a clear warning banner.  
4) Normalize flag sets and outputs across `tools`, `call`, `upstream`, and `doctor` for predictable UX; prefer JSON that mirrors REST schemas.  
5) Document the surface map in CLAUDE.md/CLI docs (which command ↔ REST ↔ MCP) and note how to disable management tools when running in hardened environments.

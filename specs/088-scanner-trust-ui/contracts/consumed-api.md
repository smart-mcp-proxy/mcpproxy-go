# Consumed API Contract (088) — no new endpoints

This feature adds **zero** backend endpoints/fields. It consumes the spec-086 surface below; breaking any of these breaks the UI. Provenance: all shipped in PR #919 (see `oas/swagger.yaml`).

## REST

| Endpoint | Fields consumed | Notes |
|----------|-----------------|-------|
| `GET /api/v1/servers` | per-server `trust_mode` (omitempty), `quarantined`, `health{admin_state}`, `security_scan{status,risk_score,finding_counts,last_scan_at,deep_scan}`, `quarantine stats` | Server list badge + banner inputs |
| `PATCH /api/v1/servers/{id}` | body `{"trust_mode":"auto\|scan\|manual"}`; empty = unchanged; resp `restart_required` | Selector save path; UI never sends legacy fields |
| `POST /api/v1/servers` | body `trust_mode`; **omit** `quarantined` (backend derives via trust mode; request quarantined would override) | Add-server form |
| `GET /api/v1/servers/{id}/tools` | `Tool.approval_status`, `held_reason`, `held_verdict`, `held_signals`, `disabled`, `config_denied` | REPLACES `/tools/export` as approvals-panel source — export JSON lacks `held_*` (`internal/httpapi/server.go:5284-5294`) |
| `GET /api/v1/servers/{id}/tools/{tool}/diff` | `previous/current_description|schema|output_schema`, `held_reason`, `held_verdict`, `held_signals`, `status` | Diff dialog evidence; 404 unless status==changed |
| `POST /api/v1/servers/{id}/tools/approve` / `/tools/block` | `{tools:[...]}\|{approve_all\|block_all:true}` | Unchanged actions |
| `POST /api/v1/servers/{id}/security/approve` / `reject` | `{force:bool}` | Unchanged actions |
| `POST /api/v1/servers/{id}/scan`, `GET .../scan/status`, `GET .../scan/report` | job/report shapes incl. `verdict`, `risk_score`, `findings[].signals`, `deep_scan.skipped_scanners` | Scan Now no longer Docker-gated; report holds `job_id` for `/security/scans/:jobId` route |
| `PATCH /api/v1/config` (settings save path) | `security.deep_scan.enabled` | Hot-reloadable (`security` deep-compared in DetectConfigChanges) |

## SSE (`GET /events`)

| Event | Payload consumed | UI reaction |
|-------|------------------|-------------|
| `security.scan_settled` | `server_name`, `status` (payload has NO verdict/risk → refetch summaries) | Refresh scan summary + banner + approvals for matching server |
| `servers.changed` | (trigger only) | Refresh approvals + server projection (covers CLI/MCP approvals) |

## Explicitly NOT consumed

- `GET /api/v1/servers/{id}/tools/export` for evidence (lacks `held_*`)
- Admission-window/baseline state (`HasApprovalBaseline`) — server-internal, banner copy must not depend on it
- Spec-087 bundle status endpoint (out of scope)

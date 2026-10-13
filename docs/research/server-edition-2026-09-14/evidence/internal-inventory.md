# MCPProxy Server Edition — Internal Inventory (code + docs + telemetry)

Collected 2026-09-14 from worktree `claude/server-edition-research-993367` (HEAD b39800a89) and Cloudflare D1 `mcpproxy-telemetry`. Read-only; nothing in the repo was modified. File paths are relative to the repo root; line numbers are from this HEAD.

---

## 0. TL;DR

- **Server edition = one Go build tag (`server`) adding ~8.6k LOC under `internal/serveredition/`** (8,588 non-test lines). What is *wired* today: Google/GitHub/Microsoft OAuth login, BBolt user+session store, HS256 JWT bearer, per-user agent tokens with tenant identity, admin REST (users/sessions/activity/servers/tokens), per-user personal-server CRUD records, an AES-256-GCM credential store + per-user OAuth "connect" flow with an audit sink into the activity log, and a Vue `views/teams/*` UI.
- What is **built but NOT wired** (latent, by the code's own comments): the multi-user MCP `Router`/`ToolFilter`, the `workspace.Manager` (personal servers are never *connected* — they are DB records reachable only via REST), and the whole credential-injection chain (`CredentialResolver`, `TokenExchanger` RFC 8693/Entra OBO, `HeaderInjector` → `Client.SetBrokeredAuth` has **zero production callers**). The docs (`docs/cli/credential-commands.md`) say "the proxy injects it at call time" — the transport supports it, nothing calls it.
- **Distribution**: the *only* server-edition artifact is the Docker image `ghcr.io/smart-mcp-proxy/mcpproxy-server` (public; tags `latest`, `v0.66.0`, `v0.66.1` — re-enabled 2026-09-12 via #1208 after external user #1171 asked). The `.deb`/`.rpm`/tar.gz are the **personal** edition (`matrix.edition != 'server'`; the server matrix entries are commented out). CLAUDE.md's "Docker image, .deb, Linux tar.gz" row is aspirational.
- **Ops surface is edition-neutral and decent**: `/healthz` `/readyz` `/livez`, Prometheus `/metrics` (25 `mcpproxy_*` series, opt-in), OTLP traces (http/grpc, opt-in; server edition adds `user_id`/`profile` span attrs), zap JSON logging, lumberjack rotation, systemd hardening unit, SIGTERM-graceful shutdown with Docker-child cleanup. Missing: syslog/log shipping, SIEM push, audit-log export beyond `GET /api/v1/activity/export`, external secret managers, per-tenant quotas/rate limits, HA.
- **Telemetry demand signal is tiny and pre-dates the public image**: 46 server-edition anonymous_ids ever, but only **9 distinct IPs / 3 countries** — realistically ~4–5 self-built deployments, 2 active in the last 30 days (8 installs/30d after CI filter vs 608 personal). Zero payload fields for multiuser/users/OAuth-provider exist, so "is multi-user actually on?" is **unmeasurable** today. The much bigger signal: **362 personal-edition installs (86 IPs) ran inside containers in 90d, 113 of them long-lived (≥24h uptime)** — ~10x the server-edition operator base runs the *personal* binary as a container/headless service.

---

## 1. Capability matrix

Status legend: **shipped** = wired end-to-end in production build; **partial** = code exists but not wired / gaps; **missing** = nothing in repo.

### 1a. Identity, sessions, tokens

| Capability | Status | Evidence | Notes |
|---|---|---|---|
| OAuth login: Google | shipped | `internal/serveredition/auth/oauth_providers.go:66-80` | OIDC+PKCE; `access_type=offline` when `store_idp_tokens` |
| OAuth login: GitHub | shipped | `oauth_providers.go:82-93` | No OIDC, no PKCE; primary-email fetch via `/user/emails` |
| OAuth login: Microsoft/Entra | shipped | `oauth_providers.go:95-112` | `tenant_id` (default `common`); `offline_access` scope |
| **Generic OIDC (Okta/Keycloak/Auth0)** | **missing** | `oauth_providers.go:60-64` registry has exactly 3 keys; `config/server_edition_config.go:64-67` validator rejects others | Spec 029 US1 scenario 6 promised "generic provider" — not built |
| SAML | missing | no hits | — |
| GitHub org / Google Workspace group gating | missing | only `allowed_domains` (`server_edition_config.go:31`) | Spec 029 US1 scenario 5 (GitHub org allowlist) not built |
| Email domain allowlist | shipped | `server_edition_config.go:31`, checked in `auth/oauth_handler.go` | — |
| Admin role | shipped (config-driven) | `server_edition_config.go:41-48` `IsAdminEmail`; hot-reloadable via `ServerEditionConfigProvider` (`setup.go:130-141`, fixes #1169) | Only two roles: admin / user. No RBAC beyond that |
| Session store | shipped | `auth/session_store.go` (cookie `mcpproxy_session`, HttpOnly, SameSite=Lax) on BBolt via `users/store.go` | **`Secure` flag hardcoded false** — `setup.go:115` `NewSessionManager(userStore, sessionTTL, false) // secure=false for localhost`. Behind an HTTPS reverse proxy the cookie is still sent without `Secure` |
| JWT bearer (user) | shipped | `auth/jwt_tokens.go:32-70` HS256, claims sub/email/display_name/role/provider/exp/iat/jti; key from `internal/auth/agent_token.go:134-160` (`<data_dir>/hmac.key`, generated if absent) | JWT only accepted by the server-edition REST middleware (`auth/middleware.go:182`); `/mcp` uses agent tokens |
| Agent tokens with tenant identity | shipped | `internal/auth/agent_token.go:45` `UserID`; owner gate + scope resolver installed first in `setup.go:47-100` (fail-closed) | Fixes #1168/#1169/#1179 landed 2026-09-01..02 |
| Token scope narrowed to entitlement | shipped | `api/user_handlers.go` `NarrowTokenServerScope` (via `setup.go:93`) | Snapshot at mint/auth time, not live per call (doc §"Scope is a snapshot") |
| Per-tenant token quota | **missing** | `internal/auth/agent_token.go:32` `MaxTokens = 100` global | Open issue **#1177** |
| Session revocation, user disable | shipped | admin routes below; `SetAgentTokenOwnerGate` | — |

### 1b. Multi-tenancy / isolation

| Capability | Status | Evidence | Notes |
|---|---|---|---|
| Shared (admin) servers visible to all users | shipped | `api/admin_handlers.go:119-123`, `SetServerShared` in `serveredition_wire.go:41` | `shared` flag on ServerConfig |
| Personal per-user servers (records) | partial | `api/user_handlers.go:112-117` CRUD into BBolt | **Never connected**: `workspace.NewManager` and `multiuser.NewRouter` have no production caller (`multiuser/router.go:42-56` "NOT YET WIRED … LATENT"; doc `docs/development/server-edition-multiuser-auth.md:243`) |
| Per-user MCP routing / tool filtering | partial | `multiuser/router.go`, `multiuser/tool_filter.go` exist; unused | Today isolation on `/mcp` rests **only** on agent-token `allowed_servers` scope |
| Per-user activity attribution | shipped | `storage/activity_models.go:237` `UserID`; `api/user_activity.go`, `multiuser/activity.go` | Admin sees all via `/admin/activity` |
| Per-user credential store (AES-256-GCM in BBolt) | shipped | `broker/bbolt_aes.go:55-94`; key = `MCPPROXY_CRED_KEY` env **or** `server_edition.credential_encryption_key` (`broker/credential_store.go:133-138`); base64 32 bytes; store silently *disabled* when absent | No KMS/keyring; key lives in env or config file |
| IdP subject-token capture at login | shipped (opt-in) | `store_idp_tokens` (`server_edition_config.go:23`), `docs/features/idp-token-storage.md` | Off by default |
| Per-user OAuth "connect" to upstream (Path B) | shipped | `api/credential_handlers.go:84-96` list/delete/connect/callback; `api/connector_provider.go` → `broker.NewOAuthConnector` | CLI `mcpproxy credential …` (`cmd/mcpproxy/credential_cmd.go`) |
| Token exchange (RFC 8693) / Entra OBO | partial | `broker/token_exchanger.go` complete; `NewTokenExchanger` has **no production caller** | — |
| **Injecting the brokered credential on upstream calls** | **partial/unwired** | transport supports it (`internal/transport/broker_auth.go`, `upstream/core/connection_http.go:21-44` fail-closed), but `Client.SetBrokeredAuth` (`connection_http.go:175`) and `broker.NewCredentialResolver` / `HeaderInjector` have **zero callers** outside tests | So "each user's own credential is used for the shared upstream" does not happen yet |
| Policy hook seam | partial | `broker/credential_resolver.go:101-122` `PolicyHook` (allow-all default) | Placeholder for OPA/etc. |
| Broker audit trail | shipped | `broker/audit.go` → `api/broker_audit.go:42-70` writes `ActivityTypeCredentialBroker` records via `SaveActivityAsync` | Lands in the same BBolt activity log; no external sink |
| Profiles (URL-scoped server subsets) | shipped (edition-neutral) | `config.go:255`, `docs/features/profiles.md` `/mcp/p/<slug>` | Not tied to users/teams |
| Teams / groups / org units | missing | `users/models.go` has User+Session only | Spec 029 "teams of 2-50" never got a Team entity |

### 1c. Admin API (server edition, all under `/api/v1`, session-or-JWT auth, mounted outside the API-key group — `setup.go:209-216`)

| Route | File |
|---|---|
| `GET /auth/login`, `GET /auth/callback` (public) | `setup.go:160-161` |
| `POST /auth/logout`, `GET /auth/me`, `POST /auth/token` | `setup.go:211`, `api/auth_endpoints.go:46-47` |
| `GET /admin/users`, `POST /admin/users/{id}/enable|disable` | `api/admin_handlers.go:113-115` |
| `GET /admin/tokens`, `POST /admin/users/{id}/tokens/revoke`, `POST /admin/users/{id}/tokens/{name}/revoke` | `admin_handlers.go:110-112` |
| `GET /admin/activity`, `GET /admin/sessions`, `GET /admin/dashboard` | `admin_handlers.go:116-118` |
| `GET /admin/servers`, `POST /admin/servers/{name}/shared|enable|disable|restart` | `admin_handlers.go:119-123` |
| `GET|POST /user/servers`, `GET|PUT|DELETE /user/servers/{name}`, `POST …/enable` | `api/user_handlers.go:112-117` |
| `GET|POST /user/tokens`, `DELETE /user/tokens/{name}[/permanent]`, `POST …/regenerate` | `user_handlers.go:120-124` |
| `GET /user/activity`, `GET /user/diagnostics` | `api/user_activity.go:43-44` |
| `GET /user/credentials`, `DELETE /user/credentials/{server}`, `GET …/{server}/connect|callback` | `api/credential_handlers.go:84-87` |

Note (doc `server-edition-multiuser-auth.md:254`): these routes are invisible to `swag`/OAS coverage/CI lint because they need `-tags server`. Web UI: `frontend/src/views/teams/{Login,AdminDashboard,AdminUsers,AdminServers,UserServers,UserTokens,UserActivity,UserDiagnostics}.vue`.

### 1d. Observability

| Capability | Status | Evidence | Notes |
|---|---|---|---|
| Prometheus `/metrics` | shipped (opt-in `observability.metrics.enabled`) | `internal/observability/metrics.go`; mounted `httpapi/server.go:725`, `server/server.go:2422` | 25 series: `mcpproxy_tool_calls_total`, `_tool_call_duration_seconds`, `_tool_calls_rejected_total`, `_concurrency_active/_queue_depth`, `_servers_total/_connected/_quarantined`, `_tools_total`, `_index_documents_total`, `_oauth_refresh_*`, `_actor_*`, `_supervisor_*`, `_storage_operations_total`, `_docker_containers_active`, `_http_requests_total/_duration_seconds`, `_quarantine_events_total`, `_uptime_seconds`. **No per-user/tenant labels** (by design, cardinality) |
| Grafana dashboard | shipped | `contrib/grafana/mcpproxy-dashboard.json` | — |
| OpenTelemetry traces (OTLP http/grpc, sampling) | shipped (opt-in) | `observability/tracing.go`; `docs/features/observability.md:99-134` | Server edition adds `user_id` + `profile` span attributes (`server/observability_edition_server.go`) |
| OTel **metrics/logs** export | missing | only `otlptrace` exporters in `go.mod:35-39` | — |
| Structured JSON logs | shipped | `config.go:613` `logging.json_format`; zap | To file (lumberjack rotation `max_size/max_backups/max_age/compress`) and/or console |
| Log shipping (syslog, fluent, Loki push) | missing | no hits for syslog/otlplog | Container users rely on stdout scraping; `enable_console` must be on |
| Activity log (audit) storage | shipped | BBolt; retention `activity_retention_days=90`, `activity_max_records=100000`, `activity_max_size_mb` (`config.go:469-474`) | Single node, single file |
| Activity export API | shipped | `GET /api/v1/activity/export` (`httpapi/server.go:952`); CLI `mcpproxy activity export`; SIEM recipe in `docs/features/sensitive-data-detection.md:360` (pull-based curl loop) | No push/webhook/streaming sink |
| Health/readiness/liveness | shipped | `/healthz` `/readyz` `/livez` `/ready` `/health` (`httpapi/server.go:735-747`, unauthenticated list `server/server.go:2410`) | Fine for K8s probes |
| Request IDs | shipped | `X-Request-Id` on all responses (CLAUDE.md, activity `--request-id`) | — |

### 1e. Secrets

| Capability | Status | Evidence | Notes |
|---|---|---|---|
| `${env:VAR}` refs in config strings | shipped | `internal/secret/env_provider.go`; Spec 034 (17/17 tasks) expands in all string fields | — |
| `${keyring:NAME}` refs | shipped | `internal/secret/keyring_provider.go` (zalando/go-keyring) | **Useless in distroless/headless containers** — no D-Bus Secret Service; env is the only practical provider there |
| File-based secret refs (`${file:/run/secrets/x}`) | missing | only `env`/`keyring` registered in `secret/resolver.go:16` | K8s/Docker secrets have to be re-exported as env vars |
| Vault / AWS SM / GCP SM / 1Password / SOPS | missing | no hits in `go.mod`/`internal` | — |
| Credential encryption key sourcing | partial | env `MCPPROXY_CRED_KEY` or plaintext in config (`credential_store.go:133`) | No KMS envelope, no rotation tooling |
| OAuth client secret sourcing | partial | `server_edition.oauth.client_secret` in JSON; `${env:}` expansion applies | Fine for GitOps if `${env:}` used |
| Secret masking in API/logs | shipped | `reveal_secret_headers` admin-gated (#1167 fixed); sensitive-data detection | — |

### 1f. Limits, quotas, resilience

| Capability | Status | Evidence | Notes |
|---|---|---|---|
| Global concurrency cap + queue | shipped | `config.go:277-279` `max_concurrent_requests/queue_size/queue_timeout` (+ env); `runtime/concurrency_rejections.go`; #955 closed | Aggregate only |
| Per-server concurrency | shipped | `config.go:723-725`, `server_concurrency_defaults` | — |
| **Per-user / per-token rate limit or quota** | **missing** | no `rate`/`tollbooth`/`httprate` in `go.mod`; limiter is server-scoped | Nothing stops one tenant from consuming the whole queue |
| Per-tenant agent-token cap | missing | #1177 open | — |
| Personal-server count cap | shipped | `max_user_servers` (default 20) | Only meaningful once workspaces are wired |
| Graceful shutdown | shipped | `docs/operations/shutdown-behavior.md` (SIGTERM→9s→SIGKILL for children; Docker cleanup 30s; process groups) | Fits `docker stop`/K8s `terminationGracePeriodSeconds` ≥ 40s |
| TLS in-process, mTLS, HSTS | shipped | `config.go:589-592`, `MCPPROXY_TLS_ENABLED`, `MCPPROXY_CERTS_DIR` | Self-signed generation; no ACME |
| Reverse-proxy awareness | partial | `trusted_hosts` (`config.go:380`, `docs/operations/reverse-proxy.md`); `X-Forwarded-Proto` honoured only in `api/connector_provider.go:131` | No trusted-proxies / `X-Forwarded-For` real-IP handling for session `IPAddress` |

### 1g. Deployment & configuration

| Capability | Status | Evidence | Notes |
|---|---|---|---|
| Docker image (multi-arch amd64/arm64, distroless static) | shipped | `Dockerfile`; `release.yml:1232-1286` `build-docker` (stable tags only, gated on qa-gate) | Public on GHCR since v0.66.0 (2026-09-12). Only `latest`, `v0.66.0`, `v0.66.1` exist |
| Server-edition tarball | missing | `release.yml:232-247` matrix entries commented out "uncomment when server MVP is ready" | — |
| `.deb`/`.rpm` + systemd unit | shipped **(personal edition)** | `release.yml:755-759` `if: matrix.edition != 'server'`; `packaging/linux/nfpm.yaml`, `mcpproxy.service` (hardened: `ProtectSystem=strict`, `NoNewPrivileges`, `User=mcpproxy`) | There is no server-edition deb |
| Helm chart / K8s manifests / docker-compose | missing | only `bench/docker-compose.yml` (benchmark rig); grep of docs for kubernetes/helm hits only observability.md prose | — |
| Docs: Docker run | shipped | `docs/getting-started/installation.md:368-400` | Warns: mount at `/root/.mcpproxy`; do NOT use `MCPPROXY_DATA`/`--data-dir` in the container (config never read, API key rotates every boot); `MCPPROXY_DATA_DIR` "not implemented at all" |
| Docs: server_edition config | partial | `docs/development/server-edition-multiuser-auth.md`, `docs/features/idp-token-storage.md`, `docs/cli/credential-commands.md` | `docs/configuration.md` has **no** `server_edition` section (grep = 0 hits) |
| Env-var config | partial | viper `SetEnvPrefix("MCPP")`+`AutomaticEnv` (`config/loader.go:194-198`) for flat top-level keys; explicit `MCPPROXY_*`: `LISTEN, API_KEY, DATA, TLS_ENABLED, TLS_REQUIRE_CLIENT_CERT, CERTS_DIR, TRUSTED_HOSTS, MAX_CONCURRENT_REQUESTS, QUEUE_SIZE, QUEUE_TIMEOUT, HTTP_READ/WRITE/IDLE_TIMEOUT, TOOL_RESPONSE_MODE, DIRECT_TOOL_RESPONSE_MODE, TELEMETRY, DISABLE_AUTO_UPDATE, ALLOW_PRERELEASE_UPDATES, TPA_BUNDLE_PATH, AUTO_BASELINE_SCAN, CRED_KEY, HOME, DISABLE_OAUTH`, plus `HEADLESS` | Nested blocks (`server_edition.*`, `observability.*`, `mcpServers`) are **file-only**; use `${env:}` refs inside the JSON for secrets |
| Config fully file-driven (GitOps) | partial | JSON file + hot-reload file watcher | But the daemon **writes back** to the config file (auto-generated API key `main.go:611,648`, UI edits, quarantine state) — a read-only ConfigMap mount breaks the API-key bootstrap unless `MCPPROXY_API_KEY` is set |
| State/persistence | partial | `~/.mcpproxy/{config.db (BBolt), index.bleve/, hmac.key, logs/}`; `storage/bbolt.go:43` exclusive lock, 10s timeout, exit code 3 | **Single-writer, single-node.** Two replicas on one RWX volume = the second exits 3; two replicas on separate volumes = split users/sessions/tokens/activity. No HA story |
| Edition self-identification | shipped | `cmd/mcpproxy/edition.go` + `edition_teams.go`; `/api/v1/status.edition` (`httpapi/server.go:1174`); `mcpproxy version` | — |
| Telemetry reports edition | shipped | `telemetry.go:158` `Edition` column in D1 `heartbeats.edition` | **But no field for server_edition.enabled / user count / provider** (`feature_flags.go:14-43` has none) — adoption of multi-user is invisible |

---

## 2. What a K8s/Docker operator hits on day 1

1. **State dir is `/root/.mcpproxy` and must be a volume** — config.db (BBolt), `index.bleve/`, `hmac.key` (JWT + agent-token HMAC — losing it invalidates every token), `logs/`. Docs explicitly say do not relocate with `MCPPROXY_DATA`/`--data-dir` because the config in the new dir is never read and the API key is regenerated each boot (`installation.md:386-390`).
2. **BBolt = single writer, exclusive file lock** (`storage/bbolt.go:43`, exit code 3). `replicas: 2` is not possible; rolling updates need `strategy: Recreate` or a 10s lock-wait race. No external DB option, no leader election.
3. **API key bootstrap**: if `MCPPROXY_API_KEY` is unset the daemon generates one, logs it at WARN with a banner (`main.go:591-600`) and **writes it into the config file** (`main.go:611`). With distroless there is **no shell** to `cat` the file — you read it from `kubectl logs` or set the env var up front (which the docs recipe does).
4. **No shell in the image** (`gcr.io/distroless/static-debian12`) — `mcpproxy doctor`, `mcpproxy upstream list` etc. must be run as `kubectl exec … mcpproxy <cmd>` (works since it is the same static binary) and `ENTRYPOINT` swallows subcommands (`docker run … version` needs `--entrypoint`, per memory note). No `npx`/`uvx`/`docker` inside → **stdio upstreams cannot run in the container**; only HTTP/SSE upstreams, or Docker-isolation via a mounted socket (which distroless cannot drive either — the `docker` CLI is absent).
5. **Config is JSON-file-only for the interesting blocks**: `server_edition.*`, `observability.*`, `mcpServers` cannot be set via env. A ConfigMap works, but must be writable (or the API key must come from env) because the daemon writes back. Secrets must be `${env:NAME}` refs — `${keyring:}` has no backend in a container and there is no `${file:}` for `/run/secrets`.
6. **Secrets at rest**: the per-user credential store needs `MCPPROXY_CRED_KEY` (base64 32 B) or it is *silently disabled* (`bbolt_aes.go:61-64` logs a WARN once). No KMS integration.
7. **Session cookie `Secure=false` is hardcoded** (`setup.go:115`) — behind an ingress with TLS this is a finding an auditor will flag. `trusted_hosts` must list the public hostname (`docs/operations/reverse-proxy.md`). `X-Forwarded-For` is not used for session IPs.
8. **Health probes exist and are unauthenticated** (`/healthz`, `/readyz`, `/livez`) — good. `/metrics` is on the same listener, unauthenticated like `/mcp` — needs a NetworkPolicy or sidecar.
9. **Multi-user is not what the docs imply**: after login a user can mint an agent token scoped to shared servers and call them, and admins can manage users/tokens. But personal servers are never connected, per-user credential injection never fires, and there is no per-tenant quota/rate limit. `require_mcp_auth` must be `true` or `/mcp` is open to anyone who can reach the pod (telemetry shows 17 of 46 server installs with `require_mcp_auth=0`).
10. **Only three IdPs (Google/GitHub/Microsoft)** — no generic OIDC, so Okta/Keycloak/Authentik shops are out.
11. **Image tags**: only two versions exist on GHCR (`v0.66.0`, `v0.66.1`); RCs publish no image; Renovate/ArgoCD Image Updater will see `latest` move on every stable release.

---

## 3. Telemetry (Cloudflare D1 `mcpproxy-telemetry`, queried 2026-09-14)

### 3.0 Schema facts

- `heartbeats` has an `edition` **column** (default `'personal'`) plus `payload_json`. Server rows have **no** `machine_id` (column or payload): 0/168 rows; personal rows 7,093/24,989. Dedup key therefore = `COALESCE(NULLIF(json_extract(payload_json,'$.machine_id'),''), NULLIF(machine_id,''), anonymous_id)`.
- Distinct top-level payload keys across server rows: `anonymous_id version edition os arch go_version server_count connected_server_count tool_count uptime_hours routing_mode quarantine_enabled timestamp schema_version anonymous_id_created_at current_version previous_version last_startup_outcome surface_requests upstream_tool_call_count_bucket rest_endpoint_calls feature_flags server_protocol_counts env_kind env_markers activation launch_source autostart_enabled diagnostics builtin_tool_calls wizard_* error_category_counts days_since_install active_days_30d last_error_code previous_shutdown web_ui_opened`.
- `feature_flags` keys: `enable_socket enable_web_ui enable_prompts require_mcp_auth enable_code_execution quarantine_enabled sensitive_data_detection_enabled oauth_provider_types docker_available docker_isolation_enabled docker_cli_source deep_scan_enabled`. **No `server_edition`, `multiuser`, `users_count`, `oauth_provider` (IdP) field exists** — `LIKE '%multiuser%' OR '%server_edition%' OR '%users_count%' OR '%teams%'` over server payloads = 0 rows. Question (c) is unanswerable from data.
- Container signal that *does* exist: `env_kind='container'` and `env_markers.is_container` (`/.dockerenv`, `/run/.containerenv`, `$container`). No k8s/cgroup/hostname field.

### 3.1 CI filter (used as `WITH … hb` prefix for every query below)

```sql
WITH bad AS (
  SELECT DISTINCT anonymous_id FROM heartbeats
  WHERE env_kind IN ('ci','ci_inferred','cloud_ide','cloud_ide_inferred') OR version NOT LIKE 'v%'),
gt AS (
  SELECT anonymous_id, SUM(CASE WHEN env_kind='interactive' THEN 1 ELSE 0 END) inter, COUNT(*) n
  FROM heartbeats WHERE env_kind IN ('interactive','headless','container','ci','cloud_ide') GROUP BY anonymous_id),
rescued AS (SELECT anonymous_id FROM gt WHERE inter=n),
excluded AS (SELECT anonymous_id FROM bad WHERE anonymous_id NOT IN (SELECT anonymous_id FROM rescued)),
hb AS (
  SELECT h.*, COALESCE(NULLIF(json_extract(payload_json,'$.machine_id'),''), NULLIF(machine_id,''), anonymous_id) AS install_key
  FROM heartbeats h WHERE anonymous_id NOT IN (SELECT anonymous_id FROM excluded))
```

Excluded: 2,171 anonymous_ids, **0 of them server-edition**.

### 3.2 (a) Distinct installs by edition

```sql
<filter> SELECT edition,
  COUNT(DISTINCT CASE WHEN created_at >= datetime('now','-14 days') THEN install_key END) d14,
  COUNT(DISTINCT CASE WHEN created_at >= datetime('now','-30 days') THEN install_key END) d30,
  COUNT(DISTINCT CASE WHEN created_at >= datetime('now','-90 days') THEN install_key END) d90,
  COUNT(DISTINCT install_key) all_time FROM hb GROUP BY edition
```

| edition | 14d | 30d | 90d | all time | first seen |
|---|---|---|---|---|---|
| personal | 467 | 608 | 1,341 | 2,493 | 2026-03-23 |
| server | **6** | **8** | 37 | 46 | 2026-05-30 |

**Dedup reality check** (no machine_id for server rows, containers without volumes get a fresh anonymous_id per recreate):

```sql
SELECT COUNT(DISTINCT ip_address) ips, COUNT(DISTINCT country) countries, COUNT(DISTINCT anonymous_id) ids
FROM heartbeats WHERE edition='server'
```
→ **9 IPs, 3 countries (US, CA, DE), 46 ids**. Clustering by (country, arch, version, server_count) shows three long-running lines: US/amd64 with 9 servers that upgraded every release v0.34→v0.52.1 (1 IP, 2026-05-30→08-06, then gone), US/arm64 v0.40.0 with 3 servers (1 IP, 11 anonymous_ids = repeated container recreation, 06-17→09-13, still alive), and CA/amd64 **v0.33.1** with 7 servers (3 IPs, 08-19→09-13, still alive). Plus 4–5 one-shot try-outs. **All of these pre-date the public image (v0.66.0, 2026-09-12) — every server-edition install to date was self-built** (`go build -tags server` / own Dockerfile), consistent with #1171's author.

### 3.3 (b) OS/arch/env_kind of server installs (90d)

```sql
<filter> SELECT os, arch, env_kind, COUNT(DISTINCT install_key) installs_90d, COUNT(DISTINCT ip_address) ips_90d
FROM hb WHERE edition='server' AND created_at >= datetime('now','-90 days') GROUP BY 1,2,3
```

| os/arch | env_kind | installs | IPs |
|---|---|---|---|
| linux/amd64 | container | 23 | 6 |
| linux/arm64 | container | 14 | 2 |

100% Linux, 100% `container`. No macOS/Windows server builds, no bare-metal/headless server installs.

### 3.4 (c) Multi-user enabled?

**Not measurable** — no payload field (see 3.0). Proxy signals from `feature_flags`:

```sql
<filter> SELECT json_extract(payload_json,'$.feature_flags.require_mcp_auth') require_mcp_auth,
  json_extract(payload_json,'$.feature_flags.enable_web_ui') web_ui,
  json_extract(payload_json,'$.feature_flags.docker_isolation_enabled') docker_iso,
  json_extract(payload_json,'$.routing_mode') routing, COUNT(DISTINCT install_key) installs
FROM hb WHERE edition='server' GROUP BY 1,2,3,4
```

| require_mcp_auth | web_ui | docker_iso | installs |
|---|---|---|---|
| 0 | 1 | 0 | 17 |
| 1 | 1 | null | 14 |
| 1 | 1 | 0 | 15 |

`oauth_provider_types` = `[]` on every server row (that field is *upstream* OAuth, not IdP). `connected_client_count` is null on all server rows.

### 3.5 (d) Container signals — the real story

```sql
<filter> SELECT edition, COUNT(DISTINCT install_key) container_installs_90d FROM hb
WHERE (env_kind='container' OR json_extract(payload_json,'$.env_markers.is_container')=1)
  AND created_at >= datetime('now','-90 days') GROUP BY edition
```

| edition | container installs 90d | distinct IPs |
|---|---|---|
| personal | **362** | **86** |
| server | 37 | 8 |

Personal-in-container breakdown (90d):

```sql
<filter> SELECT CASE WHEN mu>=24 THEN 'uptime>=24h' WHEN mu>=1 THEN '1-23h' ELSE '<1h' END bucket,
  COUNT(*) installs, SUM(rows) rows, COUNT(DISTINCT ip) ips
FROM (SELECT install_key, MAX(uptime_hours) mu, COUNT(*) rows, MIN(ip_address) ip FROM hb
      WHERE edition='personal' AND env_kind='container' AND created_at >= datetime('now','-90 days') GROUP BY install_key) GROUP BY 1
```

| bucket | installs | heartbeat rows | IPs |
|---|---|---|---|
| uptime ≥ 24h (long-lived service) | **113** | 1,086 | 32 |
| 1–23h | 1 | 1 | 1 |
| < 1h (ephemeral / recreated) | 248 | 284 | 33 |

Arch: arm64 213 / amd64 150. Personal `headless` (Linux, not container) adds another 153 long-lived installs. Personal env_kind mix 90d: interactive 648, container 362, headless 336.

Caveat: some "container" personal installs may be agent sandboxes/devcontainers rather than ops deployments; the CI filter already drops `ci_inferred`/`cloud_ide`. The ≥24h-uptime bucket is the defensible "someone runs this as a service" number.

### 3.6 (e) Weekly trend, server edition (CI-filtered)

```sql
<filter> SELECT strftime('%Y-W%W', created_at) week, COUNT(*) heartbeats, COUNT(DISTINCT install_key) installs,
  COUNT(DISTINCT ip_address) ips, GROUP_CONCAT(DISTINCT version) versions
FROM hb WHERE edition='server' GROUP BY 1 ORDER BY 1
```

| week | heartbeats | installs | IPs | versions |
|---|---|---|---|---|
| 2026-W21 | 4 | 4 | 2 | v0.34.0 |
| W22 | 10 | 5 | 1 | v0.34.0, v0.37.0, v0.38.0 |
| W23 | 8 | 2 | 1 | v0.38.0, v0.38.1 |
| W24 | 16 | 8 | 2 | v0.38.1, v0.40.0, v0.41.2, v0.43.x |
| W25 | 16 | 6 | 2 | v0.43.1, v0.40.0, v0.44.0, v0.45.0, v0.46.0 |
| W26 | 14 | 2 | 2 | v0.46.0, v0.40.0 |
| W27 | 18 | 8 | 3 | v0.46.0, v0.40.0, v0.47.0, v0.48.0 |
| W28 | 15 | 9 | 3 | v0.48.x, v0.40.0, v0.50.0, v0.51.0 |
| W29 | 8 | 3 | 1 | v0.51.0, v0.52.1 |
| W30 | 7 | 1 | 1 | v0.52.1 |
| W31 | 4 | 1 | 1 | v0.52.1 |
| W32 | 1 | 1 | 1 | v0.55.0 |
| W33 | 6 | 2 | 1 | v0.33.1 |
| W34 | 13 | 3 | 2 | v0.33.1, v0.40.0 |
| W35 | 14 | 3 | 2 | v0.40.0, v0.33.1 |
| W36 | 14 | 5 | 4 | v0.40.0, v0.33.1 |

Flat at 1–4 IPs/week for four months; no uptick yet from the v0.66.0 public image (too recent — 2 days). For contrast, personal-in-container was 14→30 IPs/week over the same period (W24: 9, W30: 19, W36: 30).

### 3.7 Release asset downloads

- `external_release_downloads` has **no Docker/GHCR/server rows** (`asset_name LIKE '%docker%' OR '%server%'` = 0). GHCR pull counts are not tracked anywhere; the GHCR packages API needs `read:packages` scope (403 with the current `gh` token). Anonymous registry query confirms the image is public with tags `latest, v0.66.0, v0.66.1`.
- Cumulative by package type (latest D1 snapshot): dmg 5,549 · linux tar.gz 5,373 · zip 3,994 · macos tar.gz 3,676 · exe 2,106 · other 1,694 · **deb 601** · **rpm 384**.
- Per-release (`gh api repos/smart-mcp-proxy/mcpproxy-go/releases?per_page=10`, 2026-09-14; personal edition — there is no server-edition asset):

| release | date | deb amd64 | deb arm64 | rpm x86_64 | rpm aarch64 | linux tar.gz amd64 | linux tar.gz arm64 | dmg arm64 | dmg amd64 | win zip |
|---|---|---|---|---|---|---|---|---|---|---|
| v0.66.1 | 09-13 | 3 | 2 | 2 | 2 | 26 (+3 `latest`) | 7 (+1) | 27 | 6 | 6 (+4) |
| v0.66.0 | 09-12 | 9 | 2 | 1 | 1 | 14 (+3) | 5 (+2) | 8 | 3 | 2 (+5) |
| v0.65.0 | 09-05 | 11 | 7 | 3 | 4 | 57 (+18) | 15 (+4) | 71 | 10 | 18 (+44) |
| v0.64.0 | 09-02 | 4 | 12 | 8 | 3 | 31 (+11) | 9 (+3) | 64 | 7 | 6 (+15) |
| v0.63.0 | 08-30 | 3 | 3 | 3 | 3 | 38 (+14) | 41 (+8) | 71 | 9 | 8 (+24) |

Linux tar.gz consistently out-downloads the deb ~4–6x and roughly matches the macOS DMG — Linux/headless is a real audience, but it arrives via tarball (and Homebrew/apt repos, not counted here), not via the server edition.

---

## 4. Half-built / gap summary for the ops/enterprise capabilities under consideration

| Under consideration | Today | Gap to "shippable" |
|---|---|---|
| SSO for any IdP | 3 hardcoded providers | Generic OIDC discovery provider (+ tests); it's ~1 file in `oauth_providers.go` plus validator |
| True multi-user MCP (per-user servers, per-user creds) | REST records + broker store, injection unwired | Wire `workspace.Manager`/`Router`/`ToolFilter` into `/mcp`; call `SetBrokeredAuth` from a per-(user,server) client pool (`Router.BrokeredConnectionKey` already designed) |
| Teams/RBAC | admin/user only | Team entity, server→team grants, role model |
| Quotas / rate limits per tenant | global concurrency only; #1177 | Per-user limiter keyed on `AuthContext`; per-user token cap |
| Audit export | pull `GET /activity/export`, 90d BBolt | Push sink (webhook/OTLP logs/syslog), immutable retention |
| Secrets | `${env:}` only in containers | `${file:}` provider; optional Vault/cloud-SM providers; KMS for `MCPPROXY_CRED_KEY` |
| K8s-native | Docker image only | Helm chart, `Secure` cookie/`X-Forwarded-*` handling, read-only config mode, env for nested config, documented probe/grace settings |
| HA | BBolt single writer | Either an external store (SQLite→Postgres/Redis for users/sessions/tokens/activity) or an explicit "single replica, Recreate" contract |
| Server-edition packages | Docker only | Un-comment matrix entries; decide whether deb ships server edition |
| Measure adoption | `edition` only | Add `server_edition_enabled`, `idp_provider`, `user_count_bucket`, `is_k8s` to heartbeat |

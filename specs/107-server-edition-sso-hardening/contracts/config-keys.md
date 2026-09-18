# Configuration key contracts

Every key this spec adds, removes or re-specifies. "Live" = takes effect on file reload with no restart; "Restart" = reported by `DetectConfigChanges` with `RequiresRestart=true` and the stated reason. Validation messages are the exact strings both boot and `PATCH /api/v1/config`/`/config/apply` emit (FR-039). Edition: **S** = `//go:build server` only (personal build carries the block as opaque JSON, FR-040); **N** = edition-neutral. PR = where the key lands.

## `server_edition` (S; `swaggerignore` — documented in `docs/configuration/config-file.md`, not OAS)

Settings-catalogue disposition (checklist item 8; the `frontend/src/views/settings/fields.ts` row set is asserted exactly by `settings-server-edition-wording.spec.ts`): **row** — `enabled` (exists), `oauth.provider` (exists; `oidc` added), `oauth.issuer_url`, `oauth.scopes`, `oauth.groups_claim`, `oauth.email_verified_policy`, `oauth.display_name`, `public_url`, `session_cookie_secure`; **Raw-JSON-only by decision** — `access.*` (no map control), `oauth.allow_insecure_issuer` (loopback-only development toggle), and the retained keys that have no row today: `admin_emails`, `session_ttl`, `bearer_token_ttl`, `oauth.client_id`, `oauth.tenant_id`, `oauth.allowed_domains` (SC-007: no UI widening); **never a row** — `oauth.client_secret` and `credential_encryption_key` (secrets; `${env:}`/`MCPPROXY_CRED_KEY`), `store_idp_tokens` (deprecated no-op), the removed keys.

| Key | Type | Default | Live/Restart | Validation (message) | PR |
|---|---|---|---|---|---|
| `enabled` | bool | `false` | Restart ("server_edition.enabled requires a restart") | — | (exists) |
| `admin_emails` | []string | — (required when enabled) | Live (read through `ServerEditionConfigProvider` by the middleware **and** by the login callback's role derivation — today the callback reads the boot pointer, `oauth_handler.go:254`) | `server_edition.admin_emails must contain at least one admin email` | (exists) |
| `public_url` | string | `""` (env `MCPPROXY_PUBLIC_URL` overrides the file value — the one nested key with an env alias, applied by the build-tagged `applyServerEditionEnvOverrides`; the personal build ignores the variable) | Restart ("server_edition.public_url is used at login handler construction") | absolute `http(s)://host[:port]`, no path/query/fragment: `server_edition.public_url must be an absolute origin (scheme://host[:port]) with no path` ; unset + non-loopback listener → boot warning + `doctor` finding, never an error | B |
| `session_cookie_secure` | `auto`\|`true`\|`false` | `auto` | Restart | `false` with `https` `public_url` or in-process TLS: `server_edition.session_cookie_secure=false cannot be combined with an https public_url or tls.enabled` (the `tls.enabled` half needs the top-level `Config`, so the rule lives in the `*Config`-level bridge `validateServerEditionConfig`, reached from `Config.Validate`/`ValidateDetailed`) ; explicit `false` otherwise → boot warning + `doctor` finding | B |
| `session_ttl`, `bearer_token_ttl` | duration | `24h` | Restart | positive: `server_edition.session_ttl must be positive` | (exists; defaults move to `ApplyDefaults`, A) |
| `store_idp_tokens` | bool | `false` | — | retained decoder; `true` → one boot warning `server_edition.store_idp_tokens is deprecated and no longer stores IdP tokens; remove it` | A |
| `credential_encryption_key` | string | env `MCPPROXY_CRED_KEY` | Restart | unchanged (fallback moves to `ApplyDefaults`) | A |
| ~~`max_user_servers`~~ | — | — | never reported | **removed**; server-build normaliser drops it from the raw map before the typed decode and records a `LoadDiagnostic` (the loader has no logger; `config.LogLoadDiagnostics` emits them once after the logger exists — `main.go` and the reload path) with `server_edition.max_user_servers is no longer supported and was ignored`; PATCH/`/config/apply` refuse with the same text **from the raw-document check `ValidateRemovedKeys`** run on the generic map before typed decoding (`json.Unmarshal` into `Config` drops unknown keys, so `Config.Validate` never sees them) | A |
| ~~`workspace_idle_timeout`~~ | — | — | never reported | **removed**; `server_edition.workspace_idle_timeout is no longer supported and was ignored` | A |
| `oauth.provider` | `google`\|`github`\|`microsoft`\|`oidc` | — | Restart ("server_edition.oauth.* is bound at login handler construction") | `server_edition.oauth.provider must be one of: google, github, microsoft, oidc (got: %s)` | B |
| `oauth.client_id`, `oauth.client_secret` | string (`${env:}`) | — | Restart | required; secret masked by `oauth.RedactedConfig` | (exists) |
| `oauth.tenant_id` | string | `common` (microsoft) | Restart | microsoft only | (exists) |
| `oauth.allowed_domains` | []string | `[]` = allow all | Restart | case-insensitive match (unchanged) | (exists) |
| `oauth.issuer_url` | string | — | Restart | required for `oidc`: `server_edition.oauth.issuer_url is required when provider is oidc` ; must be `https`, or `http` only when host is loopback **and** `allow_insecure_issuer: true`: `server_edition.oauth.issuer_url must use https (http is allowed only for a loopback host with allow_insecure_issuer: true)` ; byte-compared with the discovery `issuer` at login (`discovery_failed` on mismatch) | B |
| `oauth.allow_insecure_issuer` | bool | `false` | Restart | see above; non-loopback `http` refused regardless. **Raw-JSON-only** (no Settings row by decision — a loopback-only development toggle) | B |
| `oauth.scopes` | []string | `["openid","profile","email"]` | Restart | `openid` appended if missing (ApplyDefaults) | B |
| `oauth.groups_claim` | string | `"groups"` | Restart | non-empty for `oidc` | B |
| `oauth.email_verified_policy` | `refuse_false`\|`require_true`\|`ignore` | `refuse_false` | Restart | `server_edition.oauth.email_verified_policy must be one of: refuse_false, require_true, ignore` | B |
| `oauth.display_name` | string | provider family name | Restart | ≤ 64 chars; returned by `GET /api/v1/auth/provider` | B |
| `access` | object \| absent | absent (= today's `Shared`-only semantics) | **Live** (`access.*` read through `ServerEditionConfigProvider` on every authentication; `DetectConfigChanges` reports `server_edition.access`) | present with empty `group_servers` → every tenant gets only `default_servers` (fail closed) | C |
| `access.group_servers` | map[string][]string | `{}` | Live | non-empty map requires `oauth.provider: oidc`: `server_edition.access.group_servers requires oauth.provider "oidc" (legacy providers yield no groups)` ; each value a valid server name or `"*"`: `server_edition.access.group_servers[%q] contains an invalid server name %q` ; unknown names → boot warning + `doctor` finding only | C |
| `access.default_servers` | []string | absent/`null`/`[]` = no default grant | Live | same name rule; nil-vs-`[]` round trip is semantics-neutral by definition | C |

## Top-level (N)

| Key | Type | Default | Env | Live/Restart | Validation | PR |
|---|---|---|---|---|---|---|
| `trusted_proxies` | []string (CIDR or IP) | `[]` (trust nobody) | `MCPPROXY_TRUSTED_PROXIES` (comma list) | Live (`slices.Equal` clause; `ChangedFields: trusted_proxies`; every reader — session store, callback, connector base URL, swagger, request-metadata tag — evaluates a `func() []string` provider per request, never a captured slice) | each entry parses as CIDR or IP: `trusted_proxies[%d] %q is not a valid CIDR or IP address` — `validateTrustedProxies` reached from `Config.Validate`/`ValidateDetailed` (boot, PATCH, apply) | B |
| `audit_log` | object | personal: `{enabled:false}`; server with block absent: `{enabled:true, stdout:true, path:""}` (+ one startup line) — **under the native stdio transport** the stdout sink is never used (stdout carries JSON-RPC): with the block **absent** the server-edition default resolves to `{enabled:false}` with one WARN `audit_log.stdout is ignored under the stdio transport; set audit_log.path` (only the default is suppressed); an **explicit** `enabled: true, stdout: true` with no `path` is a sink-construction failure, `StartupError` exit 4 `audit_log.stdout cannot be used under the stdio transport (stdout carries JSON-RPC); set audit_log.path` (FR-014: an explicit value always wins — it is refused, never silently disabled); an explicit `path` is honoured and a `stdout: true` beside it is dropped with the WARN | — | Restart ("audit_log is bound at sink construction"; `jsonEqual` clause) | `enabled` with neither `stdout` nor `path`: `audit_log is enabled but has no sink (set stdout: true or a path)` — `validateAuditLog` reached from `Config.Validate`/`ValidateDetailed` (boot, PATCH, apply) | D |
| `audit_log.enabled` | bool | see above | `MCPPROXY_AUDIT_LOG_ENABLED` | Restart | explicit `false` under the server edition → startup warning "audit attribution is off" | D |
| `audit_log.path` | string | `""` | `MCPPROXY_AUDIT_LOG_PATH` | Restart | uncreatable at boot → exit code 4: `audit_log.path %q cannot be opened for append: %v` (a typed `config.StartupError{ExitCode: 4}` matched by `errors.As` in `classifyError` before its string heuristics — a bare `permission denied` message would otherwise classify as exit 5, `main.go:869-873`) | D |
| `audit_log.stdout` | bool | server: `true` when block absent | `MCPPROXY_AUDIT_LOG_STDOUT` | Restart | — | D |
| `audit_log.max_size_mb` | int | `50` | — | Restart | > 0 when a path is set: `audit_log.max_size_mb must be positive` | D |
| `audit_log.max_backups` | int | `10` | — | Restart | > 0 when a path is set | D |
| `audit_log.max_age_days` | int | `90` | — | Restart | > 0 when a path is set | D |
| `audit_log.compress` | bool | `true` | — | Restart | — (wire type `*bool` so an omitted value takes the default and an explicit `false` wins; every `audit_log.max_*` is `*int` for the same reason — explicit `0` is a validation error, omitted is the default) | D |
| `require_mcp_auth` | bool | `false` | (exists) | Live | under the server edition the *effective* value is `true` regardless (`config.EffectiveRequireMCPAuth`); explicit `false` → boot notice `require_mcp_auth: false is overridden to true because server_edition.enabled is true` + `doctor` finding; not an error | B |

## Per-server `auth_broker` (S)

| Key | Change | PR |
|---|---|---|
| `mode` | accepted set becomes `{oauth_connect}`; `token_exchange`/`entra_obo` → normaliser drops the **whole** `auth_broker` block of that server with `auth_broker.mode %q was never implemented; the auth_broker block for server %q was ignored`; PATCH/`/config/apply` refuse with the same text from the raw-document check (see `max_user_servers`) | A |
| `header`, `header_format` | removed; normaliser drops with `auth_broker.header is no longer supported and was ignored` (same for `header_format`) | A |
| `authorization_endpoint`, `token_endpoint`, `client_id`, `client_secret`, `scopes`, `resource` | unchanged (live connect flow) | — |

## Personal build (N behaviour of S blocks)

`server_edition` and each `auth_broker` are `json.RawMessage` carriers: preserved key-for-key and value-for-value through load → save → PATCH (with `UseNumber`) → save; no warning, no normalisation, no validation (FR-040). Fixture comparator: independent `UseNumber` structural equality with `9007199254740993` and `0.1000000000000000055511151231257827` planted.

## `DetectConfigChanges` clauses to add (`internal/runtime/config_hotreload.go`)

| Clause | Comparison | Result | PR |
|---|---|---|---|
| `server_edition.enabled`, `oauth.*`, `public_url`, `session_cookie_secure`, `session_ttl`, `bearer_token_ttl`, `credential_encryption_key` | `jsonEqual` on a projection struct (server build; the personal build compares the raw bytes and reports `server_edition` as changed-and-restart-pinned when they differ) | `RequiresRestart=true`, `RestartReason="server_edition settings are bound at startup"`, `ChangedFields+="server_edition"` | B |
| `server_edition.admin_emails` | `slices.Equal` | `ChangedFields+="server_edition.admin_emails"` (live) | B |
| `server_edition.access` | `jsonEqual` | `ChangedFields+="server_edition.access"` (live) | C |
| `trusted_proxies` | `slices.Equal` | `ChangedFields+="trusted_proxies"` (live) | B |
| `audit_log` | `jsonEqual` | `RequiresRestart=true`, `RestartReason="audit_log is bound at sink construction"` | D |
| dropped keys (`max_user_servers`, …) | never compared | not reported | A |

## Settings catalogue rows (`frontend/src/views/settings/fields.ts`)

`SERVER_EDITION_FIELDS` (no row for `oauth.allow_insecure_issuer` — Raw-JSON-only): remove `server_edition.max_user_servers`; `oauth.provider` options `['', 'google','github','microsoft','oidc']`; add `oauth.issuer_url` (text, `valueKind:'url'`, restart), `oauth.scopes` (textarea comma list, restart), `oauth.groups_claim` (text, restart), `oauth.email_verified_policy` (select, restart), `oauth.display_name` (text, restart), `public_url` (text, url, restart), `session_cookie_secure` (select `auto|true|false`, restart); never `client_secret`. New `AUDIT_LOG_FIELDS` accordion: `audit_log.enabled` (toggle, restart), `audit_log.stdout` (toggle, restart), `audit_log.path` (text, restart), `audit_log.max_size_mb|max_backups|max_age_days` (number, restart), `audit_log.compress` (toggle, restart). `trusted_proxies` (textarea, one CIDR per line, live) in the Security section. The `access` map is Raw-JSON only. Swift `SettingsCatalog.swift` gains read-only rows for `audit_log.*` and `trusted_proxies` (parity check, research D12).

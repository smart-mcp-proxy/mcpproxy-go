# Phase 0 Research: Settings Page

## D1 — Partial config update that preserves secrets

**Decision**: New `PATCH /api/v1/config` handler. Steps: `cfg := controller.GetConfig()` (the **real** in-memory config, secrets intact — redaction lives only in `handleGetConfig`'s response path, server.go:1126/3552), marshal `cfg`→`map[string]any`, decode the request body→`map[string]any`, `deepMergeJSON(base, patch)` (patch scalar/array values overwrite; nested objects merge recursively), marshal merged→bytes→`json.Unmarshal` into a fresh `config.Config`, then `controller.ApplyConfig(&merged, controller.GetConfigPath())`. Return the existing `ConfigApplyResult`.

**Rationale**: Starting from the real config and overlaying only the keys the client sent means (a) untouched fields keep their live values, and (b) masked secrets (api_key, secret request headers) are never round-tripped from a redacted read back to disk — the FR-003 / Constitution-IV requirement. Reuses the full validate→persist→hot-reload→restart-detection pipeline; zero duplication. Direct copy of the `handlePatchDockerIsolation` shape, generalised.

**Alternatives**: (a) full-config `POST /apply` from the form — rejected: redacted `GET /config` would persist `***REDACTED***` over the real key. (b) per-field endpoints like docker-isolation for every option — rejected: dozens of one-off handlers; the generic deep-merge is one handler.

**Merge edge cases**: arrays replace wholesale (e.g. `strip_classes`, `admin_emails`) — correct for settings. No key-removal semantics needed (settings only set values). Unknown keys: `json.Unmarshal` into typed `config.Config` ignores them, and `ApplyConfig` validates — so a bad patch fails validation rather than corrupting config.

## D2 — Restart-required fields

**Decision**: Trust the server: surface `ConfigApplyResult.RequiresRestart` + `RestartReason` + `ChangedFields` after each save. Additionally, statically badge the known restart-only set in the UI **before** save so users aren't surprised: `listen`, `data_dir`, `api_key`, `tls.*` (authoritative per `internal/runtime/config_hotreload.go`). Everything else hot-reloads (some need upstream reconnect, noted as "applies on reconnect").

## D3 — Field metadata catalogue

**Decision**: Drive the form from a declarative TS catalogue (one entry per setting: `key` (dot-path), `label`, `help`, `control` (toggle|select|number|text|secret|duration|list|multiselect), `section`, `options`/`min`/`max`, `danger?`, `restart?`). Sourced from the config inventory. This keeps Settings.vue declarative, makes `data-test` ids systematic, and makes "every non-deprecated option reachable" auditable. Hidden keys: `top_k`, `enable_tray`, `features`/`enable_web_ui`, telemetry bookkeeping (`anonymous_id`, …), `mcpServers`, `registries`.

**Security section (FR-002, ordered):** `api_key` (secret), `require_mcp_auth`, `quarantine_enabled`, `docker_isolation.enabled`, `enable_code_execution`, `read_only_mode`, `sensitive_data_detection.enabled`, `reveal_secret_headers` (danger), `listen` (restart). Danger set requiring confirm: `reveal_secret_headers`, `disable_management`, `quarantine_enabled→false`, non-loopback `listen`.

**General:** `routing_mode` (select), `tools_limit` (1–1000), `tool_response_limit`, `call_tool_timeout` (duration), `logging.level` (select), `telemetry.enabled`, `enable_prompts`.

**Advanced accordions:** code execution, docker isolation (detail), sensitive-data detection (scan toggles + categories + custom patterns), output validation, output sanitisation, activity retention, logging (file/rotation), TLS (restart), tokenizer, intent declaration, environment, security scanner, misc root.

## D4 — Verification via Chrome extension

**Decision**: Drive the live Web UI through the Chrome extension MCP tools (navigate, read_page/find for `data-test` selectors, computer for clicks/screenshots): capture Security/General/Advanced/RawJSON, exercise a toggle-save (e.g. quarantine) confirming persistence via re-read, and a danger-confirm flow. If the extension is unavailable at run time, fall back to the documented Playwright sweep. QA artifacts are produced for review but **not committed** (project policy).

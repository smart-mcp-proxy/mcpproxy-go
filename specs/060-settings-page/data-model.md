# Phase 1 Data Model: Settings Page

## API contract — `PATCH /api/v1/config`

**Request**: a partial config object (any subset of the `mcp_config.json` shape). Examples:
```json
{ "quarantine_enabled": false }
{ "docker_isolation": { "enabled": true } }
{ "logging": { "level": "debug" }, "telemetry": { "enabled": false } }
```
**Response** (`200`): `contracts.ConfigApplyResult` — same as `/config/apply`:
```json
{ "success": true, "applied_immediately": true, "requires_restart": false,
  "restart_reason": "", "changed_fields": ["quarantine_enabled"], "validation_errors": [] }
```
**Errors**: `400` invalid JSON; `400`/`200`+validation_errors on invalid values (via ApplyConfig); `500` read/apply failure. Auth: `ApiKeyAuth` / `ApiKeyQuery` (same as other config routes).

**Invariant**: fields absent from the request keep their live values; secrets (api_key, secret headers) are never modified by a request that doesn't include them.

## Frontend — SettingField (TS metadata)

| field | meaning |
|-------|---------|
| `key` | dot-path into config (e.g. `docker_isolation.enabled`) |
| `label` / `help` | human text |
| `control` | `toggle` \| `select` \| `number` \| `text` \| `secret` \| `duration` \| `list` \| `multiselect` |
| `section` | `security` \| `general` \| advanced-subsystem id |
| `options` | for select/multiselect |
| `min`/`max`/`step` | for number |
| `danger` | requires confirm dialog |
| `restart` | badge "requires restart" |
| `placeholder`/`mask` | for text/secret |

## Frontend — SaveState (per section)

`{ dirtyKeys: Set<string>, saving: bool, lastResult: ConfigApplyResult | null }`. On save: build a nested partial object from `dirtyKeys` only → `api.patchConfig(partial)` → toast + render `requires_restart`/`changed_fields`; clear dirty.

## Components

- `Settings.vue` — tab shell + load `GET /config` once; routes tabs.
- `SettingsSection.vue` — renders a list of `SettingField`s for a section + a Save button (`data-test="settings-apply-<section>"`).
- Control atoms: `SettingToggle`, `SettingSelect`, `SettingNumber`, `SettingText` (secret/show), each emitting changes + carrying `data-test="setting-<control>-<key>"`.
- `ConfirmDialog.vue` — danger confirm.
- `RestartBadge.vue` — restart-required marker.
- Raw JSON tab = existing Monaco editor (unchanged behaviour).
- `TeamsSection.vue` — rendered only when `config.teams` present.

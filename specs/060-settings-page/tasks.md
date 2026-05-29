# Tasks: UX-Friendly Settings Page

**Feature**: `060-settings-page` | **Spec**: [spec.md](./spec.md) | **Plan**: [plan.md](./plan.md)

TDD on the backend; frontend verified via Chrome extension.

## Phase 1: Setup
- [x] T001 Baseline green: `go build ./cmd/mcpproxy`, `cd frontend && npm run build` (or `make build`) pass before changes.

## Phase 2: Backend — partial-update endpoint (blocks frontend save)
- [x] T002 [P] Failing test `internal/httpapi/server_config_patch_test.go`: PATCH /config with `{"quarantine_enabled":false}` applies only that field; an untouched secret (api_key, secret header) is preserved; nested merge (`{"docker_isolation":{"enabled":true}}`) keeps sibling docker fields; invalid value returns validation error; `changed_fields` reflects the patch.
- [x] T003 Implement `handlePatchConfig` + `deepMergeJSON` helper in `internal/httpapi/server.go` (GetConfig→map→merge patch→unmarshal→ApplyConfig), modeled on `handlePatchDockerIsolation`. Register route `PATCH /api/v1/config`. Make T002 pass.
- [x] T004 Regenerate swagger (`make swagger`) for the new endpoint; `./scripts/verify-oas-coverage.sh` green.

## Phase 3: Frontend foundation (US1 enablement)
- [x] T005 [P] Add `patchConfig(partial)` to `frontend/src/services/api.ts` (PATCH /api/v1/config).
- [x] T006 [P] Field-metadata catalogue `frontend/src/views/settings/fields.ts` (SettingField[] per data-model: key/label/help/control/section/options/min/max/danger/restart; hidden keys excluded).
- [x] T007 Control atoms in `frontend/src/components/settings/`: `SettingToggle.vue`, `SettingSelect.vue`, `SettingNumber.vue`, `SettingText.vue` (secret/show), `RestartBadge.vue`, `ConfirmDialog.vue` — each with `data-test` ids. (frontend-design skill.)
- [x] T008 `SettingsSection.vue` — renders fields for a section + per-section Save (builds nested partial from dirty keys → `patchConfig`), toast + restart/changed-fields feedback, danger-confirm gating.

## Phase 4: US1 — Security & Access (P1, MVP) 🎯
- [x] T009 [US1] Redesign `frontend/src/views/Settings.vue` into a tab shell (Security/General/Advanced/Raw JSON/Teams), loading `GET /config` once; keep existing Monaco editor as the Raw JSON tab.
- [x] T010 [US1] `SecuritySection.vue` with the 9 ordered fields; api_key masked + regenerate; danger confirms for reveal_secret_headers / disable_management / quarantine-off / non-loopback listen; restart badges. Save persists only changed fields.

## Phase 5: US2 — General (P2)
- [x] T011 [US2] `GeneralSection.vue`: routing_mode, tools_limit (1–1000), tool_response_limit, call_tool_timeout, logging.level, telemetry.enabled, enable_prompts; number-bound validation before save.

## Phase 6: US3 — Advanced + Raw JSON + Teams (P3)
- [x] T012 [US3] `AdvancedSection.vue` with DaisyUI accordions per subsystem (code execution, docker isolation detail, sensitive-data detection incl. categories + custom patterns, output validation, output sanitisation, activity retention, logging, TLS, tokenizer, intent, environment, scanner, misc).
- [x] T013 [US3] Verify Raw JSON tab retains validate+apply behaviour; `TeamsSection.vue` rendered only when `config.teams` present (personal edition shows no Teams tab).

## Phase 7: Polish & Verify
- [x] T014 [P] `cd frontend && npm run build` clean (TS6/Vite8); `go test ./internal/httpapi/... ./internal/runtime/... -count=1`; `./scripts/test-api-e2e.sh` green.
- [x] T015 Docs: `docs/` settings page note; cross-link from config docs. (CLAUDE.md only if under 40k.)
- [x] T016 **Mandatory verification (Chrome extension)**: drive live Web UI — capture Security/General/Advanced/Raw JSON; toggle quarantine + save + re-read to confirm persistence + secret preserved; danger-confirm flow. Build a local QA report (NOT committed, per policy); end with `open <report>`.

## Dependencies
- T002–T004 (backend) block T008's save path.
- T005–T008 foundation block US1/US2/US3.
- US1 is the MVP; US2/US3 extend the same section framework.
- T010's tab shell (T009) is shared by all sections — build once.

## Parallel opportunities
- Backend (T002–T004) ∥ frontend foundation (T005–T007) — different stacks. Dispatch backend to a subagent while building the frontend.
- T005 ∥ T006 (different files).

## MVP scope
US1 (T009–T010) + backend PATCH (T002–T003) + foundation (T005–T008): a working, secure Security & Access form with partial saves.

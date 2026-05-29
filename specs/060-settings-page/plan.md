# Implementation Plan: UX-Friendly Settings Page

**Branch**: `060-settings-page` | **Date**: 2026-05-29 | **Spec**: [spec.md](./spec.md)
**Input**: `/specs/060-settings-page/spec.md`

## Summary

Redesign the Web UI Settings page from a single raw Monaco JSON editor into a prioritized, form-based experience: **Security & Access** → **General** → **Advanced** (subsystem accordions) → **Raw JSON** (the existing editor, kept) → **Teams** (server edition only). Add one backend endpoint — `PATCH /api/v1/config` — that deep-merges a partial payload onto the live config and routes through the existing `ApplyConfig` pipeline, so saving a section persists only changed fields and never clobbers masked secrets (api_key, secret headers). Everything else reuses the existing `GET /config` (read, redacted) and `POST /config/apply`/`validate`.

## Technical Context

**Language/Version**: Go 1.24 (backend), TypeScript 5.9 / Vue 3.5 (frontend)
**Primary Dependencies**: Chi router, Zap, existing `Runtime.ApplyConfig` + `Controller.GetConfig`/`GetConfigPath`; Vue 3, Pinia, Tailwind + DaisyUI, Monaco (existing, for Raw JSON tab)
**Storage**: None new — config in `mcp_config.json` via existing apply pipeline; no DB/schema change
**Testing**: `go test ./internal/httpapi/... ./internal/runtime/...`; Chrome-extension browser automation for the Web UI (mandatory)
**Target Platform**: Core server (personal + server editions); Web UI served at `/ui/`
**Project Type**: Web (Go backend + Vue frontend)
**Performance Goals**: N/A (config path, not hot path)
**Constraints**: Partial save MUST preserve secrets (FR-003); restart-only fields flagged (FR-007); personal edition unaffected by Teams (FR-010)
**Scale/Scope**: 1 new backend handler + ~1 new frontend api method + Settings.vue redesign into sectioned components

## Constitution Check

- **I. Performance at Scale** — N/A (config UI/endpoint).
- **II. Actor-Based Concurrency** — PASS. Reuses `ApplyConfig`; no new concurrency.
- **III. Configuration-Driven Architecture** — PASS / core to this feature. All settings remain in `mcp_config.json`; changes go through the existing validate→persist→hot-reload pipeline; the tray/Web-UI remain pure controllers reading/writing core config via REST. Sensible defaults preserved.
- **IV. Security by Default** — PASS / reinforces. Partial-update PATCH starts from the **real** in-memory config (secrets intact) and overlays only changed fields, so the redaction-on-read can never round-trip a `***REDACTED***` back to disk. Dangerous toggles gated by confirm dialogs.
- **V. TDD** — PASS. Backend gets a failing test first (partial merge preserves untouched fields incl. secrets; only changed fields in `ChangedFields`; restart-required reported).
- **VI. Documentation Hygiene** — PASS. Update `docs/` + swagger (auto-regen); CLAUDE.md note only if room (40k gate).

No violations → Complexity Tracking empty.

## Key technical decisions (see research.md)

- **PATCH semantics**: `GetConfig()` → marshal to map → deep-merge incoming partial map (patch wins, objects merged recursively) → unmarshal to `config.Config` → `ApplyConfig`. Starting from the real config is what preserves secrets and unrelated fields. Modeled on `handlePatchDockerIsolation` (server.go:3685) + the per-server PATCH deep-merge precedent.
- **Secrets**: never sent by the form unless the user explicitly edits them; the deep-merge means an absent key keeps the live value.
- **Restart classification**: surfaced from the existing `ConfigApplyResult.RequiresRestart`/`RestartReason`/`ChangedFields`; UI also statically badges the known restart-only fields (listen, data_dir, api_key, tls.*).
- **Edition gating**: Teams section rendered only when the config payload contains a `teams` object (absent in personal-edition binary).

## Project Structure

```text
specs/060-settings-page/
├── plan.md, research.md, data-model.md, quickstart.md, tasks.md
└── checklists/requirements.md

internal/httpapi/
├── server.go              # NEW handler handlePatchConfig + route PATCH /api/v1/config; helper deepMergeJSON
└── server_config_patch_test.go  # NEW: partial-merge preserves secrets + untouched fields (TDD)

oas/                       # regenerated (swagger) for the new endpoint

frontend/src/
├── services/api.ts        # NEW patchConfig(partial) method
├── views/Settings.vue     # REDESIGN: tabbed shell (Security/General/Advanced/RawJSON/Teams)
└── components/settings/    # NEW: SettingsSection, SettingToggle, SettingSelect, SettingNumber,
                            #      SettingText (masked/secret), ConfirmDialog, RestartBadge,
                            #      SecuritySection, GeneralSection, AdvancedSection (accordions), TeamsSection
```

**Structure Decision**: Web app. Backend change is one handler + a small deep-merge helper. Frontend keeps the existing `/settings` route and Monaco editor (as the Raw JSON tab) and adds form sections as child components driven by a field-metadata table (label, help, control, bounds, danger, restart) so controls stay declarative and testable.

## Phases

- **Phase 0 (research.md)**: deep-merge approach + secret-preservation proof; restart-field list; field-metadata catalogue (from the config inventory); Chrome-ext verification approach.
- **Phase 1 (data-model.md, quickstart.md, contracts/)**: PATCH request/response contract; settings field-metadata model; quickstart to enable + verify.
- **Phase 2 (tasks.md)**: via /speckit.tasks.

## Complexity Tracking

*No constitution violations — section intentionally empty.*

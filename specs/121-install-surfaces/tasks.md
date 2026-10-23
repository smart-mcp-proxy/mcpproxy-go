# Tasks: Zero-Friction Install Surfaces (Spec 121)

Order = dependency order. `[T]` = write the failing test first. `[P]` = parallelizable (different files).

## Phase 1 — Detector + telemetry (Go, US2.6, US4)

- [ ] T001 [T] `internal/updatecheck/channel_scoop_test.go`: scoop paths (`C:\Users\u\scoop\apps\mcpproxy\current\mcpproxy.exe`, `\scoop\persist\`, forward-slash and case variants) → `scoop`; tarball marker + scoop path → `scoop`; non-scoop windows path stays `tarball`.
- [ ] T002 `internal/updatecheck/channel.go`: add `ChannelScoop`, `knownChannels`, path heuristic before the tarball fallback (normalize `\`→`/`, lowercase compare).
- [ ] T003 [T] `guidance_test.go` + `cmd/mcpproxy/update_cmd_test.go`: `UpdateCommand("scoop")=="scoop update mcpproxy"`, `decideAction(scoop)==actionCommand`, existing channels' outputs unchanged (table of all old channels).
- [ ] T004 `guidance.go`, `update_cmd.go` (+help text line 141 area): scoop command/action.
- [ ] T005 [P] `frontend/src/components/UpdateBanner.vue`, `cmd/generate-types/main.go` + `frontend/src/types/contracts.ts`, `internal/contracts/types.go` doc comments: add scoop to the channel lists; run `make swagger`/type generation if comments feed it; frontend unit test if banner has one.
- [ ] T006 [T] `internal/telemetry/install_channel_test.go`: mapper totality (every `updatecheck` channel const + garbage → enum), precedence table (env > exe marker > detector > weak data-dir marker), weak marker ignored for brew/dmg/winget/deb/rpm/docker/windows-installer/scoop, accepted for tarball/go-install/unknown, invalid/oversized/unreadable marker falls through, env `unknown` ignored, env `registry` over detector `docker`, scoop with no declaration.
- [ ] T007 `internal/telemetry/install_channel.go`: types, `AllInstallChannels`, `MapDetectorChannel`, `ResolveInstallChannel` with injectable deps, 64-byte capped reader; `sync.Once` cache wrapper for production.
- [ ] T008 [T] payload tests: `install_channel` present and in enum for enabled telemetry; absent/unread when disabled (incl. preview path); privacy canary (marker content `C:\Users\secret`, `../../etc`, 10 KB garbage never appear in the serialized payload); anonymity scanner accepts every enum value.
- [ ] T009 `telemetry.go` (+ `anonymity.go`): field + wiring in `BuildPayload` on the enabled path only. No `SchemaVersion` change; assert existing schema pins untouched.
- [ ] T010 `docs/features/telemetry.md` (field, enum, precedence, privacy, E7 query hint), `docs/configuration.md` env var `MCPPROXY_INSTALL_CHANNEL`.

## Phase 2 — Registry (US1)

- [ ] T020 [T] `scripts/stamp-server-json.test.sh` (fixtures): stamps `.version` (no `v`) and `.packages[0].identifier` tag (with `v`); refuses an input with no `packages`; idempotent.
- [ ] T021 `scripts/stamp-server-json.sh`.
- [ ] T022 `server.json`: add package from `contracts/server-package.json`; `Dockerfile` + `release.yml` build-docker `labels:`: `io.modelcontextprotocol.server.name`.
- [ ] T023 [T] `cmd/release-gate/distribution_workflow_test.go`: (a) server.json name == Dockerfile label == release.yml label; (b) `mcp-registry` transitively needs `build-docker` and `release`, has the stable-only guard and `continue-on-error`; (c) both registry jobs call `stamp-server-json.sh` and `registry-preflight.sh`; (d) no registry/winget/scoop job runs for `-` tags; (e) server.json passes `mcp-publisher validate` when the binary is present (skip otherwise).
- [ ] T024 `scripts/registry-preflight.sh <tag>` (anonymous pull token, config label check, `docker run` smoke with declared env/port, MCP `initialize` via curl, cleanup, clear failure causes; `--skip-smoke` for environments without docker).
- [ ] T025 `release.yml` + `retry-sign-release.yml` mcp-registry jobs: stamp → preflight → validate → publish; drop inline `jq`.
- [ ] T026 `docs/mcp-registry-publishing.md`, `docs/release-runbook.md`: packages, label, preflight, immutability.

## Phase 3 — Windows (US2)

- [ ] T030 [T] `scripts/gen-scoop-manifest.test.sh` with a fixture `checksums.txt` (v0.71.1 real hashes): output valid JSON, correct URLs/hashes for both arches, `bin`, `checkver`, `autoupdate`, fails on missing asset hash.
- [ ] T031 `scripts/gen-scoop-manifest.sh`.
- [ ] T032 `scripts/installer.iss`: `/CHANNEL=` handling (accept only `winget`), marker write/stale delete, `[UninstallDelete]`, `NeedsAddPath` MsgBox guarded by `WizardSilent`; keep file ASCII; document manual compile check.
- [ ] T033 `.github/workflows/publish-windows-packages.yml` (reusable + dispatch): scoop job (token-gated, idempotent commit), winget job (SHA-pinned action, package-exists + token gate); both stable-only; extend T023 to assert these.
- [ ] T034 `release.yml` and `retry-sign-release.yml`: call the reusable workflow after `release` (non-blocking: `continue-on-error`).
- [ ] T035 `packaging/windows/winget/` templates (3 YAML files) + `docs/operations/windows-package-managers.md` (install/upgrade/uninstall commands; one-time human checklist: create `smart-mcp-proxy/scoop-bucket`, create fine-grained PAT → `SCOOP_BUCKET_TOKEN`, fork `microsoft/winget-pkgs`, classic PAT → `WINGET_TOKEN`, first `wingetcreate new` submission).
- [ ] T036 `docs/getting-started/installation.md`: winget/scoop sections.

## Phase 4 — Agent install + llms (US3)

- [ ] T040 [T] `scripts/sync-agent-prompt.test.sh`: `--check` fails on drift between README block and docs page; passes after sync.
- [ ] T041 `docs/getting-started/agent-install.md` (prompt source of truth; no secrets; verification; Claude Desktop → wizard), `scripts/sync-agent-prompt.sh`, README block between markers; wire `--check` into `unit-tests.yml`/lint job.
- [ ] T042 `website/sidebars.js` entry (first under Getting Started); confirm `include` covers it; run `npm run build` (broken-link guard).
- [ ] T043 [T] `website/scripts/check-llms.mjs` (exists, install page first, excluded docs absent, size ≤ budget) with a negative self-test.
- [ ] T044 `website/docusaurus.config.js`: `includeOrder`/`ignoreFiles` for the llms plugin; measure size; set budget; `package.json` `postbuild`.
- [ ] T045 Verify prompt end-to-end in an isolated environment (scratch HOME, port 18xxx, scratch data/config); record redacted evidence in `quickstart.md`/PR.

## Phase 5 — Wrap-up

- [ ] T050 `CLAUDE.md` Recent Changes entry (keep size < 40k).
- [ ] T051 Lint bare + `--build-tags server`; `go test -race ./internal/...` (CI skip regex for server pkg); `./scripts/test-api-e2e.sh` (check no foreign cores first); docs build; frontend tests if touched.
- [ ] T052 Sol review of the final diff (chunked); verify findings; record in `review-log.md`.
- [ ] T053 PR with title "feat(distribution): registry packages, winget/scoop, agent install prompt, install-channel telemetry (E7)", reviewer verdicts, human-steps checklist, receiver/dashboard follow-up note.

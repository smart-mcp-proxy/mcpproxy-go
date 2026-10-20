# Implementation Plan: Zero-Friction Install Surfaces (E7)

**Branch**: `121-install-surfaces` | **Date**: 2026-10-20 | **Spec**: [spec.md](spec.md)

## Summary

Five small, mostly non-Go changes plus one narrow Go change:
(A) `server.json` OCI package + image ownership label + one shared stamp/preflight script used by both registry publishers;
(B) a reusable Windows-packages workflow (winget via a SHA-pinned winget-releaser action, scoop via a generated manifest pushed to a bucket repo) fed by `checksums.txt`, an Inno installer that records a `winget` channel marker and is silent-safe, and a `scoop` channel in the update-channel detector;
(C) install-first tuning of the existing docusaurus-plugin-llms output, a post-build check, and a single-source "Paste this into your agent" prompt (README + docs page, drift-tested);
(D) `install_channel` in the heartbeat via a pure resolver (env > exe marker > detector > data-dir marker) that maps detector IDs to a closed telemetry enum;
(E) follow-up note only (already written: `followup-mcpb.md`).

## Technical Context

**Language/Version**: Go 1.26 (telemetry, updatecheck), Bash + jq (stamp/manifest scripts), GitHub Actions YAML, Inno Setup Pascal script, Docusaurus 3 (Node 22)
**Primary Dependencies**: none new in Go. CI: `mcp-publisher` (already used), a SHA-pinned winget-releaser action (new, CI-only), `yq`/`jq` (runner-provided). Docs: existing `docusaurus-plugin-llms`.
**Storage**: marker files `install-channel` (one token, ≤64 bytes); no DB change.
**Testing**: Go table tests (`internal/telemetry`, `internal/updatecheck`), shell tests for `scripts/*` using fixtures (checksums, server.json), a Go test that parses `.github/workflows/*.yml` ordering/guard invariants (existing `cmd/release-gate` pattern), docs post-build check script, prompt-drift test.
**Target Platform**: macOS/Linux/Windows runners; Windows installer; docs site on Cloudflare Pages.
**Project Type**: single repo, mixed (Go + CI + docs).
**Performance Goals**: telemetry resolver runs once per heartbeat; marker read ≤ 64 bytes; zero startup cost.
**Constraints**: no new Go dependencies; privacy contract (closed enum only); release workflow must never fail because an optional distribution surface is unconfigured.
**Scale/Scope**: ~600 LoC Go+tests, ~300 LoC scripts, ~250 LoC workflow YAML, 3 docs pages.

## Constitution Check

| Principle | Status |
|---|---|
| I. Performance | N/A (no hot path); resolver is once-per-heartbeat. |
| II. Actor concurrency | N/A; pure function, no shared state. |
| III. Config-driven | No new config field (env var + marker only, deliberately not in `Config` — avoids the 5-point config checklist and keeps the value out of user-editable config). |
| IV. Security by default | Closed-enum validation; size-capped marker read; checksum-verified install prompt; no credentials in prompt/evidence; third-party action SHA-pinned; tokens only in repo secrets, jobs skip when absent. |
| V. TDD | Tests first for resolver, mapper, scoop detection, stamp script, manifest generator, drift/post-build checks. |
| VI. Documentation hygiene | Installation docs, telemetry.md, registry-publishing.md, release-runbook.md, new agent-install page + sidebar + include allowlist. |

No violations.

## Design decisions

### A. Registry
- `server.json.packages = [{registryType:"oci", identifier:"ghcr.io/smart-mcp-proxy/mcpproxy-server:v0.0.0", transport:{type:"streamable-http", url:"http://localhost:8080/mcp"}, runtimeHint:"docker", runtimeArguments:[-p 127.0.0.1:8080:8080], environmentVariables:[{name:"MCPPROXY_INSTALL_CHANNEL", value:"registry"}]}]`. Identifier tag is a placeholder pin stamped by `scripts/stamp-server-json.sh <tag>` (also sets `.version`).
- Dockerfile gains `LABEL io.modelcontextprotocol.server.name="io.github.smart-mcp-proxy/mcpproxy-go"`; `build-docker` `labels:` gets the same line (belt and braces; build-push labels override). Consistency guarded by a Go test (`cmd/release-gate` style) comparing server.json name, Dockerfile label and workflow label.
- `scripts/registry-preflight.sh <tag>`: anonymous token → manifest → config blob → label check; `docker run` smoke (declared env, loopback port) → MCP `initialize`; prints cause and exits non-zero. Both `release.yml` and `retry-sign-release.yml` mcp-registry jobs: stamp → preflight → `mcp-publisher validate` → publish; job stays `continue-on-error`.
- Ordering: `mcp-registry` already needs `release` which needs `build-docker` (verified `release.yml:1340`); a Go invariant test pins it. Retry workflow: add the same preflight; image already exists for recovered releases.
- Existing versions (≤0.71.x) stay package-less (registry immutability); first stable release after merge publishes packages.

### B. Windows
- New reusable workflow `.github/workflows/publish-windows-packages.yml` (`workflow_call` + `workflow_dispatch` with `tag`): jobs `scoop` and `winget`, both `if: !contains(tag,'-')`, each first checking its secret/repo is configured (else `::notice::` and exit 0).
- `scripts/gen-scoop-manifest.sh <tag> <checksums.txt>` → `mcpproxy.json` (version, per-arch `architecture.{64bit,arm64}.{url,hash}` from `mcpproxy-<ver>-windows-<arch>.zip`, `bin: [mcpproxy.exe]`, `shortcuts` for the tray, `checkver: github`, `autoupdate` with `$version` templates and a `hash` url to `checksums.txt`). Job commits it to `smart-mcp-proxy/scoop-bucket/bucket/mcpproxy.json` using `SCOOP_BUCKET_TOKEN`; idempotent (no diff → no commit).
- winget: SHA-pinned `vedantmgoyal9/winget-releaser` (wingetcreate wrapper), `identifier: smart-mcp-proxy.mcpproxy`, `installers-regex: 'mcpproxy-setup-v.*-(amd64|arm64)\.exe$'`, token `WINGET_TOKEN`; a checked-in template `packaging/windows/winget/` (YAML 3-file skeleton incl. `InstallerType: inno`, `Scope: machine`, `InstallerSwitches.Custom: /CHANNEL=winget`, `Silent: /VERYSILENT /SUPPRESSMSGBOXES /NORESTART`) is what the human uses for the one-time first submission (`wingetcreate new` flags in docs). Job only runs if the package exists in winget-pkgs (checked via the GitHub API), else notice.
- `release.yml` calls the reusable workflow after `release`; `retry-sign-release.yml` calls it too (post-release-only path).
- Inno (`scripts/installer.iss`): `{param:CHANNEL|}`-style handling in `[Code]`: write `{app}\install-channel` (value validated to `winget`) in `CurStepChanged(ssPostInstall)` if the param is present, else delete a stale one; `[UninstallDelete]` removes it; `NeedsAddPath`'s `MsgBox` guarded by `not WizardSilent`.
- Detector: `ChannelScoop="scoop"` in `knownChannels`; path heuristic (`\scoop\apps\mcpproxy\` or `\scoop\persist\`, case-insensitive, slash-normalized) before the tarball fallback; `UpdateCommand` → `scoop update mcpproxy`; `decideAction` → command; `UpdateBanner.vue` + generated types doc strings updated.

### C. Agent-readable install
- Single source `docs/getting-started/agent-install.mdx`? MDX import would complicate drift check; instead single source = `docs/getting-started/agent-install-prompt.txt`? Decision: prompt lives in `docs/getting-started/agent-install.md` fenced block (published page); README block is copied by `scripts/sync-agent-prompt.sh` (writes between markers) and a Go/shell test fails on drift (`scripts/sync-agent-prompt.sh --check`, wired into the unit-tests job).
- Page added to docusaurus `include` allowlist (it matches `getting-started/**` already) and `sidebars.js` (first item under Getting Started).
- llms: plugin options `includeOrder` puts `getting-started/agent-install` first; `ignoreFiles` for generated reference pages (`errors/**`, big API reference) to bound `llms-full.txt`; `website/scripts/check-llms.mjs` (post-build): files exist, agent-install first, excluded docs absent, size ≤ budget (set after measuring). Hooked into `npm run build` via a `postbuild` script and runs in `docs.yml`.
- Prompt content: detect OS; check `command -v mcpproxy` / existing instance first and stop to ask if present; macOS/Linux → `brew install smart-mcp-proxy/mcpproxy/mcpproxy` (cask on macOS desktop), Windows → `winget install smart-mcp-proxy.mcpproxy` else `scoop`, else release archive with `checksums.txt` verification and a data-dir marker `curl`; `mcpproxy serve` (background); verify with `mcpproxy doctor` / `mcpproxy upstream list`; `mcpproxy connect <client>`; tell user to reload the client; never read/print config or keys.

### D. Telemetry
- `internal/telemetry/install_channel.go`: `type InstallChannel string` + `AllInstallChannels()`; `ResolveInstallChannel(deps)` where deps injects env getter, exe path, file reader, data dir, detector func — pure and table-testable. `MapDetectorChannel(string) InstallChannel` is total (homebrew→brew, dmg, deb, rpm, docker, go-install, windows-installer, tarball, scoop, unknown→unknown, anything else→unknown).
- `HeartbeatPayload.InstallChannel string json:"install_channel,omitempty"`; filled in `BuildPayload` only on the enabled path; resolved once per process (cached via `sync.Once`, since channel cannot change while running) — preview path uses the same cached resolver but only when telemetry is enabled. No `SchemaVersion` bump (FR-030).
- Anonymity scanner (`anonymity.go`) allow-lists the field's closed values; `payload_privacy_test.go` gains a canary (marker content `C:\Users\secret` and an out-of-set token must not appear).
- `MCPPROXY_INSTALL_CHANNEL` documented in `docs/features/telemetry.md` and `docs/configuration.md` env table (env var only; no config field → config checklist N/A).

### E. Follow-up
`followup-mcpb.md` (done).

## Project Structure

```text
specs/121-install-surfaces/{spec,plan,research,data-model,quickstart,tasks,review-log,followup-mcpb}.md, contracts/, checklists/
server.json                         # + packages
Dockerfile                          # + LABEL
scripts/{stamp-server-json,registry-preflight,gen-scoop-manifest,sync-agent-prompt}.sh (+ tests)
scripts/installer.iss               # channel switch, silent-safe
packaging/windows/winget/           # one-time bootstrap templates
.github/workflows/{release,retry-sign-release,publish-windows-packages,docs}.yml
internal/updatecheck/{channel,guidance}.go (+tests)
internal/telemetry/{install_channel,telemetry,anonymity}.go (+tests)
cmd/mcpproxy/update_cmd.go (+test)   # scoop action
frontend/src/components/UpdateBanner.vue, frontend/src/types/contracts.ts, cmd/generate-types
website/{docusaurus.config.js,sidebars.js,scripts/check-llms.mjs,package.json}
docs/getting-started/{installation,agent-install}.md, docs/features/telemetry.md, docs/mcp-registry-publishing.md, docs/release-runbook.md, docs/operations/windows-package-managers.md, README.md
```

## Risks

- Winget-releaser/first-submission delays are human/Microsoft-bound → job skips cleanly; docs checklist.
- Inno changes cannot be executed on macOS CI; verified by installer.iss review + a Windows runner dry compile is out of reach locally → noted in PR as "needs first-release verification"; the `[Code]` change is minimal.
- Registry immutability: preflight is the only guard; smoke test uses the exact declared args.
- Merge collision with concurrent specs on `telemetry.go`/`release.yml`: kept additive and localized.

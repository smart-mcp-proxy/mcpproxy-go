# Implementation Plan: macOS TCC-safe Connect wizard & App-Data denial diagnostics

**Branch**: `075-macos-tcc-connect` | **Date**: 2026-06-17 | **Spec**: [spec.md](./spec.md)
**Input**: Feature specification from `/specs/075-macos-tcc-connect/spec.md`

## Summary

The Connect feature eagerly reads the **contents** of every installed MCP client config in `Service.GetAllStatus()` (`internal/connect/connect.go:135` → `findEntry` → `os.ReadFile`) to compute the `Connected` flag. On macOS 14+ this fires the "App Data" privacy prompt ("wants to access data from other apps") for every client, and a denial makes the reads fail silently forever.

Approach:
1. **Stat-only overall status**: `GetAllStatus` keeps using `os.Stat` for `Exists` but stops calling `findEntry`. `Connected` becomes a tri-state (`unknown` until explicitly checked). Add `GetStatus(clientID)` that performs the single-client content read on demand.
2. **Access-outcome classification**: a small helper classifies a config access into `accessible | absent | denied | malformed`, derived from `errors.Is(err, fs.ErrPermission)` / `syscall.EPERM`/`EACCES` (not string matching). Reads/writes in `connect`/`disconnect` and `findEntry` route through it.
3. **Actionable surfacing**: denied outcomes populate a new `AccessState` + `Remediation` field on `ClientStatus` and produce a typed error from connect/disconnect carrying the remediation string (System Settings path + `tccutil reset SystemPolicyAppData <bundle-id>`).
4. **Doctor check**: a macOS-only diagnostics check that detects a persisted App-Data denial affecting Connect and reports remediation; no-op on other platforms.

All TDD; the denied path is tested by injecting a permission error through a seam (a `configReader` func var / interface), no real OS denial required.

## Technical Context

**Language/Version**: Go 1.24 (toolchain go1.24.10)
**Primary Dependencies**: stdlib (`os`, `io/fs`, `errors`, `syscall`, `runtime`), `BurntSushi/toml` (existing), Cobra (existing, doctor command), Chi (existing, Connect REST). No new deps.
**Storage**: None new. Reads/writes existing on-disk client config files; no BBolt schema change.
**Testing**: `go test` unit tests in `internal/connect` and the diagnostics package; permission-denied path via injected reader/seam; existing Connect REST contract tests must still pass.
**Target Platform**: macOS 13+ (primary for the privacy behavior), Linux, Windows (behavior preserved).
**Project Type**: Single Go module (core server). Optional FE follow-up to render the new tri-state, tracked but not required for the backend MVP.
**Performance Goals**: Overall status must do **zero** content reads (only metadata) — strictly faster than today.
**Constraints**: Backward-compatible Connect REST payload (add fields only). Classification must use OS error class, not text. macOS check must be a no-op elsewhere.
**Scale/Scope**: ~8 supported clients; status called on Connect page load / wizard predicate (`GetConnectedCount`).

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-check after Phase 1 design.*

- **I. Performance at Scale**: PASS — change strictly reduces I/O on the status path (removes per-client content reads). No hot-path regression.
- **II. Actor-Based Concurrency**: PASS — no new concurrency; pure function changes + on-demand reads. No new locks.
- **III. Configuration-Driven Architecture**: PASS — no new config keys required; behavior is automatic. Tray remains a UI controller reading core REST.
- **IV. Security by Default**: PASS / reinforces — reduces unnecessary access to other apps' data (least privilege); no weakening of quarantine/isolation. The Docker probe (security-relevant) is untouched.
- **V. Test-Driven Development**: PASS — every change is red→green; denied path injected via seam; REST contract tests preserved.
- **VI. Documentation Hygiene**: PASS — docs updated (CLAUDE.md REST notes if payload fields added; a docs note on the macOS App-Data prompt + remediation).
- **Upstream Client Modularity / DDD layering**: N/A to `internal/connect`, which is a self-contained adapter package; change stays within it + the diagnostics package.

**Result**: No violations. Complexity Tracking not required.

## Project Structure

### Documentation (this feature)

```text
specs/075-macos-tcc-connect/
├── plan.md              # This file
├── research.md          # Phase 0 output
├── data-model.md        # Phase 1 output
├── quickstart.md        # Phase 1 output
├── contracts/           # Phase 1 output (REST status payload delta)
│   └── connect-status.md
└── tasks.md             # Phase 2 (/speckit.tasks)
```

### Source Code (repository root)

```text
internal/connect/
├── connect.go            # GetAllStatus (stat-only), new GetStatus(clientID), connect/disconnect route through classifier
├── access.go             # NEW: AccessOutcome classification (accessible/absent/denied/malformed) + remediation text
├── access_test.go        # NEW: classification unit tests incl. injected EPERM
├── connect_test.go       # extend: GetAllStatus does no content reads; GetStatus on-demand; denied surfacing
├── clients.go            # unchanged (path table); bundle IDs constant may live here or access.go
└── backup.go             # route os.Open through classifier so backup denial surfaces too

internal/diagnostics/      # (or internal/management/diagnostics.go — confirm in tasks)
├── tcc_appdata_darwin.go      # NEW: macOS App-Data denial check
├── tcc_appdata_other.go       # NEW: no-op build-tagged stub for !darwin
└── tcc_appdata_test.go        # NEW: check reports warn/pass; no-op off darwin

internal/httpapi/
└── connect.go            # GET /connect unchanged contract; map new ClientStatus fields into response
```

**Structure Decision**: Single Go module. All Connect logic stays inside the self-contained `internal/connect` adapter package; the doctor check lands in the existing diagnostics registry (exact package pinned during tasks). The REST handler (`internal/httpapi/connect.go`) only widens the JSON it maps from `ClientStatus`. An optional frontend follow-up (render tri-state + remediation banner) is noted but not part of the backend MVP and can ship separately.

## Complexity Tracking

> No constitution violations — section intentionally empty.

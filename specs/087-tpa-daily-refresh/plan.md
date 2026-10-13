# Implementation Plan: Daily Offline-First Refresh of the TPA Signature Bundle + mcpproxy Self-Update Awareness

**Branch**: `087-tpa-daily-refresh` | **Date**: 2026-07-16 | **Spec**: [spec.md](./spec.md)
**Input**: Feature specification from `/specs/087-tpa-daily-refresh/spec.md`

## Summary

Give the deterministic offline scanner (Spec 076/077) an updatable, versioned signature database with an offline-first refresh lifecycle. A known-good `scanner-bundle.json` is embedded into the binary via `go:embed`, so the offline tier has signatures with no network on first launch. A new non-`detect` package `internal/security/bundle` owns the file lifecycle the offline import-guard forbids `detect` from touching: load/validate the embedded default, an operator file-drop at a configured path, or an opt-in signature-verified network fetch; run every candidate through a **version + RE2-compile + recall/FP activation self-check** before an **atomic, single-flight** swap; and **fail safe to last-known-good** on any failure. The compiled rules are handed as in-memory data to a new pure `detect.Check` (`checks.BundleCheck`) added to the hardcoded check slice at `internal/security/scanner/inprocess.go:91-104`, so bundle hits (hard tier, fail-closed) drive the same quarantine gate as the built-in checks with no new verdict plumbing. A background daily refresher mirrors the existing `internal/updatecheck.Checker` lifecycle (Start/ticker/hot-reload) and, in the same daily cycle, reuses the Spec 079 update checker to surface — never install — a newer mcpproxy release. All behavior is config-driven under `security.bundle` with env override and hot-reload. The shipped embedded default must keep the existing CI eval gate green.

## Technical Context

**Language/Version**: Go 1.24
**Primary Dependencies**: stdlib only for the hot path (`embed`, `encoding/json`, `regexp` (RE2), `crypto/ed25519` + `crypto/sha256` for signature verification, `net/http` for the opt-in fetch, `sync`/`context`/`time` for the refresher); existing `internal/security/detect`, `internal/security/scanner`, `internal/updatecheck`, `internal/config`, `internal/runtime`. No new third-party dependency (ed25519 is stdlib; avoids a new signing lib).
**Storage**: Read-only embedded bundle (compiled in); operator bundle + persisted last-known-good copy under the scanner `Engine.dataDir` (already the home for `scanner-cache`/report dirs). No BBolt schema change — bundle state is a file + in-memory active pointer.
**Testing**: `go test -race ./internal/security/...` with valid/invalid fixture bundles (unsupported version, uncompilable pattern, regressing rule set, unsigned/wrong-key/corrupted payloads); the CI eval gate `go run ./cmd/scan-eval --corpus specs/065-evaluation-foundation/datasets/detect_corpus_v1.json --gate --min-recall 0.90 --max-fp 0.05` must stay green with the embedded default; `./scripts/test-api-e2e.sh` for the info-surface.
**Target Platform**: All editions/platforms; the bundle path is pure Go (no Docker/Landlock), so no build tags. The embedded default is cross-platform.
**Project Type**: Single Go module (backend); config-schema additive, one new REST/info field, no frontend rework.
**Performance Goals**: Refresh (fetch + verify + compile + self-check) runs off the hot path; activation is an atomic pointer swap so p99 scan latency during refresh is indistinguishable from steady state (constitution: no degradation at 1k tools). Rule matching stays O(tools × text × rules) with RE2 (no catastrophic backtracking).
**Constraints**: Offline-first (default path issues zero network requests); deterministic (byte-identical bundle ⇒ no-op refresh); fail-closed (any validation failure retains last-known-good, never an empty rule set); single-flight refresh.
**Scale/Scope**: One new package (`internal/security/bundle`) + one new `detect.Check`, one config block (`security.bundle`), one refresher wired into runtime lifecycle, one info-surface field, an embedded default bundle + self-check corpus. Reuses the entire detect/scanner/updatecheck stack unchanged.

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-check after Phase 1 design.*

- **I. Performance at Scale** — ✅ Refresh is off-hot-path on a background goroutine; activation is an atomic pointer swap under a single-flight guard; rule matching is bounded RE2. No per-scan file I/O (loader runs at refresh time, not scan time).
- **II. Actor-Based Concurrency** — ✅ The refresher is a single background loop with a ticker + hot-reload channel, mirroring `internal/updatecheck.Checker`; it owns bundle state and publishes an immutable active-bundle pointer read lock-free by scanners. No shared mutable rule slice.
- **III. Configuration-Driven Architecture** — ✅ Bundle path, refresh enable/interval, fetch enable, source URL, and trusted public key live in `mcp_config.json` (`security.bundle`) with env override and hot-reload; nothing hardcoded. Reuses the existing `update_check` block for release awareness.
- **IV. Security by Default** — ✅ Network fetch is opt-in and signature-verified (ed25519 detached sig against a configured key), fails closed with no key; every path fails safe to last-known-good; the activation self-check makes the built-in detection bar a hard floor; bundle hits are hard-tier fail-closed (any hit ⇒ quarantine). No auto-installer added.
- **V. Test-Driven Development** — ✅ Loader/verifier/self-check are built test-first with a fixture matrix of invalid bundles; the embedded default must keep the CI eval gate green (integration-level TDD contract). Failing `_test.go` precedes implementation per repo CLAUDE.md.
- **VI. Documentation Hygiene** — ✅ Update `docs/features/tool-scanner.md` (or the security-quarantine doc), CLAUDE.md/README config reference, and the info-endpoint API docs to describe the bundle lifecycle, the `security.bundle` block, and the signing contract.

**Result**: PASS, no violations. Complexity Tracking not required.

## Project Structure

### Documentation (this feature)

```text
specs/087-tpa-daily-refresh/
├── plan.md              # This file
├── spec.md              # Feature spec
├── research.md          # Phase 0 output — embed strategy, ed25519 sig format, self-check corpus sourcing, updatecheck reuse
├── data-model.md        # Phase 1 output — Bundle, Rule, Source, Signature, ActiveBundle/LKG, RefreshStatus entities
├── quickstart.md        # Phase 1 output — drop a bundle, enable signed fetch, read bundle status
├── contracts/
│   └── bundle-loader.md  # Phase 1 output — Go loader/verifier/self-check interface + config schema contract
├── checklists/
│   └── requirements.md   # Spec quality checklist
└── tasks.md              # Phase 2 output (/speckit.tasks — NOT created by /speckit.plan)
```

### Source Code (repository root)

```text
internal/security/bundle/                     # NEW package — file lifecycle detect may not touch (offline import-guard)
├── embed.go              # //go:embed data/scanner-bundle.json — the compiled default; exported EmbeddedBundle()
├── data/
│   └── scanner-bundle.json      # embedded known-good default (produced by tpa-db export)
├── types.go              # Bundle, Rule, Source, RefreshStatus, ActiveBundle (immutable snapshot) types
├── loader.go             # parse + bundle_version compatibility gate (FR-005) + RE2 compile all patterns (FR-006) + structural_diff skip (FR-007)
├── verify.go             # ed25519 detached-signature verification against configured key (FR-013/FR-014)
├── selftest.go           # activation self-check: run cmd/scan-eval gate logic over embedded corpus with candidate rules layered on built-ins (FR-008)
├── data/selftest_corpus.json    # embedded labeled corpus for the activation self-check (provenance/license carried, FR-021)
├── refresh.go            # Refresher: daily ticker + on-demand + hot-reload, single-flight, atomic swap, fail-safe to LKG (FR-004/009/010/011)
├── fetch.go              # opt-in HTTP fetch: size cap, timeout, log-rate-limit, off by default (FR-012/FR-015)
└── status.go             # RefreshStatus snapshot for the info surface (FR-017/FR-019)

internal/security/detect/checks/bundle_check.go   # NEW pure detect.Check — holds pre-compiled rules, Inspect emits hard-tier signals (FR-020)
internal/security/scanner/inprocess.go            # MODIFIED — inject BundleCheck into the hardcoded slice (91-104); read active bundle from Engine
internal/security/scanner/engine.go               # MODIFIED — hold a *bundle.Refresher (or active-bundle accessor) alongside dataDir (25/84-89)

internal/config/config.go                         # MODIFIED — add SecurityConfig.Bundle *BundleConfig (path, refresh enable/interval, fetch enable, source URL, trust key) + accessors, validation (2179-2244)
internal/config/loader.go                          # MODIFIED (if needed) — no migration; block is additive/nil-default-safe

internal/runtime/runtime.go                        # MODIFIED — construct + Start the Refresher alongside updateChecker (84/2472-2487); wire hot-reload apply (1435-1469)
internal/runtime/lifecycle.go                      # MODIFIED (if needed) — surface bundle status in the info/status payload

internal/httpapi/server.go                         # MODIFIED — add bundle status to /api/v1/info next to the existing update object; keep release status distinct (FR-017/FR-019)

cmd/scan-eval/gate.go                               # REUSED verbatim as the self-check's gate logic source of truth; keep gateChecks() in sync with inprocess.go (add BundleCheck)

docs/features/tool-scanner.md                       # MODIFIED — document the bundle lifecycle, security.bundle config, signing contract
```

**Structure Decision**: Single Go module. The file/network/crypto lifecycle lives in a NEW self-contained package `internal/security/bundle` because the `detect` package is import-guarded offline (`internal/security/detect/imports_test.go:20-40` forbids `os`, `path/filepath`, `net/http`, and the `scanner` package) — a bundle file loader cannot live in `detect`. `bundle` compiles rules to in-memory data and hands them to a pure `checks.BundleCheck` that DOES live in `detect/checks`, preserving the offline determinism contract. The `scanner` package (which already owns `dataDir` and constructs the detect engine at `inprocess.go:89`) is the wiring point; `runtime` owns the refresher's lifecycle exactly as it owns the existing update checker.

## Phased Tasks (TDD — write failing tests first, per phase)

> Every phase is test-first: author the `_test.go` fixtures/expectations, watch them fail, then implement. Each phase ends with an explicit verification command. Phases map to the spec's P1 → P2 → P3 so the feature is shippable after each priority.

### Phase 0 — Research (`research.md`)

- Decide the embed strategy: `go:embed` a checked-in `internal/security/bundle/data/scanner-bundle.json` produced by the tpa-db exporter; document how it is refreshed at release time and how the build fails if it is absent (FR-001).
- Decide the signature scheme: ed25519 detached signature (stdlib `crypto/ed25519`), base64/hex-encoded sidecar; document key distribution and the fail-closed-without-key rule (FR-013/FR-014). Record why not GPG/cosign (no new dependency).
- Decide the self-check corpus source: an embedded compact labeled corpus (subset/mirror of `specs/065-evaluation-foundation/datasets/detect_corpus_v1.json`) carrying provenance/license (FR-021), and confirm the vacuity guards (≥1 gated-malicious, ≥1 hard-negative) hold.
- Confirm reuse of `internal/updatecheck` for P3 (Start/ticker/GetVersionInfo/guidance) — no new release logic (FR-018).
- **Verify**: `research.md` resolves every NEEDS-CLARIFICATION with a decision + rationale; Constitution Check re-confirmed.

### Phase 1 — Design & Contracts (`data-model.md`, `contracts/bundle-loader.md`, `quickstart.md`)

- `data-model.md`: Bundle / Rule / Source / Signature / ActiveBundle+LKG / RefreshStatus entities and the `security.bundle` config shape.
- `contracts/bundle-loader.md`: the Go interfaces — `Load([]byte) (*Bundle, error)` (version + compile + structural_diff-skip), `Verify(payload, sig []byte, key) error`, `SelfCheck(candidate, builtins) (Report, error)`, `Refresher.Refresh(ctx, trigger)` — and the config JSON schema with defaults.
- `quickstart.md`: three flows — first-launch offline embedded, drop-a-bundle refresh, enable signed fetch — plus reading bundle status from `/api/v1/info`.
- **Verify**: re-run Constitution Check against the design; confirm `gateChecks()` (`cmd/scan-eval/gate.go:78`) and the live check slice (`inprocess.go:91-104`) will both include `BundleCheck` so the gate measures the shipped detector.

### Phase 2 (P1) — Embedded default + loader + BundleCheck + file-drop refresh (offline, no network)

Test-first order:
1. `bundle/loader_test.go`: fixtures for supported-version parse, unsupported major/minor refuse (FR-005), uncompilable pattern reject+count (FR-006), structural_diff skip-not-fail (FR-007), byte-identical no-op (FR-011). Then implement `types.go`, `loader.go`, `embed.go`.
2. `detect/checks/bundle_check_test.go`: a compiled rule set → `Inspect` emits one hard-tier `Signal` per hit with `CheckID` like `tpa.TPA-2026-0001.hidden_instruction`, deterministic order, `ThreatType` mapped from `category`; a clean tool → no signal. Then implement `bundle_check.go`.
3. `bundle/selftest_test.go`: a regressing candidate is rejected; a clean candidate passes; vacuity guard fails closed (FR-008, SC-004). Implement `selftest.go` reusing `cmd/scan-eval/gate.go` decision logic.
4. `bundle/refresh_test.go`: file-drop at configured path activates atomically; invalid drop retains LKG with a recorded reason (FR-009); single-flight coalescing (FR-010); manual + daily trigger. Implement `refresh.go`, `status.go`.
5. Wire into `scanner/inprocess.go` (inject `BundleCheck` into the slice) and `scanner/engine.go` (hold active-bundle accessor); add `SecurityConfig.Bundle` in `config.go` (nil-default-safe) + validation; construct/Start the Refresher in `runtime.go`.
6. Update `cmd/scan-eval/gate.go` `gateChecks()` to include `BundleCheck` so the CI gate measures the shipped detector; embed the default bundle so the gate stays green (SC-008).
- **Verify**: `go test -race ./internal/security/bundle/... ./internal/security/detect/...`; `go run ./cmd/scan-eval --corpus specs/065-evaluation-foundation/datasets/detect_corpus_v1.json --gate --min-recall 0.90 --max-fp 0.05` exits 0 with the embedded default; manual: start with no network, drop a fixture bundle, confirm activation + a bundle-only signature fires and an invalid drop keeps LKG (SC-001/002/003).

### Phase 3 (P2) — Optional signed network fetch + verification + fallback

Test-first order:
1. `bundle/verify_test.go`: correctly-signed payload verifies; unsigned, wrong-key, and byte-corrupted payloads are rejected before compile; no-key-configured fails closed (FR-013/FR-014). Implement `verify.go`.
2. `bundle/fetch_test.go` (httptest server): signed candidate → verify → self-check → activate; unsigned/wrong-key/corrupted/404/timeout/oversized → retain LKG with reason; disabled → zero requests (FR-012/FR-015, SC-005). Implement `fetch.go`; extend `refresh.go` to include the fetch trigger in the daily cycle.
3. Config: extend `security.bundle` with `fetch.enabled`, `fetch.url`, `fetch.public_key`, size/timeout bounds; hot-reload apply in `runtime.go` (mirror `update_check` apply at 1435-1469).
- **Verify**: `go test -race ./internal/security/bundle/...`; e2e: with fetch disabled, assert no bundle-fetch request over a run; with fetch enabled against a local signed artifact, assert verify→gate→activate and that a wrong-key artifact is never activated (SC-005).

### Phase 4 (P3) — Daily release-availability surfacing (reuse Spec 079)

Test-first order:
1. `runtime` test: the daily cycle invokes the existing update checker and the info payload carries release status distinct from bundle status (FR-018/FR-019, SC-007); with `update_check.enabled=false` / `MCPPROXY_DISABLE_AUTO_UPDATE` no request and no nudge (parity with Spec 079 FR-015).
2. `httpapi/server.go`: add the bundle-status object to `/api/v1/info` next to the existing update object; keep them separately labeled.
- **Verify**: `./scripts/test-api-e2e.sh` for the info surface; assert an older-than-latest build surfaces the correct channel-appropriate upgrade command and release URL and downloads/executes no binary (SC-007); assert disabled ⇒ silent.

### Phase 5 — Hardening & docs

- Edge cases from the spec: directory/zero-byte/malformed path, clock-skew missed tick, hot-reload of path/source mid-run, vacuous self-check corpus.
- `docs/features/tool-scanner.md` + CLAUDE.md/README config reference + `/api/v1/info` API docs (Principle VI).
- **Verify**: `./scripts/run-linter.sh`, `go test ./internal/... -v`, `./scripts/test-api-e2e.sh`, `./scripts/run-all-tests.sh`; final CI eval gate green.

## Complexity Tracking

No constitution violations. Section intentionally empty.

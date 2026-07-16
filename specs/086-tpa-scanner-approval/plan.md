# Implementation Plan: Offline TPA Scanner + Trust-Tiered Approval Modes

**Branch**: `086-tpa-scanner-approval` | **Date**: 2026-07-16 | **Spec**: [spec.md](./spec.md)
**Input**: Feature specification from `/specs/086-tpa-scanner-approval/spec.md`

## Summary

Two coupled capabilities. (1) A **bundle-backed offline scanner**: a scanner-side loader reads tpa-db's `data/scanner-bundle.json` (contract v0.1.0) from `scanner.Engine.dataDir`, validates `bundle_version`, compiles every `engine: regex` rule to an RE2 `*regexp.Regexp` once, and injects a new pure `detect.Check` (`checks.BundleCheck`) into the hardcoded check slice at `internal/security/scanner/inprocess.go:91-104`. Each rule hit emits a hard-tier `Signal` (`CheckID = tpa.<id>.<detector>`), flowing through the existing `aggregate()` → `detectFindingToScanFinding` → `ScanFinding` → verdict/quarantine path with no new plumbing. `structural_diff` / `resource_content` / `server_manifest` rules are declared not-runnable in the offline tier for v1 (contract §1.3/§5). (2) A **three-mode per-server trust setting** (`auto` / `scan` / `manual`) via a new `ServerConfig.TrustMode` string enum, migrated from the `AutoApproveToolChanges *bool` tri-state, that governs BOTH new-server admission and tool-change approval. `scan` mode auto-approves only on a green (zero hard-tier hits) offline verdict, else routes to the existing human approval endpoints; it introduces a new `ReasonScanApproved` `TransitionReason` into the `assertToolApprovalInvariant` allow-lists. Verified by unit tests + the existing `cmd/scan-eval` recall/FP CI gate.

## Technical Context

**Language/Version**: Go 1.24
**Primary Dependencies**: stdlib only for the check (`regexp`, `encoding/json`); the scanner-side loader may use `os`/`path/filepath` (forbidden inside `detect`, allowed in `scanner`). Reuses `internal/security/detect`, `internal/security/scanner`, `internal/runtime/tool_quarantine.go`, `internal/config`, `internal/hash`. No new third-party dependency.
**Storage**: Reuses the BBolt tool-approval store and `scanner.IntegrityBaseline`; config lives in `mcp_config.json`. Additive only: one new `ServerConfig.TrustMode` string field; no store schema change (bundle findings reuse `ScanFinding.Signals`).
**Testing**: `go test -race ./internal/security/... ./internal/config/... ./internal/runtime/...`; corpus eval via `go run ./cmd/scan-eval --corpus specs/065-evaluation-foundation/datasets/detect_corpus_v1.json --gate --min-recall 0.90 --max-fp 0.05`; config migration round-trip in `internal/config/auto_approve_tool_changes_test.go`.
**Target Platform**: All editions/platforms; detection is platform-independent (no Docker/Landlock), no build tags.
**Project Type**: Single Go module (backend); no new frontend framework — Web UI reuses existing finding views. Config/report fields are additive and already serialized.
**Performance Goals**: Bundle compiles once at scanner construction (O(rules)); per-tool check is O(rules × description length) linear RE2 scan, staying under the existing scan-timeout budget for 1,000 tools (Constitution I).
**Constraints**: `detect` stays fully offline (import-guarded — no `os`/`net`/`path/filepath`); the check is pure/total/deterministic (recover-isolated, byte-identical output). `scan` mode fails closed on any missing verdict. Config migration idempotent across hot-reload.
**Scale/Scope**: 1 loader + 1 `detect.Check` + 1 config enum field + migration extension + 4 `checkToolApprovals` decision seams + 1 admission seam + merge/copy branches + CLI/WebUI surfacing. Corpus unchanged (gate must stay green).

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-check after Phase 1 design.*

- **I. Performance at Scale** — PASS. Bundle compiled once; per-tool check is a bounded linear regex pass over `ToolView` text. No per-tool file I/O (loader runs at construction). Stays under the 1k-tool budget.
- **II. Actor-Based Concurrency** — PASS. No new long-lived actor. The bundle check is a pure function invoked inside the existing scan; the admission scan-gate subscribes to the existing `EventTypeSecurityScanSettled` event bus rather than adding a new goroutine loop. Per-check `recover()` isolates panics.
- **III. Configuration-Driven Architecture** — PASS. `trust_mode` and the bundle path/toggle live in `mcp_config.json` with env override + hot-reload (migrations re-run on every `initializeRegistries`). No hardcoded bundle path.
- **IV. Security by Default** — PASS. This is the security feature. Default `trust_mode` is `manual` (nil==secure, matching `IsQuarantineEnabled()`); `scan` mode fails closed on any absent verdict; bundle hits are hard-tier (auto-quarantine). Evidence is render-safe (reuses `detect` finding formatting). New servers still quarantined by default (Principle IV mandate) unless explicitly set to `auto`.
- **V. Test-Driven Development** — PASS/mandatory. Every seam is built test-first: bundle-loader table tests, `BundleCheck.Inspect` positive + hard-negative fixtures, migration round-trip (idempotency + no-clobber), `checkToolApprovals` scan-gate decision tests, `MergeServerConfig` patch test. The `cmd/scan-eval` gate is the integration-level contract.
- **VI. Documentation Hygiene** — PASS. Update `docs/features/security-quarantine.md` (trust modes), a new `docs/features/tpa-scanner-bundle.md` (bundle format + not-runnable tiers), README config table, and `CLAUDE.md` if the config surface changes.

**Result**: PASS, no violations. Complexity Tracking not required.

## Project Structure

### Documentation (this feature)

```text
specs/086-tpa-scanner-approval/
├── plan.md              # This file
├── spec.md              # Feature spec
├── research.md          # Phase 0 output (bundle-load seam, verdict-source decisions, structural_diff deferral)
├── data-model.md        # Phase 1 output (TrustMode enum + migration table, BundleCheck rule struct)
├── quickstart.md        # Phase 1 output (set trust_mode, drop a bundle, observe held change)
├── contracts/
│   └── trust-mode-and-bundle-check.md   # Phase 1 — Go interface + config contract
├── checklists/
│   └── requirements.md  # Spec quality checklist
└── tasks.md             # Phase 2 output (/speckit.tasks — NOT created by /speckit.plan)
```

### Source Code (repository root)

```text
internal/security/scanner/
├── bundle_loader.go        # NEW — load+validate scanner-bundle.json from e.dataDir; verify bundle_version;
│                           #        compile each regex rule once; log+count compile/load errors; build []compiledRule
├── bundle_loader_test.go   # NEW — version-gate, RE2-compile-error, determinism, structural_diff/other-target skip
├── inprocess.go            # MODIFIED — construct BundleCheck from loaded rules; add to the check slice (91-104);
│                           #            wire e.dataDir bundle load at engine construction
├── engine.go               # MODIFIED (minimal) — load bundle in/near NewEngine using existing dataDir field
├── service.go              # MODIFIED — admission scan-gate: on EventTypeSecurityScanSettled for a scan-mode
│                           #            server, consult deriveBaselineVerdict; if clean → ApproveServer(force=false)
└── types.go                # (unchanged — reuses ScanFinding.Signals for TPA ids)

internal/security/detect/
├── checks/bundle.go        # NEW — BundleCheck: holds []compiledRule; ID(); Inspect ranges rules matching a
│                           #        ToolView surface, emits one TierHard Signal per hit (CheckID tpa.<id>.<detector>),
│                           #        stable order. Pure/total/deterministic — NO file I/O (rules injected in-memory)
└── checks/bundle_test.go   # NEW — regex hit → hard signal; benign → none; target routing; deterministic order

internal/config/
├── config.go               # MODIFIED — add ServerConfig.TrustMode string (json trust_mode, mapstructure trust-mode)
│                           #            + EffectiveTrustMode() typed accessor; extend normalizeServerQuarantineFlags
│                           #            (~997) with the *bool→TrustMode pass; make IsQuarantineSkipped()/
│                           #            IsAutoApproveToolChanges() thin wrappers over EffectiveTrustMode()
├── merge.go                # MODIFIED — add TrustMode PATCH branch to MergeServerConfig (191-245) + field to
│                           #            CopyServerConfig (541-557)
├── loader.go               # (unchanged — migration already invoked via initializeRegistries 516-519)
├── auto_approve_tool_changes_test.go  # MODIFIED — extend: legacy→TrustMode migration, explicit-wins, idempotency
└── merge_test.go           # MODIFIED/NEW — TrustMode PATCH round-trip

internal/runtime/
├── tool_quarantine.go      # MODIFIED — derive serverSkipped/autoApproveChanges from EffectiveTrustMode();
│                           #            add scan-mode branch at the 4 decision seams (new tool 315-324,
│                           #            pending 459-467, changed 555-591, rug-pull 738-769) that consults the
│                           #            offline verdict and auto-approves only on green via ReasonScanApproved;
│                           #            add ReasonScanApproved to assertToolApprovalInvariant allow-lists (132-149)
├── tool_quarantine_test.go # MODIFIED — scan-green auto-approve, scan-hit hold, fail-closed no-verdict, auto/manual
├── lifecycle.go            # MODIFIED — scan-mode new-server admission: after connect (OnServerConnected 86-110)
│                           #            trigger StartScan for a scan-mode server
└── scan_notify.go          # (reuse — EventTypeSecurityScanSettled is the admission-gate hook)

internal/server/
├── add_from_registry.go    # MODIFIED — admission default consults per-server TrustMode (via new helper
│                           #            cfg.QuarantineDefaultForServer(sc)) at 109-116; CN-002 preserved (no request override)
├── mcp.go                  # MODIFIED — same admission helper at 3939
└── mcp_tool_policy_result.go / mcp_visibility.go  # MODIFIED — surface matched TPA ids in gate/describe output

internal/httpapi/
├── server.go               # MODIFIED — same admission helper at 1471; ensure /tools/approve + /security views carry Signals
└── security_scanner.go     # (reuse ApproveServer path)

cmd/scan-eval/
├── gate.go                 # VERIFY/MODIFIED — gateChecks() (78-85) must include BundleCheck to mirror the live
│                           #            scanner registry; categoryCheck (66-72) gains any new bundle-driven category
└── main.go                 # (unchanged invocation)

docs/features/
├── tpa-scanner-bundle.md   # NEW — bundle format, version gate, not-runnable tiers
└── security-quarantine.md  # MODIFIED — three trust modes, migration from skip_quarantine/auto_approve_tool_changes
```

**Structure Decision**: Single Go module. The bundle **loader** lives in `internal/security/scanner` (file I/O allowed) and the bundle **check** lives in `internal/security/detect/checks` (pure, import-guarded) — this split is forced by `detect`'s offline import guard (`imports_test.go`). The trust-mode enum is a config-layer concern (`internal/config`) consumed by the runtime seam (`internal/runtime/tool_quarantine.go`) and the admission seams (`internal/server`, `internal/httpapi`), unifying two mechanisms that are separate today.

## Phased, TDD-ordered task outline

Each phase is independently testable and ordered test-first (failing `_test.go` before implementation, per Constitution V + repo CLAUDE.md).

**Phase 0 — Research (`research.md`)**
- Confirm the bundle-load seam: `scanner.Engine.dataDir` (engine.go:25, set in NewEngine 84-89) as the load point; document the `imports_test.go` constraint forcing loader-in-scanner / check-in-detect.
- Decide verdict source: reuse `deriveBaselineVerdict`/`isBlockingFinding`; pin "green" = `clean` (FR-013).
- Decide `structural_diff` / non-tool-description targets deferral (FR-006/FR-007) and record the `IntegrityBaseline`-only alternative as future work.
- Confirm MCP-2931 runtime consumption is live (`tool_quarantine.go:199-205`) — model current behavior from runtime, not `config.go` comments.

**Phase 1 — Bundle loader + BundleCheck (delivers P1's detection substrate; verified by unit tests)**
1. `bundle_loader_test.go`: version-gate refusal, RE2 compile-error counted-not-dropped, deterministic rule order, `structural_diff`/`resource_content`/`server_manifest` marked not-runnable.
2. `bundle_loader.go`: parse+validate `scanner-bundle.json`, compile regex rules once, build `[]compiledRule` sorted by `(id, detector)`.
3. `checks/bundle_test.go`: regex hit → one `TierHard` signal `tpa.<id>.<detector>` with `rule.confidence`; benign → none; stable emit order; target routes to `ToolView.Description`/`NormalizedText`.
4. `checks/bundle.go`: implement `ID()` + `Inspect()`, pure/total/deterministic.
5. Wire `BundleCheck` into `inprocess.go:91-104` and construct it from the loaded bundle at engine construction; add to `cmd/scan-eval/gate.go` `gateChecks()` so the gate measures the shipped detector.
6. **Verify**: `go test -race ./internal/security/...` + `go run ./cmd/scan-eval --gate --min-recall 0.90 --max-fp 0.05` stays green (SC-002).

**Phase 2 — TrustMode enum + migration (delivers P3's config substrate; verified by config tests)**
1. Extend `auto_approve_tool_changes_test.go`: legacy `skip_quarantine:true`→`auto`, `auto_approve_tool_changes:false`→`manual`, explicit `trust_mode` wins, idempotency across re-save (no-clobber invariant).
2. `config.go`: add `ServerConfig.TrustMode` + `EffectiveTrustMode()`; extend `normalizeServerQuarantineFlags` with the `*bool→TrustMode` pass; make `IsQuarantineSkipped()`/`IsAutoApproveToolChanges()` thin wrappers.
3. `merge_test.go` + `merge.go`: TrustMode PATCH branch in `MergeServerConfig`, field in `CopyServerConfig` (SC-005).
4. **Verify**: `go test -race ./internal/config/...`.

**Phase 3 — Scan-then-approve for tool changes (P1 core value; verified by runtime tests)**
1. `tool_quarantine_test.go`: scan-mode green change → auto-approve via `ReasonScanApproved`; scan-mode bundle-hit change → held `changed`/blocked with TPA id; no-verdict → fail-closed hold; `auto` → approve without scan; `manual` → always hold.
2. `tool_quarantine.go`: add `ReasonScanApproved` to `assertToolApprovalInvariant` allow-lists; derive mode from `EffectiveTrustMode()`; add scan-gate branch at the four decision seams that reads the inline offline verdict and auto-approves only on green.
3. **Verify**: `go test -race ./internal/runtime/...` + drive `checkToolApprovals` over the labeled corpus (SC-001, SC-003).

**Phase 4 — Scan-then-approve for new-server admission (P2; verified by runtime + service tests)**
1. Tests: scan-mode add → quarantine+connect+scan; green settle → `ApproveServer(force=false)` auto-invoked; dangerous → stays quarantined; `auto` → admitted unquarantined; connect-failure → fail-closed.
2. `service.go` / `lifecycle.go`: subscribe the admission gate to `EventTypeSecurityScanSettled`; add `cfg.QuarantineDefaultForServer(sc)` helper and route the three add paths (`add_from_registry.go:113`, `mcp.go:3939`, `httpapi/server.go:1471`) through it (CN-002 preserved).
3. **Verify**: `go test -race ./internal/runtime/... ./internal/security/scanner/...` + `./scripts/test-api-e2e.sh`.

**Phase 5 — Surfacing + docs (P3 remainder; verified by CLI/REST + docs review)**
1. Surface matched TPA ids/level/confidence in CLI (`mcpproxy security scan`, tool-approval), REST `/security`, and Web UI via `ScanFinding.Signals` (SC-006).
2. Docs: new `docs/features/tpa-scanner-bundle.md`, updated `security-quarantine.md`, README config table, `CLAUDE.md`.
3. **Verify**: `./scripts/run-all-tests.sh`, `./scripts/run-linter.sh`, full `cmd/scan-eval` gate.

## Complexity Tracking

No constitution violations. Section intentionally empty.

# NEXT-STEPS — Sequencing the TPA Scanner + Trust-Tiered Approval + Daily Refresh

Cross-cutting rollout note spanning **Spec 086** (`086-tpa-scanner-approval`) and
**Spec 087** (`087-tpa-daily-refresh`). Read both `spec.md` files first; this doc
only sequences them and pins the shared seams.

## 1. Product goal (end to end)

Give mcpproxy a fast, fully-offline Tool-Poisoning-Attack scanner whose knowledge
is an updatable signature database — the tpa-db `scanner-bundle.json` — compiled
into one deterministic `detect.Check` that runs inside the existing in-process
scanner and drives the same hard-tier quarantine gate as the built-in checks. On
top of that, replace the blunt `AutoApproveToolChanges` boolean with an opt-in,
per-server three-mode trust setting (`auto` / `scan` / `manual`) so a `scan`-mode
server auto-approves a new server or a tool-description change **only** when the
offline scan is green (zero bundle-rule hits), and fails closed to human review
otherwise. Finally, keep the signature DB current with a daily, offline-first,
fail-safe-to-last-known-good refresh (embedded default → manual file drop →
optional signed network fetch), gated on every activation by the same
`cmd/scan-eval` recall/FP bar that governs the build.

## 2. Dependency-ordered rollout

Land in this order — each stage is independently shippable and the later stages
consume the earlier stages' seams. **Loader before approval-gating before refresh.**

1. **Bundle loader + bundle-backed `detect.Check`** (Spec 086, FR-001..FR-007; US1 offline scanner half).
   The root of everything. Load and version-check `scanner-bundle.json` in the
   `internal/security/scanner` package (NOT `detect` — `imports_test.go` forbids
   `os`/`path/filepath`/`net` there), compile every `engine: regex` `pattern`
   under RE2 once, and inject the pre-compiled rules as an in-memory
   `detect.Check` added to the hardcoded slice at
   `internal/security/scanner/inprocess.go:91-104`. Emits one `TierHard` `Signal`
   per hit (`CheckID = tpa.<TPA-id>.<detector>`). Findings then flow for free
   through `detectFindingToScanFinding` → `ScanFinding` → the verdict machinery.
   Nothing downstream can exist without a live bundle producing findings.

2. **Trust-mode enum + tool-change scan gate** (Spec 086, FR-008..FR-014, FR-018; US1 approval half, US3).
   Add `ServerConfig.TrustMode` + `EffectiveTrustMode()`, and wire the `scan`
   branch into `checkToolApprovals` (`internal/runtime/tool_quarantine.go:185`):
   run the fast in-process offline scan inline over the changed tool and
   auto-approve only on a `clean` verdict via a new
   `TransitionReason ReasonScanApproved` (added to the
   `assertToolApprovalInvariant` allow-lists at `tool_quarantine.go:132`). Depends
   on stage 1 for the verdict; this is the core value (a tool change is when the
   attack lands).

3. **New-server admission under trust-mode** (Spec 086, FR-011; US2).
   Unify admission (today global-only via
   `DefaultQuarantineForNewServer()`/`add_from_registry.go:109-116`) under the
   same per-server mode: `scan` mode quarantines + connects + scans, then on
   `EventTypeSecurityScanSettled` auto-invokes `ApproveServer(force=false)` iff
   green. Depends on stage 2's mode + verdict definition; touches all three add
   paths (`add_from_registry.go`, `mcp.go:3939`, `httpapi/server.go:1471`).

4. **Config migration + PATCH plumbing** (Spec 086, FR-015..FR-017; US3).
   Extend `normalizeServerQuarantineFlags` (`config.go:997`) with the
   `AutoApproveToolChanges → TrustMode` pass, add the explicit `TrustMode` PATCH
   branch to `MergeServerConfig` (`merge.go:191-245`) and the `CopyServerConfig`
   field list. Can land alongside stage 2 but is listed here because it hardens
   the mode surface once the enum exists.

5. **Daily refresh: embedded default + manual file-drop** (Spec 087, US1, FR-001..FR-011).
   The bundle lifecycle: embed a known-good default at build time (build fails
   with none), resolve `file` drop over `embedded`, refresh daily + on-demand off
   the hot path, atomic single-flight activation, fail-safe to last-known-good.
   Depends on stage 1's loader/validator (reuses version-check + RE2 compile) and
   adds an **activation self-check** reusing the `cmd/scan-eval` gate logic.

6. **Optional signed network fetch** (Spec 087, US2, FR-012..FR-015).
   Strictly opt-in, off by default, signature-verified against an operator key
   before compile/activation, same gate as the file drop. Depends on stage 5's
   activation machinery.

7. **Release-availability awareness** (Spec 087, US3, FR-018..FR-019).
   Smallest slice: reuse `internal/updatecheck` (Spec 079) on the same daily
   cadence, surface an available mcpproxy release distinctly from bundle status;
   no auto-installer. Independent of stages 2-4; depends only on the daily ticker
   from stage 5.

## 3. The single config data-model change

One new field governs everything: **`ServerConfig.TrustMode string`** (json
`trust_mode`, mapstructure `trust-mode`), resolved via
`ServerConfig.EffectiveTrustMode()`. It supersedes the `AutoApproveToolChanges *bool`
tri-state (itself the MCP-2930 successor to the deprecated `skip_quarantine` bool).
The legacy fields are retained for back-compat; the migration
(`normalizeServerQuarantineFlags`, `config.go:997`, idempotent, runs on every
load/hot-reload) maps them forward only when `TrustMode` is empty, and an explicit
`TrustMode` always wins.

| Old state (`AutoApproveToolChanges` / legacy) | New `trust_mode` value | Notes |
|---|---|---|
| `AutoApproveToolChanges == true` (or migrated `skip_quarantine: true`) | `auto` | Approve without scanning — parity with today's `auto-approve-changes`. |
| `AutoApproveToolChanges == false` (explicit) | `manual` | Enforce human review. Explicit `false` must NOT be clobbered by a legacy `skip_quarantine: true` (existing `auto_approve_tool_changes_test.go` invariant). |
| `AutoApproveToolChanges == nil` **and** `skip_quarantine` false/unset | `manual` (inherited default) | Secure-by-default, consistent with `IsQuarantineEnabled()` nil==enabled. |
| `trust_mode` set explicitly (any value) | that value | Explicit mode always wins over legacy fields. |

`scan` is the genuinely new value — it has no legacy predecessor and requires the
stage-1 verdict seam. `IsQuarantineSkipped()` / `IsAutoApproveToolChanges()` become
thin wrappers over `EffectiveTrustMode()` so existing callers keep working.

## 4. Cross-repo seam (tpa-db → mcpproxy)

mcpproxy imports exactly **one artifact** from tpa-db: the compiled
`data/scanner-bundle.json`, per the Scanner Bundle Contract at
`tpa-db/specs/002-scanner-export/contracts/scanner-bundle.contract.md` (v0.1.0).
mcpproxy consumes ONLY this file — it never parses signature YAML. Shape:
top-level `bundle_version`, `schema_version`, `signature_count`, `rules[]` (sorted
by `(id, detector)`), `skipped[]`. Offline-runnable rules are `engine: regex`
(carry `pattern` + `flags`); `engine: structural_diff` rules carry
`rule` + `runtime: "stateful"` and are **not-runnable in the offline tier for v1**
(no prior manifest in `RegistryView`; skipped, never counted as clean coverage).
`resource_content` / `server_manifest` targets have no `ToolView`/`RegistryView`
surface (`detect/signal.go:59-75`) — declared not-runnable, surfaced as
un-evaluated coverage.

**Version-compat rule** (Contract §4): the Go loader MUST refuse a bundle whose
`bundle_version` major/minor it does not know (fail-closed → keep last-known-good),
and MUST treat unknown *additional* rule-object keys as forward-compatible
(ignore). Both `bundle_version` and `schema_version` are sourced from tpa-db's
`data/SCHEMA_VERSION` (currently `0.1.0`); they bump together when the signature
schema bumps. A bundle that fails to version-check or whose patterns fail to
compile under RE2 is a load error that is logged and counted (Contract §5), never
silently dropped.

## 5. Top 3 risks + the governing gate

1. **Granularity + sync/async mismatch on the tool-change gate.** Scans are
   per-server over tool definitions and produce one verdict, but
   `checkToolApprovals` reasons per-tool and runs *synchronously* in the discovery
   pass (`lifecycle.go:544`) — it must return allow/block immediately. The design
   reads the fast in-process offline verdict inline (the bundle check runs within
   the same `detect.Engine.Scan`), NOT a deferred deep scan; the async
   `EventTypeSecurityScanSettled` path is used only for stage-3 admission. Any
   scan-driven auto-approve must add `ReasonScanApproved` to the
   `assertToolApprovalInvariant` allow-lists or `enforceInvariant` silently keeps
   the tool blocked.

2. **"Green" ambiguity between the two gates.** `deriveBaselineVerdict`
   (`service.go:2039`) yields `clean` / `warnings` / `dangerous`, but the existing
   `ApproveServer` gate blocks only `dangerous` (`isBlockingFinding` ==
   `Tier==TierHard`, `service.go:2015`) — so `warnings` currently *passes*
   admission. Spec 086 FR-013 pins `scan`-mode "green" to `clean` (zero blocking
   hits) so the tool-change gate and the admission gate agree; `warnings` does NOT
   auto-approve. Getting this wrong makes the new mode and the existing gate
   disagree.

3. **Refresh regressing the shipped detector / hand-coupled gate sync.**
   `gateChecks()` (`cmd/scan-eval/gate.go:78`) and the live scanner registry
   (`inprocess.go:91-104`) must stay in sync *by hand* — a bundle check that ships
   but is not mirrored into the gate means the gate measures a different detector
   than ships. A daily bundle refresh can silently reshape the corpus and break
   CI, and the gate scores HARD tier only, so any signature emitting SOFT findings
   never moves recall. Mitigation: every activation (embedded, drop, fetch) runs
   the activation self-check with the same logic/thresholds, and `structural_diff`
   /`resource_content` rules must not be counted as clean coverage.

**The governing gate (every bundle change).** All bundle activity — the shipped
embedded default (Spec 087 SC-008), any dropped/fetched candidate's activation
self-check (Spec 087 FR-008), and the stage-1 bundle check itself (Spec 086
SC-002) — must keep the existing CI eval gate green:

```
go run ./cmd/scan-eval --corpus specs/065-evaluation-foundation/datasets/detect_corpus_v1.json \
  --gate --min-recall 0.90 --max-fp 0.05
```

`decide` (`gate.go:283`) FAILs if `OverallRecall < 0.90` over gated categories OR
hard-negative `FPRate > 0.05`; a candidate must retain ≥1 gated-malicious and ≥1
hard-negative sample to avoid the exit-4 vacuity guard (`gate.go:305-312`).
Thresholds are the launch bar and are NOT changed by either feature.

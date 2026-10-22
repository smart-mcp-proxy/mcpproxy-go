# Feature Specification: Scan is the default trust mode; a clean-scanned change auto-approves

**Feature Branch**: `119-scan-default-trust`
**Status**: Implemented in one PR
**Input**: Maintainer decision 2026-10-22 (UI feedback session, verbatim): "by default we don't need to
challenge user with approvals. If contract changed and scan shows OK - auto-approve it. If user switched to
manual mode - it always asks for approval of changed tools." It supersedes the 2026-10-11 red-team decision
RT-02 ("a clean verdict never approves a changed contract") for `scan` mode; RT-02 stays the rule of `manual`.

## Why

With `manual` as the default, every benign upstream release that re-words a tool description puts a review
item on Home and blocks the tool for every agent until a person clicks. The TPA scanner is in-process and
runs on every change (spec 118), so the default can lean on it: attention is asked only when the scanner
found something, or when the scan could not be trusted. The accepted residual risk is a paraphrased rug pull
that scans clean (red-team EXP-6); operators who do not accept it keep `trust_mode: manual` per server.

## User Stories

### US1 — Scan is the default trust mode (P1)
- **FR-001** `ServerConfig.EffectiveTrustMode()` resolves an EMPTY `trust_mode` with no legacy flags to
  `scan`. The legacy mapping is unchanged: `auto_approve_tool_changes: true` / `skip_quarantine: true` →
  `auto`, `auto_approve_tool_changes: false` → `manual`. A non-empty unrecognized value still fails closed
  to `manual`, and load-time normalization still rewrites it to `manual` (the tier it was already running at).
- **FR-002** Every surface that names the default follows: the Web UI selector's "current default" badge and
  the add-server form's preselected mode, the `upstream add --trust-mode` help text, the `upstream_servers`
  MCP tool schema, the generated TypeScript contracts comment, and the docs (trust-mode table,
  configuration reference, quarantine pilot doc). The Web UI's fail-closed resolution of an INVALID value
  stays `manual`; only the unset case becomes `scan`.
- **FR-003** New-server admission is mechanically unchanged: a server added with the default is quarantined,
  gets the fail-closed admission scan, and is released automatically on a clean verdict (spec 086 FR-011).
  Existing admitted servers whose `trust_mode` is empty now run under `scan` from the next discovery pass;
  this is a documented behaviour change (CHANGELOG, Breaking Changes).

### US2 — A clean-scanned contract change auto-approves in `scan` mode (P1)
- **FR-004** Under `scan`, when an approved tool's contract changes (first approved→changed detection), the
  synchronous in-process scan runs as today; a `clean` verdict with full coverage approves the new contract in
  the same pass: status `approved`, reason `scan_approved`, `ApprovedBy: scan-approved`, hold evidence
  cleared, activity event `tool_auto_approved`. Nothing is held and no attention item is raised.
- **FR-005** A `warnings` or `dangerous` verdict, or degraded/absent coverage, holds the change exactly as
  today (`changed`, blocked, `held_reason: scan_findings` / `scan_coverage`, evidence attached).
- **FR-005a** The approval in FR-004/FR-006 applies only while quarantine is enforced
  (`quarantine_enabled` on). With quarantine off globally nothing is blocked anyway; the record then keeps
  its provenance (`changed`, unblocked), as under `manual`, instead of being re-stamped `scan-approved` by a
  scan that gates nothing. (Builder's call: keeps the global kill switch a pure no-op on records.)
- **FR-006** A record that is already `changed` (held before this release, or held on a non-clean verdict
  whose definition has since moved) is re-evaluated on every discovery pass under `scan`: a clean verdict of
  the CURRENT definition approves it (same transition as FR-004). This is what clears a queue of
  clean-scanned holds after upgrading.
- **FR-007** Safety-hint flips toward a SAFER tier are never auto-approved, whatever the verdict:
  `readOnlyHint` false→true or `destructiveHint` true→false on a changed contract is held with
  `held_reason: scan_findings` and the deterministic signal `GATE-HINT-SAFER-FLIP`. A tool that re-labels
  itself read-only is exactly the change a profile `max_tier` cap and the `call_tool_read` dispatch trust;
  the scanner's `capability_mismatch` check does not cover a flip without a description change. (Builder's
  call, kept small; the review diff already renders hint flips.)
- **FR-008** The approval invariant table (`assertToolApprovalInvariant`, qapilot `ChangedToApprovedReasons`)
  admits `scan_approved` for changed→approved. The gate is the only writer of that reason and uses it only
  under `scan`.
- **FR-009** `manual` is unchanged: every change and addition is held; the spec 118 evidence scan still
  records the verdict (`awaiting_review` when clean). `auto` is unchanged.
- **FR-010** The quarantine pilot model follows: QP-10 becomes "a changed definition of an approved tool is
  held in manual mode whatever its verdict; in scan mode it is approved on a clean verdict and held on a
  dangerous one; auto approves it"; S13 expects `approved` after the benign change; S14 keeps `changed`;
  S03/S06/S08/S09 (manual) are unchanged. Any mutation in `tests/qapilot/mutations.json` whose effect is now
  the policy itself is replaced by one that still fails the suite.

## Test-fixture convention
The runtime package's fixtures were written when an unset `trust_mode` meant `manual` and they encode
manual-mode holds. `setupQuarantineRuntime` (and the spec-116 fixture) pin a server literal with no
`trust_mode` and no legacy flag to `manual`; tests of the default tier say `scan` explicitly. The resolver's
own default is pinned in `internal/config`.

## Out of scope
Server-level admission mechanics; the deep (Docker) scanners; the attention list's ranking (clean-scanned
changes no longer produce an item, so nothing there changes); reverting the hint-flip hold for `auto` (auto
never holds).

## Acceptance
1. Fresh install, server added from the catalog with no trust mode chosen: quarantined, scanned, released on
   a clean verdict; selector shows `Scan · current default`.
2. `scan` server, approved tool's description re-worded benignly: next discovery pass leaves it `approved`,
   agents keep calling it, Activity shows `tool_auto_approved`, Home shows nothing.
3. Same with a TPA payload in the new description: `changed`, blocked, `held_reason: scan_findings`, Home
   shows the review item.
4. Same with `readOnlyHint` flipped false→true and nothing else: held, signal `GATE-HINT-SAFER-FLIP`.
5. `manual` server, benign re-wording: held `changed`, `held_verdict: clean`, `held_reason: awaiting_review`.
6. `go run ./cmd/qapilot report` coverage gate and `qapilot mutate` pass with the updated model.

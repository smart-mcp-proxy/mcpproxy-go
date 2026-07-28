# Research: Security Scanner Web UI + Trust-Mode Controls (088)

Sources: 4-agent codebase-mapping workflow (2026-07-28, post-#919 merge) + 4-round Codex cross-review of spec.md. All decisions below are settled; no NEEDS CLARIFICATION remain.

## D1. Data source for held-tool evidence

- **Decision**: Switch the ServerDetail approvals panel from `GET /api/v1/servers/{id}/tools/export` to `GET /api/v1/servers/{id}/tools`; the diff dialog keeps `GET .../tools/{tool}/diff`.
- **Rationale**: The export handler's JSON struct (`internal/httpapi/server.go:5284-5294`) deliberately omits `held_reason`/`held_verdict`/`held_signals`; the tools endpoint enriches them (`server.go:2871-2875`), as does the diff endpoint (`server.go:5238-5244`). Extending export would be a backend change for no benefit.
- **Alternatives considered**: (a) extend export payload — rejected: violates zero-backend-change goal and duplicates an existing surface; (b) join export + tools client-side — rejected: two fetches for one list.
- **Consequence**: `types/api.ts` `ToolApproval` is replaced/augmented by the generated `contracts.Tool` shape (which already carries `held_*` at `contracts.ts:129-135`); the panel's field mapping updates accordingly (status field name parity checked in tests).

## D2. Quarantine banner state derivation (client-visible facts only)

- **Decision**: Derive exactly 4 states from `(trust_mode, quarantined, security_scan.status, security_scan verdict/finding_counts)`:
  1. `scan-running`: `trust_mode==scan && quarantined && security_scan.status=="scanning"` → "security scan in progress; its result determines the next step" (no auto-approval promise).
  2. `scan-blocked`: `trust_mode==scan && quarantined && verdict non-clean (status dangerous|warnings)` → "scan verdict blocked automatic approval" + verdict/risk/counts + report path.
  3. `scan-failed`: `quarantined && security_scan.status=="failed"` → "scan could not complete" + retry action; never presented as a threat verdict.
  4. `manual-review`: all other quarantined cases → "awaiting manual review" (+ latest summary if present; no summary → offer scan CTA).
- **Rationale**: `HasApprovalBaseline` (admission-window eligibility, `internal/server/server.go:531-539`) is server-internal — the page cannot distinguish admission scans from re-scans nor promise auto-approval (Codex R1-1/R2-1). `security_scan.status` includes `scanning|failed|clean|warnings|dangerous|not_scanned` so all four states are payload-derivable.
- **Alternatives considered**: additive `admission_eligible` field on the server payload — deferred (allowed by spec assumption but not needed for honest copy).

## D3. Held-signal display scope

- **Decision**: Operate on the delivered `held_signals` list as-is: client-side TPA-first ordering (`tpa.TPA-YYYY-NNNN.*` pattern → extract TPA id label), display cap with "+N more" counting only received-but-collapsed items.
- **Rationale**: Storage caps evidence at `MaxToolHeldSignals=16` in first-seen producer order with no overflow count (`internal/storage/models.go:211-215, 256-277`) — a TPA id dropped at storage time is unrecoverable client-side (Codex R1-3). CLI parity: `formatToolHold`/TPA-hoisting (`cmd/mcpproxy/tools_cmd.go:313-392`, fix b5d51e06) also operates post-storage.
- **Alternatives considered**: producer-side TPA-first ordering before the cap — recorded in spec Assumptions as an additive backend improvement, out of scope here.

## D4. Evidence → scan report linking

- **Decision**: Best-effort link to the server's most recent scan report (existing `scanReportPath(job_id)` route `/security/scans/:jobId`); pass matched signal ids via query (`?signal=`) so `ScanReport.vue` can highlight findings whose `signals[]` intersect. No report → show signals + "Run scan" CTA, no dead link.
- **Rationale**: Hold evidence carries no job/report/finding id (`internal/storage/models.go:237-253`); tool-change holds come from a synchronous in-process scan (`scanner.ScanToolMetadataVerdict`) that persists no report (Codex R1-4). The latest per-server report is reachable via existing `GET /servers/{id}/scan/report` (job_id inside) — already used by ServerDetail.
- **Alternatives considered**: exact finding anchoring via new evidence fields — additive backend work, deferred.

## D5. Live-update event coverage

- **Decision**: ServerDetail listens to BOTH window events re-dispatched by `stores/system.ts`: `mcpproxy:scan-settled` (refresh scan summary + banner + approvals when `server_name` matches) and `mcpproxy:servers-changed` (refresh approvals + server projection — approvals via CLI/MCP emit this). Existing MCP-2740 scan-status polling stays as fallback; SSE loss regresses nothing (FR-020).
- **Rationale**: Approvals do NOT emit scan-settled; the current watcher reloads approvals only on connected/enabled transitions (`ServerDetail.vue:1658-1687`), so CLI approvals were invisible (Codex R1-5). `security.scan_settled` payload lacks verdict/risk (`event_bus.go:657-668`) → refetch server list/scan summary on event rather than trusting payload.
- **Alternatives considered**: new SSE event for tool approvals (`activity.tool_quarantine_change` exists) — usable later; `servers.changed` suffices and is already re-dispatched.

## D6. Add-server flow control

- **Decision**: Replace `AddServerModal.vue`'s `quarantined` checkbox (lines ~175-199/582-592/804-807) with the TrustModeSelector (manual preselected); the POST body sends `trust_mode` and omits `quarantined` so the backend derives admission via `QuarantineDefaultForServer` (`config.go:1635-1655`). Import tab / registry / onboarding flows unchanged (safe defaults).
- **Rationale**: POST treats `quarantined` as an explicit override AFTER trust-mode derivation (`httpapi/server.go:1556-1578`) — keeping both controls lets users contradict the mode semantics (Codex R1-6).

## D7. Trust-mode display & derivation

- **Decision**: Show the reported `trust_mode` as-is; when it's not one of auto|scan|manual and non-empty, show raw value + "effective: manual" note; empty → "manual (default)". No legacy-provenance labeling. Saving always PATCHes `{trust_mode}` only. Client util `effectiveTrustMode()` mirrors backend fail-closed rule for display.
- **Rationale**: Load-time normalization writes migrated legacy values INTO `TrustMode` (`config.go:1047-1066`), so provenance is indistinguishable client-side (Codex R1-7). PATCH semantics: empty string = leave unchanged (`server.go:1846`); `restart_required` comes back in the response message envelope and must be surfaced (existing pattern from the auto-approve card, `ServerDetail.vue:2710`).

## D8. Security tab un-gating & deep-scan toggle

- **Decision**: Drop `v-if="hasEnabledScanners()"` on the Security tab (`ServerDetail.vue:286`) and the Docker-required disable on Scan Now; baseline scanning is always available (spec 077: deterministic engine always on; in-process scanner id `tpa-descriptions`). Skipped Docker scanners render via the existing `deep_scan.skipped_scanners` descriptor. Add `security.deep_scan.enabled` toggle to `securityFields` in `views/settings/fields.ts` (dotted-path FieldDef, hot-reloadable — `security` block is deep-compared in `DetectConfigChanges`).
- **Rationale**: The gate predates spec 077's always-on baseline; QA SRF-04 and the gaps map both flagged unreachable baseline results on default installs.

## D9. Hygiene copy

- **Decision**: Replace the MCP-2917 hint (`ServerDetail.vue:350-354`) recommending `skip_quarantine: true` with copy pointing at the trust-mode selector ("set trust mode to Auto to trust this server's tool changes"), linking the Configuration tab.
- **Rationale**: `skip_quarantine` deprecated by MCP-2931 → `auto_approve_tool_changes` → now `trust_mode` (086 FR-012/FR-015).

## D10. Testing strategy

- **Decision**: Pure-function utils (`trustMode.ts`, `holdEvidence.ts`, banner derivation) carry the logic and get exhaustive table-driven vitest specs; component specs mount the two new leaf components + targeted ServerDetail behaviors (existing `server-detail-auto-approve.spec.ts` pattern). Fixtures cover: unset/invalid/legacy/explicit trust modes, both hold reasons, clean-verdict+coverage hold, 16-signal cap, missing report, SSE loss, special-character server names (`encodeURIComponent` route convention). Playwright sweep per `docs/development/web-ui-verification.md` is the E2E gate; results land in `specs/088-scanner-trust-ui/verification/`.
- **Rationale**: SC-007's dual gate (existing suite + new behavior specs); vitest include path constraint (`frontend/tests/unit/*.spec.ts` only).

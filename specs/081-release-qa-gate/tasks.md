# Tasks: Release Qualification Gate (Spec 081)

Mirrors the `release-qa-gate` epic in `roadmap.yaml`. Each task is one PR;
operating notes live in `docs/development/release-gate.md`.

- [x] T1 — tag-blocking `release-qa-gate.yml`: server-type matrix (stdio/http/sse/docker/oauth), US2 invariants (activity-log request ids, counters, quarantine flow, upgrade-in-place), publish jobs in `release.yml`/`prerelease.yml` gated on the verdict, `scan-eval` unconditional on tags (US1, US2; FR-001–FR-015, FR-022) — #819
- [x] T2 — Playwright Web UI sweep wired into the gate as `advisory/web-ui-sweep` (US3; FR-016/FR-017, advisory until promoted) — #1030
- [x] T3 — macOS tray smoke on a macOS runner, advisory until 3 consecutive passes: `advisory/macos-smoke` builds the Swift tray + runs `swift test`, boots an isolated core, launches an ad-hoc-signed bundle against it and asserts the socket handshake (core version read over the socket) and a clean SIGTERM exit; local `scripts/test-macos-smoke.sh`. The `mcpproxy-ui-test` menu assertions need an Accessibility grant that hosted runners do not give, so they remain a follow-up (US4; FR-019–FR-021)
- [x] T4 — surface-state consistency: blocking `invariant/surface-consistency` job — CLI (`upstream list -o json`), MCP (`upstream_servers list`) and the rendered Web UI agree with the core's `/api/v1/servers` on enabled, quarantined, connection state, `health.level`/`admin_state`/`status` and tool counts across the five-type matrix plus quarantined/disabled/unhealthy servers; settled, live (no reload) and reload phases; per-field diff on failure; `--seed-mismatch` / `seed_consistency_mismatch` negative path; local `scripts/test-surface-consistency.sh`. Tray recorded as not-compared until T3 (US5; FR-018)
  - [x] T4.1 — failing comparator tests first (`cmd/release-gate/consistency_test.go`, `consistency_workflow_test.go`)
  - [x] T4.2 — `release-gate consistency` subcommand + Web UI reader `e2e/web-ui-sweep/surface-snapshot.mjs`
  - [x] T4.3 — manifest: `reserved/surface-consistency` → blocking `invariant/surface-consistency`; gate job + verdict dependency
  - [x] T4.4 — local runner `scripts/test-surface-consistency.sh`; docs

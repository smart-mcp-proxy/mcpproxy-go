# mcpproxy-roadmap

> **Maintained by**: Paperclip CEO agent (heartbeat + on-PR-merge)
> **Last full rewrite**: 2026-04-25 (initial seed)
> **Cadence**: rewritten in full on each goal milestone (anti-spam: max one rewrite per 24h per article)

## In flight

Specs with active tasks and recent commits (lookback: 30 days).

- [[spec-042-telemetry-tier2]] — telemetry v3 schema extension (env_kind, activation funnel, autostart) — in `internal/telemetry/`, multi-platform
- [[spec-043-linux-package-repos]] — .deb/.rpm via nfpm + R2 distribution — packaging + CI
- [[spec-044-diagnostics-taxonomy]] — stable error-code catalog with REST + CLI surfacing — cross-cutting
- [[spec-044-retention-telemetry-v3]] — retention + activation funnel signals — telemetry
- [[spec-037-macos-swift-tray]] — macOS native Swift tray app design + CI — `native/macos/`

## Recently shipped (last 30 days)

- 2026-04-25 PR #407 — fix(webui): tooltip-left on Scan Now button to prevent right-edge clipping
- 2026-04-25 PR #408 — feat(macos-tray,api): expose resolved isolation defaults + per-field clear
- 2026-04-XX [[spec-044]] — diagnostics taxonomy + telemetry v3 (commits 1a0646f8, ebcbfcc6)
- 2026-04-XX [[spec-043]] — Linux package repos (commit 19b853a5, 0776a15a)
- 2026-04-XX [[spec-040]] — server UX subset (commit c2007936)
- 2026-04-XX fix(macos-tray) — preserve bool fields on server PATCH (commit 74f2bfd8)
- 2026-04-XX fix(docker) — probe detects Docker via full path when PATH is minimal (commit 7619f0ab)

## Planned (approved, not yet started)

Specs with no `tasks.md` or empty tasks.

- [[spec-037-macos-swift-tray]] — execution awaiting CEO routing
- [[spec-038-mcp-accessibility-server]]
- [[spec-039-connect-and-dashboard]]
- [[spec-039-scanner-qa-audit]]
- [[spec-039-security-scanner-plugins]]
- [[spec-041-quarantine-invariants]]
- [[spec-045-paperclip-cockpit]] — this very article's parent. Bootstrap in progress.

## Parked

(empty — items here are explicitly deprioritized but not abandoned)

---

**Cross-links**: Each `[[spec-NNN-name]]` resolves to the spec dir under `specs/NNN-name/` in the mcpproxy-go repo. Each PR number resolves to `https://github.com/smart-mcp-proxy/mcpproxy-go/pull/NNN`.

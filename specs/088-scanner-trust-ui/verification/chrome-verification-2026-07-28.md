# Live Chrome verification — spec 088 (2026-07-28)

Method: Claude-in-Chrome driving the embedded Web UI of a locally built binary (`make build`, v0.52.1 base) on an isolated instance (`127.0.0.1:18088`, scratch `--config` + `--data-dir`, throwaway API key). Performed instead of the Playwright sweep per operator instruction; unit/integration coverage (563 vitest tests) complements it.

## Verified live (screenshot-confirmed)

| Check | FR | Result |
|-------|----|--------|
| Server tile shows trust-mode badge ("Manual") | FR-007 | PASS |
| Quarantine banner "Awaiting manual review" + "No security scan has run yet — you can run one first" (absent `security_scan` → run-scan suggestion) | FR-013/FR-015 | PASS |
| Security tab present with ZERO deep scanners enabled | FR-016 | PASS |
| Configuration tab: tri-mode selector, Manual marked "current default", dual-behavior copy (tool changes + admission) per mode, "least safe" chip on Auto | FR-001/FR-002 | PASS |
| Auto selection opens inline confirm ("Apply "Auto" trust mode?" — rug-pull + no-quarantine-no-scan copy); Cancel reverts to Manual with no PATCH | FR-003 | PASS |
| Selecting Scan saves immediately: `PATCH {"trust_mode":"scan"}` persisted (API read-back `trust_mode: "scan"`) | FR-004 | PASS |
| Backend chain observed: `triggering admission baseline scan for scan-mode server (spec 086 FR-011)` → clean verdict → `auto-approved scan-mode server` → `quarantined: false` | 086 FR-011 integration | PASS |
| Page updated LIVE while open (no reload): banner cleared, Quarantined: No, Security tab chip "just now", Scan radio selected | FR-019 | PASS |
| Add Server modal: trust-mode selector present (Auto/Scan/Manual radios), NO quarantine checkbox anywhere in the dialog | FR-006 | PASS |
| Settings → Security & Access: "Deep scan (Docker scanners)" toggle present | FR-018 | PASS |
| Console: zero errors during the whole session | SC-007 | PASS |

## Covered by unit tests only (not visually exercised — requires TPA rug-pull harness)

- Hold-evidence rendering (badge, TPA-first chips, diff-dialog evidence, `?signal=` report links): 40+ tests across hold-evidence(.badge)/server-detail-hold-evidence/tools-view-hold-evidence specs.
- Banner `scan-blocked` / `scan-failed` states: quarantine-banner-states + server-detail-quarantine-banner specs (derivation + wiring).
- Recommended before release: run the tpa-rugpull QA harness (DESC_FILE ctl-server + TPA-2026-0001 poison string) against a build for a visual pass of US2.

## Incidental finding

`./mcpproxy serve --data-dir <scratch>` still loads `~/.mcpproxy/mcp_config.json` (30 real servers) unless `--config` is also passed — surprising isolation footgun for dev/QA instances; not a spec-088 issue.

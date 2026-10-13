# Quickstart: Security Scanner Web UI + Trust-Mode Controls (088)

## Build & unit tests

```bash
cd frontend && npm test          # vitest — specs live in frontend/tests/unit/*.spec.ts ONLY
make build                       # embeds frontend/dist into the Go binary (stale-embed gotcha: rebuild Go after frontend changes)
```

## Run an isolated dev instance (never touch the tray's :8080 / default data dir)

```bash
pkill -f 'mcpproxy.*18088' 2>/dev/null || true
./mcpproxy serve --listen 127.0.0.1:18088 --data-dir /tmp/mcpproxy-088-qa --api-key qa088 &
open "http://127.0.0.1:18088/ui/?apikey=qa088"
```

## Seed the scenarios

```bash
# 1. Trust-mode display/save (US1): add a server, flip modes via UI, verify via:
curl -s -H 'X-API-Key: qa088' http://127.0.0.1:18088/api/v1/servers | jq '.data.servers[] | {name, trust_mode, quarantined}'

# 2. Held evidence (US2): use the TPA QA harness (memory: tpa-rugpull-qa-harness) —
#    DESC_FILE ctl-server rug-pull with the TPA-2026-0001 poison string on a trusted server
#    → tool goes 'changed' with held_reason=scan_findings + tpa.TPA-2026-0001.* signals.

# 3. Banner states (US3): scan-mode quarantined server (scanning / dangerous verdict),
#    failed scan (point a scan at a dead stdio server), manual-mode quarantined server.

# 4. Baseline visibility (US4): default config (zero deep scanners) → Security tab + Scan Now present.

# 5. Live refresh (US5): with the server page open:
./mcpproxy tools list --server <name> ...            # observe held column parity
curl -s -X POST -H 'X-API-Key: qa088' -H 'Content-Type: application/json' \
  -d '{"approve_all":true}' http://127.0.0.1:18088/api/v1/servers/<name>/tools/approve
# page must reflect the approval without reload
```

## Playwright verification sweep (required — frontend touched)

Per `docs/development/web-ui-verification.md`: mcpproxy on 127.0.0.1:18081 + throwaway data dir, symlink `e2e/playwright/node_modules`, chromium-1217 pin, `[data-test=...]` locators, `domcontentloaded` (networkidle hangs on SSE). Report → `specs/088-scanner-trust-ui/verification/`.

Key new data-test hooks: `trust-mode-selector`, `trust-mode-option-{auto|scan|manual}`, `trust-mode-invalid-note`, `hold-evidence-badge`, `hold-tpa-chip`, `quarantine-banner-state`, `deep-scan-toggle`.

## Lint / pre-push

```bash
/opt/homebrew/bin/golangci-lint run --config .github/.golangci.yml ./...   # v2, stricter than local script (should be a no-op — no Go changes expected)
./scripts/test-api-e2e.sh                                                  # required before commit
```

# Quickstart and Live Verification Plan: Spec 117

## 1. Walkthrough (operator's view after all three PRs)

1. Add a server. It is quarantined and its definitions are captured. Open the review (Web `/review/<server>`, tray sheet, or `mcpproxy review show <server>`). Every tool row carries a fingerprint the client keeps.
2. The upstream changes a tool while the review is open. Approving shows "The review is out of date (definition changed since review: read_note) — nothing was approved". The review reloads with the new definition and the server stays quarantined.
3. Approve again. Only the reviewed tools are promoted. A tool the upstream adds a minute later appears in the Review queue as pending.
4. Months later the server replaces its whole toolset. The new tools wait in the Review queue. No discovery pass auto-approves them, before or after a restart.

## 2. Fixtures

**Inventory fixture**: a stdio MCP server that re-reads its `tools/list` from a JSON file on every call, so a test can replace the inventory live. The design-time copy is kept with the evidence (`docs/qa/0.70.0-ux-2026-10-10/remediation/evidence/s117-design/invfixture.py` in the maintainer checkout; never committed). Copy it into the run dir.

**Harness**: `/private/tmp/claude-501/qa070/start.sh <run> <port> <binary> min`. Then add servers via `POST /api/v1/servers`, or write the run's `config.json` directly as the design repro did. Use a high port (not 8080), a scratch HOME/data dir, and `stop.sh <run>` only.

**Go fixtures**:
- `internal/runtime` test runtime with storage plus `toolApprovalReadHook`, `discoveryAfterPublish`, `localReindexAfterSnapshot`, `quarantinedCaptureBeforePersist` (existing hooks).
- New hooks added by PR-3: `inventoryBeforeApplyLock(server, seq)` and `inventoryAfterDecisions(server, seq)` to pause passes deterministically.

## 3. Live checks (one per PR; evidence in `docs/qa/0.70.0-ux-2026-10-10/remediation/evidence/<key>/EVIDENCE.md`, never committed)

### L1: durable baseline (PR-1, key `s117-pr1`)
1. Trusted server `notes`, inventory `[read_note]`. Wait for `approved_by: auto-baseline`.
2. Replace with `[wipe_all_notes]` (destructive). Run `POST /servers/notes/refresh` three times. Restart the daemon, then run refresh once more.
3. Expect:
   - review `wipe_all_notes pending` after every step;
   - `GET /api/v1/index/search?q=wipe` shows no hit;
   - MCP `call_tool_destructive notes:wipe_all_notes` is refused (`mcp.py`), and the fixture saw no `tools/call`.
4. Variant: put an authoritative empty inventory `[]` between A and B. Same expectations.
5. Variant: a quarantined server approved with an empty review (`expected_hashes: {}` once PR-2 exists; a blind CLI approval before that). Tools added afterwards are pending.

### L2: review binding (PR-2, key `s117-pr2`)
1. Quarantined `notes2` with `[read_note]` (read-only). Capture it, then save `GET /review` (H1 = `current_hash`).
2. Change `read_note` to destructive. Capture. `POST security/approve {"block":[],"expected_hashes":{"read_note":H1}}` → expect 409 `review_out_of_date`, `stale_tools.changed == ["read_note"]`, the server still quarantined and the record still pending.
3. `GET /review` → H2. Approve with H2 → 200, and `read_note` is approved at the destructive definition the operator has now seen.
4. Add `drop_db` after the GET and before the approve → 409 `unreviewed: ["drop_db"]`. With `block:["drop_db"]` → 200, and `drop_db` is approved+disabled.
5. Unbound approve (no field) on a fresh quarantined server → 200. A tool added after the approval → pending.
6. Web (Playwright, review screen):
   - change a definition between load and click → the notice shows "out of date", the reloaded rows are visible, no force dialog;
   - a force retry after "dangerous" sends the original `expected_hashes` (check the request body in the network log).
7. CLI: run `mcpproxy review show notes2 -o json > r.json`, change the definition, then run `mcpproxy review approve notes2 --from-review r.json --yes` → exit 1 with the hint line; `-o json` prints the 409 envelope. Interactive approve without the file lists the reachable tools before asking.
8. Tray (when #1572 is merged): repeat step 2 from the review sheet → the sheet reloads and shows the core's message.

### L3: capture order (PR-3, key `s117-pr3`)

Most interleavings are proved with paused-pass Go tests. The live run checks for absence of regressions and for throughput:
1. `big` profile (320-tool trusted server plus the 180-tool quarantined one). Time a full sweep on main and on PR-3 (SC-005: ≤10% slower).
2. Run 50 iterations of a concurrent loop: `refresh` + approve one held tool + quarantine/unquarantine toggle. Check:
   - no deadlock (the daemon answers `/api/v1/status` within 2 s throughout);
   - final state consistent: the review approval states match the index (`GET /index/search`) and dispatch (`call_tool_read` on approved read tools succeeds, held tools are refused).
3. Replace the endpoint of a quarantined server (edit its `command`/`args` in the run's `config.json` so the file watcher reloads it) while `review fetch` runs → no record from the old endpoint (log line "discarded capture from retired client").

## 4. Commands (each PR)

```bash
go test -race ./internal/runtime/... ./internal/storage/... ./internal/httpapi/... ./internal/security/scanner/... ./internal/upstream/... ./cmd/mcpproxy/...
go test -race -tags server -timeout 20m -skip "E2E|Binary|MCPProtocol|TestInfoEndpoint|TestGracefulShutdownNoPanic|TestSocketInfoEndpoint" ./internal/serveredition/... ./internal/config/... ./internal/oauth/... ./internal/server/... ./internal/httpapi/... ./internal/storage/...
(cd frontend && npx vitest run)                              # PR-1 copy, PR-2 binding
/opt/homebrew/bin/golangci-lint run --config .github/.golangci.yml ./...
/opt/homebrew/bin/golangci-lint run --config .github/.golangci.yml --build-tags server ./...
./scripts/test-api-e2e.sh                                    # on a high port / scratch HOME (memory isolated-dev-instance)
make build && git checkout -- frontend/package-lock.json     # only if npm touched it
```

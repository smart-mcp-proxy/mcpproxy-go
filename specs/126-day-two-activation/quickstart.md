# Quickstart: verifying Spec 126 (Day-Two Activation)

Live checks run on an isolated scratch instance: high port, scratch data dir, own log dir. Never run them against the maintainer's live app. Kill any scratch core before you rebuild, because the core locks the DB.

```bash
go build -o /tmp/s126/mcpproxy ./cmd/mcpproxy
mkdir -p /tmp/s126/data /tmp/s126/logs
echo '{"listen":"127.0.0.1:18919","api_key":"s126-test-key","mcpServers":[]}' > /tmp/s126/mcp_config.json
# --config is mandatory: --data-dir alone still reads ~/.mcpproxy/mcp_config.json
MCPPROXY_TELEMETRY=false /tmp/s126/mcpproxy serve --config /tmp/s126/mcp_config.json \
  --data-dir /tmp/s126/data --log-dir /tmp/s126/logs &
# Stop only this instance: pkill -f 'mcpproxy serve.*s126'
set -euo pipefail   # every assertion below stops the run on failure
K='X-API-Key: s126-test-key'; B=http://127.0.0.1:18919/api/v1
```

## L1. Starter offer → add → bound approval → indexed (US1, SC-005)

```bash
curl -s -H "$K" $B/onboarding/starter | jq '.data | {eligible,reason,preselect,launcher,candidate}'
# expect eligible=true, candidate.kind="time" (uv installed), preselect=true

CMD=$(curl -s -H "$K" $B/onboarding/starter | jq -er .data.candidate.command)
curl -s -H "$K" -X POST $B/onboarding/starter \
  -d "{\"kind\":\"time\",\"surface\":\"wizard\",\"shown_command\":\"$CMD\"}" | jq .data.server
# expect quarantined=true

# Before approval: tool_count stays 0 and a call is refused.
curl -s -H "$K" $B/servers | jq '[.. | .tool_count? // empty] | add'   # 0 (indexed tools)
sleep 20; R=$(curl -s -H "$K" $B/servers/time/review)
echo "$R" | jq -e '(.data.tools | length) > 0 and all(.data.tools[]; (.current_hash // "") != "") and (.data.server.incarnation != null)'
echo "$R" | jq '[.data.tools[].name]'   # ["convert_time","get_current_time"]
# SC-005: a call before approval is refused and never reaches the upstream
N0=$(curl -s -H "$K" "$B/activity?type=policy_decision&server=time&tool=get_current_time&status=blocked&limit=1000" | jq '.data.activities | length')
# If the core is on mcp-go >= v1.0, also send `Mcp-Method: <method>` on each POST.
# Use the daemon's MCP endpoint, NOT `mcpproxy call` (it falls back to a standalone mode that bypasses the daemon).
M=http://127.0.0.1:18919/mcp; HJ='Content-Type: application/json'; HA='Accept: application/json, text/event-stream'
SID=$(curl -s -D - -o /dev/null -H "$HJ" -H "$HA" $M -d '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"s126-qa","version":"0"}}}' | awk -F': ' 'tolower($1)=="mcp-session-id"{print $2}' | tr -d '\r')
curl -s -H "$HJ" -H "$HA" -H "Mcp-Session-Id: $SID" $M -d '{"jsonrpc":"2.0","method":"notifications/initialized"}' >/dev/null
curl -s -H "$HJ" -H "$HA" -H "Mcp-Session-Id: $SID" $M -d '{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"call_tool_read","arguments":{"name":"time:get_current_time","args_json":"{\"timezone\":\"UTC\"}"}}}' \
  | tee /tmp/s126/refused.txt | grep -q '"isError":true'      # must be refused
grep -qi 'quarantin' /tmp/s126/refused.txt                      # names quarantine
# The quarantine gate writes a policy_decision (no tool_call completion). Zero upstream dispatch is proven by the SC-005 safety test's upstream spy, not here.
N1=$(curl -s -H "$K" "$B/activity?type=policy_decision&server=time&tool=get_current_time&status=blocked&limit=1000" | jq '.data.activities | length')
[ "$N1" -eq $((N0 + 1)) ]   # exactly one NEW blocked decision, for this attempt

# Bound approval (Spec 117), as the wizard sends it.
H=$(echo "$R" | jq -ec '[.data.tools[] | {(.name): .current_hash}] | add')   # hashes and incarnation were asserted non-empty above
I=$(echo "$R" | jq -e .data.server.incarnation)
curl -s -H "$K" -X POST $B/servers/time/security/approve \
  -d "{\"expected_hashes\":$H,\"expected_incarnation\":$I}" | jq -e .success
curl -s -H "$K" $B/servers | jq '[.. | .tool_count? // empty] | add'   # >= 2
curl -s -H "$K" $B/onboarding/state | jq .data.day_two     # starter_added/live true, starter_kind "time"
```

**Negative checks:**
- Call `POST /onboarding/starter` with an agent token: 403, and no server is added (check `GET /servers`).
- Remove and re-add `time` between the review and the approval: the approval returns 409 `server_replaced`.
- Send `"shown_command":"uvx something-else"`: the response is 409 `starter_changed`, and nothing is added.
- Add `{"registries":[{"id":"reference","protocol":"custom/docker",...}]}` to the config: the offer becomes `reason=reference_overridden`.
- Put a user server named `time` with a different command in the config: `time` is skipped (`name_taken`), and `sequentialthinking` is offered if `npx` resolves.

## L2. Web wizard and Home (US1, US2, US3)

PR-B runs steps 2-4, PR-C runs step 1, and the combined sweep after PR-B, PR-C and PR-D merge runs all of them.

Run the Playwright sweep (`docs/development/web-ui-verification.md`) against the scratch instance with an empty config:
1. The wizard auto-opens. The Clients tab, after one fake client connect (use the `MCPPROXY_FIXTURE_PATH` client fixtures), shows "Connect another client".
2. The Servers tab shows the pre-selected starter card with its command and "Add and inspect".
3. After the inline approval, the Verify tab shows the tool count and the first prompt.
4. Dismiss a second fresh instance's wizard without a starter. Home's "Get started" card offers "Add and inspect time", not pre-selected.

Take screenshots for the PR. Check for no console errors.

## L3. Weekly digest (US4)

Seed the activity log with the fixture from task T052 (`internal/storage/testdata/digest_fixture.json`). It produces 10 days of records: two clients, a refusal pair, quarantine and profile blocks, holds that enter and re-enter, one already-held change, sensitive detections, and a prune that advances the watermark. Then:

```bash
curl -s -H "$K" "$B/activity/summary?period=7d&view=cli" | jq .data.digest
# Pin the CLI to the scratch instance; without these it may query the live daemon via the default socket.
MCPPROXY_TRAY_ENDPOINT=http://127.0.0.1:18919 MCPPROXY_API_KEY=s126-test-key \
  /tmp/s126/mcpproxy activity summary --since 7d -o json \
  --config /tmp/s126/mcp_config.json --data-dir /tmp/s126/data | jq .digest     # identical
```

The figures must equal the fixture's expected values (`internal/storage/testdata/digest_expected.json`).

## L4. Telemetry payload (US5, SC-007)

```bash
go test ./internal/telemetry -run TestPayloadV17 -v              # T012: asserts and logs a sample schema-17 payload
go test ./internal/telemetry -run 'TestScanForPII.*DayTwo' -v
```

Then run a fresh instance, with telemetry on, against a local receiver stub:
- `GET /api/v1/telemetry/payload` (or `mcpproxy telemetry show-payload`) shows `day_two.install_origin: fresh`, `install_age: d0_6` and `upstream_outcomes.coverage: full`.
- After one usable call and one `isError` call, it shows `succeeded: 1-10` and `unusable: 1-10`.
- Restart over the same data directory: the first window shows `coverage: partial`.

## L5. Native (US2, US3, US4)

Use the tray dev-swap recipe (`docs/development/macos-tray.md`, `mcpproxy-ui-test`):
- the Home empty state shows the starter card;
- the Connect Client success state lists "Connect another client";
- the glance block shows "This week: N calls · H held · B blocked".

## Telemetry queries for judging E3 (run by the maintainer on D1)

They use the canonical CI filter and identity keys from the 2026-10-20 telemetry note. `release_v` is the first build carrying PR-B, the starter UI.

**SC-001 and SC-004** come from the same FR-040 query (`day-two-cohort.ts`), in its `tools_indexed_d0_13` and `clients_1plus_day0` / `clients_2plus_day0` figures, on the same eligible cohort. The query never takes them from a separate cohort definition.
- SC-001's 71.5% figure came from an earlier instrument (any heartbeat, no new-install test), so it is not the comparison point. The PR-A baseline run becomes the reference.
- The SC-001 companion, the sticky `first_tool_indexed_ever`, ships with PR-B. It is read on rows at PR-B's schema version or later where the field is present.

**SC-003: launcher availability and starter conversion.** These fields ship with PR-B at its schema version, written `:pr_b_schema` below. Read them only where the field is present:

```sql
SELECT json_extract(payload_json,'$.day_two.starter_launcher') AS l, COUNT(DISTINCT anonymous_id)
FROM heartbeats
WHERE schema_version >= :pr_b_schema AND json_type(payload_json,'$.day_two.starter_launcher') IS NOT NULL AND l != ''
GROUP BY l;
-- conversion: share of anonymous ids with any starter_offered = 1 that ever have starter_live = 1
```

**SC-002 and SC-008: return in days 7–13, active return, starter-only, coverage.** These come from the FR-040 query in `mcpproxy-dash` (`src/lib/server/queries/day-two-cohort.ts`, tasks T085), never from a one-off script.
- Run it in `baseline` mode before the starter release, and record the result as the SC-002 reference.
- Run it in `schema17` mode for the release cohorts.

It reports, per weekly cohort:
- eligible new installs;
- day 0–6 activity;
- return in days 7–13;
- active return, which is dispatch-based in the baseline and completion-based on schema 17;
- starter-only installs;
- the research-R13 coverage rows.

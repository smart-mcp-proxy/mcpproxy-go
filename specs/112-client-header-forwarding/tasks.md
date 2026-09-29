---
description: "Tasks for Spec 112 — MCP client header forwarding"
---

# Tasks: MCP Client Header Forwarding

**Input**: [spec.md](spec.md), [plan.md](plan.md). TDD: every task writes its failing test first, then implements.

## Phase 1 — Pure pieces (`internal/headerfwd`)

- [x] T001 [P] [US3] `Denied(name)` table test: every FR-004 name and prefix in lower, canonical and upper case; allowed samples (`X-User-Id`, `X-Tenant-Id`, `Accept-Language`) pass (`internal/headerfwd/deny.go`, `deny_test.go`) — FR-004
- [x] T002 [P] [US3] `ValidateNames` / `NormalizeNames`: invalid token, wildcard, >32, duplicate case-insensitive, denied, static-key collision; normalize returns kept + dropped (`validate.go`) — FR-005
- [x] T003 [P] [US1] `Capture`: union filter, deny list, names listed in `Connection`, multi-value join, 4 KiB / 16 KiB caps, control-char drop, empty omit, clone (mutating `r.Header` afterwards does not change the snapshot) (`capture.go`) — FR-006, FR-012
- [x] T004 [P] [US6] `Snapshot` formatting: `%v`, `%+v`, `%#v`, `%s`, `json.Marshal`, `zap.Any`, `zap.Object`, `slog` never contain the value (`snapshot.go`) — FR-007
- [x] T005 [P] [US4] `Outbound(policy)`: disabled → empty; non-HTTP transport → empty; static collision dropped; allowlist subset — FR-010
- [x] T006 [P] [US3] `HeaderFunc`: no key B → nil; key A only → nil; re-applies deny list and static collision; returns a fresh map each call — FR-009, FR-010
- [x] T007 [P] [US6] `Scrub`: exact-value (≥4), JSON-escaped value, and name-pattern forms (`Name: v`, `"Name" : "v"`, `Name=v`, case-insensitive) incl. a 2-char value; base64 of the value is NOT caught (boundary asserted) — FR-016
- [x] T008 [P] `Digest`: stable for same set, differs by value, per-process key, never equals raw values — FR-017

## Phase 2 — Config and persistence

- [x] T009 [US5] `Config.ForwardClientHeaders` + `IsClientHeaderForwardingEnabled` (nil/true/false) + env override `MCPPROXY_FORWARD_CLIENT_HEADERS` via process-only Field; save round-trip does not persist env value (`internal/config/config.go`, `loader.go`, `process_overrides.go`) — FR-001, FR-003
- [x] T010 [US1] `ServerConfig.ForwardHeaders`; `ValidateDetailed` clause; load-time normalization with name-only warning, boot never fails (`config.go`) — FR-002, FR-005
- [x] T011 [US1] `CopyServerConfig` / `MergeServerConfig` (nil keeps, `[]` clears, diff entry) (`merge.go`) — FR-020
- [x] T012 [US5] `DetectConfigChanges` clause for the global switch (resolved bool) (`internal/runtime/config_hotreload.go`) — FR-020
- [x] T013 [US1] `UpstreamRecord.ForwardHeaders` in all 5 conversion sites + storage round-trip test (`internal/storage/models.go`, `manager.go`, `async_ops.go`) — FR-020
- [x] T014 `ServerFieldMaskDecisions` row `forward_headers: NotSecret` (`internal/oauth/serverfields.go`) — FR-021
- [x] T015 [US1] `contracts.Server` + converters + StateView projections; `cmd/generate-types` template + regenerate `frontend/src/types/contracts.ts` — FR-020
- [x] T016 [US1] REST create/PATCH (preserve on omit) and MCP `upstream_servers` add/patch; validation errors surface to caller (`internal/httpapi/server.go`, `internal/server/mcp.go`); `make swagger` — FR-020

## Phase 3 — Capture and dispatch

- [ ] T017 [US1] `clientFacingStreamableOptions(cfgProvider)` with `WithHTTPContextFunc`; test that all five mounts and the `/v1/tool_code` aliases capture, and REST `/api/v1/tools/call` does not (`internal/server/server.go`) — FR-006
- [ ] T018 [US1] `core.Client.SetForwardPolicyProvider`; `CallTool` sets key B, scrubs error and logged result copies, returns the per-call outbound set to its caller (`internal/upstream/core/client.go`) — FR-009, FR-016
- [ ] T018a [US6] Recording sinks scrub the success result: activity `Response` and `ToolCallRecord` built from a result that echoes the sentinel contain no sentinel; the result returned to the client is unchanged (`internal/server/mcp.go`, `mcp_routing.go`, `mcp_code_execution.go`) — FR-016.3
- [ ] T019 [US5] `managed.Client` installs the live provider (after construction and every reconnect); hot-reload test flips switch and allowlist with no reconnect (`internal/upstream/managed/client.go`) — FR-010, US5.3
- [ ] T020 [US4] `transport.HTTPTransportConfig.HeaderFunc`, appended in OAuth and plain branches outside the timeout branches; existing timeout tests unchanged; SSE client untouched (`internal/transport/http.go`) — FR-014
- [ ] T021 [US3] `CheckRedirect` strips key-B names on cross-origin 307; same-origin keeps them. Table test over **every** `CreateHTTPClient` path — plain without static headers (formerly `WithHTTPTimeout` default client), plain with static headers and no trace/Retry-After, plain with trace, plain with Retry-After, OAuth without trace/Retry-After, OAuth with Retry-After — each asserting the cross-origin target never sees the sentinel and the existing per-branch timeout is unchanged (`internal/transport/http.go`) — FR-013
- [ ] T022 [US6] `LoggingTransport` masks key-B and allowlisted names in request and response headers; test uses a custom name unknown to `RedactHeaders` (`X-Tenant-Id`), captures both stdout and a zap observer, asserts the sentinel is absent and the name still present (`internal/transport/logging.go`) — FR-018
- [ ] T023 [US6] `read_cache` stores `ForwardedDigest`; redeem with different/missing digest is refused (`internal/cache`, `internal/server/mcp.go`) — FR-017
- [ ] T024 One-time name-only warnings: allowlist on sse/stdio server; plain-HTTP non-loopback upstream — FR-014

## Phase 4 — Integration tests (`httptest` recording streamable upstream)

- [ ] T025 [US1] Allowed header forwarded on `call_tool_read`, direct surface and `code_execution` sub-calls; missing header omitted; non-allowlisted never sent; other server gets nothing — FR-008
- [ ] T026 [US2] 50 concurrent clients, unique values, `-race`; every upstream call carries its own value — SC-001
- [ ] T027 [US3] Blocked names (all FR-004, 3 casings) under plain, static-header and OAuth upstreams; OAuth upstream receives the proxy token — SC-002
- [ ] T028 [US4] Static collision: static value wins; configs without `forward_headers` produce identical requests — SC-005
- [ ] T029 [US1] `reconnect_on_use` by client A: initialize/tools/list carry nothing, the following `tools/call` does; background discovery and `upstream_servers refresh` carry nothing — FR-008
- [ ] T030 [US5] Disabled via config and env: nothing forwarded — SC-004
- [ ] T031a [US6] Upstream echoes headers in a **successful** result (exact, JSON-escaped, `Name: value`) and in a response header: client gets the unmodified result; sentinel absent from activity `Response`, tool-call record, logs, trace stdout — SC-003, FR-016.3
- [ ] T031b [US1] Mount coverage: enumerate every client-facing streamable mount from the router (`/mcp`, `/mcp/all`, `/mcp/code`, `/mcp/call`, `/mcp/p/<slug>`, `/v1/tool_code` aliases) and assert an allowlisted header reaches the upstream on `tools/call` from each — SC-007
- [ ] T031 [US6] Upstream echoes headers in a 500 body: sentinel absent from `main.log`, per-server log, trace stdout, activity, BBolt tool-call record, audit line, health `LastError`, client error — SC-003
- [ ] T032 REST `/api/v1/tools/call` with an allowlisted-name header forwards nothing — FR-006

## Phase 5 — Docs and verification

- [ ] T033 `docs/configuration.md`: "Client Header Forwarding" section (format, default, deny list, precedence, transport limits, redaction and its best-effort echo boundary, the unverified-client-assertion trust warning per FR-022, limitations), Server Fields row, env var row, Validation bullets
- [ ] T034 `cmd/mcpfixture`: `echo_headers` tool for local verification
- [ ] T035 Run gates: race tests, server-tag tests with CI skip regex, golangci-lint v2 twice, `make swagger` verify, `./scripts/test-api-e2e.sh`
- [ ] T036 Real instance verification (isolated HOME/data dir, high port, mcpfixture upstream, curl `/mcp` with allowlisted + denied headers, grep logs for sentinel) — SC-006
- [ ] T037 Cross-model review (codex, user-specified model), stdin closed, `gtimeout`; ≤10 rounds

## Dependencies

```
T001-T008 (parallel) -> T009-T016 -> T017-T024 -> T025-T032 -> T033-T037
T018 depends on T006, T007; T018a on T018; T019 on T018, T009, T010; T020 on T006; T023 on T008
```

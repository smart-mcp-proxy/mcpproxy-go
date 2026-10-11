# Feature Specification: Review-Bound Approvals, Durable Tool Baselines and Ordered Tool Inventories

**Feature Branch**: `117-review-bound-approvals`
**Created**: 2026-10-11
**Status**: Draft (design only; implementation is split into three PRs, see plan.md)
**Input**: GitHub issues [#1566](https://github.com/smart-mcp-proxy/mcpproxy-go/issues/1566) (approvals not bound to the reviewed definitions), [#1567](https://github.com/smart-mcp-proxy/mcpproxy-go/issues/1567) (tool baseline not durable) and [#1565](https://github.com/smart-mcp-proxy/mcpproxy-go/issues/1565) (out-of-order inventory passes). The OpenCode Sol 6.1 cross-model review of the split UX-02 work (#1555) found all three. They were deliberately left out of the minimal core fix #1571 (`9d0d3c2ca`, the per-server tool-approval lock).

**Related**: Spec 032 (tool-level quarantine, rug-pull hashes), MCP-2931 (trust baseline), Spec 086 (trust modes, scan gate), Spec 105 (raw-name approval keying), Spec 109 (review queue and review screens), PR #1572 (the macOS tray half, #1569). PR #1572 already sends `expected_hashes` and reads `current_hash`; both are inert on today's core. #1568 (Web review-session hygiene) is not in scope.

All code references are to `origin/main` at `ee7834e71` unless stated otherwise.

## Context and Motivation

Tool-level quarantine has three decision points:
- Discovery files a record per tool: pending, approved or changed.
- An operator approves: per tool, all tools, or the whole server through the scan gate.
- The inventory is published to the search index and to the StateView that dispatch reads.

Each point has a gap. Through it, a tool can become reachable although nobody reviewed the definition that is actually live.

| # | Issue | What happens on main | Code |
|---|---|---|---|
| G1 | #1566 | An approval approves whatever definition is current **when it executes**, not the one the operator saw. A definition that changes between `GET /review` and `POST .../approve` is approved unseen. | `approveToolsLocked` sets `ApprovedHash = CurrentHash` (`internal/runtime/tool_quarantine.go:2062-2112`). `handleApproveTools` accepts only `tools`/`approve_all` (`internal/httpapi/server.go:6851-6930`). `securityApproveRequest` has only `force`/`block` (`internal/httpapi/security_scanner.go:335-338`). |
| G2 | #1566 | A server approval promotes **every** pending record after the unquarantine. That includes tools first captured after the operator's review, and the response can still report zero holds. | `QuarantineServer(false)` → `approveBaselineToolsForServer` (`internal/runtime/lifecycle.go:2227-2233`, `tool_quarantine.go:2469-2516`). The scanner commit and the unquarantine are separate steps (`internal/security/scanner/service.go:1790-1830`). |
| G3 | #1566 | Safety annotations are deliberately excluded from the approval hash (`tool_quarantine.go:25-35`). A tool reviewed as read-only that turns destructive therefore keeps its approval, and a review has no way to detect the change. | `CurrentAnnotations` is refreshed while the status stays approved (`tool_quarantine.go:844-847`). The review payload carries no hash at all (`internal/runtime/review.go:112-128`). |
| G4 | #1566 | The CLI and Web review screens send no binding. An empty review or a blind ("approve without seeing tools") approval binds to nothing. | `cmd/mcpproxy/review_cmd.go:41-91`, `frontend/src/components/ReviewScreen.vue:246-269`, `frontend/src/services/api.ts:556-565,1603-1607`. |
| G5 | #1567 | Whether a server "has a baseline" is inferred from the presence of an approved or changed record. Once the last such record is deleted (inventory replaced, authoritative empty refresh), the next discovery pass treats the server as new and auto-approves the new tools. | `serverHasBaseline` / `isBaselinePass` (`tool_quarantine.go:481-491`). Removed-tool record delete (`lifecycle.go:1149-1185`). Promotion `case isBaselinePass && priorHashMatches` (`tool_quarantine.go:880-881`). |
| G6 | #1567 | An approval of an empty review leaves no evidence of the decision. The first inventory after unquarantine is auto-approved as a first baseline. | same as G5 |
| G7 | #1565 | Discovery passes carry no capture order. An older capture applied after a newer one overwrites newer approval records. It also deletes freshly reindexed tools (blocked-tool eviction), restores stale annotations to the index, and republishes stale safety hints to the StateView, which sets dispatch tiers. | `commitDiscoveredTools` / `applyDifferentialToolUpdate` (`lifecycle.go:776-796,1018-1308`). Eviction `lifecycle.go:1188-1200`. StateView publish `lifecycle.go:483-505,944-960`. |
| G8 | #1565 | A pass makes its approval decision under the per-server lock but writes the index outside it. An operator approval can land in between, and the pass then evicts the tool the operator just approved. | The lock is released at the end of `checkToolApprovals` (`tool_quarantine.go:446`). The index writes come after it (`lifecycle.go:1149-1250`). |
| G9 | #1565 | A pass that passed its eligibility check can re-index a server that was quarantined and purged in the meantime. | `QuarantineServer` flips storage under the lock but purges outside it (`lifecycle.go:2186-2195`). `applyDifferentialToolUpdate` does not re-check quarantine. |

**Live reproduction (2026-10-11).** The binary was built from `ee7834e71` and run on the isolated harness at :18917.
- **G5**: a trusted server served `read_note`, which was auto-baselined. Its inventory was then replaced by the destructive `wipe_all_notes`. The first refresh left it pending. The second refresh approved it with `approved_by: auto-baseline`, and `call_tool_destructive notes:wipe_all_notes` reached the upstream.
- **G1/G3**: a quarantined server was reviewed with a read-only `read_note`. The upstream then changed its description and set `destructiveHint`. `POST security/approve` with a stale `expected_hashes` returned 200 `approved_count:1 still_pending:0` and activated the destructive definition.

The evidence is kept outside the repo, at `docs/qa/0.70.0-ux-2026-10-10/remediation/evidence/s117-design/EVIDENCE.md` in the maintainer checkout.

## Scope Boundary

| In scope | Out of scope (unchanged, or a follow-up) |
|---|---|
| A **review fingerprint** per tool in `GET /api/v1/servers/{id}/review` (`current_hash`). It covers the definition the approval hash covers, plus the four safety hints | The rug-pull hash (`calculateToolApprovalHash*`) and its annotation policy. An approved tool whose hints change **after** activation stays approved (Spec 032 policy). Dispatch tiering already reads live hints |
| `expected_hashes` on `POST .../security/approve` and `POST .../tools/approve` (both `tools` and `approve_all`). It is validated under the per-server approval lock. A mismatch returns 409 "out of date" and writes nothing | `POST .../tools/block` / `block_all`. Blocking is never less safe than the reviewed state |
| Server approval runs as **one** critical section: validate, record the decision, commit the integrity baseline and blocks, promote the reviewed pending tools, flip storage quarantine. No promote-all after the unquarantine | Unquarantine paths that are not a review approval (`POST /servers/{id}/unquarantine`, the `upstream_servers` MCP op, config edits). They keep today's promote-pending behaviour |
| The Web review screen and CLI `mcpproxy review approve` send the binding. The macOS tray (PR #1572) already does | The Tools page bulk approve (`frontend/src/views/Tools.vue:1075-1100`), `mcpproxy security approve`, and the MCP `quarantine_security approve_tool/approve_all_tools` operations stay **unbound** (legacy). Follow-up O3 |
| A durable per-server **tool baseline marker**, consulted by `serverHasBaseline`. Trusted discovery and every review approval commit set it. Only removing the server clears it | How a never-baselined trusted server's **first** non-empty inventory is treated. It is still auto-baselined |
| A monotonic **capture sequence**, stamped where `tools/list` actually runs. Every store write of a pass (records, index, last-good snapshot, StateView) is gated on it under the per-server lock. Clients are retired when replaced | Discovery cadence, coalescing, retries and the Spec 105 connection-generation checks. These are kept; the sequence is an extra guard |
| Docs: REST, CLI review commands, the security-quarantine feature page, swagger | macOS tray code (`native/`), which PR #1572 owns. The tray copy for blind approval is a follow-up (O1) |

**Editions.** Both editions expose the same review and approval endpoints, and the behaviour is identical. There are no server-edition-only code changes.

**Compatibility.** Every wire change is additive. A request without `expected_hashes`, or with `null`, is an **unbound** legacy request and keeps working. The safety changes in FR-012 and FR-023 still apply, because they cover every server approval. Clients that predate `current_hash` keep working. PR #1572 sends an unbound request only when no reviewed tool has a hash, which happens only against an older core.

## Definitions

- **Review fingerprint**: an opaque string per tool approval record, reported as `current_hash` in the review payload. It changes whenever the approval-hashed definition changes (name, description, input schema, output schema). It also changes when any of `readOnlyHint`, `destructiveHint`, `idempotentHint` or `openWorldHint` changes. It is **not** the storage `CurrentHash`. Clients may compare it only with a later review.
- **Bound request**: an approve request carrying `expected_hashes`, a JSON object that may be empty. **Unbound request**: the field is absent or `null`.
- **Reachable set** of a server approval: the records the approval would make callable, minus the `block` list. That is every `pending` record (promoted) and every enabled `approved` record (activated by leaving quarantine).
- **Baseline marker**: a durable per-server record stating that the server's tool baseline has been decided. While it exists, a new or replaced tool is never auto-approved as a first baseline.
- **Capture**: one upstream `tools/list` result. The caller that leads the upstream call stamps it with a **capture sequence** (`seq`): process-global and strictly increasing. Coalesced waiters receive the leader's capture.
- **Applied capture** of a server: the highest `seq` whose inventory has been applied to that server's records and index. **Published capture**: the highest `seq` published to the StateView.

## User Scenarios & Testing

### User Story 1: An approval only approves what the operator reviewed (Priority: P1)

An operator opens the review of a quarantined server, decides which tools to allow, and approves. If any tool in the reachable set appeared or changed (definition or safety hints) since the review loaded, nothing is approved. The server stays quarantined, and the operator is told to reload the review.

**Why this priority**: this is the security gap with a live reproduction (G1-G3), and the tray already sends the contract.

**Independent Test**: PR-2 tests T2xx (runtime, httpapi, CLI, vitest) and live check L2 in quickstart.md.

**Acceptance Scenarios**:

1. **Given** a quarantined server whose review shows a read-only `read_note` with `current_hash` H1, **When** the upstream changes `read_note` to `destructiveHint: true`, a capture runs, and the operator approves with `expected_hashes: {"read_note": H1}`, **Then**:
   - the response is HTTP 409, with an error containing "out of date" that names `read_note`;
   - the server is still quarantined and the record is still pending;
   - no integrity baseline was written.
2. **Given** the same review, **When** a new tool `drop_db` is captured before the approval and the request carries only `{"read_note": H1}`, **Then** the response is 409 naming `drop_db` as "not in the review". With `block: ["drop_db"]` instead, the approval succeeds and `drop_db` is approved+disabled.
3. **Given** an empty captured review or a blind approval (nothing captured), and a client that sends `expected_hashes: {}`, **When** a tool is captured before the commit, **Then** the response is 409. **When** a tool is first captured after the commit, **Then** it is pending, through PR-1's marker (FR-023), which PR-2 requires.
4. **Given** a trusted server with pending tool `t` at H1, **When** `POST tools/approve {"tools":["t"],"expected_hashes":{"t":H1}}` runs after `t` changed, **Then** the response is 409 and `t` stays held. With the fresh fingerprint, `t` is approved.
5. **Given** a client that sends no `expected_hashes`, **When** it approves, **Then** today's behaviour applies, except that tools filed after the commit stay pending (FR-012).

### User Story 2: A server's baseline survives inventory replacement and restarts (Priority: P1)

A trusted server's first inventory was auto-baselined. The server later replaces its tools wholesale, or first returns an authoritative empty list. The new tools stay pending until reviewed, across repeated discovery and restarts.

**Why this priority**: the live reproduction auto-approved and dispatched an unreviewed destructive tool (G5). The fix is small and independent.

**Independent Test**: PR-1 tests T1xx and live check L1.

**Acceptance Scenarios**:

1. **Given** a trusted manual-trust server `notes` auto-baselined with `read_note`, **When** its inventory becomes `[wipe_all_notes]` and discovery runs three times with a restart in between, **Then**:
   - `wipe_all_notes` is pending every time;
   - it is absent from the shared and profile indexes;
   - `call_tool_destructive` is refused without an upstream dispatch.
2. **Given** the same server, **When** an authoritative empty refresh runs and the inventory `[wipe_all_notes]` appears afterwards, **Then** the result is the same.
3. **Given** a quarantined server approved with an empty review, **When** tools appear after the unquarantine, **Then** they are pending.
4. **Given** a store written by an older binary (approved records, no marker), **When** the new binary's first discovery pass runs, **Then** the marker is back-filled and the behaviour is unchanged.
5. **Given** a server removed and re-added under the same name, **When** its first inventory arrives on a trusted server, **Then** it is auto-baselined as a new server, because the marker was cleared together with the records.

### User Story 3: Late discovery results never undo newer state (Priority: P2)

Several discovery triggers run for the same server: connect, `tools/list_changed`, the sweep, refresh, the approval reindex and the quarantined capture. Whatever order they finish in:
- the stores end up reflecting the newest capture;
- an operator decision made after a capture is never reverted by that capture;
- a quarantined server never re-enters the index.

**Why this priority**: the races need specific interleavings and are harder to hit than US1/US2. The fix is also the largest. It composes with PR-1/PR-2 but does not depend on them.

**Independent Test**: PR-3 tests T3xx with paused passes (test hooks), and live check L3.

**Acceptance Scenarios**:

1. **Given** capture C1 (`t` read-only) is paused before apply and capture C2 (`t` destructive) has been applied, **When** C1 resumes, **Then** C1 writes nothing (records, index, last-good snapshot, StateView), and dispatch still sees `t` as destructive.
2. **Given** a pass that computed `t` as blocked, **When** the operator approves `t` and the reindex runs before the pass's index writes, **Then** `t` stays indexed. The older decision does not evict it.
3. **Given** an older capture listing `x` and a newer one without it, applied newer-first, **When** the older one resumes, **Then** it neither deletes nor resurrects `x`'s record.
4. **Given** a pass paused after its eligibility check, **When** the server is quarantined and purged and the pass resumes, **Then** the shared and profile indexes stay empty for the server, including previously approved tools.
5. **Given** two concurrent `ListTools` callers coalesced onto one upstream call, **Then** both receive the leader's capture sequence, and a capture started later always carries a larger sequence.
6. **Given** a server whose endpoint is replaced while a quarantined capture from the old client is in flight, **Then** the old client's result is discarded (the client is retired) and no record is written from it.

### Edge Cases

- `expected_hashes` names a tool that no longer has a record: the name is ignored for approval, since there is nothing to approve. If that tool is in `block`, a bound request answers 409 out of date instead of today's 400 "unknown tool block target".
- An `expected_hashes` value is an empty string: the tool counts as "not in the review".
- A tool in the reachable set has a record with no storage `CurrentHash` (a toggle-synthesized record): its fingerprint is still non-empty, derived from the stored definition, so a review that contains it stays bound.
- A dangerous scan verdict and a stale review at the same time: the scan gate answers first, with the unchanged message containing "dangerous". The force retry re-sends the original binding and then gets 409 out of date. The out-of-date message never contains "dangerous", because the tray keys its force dialog on that word.
- Duplicate names in `tools`: on main each occurrence is written and counted (`tool_quarantine.go:2064-2099`, `httpapi/server.go:6922-6924`). The bound path validates the set once and deduplicates; the unbound path keeps today's behaviour (design review r3 #5). T205 covers the bound dedupe.
- `approve_all` with `expected_hashes`: the reachable set is every `pending` and `changed` record.
- Capture sequence 0 (a caller with no managed client, such as tests or fixtures): treated as "unordered". It is applied only while the server has no applied capture yet, never after one.
- Re-applying the same capture (the approval reindex from the last-good snapshot) is allowed, because `seq == applied`.
- Clock changes do not matter: sequences are counters, never wall-clock.
- Downgrade to an older binary: the marker bucket is ignored and behaviour returns to inference from records. No data is lost.

## Requirements

### Functional Requirements: review fingerprint and binding (#1566, PR-2)

- **FR-001**: `GET /api/v1/servers/{id}/review` MUST report `current_hash` for every tool: the review fingerprint of its stored record (the definition the approval hash covers, plus the four hints). The field is additive.
- **FR-002**: The fingerprint MUST change when the description, input schema, output schema or any of the four hints changes. It MUST NOT change for `title`, approval status, `disabled`, timestamps or scan evidence.
- **FR-003**: The fingerprint MUST be non-empty for every record, including records with an empty storage `CurrentHash`.
- **FR-004**: The fingerprint format is versioned and opaque: `rv1-` followed by 64 hex characters. A fingerprint from another version never matches; the client then reloads, which is harmless.
- **FR-005**: `POST /api/v1/servers/{id}/security/approve` MUST accept `expected_hashes`, an object mapping tool name to fingerprint. When it is present, every record in the reachable set MUST appear in it with an equal fingerprint. Otherwise the request fails with HTTP 409 and nothing is written (no integrity baseline, no blocks, no promotion, no quarantine change). The server stays quarantined.
- **FR-006**: `POST /api/v1/servers/{id}/tools/approve` MUST accept `expected_hashes`:
  - with `tools`, every requested tool that has a record must match;
  - with `approve_all`, every `pending` and `changed` record must match.

  A mismatch returns 409 and nothing is written. Requested names without a record keep today's `not_approved` reporting.
- **FR-007**: Validation MUST run under the server's tool-approval lock, in the same critical section as the writes it guards.
- **FR-008**: The 409 response MUST carry the usual `request_id` and this shape:
  `{"success":false,"error":"tool review for server '<s>' is out of date (<details>); nothing was approved — fetch the review again", "error_code":"review_out_of_date", "stale_tools":{"changed":[...],"unreviewed":[...],"missing_blocks":[...]}}`.
  The error text MUST contain "out of date" and MUST NOT contain "dangerous".
- **FR-009**: Extra names in `expected_hashes` MUST be ignored. These are tools outside the reachable set, such as changed or already-blocked tools.
- **FR-010**: Unbound requests MUST keep working (compatibility), subject to FR-012.
- **FR-011**: A bound server approval MUST perform these steps under one hold of the server's tool-approval lock:
  1. validation;
  2. the baseline marker (from PR-1, which PR-2 requires);
  3. the integrity baseline + blocks commit;
  4. promotion of the validated pending records;
  5. the storage quarantine flip.

  The config save/reload, events and reindex follow outside the lock.
- **FR-012**: Every server approval, bound or unbound, MUST promote only the pending records present at its commit. The promote-all after unquarantine (`approveBaselineToolsForServer` in `QuarantineServer`) MUST NOT run for the security-approval path. Tools filed after the commit stay pending.
- **FR-013**: The Web review screen MUST send `expected_hashes` built from the review it shows at click time:
  - every reviewed tool that carries a fingerprint;
  - `{}` for an empty or not-captured review;
  - an unbound request only when no tool in the review carries a fingerprint.

  The force retry MUST re-send the binding of the attempt that triggered it. A per-tool approve sends that tool's fingerprint.
- **FR-014**: On a 409 out of date, the Web review screen MUST:
  - reload the review;
  - keep the operator's explicit choices that still apply;
  - show the core's message as a notice next to the reloaded controls, not in the error panel and not as the force dialog.
- **FR-015**: `mcpproxy review approve` MUST send the same binding:
  - quarantined path: all reviewed tools, or `{}` for an empty review;
  - trusted path: the selected tools, or every held tool for the default `approve_all`.

  The CLI MUST bind to what the operator actually reviewed (design review r2 #1):
  - **`--from-review <file>`** (new): the file is the JSON output of `mcpproxy review show <server> -o json`. It MUST be a structurally valid success envelope whose `data.server.name` equals the positional server; otherwise the CLI exits 1 **before sending any request** (design review r6 #2; the tray does the same check, `review.server.name == server`). The fingerprint also covers the server name, so a cross-server binding can never match. The selection and the binding come from that file, so a definition that changed after `review show` fails as out of date. This is the scripted and headless path.
  - **Interactive, without `--from-review`**: before the confirmation, the prompt lists every tool it will make reachable, with tier, status and the first line of the description, plus a diff marker for changed tools. The binding is to that displayed fetch, which is then the review the operator saw.
  - **`--yes` without `--from-review`**: binds to the review fetched at that moment. This is documented as "approve what the server serves now", and it is still protected against changes during the call.

  On 409 it MUST exit non-zero (exit code 1) and print the core's message plus `Run 'mcpproxy review show <server>' and approve again.`
- **FR-017**: A record already in `changed` state MUST track the live definition when a later capture differs from the stored current one, in its hash **or in any of the four safety hints** (design review r4 #1, r5 #1). The pass updates `CurrentHash`, description, schemas and annotations, and keeps `Previous*` and `ApprovedHash` as the last approved contract. Today the record stays frozen at the first changed definition (`tool_quarantine.go:950-1039`). A review would therefore show H1 while H2 is live, and a bound approval of H1 would pass. With FR-017 the fingerprint follows H2, so the H1 binding fails as out of date. This also fixes the unbound path, which today approves H1 as `ApprovedHash` until the next pass re-flags it.
- **FR-016**: `docs/api/rest-api.md`, `docs/cli/review-commands.md`, `docs/features/security-quarantine.md` and `oas/swagger.yaml` MUST describe `current_hash`, `expected_hashes`, the 409 and the unbound fallback.

### Functional Requirements: durable baseline (#1567, PR-1)

- **FR-020**: Storage MUST hold a per-server baseline marker in a new bucket `tool_baseline_decisions`. The key is the server name. The value is JSON `{decided_at, source, by}`, with `source` one of `discovery|server_approval|inferred`.
- **FR-021**: `serverHasBaseline` MUST be true when the marker exists OR any approved/changed record exists. Today's inference is kept, so older stores work without migration.
- **FR-022**: A discovery pass that leaves at least one approved or changed record for the server MUST set the marker. The source is `discovery`, or `inferred` when the pass only found pre-existing approved/changed records. The marker is set inside the pass's locked section, before any record of that pass can be deleted.
- **FR-023**: Every security (server) approval commit MUST set the marker (`source: server_approval`, `by: <approver>`) inside the commit's locked section, before the integrity baseline is written. This holds whether the request is bound or unbound and whether the server has tools or not.
- **FR-024**: The marker MUST be removed only together with the server's approval records, in the same bbolt transaction: `DeleteServerToolApprovals` (server removal) and `PruneToolApprovalsNotIn` (orphan prune).
- **FR-025**: A trusted server whose first inventory is empty MUST NOT get a marker from discovery. Its first non-empty inventory is still its baseline.
- **FR-027**: A failed marker write (or a failed marker read, which counts as "has baseline") MUST abort the destructive half of the apply (design review r4 #3). An aborted apply performs no absent-record delete, no tool-hash delete and no unrestricted indexing. It also leaves the last-good snapshot and capture stamp unchanged: they are restored if they were already published (design review r7 #3) and, with PR-3, no applied-sequence advance. The holds it computed stay in force: blocked tools are evicted, nothing new is indexed. Today an error from `checkToolApprovals` is swallowed into an empty `BlockedTools` result (`lifecycle.go:1019-1026`), and absent approvals are then deleted (`:1171-1183`). PR-1 MUST make that error path fail closed for marker errors, independently of PR-3.
- **FR-026**: The Web blind-approval dialog and the CLI blind prompt MUST state that tools the server lists later will wait for review.

### Functional Requirements: capture order (#1565, PR-3)

- **FR-030**: `managed.Client` MUST stamp each upstream `tools/list` with a capture `{seq, epoch, client}`. The leader takes it after it owns the list and before the upstream request. Coalesced waiters MUST receive the result and capture of the flight they joined, held in an immutable per-flight result, never a shared last-result slot. `ListTools` keeps its signature and delegates. Ping-only health checks produce no capture.
- **FR-031**: The sweep MUST obtain a capture per server (`DiscoverToolsReportCaptured`), not one ticket per sweep.
- **FR-032**: Applying an inventory MUST happen in one critical section under the server's tool-approval lock, and MUST write nothing when `seq < applied` for the server. A current capture sets `applied = seq`. Applying covers:
  - the `checkToolApprovals` decisions;
  - absent-record removal, record deletes and tool-hash deletes;
  - blocked-tool eviction;
  - added/modified/annotation index writes;
  - the last-good snapshot update.
- **FR-033**: The StateView publish of an inventory MUST run under the same lock, and only while `seq >= applied` and `seq >= published`. It then sets `published = seq`. The sweep MUST publish per server through this gate.
- **FR-033a**: A successful **empty** capture on the lenient path, which keeps the retained inventory (sweep `lifecycle.go:403-414`; reactive `:898-912`, through `markZeroToolServersDiscovered`, `:999-1009`), MUST still pass through the sequence- and client-validated section (design review r7 #2). It writes no records and no index entries, and keeps the retained inventory. It does advance `applied` (an observation fence), so an older in-flight non-empty capture is rejected afterwards. The discovery-completed marker is published only from inside that section.
- **FR-034**: Before any index write, the apply MUST re-check under the lock that the server is enabled and not quarantined, in config OR storage, failing closed. A quarantined server's inventory is recorded for review only. Every writer that makes a server **ineligible**, whatever its quarantine state, MUST go through this path. That covers quarantine (FR-035) and every disable writer: `EnableServer(false)` (`lifecycle.go:2086`, with its async purge at `:2107-2123`); `BulkEnableServers` (`:2288`); REST PATCH `enabled:false` (`server/server.go:2047-2050,2107-2109`); config reload (`lifecycle.go:1500-1502,1555-1557`); and the restart persist (`:2347-2355`) (design review r7 #1). Each MUST store the change and purge the shared index entries in one critical section under that server's approval lock, so the check cannot be passed by a stale view (design review r6 #1). The operation is `persistServerIneligibleLocked`, which generalises `persistServerQuarantinedLocked`. The profile refresh follows after unlock.
- **FR-035**: Every writer that can store a server record as quarantined MUST do so through one runtime operation, `persistServerQuarantinedLocked`. That operation saves storage and purges the shared index entries in one critical section under the server's approval lock. The writers are (design review r2 #3, r3 #1):
  - `QuarantineServer(true)`;
  - the config-load path (`LoadConfiguredServers`, `lifecycle.go:1539-1557`);
  - REST `PATCH /servers/{id}` with `quarantined: true` (`httpapi/server.go:2848-2853,2982` → `Server.UpdateServer`, `server/server.go:2055-2057,2107-2121`; the later reload no longer sees a transition, so it never purges);
  - the disk-first restart persist (`lifecycle.go:2347-2355`).

  As defence in depth, an apply that finds the server quarantined under the lock also purges the server's shared index entries before returning. An index left populated by a writer this list missed then heals on the next pass.

  Today the config path purges first and then calls `SaveUpstreamServer` (`newlyQuarantined`). Under this requirement it saves the quarantined storage record **before** purging, inside that section, so an apply that takes the lock afterwards sees the quarantine. The profile-index refresh may follow outside the section.
- **FR-036**: The upstream manager MUST retire a client it removes or replaces, under its capture writer lock. An inventory captured by a retired client, or under an epoch that is no longer current, MUST be discarded without writes.
- **FR-037**: `lastGoodTools` MUST store the complete capture identity (seq, epoch, client) with the snapshot and MUST NOT be replaced by an older capture. Re-applying the snapshot (approval reindex, sweep fallback) is allowed only while that capture is still current (client not replaced or retired, epoch unchanged). Otherwise the approval reindex re-lists the server and the sweep fallback skips it.
- **FR-038**: The profile-index refresh, signature-cache reconcile, SSE events and activity records MUST run after the lock is released.

### Key Entities

- **ReviewFingerprint**: derived, not stored. See data-model.md.
- **StaleToolReviewError**: lists of changed, unreviewed and missing-block tools, in the leaf package `internal/toolreview`. Maps to HTTP 409.
- **ToolBaselineDecision**: stored in a new bucket. Server → `{decided_at, source, by}`.
- **InventoryCapture**: in memory. `{seq uint64, epoch int64, client *managed.Client}`.
- **ToolApprovalServerLock**: in memory. The existing per-server mutex plus the `applied` and `published` sequences.

## Success Criteria

- **SC-001**: Both live reproductions in this spec fail closed on the fixed binary. In L1 the replaced destructive tool stays pending and is not dispatched. In L2 the stale approval returns 409 and the server stays quarantined.
- **SC-002**: Every acceptance scenario has a failing-first test that passes after its PR.
- **SC-003**: PR #1572 (tray) works unchanged against PR-2:
  - its bound requests are validated;
  - its 409 parsing ("out of date") triggers a reload;
  - a core with this spec always reports fingerprints, so the tray never falls back to an unbound request against it.
- **SC-004**: The existing quarantine suites show no regression under `-race`: `internal/runtime/tool_quarantine*_test.go`, `internal/httpapi/*quarantine*`, `internal/security/scanner`, and the UX-02 concurrency suite (`tool_quarantine_concurrency_test.go`).
- **SC-005**: Under PR-3, a discovery pass for a 320-tool trusted server takes no more than 10% longer than on main, because index writes now run under the per-server lock. Measured with the existing `big` harness profile.
- **SC-006**: No new dependencies. Both golangci-lint passes are clean.

## Assumptions

- **A1**: The wire field is named `current_hash` because PR #1572 already decodes that name. It carries the review fingerprint, not the storage hash. The `GET /servers/{id}/tools/{tool}/diff` response keeps its storage `current_hash` unchanged. The two payloads use the same name for different values, and the docs say so.
- **A2**: The fingerprint covers only the four hints, not `title`, because only the hints drive `reviewTier` (`review.go:330-336`) and the dispatch tier.
- **A3**: The reachable set excludes `changed` records for server approval. Server approval never promotes them (`pendingOnly`, `tool_quarantine.go:2083-2085`), so they stay held regardless.
- **A4**: A bound request may block a tool whose definition changed since the review, because a block is never less safe than any reviewed state. Only a vanished block target is out of date.
- **A5**: The security-approval path stops calling promote-all after unquarantine for unbound requests too (FR-012). With PR-1, older tray builds and scripts that approve blind therefore get later tools held for review. This is the issue's "unbound legacy requests stay accepted but keep additions held".
- **A6**: A blind approval records the baseline (FR-023), so a never-captured server's first inventory after approval is held, not auto-trusted. Rationale: the operator approved nothing they could see. See O1.
- **A7**: Unquarantine paths outside the review approval keep today's promote-pending semantics and do not set the marker. The trust model for those paths is unchanged, and the marker is still inferred from the approved records they create.
- **A8**: The capture sequence is process-global (one atomic counter), so it orders captures across client replacements. It is never persisted. After a restart every server starts at `applied = 0`.
- **A9**: Holding the per-server approval lock across index writes is acceptable. The lock is per server, the writes are bounded, and `captureSnapMu → approval lock` is already the order `commitDiscoveredTools` uses.
- **A10**: The MCP `quarantine_security` approval operations stay unbound. They are agent surfaces without a review screen, and adding arguments would change the frozen tool-surface goldens. Follow-up O3.

## Open Questions (for the lead; the design proceeds with the stated default)

- **O1**: Blind approvals hold later tools (A6). The alternative is that a blind approval keeps auto-trust of the first inventory. Default: hold. It is safer and matches the tray's empty-snapshot binding. Updating the tray copy needs a follow-up on `native/`.
- **O2**: Should the 409 body list the old and new fingerprints of each changed tool? Default: names only, since clients reload anyway.
- **O3**: Bind the Tools page bulk approve and the MCP approval operations in a follow-up. This needs the fingerprint in `GET /servers/{id}/tools` and a golden update.
- **O4**: PR order. PR-1 (#1567) must merge before PR-2 (#1566) is cut, because PR-2's post-commit guarantee needs the marker (design review r1 #3). PR-3 (#1565) is independent and can go at any point; the default is last, because it is the largest.

## Out of Scope

- The rug-pull hash and its annotation policy.
- Tool blocks.
- Unquarantine paths outside the review approval.
- Prompt approvals (Spec 100).
- macOS tray code.
- Web review-session hygiene (#1568).
- The MCP approval operations (O3).

## Commit Message Conventions

Commits use `Related #1565`, `Related #1566` and `Related #1567`. `Closes #...` appears only in the body of the PR that completes the issue, with one keyword per issue. Commits carry no co-author trailers and no AI attribution.

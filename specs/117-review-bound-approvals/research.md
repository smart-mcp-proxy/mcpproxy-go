# Research: Review-Bound Approvals, Durable Baselines and Ordered Inventories (Spec 117)

All code references are to `origin/main` at `ee7834e71`. "Branch X" means `origin/fix/qa070-ux02-approval-serialization`: the rejected oversized UX-02 branch, which implemented binding and capture order ad hoc in commits `92615fb74`..`51eb8a94f`. It is used here as research only. Its reviews are in `docs/qa/0.70.0-ux-2026-10-10/remediation/reviews/ux02-r*.txt`.

## Existing code inventory

| Area | Where | What it gives us | Gap |
|---|---|---|---|
| Per-server approval lock (#1571) | `internal/runtime/tool_approval_lock.go` (`lockToolApprovals`, `WithToolApprovalLock`, `serverQuarantinedInStorage`, `deleteToolApprovalLocked`); `runtime.go:229-234` (`toolApprovalLocks sync.Map`) | Serialized read-modify-write of approval records. Fail-closed storage quarantine read | Covers records only, not index or StateView writes (G8). No ordering of whole passes (G7) |
| Discovery decisions | `tool_quarantine.go:439-1340` `checkToolApprovals` | The whole pass runs under the lock (`:446`). `serverHasBaseline` snapshot (`:481-491`). New-tool branch (`:640-755`). Approved branch refreshes `CurrentAnnotations` (`:844-847`). Pending promotion (`:880-881`) | Baseline inferred from records (G5/G6). No capture order |
| Approval hash | `tool_quarantine.go:25-61` `calculateToolApprovalHashWithOutputSchema` | name + description + normalized input schema (+ output schema) | Annotations deliberately excluded (G3). The policy is kept (issue: "keep the rug-pull hash policy unchanged") |
| Operator approval | `tool_quarantine.go:2035-2112` `ApproveTools` → `approveToolsAndNotify` → `approveToolsLocked`. `ApproveAllTools` `:2519-2545` | Re-reads each record under the lock, enforces invariants, promotes | `ApprovedHash = CurrentHash` at execution time. No expected value (G1) |
| Server-approval promotion | `tool_quarantine.go:2469-2516` `approveBaselineToolsForServerCount` (pendingOnly), called from `QuarantineServer(false)` `lifecycle.go:2227-2233` | Never promotes `changed` | Runs after the unquarantine, so it promotes tools filed after the review (G2) |
| Quarantine flip | `lifecycle.go:2175-2258` `QuarantineServer` | Storage flip under the lock (`:2186-2189`). Purge, explicit decision, `SaveConfiguration`, `LoadConfiguredServers`, events, `HandleUpstreamServerChange` | Purge outside the lock (G9). Promote-all on unquarantine (G2) |
| Scan-gate approval | `internal/security/scanner/service.go:1708-1838` `ApproveServer(WithBlocks)` → `approveServer`: dangerous gate (`:1756-1767`), `SaveIntegrityBaseline(WithBlocks)` under `WithToolApprovalLock` (`:1790-1800`), `RecordToolBlocksForSecurityApproval` (`:1802-1811`), `unquarantiner.UnquarantineServer` (`:1818-1828`). Interfaces `ServerUnquarantiner`, `ToolBlockRecorder`, `ToolApprovalLocker` (`:126-142`) | Atomic baseline+blocks commit | Commit and unquarantine are separate steps (#1566 "commit-to-unquarantine window") |
| Storage blocks | `internal/storage/scanner.go:430-470` `SaveIntegrityBaselineWithBlocks` | Writes approved+disabled records in the baseline tx. A missing record → `ErrToolApprovalBlockNotFound` → HTTP 400 | — |
| Adapter | `internal/server/server.go:4413-4445` `serverUnquarantinerAdapter` | Bridges the scanner to `Server.UnquarantineServer` → `QuarantineServer(false)` (`:1963`) | — |
| REST approve | `internal/httpapi/server.go:6851-6930` `handleApproveTools`; `security_scanner.go:335-397` `handleSecurityApprove` + `securityApproveRequest`. `tool_approval_outcome.go` (UX-02 counts) | Outcome counts (`approved_count`, `still_pending`, …) | No binding field |
| Review payload | `internal/runtime/review.go:84-128` (`ServerReview`, `ReviewTool`); composer `:230-300` reads records under `captureSnapMu.RLock`; `DefinitionsCaptured = len(records) > 0` (`:271`) | Stored records with annotations, tier, default selection | No per-tool hash |
| CLI | `cmd/mcpproxy/review_cmd.go:41-98` (approve), `:240-290` `reviewServerState` | Quarantined path → `security/approve` with `block`. Trusted path → `tools/approve` with `approve_all` or `tools` | No binding |
| Web | `frontend/src/components/ReviewScreen.vue:213-269` (`load` with `loadGeneration`/`visitGeneration`, `approve`, `approveTool`), `frontend/src/services/api.ts:556-565` `approveTools`, `:1603-1607` `securityApprove` | Generation-guarded loads. The force retry re-sends `lastBlock` | No binding |
| Tray (PR #1572) | `native/macos/.../APIClient.swift` `securityApproveServer(…expectedHashes:)`, `approveSpecificTools(…expectedHashes:)`; `Models.swift` `ReviewTool.currentHash` (`current_hash`); `ReviewQueueView.swift` `expectedHashes`, `approvalDecision`, `staleReviewMessage` (409 + "out of date") | The client half of the contract, ready | Inert until the core implements it |
| Discovery passes | `lifecycle.go:377-511` sweep (`DiscoverToolsReport`, `commitDiscoveredTools` per server, `reapplyLastGoodSnapshot` fallback, batch `RefreshToolsFromDiscovery`); `:798-975` single server; `:567-692` quarantined capture (`WithCurrentClientOnEpoch` + supervisor generation); `:776-796` `commitDiscoveredTools` (holds `captureSnapMu`, publishes `lastGoodTools`, then `applyDifferentialToolUpdate`); `:1018-1308` `applyDifferentialToolUpdate` | Global `captureSnapMu` already serializes whole applies. Spec 105 generation checks drop StateView publishes from a superseded **connection** | No order between two captures on the **same** connection (G7). Index writes outside the approval lock (G8). No quarantine re-check inside the apply (G9) |
| Approval reindex | `tool_quarantine.go:1973-2032` `reindexServerToolsAfterApprovalChange` → `reindexFromLastGoodSnapshot` (re-applies the stored snapshot) | No network round trip | The snapshot may be older than one already applied if commits ran out of order |
| ListTools coalescing | `internal/upstream/managed/client.go:966-1100` (`acquireListToolsContext`, `ListTools`, `runListToolsAsLeader`, `publishListToolsResult`) | One leader per in-flight list. Waiters share the result. `listEpoch` captured by the leader (`:1083`) | No capture identity returned to callers |
| Client replacement | `internal/upstream/manager.go:440-480` `AddServerConfig` (deletes the map entry under `mu`+`captureMu` when the endpoint config changed, disconnects outside); `:1228-1238` `WithCurrentClientOnEpoch` | Pointer + epoch validation for the quarantined capture | Not used by the normal discovery paths. No retire flag (#1565 last bullet) |
| Server removal | `internal/storage/bbolt.go:662-720` `DeleteServerToolApprovals`, `PruneToolApprovalsNotIn`; callers `internal/server/server.go:2182`, `lifecycle.go:1455-1465` | Records cleared with the server | Where the marker must also be cleared |

## Decisions

### D1. The fingerprint is a separate value; the rug-pull hash is unchanged

**Decision** (revised in Sol r2): `reviewFingerprint(record) = "rv2-" + hex(sha256(lp("rv2") + lp(server) + lp(name) + lp(CurrentHash) + lp(description) + lp(norm(schema)) + lp(norm(outputSchema)) + lp(hints)))`, where `lp(s)` is an 8-byte big-endian length followed by `s`.
- The first version (`rv1`) hashed `CurrentHash` (or a `nohash:` digest when empty) in place of the fields. The storage hash joins the fields with an unescaped `|` and skips an absent output schema, so two different contracts could share a fingerprint (Sol r2 P1).
- `hints` is `ro=<-|0|1>;de=…;id=…;ow=…`.

**Rationale**: the issue says to keep the rug-pull hash policy. Annotations are excluded from it on purpose, because some servers flap them across reconnects (`tool_quarantine.go:27-31`). The fingerprint is only compared within one review → approve round trip, so flapping costs at most one extra reload.

**Alternatives**:
- Reuse `CurrentHash`: misses G3.
- Branch X's `CurrentHash` + `~hint` suffix: works, but is not versioned and leaks the storage hash shape.
- Include `title`: not safety-relevant (A2).

### D2. Wire name `current_hash` on the review tool

**Decision**: report the fingerprint as `current_hash`, because PR #1572 decodes exactly that key (`Models.swift`, `CodingKeys … currentHash = "current_hash"`).

**Alternatives**: `review_hash`, which is clearer but would need a tray change, and the tray is out of scope. The storage-hash `current_hash` in the diff endpoint (`server.go:7156`) is a different payload. Docs note the collision (A1).

### D3. Binding contract shape (matches PR #1572)

| Request | Bound when | Validated set | Mismatch |
|---|---|---|---|
| `security/approve {force, block, expected_hashes}` | key present and not `null` (Go: `map[string]string` non-nil, so `{}` is bound) | the reachable set: pending + enabled approved, minus `block` | 409, nothing written |
| `tools/approve {tools, expected_hashes}` | same | the requested tools that have a record | 409 |
| `tools/approve {approve_all, expected_hashes}` | same | every pending + changed record | 409 |

The tray's expectations, checked against `pr1572.diff`:
- (a) It sends hashes for **all** review tools, blocked ones included. Extra names are ignored (FR-009).
- (b) A hashless tool in a hashed review is left out of the binding, "the core then refuses it as out of date unless that tool is blocked". This matches the unreviewed rule.
- (c) A blind approval with nothing captured sends `[:]`. A captured but empty review sends `[:]`.
- (d) 409 detection: status 409 and the message contains "out of date". The force dialog is keyed on "dangerous" (FR-008).
- (e) A per-tool approve sends `{tool: hash}` and, for a hashless tool in a hashed review, `{}`. Both behave as bound.

**Rationale**: these are the semantics Sol reviewed across rounds r1-r10 on Branch X. Rounds r8 A/B ended in PASS on them, and the remaining findings (r9 frontend session hygiene, r10 the commit-to-unquarantine window) are handled by D5 and #1568.

### D4. Validation lives in the runtime, under the per-server lock

**Decision**:
- New `Runtime.ApproveToolsReviewed(server, names, expected, by)` and `Runtime.ApproveAllToolsReviewed(server, expected, by)`. A nil `expected` means unbound; these delegate to today's code.
- The check `staleReviewedRecords(server, candidates, expected) *StaleToolReviewError` runs on records read inside the locked section.
- `StaleToolReviewError` lives in a new leaf package `internal/toolreview` (stdlib imports only). `internal/storage` already imports `internal/security/scanner` (`storage/manager.go:16`, `storage/scanner.go:7`), so the scanner can import neither storage nor the runtime. With a leaf package, scanner, storage, runtime and httpapi can all name the type without a cycle (design review r1 #1). httpapi maps it with `errors.As` to 409 `review_out_of_date`.

**Alternatives**: validating in the HTTP handler before calling the runtime, rejected because of the TOCTOU between the check and the write. Validating in storage inside the bbolt tx, rejected because fingerprint computation is runtime policy, and the lock already gives atomicity with respect to every writer.

### D5. Server approval: one critical section, storage flip included

**Decision**: add a runtime operation `CommitServerApproval(server, blocks, expected, by, commit func() error) (promoted int, err error)`. Under one hold of the server's lock it runs:
1. validate (bound only);
2. mark the baseline (PR-1);
3. `commit()`: the scanner's `SaveIntegrityBaseline(WithBlocks)`;
4. promote the pending records not in `blocks`. When bound, these are exactly the validated ones;
5. `storageManager.QuarantineUpstreamServer(server, false)`.

After unlock, `FinishServerApproval(server)` runs the rest of `QuarantineServer(false)`: explicit decision, `SaveConfiguration`, `LoadConfiguredServers`, events, activity, `HandleUpstreamServerChange`. It skips `approveBaselineToolsForServer`.

The scanner gets a new optional interface, `ReviewedServerApprover`, with these two methods. `approveServer` uses it when present and otherwise falls back to today's path, for test fakes.

**Rationale**:
- Once the storage flip is in the same section as validation, there is no commit-to-unquarantine window to revalidate (Sol r10 #1). Branch X instead revalidated at the later flip (`51eb8a94f`), which left a partially applied approval (baseline saved, tools promoted, server still quarantined) on mismatch.
- With the flip inside the section, a capture after unlock sees storage = unquarantined. It follows the normal trusted-server rules: new tools are held because the marker exists, and an annotation change on an approved tool is post-activation policy (out of scope).
- `SaveConfiguration`/`LoadConfiguredServers` can start discovery, which takes the same lock, so they must stay outside (lock discipline in `tool_approval_lock.go:25-38`).

**Failure handling**: if `SaveConfiguration` fails after the flip, the storage flip stays and the error is returned. That is today's `QuarantineServer` behaviour (`lifecycle.go:2186-2209`).

### D6. Unbound server approvals also stop promoting after unquarantine (A5)

**Decision**: FR-012 applies to both forms. For an unbound request, the promoted set is "pending records present at commit", so legacy clients keep "approve = trust the current snapshot". Additions after the commit are held. Non-review unquarantine paths keep `approveBaselineToolsForServer`.

**Rationale**: the issue's suggested direction ("unbound legacy requests stay accepted but keep additions held") and G2.

### D7. Durable marker: separate bucket, OR-ed with today's inference

**Decision**: add bucket `tool_baseline_decisions` with JSON values (data-model.md).
- `serverHasBaseline = marker || anyApprovedOrChanged`.
- Discovery sets the marker inside `checkToolApprovals`, under the lock, when the pass ends with at least one approved/changed record for the server (FR-022). The marker is written before the absent-record deletes that follow in `applyDifferentialToolUpdate`. When prior approved/changed records exist it is written (`inferred`) before the per-tool loop, ahead of every record write of the pass; otherwise (`discovery`) after the loop (FR-027a, spec review r3 #1).
- Every server approval sets it in `CommitServerApproval` (FR-023). PR-1 introduces that runtime method in a minimal form: under the lock, mark and then run the scanner's commit closure. The scanner calls it through the optional `ServerApprovalCommitter` capability of its unquarantiner adapter, for both the plain `SaveIntegrityBaseline` branch and the `…WithBlocks` branch. The scanner never touches storage for the marker (import cycle, design review r1 #1). PR-2 grows the same method into the full D5 section.
- Removal happens in the same tx as `DeleteServerToolApprovals` / `PruneToolApprovalsNotIn`.

**Why not a field on each record**: the issue's failure is exactly that the records disappear.

**Why not on the server config**: config is user-editable, synced to the file, and reloaded. A baseline fact must not be editable by a config round trip.

**Why OR with inference**: there is no migration step, and downgrades are safe. Branch X (`e4b2f5a8a`, `bbd2e6131`) used a bucket too. Its r6 finding (discovery never wrote it) is FR-022.

### D8. Empty first inventory does not create a discovery marker (FR-025)

**Decision**: a trusted server that lists zero tools first (lazy servers, a server still starting) keeps auto-baselining its first non-empty inventory.

**Rationale**: marking on empty would hold every tool of many legitimate servers, a UX regression the issues do not ask for. The authoritative-empty-after-baseline case is covered, because the earlier non-empty pass already set the marker.

### D9. Capture sequence at the real capture point

**Decision**:
- `managed.Client.ListToolsCaptured(ctx) ([]*config.ToolMetadata, InventoryCapture, error)`. `runListToolsAsLeader` takes `seq = nextCaptureSeq()` (package-level `atomic.Uint64`) right after acquiring the list, next to `listEpoch` (`client.go:1083`).
- It stores `{seq, epoch, client}` with the published result, and waiters return the leader's capture.
- Each upstream list gets an **immutable per-flight result** (`listFlight{done chan struct{}; tools; err; capture}`). The leader creates it in `acquireListToolsContext`, and each waiter keeps a pointer to the flight it joined. Today a waiter reads the client-global `listToolsLastResult` after the channel closes (`client.go:1036-1063`), and a new leader can reset that slot first (`:979-982`), so the waiter gets nil or the next flight's data. That race exists on main too, and adding a capture to the same slot would inherit it (design review r2 #4).
- The tool-count refresh, which shares the list, publishes into its flight. The health check is ping-only and does not list (`client.go:1772-1786`), so it produces no capture (design review r2 #6).
- `ListTools` returns only the tools.
- The manager adds `DiscoverToolsReportCaptured` → `map[server]InventoryCapture`.

**Rationale**: #1565 states that tickets taken before `ListTools` do not order the actual captures. Branch X learned this the hard way (`af51ef111` → `e51b06184`).

**Alternative**: a per-server sequence. Rejected: a process-global counter orders captures across client replacement without extra state (A8).

### D10. One locked apply phase; StateView publish gated

**Decision**:
- Split `applyDifferentialToolUpdate` into a locked phase and an unlocked tail.
- The locked phase (`applyInventoryLocked(server, tools, capture)`), under the per-server lock:
  1. stale check (`capture.seq < applied` → return `stale`);
  2. client check (not retired, epoch current; skipped for seq 0 fixtures);
  3. quarantine re-check (config OR storage);
  4. `checkToolApprovalsLocked`: today's body, minus taking the lock;
  5. index diff and writes, absent-record and hash deletes over the FR-032a workset (index-derived removals plus absent approved+enabled records), blocked eviction;
  6. stage the `lastGoodTools` snapshot (newer-or-equal only), its capture stamp, `definitions` (non-empty captures) and `published` in locals; nothing is written to the shared fields yet;
  7. StateView publish (`seq >= published`). The publication is accepted only when the supervisor returns no error **and** reports it published (`published == true`; for the sweep, the server is not in the returned `stale` list);
  8. only after an accepted publication: commit the staged snapshot, stamp, `definitions` and `published`, and clear review-dirty when the capture is complete.

  Any error or rejection in steps 5-7 is an incomplete capture (FR-018): `applied` stays advanced to the capture's seq, dirty is set or moved up to it, the staged values of step 6 are discarded, and step 8 does not run, so the snapshot, stamp, `definitions` and `published` remain at the prior successful capture (spec review r4 #1, r5 #2). Steps 6-8 run under `captureSnapMu` and the approval lock, so no reader sees a staged value. Step 8 is in-memory and cannot fail.
- The unlocked tail: `reindexAffectedProfiles`, `reconcileSignatureCache`, `warmSignatureCache`, events.
- The sweep publishes each server through the gate instead of one batch `RefreshToolsFromDiscovery`. Lenient empty results also enter the gated section as an observation: they advance `applied` without writing, and the discovery marker is published there (FR-033a, design review r7 #2).

**Lock order**: `captureSnapMu` (global; already held by `commitDiscoveredTools`, `reindexFromLastGoodSnapshot` and `reapplyLastGoodSnapshot`) → per-server approval lock → manager `captureMu` (read) → client `epochMu` → storage manager lock / index lock / supervisor lock.

**Nested lock-taking helpers.** The current apply calls `deleteToolApprovalLocked` (`lifecycle.go:1179`), which takes the same non-reentrant mutex (`tool_approval_lock.go:86-88`). Inside the widened locked phase, every such helper is replaced by its lock-free form (design review r2 #2).

**One existing path runs in the opposite order and must be inverted** (design review r1 #2). The quarantined capture calls `WithCurrentClientOnEpoch` first (`lifecycle.go:649-663`). That holds manager `captureMu` (R) and client `epochMu` across its callback (`manager.go:1228-1237`, `client.go:741-749`), and the callback then takes `captureSnapMu` and the approval lock (`lifecycle.go:730-739`, `tool_quarantine.go:446`). PR-3 removes that outer wrapper. Every inventory path, the quarantined capture included, goes through `applyInventory`, which takes `captureSnapMu` → approval lock. Only then, still inside the locked section, does it take `captureMu` (R) + `epochMu` through a new `Manager.WithCaptureCurrent(capture, fn)`. That call checks pointer, epoch and retirement and holds both locks across the record/index writes, so the protection the wrapper gave is kept for the whole write.

Audit of the other `captureMu`/`epochMu` holders on main: `AddServerConfig`/`RemoveServer` (`manager.go:444,656`) and the connect/disconnect transitions (`client.go:591,686,1152,1337,1466`). None of them calls into the runtime while holding the lock. T305 pins this with a deadlock test (normal apply vs quarantined capture vs endpoint replacement, run concurrently) and with hook-based assertions that those sections never reach `checkToolApprovals`.

Other paths already follow the order: `commitDiscoveredTools`, `reindexFromLastGoodSnapshot` and `reapplyLastGoodSnapshot` take `captureSnapMu` before the approval lock, and the operator approvals take only the approval lock. Supervisor publish only updates its snapshot and StateView (`supervisor.go:1214-1230`); events go out through `supervisorEventForwarder` (`lifecycle.go:2596`). That already holds on main, so T305 records it as a regression check, not a failing-first test.

**Rationale**: G8 and G9 need the index writes inside the lock. G7 needs the order check in the same section. Branch X's `applyInventory` + `publishInventoryIfCurrent` (`edc13dd47`) is the reviewed precedent: r8 PASS on that part.

### D11. Client retirement

**Decision**:
- Add `managed.Client.Retire()`: sets `retired` under `epochMu`.
- `WithConnectionEpoch` and a new `CaptureIsCurrent(capture)` report false for retired clients.
- `Manager.AddServerConfig` (endpoint change branch) and `RemoveServer` call `Retire()` on the old client while holding the `captureMu` writer lock but **after releasing `manager.mu`** (design review r8 #1). Today `AddServerConfig` holds both (`manager.go:444-445,469-482`), and `Supervisor.DiscoveryGeneration` → `GetServerState` → `Manager.GetClient` takes `manager.mu` (R) (`supervisor.go:1157-1160`, `actor_pool.go:165-166`, `manager.go:1214-1218`). If retirement ran under `manager.mu`, it would order `manager.mu → epochMu` against the callback's `epochMu → manager.mu`.
- **No manager or supervisor lookups inside the epoch-held callback.** The supervisor-generation check that the quarantined capture performs inside its callback today (`lifecycle.go:649-661`) moves to just before `WithCaptureCurrent`, still under the approval lock. Inside the callback, pointer + epoch + `retired` are checked from the client alone; a reconnect after the generation check moves the epoch and is caught there. `captureMu` (W) still excludes every `WithCaptureCurrent` reader during replacement, so the protection is unchanged.

**Rationale**: the #1565 last bullet. Pointer checks exist only in the quarantined capture today. Retirement makes "this capture's client is still the server's client" a single cheap check for every path.

### D12. PR split

| PR | Issue | Contents | Depends on |
|---|---|---|---|
| PR-1 `fix: durable tool baseline` | #1567 | storage bucket + API; `serverHasBaseline` OR; discovery marks; scanner approval marks under the lock; removal; blind-copy text in Web/CLI | none |
| PR-2 `fix: bind approvals to the reviewed definitions` | #1566 | fingerprint + review field; `expected_hashes` on both endpoints; `StaleToolReviewError` → 409; `CommitServerApproval`/`FinishServerApproval`; no post-unquarantine promote-all on the security path; Web + CLI binding; docs + swagger | **PR-1 merged** (extends PR-1's `CommitServerApproval`; the empty-review guarantee needs the marker) |
| PR-3 `fix: apply tool inventories in capture order` | #1565 | capture sequence; `DiscoverToolsReportCaptured`; locked apply phase; gated publish; quarantine purge under the lock; retirement | none; composes with #1571 |

Each PR is a separate, self-contained change with its own tests, and each keeps unbound clients working. PR-1 and PR-3 depend on nothing. **PR-2 is cut after PR-1 merges** (design review r1 #3). Without the durable marker, an empty bound approval leaves no approved or changed record, and the first later capture is still a baseline pass (`tool_quarantine.go:481-491`, `:664-665`). Removing the post-unquarantine promote-all alone does not hold those tools. Making the dependency explicit is simpler and safer than duplicating a marker in PR-2.

**PR-1/PR-2 coupling**: PR-1 introduces `CommitServerApproval` (lock, mark, commit). PR-2 extends it with validation, promotion and the storage flip.

## Lessons taken from Branch X (and not repeated)

1. Ad hoc growth: binding, ordering, baseline and Web session fixes interleaved over 15 commits. That is why the branch was rejected as oversized. Here each issue is one PR with declared scope.
2. Tickets taken before `ListTools` were wrong (`af51ef111`, fixed by `e51b06184`). D9 starts from the leader-taken capture.
3. A discovery-established baseline was missed at first (r6-A #1). FR-022 covers it from the start.
4. Revalidation at the unquarantine was bolted on (r10 #1). D5 removes the window instead.
5. Hashless records unbound the whole approval (fixed in `edc13dd47`). FR-003 gives every record a fingerprint.
6. Blind/empty CLI and Web approvals stayed unbound (r7-c2 #1). FR-013/FR-015 specify `{}`.

## Open questions resolved by assumption

See spec.md A1-A10 and O1-O4.

## Design review round 1 (OpenCode gpt-6.1-sol) resolutions

1. **High, import cycle** (`storage` imports `security/scanner`): the shared types move to the leaf package `internal/toolreview`. The scanner reaches the marker only through the runtime adapter's `ServerApprovalCommitter` (D4, D7).
2. **High, lock-order inversion with the quarantined capture**: the outer `WithCurrentClientOnEpoch` wrapper is removed, and client validity is checked inside the locked section through `WithCaptureCurrent`. Audit of the other lock holders is in D10. A failing-first deadlock test was added (T305a).
3. **High, PR-2 alone misses the empty-review guarantee**: PR-2 now explicitly requires PR-1 (D12, O4).
4. **Medium, snapshot re-application cannot validate the client**: `lastGoodTools` keeps the full capture. A stale snapshot is re-listed or skipped, never re-applied (FR-037, data-model §7, T306(6b)).
5. **Medium, T104 would not fail on main through `checkToolApprovals` alone**: it now drives the full apply path with the index, plus restart and dispatch refusal.
6. **Medium, some PR-3 tests are already green on main**: T305(b)/(c) and T306(6) are reclassified as regression checks. The `CommitServerApproval` check is conditional on PR-2 (composition, not dependency).

## Design review round 2 (OpenCode gpt-6.1-sol) resolutions

1. **High, the CLI binds to a hidden fetch**: added `--from-review <file>` (binds to a saved `review show -o json`), an interactive prompt that lists what becomes reachable, and documented `--yes` semantics (FR-015, T215).
2. **High, nested `deleteToolApprovalLocked` would self-deadlock in the widened section**: lock-free helpers inside the locked phase plus an audit (T307) and a timeout test (T306(8)).
3. **High, config-driven quarantine bypasses the lock**: FR-035 covers the `LoadConfiguredServers` newly-quarantined branch (storage save, then purge, under the lock). Test T306(9).
4. **Medium, coalesced waiters read a shared last-result slot**: an immutable per-flight result (research D9, T302) and a failing-first interleaving test (T301).
5. **Medium, failing-first labels**: T305(a) and the T207 empty-review check are reclassified as composition regressions. T208's failing pair is the post-commit promotion window.
6. **Low, the health check does not list**: removed from capture producers.

## Design review round 3 (OpenCode gpt-6.1-sol) resolutions

1. **High, more quarantine writers** (PATCH `quarantined:true` via `UpdateServer`; the restart persist): every quarantined-record writer goes through `persistServerQuarantinedLocked`. A quarantined apply self-heals the index. A CI guard script pins the writer list (FR-035, T309, T306(9b)).
2. **Medium, T104 cannot import `internal/server`**: the dispatch refusal moves to T104s in `internal/server`.
3. **Medium, T306(2) would deadlock as written**: the approval runs asynchronously and is asserted blocked, then released. A separate main-compatible window test was added (2m).
4. **Low, stale "in storage" scope line**: fixed to `internal/toolreview`.
5. **Low, duplicate names are not deduplicated on main**: documented. The bound path deduplicates and is tested.

## Design review round 4 (OpenCode gpt-6.1-sol) resolutions

1. **High, a `changed` record is frozen at its first changed definition**: a bound approval of the stale H1 would pass while H2 is live. FR-017 makes changed records track the live definition (keeping the approved contract in `Previous*`/`ApprovedHash`). T205b fails first.
2. **Medium, T306(2m) would block on `captureSnapMu`**: rewritten to complete only the approval (which takes the approval lock only), hold the reindex, and assert the decision/index inconsistency.
3. **Medium, a marker-write failure falls into today's fail-open error path**: FR-027 aborts the destructive half of the apply on marker errors, in PR-1. T104f is a fault-injection test on an older store.

## Design review round 5 (OpenCode gpt-6.1-sol) resolutions

1. **High, a hint-only change to a changed record stays stale**: FR-017 now refreshes on a hash **or** hint difference, i.e. whenever the review fingerprint would change. A hint-only test variant was added to T205b.
2. **Medium, T306(2m) required a synchronous approval inside the widened lock**: the approval now runs asynchronously, with different expected orderings on main (it completes during the pause; stale eviction) and after T307 (it waits; final invariant holds).

## Design review round 6 (OpenCode gpt-6.1-sol) resolutions

1. **Medium, the disable writers bypass the apply lock**: FR-034 now requires every ineligibility writer (quarantine and disable, including bulk) to store and purge under the server's approval lock (`persistServerIneligibleLocked`). Test T306(9c). The bulk path takes per-server locks in sorted order, so two bulk calls cannot deadlock.
2. **Medium, a saved review is not tied to its server**: `--from-review` must match `data.server.name` before any request is sent, and the fingerprint now covers the server name. CLI test with identical definitions on A and B.

## Design review round 7 (OpenCode gpt-6.1-sol) resolutions

1. **Medium, more disable writers** (PATCH, config reload, restart persist): FR-034 enumerates every disable writer regardless of quarantine. The CI guard also covers `EnableUpstreamServer`. T306(9c) covers each writer.
2. **Medium, lenient empty captures bypass the fence**: FR-033a. An empty lenient result is an observation that advances `applied` without writing, and the discovery marker is published inside the gated section. Test T306(5e).
3. **Medium, a marker failure still leaves the snapshot published**: `commitDiscoveredTools` restores `lastGoodTools` and `lastCaptureAt` on `errBaselineMarker`. T104f asserts both. PR-1's declared scope was updated.

## Design review round 8 (OpenCode gpt-6.1-sol) resolutions

1. **High, a manager.mu/epochMu inversion through retirement and the generation check**: retirement now runs after `manager.mu` is released, still under `captureMu` (W). The epoch-held callback must not look up the manager or supervisor; the generation check moves before `WithCaptureCurrent`. A deadlock test was added (T303).
2. **High, a failed record write leaves the old binding valid**: FR-018 makes such a capture uncommitted (nothing published) and marks the server review-dirty. Bound approvals return 409 and unbound ones 503 until a full capture succeeds. The restart limitation is stated. Fault-injection test T205c.
3. **Medium, the observation fence blocks operator reindex**: a `definitions` sequence lets the retained snapshot be reconciled without moving the fences. Test T306(5r).

## Design review round 9 (OpenCode gpt-6.1-sol) resolutions

1. **High, a failed capture did not fence older captures or replays**: an incomplete capture now advances `applied` as an observation fence. Dirty records F, and only a fully recorded capture with seq > F clears it. Replays never clear it and are replaced by a re-list while the server is dirty. Because this needs the capture sequence, FR-018 moved to PR-3. The PR-2 interim residual risk is recorded as O5 (pre-existing on main).
2. **High, legacy approvals bypassed the dirty guard**: the guard is in the common locked approval path, covering `approveToolsLocked`, `ApproveAllTools`, baseline promotion and `CommitServerApproval`, with `ErrReviewNotRecorded` → 503. T306d(d) tests the legacy methods directly.
3. **Medium, the supervisor republishes retained tools for disabled servers**: PR-3 scope now includes the supervisor change, and T306(9c) runs a reconciliation after the purge.
4. **Low, T306(5r) is not failing-first on main**: reclassified as a composition regression.

## Design review round 10 (OpenCode gpt-6.1-sol, final round under the 10-round cap) resolutions

1. **High, enabling an approved+disabled tool bypassed the dirty guard**: the FR-018 guard now covers every reachability transition: single/bulk enable and toggle-synthesized approvals. Disable and block stay allowed. T306d(d2) checks for zero dispatch.
2. **Medium, PR-3's changed → changed-again fault test depends on PR-2's FR-017**: reclassified as a composition test that runs once PR-2 is merged. PR-3's standalone failing-first suite uses only branches that write records on main.

Round 10 reached the review-round cap. These two fixes were applied after the last review and **have not been re-reviewed** (no round 11, per the cap rule). The first implementation PR's review covers them.

## Spec review round 1 (OpenCode gpt-6.1-sol, new review series after the design-review cap) resolutions

1. **High, a failed capture left already approved+enabled tools callable with stale dispatch metadata**: genuine. FR-018 withheld the failed capture's StateView, so dispatch kept classifying by the last recorded hints while the upstream served the unrecorded definition, and the dirty guard covered only transitions that make a tool reachable, not tools that already were. FR-018 now refuses dispatch of every tool of a dirty server in the shared gate (`evaluateExactToolGate`), so the variants, the direct surface and code execution all inherit it; listing is unchanged. T306d(f) asserts zero upstream calls on all three surfaces until a complete capture.
2. **Medium, a lenient-empty capture could clear review-dirty**: genuine. FR-018 now defines a complete capture (non-empty and fully recorded, or authoritative empty with removal completed); a lenient-empty observation advances `applied` but never clears `reviewDirtyAt`. T306d(g) covers both the refusal and the two clearing sub-cases.

## Spec review round 2 (OpenCode gpt-6.1-sol) resolutions

1. **Medium, the dirty refusal in the direct evaluator would hide listed tools**: genuine. `directCallabilityEvaluator.evaluate(...).callable` drives the scoped direct listing, the call-time `WithToolFilter` re-run and `describe_tool` visibility (`mcp_direct_callability.go:54-150`), so reporting dirty through it would hide the tools and turn a call into unknown-tool before the handler answered. FR-018 now keeps the verdict in a separate field on both the gate (orthogonal to `class`; only the dispatch-only `callable()` reads it) and the direct decision (never folded into `callable`), and only the dispatch blocks refuse on it. T306d(f2) covers a read-scoped agent on `/mcp/all`: listed, describable, the call answers the dirty text with zero upstream calls. T309d adds a guard test against listing paths reading the verdict.
2. **Low, PR-3's declared scope omitted required surfaces**: genuine. The scope statement now lists the supervisor, `internal/toolreview`, the `internal/server` dispatch, refusal, explainer and `Server.UpdateServer` changes, the httpapi 503 mapping and the CI guard script, matching T301-T313 and the plan.md inventory. Its only wire change is the 503 error response. plan.md's PR-3 inventory now names the actual direct evaluator file (it said `internal/preflight/*`) and the refusal builders.

## Spec review round 3 (OpenCode gpt-6.1-sol) resolutions

1. **High, a marker abort left the pass's earlier record writes unfenced**: genuine. `checkToolApprovals` writes records inside its loop (an approved record's `CurrentAnnotations` update, `tool_quarantine.go:844-855`), and T105 placed the marker write after the loop, so a marker failure could follow a persisted hint update while FR-027 withheld the StateView, snapshot and stamp. The record and the dispatch metadata then described different captures, and with PR-3 no fence stopped an older capture from overwriting the newer record. FR-027a resolves it by ordering rather than by rollback: when prior approved/changed records exist (the only case where the pass can update a dispatchable definition), the `inferred` marker is written before the loop, so a marker read/write error, or a failed prior-record listing, aborts before the first record write. When no prior approved/changed record exists, the `discovery` marker stays after the loop and its failure is not an abort, because the pass's own approved records establish the baseline by inference (FR-021) and the next pass marks before it can delete them. With PR-3 the abort is an FR-018 incomplete capture (fence at F, review-dirty at F, whole-server dispatch refusal until a complete capture > F). Alternative considered: stage every record write of the pass and commit them with the marker in one bbolt transaction. Rejected for PR-1: it rewrites the ~900-line loop and its helpers (`recordScanHold`, `scanApproveChange`, the Spec 105 sibling consults) behind an overlay, which ordering makes unnecessary. Tests: T104f (hint update + marker fault, listing fault, non-aborting discovery fault) and the composition case T306d(h). The PR-1-only interim exposure (an aborted capture is withheld, older captures unfenced until PR-3) is recorded in O5.

## Spec review round 4 (OpenCode gpt-6.1-sol) resolutions

1. **High, review-dirty could clear before dispatch metadata was published**: genuine. FR-018 defined a complete capture by record persistence alone, but the apply indexes after the records and before the StateView publish, and `BatchIndexTools` can fail there (`lifecycle.go:1209-1248` returns the error). C1 publishes approved read-only t; C2 records nothing for t's new destructive hints and marks dirty; C3 records the hints, fails indexing and would clear dirty while the StateView still holds C1's read-only hints, and since hints are outside the approval hash t stays approved and callable at the read tier. FR-018 now treats any failure between record persistence and publication as an incomplete capture (fence, dirty set or moved up, nothing published) and clears dirty only in the publish step after it succeeded. D10 step 7 and data-model §lock state say the same. Test T306d(i) (C1/C2/C3 with a `BatchIndexTools` fault, plus a clean-server variant), using an index seam added in T309d. Alternative considered: roll back the record writes on an index fault. Rejected: it needs the transactional overlay already rejected in round 3, and fail-closed dispatch plus the next capture's rewrite gives the same safety.
2. **Medium, the output-schema backfill kept stale safety hints**: genuine. The approved-record backfill (`tool_quarantine.go:572-601`) saves new hashes and schemas and `continue`s without touching `CurrentAnnotations`, so a review after a migrating capture shows the stored hints, not the captured ones, and a binding to that stale fingerprint would match. New FR-017a: every recording path refreshes the hints in the same save, the backfill included, with the rug-pull hash policy unchanged. An audit of every save site in `checkToolApprovals` found the backfill to be the only gap. Test T205c, implementation in T206.

## Spec review round 5 (OpenCode gpt-6.1-sol) resolutions

1. **Medium, a partial removal could leave an absent tool's approval record forever**: genuine, verified against the code. `applyDifferentialToolUpdate` derives `removedTools` only from the index diff (`lifecycle.go:1099-1103`), deletes the index document first (`:1154`) and the record afterwards (`:1179`, error logged at debug). If the record delete fails after the index delete succeeded, no later pass sees the name again, so the obsolete approval stays and is reused if the name reappears. New FR-032a: the removal workset is the index-derived set plus every absent `approved && !Disabled` record (the only indexable state), with the legacy-alias and pending/changed/disabled retention rules unchanged, idempotent deletes, a delete failure as an FR-018 tail failure, and a storage-derived workset that also survives restarts. Alternative considered: an in-memory retained removal workset under the approval lock. Rejected: it is lost on restart, which would re-create the permanent leftover after a crash between the two deletes. Test T306d(j), implementation in T309d.
2. **Medium, snapshot-before-publish had no recovery when publication failed**: genuine. D10 updated `lastGoodTools` in step 6 and published in step 7 but claimed an error skips both; a step-7 failure cannot un-run step 6, and `RefreshServerToolsFromDiscovery` reports a dropped result as `published == false` with a nil error (`supervisor.go:1214-1229`), which the spec did not count as a failure. D10 now stages the snapshot, stamp, `definitions` and `published` and commits them only after an accepted publication (step 8); a rejection is a failure. FR-018 and FR-033 say the same, and data-model §7 records the staging. Alternative considered: restore the previous values on failure (the PR-1 `errBaselineMarker` pattern). Rejected for PR-3 because staging has no window in which another reader can see the incomplete capture, and needs no per-field restore list. Test T306d(k).

## PR-1 implementation notes (fix/117-pr1-durable-baseline, on top of Spec 116 PR1 and the TPA RT-01..07 fixes)

Main moved after this design (Spec 116 D15: the discovery pass publishes its last-good snapshot, capture stamp and StateView entry inside the approval-lock hold, through `held`, right after its final eligibility check). PR-1 adapts FR-027/FR-027a to that shape:

1. **The baseline step runs before `held`.** `applyDifferentialToolUpdateHeld` calls `resolveToolBaselineHeld` (prior-record listing, marker read, inferred back-fill) under the lock and before `held`, and passes the result to `checkToolApprovalsResolved`. A failure returns `errBaselineMarker` before anything of the capture is published or written, so T105's "restore the snapshot and stamp" step is not needed: nothing was staged. `commitDiscoveredToolsAt` reports such a pass as eligible and not published, so the sweep re-lists it (the per-server sweep publish of Spec 116 replaces the batch `RefreshToolsFromDiscovery` the design referred to).
2. **No eviction on abort.** The abort precedes every decision, so the stored records and the index still describe the last applied capture and its holds; there is no new blocked set to evict.
3. **A marker read error aborts** the pass (fail closed), as T105 states; data-model §5's "read error counts as has baseline" is subsumed (an aborted pass cannot auto-baseline).
4. **Discovery marker `by`** is the `ApprovedBy` of the first approved record the pass left (`auto-baseline`, or `auto` / `scan-approved` / `auto-approve-changes` under a lifted gate).
5. **`CommitServerApproval` (PR-1 form)** rejects a non-nil `expected` (PR-2), writes the marker first and fails the approval before anything is committed if it cannot. A commit failure after the marker leaves it in place (only holds later tools: the safe direction).
6. **Fault injection** uses the runtime test hook `testHooks.toolBaselineFault(server, op)` with op `list|read|write`, because `Runtime.storageManager` is the concrete `*storage.Manager`.
7. The scanner keeps the legacy `ToolApprovalLocker` path only for an unquarantiner without the new capability (test fakes); both commit branches use `ServerApprovalCommitter` in production.

## PR-2 implementation notes (fix/117-pr2-review-binding, on top of PR-1 34737710d, Spec 116 PR1 and the TPA RT-01..07 fixes)

1. **The server approval is one Spec 116 config commit.** `Runtime.CommitServerApproval` runs `Commit("security_approve")` with `SetQuarantined(server, false)` and an `UnderApprovalLock` hook (the integration point Spec 116 left for this spec). The hook validates the binding, writes the baseline marker, runs the scanner's integrity baseline (+ blocks) commit and promotes the pending records present at that moment; the commit pipeline then writes the config file and the storage row (quarantined=false) and releases the lock. So validation, marker, baseline, blocks, promotion and the storage flip share one hold of the server's approval lock, and the config file and the live config are published by the same commit (D5 asked only for the storage flip in the section; Spec 116 makes the whole flip one step). Lock order is Spec 116's: configCommitMu (L0) → approval lock (L1) → storage; nothing in the hook takes L0 or captureSnapMu.
2. **`FinishServerApproval`** is what remains after the commit: the quarantine-change activity record and `HandleUpstreamServerChange` (reindex/connect). There is no `SaveConfiguration`/`LoadConfiguredServers` step any more (the commit did both), and no promotion (FR-012). The plan's `finishQuarantineChange(server, quarantined, promote)` split of `QuarantineServer` is therefore not needed: `QuarantineServer` is unchanged and keeps its promote-pending for the non-review unquarantine paths (A7).
3. **Failure after the hook.** A config-file or storage failure after the hook aborts the commit with the hook's writes kept (marker, integrity baseline, blocks, promoted records) while the server stays quarantined. These are fail-safe: a quarantined server exposes no tool, and a retry validates the same fingerprints (status is not part of them). The `tool_approved` activity events of the promotion are emitted inside the hook (as `approveToolsLocked` always did), so in that failure case they precede the failed flip.
4. **FR-017 and FR-017a were already met on main** by the TPA red-team fixes (#1596): RT-03 folds the effective safety hints into the rug-pull hash, so an approved tool whose hints flip turns `changed`, and `refreshHeldContractSnapshot` (Sol r3 P2) keeps a `changed` record's current fields on the live definition (hash or hints). The output-schema backfill sets `CurrentAnnotations` from the capture and only runs when the stored hints match; a hint flip routes it to the rug-pull path. T205b and T205c are therefore regression tests (they pass on main's runtime once the fingerprint exists), not failing-first. Their spec wording ("still approved" after the backfill) predates RT-03; the test asserts the property FR-017a protects: the review shows the captured hints and a binding to the old hints is refused.
5. **The fingerprint uses the raw hints** (unset / false / true), per data-model §1, while the rug-pull hash compares effective hints. A hint spelled out at its default therefore moves the fingerprint of an approved record without changing its status (tested: `TestCommitServerApproval_ApprovedEnabledHintMoveIsChanged`).
6. **Web force retry.** The force retry re-sends the block list and the binding of the attempt that answered "dangerous". A review reload while the force dialog is open still drops the retry (the #1568 session hygiene already on main), so "re-sends the original binding even after a reload" (T213) became "re-sends it when no reload intervened; after a reload a fresh decision is required".
7. **Unbound call shape.** The Web review screen calls `securityApprove(server, force, block)` / `approveTools(server, [name])` without the binding argument when the review carries no fingerprint, so an older core sees byte-identical requests.
8. **CLI `--from-review`** accepts the review data object that `mcpproxy review show <server> -o json` prints (the CLI drops the REST envelope) and also the REST success envelope; an error envelope, a malformed file, a file without `server`/`tools`, or another server's review exits 1 before any request. The trusted-server path binds `--tools` to the named tools and the default `approve_all` to every pending/changed tool; an empty review binds to `{}`. The interactive prompt lists the reachable tools on stdout (stderr under `-o json/yaml`). `review approve` no longer prints cobra's usage text for a refusal from the core.
9. **A bound request on a core path that cannot check it fails closed**: the scanner refuses a non-nil binding when its unquarantiner lacks `ReviewedServerApprover`, the adapter refuses it without a runtime, and the security handler answers 503 when the controller lacks `ApproveServerReviewed`.

## PR-3 implementation notes (fix/117-pr3-capture-ordering, on top of PR-2 942afe66f, Spec 116 PR1/PR2 and the TPA RT-01..07 fixes)

Main moved a long way after this design. Spec 116 D15 already runs a discovery pass's final eligibility check (stored row enabled, not quarantined, at the instance's commit generation), its approval decisions, absent-record and tool-hash deletes, blocked eviction, index writes, signature warm-up, last-good snapshot, capture stamp and StateView publish in **one hold of the server's approval lock**, and every writer that makes a server ineligible (quarantine, disable, bulk enable, REST PATCH, config reload, restart persist) is a `Runtime.Commit` that stores the row and purges the server's index entries under that same lock (Spec 116 FR-008). G8 and G9 were therefore closed before PR-3; what remained of #1565 was capture **order** (G7), the sweep's single ticket, the retained snapshot's identity and client retirement. PR-3 adapts the design to that shape:

1. **Capture identity (FR-030).** `listToolsCall` (the per-flight result introduced on main by the Sol r6/r7 fixes) now also carries `capture managed.InventoryCapture{Seq, Epoch, Client}`, stamped in `acquireListToolsContext` (the leader owns the list, before the upstream request) from a process-global `atomic.Uint64`. Waiters return the capture of the flight they joined. Per client, flights are serialized (one leader at a time), so flight start order is capture order; across clients (replacement) the global counter plus the currency check below order them. `ListTools`/`ListToolsFresh` keep their signatures; `ListToolsCaptured`/`ListToolsFreshCaptured` return the capture. The tool-count refresh shares `acquireListToolsContext`, so it captures too; the health check stays ping-only.
2. **Retirement (FR-036).** `Client.Retire()` is an atomic flag, set by `AddServerConfigAt` (endpoint change) and `RemoveServer` while they hold `captureMu` for writing. `CaptureIsCurrent(c)` = same client, not retired, connection epoch unchanged; it reads two atomics and takes no lock. `WithConnectionEpoch` also reports a retired client as not current. **Deviation:** there is no `Manager.WithCaptureCurrent` holding `captureMu`(R)+`epochMu` across the writes. The point check under the approval lock is enough for ordering: a reconnect or replacement after the check produces a newer capture, which has to take the same approval lock and is applied after this one. Holding `epochMu` across index writes would block Connect/Disconnect for the length of a 320-tool apply. The quarantined capture keeps main's outer `WithCurrentClientOnEpoch` wrapper; PR-3 adds no lock edge in either direction (nothing under the approval lock takes `captureMu` or `epochMu`), so the inversion the design worried about does not arise.
3. **Ordered admission (FR-032, FR-033, FR-033a, FR-037).** `toolApprovalLocks` values are now `*toolApprovalServerLock{mu, applied, definitions}`. `admitInventoryHeld` runs in every inventory write path, under the approval lock, right after the D15 verdict and before any write: the sweep and single-server discovery (`commitInventoryAt` → `applyInventoryGuarded`), the quarantined/ineligible review capture (`persistToolDefinitionsLocked`), the lenient empty observation (`markServerZeroToolsDiscoveredWith`) and the snapshot reconciliations (`reindexFromLastGoodSnapshot`, `reapplyLastGoodSnapshot`). Rules: not current → `errInventoryNotCurrent` (caller re-lists, like `errDiscoveryStaleGeneration`); `seq < applied` → `errInventoryStale` (terminal, no re-list; the newer capture stands); seq 0 only while `applied == 0`; admitted → `applied = seq`, and `definitions = seq` for a capture that carries definitions (non-empty, authoritative empty, review capture). A lenient empty result advances `applied` only. A reconciliation advances nothing and is admitted only while the snapshot's capture is current and is the `definitions` capture; otherwise the approval reindex re-lists (`RefreshServerTools`) and the sweep fallback skips.
   - **Deviation: no separate `published`.** The StateView publish already runs inside the admitted hold (Spec 116 D15), so `applied` gates it; a second counter would always equal it.
   - **`applied` and `definitions` advance at admission, not at success.** A newer capture whose apply then fails (marker abort, index error) still fences older captures, and the retained snapshot is no longer re-applied over its partially written records (the reconciliation re-lists instead). This is the fence half of FR-018.
   - `lastGoodTools` keeps its type; a parallel `lastGoodCapture` map (same mutex) stores the snapshot's full capture, is replaced only by a newer-or-equal capture (`storeLastGoodLocked`) and is purged with the snapshot (`commitMemoryPurgeHeld`, the sweep's removed-server prune).
4. **Sweep (FR-031).** `Manager.DiscoverToolsReportCaptured` returns one capture per listed server, from that server's own list; the sweep passes it to `commitInventoryAt` and to the zero-tool marker. A stale result reports published (no re-list); a not-current one is re-listed with the other stale sweep servers.
5. **Lock order** is unchanged: `captureSnapMu` → approval lock → storage / index / supervisor state (Spec 116 adds `configCommitMu` above the approval lock). The new code takes no lock of its own.
6. **Not in PR-3 (follow-up, failure-mode hardening independent of ordering):** FR-018's review-dirty flag with its dispatch refusal, `ErrReviewNotRecorded`/503 mapping and enable/approve guards; FR-032a's record-derived removal workset; the supervisor change that drops retained tools of disabled/quarantined servers (FR-034 last sentence). The writer routing of FR-034/FR-035 (`persistServerIneligibleLocked`, `scripts/check-quarantine-writers.sh`) is superseded by Spec 116's single commit pipeline. T305(a)'s "intermediate T307 with the wrapper kept" run is moot for the same reason (item 2).
7. **Live reproduction.** #1565 reproduces on main without test hooks: the periodic sweep lists every server before it applies any result, so a sweep that lists a fast server and then a slow one holds the fast server's capture for the slow list's duration. A refresh in that window records the newer definition (a read-only tool turned destructive is held as `changed`), and the sweep then applies its older read-only capture: main reverts the hold to approved, re-indexes the tool and republishes read-only hints, and a read-scoped `call_tool_read` dispatches the now-destructive tool. With PR-3 the sweep's capture is dropped ("Tool inventory dropped: a newer capture was already applied") and the call is refused at the destructive tier. Evidence: `docs/qa/0.70.0-ux-2026-10-10/remediation/evidence/117pr3/`.

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

**Decision**: `reviewFingerprint(record) = "rv1-" + hex(sha256("rv1\x00" + name + "\x00" + base + "\x00" + hints))`.
- `base` is `record.CurrentHash`. When `CurrentHash` is empty, `base` is `"nohash:" + sha256(description \x00 schema \x00 outputSchema)`.
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
- Discovery sets the marker inside `checkToolApprovals`, under the lock, when the pass ends with at least one approved/changed record for the server (FR-022). The marker is written before the absent-record deletes that follow in `applyDifferentialToolUpdate`.
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
  5. index diff and writes, absent-record and hash deletes, blocked eviction;
  6. `lastGoodTools` update (newer-or-equal only);
  7. StateView publish (`seq >= published`).
- The unlocked tail: `reindexAffectedProfiles`, `reconcileSignatureCache`, `warmSignatureCache`, events.
- The sweep publishes each server through the gate instead of one batch `RefreshToolsFromDiscovery`. `markZeroToolServersDiscovered` keeps its own path.

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
- `Manager.AddServerConfig` (endpoint change branch) and `RemoveServer` call `Retire()` on the old client inside the `captureMu` writer section, before the map entry is replaced.

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

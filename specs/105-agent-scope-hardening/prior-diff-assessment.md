## Spec 105 abandoned diff — hunk-by-hunk triage

**Base check.** The diff is against `6b005e949`; HEAD (`b39800a89`) differs from that base in only two touched files (`internal/runtime/lifecycle.go` SaveConfiguration hunk, `internal/server/server.go` `"enabled"` field) — neither overlaps a diff hunk, so every hunk applies to HEAD textually. "Compiles" below is conceptual against HEAD; I did not apply the patch.

**Headline.** Four hunks are sound and worth porting (cache provenance/durable invalidation, `RawName` + index identity, runtime approval identity, `retrieve_tools` scope-before-limit, profile-URL predicate). Three are actively broken or regress HEAD (`mcp_routing.go` does not compile; `mcp_direct_catalog.go` + `mcp_direct_scope.go` together delete **every built-in** from the direct surface for **every** caller; `tool_gate.go` opens an implicit-approval hole and orphans dead code). The codex plan/tasks are a thin restatement of the spec (T002–T017 unchecked) and carry no design not already in `spec.md`; `research.md` has one useful decision list, nothing more.

---

### 1. Cache provenance version — `internal/cache/{authorization,manager,models}.go`, `authorization_test.go`, new `provenance_hardening_test.go`

| | |
|---|---|
| **FR** | FR-001 (caller-kind-first superset, recursive provenance), FR-002 (legacy/internal entries refused + durably invalidated), Edge case "Durable invalidation", SC-004/SC-005 |
| **Compiles** | Yes. `Version int` field, `valid()`, `errInvalidProvenance`, `ReadCacheResponse.Producer *Authorization \`json:"-"\`` all self-contained. `StoreAs` stamps `Version=1`; `Store` (registry at `runtime.go:2226`, guesser) leaves it 0 → refused via `GetRecordsAs` but still readable via unguarded `Get` (registry/guesser use `Get`) — exactly research.md's intent. |
| **Correct** | Mostly. (a) `getGuarded` rewrite fixes the real bug the spec names: HEAD's expiry branch does `bucket.Delete` then `return fmt.Errorf(...)` inside `db.Update`, which **rolls the delete back**. The diff commits, returns `refusal` out-of-band — right shape. (b) Admin-first shortcut in `CouldHaveProduced` matches FR-001 text literally ("an administrator qualifies for any snapshot"), so the four flipped table rows are spec-mandated — but note this **loosens** HEAD's Spec-104 (#1226, Sep 8) profile-first rule for profile-scoped admins and SC-005 doesn't enumerate it; flag in PR. The "deny-all admin reader → true" row is the least defensible; keeping `len(reader.ProfileServers)==0 → false` for admins too would be safer and still satisfy every fixture. (c) Legacy test rewrite is fine (entry deleted after each refusal, so re-`Store` works). |
| **Verdict** | **Port with changes**: keep the deny-all guard ahead of the admin shortcut; add a `Version` doc comment; new test file needs goimports ordering. |

### 2. `read_cache` handler — `internal/server/mcp.go` @5681/5704, `mcp_read_cache_authz_test.go`, `mcp_call_tool_direct_test.go`

| | |
|---|---|
| **FR** | FR-001 non-disclosing refusal + recursive provenance; contracts/scope.md "cache refusal never explains" |
| **Compiles** | Yes (`response.Producer` non-nil on success because `GetRecordsAs` now refuses nil/invalid). Test import block is unsorted (`internal/cache` before `testing`) — gofmt/gci will complain. |
| **Correct** | (a) Collapsing **every** error to `"cache key not found"` is over-broad: a BBolt failure now masquerades as not-found. Collapse only `ErrUnauthorizedRead`, not-found and expired; keep `"Failed to retrieve cached data"` for real errors. (b) Child page stamped with `*response.Producer` (parent) instead of HEAD's `reader`: this satisfies "monotone" (child never broader than parent) and passes the FR-001 admin-{a}/pinned-agent fixture; the spec's "narrower of the two" is not implemented, but parent-stamp is a sound simplification since the gate already proved reader ⊇ parent. Document it. (c) `Store` → `StoreAs(anonymous)` in the direct test is required by FR-002; other `cacheManager.Store` test callers in `internal/cache/*_test.go` use `Get`, not `GetRecordsAs`, so unaffected. |
| **Verdict** | **Port with changes** (narrow the error collapse; fix import order). |

### 3. Tool identity — new `internal/config/tool_identity.go`, `config.go` `RawName`, `internal/upstream/core/client.go`, `internal/index/bleve.go`

| | |
|---|---|
| **FR** | FR-009 identity preservation end-to-end ("`erase` and `ns:erase` become distinct index entries" — SC-005 admin exception) |
| **Compiles** | Yes. `bleve.go` still imports `strings` for `CanonicalToolName`. |
| **Correct** | The root defect is real and confirmed at HEAD: `IndexTool` does `SplitN(Name, ":", 2)` on a bare upstream name, so `ns:erase` on server `srv` gets `docID = "srv:erase"` and **collides with a real `erase`**. `RawToolName` (RawName if set, else `TrimPrefix(Name, ServerName+":")`) is correct for both bare-from-upstream and canonical-from-index metadata. Two gaps: (a) on index reads the diff sets `RawName` from the stored `tool_name` field, which for **pre-existing index docs is the collapsed name** (`"erase"`) — derive it from the canonical `Name` instead (`TrimPrefix(canonical, server+":")`), which is right for old and new docs. (b) Old collapsed docIDs stay in the on-disk index until a full rebuild; `DeleteTool(srv, "ns:erase")` will not remove the stale `srv:erase` doc. Needs a rebuild trigger (the storage schema-version pattern at `tool_quarantine.go:437` is the existing precedent). |
| **Verdict** | **Port with changes** (RawName derivation on read; index rebuild/migration). |

### 4. Index scoped search — `internal/index/manager.go` `SearchScoped` + `mcp.go` @1717

| | |
|---|---|
| **FR** | FR-005 "scope filtering before result limiting"; Edge case "Ranking under scope" (selected corpus preserved, filter, then top-K) |
| **Compiles** | Yes. `searchIndex` is `*index.Manager`; `p.serverInScope` exists (`mcp_visibility.go:161`); hoisting `authCtx` above the search is fine. Uses `m.bleveIndex.GetDocumentCount()` directly, so no re-entrant RLock. |
| **Correct** | Semantically exactly the spec's "exhaustive search of the selected corpus filtered to authorized hits before taking K". Only server-scope is applied pre-limit; callability still post-limit — matches the spec's wording. Cost: fetches every hit with fields for every scoped call (FR-011 p95 risk on big fleets). A bleve boolean query with a `server_name` disjunction over `authCtx.AllowedServers ∩ profileScope.AllowedServerNames()` would be cheaper, but the closure-based version is acceptable as a first cut with the 527-tool benchmark. |
| **Verdict** | **Port verbatim**, note the perf follow-up. |

### 5. Scoped counts / usage / risk — `mcp.go` @2012–2062, `mcp_annotations.go`, `mcp_visibility.go` `scopedIndexedToolCount`

| | |
|---|---|
| **FR** | FR-005 (indexed counts over authorized population, usage ranking over authorized approved tools before top-N, session risk over authorized servers) |
| **Compiles** | Yes. `snapshot.Servers` is `map[string]*ServerStatus` so `for name, server := range` works; `GetToolStats(0)` = unlimited (`topN > 0` guard at `storage/manager.go:439`); `visible, _ := p.indexedToolVisible(...)` inside the `if` shadows the outer `visible` slice — compiles but is a `govet shadow` smell; rename. |
| **Correct** | Risk/usage scoping is right. But `scopedIndexedToolCount` counts via `indexedToolVisible` (scope **and** callability), so for an unscoped admin `debug.total_indexed_tools` and `usage_summary` drop disabled/removed tools — a silent SC-005 admin-parity break. Use scope-only (`serverInScope`) for the count, and for admins keep `getIndexedToolCount()`. `scopedIndexedToolCount` also walks the whole index per debug call (fine, debug only). |
| **Verdict** | **Port with changes** (rename shadowed var; scope-only count; admin parity). |

### 6. Runtime approval identity — `internal/runtime/lifecycle.go`, `tool_quarantine.go`

| | |
|---|---|
| **FR** | FR-009 producer side ("approval-record creation, baseline capture, index updates ... record the same canonical server / raw name pair") |
| **Compiles** | Almost: all four `extractToolName` call sites are replaced but the function is left in place → `unused` lint failure in CI (`.github/.golangci.yml`). Three `_test.go` files still call it, so either keep it (tests) or delete it and fix the tests. Unrelated HEAD drift in `SaveConfiguration` (#1272) does not conflict. |
| **Correct** | Yes — this is precisely the "producers made exact-name (Spec 105 FR-009 follow-up)" that HEAD's `tool_gate.go` comment is waiting for. `collectPeerToolMetadata` stamping `RawName` is right. The `oldToolsMap`/`newToolsMap` keys become raw names, and `DeleteTool(serverName, toolName)` at lifecycle.go:767/800 now matches the new docID scheme. |
| **Verdict** | **Port verbatim** + resolve `extractToolName` (keep for tests or delete). Must ship together with hunk 3. |

### 7. Reader side of approvals — `internal/server/tool_gate.go`

| | |
|---|---|
| **FR** | FR-009 "legacy collapsed records approve only the exact raw name they store; the namespaced tool remains pending until approved by its own name; 'no record found' is never classified as callable for a tool the discovery snapshot contains" |
| **Compiles** | No, not cleanly: `mergeApprovalRecords` and `approvalLockRank` become unused (lint fail), and `TestToolGate_LegacyCollapsedApprovalRecord_StillGates` (`mcp_call_tool_target_tier_test.go:474`, six sub-tests) is not updated and will fail. |
| **Correct** | **No — it regresses HEAD.** `evaluateExactToolGate` treats `ErrToolApprovalNotFound` as *implicit-approved*. HEAD's merge reader at least surfaces a collapsed `pending` record for `ns:erase`; the diff's exact-only reader returns not-found → `ns:erase` becomes **callable** until the next discovery cycle files a record under its own name. That is the opposite of the spec sentence quoted above. The spec-correct reader is: exact record wins; if absent and the tool is in the discovery snapshot → treat as pending (fail closed), never implicit-approved; a legacy collapsed record must not *approve* the namespaced name (HEAD's `case exact == nil: return legacy` does, so HEAD is also non-compliant, but in the safe-ish direction). |
| **Verdict** | **Discard**; redesign with the not-found rule + a one-shot migration of collapsed records (schema-version bump), and rewrite the collapsed-record test to the new contract. |

### 8. Direct surface — `mcp_direct_catalog.go`, `mcp_direct_scope.go`

| | |
|---|---|
| **FR** | FR-008 ("built-ins positively identified, never inferred from a parse failure"; withhold only when no registration identity; admin exception in SC-005), FR-006 prompt withholding for all callers |
| **Compiles** | Yes, but the `directResolveNoCatalog` branch is left with an orphaned comment block and mis-indented `continue`; the prompt filter is left with a bare `{ ... }` block and a Warn whose text still says "for scoped caller". |
| **Correct** | **No — severe regression.** `builtinDirectToolNames` at HEAD is an **empty map** (`mcp_direct_catalog.go:374`); the parse-failure fallback the diff removes is currently the *only* thing classifying `describe_tool`, `retrieve_tools`, `read_cache`, `code_execution`, … as built-ins. Combined with the direct-scope hunk (early `return tools` for admins removed; `NoCatalog` → always `continue`; `Denied` → `continue`), every built-in disappears from the direct listing for every caller, and two existing tests (`mcp_direct_catalog_publish_test.go:100`, `mcp_direct_catalog_test.go:199`) fail. The intent is right; the prerequisite — populate the set from the registered built-in names — was never done. Dropping unstamped prompts for admins too is spec-consistent (SC-005). |
| **Verdict** | **Discard as-is; re-port** after populating `builtinDirectToolNames` (or deriving it from `p.server`'s registered built-ins), then remove the parse fallback and the admin early-return; keep the NoCatalog window behaviour for admins or accept the empty-startup listing explicitly. |

### 9. `mcp_routing.go` `renderDirectTools` hunk

| | |
|---|---|
| **FR** | intended FR-006 / SC-005 ("empty upstream prompt name `a:` registered as `a__` is no longer listed for anyone") |
| **Compiles** | **No.** `serverName` / `promptName` are not in scope in `renderDirectTools` (it iterates tool names). The identifiers exist at `mcp_routing.go:1322` (prompt aggregation) and `:1532` (prompt scan), which is where the check belongs. |
| **Verdict** | **Discard**; re-implement as `if !ok \|\| serverName == "" \|\| promptName == "" { continue }` at line 1322 (and treat `promptName == ""` as unfetchable in `prompts/get` at `mcp.go:900`). |

### 10. Profile tool + URL middleware — `profile_tool.go`, `server.go` @2321, `profile_pin_enforcement_test.go`, new `profile_scope105_test.go`

| | |
|---|---|
| **FR** | FR-003 (`active_profile` = stored selection, servers = effective scope; URL precedence), FR-004 (uniform non-disclosing 404 across missing/deleted/non-selectable/pin-mismatch/no-profiles for `/mcp/p/<slug>`, `/mcp/p`, `/mcp/p/`) |
| **Compiles** | Yes. `server.go` already imports `auth`; `selectableProfileNames`, `callerVisibleServers`, `profile.WithProfileScope/NewProfileScope`, `setProfileCtx`, `newSetProfileTestServer` (has research/deploy/mixed), `newTruncatingRetrieveToolsProxy`, `rt.UpdateConfig`, `ConfigSnapshot().Clone()` all exist. `Server{runtime: rt}` literal works. |
| **Correct** | HEAD already has the intersection predicate (`selectableProfileNames`, Spec 104 FR-016b); the diff adds the two missing pieces correctly: `effectiveProfileServers` re-resolves after the session write (pin > URL > session), and the pin branch now also requires a non-empty visible intersection. Reporting `active_profile:""` on clear-with-pin matches FR-003 literally (flipped assertions are spec-mandated; note it as a Spec-104 behaviour change). Middleware: computing `selectableProfileNames` for agent callers before the "no profiles" branch gives one status/body for all six cases including pin-mismatch (HEAD returns 403 with the pin name — a disclosure) and the empty profile (wildcard agents are still `IsScopedCaller`, so `len(callerVisibleServers)==0` refuses it). Gaps: slug parsing duplicated; existing tests asserting the 403 pin-mismatch body and the `"available"` list for agent tokens must be updated; admins unchanged (correct). |
| **Verdict** | **Port with light changes** (dedupe slug parsing; update the older 403/`available` tests; fix test import ordering). |

---

### Suggested port order

1. Hunks 3 + 6 together (identity), with index rebuild trigger and `extractToolName` cleanup.
2. Hunk 7 redesigned (fail-closed not-found for discovered tools + collapsed-record migration); only then does hunk 6 close FR-009.
3. Hunks 1 + 2 (cache), with the deny-all guard kept and the error collapse narrowed.
4. Hunks 4 + 5 (`retrieve_tools`), with admin-parity fixes.
5. Hunk 10 (profiles), then hunk 8 re-done on top of a populated `builtinDirectToolNames`, then hunk 9 at the correct site.

Files referenced: `/private/tmp/claude-501/-Users-user-repos-mcpproxy-go--claude-worktrees-next-logical-task-93d333/2fafff0e-16ac-49f6-965a-dffdee48f8be/scratchpad/codex105.diff`; HEAD sources under `/Users/user/repos/mcpproxy-go/.claude/worktrees/next-logical-task-93d333/internal/{cache,config,index,runtime,server}/`.

# Tasks: Schema-Deferred Direct Mode — Full Enumeration Without Schemas

**Input**: Design documents from `/specs/102-schema-deferred/`
**Prerequisites**: [plan.md](plan.md) (required), [spec.md](spec.md) (user stories), [research.md](research.md), [data-model.md](data-model.md), [contracts/direct-deferred-surface.md](contracts/direct-deferred-surface.md)

**Tests**: Test tasks ARE included. Constitution V (Test-Driven Development) is a PASS gate in plan.md — *"Every behavior lands test-first"* — so each behavioral task is preceded by its failing test.

**Organization**: By user story, in spec priority order. US1 and US2 are both P1 and both required for the feature to make sense (deferral without the recovery stage trades tokens for failed calls); US3 and US4 are P2.

## Format: `[ID] [P?] [Story] Description`

- **[P]**: parallelizable — different file, no dependency on an incomplete task
- **[Story]**: US1–US4; Setup / Foundational / Polish phases carry no story label

---

## ⚠️ Phase 0: Open approvals (each blocks its OWN dependents, not everything)

plan.md's Complexity Tracking records **three narrowings of normative spec MUSTs**, each ending *"needs the maintainer's assent at the tasks stage"*. That stage is now. These are not implementation choices — each one changes what the spec promises, so the answer belongs in spec.md, not in the plan's appendix.

**They do not all block everything.** An earlier draft made Phase 0 a blanket gate; that stalls work the approvals cannot affect. The real dependencies:

| Approval | Blocks | Does NOT block |
|---|---|---|
| T001 (FR-017 atomicity) | catalog **publication** (T017–T020) | config axis, `Peek`, rendering |
| T002 (stale schema) | pre-dispatch validation semantics (Phase 5) and the skew residual tests (T072+) | everything else |
| T003 (stale annotations) | the permission-tier and annotations-override work (T024, T053) | everything else |
| T004 (fold D1/D2/D3 into spec.md) | **Phase 1** — the config key it names is what Phase 1 builds | — |

So T004 gates Phase 1; T001–T003 gate their own dependents and are otherwise concurrent with it.

- [x] T001 Obtain and record the maintainer's decision on narrowing 1: **FR-017's "rebuilt atomically, so no window exposes one without the other" ships as a safety property, not a transaction.** The catalog and mcp-go's tool registry are two publications and cannot be made one without forking mcp-go or taking the direct listing off `SetTools`. Record the accepted wording in `specs/102-schema-deferred/spec.md` FR-017 (amend the MUST, or add an explicit "delivered as" clause naming D13's five rules).
- [x] T002 Obtain and record the maintainer's decision on narrowing 2: **a schema-only change (input or output) can be described one generation stale for the width of the publication window** — also a narrowing of SC-007. Record in `specs/102-schema-deferred/spec.md` beside FR-017/SC-007, including that the input case self-heals via the pre-dispatch validator (US3) and the output case does not but is advisory only.
- [x] T003 Obtain and record the maintainer's decision on narrowing 3: **an annotations-only change can be listed/described one generation stale** — a narrowing of FR-011 and SC-005. Record in `specs/102-schema-deferred/spec.md`, including the compensating property that call-time authorization never reads the catalog (`makeDirectModeHandler` re-derives the tier from its own captured upstream annotations).
- [x] T004 Fold the plan's D1, D2 and D3 resolutions back into `specs/102-schema-deferred/spec.md`, removing the `[NEEDS CLARIFICATION]` markers in FR-001 (dedicated `direct_tool_response_mode` key), FR-006 (deferred entries strip `outputSchema`; `describe_tool` definitions gain additive `output_schema`) and the FR-009 batch-cap question (cap stays 5; 50 for `check:true`). A spec carrying unresolved markers while its plan treats them as decided is the drift this task exists to close.

**Checkpoint**: spec.md contains no `[NEEDS CLARIFICATION]` marker and no normative MUST that the plan silently narrows. T004 is done before Phase 1 starts; T001–T003 are done before their named dependents.

---

## Phase 1: Setup (shared infrastructure)

**Purpose**: the config axis and the one cache primitive everything else reads through. No behavior change on any surface yet.

- [x] T005 Add `DirectToolResponseMode` field and the `ToolResponseModeDeferred` constant beside the existing `ToolResponseMode` declarations in `internal/config/config.go`, defaulting to `"full"`.
- [x] T006 Add validation for `direct_tool_response_mode` (accepted values `full`, `deferred`) beside the existing `tool_response_mode` validation block in `internal/config/config.go`, and extend `routing_mode` validation so `"schema_deferred"` is rejected with a message naming the supported composition (`routing_mode: "direct"` + `direct_tool_response_mode: "deferred"`) per FR-002.
- [x] T007 Write failing test (before T005/T006) for config validation messages and default resolution in `internal/config/config_test.go`: default is `full`; `deferred` accepted; an unknown value names both accepted values; `routing_mode: "schema_deferred"` produces the composition-naming error.
- [x] T008 [P] Add the `MCPPROXY_DIRECT_TOOL_RESPONSE_MODE` environment alias beside the existing `MCPPROXY_TOOL_RESPONSE_MODE` alias in `internal/config/loader.go`.
- [x] T009 [P] Add the `--direct-tool-response-mode` serve flag in `cmd/mcpproxy/main.go`, following the existing `--tool-response-mode` flag pattern, with help text that says *direct* surface (the existing flag's help says "retrieve_tools serialization mode" and must stay that way — FR-001 parity requirement).
- [x] T010 [P] Write failing test for env/flag/config precedence for the new axis in `internal/config/loader_test.go`, mirroring the existing `tool_response_mode` precedence test.
- [x] T011 [P] Write failing test for `toolsig.Cache.Peek` in `internal/toolsig/cache_test.go`: a warmed hash returns `(sig, true)`; an unwarmed hash returns `(zero, false)` **and does not compile or memoize** — assert the miss leaves the cache size unchanged, since an accessor that silently compiled would violate FR-005's "no per-request compilation".
- [x] T012 Add the non-compiling, miss-reporting `Peek(hash) (Signature, bool)` method to `internal/toolsig/cache.go`, leaving the existing compiling accessor and the index-time `Warm` path untouched for their current callers (FR-005).

**Checkpoint**: config axis exists and validates; `Peek` exists. Nothing reads either yet; every existing test still passes.

---

## Phase 2: Foundational (BLOCKING — the catalog)

**Purpose**: the `directCatalog` snapshot (FR-017) that listing, describe, validation and the discovery filters all resolve through. **No user story can begin until this phase is complete** — US1 renders from it, US2 resolves through it, US3 validates against it, US4 rebuilds it.

**⚠️ D13 rule 1 is structural, not stylistic**: the builder must not publish. `buildDirectModeTools` returns the catalog alongside the tool set so the single publisher can `SetTools` *first* and swap the catalog immediately after.

- [x] T013 Write failing test for the pure builder seam in `internal/server/mcp_direct_catalog_test.go`: driving `buildDirectCatalog` with a fixture `[]*config.ToolMetadata` produces one entry per tool carrying display name, `(server, tool)` pair, description, `ParamsJSON`, `OutputSchemaJSON`, Spec-032 `Hash`, annotations and `requiredPermission`. Fixture-driven because `upstream.Manager` is concrete and `DiscoverTools` only returns tools from connected clients (research.md R12).
- [x] T014 Write failing test for display-name collision withholding in `internal/server/mcp_direct_catalog_test.go`: server `a` + tool `b__c` and server `a__b` + tool `c` flatten to the same `a__b__c`; assert **neither is listed** and **neither resolves through the catalog**, and that a warning names both origins — in both flattening directions (D6/D13 rule 5). Today's code is undefined here, not merely non-deterministic. The matching *"neither is describable"* assertion is deliberately NOT here: describe resolution does not exist until T045/T046, so bundling it would make this Phase 2 test un-greenable inside Phase 2. It lands as T046a.
- [x] T015 Create `internal/server/mcp_direct_catalog.go` with the immutable `directCatalog` type, a sorted deterministic build, collision withholding with both origins logged, and a `generation` counter logged per publish (D13).
- [x] T016 Add the `atomic.Pointer[directCatalog]` field to `MCPProxyServer` in `internal/server/mcp.go` and the `publishDirectCatalog` swap in `internal/server/mcp_direct_catalog.go`; retire the mutex-guarded `directToolPermissions` map the catalog absorbs.
- [x] T017 Write failing test in `internal/server/mcp_direct_catalog_test.go` asserting the catalog pointer is **never** swapped from inside the builder, and that the publisher orders `SetTools` strictly before `publishDirectCatalog` (D13 rule 1).
- [x] T018 Split `buildDirectModeTools` into the pure pair `buildDirectCatalog` + `renderDirectTools` in `internal/server/mcp_routing.go`, returning `([]ServerTool, *directCatalog)` — a **non-nil catalog on every return path**, including the `DiscoverTools` error path that returns `nil` early today (D12).
- [x] T019 Update the two `RefreshDirectModeTools` / test call sites for the new builder signature in `internal/server/mcp_routing.go`, making `RefreshDirectModeTools` the single publisher.
- [x] T019a Change `makeDirectModeHandler`'s signature in `internal/server/mcp_routing.go` so it takes and captures its own `*directCatalogEntry`, and have `renderDirectTools` pass each entry as it builds the handler. **This production task was missing entirely from an earlier draft** — T020 updated call sites for a signature change no task performed, and Phase 5's validation task consumed a captured entry nothing had captured. The closure capture is what makes R9's "a dispatch can never validate against a definition other than the one its own registration advertised" true, so it belongs here, in the foundational phase, not with the validator that later reads it. **This box was ticked in Phase 2 but the work was never done** — the handler still took `(serverName, toolName, annotations)` when Phase 5 arrived looking for the captured entry, which is the exact failure the task was written to prevent. Actually landed with Phase 5 (T059–T064); the four call sites were updated then too, so T020's tick had the same problem.
- [x] T019b Append the `describe_tool` registration inside direct tool-set **construction** in `internal/server/mcp_routing.go`, on **every** return path including the `DiscoverTools` error path (FR-018). **Moved out of Phase 4**: T024 asserts a freshly initialized proxy already lists `describe_tool`, so leaving the registration in the describe phase made Phase 2 depend on Phase 4 — a cycle through its own prerequisite. Registration is a one-line append with no resolver behind it; the describe *handler* work stays in Phase 4, where it belongs.
- [x] T020 Update `makeDirectModeHandler` call sites for the T019a signature change in `internal/server/mcp_routing_test.go`, `internal/server/toon_surface_isolation_test.go`, `internal/server/profile_pin_enforcement_test.go` and `internal/server/mcp_direct_callability_test.go`. Not `[P]`: it follows T019a, and two of these files also move under T023.
- [x] T021 Write failing test for deny-on-catalog-miss in `internal/server/mcp_direct_scope_test.go`: an unknown display name is denied by the filters, built-ins are allowed by explicit name set, and a **nil** catalog is not treated as a miss (D13 rule 2).
- [x] T022 Rewrite `filterDirectModeToolsForAuth` in `internal/server/mcp_direct_scope.go` to resolve `(server, tool)` through the catalog instead of `ParseDirectToolName`, taking the permission tier from the entry's upstream-derived `requiredPermission` — **never** from the registered `mcp.Tool.Annotations`, which carry mcp-go's `destructiveHint=true` default (D10/D13 rule 3). Retire `lookupDirectToolPermission`'s separate map.
- [x] T023 [P] Rewrite `filterDirectToolsForAgentCallability` in `internal/server/mcp_direct_callability.go` to use the same catalog resolution (D10).
- [x] T024 Write failing test for the D15 initial rebuild in `internal/server/mcp_routing_test.go`: on a freshly initialized proxy with **zero** upstream servers, the catalog is non-nil and `p.directServer` already lists `describe_tool` before any `servers.changed` fires. Depends on T019b for the registration and, for the tier assertions it makes, on approval T003.
- [x] T025 Make `initRoutingModeServers` perform the initial direct rebuild by **calling** `RefreshDirectModeTools()` (not a second copy of the ordering) in `internal/server/mcp_routing.go` (D15).
- [x] T026 Write failing regression test in `internal/server/server_test.go`: a `servers.changed` published immediately after `NewServer` returns still reaches the direct rebuild (R14).
- [x] T027 Hoist `SubscribeEvents()` out of the listener goroutine into the constructor, ahead of `StartBackgroundInitialization`, in `internal/server/server.go` — publishing an empty catalog at init retires the nil-catalog accidental self-heal, so a dropped first `servers.changed` no longer recovers (D15/R14).

**Checkpoint**: one authoritative catalog exists, is published in the right order, and both discovery filters resolve through it. Full-mode direct listings are unchanged; no user-visible behavior has moved yet.

---

## Phase 3: User Story 1 — Deferred enumeration (Priority: P1) 🎯 MVP

**Goal**: with `direct_tool_response_mode: "deferred"`, every visible upstream tool is still listed — same names, count and annotations — but each entry carries its compact signature and a minimal permissive schema instead of the upstream `inputSchema`/`outputSchema`.

**Independent test**: enable deferral on a fixture proxy with multiple upstreams, fetch `tools/list` on the direct surface, and assert every tool present, no upstream schema properties, signature-suffixed descriptions, and ≥70% token reduction against the frozen 45-tool corpus.

- [x] T028 [US1] Write failing tests for deferred entry rendering in `internal/server/mcp_routing_deferred_test.go`: the marshalled `inputSchema` is byte-exactly `{"type":"object"}` — asserted **on the JSON, not the Go struct**, since `mcp.NewTool` marshals `{"properties":{},"required":[],"type":"object"}` (R11/D9); no upstream properties or required list; the description is the existing `[server] …` text with the Spec-085 signature appended.
- [x] T029 [US1] Write failing test for annotations parity in `internal/server/mcp_routing_deferred_test.go`: the marshalled `annotations` object is **byte-identical to full mode's** for three fixtures — nil upstream annotations, one hint set, all five set — proving the raw-schema constructor was seeded with mcp-go's `NewTool` defaults (D9). Also assert a deferred entry marshals without `errToolSchemaConflict`.
- [x] T030 [US1] Write failing test for the signature cache miss path in `internal/server/mcp_routing_deferred_test.go`: a tool whose hash is not warmed is listed **without** a signature suffix — never dropped, never delayed (FR-005).
- [x] T031 [US1] Implement the deferred branch of `renderDirectTools` in `internal/server/mcp_routing.go` using `mcp.NewToolWithRawSchema(…, json.RawMessage(`{"type":"object"}`))`, seeded with mcp-go's `NewTool` annotation defaults and *then* given the upstream overrides (D9), reading signatures via `toolsig.Cache.Peek`. Leave the full-mode `NewTool` path untouched (FR-015).
- [x] T032 [US1] Write failing test for set identity across modes in `internal/server/mcp_routing_deferred_test.go`: same names, count, annotations and ordering source in full and deferred (FR-008), including under agent-token and profile filters (SC-005/FR-016).
- [x] T033 [US1] Write failing test for FR-015 byte-stability in `internal/server/mcp_routing_deferred_test.go` using the **live stdio fixture** form (reuse the `preflight_e2e_test.go` harness + `testdata/preflight_fixture_server.js`), which runs unchanged at the merge-base and on this branch. Assert deferral-off rendering is byte-identical to pre-feature output modulo the appended `describe_tool` entry. Fall back to the same-tree differential form only if the fixture is judged too heavy — and say which was used in the test's doc comment. **Landed as the live-fixture form** in `internal/server/mcp_routing_deferred_e2e_test.go` rather than `mcp_routing_deferred_test.go`: the preflight harness is `//go:build !windows`, so the test must carry that tag and cannot share a file with the untagged unit tests. Golden `internal/server/testdata/direct_full_prefeature.golden.json` was captured by running this same file, unmodified, against a merge-base binary.
- [x] T034 [US1] Write failing test for the direct-server instructions in `internal/server/mcp_routing_test.go`: a custom `instructions` config value still appears on the direct server's `initialize`, with the deferral legend appended, in **both** modes (D11).
- [x] T035 [US1] Add `resolveDirectInstructions(custom string)` in `internal/server/mcp_routing.go` — custom when non-empty, else a **direct-specific default**, then the legend — and attach it via `WithInstructions` on `directServer` in `initRoutingModeServers`. Deliberately **not** `resolveInstructions`, whose `defaultInstructions` advertises `retrieve_tools` and `call_tool_*`; the direct default names only `server__tool` calling, `describe_tool` and the ABOUT links — never `upstream_servers`, which this surface does not register (D16). Safe only because T019b registers `describe_tool` back in Phase 2: naming a tool the surface does not expose would be exactly the D16 mistake one level up.

- [x] T035a [US1] Run the SC-001/SC-002 token gates for this phase's own claim: US1's stated independent test asserts **≥70% payload reduction** on the frozen 45-tool corpus, and no other Phase 3 task measures it. Use the spec-083 profiler pipeline directly here; the reusable `bench/arms/` arm and its render golden stay T075/T076 in Polish. Without this, Phase 3 cannot be signed off against the criterion it declares.

- [x] T035b [US1] **Found by live verification, not by any planned task.** Warm the signature cache for a server's WHOLE allowed tool set in `applyDifferentialToolUpdate` (`internal/runtime/lifecycle.go`), not only for the tools an update added or modified. The narrow form left the cache permanently empty on every restart against an existing index — the differential update finds nothing to do, so neither warm branch ran — and because `Peek` never compiles, the deferred listing then lost **every** compact signature for the life of the process while still looking well-formed. Spec 085 never saw this: its compact `retrieve_tools` reads through the compiling accessor and merely paid a first-call compile. Regression test: `TestApplyDifferentialToolUpdate_WarmsUnchangedToolsOnRestart` in `internal/runtime/sigcache_warm_test.go`; `renderDirectTools` also now logs `signature_misses` per rebuild, because a missing suffix is otherwise indistinguishable in the payload from a tool with no parameters.

**Checkpoint**: US1 is independently demonstrable — flip the config, fetch `tools/list`, observe the token drop with the full tool set intact.

> ### ✅ RESOLVED 2026-08-29 — SC-001 restated per corpus shape (was: escalation from T035a)
>
> **Maintainer decision:** adapt the threshold. SC-001 now asks **≥25%** on the frozen
> 45-tool corpus and **≥30%** on the 527-tool snapshot — roughly 15% relative below each
> measured value, as regression headroom rather than a second projection. Both bounds are
> now ASSERTED in `internal/server/mcp_routing_deferred_tokens_test.go`
> (`TestDeferredDirect_TokenReduction_Corpus45` / `_LargeCorpus`), so that file is the
> SC-001 gate rather than a stand-in. The original 70% is retained as an upper tripwire:
> clearing it means the corpus premise changed and the criterion must be re-derived.
>
> The original escalation is kept below, because it is the evidence the decision rests on.
>
> ### ⚠️ Escalation from T035a — SC-001's original thresholds were not reachable
>
> T035a ran the gates and SC-002 passes (39/45 = 86.7% one-shot callable, floor
> 80%). **SC-001 does not, and cannot on either dataset in this repo.** Measured
> with the real renderer and the profiler's pinned `cl100k_base` encoder:
>
> | corpus | tools | full | deferred | reduction | SC-001 asks |
> |---|---|---|---|---|---|
> | `corpus_v2.tools.json` (frozen reference) | 45 | 6116 | 4301 | **29.7%** | ≥70% |
> | `livemcptool_snapshot/tools.json` | 527 | 99918 | 65138 | **34.8%** | ≥85% |
>
> The shortfall is arithmetic. On corpus_v2 the upstream `inputSchema` is 2604 of
> 6116 tokens (42.6%); the rest is description (1784), mcp-go's unconditional
> `annotations` block (1125, ~25/entry, untouchable by any serialization change),
> names (286) and punctuation. Deferral removes the schema and adds the FR-004
> signature, so **even deleting the schema *and* the signature outright caps out
> at 38.9%** — below SC-001's floor. FR-004 forbids closing the gap by
> truncating descriptions, and Spec 085's first-sentence grammar only reaches
> ~68% here.
>
> This is a **spec-numbers defect, not an implementation defect**: SC-001 assumes
> the schema is a far larger share of a `tools/list` payload than it is in either
> dataset. Resolution belongs to **T076** — restate the threshold per corpus
> shape, or re-target the criterion at a schema-heavy corpus — and needs the
> maintainer's decision. Until then `TestDeferredDirect_TokenReduction_Corpus45`
> asserts a 25% **regression floor** (measured 29.7%) plus an upper guard that
> fails if a future change ever does clear 70%, so this note cannot go stale
> silently.

---

## Phase 4: User Story 2 — describe_tool on the direct surface (Priority: P1)

**Goal**: `describe_tool` is present on the direct surface in both modes, accepts both id forms, resolves through the registration mapping, and never discloses anything the same session's `tools/list` would not.

**Independent test**: call `describe_tool` with a direct `server__tool` id and assert the definition is field-equal to the Spec-085 full rendering; assert an out-of-visibility tool returns a per-id `not_found`.

- [x] T036 [US2] Invert and rename the `"direct routing mode"` subtest of `TestDescribeTool_RegisteredInRetrieveToolsModeOnly` in `internal/server/mcp_describe_tool_test.go` to assert **presence** on the direct surface (FR-009/FR-018). It is green on `origin/main` today, so its failure during implementation is expected, not a regression.
- [x] T037 [US2] Append the `describe_tool` registration inside direct tool-set **construction** in `internal/server/mcp_routing.go`, on **every** return path including the `DiscoverTools` error path, so a `SetTools` refresh or an upstream hiccup can never drop the built-in (FR-018). **Already satisfied by T019b in Phase 2** — `withDirectBuiltins` is applied on all three return paths of `buildDirectModeTools`; verified, not re-implemented. T046 swaps the registered handler for the direct-surface variant.
- [x] T038 [US2] Rewrite all **five** retrieve_tools-specific strings in `internal/server/mcp_describe_tool.go` to be surface-neutral (D5/R5): the tool description, both parameter descriptions, the `describeNotFoundRemediation` constant and the inline malformed-id remediation. The `tool_ids` prose must name **both** accepted id forms, since FR-011 requires `server__tool` here. Keep the single builder.
- [x] T039 [US2] Re-measure `TestDescribeTool_DefinitionTokenBudget` (`internal/server/mcp_describe_tool_test.go`) after T038. The 250-token budget currently sits at ~243, so naming both id forms may overrun it. If it does: shorten the prose first; raise the budget only with the same deliberate, documented justification the 150 → 250 bump carried.
- [x] T040 [US2] Regenerate exactly `default_server.json` and `retrieve_tools_mode.json` under `internal/server/testdata/toolslist_goldens/` via the documented `MCPPROXY_WRITE_TOOLSLIST_GOLDENS` flow — the one enumerated FR-010 exception. `code_execution_mode.json` and the frozen `pre099/` baseline are **never** regenerated; a diff in either means the change reached further than claimed.
- [x] T041 [US2] Extend `describePlainDelta` in `internal/server/describe_plain_corpus_test.go` with the named remediation substitutions, keeping `testdata/describe_plain_corpus/pre099.json` frozen. Widen the delta value to an ordered slice of substitutions if one string needs more than one — and update the assertion site accordingly.
- [x] T042 [US2] Write failing test for both id forms in `internal/server/mcp_describe_direct_test.go`: canonical `server:tool` and direct `server__tool` resolve to the same definition through the **registration mapping**, including a server name that itself contains `__` — never by re-parsing the display name.
- [x] T043 [US2] Write failing test for the permission-tier gate in `internal/server/mcp_describe_direct_test.go`: a read-scoped token gets `not_found` for a destructive tool absent from its own listing (the disclosure hole the existing resolver leaves open).
- [x] T044 [US2] Write failing test for the catalog-divergence case in `internal/server/mcp_describe_direct_test.go`: a listed pending/changed tool returns its **snapshot-backed definition** in definition mode and a `pending_approval` / `changed` verdict under `check: true` (SC-007, FR-011/FR-017) — the case where a naive index-backed resolver answers `not_found` and makes deferral strictly worse than full mode.
- [x] T045 [US2] Create `internal/server/mcp_describe_direct.go`: the catalog-backed direct resolver with listing-parity gates (token server scope + operation-permission tier + profile + agent callability), plain `not_found` for invisible ids in both modes, snapshot-backed definitions for visible ids.
- [x] T046 [US2] Add the per-surface resolver seam to the `describe_tool` handler in `internal/server/mcp_describe_tool.go` — existing surfaces keep the index-backed `toolVisibleToSession` **byte-identical**; the direct surface gets the T045 resolver.
- [x] T046a [US2] Write failing test for the collision case's describe half in `internal/server/mcp_describe_direct_test.go`: a withheld `__` collision resolves in **neither** id form — the assertion split out of T014, which could not go green inside Phase 2 because no resolver existed yet.
- [x] T046b [US2] Write failing test for `output_schema` in `internal/server/mcp_describe_direct_test.go`: present iff the tool declares one, absent otherwise. **Before T047**, not after — an earlier draft ordered this test after its implementation, which cannot fail first and so does not satisfy the constitution's test-first gate.
- [x] T047 [US2] Add the additive `output_schema` field at the **definition-assembly seam** in `internal/server/mcp_describe_tool.go` (D2), not in `buildFullToolEntry`, so full-mode retrieve_tools bytes are untouched (R2).
- [x] T048 [US2] Re-run gate 4 (`TestDescribeToolPlainCorpus_ByteIdenticalWithOneEnumeratedDelta`) after T047 rather than assuming it: `output_schema` is `omitempty` and today's fixture tools declare none, but that must be verified, not asserted from the plan.
- [x] T049 [US2] Write failing test for the annotations-override case in `internal/server/mcp_describe_direct_test.go`: a listed pending/changed **destructive** tool describes with its real annotations and `call_with: "destructive"` — `buildFullToolEntry` otherwise reads annotations from the StateView and would silently downgrade the safety hint to `read` (D10).
- [x] T050 [US2] Add the optional catalog-supplied annotations override at the definition-assembly seam in `internal/server/mcp_entry_builder.go`, unused on the retrieve path so `TestRetrieveToolsFullMode_GoldenByteIdentity` passes unregenerated on **both** its goldens.
- [x] T051 [US2] Write failing test for the check-mode adapter in `internal/server/mcp_describe_direct_test.go`: a `server__tool` id under `check: true` returns its verdict **under the id the caller sent** (D14) — without the canonicalize-and-restore path every direct id answers `not_found`, since `preflight.Evaluate` accepts colon ids only.
- [x] T052 [US2] Implement the check-mode adapter in `internal/server/mcp_describe_direct.go`: canonicalize `server__tool` → `server:tool` before `preflight.Evaluate`, gate invisibility **without** consulting the evaluator, project `status`/`reason`/`action`/`retryable` through untouched, and restore the caller's original id and ordering in both the response and the activity record (D14).
- [x] T053 [US2] Add the id-gate seam for the direct surface in `internal/server/mcp_describe_check.go`, leaving verdict logic unchanged.
- [x] T054 [US2] Create `internal/server/preflight_glue.go`'s `directCatalogIndexReader` and pass it as `preflight.EvalContext.Index` on the direct surface, so id resolution **and** the `did_you_mean` corpus share the catalog authority (D10/FR-017).
- [x] T055 [US2] Write failing test for suggestion discipline in `internal/server/mcp_describe_direct_test.go`: a read-scoped token's `check: true` miss returns no `did_you_mean` entry naming a destructive tool absent from its own listing, and the definition-mode case-correction suggestion is gated by the same catalog-backed resolver (D10).
- [x] T056 [US2] Add the resolver seam to `suggestCanonicalToolID` in `internal/server/mcp_visibility.go`, keeping the existing retrieve-surface resolvers byte-identical. **Landed as a sibling, not a branch**: `suggestDirectToolID` in `mcp_describe_direct.go`, with the seam at the call site in `resolveDescribeDefinition`. Threading a surface flag into `suggestCanonicalToolID` would put direct-only rules (the operation-permission tier, withheld display-name collisions) inside a predicate three retrieve-path callers share; the function is documented and byte-identical.
- [x] T057 [US2] Write failing test for listing↔describe parity **in both directions** in `internal/server/mcp_describe_direct_test.go`: with a server whose name contains `__`, compare the session's rendered `tools/list` name set against the set of ids that resolve in definition mode, for an admin session, a read-scoped token, a write-scoped token and a profile-pinned session. No id may be describable-but-unlisted (disclosure) or listed-but-undescribable (SC-007).
- [x] T058 [US2] Write failing test in `internal/server/mcp_describe_direct_test.go` for a server removed between `tools/list` and `describe_tool`: the stale id returns a per-id not-found **without failing the batch**. (The `output_schema` half moved to T046b, where it precedes its implementation.)

**Checkpoint**: Catalog → Inspect → Execute is complete on one surface. US1 + US2 together are the shippable increment.

> ### Known limitations closed out with Phase 4 (from the cross-model review)
>
> - **A server name containing `:` has no check-mode verdict.** `preflight` ids
>   are `<server>:<tool>` split on the FIRST colon, so such a server cannot be
>   named to the evaluator at all — canonicalizing would hand it a different
>   (server, tool) pair and it would answer confidently about another tool. The
>   tool stays listed and fully describable (definition mode reads the snapshot
>   and never canonicalizes); only `check: true` refuses it, as `not_found`.
>   A wrong verdict is worse than no verdict. Config validation only requires a
>   server name to be non-empty, so this is reachable, and it is a property of
>   the repo-wide canonical id grammar rather than of this feature — the index,
>   `call_tool_*` and the retrieve surfaces share it. Fixing it properly means
>   changing that grammar, which is out of scope here.
>   Test: `TestDescribeDirectCheck_ColonInServerNameIsRefusedNotMisEvaluated`.
> - **`did_you_mean` formatting differs between a gated and an
>   evaluator-produced `not_found`.** Gated ids suggest in both accepted
>   grammars (the caller's own vocabulary); the evaluator suggests canonical ids
>   only. This is not a disclosure — both corpora are the SAME
>   session-visible snapshot (`directCatalogIndexReader` is built from it), so
>   neither can name anything the caller could not list; only the formatting
>   differs.
> - **Listing↔describe parity holds WITHIN a catalog generation.** Across the
>   `SetTools`-then-publish window a request can see registry N+1 with catalog N,
>   so a just-added tool is listed but not yet describable. That is D13's
>   documented one-directional guarantee, accepted at T001 as a safety property
>   rather than a transaction, and it fails in the safe direction (never
>   describable-but-unlisted). Phase 4 additionally pins ONE snapshot per
>   describe/check call, so a rebuild cannot make two ids of the same batch — or
>   a batch and its own suggestion corpus — disagree.

---

## Phase 5: User Story 3 — Self-healing direct calls (Priority: P2)

**Goal**: a wrong guess from a lossy signature costs exactly one bounded retry, not an opaque upstream error.

**Independent test**: call a direct tool omitting a required parameter; assert the error embeds the full input schema and a hint, and that a transport-level failure attaches no schema.

- [x] T059 [US3] Write failing test for pre-dispatch validation in `internal/server/mcp_routing_validation_test.go`: a missing required argument yields `invalid_params` with the tool's **full** input schema and a one-line hint embedded, in **both** serialization modes (self-healing is mode-independent).
- [x] T060 [US3] Write failing test in `internal/server/mcp_routing_validation_test.go` that validation runs against the **stored upstream schema**, never the advertised `{"type":"object"}` placeholder — the stale-full-mode-client scenario (US3 scenario 4).
- [x] T061 [US3] Write failing test in `internal/server/mcp_routing_validation_test.go` for fail-open: an uncompilable or unsupported stored schema dispatches exactly as today, counted in logs, never blocking a call a schemaless proxy would have allowed (FR-013b).
- [x] T062 [US3] Write failing test in `internal/server/mcp_routing_validation_test.go` that non-argument failures (upstream down, auth, timeout) attach **no** schema and keep their current shapes.
- [x] T063 [US3] Write failing test in `internal/server/mcp_routing_validation_test.go` asserting that a validation failure emits the `emitActivityToolCallStarted` + `emitActivityToolCallCompleted("error", …)` pair, so the rejected call is visible in the availability funnel.
- [x] T064 [US3] Add pre-dispatch validation **and** its activity emission in one change in `internal/server/mcp_routing.go`: call `p.inputValidator.validateArgs` against the captured catalog entry's `ParamsJSON` (captured by T019a), placed **immediately after** `directToolCallabilityBlockWithReason` and **before** `markSessionWorked`, render the existing `invalidParamsErrorResult` on failure, and emit the started/completed-error pair matching the Spec-085 `call_tool_*` path. The unconditional `emitActivityToolCallStarted` stays on the dispatch path only.

  **These are deliberately one task.** An earlier draft split validation from its activity emission; landing the first alone would start rejecting calls that appear nowhere in the funnel — creating precisely the observability blind spot issue #969 established this handler must not have, and calling it an intermediate state.

**Checkpoint**: deferral's worst case is one retry.

---

## Phase 6: User Story 4 — Operator opt-in, hot-reload, cache-safe rollout (Priority: P2)

**Goal**: flipping the setting on a running proxy rebuilds the direct surface and notifies connected clients, with no restart and no churn on unrelated config edits.

**Independent test**: with a client connected, flip the config value; assert a `notifications/tools/list_changed` is emitted, the next `tools/list` reflects the new serialization, and the tool set is identical across the flip.

- [x] T065 [P] [US4] Add the `direct_tool_response_mode` clause to `DetectConfigChanges` in `internal/runtime/config_hotreload.go`, beside the existing `tool_response_mode` clause.
- [x] T066 [US4] Write failing test in `internal/runtime/config_hotreload_test.go` that a change to the new field is detected and an unrelated edit is not.
- [x] T067 [US4] Write failing test for the rebuild guard in `internal/server/server_test.go`: a serialization flip triggers a rebuild **and** a notification; an unrelated config edit triggers **no** `SetTools` and **no** notification (the FR-014 no-churn rule, which a nil catalog would otherwise defeat).
- [x] T068 [US4] Add the guarded direct rebuild to the `config.reloaded` branch of `listenForRoutingModeRefresh` in `internal/server/server.go`: compare the mode the current catalog was built with against the live effective mode read via `p.currentConfig()` — **never** construction-time `p.config` — and rebuild only on a real change.
- [x] T069 [US4] Write failing test (alongside T067, same file, before T068) in `internal/server/server_test.go` that a flip while `DiscoverTools` is failing still rebuilds and is not lost: an empty catalog is published with the new mode recorded (R8).
- [x] T070 [US4] **Requires Phase 5 complete.** Write the eight publication-skew interleaving tests in `internal/server/mcp_direct_skew_test.go` (D13), with a rebuild paused between `SetTools` and the catalog publish and a concurrent scoped `tools/list` + `describe_tool`, in three groups: (1) closed by design — added name, removed name, description-visible change, origin flip; (2) closed by withholding — a within-generation display-name collision; (3) the three documented residuals — input-schema-only, output-schema-only, annotations-only (read → destructive). The input-schema case **must** be semantically different but signature-identical (a nested-property edit the 085 grammar collapses to `~`) with the new hash warmed before the rebuild renders, or `Peek` misses and the description changes visibly instead; a canonicalization-equal edit will not do, since `hash` re-marshals the parsed schema.
- [x] T071 [US4] Write the two **no-rebuild** cache tests in `internal/server/mcp_direct_skew_test.go`: a signature-cache miss→warm and a hit→eviction between registration and a later filter/describe call must both leave the tool listed and describable — proof that the discriminator reads the **stored** `renderedDescription` rather than re-rendering.
- [x] T072 [US4] **Requires Phase 5 complete** — its self-healing assertions are Phase 5 behaviour. Assert across the whole skew set in `internal/server/mcp_direct_skew_test.go`: no describable-but-unlisted id; no entry scope-checked against one origin while its handler dispatches to another; no read-scoped token having a destructive tool's **call** admitted; `generation` increments exactly once per paused rebuild and not at all on a guarded no-op reload; and no stale definition leading to a call that *succeeds* against the wrong schema.

**Checkpoint**: the feature is operable on a running proxy and the concurrency story is proven rather than asserted.

> ### What the skew window actually exposes (T070, measured not assumed)
>
> D13's "the filters deny it, which is safe" is true of **scoped** sessions
> specifically. `filterDirectModeToolsForAuth` short-circuits for a session with
> no agent token and no active profile (`if !isScopedAgent && profileScope ==
> nil { return tools }`), so an unscoped session is served the raw registry and
> never consults the catalog at all. Two consequences, both recorded as tests
> rather than asserted away:
>
> - **Added name**: a scoped session sees nothing (denied on both sides); an
>   unscoped one sees it listed while describe still answers `not_found`.
>   Listed-but-undescribable — the safe direction, for a session entitled to the
>   whole surface anyway.
> - **Removed name**: it leaves the registry first, so the previous catalog can
>   still describe it for the width of the window. Stale, not a disclosure — the
>   same session could have described it one request earlier and gets the
>   definition it was already served.
>
> Both close at the publish. Neither is a new residual in the T002/T003 sense
> (those are the invisible schema- and annotations-only changes); they are the
> visible, self-correcting edges of the ordering, and the tests name them so a
> future reader does not have to rediscover that the "atomic" language in FR-017
> was narrowed at T001.
>
> Group 3's three residuals are reproduced as specified, including the
> signature-identical nested-schema edit (a canonicalization-equal edit will not
> do — `hash` re-marshals the parsed schema), and the annotations-only case
> proves its compensating property: a read-scoped token still sees the stale
> listing but its **call** is refused, because dispatch re-derives the tier from
> the annotations its own registration captured and never reads the catalog.
>
> **One more accepted consequence** (cross-model review): a flip made while
> `DiscoverTools` is failing publishes an EMPTY catalog stamped with the new
> mode, as T069 requires — so the guard afterwards sees no drift and a later
> reload will not retry. That is deliberate: the empty listing is itself the
> visible problem, and the `servers.changed` that fixes discovery rebuilds
> unconditionally in whatever mode is then configured. Making the guard retry
> instead would rebuild on every reload for as long as discovery stayed down.

---

## Phase 7: Polish & cross-cutting

- [x] T073 Add the standalone direct built-in gate to `internal/server/toolslist_snapshot_test.go` (**landed in a new file, `internal/server/toolslist_direct_builtins_test.go`, because the task itself requires the gate to be standalone rather than an entry in `toolsListGoldenSurfaces` — a separate file expresses that better than an orphan test in the shared snapshot file**) with `testdata/toolslist_goldens/direct_mode_builtins.json`: zero upstream tools (listing = `describe_tool` only), **both** serialization modes, membership + byte-exact serialization, plus the direct server's instructions string captured with an empty `instructions` config so the bytes are deterministic. It MUST be standalone, not an entry in `toolsListGoldenSurfaces` — `_DeltaIsEnumerated` reads a frozen `pre099/<surface>.json` per listed surface, and direct mode has no pre-feature baseline.
- [x] T074 In the same gate, assert over `p.directServer.ListTools()` **and** a real `tools/list` driven through the direct server on a session with no agent token, treating any difference between the two sets as a failure. `ListTools()` is the registration map; `handleListTools` serves `filteredTools(ctx)`, and `directServer` is the one routing-mode server carrying `WithToolFilter`s — so registered and served can diverge on exactly this server. Sort by tool name before serializing: `ListTools()` returns a map and Go randomizes iteration order.
- [x] T075 [P] Add the deferred-direct arm to `bench/arms/` plus its `bench/arms/testdata/*_golden.txt` render golden, following the registry contract in `specs/083-discovery-profiler/contracts/arm-interface.md` and `bench/arms/arm.go`. The existing arms all encode `retrieve_tools` result sets; none renders a direct `tools/list`.
- [x] T076 Run the SC-001/SC-002 token gates over the frozen 45-tool corpus with the spec-083 profiler's pinned tokenizer: assert ≥70% payload reduction and ≥80% one-shot-callable (non-lossy) share, and record the measured numbers in the PR description.

  **Measured, through BOTH paths, and they agree.** The T075 arm run through the profiler
  itself (`go run ./bench/cmd/bench -corpus-v2 … -arms baseline_json,compact_sig,direct_deferred`):

  | arm | tokens | savings vs baseline |
  |---|---|---|
  | `baseline_json` | 4386 | — |
  | `compact_sig` | 2081 | 52.6% |
  | **`direct_deferred`** | **3090** | **29.5%** |

  The in-process gate (`TestDeferredDirect_TokenReduction_Corpus45`, which measures the real
  `renderDirectTools` output rather than an arm's re-encoding) says **29.7%**.

  They are NOT the same measurement and should not be described as differing by one term: the
  arm emits canonical JSON without `annotations` or mcp-go's `_meta`, while the in-process gate
  counts the real marshalled `mcp.Tool` including both. (Adding a constant to both columns
  lowers a savings percentage rather than cancelling — full-mode totals are 4386 vs 6116 tokens
  for the same 45 tools, which is the size of the modelling gap.) What matters is that two
  independently-built paths over the same corpus land **within 0.2 percentage points** of each
  other, and both are less than half of what SC-001 asks for.
  SC-002 measures **86.7%** (39/45 non-lossy), comfortably over its 80% floor.

  So SC-001's ≥70% is **not met and not reachable**, confirmed independently of the
  implementation by the spec's own sanctioned pipeline. The ≥70% assertion this task asks for
  is therefore NOT added — writing a test that cannot pass is not a gate. See the escalation
  under the Phase 3 checkpoint for why the ceiling is arithmetic (38.9% even if the schema AND
  the signature were both deleted). **This is the one item in the spec that needs a maintainer
  decision rather than more implementation.**
- [x] T077 [P] Add E2E coverage in `internal/server/e2e_test.go`: live flip with a connected client (`notifications/tools/list_changed` observed, next listing reflects the mode, tool set identical — SC-006); guessed-wrong → one self-healing retry succeeds (SC-003); and the legacy aliases `/v1/tool_code` and `/v1/tool-code` covered by the **same** mode and notification assertions as `/mcp` (FR-003) — they are easy to miss and are explicitly in the direct-serving set.
- [x] T078 Run `make swagger` after the config struct change and commit the regenerated `oas/` artifacts. **Not deferrable to Polish, as an earlier draft had it**: the pre-push hook runs `swagger-verify` and rejects the push the moment the config struct changes, so this lands with T005 in Phase 1. Left listed here as the record of where it actually happened.
- [x] T079 [P] Document `direct_tool_response_mode`, its env alias and its serve flag in `docs/configuration.md`.
- [x] T080 [P] Write the feature doc under `docs/features/`: the deferral convention (`*`/`~` signature grammar), `describe_tool` on the direct surface, and the client-compat notes — schema-driven form UIs render empty forms; stale cached listings are safe in both directions; the `initialize` instructions delta.
- [x] T081 [P] Update `CLAUDE.md`: the MCP-protocol built-ins line (`describe_tool` availability on the direct surface) and a Recent Changes entry.
- [x] T082 Run the full gate set before opening the PR: `go test -race ./internal/...` (using the CI skip regex for `internal/server` locally), `go build -tags server -o /dev/null ./cmd/mcpproxy`, and `/opt/homebrew/bin/golangci-lint run --config .github/.golangci.yml ./...` — the v2 linter CI uses, which is stricter than `scripts/run-linter.sh`.
- [x] T083 Verify the five frozen gates land as predicted: gates 1 and 3 pass **unregenerated**; gate 2 shows exactly the two enumerated regens; gate 4 passes with the extended `describePlainDelta`; gate 5 is re-measured, not assumed. A diff anywhere else is a regression to fix, not a baseline to update.

  **Verified, not assumed.** All five pass. Golden churn across the entire branch is exactly:
  the two enumerated regens (`default_server.json`, `retrieve_tools_mode.json`) plus three NEW
  files (`direct_full_prefeature.golden.json`, `toolslist_goldens/direct_mode_builtins.json`, and
  the bench arm golden `bench/arms/testdata/directdeferred_golden.txt`). *(Corrected 2026-08-29:
  this note originally said "four NEW files … the two `direct_mode_builtins_*.json`".
  `git show 9aef8d6f1 --diff-filter=A` lists three; there is a single builtins golden, which
  covers both serialization modes in one file rather than one file per mode.)* `code_execution_mode.json` and both `pre099/` baselines are untouched —
  which is what confines the change to the two surfaces that register describe_tool.

  `go test -race ./internal/...` is green except `TestResolveDockerStatusResolvableAndWorking`,
  which passes in isolation and on a re-run of its own package: it writes a fake `docker` into
  a TempDir and manipulates PATH, so it is PATH-sensitive under a parallel whole-tree race run.
  Unrelated to this feature — it never touches the direct surface.

---

## Dependencies & execution order

```text
T004 (fold D1/D2/D3 into spec.md) ──► Phase 1 (config axis + Peek)
T001 (FR-017 atomicity)           ──► catalog publication (T017–T020)
T002 (stale schema)               ──► Phase 5, and T070/T072's residual cases
T003 (stale annotations)          ──► T024, T053

Phase 1 ──► Phase 2 (catalog; includes T019a handler capture + T019b describe_tool registration)
              │
              ├─► Phase 3 (US1, P1)
              ├─► Phase 4 (US2, P1)   [T046a completes the T014 collision assertion]
              │
              ├─► Phase 5 (US3, P2)   [needs T019a's captured entry; independent of US1/US2]
              │        │
              │        └─► Phase 6 (US4, P2)  ── T070/T072 assert self-healing, which is Phase 5
              │
              └─► Phase 7 (polish)  [T073/T074 need Phase 4; T075/T076 generalize T035a]
```

Three edges an earlier draft got wrong, all of them cycles or silent gaps:

- **Phase 2 used to depend on Phase 4.** T024 asserts a fresh proxy lists `describe_tool`, but the registration sat in Phase 4 — so the foundational phase could not go green until the phase built on top of it had landed. Fixed by moving the one-line registration to T019b.
- **Nothing changed `makeDirectModeHandler`'s signature.** T020 updated call sites for a change no task made, and Phase 5 read a captured entry nothing had captured. Fixed by T019a.
- **Phase 6 quietly needed Phase 5.** T070's input-schema residual asserts the stale-schema case self-heals on one retry, and T072 asserts no call succeeds against the wrong schema — both are pre-dispatch-validation behaviour.

- **US1 and US2 are independent of each other** once Phase 2 lands, and can proceed in parallel by different agents: US1 touches `renderDirectTools` and the instructions helper; US2 touches the describe seam and the direct resolver. They meet only at the shared `buildDirectModeTools` return path (T031 / T037) — sequence those two, or land T037 first since it is a one-line append.
- **US3 depends on Phase 2 only** (the handler's captured catalog entry), not on US1 — it can land before deferral is switchable.
- **US4 depends on there being a rebuild to guard** (Phase 2 T019/T025).

## Parallel execution examples

`[P]` means **a different file**, so the marker was removed everywhere it contradicted that — T028/T029/T030 share one new test file, as do T042–T058, T059–T062, and T067/T069. They are still *batchable*, which is not the same thing:

**Phase 1** — T008 (`loader.go`), T009 (`main.go`), T010 and T011 are genuinely different files and keep `[P]`. T005/T006/T007 all touch `config.go` (or test it) and are sequential.

**Phase 3 (US1)** — T028, T029, T030 are three cases in one file: write them as one sitting, then T031 makes all three pass.

**Phase 4 (US2)** — the test tasks all land in `mcp_describe_direct_test.go`: write them as one batch, in the listed order, then the implementation tasks T045–T054 follow sequentially (shared files).

**Phase 7** — T075, T077, T078, T079, T080, T081 touch six different trees and are genuinely parallel.

## Implementation strategy

**MVP = Phase 0 (T004 + T001) + Phase 1 + Phase 2 + Phase 3 + Phase 4**, and it includes **T035a** — the token measurement. Shipping US1 without measuring the reduction would mean shipping the feature without evidence for the one number that justifies it; the earlier draft left that gate in Polish, outside the MVP that claims it. US1 without US2 is not shippable — the spec says so directly ("deferral without the second stage trades tokens for failed calls"), and the catalog-divergence edge case makes an unrecovered deferred listing *strictly worse* than full mode for pending/changed tools. Ship the two P1 stories together.

**Increment 2**: US3. Turns the worst case from an opaque failure into one bounded retry. Independently valuable — it improves direct mode even with deferral off.

**Increment 3**: US4. Required before anyone can flip the setting on a live deployment; until then the setting is start-time only.

**Do not** flip the default in this feature (D8). Any future default flip is its own evidence-gated decision, and the token gates in T076 are the evidence it would need.

## Notes

- Total: **88 tasks**. Six were added by cross-model review: T019a (handler signature + entry capture), T019b (describe_tool registration moved out of Phase 4), T035a (Phase 3's own token gate), T046a (collision describe half), T046b (output_schema test before its implementation), and the T063/T064 merge-and-split around activity emission.
- Commits reference `Related #971` — never `Fixes` or `Closes`.
- Local `internal/server` runs need the CI skip regex; a bare `go test ./internal/server` hangs to a 7-minute timeout panic, and `scripts/test-api-e2e.sh` must never be run against a shared machine (it blanket-kills other cores).
- Every task naming a line number in plan.md should re-locate the symbol before editing: the plan's references were accurate at its merge-base and `internal/server/mcp_routing.go` has moved since.

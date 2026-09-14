# Contract: two-fixture differential oracle (FR-013, SC-001, SC-007)

PRs A–G ship standalone tests on the Phase-1 fixtures (`scope_fixture_test.go`); H1 introduces this harness and re-registers those scenarios by User Story id (research D14). Nothing before H1 depends on it.

## Fixtures (`internal/server/scope_differential_test.go`)

```go
type scopeFixture struct{ proxy *MCPProxyServer; sentinels []string; full bool }
func newScopeFixture(t *testing.T, full bool) *scopeFixture
```
- `full=true`: servers `a`, `b`, `a__b`; each hidden server carries `SENTINEL_<server>` in a tool name, a tool description, a prompt, a cached response payload, a log line and a usage-history entry. `a` has a read-only tool, a write tool, a destructive tool and `ns:erase`/`erase` pair.
- `full=false`: server `a` only, same `a` content.
- Tokens: `aOnly := agentCtx(allowed=["a"], perms=[read])`, `aOnlyFull := agentCtx(allowed=["a"], perms=[read,write,destructive])`, `wildcard := agentCtx(allowed=["*"], …)`, `admin := auth.AdminContext()`.
- Server-edition variant (`-tags server`): same fixtures with `AuthTypeUser` callers where the surface exists.

## Runner

```go
func runScopeScenario(t *testing.T, usID string, fn func(f *scopeFixture, ctx context.Context) any)
```
1. Runs `fn` against both fixtures with `aOnly`.
2. `normalizeScopeResponse`: strips only **nondeterministic** fields (ids, timestamps, request ids), sorts unordered lists. Seeded usage counts are deterministic and are compared.
3. Ranking-dependent fields named by SC-001 (results, `total`, `filter_diagnostics`, scores) are **excluded from the cross-fixture equality and asserted per fixture** against the retrieve-oracle derivation: unchanged `Search` with `Size=documentCount` over the selected corpus, filtered to authorized hits, cut to the ranked window (`spec.md:113`). The baseline is the existing search, never `SearchScoped` itself.
4. Asserts: normalized(A) == normalized(B) byte-for-byte for everything else; no sentinel substring in the raw A response; where the scenario names an admin control, admin(A) ≠ admin(B) (proves the scenario discriminates).
5. Registers `usID` in the coverage table; `TestScopeCoverage_EveryUserStoryScenario` fails if any of US1.1–US1.8, US2.1–US2.6, US3.1–US3.4 has no registration.

## Counting oracle (SC-002)

`startCountingTargetTierUpstream` (exists) — every refusal scenario asserts `calls == 0` after the request; every allowed cell asserts `calls == 1`. The FR-009 tables are generated at the spec's shape (`spec.md:132`): **retrieve 54 cells** = 3 permission sets (`{read}`, `{read,write}`, `{read,write,destructive}`) × 3 target tiers × 3 `call_tool_*` variants × strict-intent on/off, each pre-classified as allowed / insufficient-permission / intent-mismatch; plus a **direct** table (permission set × target tier, through `HandleMessage`) and a **nested** table (permission set × target tier, through the real sandbox runtime with the envelope asserted). Includes the paired-name rows (`erase` approved, `ns:erase` config-denied / unapproved) and `auth.AdminContext()` control rows.

## HTTP matrix (FR-014, `scope_http_matrix_test.go`)

Real tokens minted through `mintAgentToken(name, allowed, perms, pin)`; requests through `mcpAuthMiddleware` over loopback HTTP for every surface `{/mcp, /mcp/all, /mcp/code, /mcp/call, /mcp/p/<slug>, legacy aliases}` × operation `{tools/list, tools/call each built-in, prompts/list, prompts/get}`. `yes` cells run the differential runner; `n/a` cells assert the unregistered-name `-32602`. No `E2E` in the test name (CI skip regex). One environment per fixture (4 s startup).

## Latency (FR-011, `scope_latency_test.go`)

527-tool snapshot via `loadDeferredLargeCorpus`, a frozen 50-prompt set and a frozen 10-page cache entry; 20 warm-up + 200 timed calls per caller for each of `retrieve_tools`, `read_cache`, `prompts/list`, `tools/list`; assert `p95(aOnly) − p95(admin) ≤ 20ms` per operation; skipped under `-race`. **Merge-base bound**: `.github/workflows/scope-latency.yml` (non-race, CI reference runner) checks out merge-base into a second directory, copies head's `scope_latency_test.go` + `testdata/scope_latency/` fixtures over it (test-only, backward compatible), runs both in one job, and fails if administrator p95 regresses by more than max(10%, 5 ms) on any operation **or if either side yields no measurement for any operation** (research D10).

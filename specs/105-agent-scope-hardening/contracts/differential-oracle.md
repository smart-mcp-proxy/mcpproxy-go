# Contract: two-fixture differential oracle (FR-013, SC-001, SC-007)

Every Spec 105 PR adds its scenarios to this harness; H1 completes it and enumerates coverage by User Story id.

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
2. `normalizeScopeResponse`: strips ids, timestamps, request ids, `usage_count` deltas, ranking-dependent fields listed in SC-001 (retrieve-oracle exclusion), sorts unordered lists.
3. Asserts: normalized(A) == normalized(B) byte-for-byte; no sentinel substring in the raw A response; where the scenario names an admin control, admin(A) ≠ admin(B) (proves the scenario discriminates).
4. Registers `usID` in the coverage table; `TestScopeCoverage_EveryUserStoryScenario` fails if any of US1.1–US1.8, US2.1–US2.6, US3.1–US3.4 has no registration.

## Counting oracle (SC-002)

`startCountingTargetTierUpstream` (exists) — every refusal scenario asserts `calls == 0` after the request; every allowed cell asserts `calls == 1`. The FR-009 54-cell table (3 paths × 6 permission sets × 3 targets) is generated, not hand-written.

## HTTP matrix (FR-014, `scope_http_matrix_test.go`)

Real tokens minted through `mintAgentToken(name, allowed, perms, pin)`; requests through `mcpAuthMiddleware` over loopback HTTP for every surface `{/mcp, /mcp/all, /mcp/code, /mcp/call, /mcp/p/<slug>, legacy aliases}` × operation `{tools/list, tools/call each built-in, prompts/list, prompts/get}`. `yes` cells run the differential runner; `n/a` cells assert the unregistered-name `-32602`. No `E2E` in the test name (CI skip regex). One environment per fixture (4 s startup).

## Latency (FR-011, `scope_latency_test.go`)

527-tool snapshot via `loadDeferredLargeCorpus`; 20 warm-up + 200 timed `retrieve_tools` per caller; assert `p95(aOnly) − p95(admin) ≤ 20ms`; skipped under `-race`. Manual merge-base `benchstat` recipe in quickstart.

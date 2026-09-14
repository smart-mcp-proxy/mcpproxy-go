# Quickstart: verifying a Spec 105 PR

Applies to each of A, B, C, D, E, F, G, H0, H1. Run from the feature worktree.

```bash
# 1. Red first: the PR's new/inverted tests must fail on the merge base
git stash-free check: create the tests, run only them, expect FAIL, then implement.
go test -race -count=1 -run 'TestScope|TestRetrieveTools_ScopeOracle|<PR tests>' ./internal/server/...

# 2. Green: touched packages + the server package under the CI skip regex (or it hangs to the 7m panic)
go test -race -count=1 ./internal/cache/... ./internal/index/... ./internal/runtime/... ./internal/logs/... ./internal/oauth/... ./internal/upstream/core/... ./internal/preflight/... ./internal/jsruntime/...
go test -race -count=1 -skip 'E2E|Binary|MCPProtocol|TestInfoEndpoint|TestGracefulShutdownNoPanic|TestSocketInfoEndpoint' ./internal/server/...
go test -tags server -race -count=1 ./internal/serveredition/...      # D, B (user callers)
go build -tags server -o /dev/null ./cmd/mcpproxy                     # never bare: it clobbers ./mcpproxy

# 3. Goldens untouched (H0 is the only exception, limited to two description strings)
git diff --stat -- internal/server/testdata && test -z "$(git diff --stat -- internal/server/testdata)"

# 4. Lint with the CI config (stricter than scripts/run-linter.sh)
/opt/homebrew/bin/golangci-lint run --config .github/.golangci.yml ./...

# 5. API E2E (required before commit)
./scripts/test-api-e2e.sh

# 6. Cross-model review (astra), sequential, stdin closed, files staged repo-relative
mkdir -p .review-tmp && git diff origin/main...HEAD > .review-tmp/pr.diff
until mkdir /private/tmp/claude-501/opencode.lock 2>/dev/null; do sleep 20; done
gtimeout 1800 opencode run --model github-copilot/gpt-6-astra \
  "Review .review-tmp/pr.diff against specs/105-agent-scope-hardening/spec.md FR-<n> and gap-map.md gaps <ids>. Verify each claim against the cited file:line. Answer directly with findings; end with VERDICT: CLEAN or VERDICT: FINDINGS." < /dev/null > .review-tmp/astra-r1.txt
rmdir /private/tmp/claude-501/opencode.lock
grep -c "permission requested" .review-tmp/astra-r1.txt   # must be 0
grep "VERDICT:" .review-tmp/astra-r1.txt                   # must exist — exit 0 without it is NOT clean
```

Round cap: 10 fix→re-review rounds per PR; verify each finding against the code before fixing.

## FR-011 manual merge-base measurement (C, once)

```bash
go test -run TestScopeLatency -bench . -count 6 ./internal/server/ > /tmp/new.txt
git stash-free: check out origin/main in a second worktree and repeat into /tmp/old.txt
benchstat /tmp/old.txt /tmp/new.txt     # scoped p95 within 20ms of admin; record numbers in the PR body
```

## Live check (A, B, D, E — one real daemon each)

```bash
./mcpproxy serve --listen 127.0.0.1:18105 --data-dir /tmp/mcpproxy-105 --config /tmp/mcpproxy-105/mcp_config.json --log-level=debug &
./mcpproxy token create --name a-only --servers a --permissions read -o json     # then curl /mcp with X-API-Key
```
Kill any running core first (BBolt lock, exit 3). Use a high port and scratch `--data-dir` **and** `--config`.

## PR body checklist

- Gap ids closed (from gap-map §1) and the tests that pin each.
- Tests inverted (name → what it asserted before → what it asserts now).
- SC-005 admin controls kept (test names).
- Spec text amendments (A only) and research.md decision ids applied.
- Astra rounds run, model actually used, final VERDICT line quoted.
- Links to docs use docs.mcpproxy.app URLs, never repo paths.

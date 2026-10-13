# T035 — Cross-cutting gates on branch `090-tray-glance-v2`

Run after T001–T034 landed. Compare against
[baseline.md](baseline.md), which recorded **no** pre-existing failures — so a
red gate here would have been ours.

- Date: 2026-07-31
- Result: **all four gates GREEN**

## 1. `./scripts/run-linter.sh`

```text
Running golangci-lint...
0 issues.
```

## 2. `go test ./internal/...`

Exit code 0. 59 packages `ok`, 0 `FAIL`, 2 with no test files
(`internal/testutil`, `internal/upstream/cli`).

```text
ok  	github.com/smart-mcp-proxy/mcpproxy-go/internal/httpapi	(cached)
ok  	github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime	(cached)
ok  	github.com/smart-mcp-proxy/mcpproxy-go/internal/storage	(cached)
ok  	github.com/smart-mcp-proxy/mcpproxy-go/internal/server	(cached)
…
ok  	github.com/smart-mcp-proxy/mcpproxy-go/internal/upstream/types	2.570s
```

(`(cached)` entries are content-keyed hits from the `-count=1` runs the Go track
made on this same tree; a cache hit is a pass on identical inputs.)

## 3. `cd native/macos/MCPProxy && swift test`

```text
Test Suite 'MCPProxyPackageTests.xctest' passed
	 Executed 609 tests, with 0 failures (0 unexpected) in 37.795 (37.828) seconds
Test Suite 'All tests' passed
	 Executed 609 tests, with 0 failures (0 unexpected) in 37.795 (37.834) seconds
✔ Test run with 0 tests in 0 suites passed after 0.001 seconds.
```

Baseline was 502 tests; spec 090 added 107.

## 4. `make swagger-verify`

```text
✅ OpenAPI 3.1 spec generated: oas/swagger.yaml and oas/docs.go
🔎 Verifying OpenAPI artifacts are committed...
✅ OpenAPI artifacts are up to date.
```

## Quickstart validation

Every command in [quickstart.md](../quickstart.md) was checked against the tree:

| Command | Status |
|---------|--------|
| `go test ./internal/httpapi/... ./internal/runtime/... ./internal/storage/... ./internal/server/...` | valid, green |
| `make swagger && make swagger-verify` | valid, green |
| `cd native/macos/MCPProxy && swift test` | valid, green |
| `scripts/build-swift-app.sh` | file exists |
| `./mcpproxy serve --listen … --data-dir … --config …` | all three flags exist (`--listen` on `serve`; `--config`/`--data-dir` are persistent root flags) — **added the missing `make build` step to quickstart**, since `./mcpproxy` does not exist until it is built |
| `go test ./internal/httpapi -run TestActivity` | valid; matches `TestActivityList_ExcludePayloads` (the whitelist test) among others |
| `specs/090-tray-glance-v2/fixtures/activity-replay.jsonl` | present (880 KB), consumed by GlanceFixtureReplayTests |
| Named Swift suites (GlanceSelection/Grouping/Presence/FixtureReplay, MenuOpenNetworkTests) | all present |

## Still open

`manual-protocol.md` has NOT been run — it needs a built app, a seeded dev core
and screen-recording permission. It remains a release-blocking manual step.

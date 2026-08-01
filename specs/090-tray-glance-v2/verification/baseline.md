# T001 — Pre-change baseline on branch `090-tray-glance-v2`

Recorded before any spec-090 code change, so a later red test can be attributed
to this feature rather than to something that was already broken.

## Swift tray — `cd native/macos/MCPProxy && swift test`

- Date: 2026-07-31
- Result: **GREEN**

```text
Test Suite 'MCPProxyPackageTests.xctest' passed
	 Executed 502 tests, with 0 failures (0 unexpected) in 34.353 (34.375) seconds
Test Suite 'All tests' passed
	 Executed 502 tests, with 0 failures (0 unexpected) in 34.353 (34.381) seconds
✔ Test run with 0 tests in 0 suites passed (swift-testing side, no swift-testing tests exist yet)
```

**Pre-existing Swift failures: none.** Any Swift failure from here on is ours.

## Go core — `go test ./internal/httpapi/... ./internal/runtime/... ./internal/storage/... ./internal/server/...`

- Date: 2026-07-31
- Result: **GREEN**

```text
ok  	github.com/smart-mcp-proxy/mcpproxy-go/internal/httpapi	2.093s
ok  	github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime	107.527s
ok  	github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime/configsvc	1.893s
ok  	github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime/stateview	0.399s
ok  	github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime/supervisor	3.816s
ok  	github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime/supervisor/actor	2.063s
ok  	github.com/smart-mcp-proxy/mcpproxy-go/internal/storage	22.331s
ok  	github.com/smart-mcp-proxy/mcpproxy-go/internal/server	535.956s
ok  	github.com/smart-mcp-proxy/mcpproxy-go/internal/server/tokens	3.618s
```

**Pre-existing Go failures: none.** Any Go failure from here on is ours.

Note: `internal/server` takes ~9 minutes end to end, so subsequent task
verification runs use `-run` filters on the touched packages and the full
four-package sweep is reserved for phase boundaries.

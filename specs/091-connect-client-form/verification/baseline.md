# T001 — Green baseline on branch `091-connect-client-form`

Recorded before any spec-091 change. Go track only; the Swift (`swift test`)
half of T001 is recorded by the Swift track in the section reserved below.

## Go — `go test ./internal/connect/... ./internal/httpapi/... -count=1`

Toolchain: `go1.25.5 darwin/arm64`. Baseline commit: `21049e7e9`.

```
ok  	github.com/smart-mcp-proxy/mcpproxy-go/internal/connect	0.322s
ok  	github.com/smart-mcp-proxy/mcpproxy-go/internal/httpapi	0.664s
```

**Pre-existing failures: none.** Both packages are fully green, so any red
observed during Phase 2 is attributable to the spec-091 change under test.

## SC-003 — already covered by the unchanged undo suite

SC-003 ("the undo round-trip restores the client's config to its exact
pre-connect content for the existing-file case, and removes the created file for
the new-file case") requires **no new Go test**: `internal/connect/undo_test.go`
already asserts exactly both halves, and spec 091 does not modify `undo.go`.

Confirmed green at baseline:

```
$ go test ./internal/connect/ -run 'TestUndo_RestoresByteIdenticalPreConnectFile|TestUndo_NoPriorFile_RemovesCreatedFile' -count=1 -v
=== RUN   TestUndo_RestoresByteIdenticalPreConnectFile
--- PASS: TestUndo_RestoresByteIdenticalPreConnectFile (0.00s)
=== RUN   TestUndo_NoPriorFile_RemovesCreatedFile
--- PASS: TestUndo_NoPriorFile_RemovesCreatedFile (0.00s)
PASS
ok  	github.com/smart-mcp-proxy/mcpproxy-go/internal/connect	0.178s
```

- `TestUndo_RestoresByteIdenticalPreConnectFile` → byte-exact restore of the
  pre-connect file (existing-file case).
- `TestUndo_NoPriorFile_RemovesCreatedFile` → removal of the file the connect
  created (new-file case).

These two must stay green through Phase 2 (they are part of the
`./internal/connect/...` run in T011). Manual protocol checks 9 / 9b re-verify
the same round-trip live against a running core; that live re-verification is
the only SC-003 work remaining.

## Swift — `cd native/macos/MCPProxy && swift test`

Toolchain: `Apple Swift version 6.3.3 (swiftlang-6.3.3.1.3)`, target
`arm64-apple-macosx26.0`, package tools-version 5.9. Baseline commit `075adaed3`
(same tree as the Go baseline above — no spec-091 source change yet).

```
Test Suite 'MCPProxyPackageTests.xctest' passed at 2026-07-31 18:07:08.149.
	 Executed 502 tests, with 0 failures (0 unexpected) in 34.630 (34.660) seconds
Test Suite 'All tests' passed at 2026-07-31 18:07:08.149.
	 Executed 502 tests, with 0 failures (0 unexpected) in 34.630 (34.666) seconds
✔ Test run with 0 tests in 0 suites passed after 0.001 seconds.
```

**Pre-existing failures: none.** All 502 XCTest cases pass (the swift-testing
runner reports 0 suites — the package has no swift-testing tests). Any red seen
in the Swift track from here is attributable to the spec-091 change under test.

The Swift tasks (T012+) synthesize API JSON through a stub `URLProtocol` and an
injected data-source seam, so none of them requires a running core; the Go track
can land in parallel without affecting this baseline.

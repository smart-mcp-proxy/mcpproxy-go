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

_Recorded by the Swift track of T001._

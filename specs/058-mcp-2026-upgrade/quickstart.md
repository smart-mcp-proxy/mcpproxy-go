# Quickstart: MCP 2026-07-28 Upgrade

**Feature**: 058-mcp-2026-upgrade · **Phase**: 1 · **Date**: 2026-09-03

How to get a working environment, reproduce the two known failures, and run the gates. Every command here was run during Phase 0 research.

## 1. Bump the library in a scratch worktree

Never bump in place — the pin change touches test files across six packages.

```bash
git worktree add ../mcp2026 -b 058-bump origin/main
```

```bash
cd ../mcp2026 && go get github.com/mark3labs/mcp-go@v1.0.0 && go mod tidy
```

`go mod tidy` pulls in no new module. Production code compiles as-is on both editions:

```bash
go build ./... && go build -tags server -o /dev/null ./cmd/mcpproxy && go vet -tags server ./internal/serveredition/...
```

## 2. Fix the one compile break

`go vet ./...` reports exactly one undefined symbol, in test code only. `server.NewTestStreamableHTTPServer` moved to a new package with an identical signature.

```bash
grep -rln 'NewTestStreamableHTTPServer' --include='*_test.go' .
```

Swap the calls to `servertest.NewTestStreamableHTTPServer` and add `"github.com/mark3labs/mcp-go/server/servertest"` to the imports. Six files, 32 call sites. Drop the now-unused `mcpserver` import where it becomes orphaned.

## 3. Reproduce the two known failures

```bash
go test ./internal/server -run 'TestProfile_' -count=1
```

Both fail with `set_profile requires an active MCP session; no session id is bound to this request`. The cause is not the profile code: the v1.0.0 client probes `server/discover` first, mcpproxy advertises every version by default, so the client goes modern and no session id is minted.

Confirm the failure set is *only* those two:

```bash
go test ./internal/server -count=1 -skip 'E2E|Binary|MCPProtocol|Docker|OAuth' -timeout 20m
```

Expect `--- FAIL` twice and nothing else. Takes about three minutes.

## 4. Make them pass without touching profile logic

Either knob works, and knowing that is the point — it proves the failure is about era negotiation, not about profiles:

- **Server-side (this is the FR-028 pin)**: add `server.WithStreamableHTTPProtocolVersions(mcp.LegacyProtocolVersions()...)` to the transport. Apply it at all five sites in `internal/server/server.go` via one shared helper, or it will drift.
- **Client-side (for era-pinned tests)**: pass `client.WithProtocolVersion(mcp.LATEST_LEGACY_PROTOCOL_VERSION)` in the test harness.

## 5. Write tests that cannot pass vacuously

Under the FR-028 pin an unpinned client negotiates *down*, so an assertion like `c.ProtocolVersion() == "2026-07-28"` passes while proving nothing. Pin the era explicitly on each side and run every acceptance test under both:

```go
forEachEra(t, func(t *testing.T, era Era) { /* ... */ })
```

**Naming trap**: `release-qa-gate.yml` and `e2e-tests.yml` skip on the bare substring `MCP`. A test named `TestMCPSomething` runs in unit CI and is silently skipped by the race suite. Do not put `MCP` in a new test name.

## 6. Gates before merging the bump

```bash
go test -race ./internal/... && go test -tags server ./internal/serveredition/... -race
```

```bash
./scripts/test-api-e2e.sh
```

```bash
/opt/homebrew/bin/golangci-lint run --config .github/.golangci.yml ./...
```

The frozen tool-surface goldens must be byte-identical. They survive the bump: `mcp.Tool`, `ToolAnnotation`, `ToolArgumentsSchema` and their marshallers are unchanged between v0.57.0 and v1.0.0.

## 7. Inspect era behaviour by hand

Kill any running core first — it locks the database.

```bash
./mcpproxy serve --listen 127.0.0.1:18080 --data-dir /tmp/mcp058 --config /tmp/mcp058/cfg.json --log-level=debug
```

A modern request is one carrying `_meta` with a `protocolVersion` of `2026-07-28`; a legacy one completes an `initialize` handshake first. Useful things to observe: the session id is empty for every modern request, `initialize` and `ping` return method-not-found, and `GET`/`DELETE` return 405 only when the request carries a modern protocol header.

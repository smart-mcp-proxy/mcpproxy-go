# Quickstart — Tray Glance v2 (Spec 090)

## Build & test

```bash
# Go core: unit tests for the three backend deltas
go test ./internal/httpapi/... ./internal/runtime/... ./internal/storage/... ./internal/server/...

# Regenerate + verify OpenAPI after the exclude_payloads doc change
make swagger && make swagger-verify

# Swift tray: pure pipeline + fixture replay + menu tests
cd native/macos/MCPProxy && swift test

# Go binary (needed before `./mcpproxy serve` below)
make build

# Full app build (DMG-less dev build)
scripts/build-swift-app.sh
```

## Run against a dev core (isolated — never the personal instance)

```bash
./mcpproxy serve --listen 127.0.0.1:18090 \
  --data-dir /tmp/mcpproxy-090-data --config /tmp/mcpproxy-090-config.json
```

Then launch the built app and point it at the dev core (socket path comes from
the data dir). Generate traffic through the MCP endpoint to populate the
glance; the sanitized fixture documents realistic shapes.

## Key invariants to keep green

- `swift test` — GlanceSelection/Grouping/Presence/FixtureReplay suites.
- Menu-open zero-request test (spec 048 invariant, FR-022).
- `go test ./internal/httpapi -run TestActivity` — projection whitelist.
- Manual protocol: `specs/090-tray-glance-v2/verification/manual-protocol.md`.

# Quickstart — Native Connect Client Form (Spec 091)

## Build & test

```bash
# Go: connect package + endpoint tests (token, sanitization, conflict)
go test ./internal/connect/... ./internal/httpapi/... -count=1

# Swagger after the two contract deltas
make swagger && make swagger-verify

# Swift: model/state-machine + decoding tests
cd native/macos/MCPProxy && swift test

# Binaries
make build
scripts/build-swift-app.sh
```

## Manual run (isolated dev core — never the personal instance)

```bash
./mcpproxy serve --listen 127.0.0.1:18091 \
  --data-dir /tmp/mcpproxy-091-data --config /tmp/mcpproxy-091-config.json
```

Seed scratch client configs per
`specs/091-connect-client-form/verification/manual-protocol.md` (create-capable
absent, OpenCode absent, malformed, connected, unconnected) and run its checks.

## Invariants to keep green

- No raw secret ever appears in a preview response (`internal/connect` tests).
- 409 + zero write on token drift; absent token = legacy behavior.
- Connect control cannot exist before a preview has rendered (model tests).
- Tray performs zero client-config file reads; list causes zero content reads.

# Quickstart — Native Connect Client Form (Spec 091)

## Build & test

```bash
# Go: connect package + endpoint tests (token, summary secrecy, conflict)
go test ./internal/connect/... ./internal/httpapi/... -count=1

# SC-006: socket → gated connect-write route end-to-end
go test ./internal/server -run ConnectSocket -count=1

# Swagger after the two contract deltas
make swagger && make swagger-verify

# Swift: model/state-machine + decoding tests
cd native/macos/MCPProxy && swift test

# Binaries
make build
scripts/build-swift-app.sh
```

## Manual run (isolated dev core — never the personal instance)

`--config` must point at a file that already EXISTS — the core exits with
`failed to read config file: … no such file or directory` rather than creating
one. Seed it first:

```bash
printf '{"listen":"127.0.0.1:18091","data_dir":"/tmp/mcpproxy-091-data","mcpServers":[]}\n' \
  > /tmp/mcpproxy-091-config.json

./mcpproxy serve --listen 127.0.0.1:18091 \
  --data-dir /tmp/mcpproxy-091-data --config /tmp/mcpproxy-091-config.json
```

Smoke-check the deltas over the core's admin socket (a preview never writes and
never takes a backup):

```bash
curl -s --unix-socket /tmp/mcpproxy-091-data/mcpproxy.sock \
  http://localhost/api/v1/connect/cursor/preview
```

Expect `precondition_token` (64 hex chars), `existing_entry_summary` when
`entry_exists` is true, and `connect_refusal` for OpenCode with no config.

Seed scratch client configs per
`specs/091-connect-client-form/verification/manual-protocol.md` (create-capable
absent, OpenCode absent, malformed, connected, unconnected) and run its checks.

## Invariants to keep green

- No raw secret ever appears in a preview response (`internal/connect` tests
  incl. rotated keys, bearer/env secrets, `?apikey=`, `user:pass@`).
- 409 `precondition_failed` + zero write on token drift (file OR adopted-entry
  OR pending-entry drift); absent token = legacy behavior.
- Connect control cannot exist before a refusal-free preview has rendered
  (model tests); replace flows send force+token, never force alone.
- Tray performs zero client-config file reads; list causes zero content reads;
  mutating requests are strict-socket (no silent TCP fallback).

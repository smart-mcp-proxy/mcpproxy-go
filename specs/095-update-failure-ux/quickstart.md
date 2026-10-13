# Quickstart — verifying spec 095 locally

## Automated

```bash
# Go: handler + counters + gates
go test ./internal/httpapi/... ./internal/telemetry/... ./internal/diagnostics/... -run 'UpdateFailure|Diagnostics' -v

# Swift: classifier, dialog controller, retry ordering, APIClient
cd native/macos/MCPProxy && swift test   # judge by the final summary line

# Contract artifacts in sync
make swagger && git diff --exit-code oas/

# Lint (CI config)
/opt/homebrew/bin/golangci-lint run --config .github/.golangci.yml ./...
```

## Manual REST smoke (isolated core — never against the running personal instance)

```bash
go build -o /tmp/mcpproxy-095 ./cmd/mcpproxy
/tmp/mcpproxy-095 serve --listen 127.0.0.1:18095 --data-dir /tmp/mcpproxy-095-data --config /tmp/mcpproxy-095-data/config.json &
APIKEY=$(jq -r .api_key /tmp/mcpproxy-095-data/config.json)

# record one download failure
curl -s -o /dev/null -w '%{http_code}\n' -X POST -H "X-API-Key: $APIKEY" \
  -H 'Content-Type: application/json' -d '{"stage":"download"}' \
  http://127.0.0.1:18095/api/v1/telemetry/update-failure           # → 204

# rejects
curl -s -X POST -H "X-API-Key: $APIKEY" -d '{"stage":"nope"}' ...  # → 400
curl -s -X POST -H "X-API-Key: $APIKEY" -d '{"stage":"install","x":1}' ...  # → 400

# visible in payload preview (dev builds don't transmit, but preview shows assembly)
curl -s -H "X-API-Key: $APIKEY" http://127.0.0.1:18095/api/v1/telemetry/payload | jq '.data.diagnostics.error_code_counts_24h'
```

Note: a dev (non-semver) build reports telemetry-inactive → the endpoint 204-no-ops and the counter stays absent. To exercise the active path locally, build with a semver ldflag version (see manual protocol) or rely on the Go handler tests, which pin the gate.

## Live dialog verification (Sparkle test rig)

Follow `verification/manual-protocol.md`: local appcast offering a higher version with an enclosure URL pointing at an unreachable port → tray "Check for Updates…" → the new recovery dialog (not stock "Update Error!") appears; Try Again re-checks after teardown; Download from Website opens the expected URL; screenshots via `mcpproxy-ui-test`.

# Quickstart — verifying Spec 121 locally

```bash
# A. registry
scripts/stamp-server-json.sh v0.71.1 > /tmp/server.json && mcp-publisher validate /tmp/server.json
scripts/registry-preflight.sh v0.71.1        # needs docker; fails until a labelled image exists
# B. scoop manifest from a real release
gh release download v0.71.1 -p checksums.txt -D /tmp/rel && scripts/gen-scoop-manifest.sh v0.71.1 /tmp/rel/checksums.txt | jq .
# C. prompt drift + llms check
scripts/sync-agent-prompt.sh --check && (cd website && npm ci && npm run build)
# D. telemetry
go test ./internal/telemetry/... ./internal/updatecheck/... -run 'InstallChannel|Scoop' -race
```
